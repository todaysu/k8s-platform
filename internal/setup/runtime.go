package setup

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	platformRuntimeSecret   = "ftc-platform-runtime"
	platformBootstrapSecret = "ftc-platform-bootstrap"
	platformConfigMap       = "ftc-platform"
)

type RuntimePlan struct {
	Namespace         string
	PlatformImage     string
	ImagePullSecret   string
	APIReplicas       int32
	CollectorReplicas int32
	WorkerReplicas    int32
}

func DefaultRuntimePlan() RuntimePlan {
	return RuntimePlan{Namespace: "ftc-platform", APIReplicas: 2, CollectorReplicas: 2, WorkerReplicas: 1}
}

func BuildRuntimeConfigMap(plan RuntimePlan) *corev1.ConfigMap {
	namespace := runtimeNamespace(plan)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: platformConfigMap, Namespace: namespace, Labels: map[string]string{"app.kubernetes.io/part-of": "ftc-platform"}},
		Data: map[string]string{
			"LISTEN_ADDR":                        ":8080",
			"METRICS_ADDR":                       ":9090",
			"WEB_DIST_DIR":                       "/app/web",
			"SETUP_MODE":                         "false",
			"SECURE_COOKIES":                     "true",
			"TRUST_PROXY_HEADERS":                "true",
			"ALLOW_LOOPBACK_WS_ORIGIN":           "false",
			"ENABLE_POD_EXEC":                    "false",
			"LEADER_ELECTION_ENABLED":            "true",
			"LEADER_ELECTION_NAMESPACE":          namespace,
			"LEADER_ELECTION_NAME":               "ftc-platform-collectors",
			"HEALTH_COLLECTION_INTERVAL":         "1m",
			"HEALTH_COLLECTION_TIMEOUT":          "20s",
			"HEALTH_COLLECTION_WORKERS":          "5",
			"HEALTH_INSTANCE_SOURCE":             "workloads",
			"CAPACITY_COLLECTION_INTERVAL":       "5m",
			"CAPACITY_COLLECTION_TIMEOUT":        "45s",
			"CAPACITY_COLLECTION_WORKERS":        "3",
			"RESOURCE_INDEX_INTERVAL":            "10m",
			"RESOURCE_INDEX_TIMEOUT":             "30s",
			"RESOURCE_INDEX_WORKERS":             "2",
			"SECURITY_SCAN_WORKERS":              "2",
			"SECURITY_SCAN_TIMEOUT":              "30m",
			"SECURITY_POD_STARTUP_TIMEOUT":       "5m",
			"SECURITY_HOST_SCAN_WORKERS":         "5",
			"SECURITY_SCANNER_NAMESPACE":         "kube-system",
			"SECURITY_SCANNER_IMAGE_PULL_SECRET": "",
			"SYSTEM_NAMESPACES":                  "kube-system,cmb-monitoring",
		},
	}
}

func BuildRuntimeDeployments(plan RuntimePlan) ([]*appsv1.Deployment, error) {
	namespace := runtimeNamespace(plan)
	image := strings.TrimSpace(plan.PlatformImage)
	if image == "" {
		return nil, fmt.Errorf("平台运行镜像不能为空")
	}
	replicas := func(value int32) *int32 {
		if value < 0 {
			value = 0
		}
		return &value
	}
	affinity := &corev1.Affinity{PodAntiAffinity: &corev1.PodAntiAffinity{
		PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
			Weight: 100,
			PodAffinityTerm: corev1.PodAffinityTerm{
				TopologyKey:   "kubernetes.io/hostname",
				LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "ftc-platform"}},
			},
		}},
	}}
	container := func(role string) corev1.Container {
		readinessPath, livenessPath := "/api/v1/health", "/api/v1/health"
		if role != "api" {
			readinessPath, livenessPath = "/readyz", "/healthz"
		}
		return corev1.Container{
			Name:            "platform",
			Image:           image,
			ImagePullPolicy: corev1.PullIfNotPresent,
			EnvFrom:         []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: platformConfigMap}}}},
			Env: []corev1.EnvVar{
				{Name: "PLATFORM_ROLE", Value: role},
				{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
				{Name: "POD_NAMESPACE", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"}}},
				{Name: "PLATFORM_DATABASE_DSN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: platformRuntimeSecret}, Key: "dsn"}}},
				{Name: "ADMIN_USERNAME", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: platformRuntimeSecret}, Key: "admin-username"}}},
				{Name: "ADMIN_PASSWORD_HASH", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: platformRuntimeSecret}, Key: "admin-password-hash"}}},
				{Name: "CREDENTIAL_ENCRYPTION_KEY", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: platformBootstrapSecret}, Key: "encryption-key"}}},
			},
			Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}, {Name: "metrics", ContainerPort: 9090}},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: readinessPath, Port: intstr.FromString("http")}},
				InitialDelaySeconds: 3, PeriodSeconds: 10,
			},
			LivenessProbe: &corev1.Probe{
				ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: livenessPath, Port: intstr.FromString("http")}},
				InitialDelaySeconds: 15, PeriodSeconds: 20,
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("512Mi")},
				Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("2Gi")},
			},
			SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: boolPointer(false), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}, ReadOnlyRootFilesystem: boolPointer(true)},
			VolumeMounts:    []corev1.VolumeMount{{Name: "tmp", MountPath: "/tmp"}},
		}
	}
	spec := func(role, name string, count int32, withAffinity bool) appsv1.DeploymentSpec {
		deployment := appsv1.DeploymentSpec{
			Replicas: replicas(count),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app.kubernetes.io/name": name, "ftc-platform/role": role}},
				Spec: corev1.PodSpec{
					ServiceAccountName: "ftc-platform",
					SecurityContext:    &corev1.PodSecurityContext{RunAsNonRoot: boolPointer(true), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
					Containers:         []corev1.Container{container(role)},
					Volumes:            []corev1.Volume{{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
				},
			},
		}
		if withAffinity {
			deployment.Template.Spec.Affinity = affinity
		}
		if plan.ImagePullSecret != "" {
			deployment.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: plan.ImagePullSecret}}
		}
		return deployment
	}
	return []*appsv1.Deployment{
		{ObjectMeta: metav1.ObjectMeta{Name: "ftc-platform", Namespace: namespace, Labels: map[string]string{"app.kubernetes.io/name": "ftc-platform", "app.kubernetes.io/part-of": "ftc-platform"}}, Spec: spec("api", "ftc-platform", plan.APIReplicas, true)},
		{ObjectMeta: metav1.ObjectMeta{Name: "ftc-platform-collector", Namespace: namespace, Labels: map[string]string{"app.kubernetes.io/name": "ftc-platform-collector", "app.kubernetes.io/part-of": "ftc-platform"}}, Spec: spec("collector", "ftc-platform-collector", plan.CollectorReplicas, true)},
		{ObjectMeta: metav1.ObjectMeta{Name: "ftc-platform-worker", Namespace: namespace, Labels: map[string]string{"app.kubernetes.io/name": "ftc-platform-worker", "app.kubernetes.io/part-of": "ftc-platform"}}, Spec: spec("worker", "ftc-platform-worker", plan.WorkerReplicas, false)},
	}, nil
}

func BuildRuntimeService(namespace string) *corev1.Service {
	if namespace == "" {
		namespace = "ftc-platform"
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "ftc-platform", Namespace: namespace, Labels: map[string]string{"app.kubernetes.io/name": "ftc-platform", "app.kubernetes.io/part-of": "ftc-platform"}},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app.kubernetes.io/name": "ftc-platform"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromString("http")}, {Name: "metrics", Port: 9090, TargetPort: intstr.FromString("metrics")}},
		},
	}
}

func BuildRuntimePDB(namespace string) *policyv1.PodDisruptionBudget {
	if namespace == "" {
		namespace = "ftc-platform"
	}
	minimum := intstr.FromInt(1)
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "ftc-platform", Namespace: namespace},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &minimum,
			Selector:     &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "ftc-platform"}},
		},
	}
}

// ApplyRuntime creates or updates the runtime ConfigMap, Service, PDB and the
// API/Collector/Worker Deployments. The runtime Secret must exist first.
func (server *Server) ApplyRuntime(ctx context.Context, plan RuntimePlan) error {
	if server.client == nil {
		return fmt.Errorf("初始化控制器未连接 Kubernetes API")
	}
	namespace := runtimeNamespace(plan)
	config := BuildRuntimeConfigMap(plan)
	deployments, err := BuildRuntimeDeployments(plan)
	if err != nil {
		return err
	}
	if _, err := server.client.CoreV1().ConfigMaps(namespace).Create(ctx, config, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("创建运行 ConfigMap 失败：%w", err)
	}
	service := BuildRuntimeService(namespace)
	if _, err := server.client.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("创建运行 Service 失败：%w", err)
	}
	pdb := BuildRuntimePDB(namespace)
	if _, err := server.client.PolicyV1().PodDisruptionBudgets(namespace).Create(ctx, pdb, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("创建 PDB 失败：%w", err)
	}
	for _, desired := range deployments {
		existing, err := server.client.AppsV1().Deployments(namespace).Get(ctx, desired.Name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			if _, err := server.client.AppsV1().Deployments(namespace).Create(ctx, desired, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("创建 Deployment %s 失败：%w", desired.Name, err)
			}
		case err != nil:
			return fmt.Errorf("读取 Deployment %s 失败：%w", desired.Name, err)
		default:
			desired.ResourceVersion = existing.ResourceVersion
			if _, err := server.client.AppsV1().Deployments(namespace).Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
				return fmt.Errorf("更新 Deployment %s 失败：%w", desired.Name, err)
			}
		}
	}
	return nil
}

func runtimeNamespace(plan RuntimePlan) string {
	if strings.TrimSpace(plan.Namespace) == "" {
		return "ftc-platform"
	}
	return plan.Namespace
}

func boolPointer(value bool) *bool { return &value }
