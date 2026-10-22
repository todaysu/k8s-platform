package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	localStorageClass = "ftc-platform-local"
	minimumVolumeGiB  = 10
)

type LocalVolumeRequest struct {
	Node    string `json:"node"`
	Path    string `json:"path"`
	SizeGiB int64  `json:"size_gib"`
}

type LocalStoragePlan struct {
	ClassName string
	Volumes   []LocalVolumeRequest
	PVs       []*corev1.PersistentVolume
	Class     *storagev1.StorageClass
}

// PlanLocalStorage validates the operator-provided node/path pairs and builds
// static local PVs. It is pure; nothing is written until ApplyLocalStorage.
func PlanLocalStorage(volumes []LocalVolumeRequest, className string) (LocalStoragePlan, error) {
	if strings.TrimSpace(className) == "" {
		className = localStorageClass
	}
	if len(volumes) != 3 {
		return LocalStoragePlan{}, fmt.Errorf("PXC 3 节点需要恰好 3 个本地卷，当前为 %d 个", len(volumes))
	}
	nodes := map[string]bool{}
	normalized := make([]LocalVolumeRequest, 0, len(volumes))
	for index, volume := range volumes {
		node := strings.TrimSpace(volume.Node)
		if node == "" {
			return LocalStoragePlan{}, fmt.Errorf("第 %d 个本地卷缺少节点名", index+1)
		}
		if nodes[node] {
			return LocalStoragePlan{}, fmt.Errorf("节点 %s 被重复选择", node)
		}
		cleanPath := path.Clean(strings.TrimSpace(volume.Path))
		if err := validateLocalPath(cleanPath); err != nil {
			return LocalStoragePlan{}, fmt.Errorf("节点 %s 的路径无效：%w", node, err)
		}
		if volume.SizeGiB < minimumVolumeGiB {
			return LocalStoragePlan{}, fmt.Errorf("节点 %s 的容量不能小于 %d GiB", node, minimumVolumeGiB)
		}
		normalized = append(normalized, LocalVolumeRequest{Node: node, Path: cleanPath, SizeGiB: volume.SizeGiB})
		nodes[node] = true
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Node < normalized[j].Node })

	wait := storagev1.VolumeBindingWaitForFirstConsumer
	retain := corev1.PersistentVolumeReclaimRetain
	class := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: className,
			Labels: map[string]string{
				"app.kubernetes.io/part-of": "ftc-platform",
				"ftc-platform/role":         "mysql-storage",
			},
			Annotations: map[string]string{
				"ftc-platform/managed-by": "ftc-platform-setup",
			},
		},
		Provisioner:       "kubernetes.io/no-provisioner",
		VolumeBindingMode: &wait,
		ReclaimPolicy:     &retain,
	}
	// Local static volumes cannot be expanded by the provisioner. Keep the SC
	// honest; the wizard shows a capacity warning instead of a false promise.
	expansion := false
	class.AllowVolumeExpansion = &expansion

	plan := LocalStoragePlan{ClassName: className, Volumes: normalized, Class: class}
	for _, volume := range normalized {
		sum := sha256.Sum256([]byte(volume.Node + "\x00" + volume.Path))
		name := "ftc-mysql-" + hex.EncodeToString(sum[:6])
		size := resource.NewQuantity(volume.SizeGiB*1024*1024*1024, resource.BinarySI)
		filesystem := corev1.PersistentVolumeFilesystem
		pv := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					"app.kubernetes.io/part-of": "ftc-platform",
					"ftc-platform/role":         "mysql-data",
					"ftc-platform/node":         volume.Node,
				},
				Annotations: map[string]string{
					"ftc-platform/path": volume.Path,
				},
			},
			Spec: corev1.PersistentVolumeSpec{
				Capacity:                      corev1.ResourceList{corev1.ResourceStorage: *size},
				AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				PersistentVolumeReclaimPolicy: retain,
				StorageClassName:              className,
				VolumeMode:                    &filesystem,
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					Local: &corev1.LocalVolumeSource{Path: volume.Path},
				},
				NodeAffinity: &corev1.VolumeNodeAffinity{
					Required: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{{
							MatchExpressions: []corev1.NodeSelectorRequirement{{
								Key:      "kubernetes.io/hostname",
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{volume.Node},
							}},
						}},
					},
				},
			},
		}
		plan.PVs = append(plan.PVs, pv)
	}
	return plan, nil
}

func validateLocalPath(value string) error {
	if value == "" || !strings.HasPrefix(value, "/") {
		return fmt.Errorf("必须是绝对路径")
	}
	if value == "/" {
		return fmt.Errorf("不能使用根目录")
	}
	if strings.Contains(value, "..") {
		return fmt.Errorf("不能包含 ..")
	}
	blocked := []string{"/bin", "/boot", "/dev", "/etc", "/home", "/lib", "/lib64", "/proc", "/root", "/run", "/sbin", "/sys", "/tmp", "/usr", "/var/lib/kubelet", "/var/lib/mysql", "/var/log", "/var/run"}
	for _, prefix := range blocked {
		if value == prefix || strings.HasPrefix(value, prefix+"/") {
			return fmt.Errorf("不能使用系统目录 %s", prefix)
		}
	}
	return nil
}

// ApplyLocalStorage creates the no-provisioner StorageClass and the static PVs.
// Existing objects are validated and never overwritten.
func (server *Server) ApplyLocalStorage(ctx context.Context, plan LocalStoragePlan) error {
	if server.client == nil {
		return fmt.Errorf("初始化控制器未连接 Kubernetes API")
	}
	nodes, err := server.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("读取节点失败：%w", err)
	}
	known := map[string]bool{}
	for _, node := range nodes.Items {
		known[node.Name] = true
	}
	for _, volume := range plan.Volumes {
		if !known[volume.Node] {
			return fmt.Errorf("节点 %s 不存在", volume.Node)
		}
	}

	classes := server.client.StorageV1().StorageClasses()
	existingClass, err := classes.Get(ctx, plan.ClassName, metav1.GetOptions{})
	switch {
	case err == nil:
		if existingClass.Provisioner != "kubernetes.io/no-provisioner" {
			return fmt.Errorf("StorageClass %s 已存在且不是 no-provisioner；请换一个名字", plan.ClassName)
		}
		if existingClass.VolumeBindingMode == nil || *existingClass.VolumeBindingMode != storagev1.VolumeBindingWaitForFirstConsumer {
			return fmt.Errorf("StorageClass %s 已存在但 volumeBindingMode 不是 WaitForFirstConsumer", plan.ClassName)
		}
	case apierrors.IsNotFound(err):
		if _, err := classes.Create(ctx, plan.Class, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("创建 StorageClass 失败：%w", err)
		}
	default:
		return fmt.Errorf("读取 StorageClass 失败：%w", err)
	}

	pvs := server.client.CoreV1().PersistentVolumes()
	for _, desired := range plan.PVs {
		existing, err := pvs.Get(ctx, desired.Name, metav1.GetOptions{})
		switch {
		case err == nil:
			existingSize := existing.Spec.Capacity[corev1.ResourceStorage]
			desiredSize := desired.Spec.Capacity[corev1.ResourceStorage]
			if existing.Spec.Local == nil || existing.Spec.Local.Path != desired.Spec.Local.Path || existingSize.Cmp(desiredSize) != 0 {
				return fmt.Errorf("PV %s 已存在但路径或容量与本次配置不一致", desired.Name)
			}
		case apierrors.IsNotFound(err):
			if _, err := pvs.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("创建 PV %s 失败：%w", desired.Name, err)
			}
		default:
			return fmt.Errorf("读取 PV %s 失败：%w", desired.Name, err)
		}
	}
	return nil
}
