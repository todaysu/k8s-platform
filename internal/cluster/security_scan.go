package cluster

import (
	"context"
	"sort"
	"time"
)

const securityFindingSampleLimit = 8

type SecurityHostBenchmarkOutput struct {
	Node string
	Data []byte
}

type SecurityHostBenchmarkConnector interface {
	RunSecurityHostBenchmark(context.Context, ParsedKubeconfig) ([]SecurityHostBenchmarkOutput, error)
}

type SecurityScanCleanupConnector interface {
	CleanupSecurityScanResources(context.Context, ParsedKubeconfig) error
}

type SecurityBaselineSummary struct {
	Controls    int `json:"controls"`
	Passed      int `json:"passed"`
	Failed      int `json:"failed"`
	Warnings    int `json:"warnings"`
	Whitelisted int `json:"whitelisted"`
	Score       int `json:"score"`
}

type SecurityBaselineFinding struct {
	ControlID       string   `json:"control_id"`
	Status          string   `json:"status"`
	Severity        string   `json:"severity"`
	Category        string   `json:"category"`
	Title           string   `json:"title"`
	Affected        int      `json:"affected"`
	Resources       []string `json:"resources"`
	Evidence        string   `json:"evidence"`
	Remediation     string   `json:"remediation"`
	WhitelistReason string   `json:"whitelist_reason,omitempty"`
}

type SecurityBaselineCoverage struct {
	Included   []string `json:"included"`
	NotCovered []string `json:"not_covered"`
}

type SecurityScanProgress struct {
	Phase          string    `json:"phase"`
	FailedPhase    string    `json:"failed_phase,omitempty"`
	Message        string    `json:"message"`
	CompletedNodes int       `json:"completed_nodes"`
	TotalNodes     int       `json:"total_nodes"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SecurityBaselineReport struct {
	Mode             string                    `json:"mode"`
	Benchmark        string                    `json:"benchmark,omitempty"`
	ScannedAt        time.Time                 `json:"scanned_at"`
	Namespaces       int                       `json:"namespaces"`
	Pods             int                       `json:"pods"`
	TruncatedSamples bool                      `json:"truncated_samples"`
	Summary          SecurityBaselineSummary   `json:"summary"`
	Coverage         SecurityBaselineCoverage  `json:"coverage"`
	Findings         []SecurityBaselineFinding `json:"findings"`
	Progress         SecurityScanProgress      `json:"progress"`
}

type securityProgressReporter func(SecurityScanProgress)
type securityProgressContextKey struct{}

func withSecurityProgressReporter(ctx context.Context, reporter securityProgressReporter) context.Context {
	return context.WithValue(ctx, securityProgressContextKey{}, reporter)
}

func reportSecurityProgress(ctx context.Context, progress SecurityScanProgress) {
	if reporter, ok := ctx.Value(securityProgressContextKey{}).(securityProgressReporter); ok && reporter != nil {
		reporter(progress)
	}
}

func sortedSecurityKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (service *OnboardingService) GetFleetSecurityBaselines(ctx context.Context) (FleetSecurityBaseline, error) {
	records, err := service.repository.ListRecords(ctx)
	if err != nil {
		return FleetSecurityBaseline{}, err
	}
	scans := map[string]SecurityBaselineRecord{}
	if service.securityRepo != nil {
		scans, err = service.securityRepo.ListSecurityBaselines(ctx)
		if err != nil {
			return FleetSecurityBaseline{}, err
		}
	}
	staleAfter := service.securityScanTimeout() + 5*time.Minute
	now := service.now().UTC()
	for id, scan := range scans {
		if scan.Status != SecurityScanStatusQueued && scan.Status != SecurityScanStatusRunning {
			continue
		}
		if now.Sub(scan.StartedAt) <= staleAfter {
			continue
		}
		finished := scan.StartedAt.Add(staleAfter)
		scan.Status = SecurityScanStatusFailed
		scan.FinishedAt = &finished
		scan.Error = "扫描任务已超时或平台重启导致中断，请重新执行。"
		scans[id] = scan
	}
	result := FleetSecurityBaseline{Clusters: make([]ClusterSecurityBaseline, 0, len(records))}
	for _, record := range records {
		item := ClusterSecurityBaseline{ClusterID: record.ID, ClusterName: record.Name, Environment: record.Environment, TechnologyStack: record.TechnologyStack}
		if scan, ok := scans[record.ID]; ok {
			copy := scan
			if service.securityRepo != nil {
				entries, whitelistErr := service.securityRepo.ListSecurityWhitelist(ctx, record.ID)
				if whitelistErr == nil {
					applySecurityWhitelist(&copy.Report, entries)
				}
			}
			item.Scan = &copy
		}
		result.Clusters = append(result.Clusters, item)
	}
	sort.Slice(result.Clusters, func(i, j int) bool { return result.Clusters[i].ClusterName < result.Clusters[j].ClusterName })
	return result, nil
}
