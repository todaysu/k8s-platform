package cluster

import (
	"context"
	appsv1 "k8s.io/api/apps/v1"
)

// IndexResourceConnector avoids building per-workload Pod detail arrays just to discard them.
type IndexResourceConnector interface {
	IndexResources(context.Context, ParsedKubeconfig) ([]ClusterWorkload, []ClusterPod, error)
}

func (connector *KubernetesConnector) IndexResources(ctx context.Context, parsed ParsedKubeconfig) ([]ClusterWorkload, []ClusterPod, error) {
	client, err := connector.listClientset(parsed)
	if err != nil {
		return nil, nil, err
	}
	workloads := []ClusterWorkload{}
	if err := visitDeployments(ctx, client, "", func(item *appsv1.Deployment) error {
		workloads = append(workloads, deploymentWorkload(*item).ClusterWorkload)
		return nil
	}); err != nil {
		return nil, nil, err
	}
	if err := visitStatefulSets(ctx, client, "", func(item *appsv1.StatefulSet) error {
		workloads = append(workloads, statefulSetWorkload(*item).ClusterWorkload)
		return nil
	}); err != nil {
		return nil, nil, err
	}
	if err := visitDaemonSets(ctx, client, "", func(item *appsv1.DaemonSet) error {
		workloads = append(workloads, daemonSetWorkload(*item).ClusterWorkload)
		return nil
	}); err != nil {
		return nil, nil, err
	}
	pods, err := connector.Pods(ctx, parsed)
	return workloads, pods, err
}
