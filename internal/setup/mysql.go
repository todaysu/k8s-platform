package setup

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	pxcClusterName = "platform-mysql"
	pxcSecretName  = "platform-mysql-secrets"
)

var pxcGVR = schema.GroupVersionResource{Group: "pxc.percona.com", Version: "v1", Resource: "perconaxtradbclusters"}

type MySQLPlan struct {
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	SecretName      string `json:"secret_name"`
	Replicas        int    `json:"replicas"`
	StorageClass    string `json:"storage_class"`
	StorageSize     string `json:"storage_size"`
	PXCImage        string `json:"pxc_image"`
	HAProxyImage    string `json:"haproxy_image"`
	BackupImage     string `json:"backup_image"`
	OperatorImage   string `json:"operator_image"`
	ImagePullSecret string `json:"image_pull_secret"`
}

func DefaultMySQLPlan() MySQLPlan {
	return MySQLPlan{
		Namespace:     "ftc-platform",
		Name:          pxcClusterName,
		SecretName:    pxcSecretName,
		Replicas:      3,
		StorageClass:  localStorageClass,
		StorageSize:   "200Gi",
		PXCImage:      "percona/percona-xtradb-cluster:8.0.42-33.1",
		HAProxyImage:  "percona/haproxy:2.8.15",
		BackupImage:   "percona/percona-xtrabackup:8.0.42-33.1",
		OperatorImage: "percona/percona-xtradb-cluster-operator:1.18.0",
	}
}

// BuildMySQLCluster returns the PXC CR. Keeping it as a function makes the
// plan testable without a live API server.
func BuildMySQLCluster(plan MySQLPlan) (*unstructured.Unstructured, error) {
	if plan.Replicas != 3 {
		return nil, fmt.Errorf("PXC 高可用必须使用 3 个副本，当前为 %d", plan.Replicas)
	}
	if strings.TrimSpace(plan.PXCImage) == "" || strings.TrimSpace(plan.HAProxyImage) == "" {
		return nil, fmt.Errorf("PXC 与 HAProxy 镜像不能为空")
	}
	if strings.TrimSpace(plan.StorageClass) == "" || strings.TrimSpace(plan.StorageSize) == "" {
		return nil, fmt.Errorf("存储类和容量不能为空")
	}
	if plan.Namespace == "" {
		plan.Namespace = "ftc-platform"
	}
	if plan.Name == "" {
		plan.Name = pxcClusterName
	}
	if plan.SecretName == "" {
		plan.SecretName = pxcSecretName
	}
	resources := map[string]any{
		"requests": map[string]any{"cpu": "1", "memory": "2Gi"},
		"limits":   map[string]any{"cpu": "2", "memory": "4Gi"},
	}
	haproxyResources := map[string]any{
		"requests": map[string]any{"cpu": "100m", "memory": "128Mi"},
		"limits":   map[string]any{"cpu": "500m", "memory": "512Mi"},
	}
	pxc := map[string]any{
		"size":            int64(plan.Replicas),
		"image":           plan.PXCImage,
		"imagePullPolicy": "IfNotPresent",
		"resources":       resources,
		"affinity":        map[string]any{"antiAffinityTopologyKey": "kubernetes.io/hostname"},
		"volumeSpec": map[string]any{"persistentVolumeClaim": map[string]any{
			"storageClassName": plan.StorageClass,
			"resources":        map[string]any{"requests": map[string]any{"storage": plan.StorageSize}},
		}},
		"podDisruptionBudget": map[string]any{"maxUnavailable": int64(1)},
		"livenessProbes":      map[string]any{"initialDelaySeconds": int64(300), "timeoutSeconds": int64(30), "periodSeconds": int64(30), "failureThreshold": int64(10)},
		"readinessProbes":     map[string]any{"initialDelaySeconds": int64(20), "timeoutSeconds": int64(30), "periodSeconds": int64(15), "failureThreshold": int64(10)},
		// The platform migration lock uses GET_LOCK, which PXC rejects in
		// ENFORCING mode.
		"configuration": "[mysqld]\npxc_strict_mode=DISABLED\n",
	}
	haproxy := map[string]any{
		"enabled": true, "size": int64(plan.Replicas), "image": plan.HAProxyImage,
		"imagePullPolicy": "IfNotPresent",
		"resources":       haproxyResources,
		"affinity":        map[string]any{"antiAffinityTopologyKey": "kubernetes.io/hostname"},
		"readinessProbes": map[string]any{"initialDelaySeconds": int64(30), "timeoutSeconds": int64(30), "periodSeconds": int64(10), "failureThreshold": int64(10)},
		"livenessProbes":  map[string]any{"initialDelaySeconds": int64(120), "timeoutSeconds": int64(30), "periodSeconds": int64(30), "failureThreshold": int64(6)},
	}
	if plan.ImagePullSecret != "" {
		pxc["imagePullSecrets"] = []any{map[string]any{"name": plan.ImagePullSecret}}
		haproxy["imagePullSecrets"] = []any{map[string]any{"name": plan.ImagePullSecret}}
	}
	spec := map[string]any{
		"crVersion":                 "1.18.0",
		"secretsName":               plan.SecretName,
		"enableCRValidationWebhook": false,
		// Internal TLS is disabled for the first delivery; PXC requires an
		// explicit unsafe flag when TLS is off.
		"tls":            map[string]any{"enabled": false},
		"unsafeFlags":    map[string]any{"tls": true},
		"updateStrategy": "SmartUpdate",
		"upgradeOptions": map[string]any{"apply": "disabled"},
		"pxc":            pxc,
		"haproxy":        haproxy,
		"logcollector":   map[string]any{"enabled": false},
	}
	if plan.BackupImage != "" {
		spec["backup"] = map[string]any{"image": plan.BackupImage}
	}
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "pxc.percona.com/v1",
		"kind":       "PerconaXtraDBCluster",
		"metadata": map[string]any{
			"name":      plan.Name,
			"namespace": plan.Namespace,
			"labels":    map[string]any{"app.kubernetes.io/part-of": "ftc-platform", "ftc-platform/role": "mysql"},
		},
		"spec": spec,
	}}
	return object, nil
}

// ApplyMySQL creates the PXC Secret and CR. It refuses to mutate an existing
// cluster, so an install retry cannot silently change a running database.
func (server *Server) ApplyMySQL(ctx context.Context, plan MySQLPlan) error {
	if err := server.ensurePXCSecret(ctx, &plan); err != nil {
		return err
	}
	object, err := BuildMySQLCluster(plan)
	if err != nil {
		return err
	}
	resource := server.dynamic.Resource(pxcGVR).Namespace(plan.Namespace)
	existing, err := resource.Get(ctx, plan.Name, metav1.GetOptions{})
	switch {
	case err == nil && existing != nil:
		return fmt.Errorf("PXC 集群 %s 已存在；请勿重复初始化", plan.Name)
	case apierrors.IsNotFound(err):
		if _, err := resource.Create(ctx, object, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("创建 PXC 集群失败：%w", err)
		}
	default:
		return fmt.Errorf("读取 PXC 集群失败：%w", err)
	}
	return nil
}

// EnsureMySQL creates the Secret and CR only when they do not exist. It is the
// retry-safe variant used by the install orchestrator.
func (server *Server) EnsureMySQL(ctx context.Context, plan MySQLPlan) error {
	if err := server.ensurePXCSecret(ctx, &plan); err != nil {
		return err
	}
	object, err := BuildMySQLCluster(plan)
	if err != nil {
		return err
	}
	resource := server.dynamic.Resource(pxcGVR).Namespace(plan.Namespace)
	_, err = resource.Get(ctx, plan.Name, metav1.GetOptions{})
	switch {
	case err == nil:
		return nil
	case apierrors.IsNotFound(err):
		if _, err := resource.Create(ctx, object, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("创建 PXC 集群失败：%w", err)
		}
		return nil
	default:
		return fmt.Errorf("读取 PXC 集群失败：%w", err)
	}
}

func (server *Server) ensurePXCSecret(ctx context.Context, plan *MySQLPlan) error {
	if server.client == nil || server.dynamic == nil {
		return fmt.Errorf("初始化控制器未连接 Kubernetes API")
	}
	if plan.Namespace == "" {
		plan.Namespace = "ftc-platform"
	}
	if plan.SecretName == "" {
		plan.SecretName = pxcSecretName
	}
	if plan.Name == "" {
		plan.Name = pxcClusterName
	}
	secret, err := server.client.CoreV1().Secrets(plan.Namespace).Get(ctx, plan.SecretName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		generated, generateErr := pxcSecret(plan.Namespace, plan.SecretName)
		if generateErr != nil {
			return generateErr
		}
		if _, err := server.client.CoreV1().Secrets(plan.Namespace).Create(ctx, generated, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("创建 PXC 密码 Secret 失败：%w", err)
		}
	case err != nil:
		return fmt.Errorf("读取 PXC 密码 Secret 失败：%w", err)
	case secret != nil:
		// Keep an existing secret: changing it here would rotate passwords
		// without going through the operator.
	}
	return nil
}

func (server *Server) WaitForMySQL(ctx context.Context, name, namespace string, timeout time.Duration) error {
	if server.dynamic == nil {
		return fmt.Errorf("初始化控制器未连接 Kubernetes API")
	}
	if name == "" {
		name = pxcClusterName
	}
	if namespace == "" {
		namespace = "ftc-platform"
	}
	deadline := time.Now().Add(timeout)
	resource := server.dynamic.Resource(pxcGVR).Namespace(namespace)
	for {
		object, err := resource.Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			state, _, _ := unstructured.NestedString(object.Object, "status", "state")
			ready, _, _ := unstructured.NestedInt64(object.Object, "status", "pxc", "ready")
			haproxyReady, _, _ := unstructured.NestedInt64(object.Object, "status", "haproxy", "ready")
			if state == "ready" && ready == 3 && haproxyReady == 3 {
				return nil
			}
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("读取 PXC 状态失败：%w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待 PXC 就绪超时（%s）", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func pxcSecret(namespace, name string) (*corev1.Secret, error) {
	keys := []string{"root", "xtrabackup", "monitor", "clustercheck", "proxyadmin", "operator", "replication"}
	data := map[string][]byte{}
	for _, key := range keys {
		value, err := randomPassword(24)
		if err != nil {
			return nil, err
		}
		data[key] = []byte(value)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{"app.kubernetes.io/part-of": "ftc-platform"}},
		Type:       corev1.SecretTypeOpaque,
		Data:       data,
	}, nil
}

func randomPassword(length int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var builder strings.Builder
	for i := 0; i < length; i++ {
		value, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		builder.WriteByte(alphabet[value.Int64()])
	}
	return builder.String(), nil
}

// EnsurePXCOperator points the pre-installed Operator Deployment at the
// private-registry image and waits for it before a PXC CR is created.
func (server *Server) EnsurePXCOperator(ctx context.Context, plan MySQLPlan) error {
	if server.client == nil {
		return fmt.Errorf("初始化控制器未连接 Kubernetes API")
	}
	namespace := plan.Namespace
	if namespace == "" {
		namespace = "ftc-platform"
	}
	deployment, err := server.client.AppsV1().Deployments(namespace).Get(ctx, "percona-xtradb-cluster-operator", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("未找到 PXC Operator Deployment；请确认 install.yaml 已包含 Operator 清单")
	}
	if err != nil {
		return fmt.Errorf("读取 PXC Operator Deployment 失败：%w", err)
	}
	changed := false
	for index := range deployment.Spec.Template.Spec.Containers {
		container := &deployment.Spec.Template.Spec.Containers[index]
		if container.Name != "percona-xtradb-cluster-operator" {
			continue
		}
		if strings.TrimSpace(plan.OperatorImage) != "" && container.Image != plan.OperatorImage {
			container.Image = plan.OperatorImage
			changed = true
		}
		if container.LivenessProbe != nil {
			if container.LivenessProbe.TimeoutSeconds < 30 {
				container.LivenessProbe.TimeoutSeconds = 30
				changed = true
			}
			if container.LivenessProbe.FailureThreshold < 10 {
				container.LivenessProbe.FailureThreshold = 10
				changed = true
			}
			if container.LivenessProbe.InitialDelaySeconds < 30 {
				container.LivenessProbe.InitialDelaySeconds = 30
				changed = true
			}
		}
	}
	if plan.ImagePullSecret != "" {
		found := false
		for _, reference := range deployment.Spec.Template.Spec.ImagePullSecrets {
			if reference.Name == plan.ImagePullSecret {
				found = true
				break
			}
		}
		if !found {
			deployment.Spec.Template.Spec.ImagePullSecrets = append(deployment.Spec.Template.Spec.ImagePullSecrets, corev1.LocalObjectReference{Name: plan.ImagePullSecret})
			changed = true
		}
	}
	if changed {
		if _, err := server.client.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("更新 PXC Operator 镜像失败：%w", err)
		}
	}
	if err := server.WaitForDeployment(ctx, namespace, "percona-xtradb-cluster-operator", 5*time.Minute); err != nil {
		return fmt.Errorf("PXC Operator 未就绪：%w", err)
	}
	return nil
}
