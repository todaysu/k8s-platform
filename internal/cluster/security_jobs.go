package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"example.com/k8s-platform/internal/metrics"
	"time"
)

// SecurityJob is a persisted execution with an unguessable attempt and owner token.
// Every write must be fenced by both tokens and an unexpired database lease.
type SecurityJob struct {
	Token  string
	Owner  string
	Record SecurityBaselineRecord
}

var securityJobsClaimed = metrics.Default.Counter("platform_security_jobs_claimed_total", "Persisted security jobs claimed by this process")
var securityJobErrors = metrics.Default.Counter("platform_security_job_storage_errors_total", "Security job claim or reaping failures")
var securityJobLeaseLost = metrics.Default.Counter("platform_security_job_lease_lost_total", "Security workers cancelled after heartbeat failure")
var securityJobsActive = metrics.Default.Gauge("platform_security_jobs_active", "Security jobs currently executing in this process")

var ErrSecurityJobLeaseLost = errors.New("security job lease lost")

type SecurityJobRepository interface {
	EnqueueSecurityJob(context.Context, SecurityBaselineRecord) error
	ClaimSecurityJob(context.Context, string, int, time.Duration) (*SecurityJob, error)
	RenewSecurityJob(context.Context, SecurityJob, time.Duration) error
	SaveSecurityJobRecord(context.Context, SecurityJob, SecurityBaselineRecord) error
	ExpireSecurityJobs(context.Context) error
}

// EnableQueuedSecurityScans is configured once before serving API requests.
func (service *OnboardingService) EnableQueuedSecurityScans() {
	_, service.securityQueued = service.repository.(SecurityJobRepository)
}

// RunSecurityWorker executes only persisted jobs. Workers can restart without
// losing queued requests; a shared database enforces the running-task budget.
func (service *OnboardingService) RunSecurityWorker(ctx context.Context, owner string) {
	repo, ok := service.repository.(SecurityJobRepository)
	if !ok {
		return
	}
	nonce, err := randomID()
	if err != nil {
		return
	}
	identity := sha256.Sum256([]byte(owner + ":" + nonce))
	owner = hex.EncodeToString(identity[:])
	workers := service.securityWorkers
	if workers <= 0 {
		workers = defaultSecurityScanWorkers
	}
	done := make(chan struct{}, workers)
	active := 0
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			for active > 0 {
				<-done
				active--
			}
			return
		}
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := repo.ExpireSecurityJobs(attempt)
		cancel()
		if err != nil && ctx.Err() == nil {
			securityJobErrors.Inc()
		}
		if err == nil && active < workers {
			attempt, cancel = context.WithTimeout(ctx, 5*time.Second)
			job, claimErr := repo.ClaimSecurityJob(attempt, owner, workers, 30*time.Second)
			cancel()
			if claimErr != nil && ctx.Err() == nil {
				securityJobErrors.Inc()
			}
			if claimErr == nil && job != nil {
				active++
				securityJobsClaimed.Inc()
				securityJobsActive.Add(1)
				go func() {
					defer func() { securityJobsActive.Add(-1); done <- struct{}{} }()
					service.runClaimedSecurityJob(ctx, repo, *job)
				}()
				continue
			}
		}
		select {
		case <-ctx.Done():
		case <-done:
			active--
		case <-ticker.C:
		}
	}
}

func (service *OnboardingService) runClaimedSecurityJob(parent context.Context, repo SecurityJobRepository, job SecurityJob) {
	ctx, cancel := context.WithTimeout(parent, service.securityScanTimeout())
	defer cancel()
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				renewal, stop := context.WithTimeout(ctx, 3*time.Second)
				err := repo.RenewSecurityJob(renewal, job, 30*time.Second)
				stop()
				if err != nil {
					securityJobLeaseLost.Inc()
					cancel()
					return
				}
			}
		}
	}()
	// Each execution has its own service state and a fenced persistence adapter.
	execution := NewOnboardingService(service.connector, service.cipher, service.repository)
	execution.securityRepo = &ownedSecurityRepository{SecurityBaselineRepository: service.securityRepo, jobs: repo, job: job}
	execution.SetSecurityScanTimeout(service.securityScanTimeout())
	job.Record.Status = SecurityScanStatusRunning
	execution.executeSecurityBaselineScan(ctx, job.Record.ClusterID, SecurityScanModeFull, job.Record)
	cancel()
	<-heartbeatDone
	// If execution returned without a terminal write, fail this owned attempt.
	// A completed or expired attempt rejects this write, preserving its result.
	failed := job.Record
	failed.Status = SecurityScanStatusFailed
	now := time.Now().UTC()
	failed.FinishedAt = &now
	failed.Error = "扫描执行已结束或被取消；请检查 Worker 和目标集群后重新提交。"
	failed.Report.Progress.Phase = "failed"
	finalCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_ = repo.SaveSecurityJobRecord(finalCtx, job, failed)
}

type ownedSecurityRepository struct {
	SecurityBaselineRepository
	jobs SecurityJobRepository
	job  SecurityJob
}

func (repo *ownedSecurityRepository) SaveSecurityBaseline(ctx context.Context, value SecurityBaselineRecord) error {
	return repo.jobs.SaveSecurityJobRecord(ctx, repo.job, value)
}

// The fenced terminal write includes history in the same transaction.
func (repo *ownedSecurityRepository) AppendSecurityBaselineHistory(context.Context, SecurityBaselineRecord) error {
	return nil
}
