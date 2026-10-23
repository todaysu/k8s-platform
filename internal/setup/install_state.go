package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

const installStateName = "ftc-platform-install-state"

// Only progress and public connection details are stored here. Database and
// administrator credentials remain in the runtime Secret or the caller's form.
func (server *Server) saveInstallState(ctx context.Context) error {
	if server.client == nil {
		return nil
	}
	status := server.installSnapshot()
	encoded, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		maps := server.client.CoreV1().ConfigMaps(server.config.Namespace)
		current, err := maps.Get(ctx, installStateName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = maps.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: installStateName, Namespace: server.config.Namespace}, Data: map[string]string{"status": string(encoded)}}, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		if current.Data == nil {
			current.Data = map[string]string{}
		}
		current.Data["status"] = string(encoded)
		_, err = maps.Update(ctx, current, metav1.UpdateOptions{})
		return err
	})
}

func (server *Server) loadInstallState(ctx context.Context) error {
	if server.client == nil {
		return nil
	}
	current, err := server.client.CoreV1().ConfigMaps(server.config.Namespace).Get(ctx, installStateName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var status InstallStatus
	if err := json.Unmarshal([]byte(current.Data["status"]), &status); err != nil {
		return fmt.Errorf("安装进度损坏：%w", err)
	}
	if status.State == "running" {
		lease, leaseErr := server.client.CoordinationV1().Leases(server.config.Namespace).Get(ctx, installLeaseName, metav1.GetOptions{})
		if leaseErr != nil && !apierrors.IsNotFound(leaseErr) {
			return leaseErr
		}
		active := leaseErr == nil && lease.Spec.HolderIdentity != nil && lease.Spec.RenewTime != nil && time.Since(lease.Spec.RenewTime.Time) < 45*time.Second
		if !active {
			status.State = "failed"
			status.Error = "初始化控制器中断；重新提交配置可从上次完成的资源继续安装。"
		}
	}
	server.installMu.Lock()
	server.install = status
	server.installMu.Unlock()
	return nil
}
