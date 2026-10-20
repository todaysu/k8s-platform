package leadership

import (
	"context"
	"log"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

type Config struct {
	Enabled   bool
	Namespace string
	Name      string
	Identity  string
}

// Run executes work locally during development and behind a Kubernetes Lease
// when running in-cluster. work must block until its context is cancelled.
func Run(ctx context.Context, value Config, work func(context.Context)) {
	if !value.Enabled {
		work(ctx)
		return
	}
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		log.Printf("collector leadership: not running in Kubernetes, using local single-process mode: %v", err)
		work(ctx)
		return
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		log.Printf("collector leadership: create Kubernetes client: %v", err)
		return
	}
	identity := value.Identity
	if identity == "" {
		identity, _ = os.Hostname()
	}
	lock := &resourcelock.LeaseLock{
		LeaseMeta:  metav1.ObjectMeta{Name: value.Name, Namespace: value.Namespace},
		Client:     client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{Identity: identity},
	}
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   30 * time.Second,
		RenewDeadline:   20 * time.Second,
		RetryPeriod:     5 * time.Second,
		ReleaseOnCancel: true,
		Name:            value.Name,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leaderContext context.Context) {
				log.Printf("collector leadership: %s became leader", identity)
				work(leaderContext)
			},
			OnStoppedLeading: func() { log.Printf("collector leadership: %s stopped leading", identity) },
			OnNewLeader: func(newIdentity string) {
				if newIdentity != identity {
					log.Printf("collector leadership: %s is leader", newIdentity)
				}
			},
		},
	})
}
