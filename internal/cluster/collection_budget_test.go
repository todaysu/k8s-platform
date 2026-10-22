package cluster

import (
	"context"
	"testing"
	"time"
)

type budgetTestRepository struct{ ClusterRepository }

func (*budgetTestRepository) AcquireRequestQuota(context.Context, string, string, int, int, time.Duration) (string, error) {
	return "test", nil
}
func (*budgetTestRepository) RenewRequestQuota(context.Context, string, time.Duration) error {
	return nil
}
func (*budgetTestRepository) ReleaseRequestQuota(context.Context, string) error { return nil }

func TestCollectionAdmissionDeadlineDoesNotCancelGrantedWork(t *testing.T) {
	parent, stop := context.WithCancel(WithCollectionLease(context.Background(), CollectionLease{Shard: 0, Count: 16, Owner: "test", Epoch: 1}))
	defer stop()
	queue, cancelQueue := context.WithCancel(parent)
	service := &OnboardingService{repository: &budgetTestRepository{}}
	work, release, err := service.collectionBudget(queue, parent, "cluster", "collection_list", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cancelQueue()
	if err := work.Err(); err != nil {
		t.Fatalf("queue cancellation escaped into granted work: %v", err)
	}
	stop()
	select {
	case <-work.Done():
	case <-time.After(time.Second):
		t.Fatal("owner cancellation did not stop granted work")
	}
}
