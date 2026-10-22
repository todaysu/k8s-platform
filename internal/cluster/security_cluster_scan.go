package cluster

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
)

// SecurityClusterScanConnector adds read-only cluster-side checks to the
// node-level kube-bench scan. It never reads Secret or ConfigMap contents.

type SecurityClusterScanConnector interface {
	RunSecurityClusterScan(context.Context, ParsedKubeconfig) (SecurityBaselineReport, error)
}

type clusterSecurityCheck struct {
	id, title, severity, status, remediation string
	resources                                map[string]struct{}
}

func (check *clusterSecurityCheck) add(resource string) {
	if check.resources == nil {
		check.resources = map[string]struct{}{}
	}
	check.resources[resource] = struct{}{}
}

func (connector *KubernetesConnector) RunSecurityClusterScan(ctx context.Context, parsed ParsedKubeconfig) (SecurityBaselineReport, error) {
	clientset, err := connector.clientsetWithTimeout(parsed, 30*time.Second)
	if err != nil {
		return SecurityBaselineReport{}, err
	}
	return scanClusterSecurity(ctx, clientset)
}

func scanClusterSecurity(ctx context.Context, clientset kubernetes.Interface) (SecurityBaselineReport, error) {
	checks := newClusterSecurityChecks()
	podsByNamespace := map[string]int{}
	policiesByNamespace := map[string]int{}
	podCount := 0
	namespaceCount := 0

	// Every List below is paginated and reduced page by page. A 200-node
	// cluster holds thousands of Pods, and materialising the full list would
	// bypass the shared collection budget this platform enforces elsewhere.
	if err := visitPods(ctx, clientset, "", func(pod *corev1.Pod) error {
		podCount++
		if excludedClusterNamespace(pod.Namespace) || isSecurityScanPod(pod) {
			return nil
		}
		podsByNamespace[pod.Namespace]++
		resource := "Pod/" + pod.Namespace + "/" + pod.Name
		if pod.Spec.HostPID || pod.Spec.HostNetwork || pod.Spec.HostIPC {
			checks["CLUSTER-002"].add(resource)
		}
		for _, volume := range pod.Spec.Volumes {
			if volume.HostPath != nil {
				checks["CLUSTER-003"].add(resource)
				break
			}
		}
		runAsNonRoot := pod.Spec.SecurityContext != nil && pod.Spec.SecurityContext.RunAsNonRoot != nil && *pod.Spec.SecurityContext.RunAsNonRoot
		for _, container := range append(append([]corev1.Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...) {
			containerResource := resource + "/" + container.Name
			security := container.SecurityContext
			if security != nil {
				if security.Privileged != nil && *security.Privileged {
					checks["CLUSTER-001"].add(containerResource)
				}
				if security.RunAsNonRoot != nil {
					runAsNonRoot = *security.RunAsNonRoot
				}
				if security.RunAsUser != nil && *security.RunAsUser == 0 {
					runAsNonRoot = false
				}
				if security.Capabilities != nil && len(security.Capabilities.Add) > 0 {
					checks["CLUSTER-005"].add(containerResource)
				}
				if security.AllowPrivilegeEscalation == nil || *security.AllowPrivilegeEscalation {
					checks["CLUSTER-004"].add(containerResource)
				}
			} else {
				checks["CLUSTER-004"].add(containerResource)
			}
			if !runAsNonRoot {
				checks["CLUSTER-004"].add(containerResource)
			}
			if len(container.Resources.Limits) == 0 || container.Resources.Limits.Cpu().IsZero() || container.Resources.Limits.Memory().IsZero() {
				checks["CLUSTER-006"].add(containerResource)
			}
			image := strings.TrimSpace(container.Image)
			if image != "" {
				lastSlash := strings.LastIndex(image, "/")
				lastColon := strings.LastIndex(image, ":")
				if lastColon <= lastSlash || strings.HasSuffix(image, ":latest") {
					checks["CLUSTER-007"].add(containerResource)
				}
			}
		}
		return nil
	}); err != nil {
		return SecurityBaselineReport{}, err
	}

	if err := visitPages(ctx, func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		return clientset.NetworkingV1().NetworkPolicies("").List(ctx, options)
	}, func(policy *networkingv1.NetworkPolicy) error {
		policiesByNamespace[policy.Namespace]++
		return nil
	}); err != nil {
		return SecurityBaselineReport{}, err
	}

	if err := visitPages(ctx, func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		return clientset.RbacV1().ClusterRoles().List(ctx, options)
	}, func(role *rbacv1.ClusterRole) error {
		if strings.HasPrefix(role.Name, "system:") || role.Labels["kubernetes.io/bootstrapping"] == "rbac-defaults" || strings.HasPrefix(role.Name, "local-path-provisioner") {
			return nil
		}
		resource := "ClusterRole/" + role.Name
		for _, rule := range role.Rules {
			if hasWildcard(rule.Verbs) || hasWildcard(rule.Resources) || hasWildcard(rule.APIGroups) {
				checks["CLUSTER-008"].add(resource)
				break
			}
		}
		return nil
	}); err != nil {
		return SecurityBaselineReport{}, err
	}

	// Service.Type is required by CLUSTER-009, so the type is evaluated during
	// the walk rather than via boundedServices, which keeps only name/ports.
	if err := visitPages(ctx, func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		return clientset.CoreV1().Services("").List(ctx, options)
	}, func(service *corev1.Service) error {
		if excludedClusterNamespace(service.Namespace) {
			return nil
		}
		if service.Spec.Type == corev1.ServiceTypeNodePort || service.Spec.Type == corev1.ServiceTypeLoadBalancer {
			checks["CLUSTER-009"].add("Service/" + service.Namespace + "/" + service.Name)
		}
		return nil
	}); err != nil {
		return SecurityBaselineReport{}, err
	}

	if err := visitPages(ctx, func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		return clientset.CoreV1().Namespaces().List(ctx, options)
	}, func(namespace *corev1.Namespace) error {
		namespaceCount++
		if excludedClusterNamespace(namespace.Name) {
			return nil
		}
		if podsByNamespace[namespace.Name] > 0 && policiesByNamespace[namespace.Name] == 0 {
			checks["CLUSTER-010"].add("Namespace/" + namespace.Name)
		}
		return nil
	}); err != nil {
		return SecurityBaselineReport{}, err
	}

	report := SecurityBaselineReport{
		Mode:       "cluster-api",
		ScannedAt:  time.Now().UTC(),
		Namespaces: namespaceCount,
		Pods:       podCount,
		Coverage: SecurityBaselineCoverage{
			Included:   []string{"Pod 安全上下文（业务 Namespace）", "自定义 ClusterRole 通配权限", "Service 暴露面（业务 Namespace）", "Namespace 网络策略（业务 Namespace）"},
			NotCovered: []string{"镜像 CVE 与供应链", "Secret/ConfigMap 内容", "节点操作系统基线（由 kube-bench 覆盖）", "kube-system/kube-public/kube-node-lease/local-path-storage/cmb-monitoring 等系统组件"},
		},
	}
	ids := make([]string, 0, len(checks))
	for id := range checks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		check := checks[id]
		resources := sortedSecurityKeys(check.resources)
		affected := len(resources)
		status := "PASS"
		if affected > 0 {
			status = check.status
		}
		sample := resources
		if len(sample) > securityFindingSampleLimit {
			sample = sample[:securityFindingSampleLimit]
			report.TruncatedSamples = true
		}
		evidence := "只读 Kubernetes API 未发现该风险。"
		if affected > 0 {
			evidence = fmt.Sprintf("只读 Kubernetes API 在 %d 个资源上发现该风险。", affected)
		}
		report.Findings = append(report.Findings, SecurityBaselineFinding{
			ControlID: check.id, Status: status, Severity: check.severity, Category: "集群侧配置",
			Title: check.title, Affected: affected, Resources: sample, Evidence: evidence, Remediation: check.remediation,
		})
	}
	recomputeSecuritySummary(&report)
	return report, nil
}

func newClusterSecurityChecks() map[string]*clusterSecurityCheck {
	return map[string]*clusterSecurityCheck{
		"CLUSTER-001": {id: "CLUSTER-001", title: "容器不应以 privileged 模式运行", severity: "critical", status: "FAIL", remediation: "移除 privileged: true，按最小权限添加必要 capability。"},
		"CLUSTER-002": {id: "CLUSTER-002", title: "Pod 不应共享宿主机 PID/网络/IPC", severity: "high", status: "FAIL", remediation: "移除 hostPID、hostNetwork、hostIPC，改用 Service 或专用网络方案。"},
		"CLUSTER-003": {id: "CLUSTER-003", title: "Pod 不应挂载 hostPath", severity: "high", status: "FAIL", remediation: "使用 PVC、ConfigMap 或只读投射卷替代 hostPath。"},
		"CLUSTER-004": {id: "CLUSTER-004", title: "容器应以非 root 运行且禁止提权", severity: "high", status: "FAIL", remediation: "设置 runAsNonRoot: true、非 0 runAsUser 和 allowPrivilegeEscalation: false。"},
		"CLUSTER-005": {id: "CLUSTER-005", title: "容器不应添加额外 capabilities", severity: "medium", status: "WARN", remediation: "删除 capabilities.add，默认 drop ALL 后按需添加。"},
		"CLUSTER-006": {id: "CLUSTER-006", title: "容器应设置 CPU 和内存 limits", severity: "medium", status: "WARN", remediation: "为每个容器设置 resources.limits.cpu 和 memory。"},
		"CLUSTER-007": {id: "CLUSTER-007", title: "容器镜像不应使用 latest 或无 tag", severity: "low", status: "WARN", remediation: "固定不可变 tag 或 digest，避免 latest。"},
		"CLUSTER-008": {id: "CLUSTER-008", title: "自定义 ClusterRole 不应使用通配权限", severity: "high", status: "FAIL", remediation: "按最小权限拆分 verbs、resources 和 apiGroups。"},
		"CLUSTER-009": {id: "CLUSTER-009", title: "Service 不应无必要地暴露 NodePort/LoadBalancer", severity: "medium", status: "WARN", remediation: "优先使用 ClusterIP；确需外部暴露时配合入口网关和访问控制。"},
		"CLUSTER-010": {id: "CLUSTER-010", title: "运行 Pod 的 Namespace 应配置 NetworkPolicy", severity: "medium", status: "WARN", remediation: "为业务 Namespace 增加默认拒绝和显式放行策略。"},
	}
}

func hasWildcard(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "*" {
			return true
		}
	}
	return false
}

func excludedClusterNamespace(namespace string) bool {
	switch namespace {
	case "kube-system", "kube-public", "kube-node-lease", "local-path-storage", "cmb-monitoring":
		return true
	}
	return false
}

func isSecurityScanPod(pod *corev1.Pod) bool {
	return pod.Labels["ftc.cmbchina.com/security-scan"] != "" || strings.HasPrefix(pod.Name, "ftc-host-scan-")
}
