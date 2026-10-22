package runtimecontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"example.com/k8s-platform/internal/platformsettings"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const restartAnnotation = "ftc.cmbchina.com/restarted-at"

type DeploymentRestarter struct {
	client    kubernetes.Interface
	namespace string
}

func NewInCluster(namespace string) (*DeploymentRestarter, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return New(client, namespace), nil
}

func New(client kubernetes.Interface, namespace string) *DeploymentRestarter {
	return &DeploymentRestarter{client: client, namespace: namespace}
}

func (restarter *DeploymentRestarter) Restart(ctx context.Context, section string) (platformsettings.ApplyStatus, error) {
	primary, fallback, executable := restartTargets(section)
	if !executable {
		return platformsettings.ApplyStatus{Status: "saved", Message: "配置已保存；当前功能尚未接入独立执行器，未触发实例重启。"}, nil
	}
	deployment, err := restarter.client.AppsV1().Deployments(restarter.namespace).Get(ctx, primary, metav1.GetOptions{})
	if apierrors.IsNotFound(err) && fallback != "" {
		deployment, err = restarter.client.AppsV1().Deployments(restarter.namespace).Get(ctx, fallback, metav1.GetOptions{})
	}
	if err != nil {
		return platformsettings.ApplyStatus{Status: "failed", Message: "配置已保存，但未能找到需要重启的 Deployment。"}, err
	}
	payload, err := json.Marshal(map[string]any{
		"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]string{restartAnnotation: time.Now().UTC().Format(time.RFC3339Nano)}}}},
	})
	if err != nil {
		return platformsettings.ApplyStatus{}, err
	}
	name := deployment.Name
	if _, err := restarter.client.AppsV1().Deployments(restarter.namespace).Patch(ctx, name, types.MergePatchType, payload, metav1.PatchOptions{FieldManager: "ftc-platform-settings"}); err != nil {
		return platformsettings.ApplyStatus{Status: "failed", Targets: []string{name}, Message: "配置已保存，但滚动重启提交失败。"}, err
	}
	return platformsettings.ApplyStatus{Status: "restarting", Targets: []string{name}, Message: fmt.Sprintf("配置已保存，正在滚动重启 %s。", name)}, nil
}

func restartTargets(section string) (primary, fallback string, executable bool) {
	switch section {
	case platformsettings.SectionRuntime:
		return "ftc-platform", "", true
	case platformsettings.SectionCollection, platformsettings.SectionEvents:
		return "ftc-platform-collector", "ftc-platform", true
	case platformsettings.SectionSecurity:
		return "ftc-platform-worker", "ftc-platform", true
	case platformsettings.SectionJava, platformsettings.SectionDatabase:
		return "", "", false
	default:
		return "", "", false
	}
}
