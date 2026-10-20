package cluster

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (service *OnboardingService) GetEventResource(ctx context.Context, q EventResourceQuery) (EventResourceSnapshot, error) {
	record, err := service.repository.GetRecord(ctx, q.ClusterID)
	if err != nil {
		return EventResourceSnapshot{}, err
	}
	result := EventResourceSnapshot{Status: "unverified", CheckedAt: time.Now().UTC(), Owners: []EventResourceReference{}, Containers: []EventContainerEvidence{}}
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		result.Detail = "无法解密集群凭据，未核验当前状态"
		return result, nil
	}
	connector, ok := service.connector.(EventResourceConnector)
	if !ok {
		result.Detail = "当前连接器不支持实时核验"
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	snapshot, err := connector.EventResource(ctx, parsed, q)
	if err != nil {
		result.Detail = "实时读取失败（" + ErrorCode(err) + "），不能判断资源是否恢复"
		return result, nil
	}
	return snapshot, nil
}
func (connector *KubernetesConnector) EventResource(ctx context.Context, parsed ParsedKubeconfig, q EventResourceQuery) (EventResourceSnapshot, error) {
	r := EventResourceSnapshot{Status: "unverified", CheckedAt: time.Now().UTC(), Owners: []EventResourceReference{}, Containers: []EventContainerEvidence{}}
	if q.UID == "" {
		r.Detail = "历史事件缺少资源 UID，无法确认当前同名对象是否为原对象"
		return r, nil
	}
	client, err := connector.clientset(parsed)
	if err != nil {
		return r, err
	}
	var object metav1.Object
	var pod *corev1.Pod
	healthy := false
	switch q.Kind {
	case "Pod":
		pod, err = client.CoreV1().Pods(q.Namespace).Get(ctx, q.Name, metav1.GetOptions{})
		if err == nil {
			object = pod
			healthy = podReady(*pod) || pod.Status.Phase == corev1.PodSucceeded
		}
	case "Node":
		var node *corev1.Node
		node, err = client.CoreV1().Nodes().Get(ctx, q.Name, metav1.GetOptions{})
		if err == nil {
			object = node
			healthy = nodeReady(*node)
		}
	case "Deployment":
		item, e := client.AppsV1().Deployments(q.Namespace).Get(ctx, q.Name, metav1.GetOptions{})
		err = e
		if e == nil {
			object = item
			healthy = deploymentWorkload(*item).Status == "healthy"
		}
	case "StatefulSet":
		item, e := client.AppsV1().StatefulSets(q.Namespace).Get(ctx, q.Name, metav1.GetOptions{})
		err = e
		if e == nil {
			object = item
			healthy = statefulSetWorkload(*item).Status == "healthy"
		}
	case "DaemonSet":
		item, e := client.AppsV1().DaemonSets(q.Namespace).Get(ctx, q.Name, metav1.GetOptions{})
		err = e
		if e == nil {
			object = item
			healthy = daemonSetWorkload(*item).Status == "healthy"
		}
	case "ReplicaSet":
		item, e := client.AppsV1().ReplicaSets(q.Namespace).Get(ctx, q.Name, metav1.GetOptions{})
		err = e
		if e == nil {
			object = item
			desired := int32(1)
			if item.Spec.Replicas != nil {
				desired = *item.Spec.Replicas
			}
			healthy = item.Status.ObservedGeneration >= item.Generation && item.Status.ReadyReplicas >= desired
		}
	default:
		r.Detail = "该资源类型暂不支持实时核验，仍可查看持久化事件证据"
		return r, nil
	}
	if apierrors.IsNotFound(err) {
		r.Status = "deleted"
		r.Detail = "原资源已不存在，历史事件仍保留"
		return r, nil
	}
	if err != nil {
		return r, mapConnectionError(err)
	}
	r.UID = string(object.GetUID())
	if r.UID != q.UID {
		r.Status = "replaced"
		r.Detail = "同名资源 UID 已变化，不能用新资源状态代表原事件对象"
		return r, nil
	}
	r.Status = "abnormal"
	r.Detail = "原资源当前未就绪；历史事件与当前状态不一定具有相同原因"
	if healthy {
		r.Status = "normal"
		r.Detail = "原资源当前正常；保留历史异常证据，不代表历史问题未发生"
	}
	if owner := metav1.GetControllerOf(object); owner != nil {
		r.Owners = append(r.Owners, EventResourceReference{Kind: owner.Kind, Name: owner.Name, UID: string(owner.UID)})
		if owner.Kind == "ReplicaSet" {
			rs, e := client.AppsV1().ReplicaSets(q.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
			if e == nil && rs.UID == owner.UID {
				if parent := metav1.GetControllerOf(rs); parent != nil {
					r.Owners = append(r.Owners, EventResourceReference{Kind: parent.Kind, Name: parent.Name, UID: string(parent.UID)})
				}
			} else {
				r.Detail += "；ReplicaSet 所属链未能核实"
			}
		}
	}
	if pod != nil {
		r.NodeName = pod.Spec.NodeName
		if metav1.GetControllerOf(pod) == nil {
			r.Detail += "；独立 Pod，无副本控制器"
		}
		for _, c := range pod.Status.ContainerStatuses {
			evidence := EventContainerEvidence{Name: c.Name, Restarts: c.RestartCount, HasPreviousLogs: c.LastTerminationState.Terminated != nil}
			if previous := c.LastTerminationState.Terminated; previous != nil {
				code := previous.ExitCode
				evidence.PreviousExitCode = &code
				evidence.PreviousReason = previous.Reason
			}
			switch {
			case c.State.Waiting != nil:
				evidence.State = "waiting"
				evidence.Reason = c.State.Waiting.Reason
			case c.State.Terminated != nil:
				evidence.State = "terminated"
				evidence.Reason = c.State.Terminated.Reason
				code := c.State.Terminated.ExitCode
				evidence.ExitCode = &code
				evidence.HasLogs = true
			case c.State.Running != nil:
				evidence.State = "running"
				evidence.HasLogs = true
			}
			r.Containers = append(r.Containers, evidence)
		}
	}
	return r, nil
}
