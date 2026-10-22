package cluster

import (
	"context"
	"strings"
)

// SecurityBenchmark describes a kube-bench benchmark profile. IDs are the
// values accepted by kube-bench --benchmark and match its cfg directory names.
type SecurityBenchmark struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CISVersion string `json:"cis_version"`
	Group      string `json:"group"`
}

var securityBenchmarkProfiles = []SecurityBenchmark{
	{ID: "cis-1.24", Name: "CIS Benchmark v1.24.0", CISVersion: "1.24.0", Group: "CIS Benchmark"},
	{ID: "cis-1.23", Name: "CIS Benchmark v1.23.0", CISVersion: "1.23.0", Group: "CIS Benchmark"},
	{ID: "cis-1.20", Name: "CIS Benchmark v1.20.0", CISVersion: "1.20.0", Group: "CIS Benchmark"},
	{ID: "cis-1.12", Name: "CIS Benchmark v1.12.0", CISVersion: "1.12.0", Group: "CIS Benchmark"},
}

func SupportedSecurityBenchmarks() []SecurityBenchmark {
	return append([]SecurityBenchmark(nil), securityBenchmarkProfiles...)
}

// NormalizeSecurityBenchmark leaves an empty value as auto-detect, which lets
// kube-bench derive the profile from the cluster version. Explicit values must
// be one of the profiles shipped in the pinned kube-bench image.
func NormalizeSecurityBenchmark(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	for _, profile := range securityBenchmarkProfiles {
		if profile.ID == value {
			return value, nil
		}
	}
	return "", newError(CodeSecurityBenchmarkInvalid)
}

type securityBenchmarkContextKey struct{}

func WithSecurityBenchmark(ctx context.Context, benchmark string) context.Context {
	return context.WithValue(ctx, securityBenchmarkContextKey{}, strings.TrimSpace(benchmark))
}

func securityBenchmarkFromContext(ctx context.Context) string {
	value, _ := ctx.Value(securityBenchmarkContextKey{}).(string)
	return strings.TrimSpace(value)
}
