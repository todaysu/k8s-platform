package cluster

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type mergingIndexRepository struct {
	ResourceIndexRepository
	calls   atomic.Int32
	entered chan struct{}
	proceed chan struct{}
}

func (*mergingIndexRepository) CountClusterResources(context.Context) (int64, error) { return 1, nil }
func (r *mergingIndexRepository) SearchClusterResources(ctx context.Context, _ string, _ int) ([]GlobalResourceResult, error) {
	if r.calls.Add(1) == 1 {
		close(r.entered)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.proceed:
	}
	return []GlobalResourceResult{{Name: "java-api"}}, nil
}
func TestSearchCanceledCallerDoesNotCancelSharedLoad(t *testing.T) {
	r := &mergingIndexRepository{entered: make(chan struct{}), proceed: make(chan struct{})}
	service := &OnboardingService{}
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := service.cachedIndexSearch(ctx, r, "JAVA-API"); first <- err }()
	select {
	case <-r.entered:
	case <-time.After(time.Second):
		t.Fatal("query did not start")
	}
	cancel()
	if err := <-first; err != context.Canceled {
		t.Fatalf("expected canceled caller: %v", err)
	}
	close(r.proceed)
	result, err := service.cachedIndexSearch(context.Background(), r, " java-api ")
	if err != nil || len(result.Items) != 1 || r.calls.Load() != 1 {
		t.Fatalf("shared load lost: %v %+v calls=%d", err, result, r.calls.Load())
	}
}
