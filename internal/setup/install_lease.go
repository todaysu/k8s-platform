package setup

import (
	"context"
	"fmt"
	"os"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

const installLeaseName = "ftc-platform-install"

func (server *Server) acquireInstallLease(ctx context.Context) (func(), <-chan struct{}, error) {
	if server.client == nil {
		return func() {}, make(chan struct{}), nil
	}
	holder := os.Getenv("POD_NAME")
	if holder == "" {
		holder = "setup"
	}
	holder = fmt.Sprintf("%s-%d", holder, time.Now().UnixNano())
	leases := server.client.CoordinationV1().Leases(server.config.Namespace)
	duration := int32(45)
	claim := func() error {
		return retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current, err := leases.Get(ctx, installLeaseName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				now := metav1.MicroTime{Time: time.Now().UTC()}
				_, err = leases.Create(ctx, &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: installLeaseName, Namespace: server.config.Namespace}, Spec: coordinationv1.LeaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &duration, RenewTime: &now}}, metav1.CreateOptions{})
				return err
			}
			if err != nil {
				return err
			}
			if current.Spec.HolderIdentity != nil && *current.Spec.HolderIdentity != holder && current.Spec.RenewTime != nil && time.Since(current.Spec.RenewTime.Time) < 45*time.Second {
				return fmt.Errorf("另一个初始化实例正在执行安装")
			}
			now := metav1.MicroTime{Time: time.Now().UTC()}
			current.Spec.HolderIdentity, current.Spec.LeaseDurationSeconds, current.Spec.RenewTime = &holder, &duration, &now
			_, err = leases.Update(ctx, current, metav1.UpdateOptions{})
			return err
		})
	}
	if err := claim(); err != nil {
		return nil, nil, err
	}
	stop := make(chan struct{})
	lost := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				beat, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					current, err := leases.Get(beat, installLeaseName, metav1.GetOptions{})
					if err != nil {
						return err
					}
					if current.Spec.HolderIdentity == nil || *current.Spec.HolderIdentity != holder {
						return fmt.Errorf("安装租约已被其他实例接管")
					}
					now := metav1.MicroTime{Time: time.Now().UTC()}
					current.Spec.RenewTime = &now
					_, err = leases.Update(beat, current, metav1.UpdateOptions{})
					return err
				})
				cancel()
				if err != nil {
					close(lost)
					return
				}
			}
		}
	}()
	return func() {
		close(stop)
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current, err := leases.Get(cleanup, installLeaseName, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if current.Spec.HolderIdentity == nil || *current.Spec.HolderIdentity != holder {
				return nil
			}
			current.Spec.HolderIdentity = nil
			_, err = leases.Update(cleanup, current, metav1.UpdateOptions{})
			return err
		})
	}, lost, nil
}
