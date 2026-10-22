package cluster

import (
	"context"
	"errors"
	"time"
)

var ErrRequestQuotaExceeded = errors.New("request quota exceeded")
var ErrRequestQuotaLeaseLost = errors.New("request quota lease lost")

// RequestQuotaRepository coordinates expensive requests across API replicas.
// Quota leases use database time and are released on request completion.
type RequestQuotaRepository interface {
	AcquireRequestQuota(context.Context, string, string, int, int, time.Duration) (string, error)
	RenewRequestQuota(context.Context, string, time.Duration) error
	ReleaseRequestQuota(context.Context, string) error
}
