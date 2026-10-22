package setup

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	pxcGroupVersion = "pxc.percona.com/v1"
	pxcResource     = "perconaxtradbclusters"
)

type PreflightNode struct {
	Name        string   `json:"name"`
	Arch        string   `json:"arch"`
	OS          string   `json:"os"`
	CPUMillis   int64    `json:"cpu_millis"`
	MemoryBytes int64    `json:"memory_bytes"`
	InternalIPs []string `json:"internal_ips"`
	Schedulable bool     `json:"schedulable"`
	Taints      []string `json:"taints,omitempty"`
}

type PreflightStorageClass struct {
	Name                 string `json:"name"`
	Default              bool   `json:"default"`
	Provisioner          string `json:"provisioner"`
	AllowVolumeExpansion bool   `json:"allow_volume_expansion"`
	VolumeBindingMode    string `json:"volume_binding_mode"`
}

type PreflightIngressClass struct {
	Name       string `json:"name"`
	Default    bool   `json:"default"`
	Controller string `json:"controller"`
}

type PreflightResult struct {
	Namespace           string                  `json:"namespace"`
	Nodes               []PreflightNode         `json:"nodes"`
	StorageClasses      []PreflightStorageClass `json:"storage_classes"`
	IngressClasses      []PreflightIngressClass `json:"ingress_classes"`
	PXCAvailable        bool                    `json:"pxc_available"`
	RuntimeSecretExists bool                    `json:"runtime_secret_exists"`
	Warnings            []string                `json:"warnings"`
	Errors              []string                `json:"errors"`
}

// preflight inspects the cluster before any resource is created. It never
// mutates the cluster; the wizard uses the result to block unsafe installs.
func (server *Server) preflight(ctx context.Context) PreflightResult {
	result := PreflightResult{Namespace: server.config.Namespace}
	if result.Namespace == "" {
		result.Namespace = "ftc-platform"
	}
	if server.client == nil {
		result.Errors = append(result.Errors, "初始化控制器未连接 Kubernetes API。")
		return result
	}

	nodes, err := server.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("读取节点失败：%v", err))
	} else {
		schedulable := 0
		for _, node := range nodes.Items {
			item := PreflightNode{
				Name:        node.Name,
				Arch:        nodeArchitecture(&node),
				OS:          node.Labels["kubernetes.io/os"],
				CPUMillis:   node.Status.Allocatable.Cpu().MilliValue(),
				MemoryBytes: node.Status.Allocatable.Memory().Value(),
				Schedulable: node.Spec.Unschedulable == false && !hasNoScheduleTaint(node.Spec.Taints),
				Taints:      taintList(node.Spec.Taints),
			}
			for _, address := range node.Status.Addresses {
				if address.Type == corev1.NodeInternalIP {
					item.InternalIPs = append(item.InternalIPs, address.Address)
				}
			}
			sort.Strings(item.InternalIPs)
			if item.Schedulable {
				schedulable++
			}
			result.Nodes = append(result.Nodes, item)
		}
		sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].Name < result.Nodes[j].Name })
		if len(result.Nodes) < 3 {
			result.Errors = append(result.Errors, "PXC 3 节点高可用至少需要 3 个节点；当前集群节点不足。")
		} else if schedulable < 3 {
			result.Errors = append(result.Errors, "可调度节点少于 3 个；请先解除节点污点或增加节点。")
		}
	}

	storageClasses, err := server.client.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("读取 StorageClass 失败：%v", err))
	} else {
		for _, class := range storageClasses.Items {
			item := PreflightStorageClass{
				Name:                 class.Name,
				Default:              class.Annotations["storageclass.kubernetes.io/is-default-class"] == "true",
				Provisioner:          class.Provisioner,
				AllowVolumeExpansion: class.AllowVolumeExpansion != nil && *class.AllowVolumeExpansion,
				VolumeBindingMode:    bindingMode(class),
			}
			result.StorageClasses = append(result.StorageClasses, item)
		}
		sort.Slice(result.StorageClasses, func(i, j int) bool { return result.StorageClasses[i].Name < result.StorageClasses[j].Name })
		if len(result.StorageClasses) == 0 {
			result.Warnings = append(result.Warnings, "未发现 StorageClass；向导会创建本地 PV 和 no-provisioner SC，不需要外部 provisioner。")
		}
	}

	ingressClasses, err := server.client.NetworkingV1().IngressClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("读取 IngressClass 失败：%v", err))
	} else {
		for _, class := range ingressClasses.Items {
			result.IngressClasses = append(result.IngressClasses, PreflightIngressClass{
				Name:       class.Name,
				Default:    class.Annotations["ingressclass.kubernetes.io/is-default-class"] == "true",
				Controller: class.Spec.Controller,
			})
		}
		sort.Slice(result.IngressClasses, func(i, j int) bool { return result.IngressClasses[i].Name < result.IngressClasses[j].Name })
		if len(result.IngressClasses) == 0 {
			result.Errors = append(result.Errors, "未发现 IngressClass；请先安装 ingress-nginx 并确认 hostNetwork/443 暴露方式。")
		}
	}

	resources, err := server.client.Discovery().ServerResourcesForGroupVersion(pxcGroupVersion)
	if err != nil || resources == nil {
		result.Errors = append(result.Errors, "未检测到 PXC Operator CRD（perconaxtradbclusters.pxc.percona.com）；请先安装 PXC Operator。")
	} else {
		for _, resource := range resources.APIResources {
			if resource.Name == pxcResource || resource.Kind == "PerconaXtraDBCluster" {
				result.PXCAvailable = true
				break
			}
		}
		if !result.PXCAvailable {
			result.Errors = append(result.Errors, "PXC API 组存在，但未找到 PerconaXtraDBCluster 资源；请检查 Operator 版本。")
		}
	}

	secret, err := server.client.CoreV1().Secrets(result.Namespace).Get(ctx, server.runtimeSecret(), metav1.GetOptions{})
	switch {
	case err == nil && secret != nil:
		result.RuntimeSecretExists = true
		result.Warnings = append(result.Warnings, "平台已完成初始化；如需重装，请先备份并删除运行 Secret。")
	case apierrors.IsNotFound(err):
	default:
		result.Errors = append(result.Errors, fmt.Sprintf("读取运行 Secret 失败：%v", err))
	}

	if result.Warnings == nil {
		result.Warnings = []string{}
	}
	if result.Errors == nil {
		result.Errors = []string{}
	}
	return result
}

func (server *Server) preflightWithTimeout(ctx context.Context) PreflightResult {
	timeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return server.preflight(timeout)
}

func (server *Server) runtimeSecret() string {
	if strings.TrimSpace(server.config.RuntimeSecret) != "" {
		return server.config.RuntimeSecret
	}
	return "ftc-platform-runtime"
}

func nodeArchitecture(node *corev1.Node) string {
	if value := node.Labels["kubernetes.io/arch"]; value != "" {
		return value
	}
	if value := node.Labels["beta.kubernetes.io/arch"]; value != "" {
		return value
	}
	return "unknown"
}

func hasNoScheduleTaint(taints []corev1.Taint) bool {
	for _, taint := range taints {
		if taint.Effect == corev1.TaintEffectNoSchedule {
			return true
		}
	}
	return false
}

func taintList(taints []corev1.Taint) []string {
	values := make([]string, 0, len(taints))
	for _, taint := range taints {
		value := taint.Key
		if taint.Value != "" {
			value += "=" + taint.Value
		}
		value += ":" + string(taint.Effect)
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func bindingMode(class storagev1.StorageClass) string {
	if class.VolumeBindingMode == nil {
		return string(storagev1.VolumeBindingImmediate)
	}
	return string(*class.VolumeBindingMode)
}
