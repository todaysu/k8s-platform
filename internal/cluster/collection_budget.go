package cluster

import (
	"context"
	"time"
)

// Bound large collection jobs across all shards and replicas. Waiting workers
// stay within their collector deadline; they do not enqueue new copies.
func (service *OnboardingService) collectionBudget(ctx, executionParent context.Context, id, lane string, total int) (context.Context, func(), error) {
	if _, scoped := CollectionLeaseFromContext(executionParent); !scoped {
		return executionParent, func() {}, nil
	}
	repo, ok := service.repository.(RequestQuotaRepository)
	if !ok {
		return nil, nil, newError(CodePersistenceFailed)
	}
	service.collectionAdmissionMu.Lock()
	if service.collectionAdmissions == nil {
		service.collectionAdmissions = map[string]chan struct{}{}
	}
	local := service.collectionAdmissions[lane]
	if local == nil {
		local = make(chan struct{}, total)
		service.collectionAdmissions[lane] = local
	}
	service.collectionAdmissionMu.Unlock()
	select {
	case local <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	held := true
	defer func() {
		if held {
			<-local
		}
	}()
	var token string
	for {
		attempt, stop := context.WithTimeout(ctx, 3*time.Second)
		var err error
		token, err = repo.AcquireRequestQuota(attempt, lane, id, total, 1, 15*time.Second)
		stop()
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		// Admission remains closed during transient DB pressure; retry only
		// inside the bounded queue deadline, without issuing remote work.
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
	work, cancel := context.WithCancel(executionParent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-ticker.C:
				attempt, stop := context.WithTimeout(work, 5*time.Second)
				err := repo.RenewRequestQuota(attempt, token, 15*time.Second)
				stop()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	held = false
	return work, func() {
		defer func() { <-local }()
		cancel()
		<-done
		release, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = repo.ReleaseRequestQuota(release, token)
	}, nil
}
