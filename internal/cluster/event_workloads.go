package cluster

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sort"
	"time"
)

// Resolve by controller references and UIDs, never by a Pod name prefix.
type eventWorkloadConnector interface {
	EventWorkloadOwners(context.Context, ParsedKubeconfig) (map[string]EventResourceReference, error)
}

func (connector *KubernetesConnector) EventWorkloadOwners(ctx context.Context, parsed ParsedKubeconfig) (map[string]EventResourceReference, error) {
	client, err := connector.clientset(parsed)
	if err != nil {
		return nil, err
	}
	pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	result := map[string]EventResourceReference{}
	// Failure to list ReplicaSets still allows grouping by the verified direct controller.
	sets, _ := client.AppsV1().ReplicaSets("").List(ctx, metav1.ListOptions{})
	parents := map[string]EventResourceReference{}
	if sets != nil {
		for _, rs := range sets.Items {
			if owner := metav1.GetControllerOf(&rs); owner != nil && owner.Kind == "Deployment" {
				parents[string(rs.UID)] = EventResourceReference{Kind: owner.Kind, Name: owner.Name, UID: string(owner.UID)}
			}
		}
	}
	for _, pod := range pods.Items {
		owner := metav1.GetControllerOf(&pod)
		if owner == nil {
			result[string(pod.UID)] = EventResourceReference{}
			continue
		}
		ref := EventResourceReference{Kind: owner.Kind, Name: owner.Name, UID: string(owner.UID)}
		if parent, ok := parents[ref.UID]; ok && ref.Kind == "ReplicaSet" {
			ref = parent
		}
		result[string(pod.UID)] = ref
	}
	return result, nil
}
func groupEventWorkloads(objects map[string]FleetEventObject, owners map[string]EventResourceReference) map[string]FleetEventObject {
	grouped := map[string]FleetEventObject{}
	for _, object := range objects {
		if object.Kind == "Pod" {
			if owner, found := owners[object.UID]; !found {
				object.GroupingNote = "历史 Pod 归属未核验，暂时单列"
			} else if owner.Kind == "" {
				object.GroupingNote = "独立 Pod，无副本控制器"
			}
		}
		original := object
		if owner, ok := owners[object.UID]; ok && object.Kind == "Pod" && object.UID != "" && owner.UID != "" {
			object.Kind, object.Name, object.UID = owner.Kind, owner.Name, owner.UID
		}
		key := eventObjectKey(ClusterEvent{ClusterID: object.ClusterID, RegardingKind: object.Kind, RegardingNamespace: object.Namespace, RegardingUID: object.UID, RegardingName: object.Name})
		aggregate, exists := grouped[key]
		if !exists {
			aggregate = object
			aggregate.EventCount = 0
			aggregate.OccurrenceCount = 0
			aggregate.Members = nil
		}
		aggregate.EventCount += original.EventCount
		aggregate.OccurrenceCount += original.OccurrenceCount
		if original.LastObservedAt.After(aggregate.LastObservedAt) {
			aggregate.LastObservedAt = original.LastObservedAt
			aggregate.Message = original.Message
		}
		if original.Kind != object.Kind || original.UID != object.UID {
			aggregate.Members = append(aggregate.Members, original)
		}
		grouped[key] = aggregate
	}
	for key, object := range grouped {
		sort.Slice(object.Members, func(i, j int) bool { return object.Members[i].UID < object.Members[j].UID })
		grouped[key] = object
	}
	return grouped
}
func (service *OnboardingService) eventWorkloadGroups(ctx context.Context, record ClusterRecord, objects map[string]FleetEventObject) map[string]FleetEventObject {
	connector, ok := service.connector.(eventWorkloadConnector)
	if !ok {
		return objects
	}
	hasPod := false
	for _, object := range objects {
		if object.Kind == "Pod" {
			hasPod = true
			break
		}
	}
	if !hasPod {
		return objects
	}
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return objects
	}
	child, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	owners, err := connector.EventWorkloadOwners(child, parsed)
	if err != nil {
		return objects
	}
	return groupEventWorkloads(objects, owners)
}
