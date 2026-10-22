package cluster

import (
	"context"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// SecurityScanMode is retained in the service boundary for API compatibility.
// V1 intentionally exposes one implementation: a kube-bench CIS node scan.
type SecurityScanMode string

const (
	SecurityScanModeFull SecurityScanMode = "full"
)

const (
	SecurityScanStatusQueued    = "queued"
	SecurityScanStatusRunning   = "running"
	SecurityScanStatusCompleted = "completed"
	SecurityScanStatusFailed    = "failed"
)

const (
	defaultSecurityScanWorkers = 2
	defaultSecurityScanTimeout = 15 * time.Minute
)

// NormalizeSecurityScanMode rejects the removed API and Kubescape modes so an
// old client cannot silently run a different scan than the operator expects.
func NormalizeSecurityScanMode(value string) (SecurityScanMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "full", "cis", "host", "kube-bench":
		return SecurityScanModeFull, nil
	}
	return "", newError(CodeSecurityScanModeInvalid)
}

// SetSecurityScanWorkers limits how many manually triggered scans may talk to
// target clusters at the same time. Call it during startup, before scans run.
func (service *OnboardingService) SetSecurityScanWorkers(workers int) {
	if workers <= 0 {
		return
	}
	service.securityMu.Lock()
	defer service.securityMu.Unlock()
	service.securityWorkers = workers
	service.securitySlots = make(chan struct{}, workers)
}

func (service *OnboardingService) SetSecurityScanTimeout(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	service.securityMu.Lock()
	defer service.securityMu.Unlock()
	service.securityTimeout = timeout
}

func (service *OnboardingService) ensureSecurityState() chan struct{} {
	service.securityMu.Lock()
	defer service.securityMu.Unlock()
	if service.securityWorkers <= 0 {
		service.securityWorkers = defaultSecurityScanWorkers
	}
	if service.securitySlots == nil {
		service.securitySlots = make(chan struct{}, service.securityWorkers)
	}
	if service.securityActive == nil {
		service.securityActive = make(map[string]struct{})
	}
	if service.securityTimeout <= 0 {
		service.securityTimeout = defaultSecurityScanTimeout
	}
	return service.securitySlots
}

// StartSecurityBaselineScan validates the request, records a queued scan and
// starts it in the background. The HTTP handler can return immediately; the
// fleet endpoint exposes the queued/running/completed state for polling.
func (service *OnboardingService) StartSecurityBaselineScan(ctx context.Context, id string, mode SecurityScanMode) (SecurityBaselineRecord, error) {
	normalized, err := NormalizeSecurityScanMode(string(mode))
	if err != nil {
		return SecurityBaselineRecord{}, err
	}
	if service.securityRepo == nil {
		return SecurityBaselineRecord{}, newError(CodePersistenceFailed)
	}
	if !service.supportsSecurityScan(normalized) {
		return SecurityBaselineRecord{}, newError(CodeSecurityScanUnavailable)
	}
	if _, err := service.repository.GetRecord(ctx, id); err != nil {
		return SecurityBaselineRecord{}, err
	}

	slots := service.ensureSecurityState()
	service.securityMu.Lock()
	if _, exists := service.securityActive[id]; exists {
		service.securityMu.Unlock()
		return SecurityBaselineRecord{}, newError(CodeSecurityScanInProgress)
	}
	service.securityActive[id] = struct{}{}
	service.securityMu.Unlock()

	benchmark, err := NormalizeSecurityBenchmark(securityBenchmarkFromContext(ctx))
	if err != nil {
		service.releaseSecurityScan(id)
		return SecurityBaselineRecord{}, err
	}
	record := SecurityBaselineRecord{
		ClusterID: id,
		Status:    SecurityScanStatusQueued,
		Scanner:   securityScannerName(normalized),
		Framework: securityFrameworkName(normalized),
		StartedAt: service.now().UTC(),
		Report: SecurityBaselineReport{Benchmark: benchmark, Progress: SecurityScanProgress{
			Phase: "queued", Message: "扫描任务已提交，正在等待执行槽位。", UpdatedAt: service.now().UTC(),
		}},
	}
	if service.securityQueued {
		defer service.releaseSecurityScan(id)
		repo, ok := service.repository.(SecurityJobRepository)
		if !ok {
			return SecurityBaselineRecord{}, newError(CodeSecurityScanUnavailable)
		}
		if err := repo.EnqueueSecurityJob(ctx, record); err != nil {
			return SecurityBaselineRecord{}, err
		}
		return record, nil
	}
	if err := service.securityRepo.SaveSecurityBaseline(ctx, record); err != nil {
		service.releaseSecurityScan(id)
		return SecurityBaselineRecord{}, err
	}

	service.securityScans.Add(1)
	go func() {
		defer service.securityScans.Done()
		defer service.releaseSecurityScan(id)
		service.runSecurityBaselineScan(id, normalized, record, slots)
	}()
	return record, nil
}

// WaitSecurityScans waits for all background scans. It is used by tests and
// by shutdown paths that want to let persisted state settle.
func (service *OnboardingService) WaitSecurityScans() {
	service.securityScans.Wait()
}

// RecoverInterruptedSecurityScans is a legacy single-process recovery adapter.
// Queued production scans use lease expiry instead; never call this on API startup.
// Background goroutines cannot survive a process restart, so any persisted
// queued/running row is no longer backed by a real worker. Mark it failed
// immediately and best-effort remove temporary resources left in the target
// cluster. Production deployments must keep the platform at one replica until
// scan ownership and heartbeats are stored in the database.
func (service *OnboardingService) RecoverInterruptedSecurityScans(ctx context.Context) {
	if service.securityRepo == nil {
		return
	}
	scans, err := service.securityRepo.ListSecurityBaselines(ctx)
	if err != nil {
		return
	}
	records, err := service.repository.ListRecords(ctx)
	if err != nil {
		return
	}
	recordByID := make(map[string]ClusterRecord, len(records))
	for _, record := range records {
		recordByID[record.ID] = record
	}
	cleaner, canClean := service.connector.(SecurityScanCleanupConnector)
	for id, scan := range scans {
		if scan.Status != SecurityScanStatusQueued && scan.Status != SecurityScanStatusRunning {
			continue
		}
		finished := service.now().UTC()
		scan.Status = SecurityScanStatusFailed
		scan.FinishedAt = &finished
		scan.Error = "平台重启导致扫描任务中断，临时资源已安排清理，请重新执行。"
		service.persistSecurityRecord(scan)
		if !canClean {
			continue
		}
		record, ok := recordByID[id]
		if !ok {
			continue
		}
		parsed, parseErr := service.parsedCredentials(record)
		if parseErr != nil {
			continue
		}
		go func(credentials ParsedKubeconfig) {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_ = cleaner.CleanupSecurityScanResources(cleanupCtx, credentials)
		}(parsed)
	}
}

func (service *OnboardingService) releaseSecurityScan(id string) {
	service.securityMu.Lock()
	delete(service.securityActive, id)
	service.securityMu.Unlock()
}

func (service *OnboardingService) supportsSecurityScan(mode SecurityScanMode) bool {
	_, ok := service.connector.(SecurityHostBenchmarkConnector)
	return mode == SecurityScanModeFull && ok
}

func (service *OnboardingService) securityScanTimeout() time.Duration {
	service.securityMu.Lock()
	defer service.securityMu.Unlock()
	if service.securityTimeout <= 0 {
		return defaultSecurityScanTimeout
	}
	return service.securityTimeout
}

func (service *OnboardingService) runSecurityBaselineScan(id string, mode SecurityScanMode, record SecurityBaselineRecord, slots chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), service.securityScanTimeout())
	defer cancel()

	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		service.markSecurityScanFailed(record, mode)
		return
	}
	service.executeSecurityBaselineScan(ctx, id, mode, record)
}

func (service *OnboardingService) executeSecurityBaselineScan(ctx context.Context, id string, mode SecurityScanMode, record SecurityBaselineRecord) {
	var progressMu sync.Mutex
	updateProgress := func(progress SecurityScanProgress) {
		progressMu.Lock()
		if progress.TotalNodes == 0 {
			progress.TotalNodes = record.Report.Progress.TotalNodes
		}
		if progress.CompletedNodes == 0 && record.Report.Progress.CompletedNodes > 0 {
			progress.CompletedNodes = record.Report.Progress.CompletedNodes
		}
		progress.UpdatedAt = service.now().UTC()
		record.Report.Progress = progress
		snapshot := record
		progressMu.Unlock()
		service.persistSecurityProgress(snapshot)
	}
	updateProgress(SecurityScanProgress{Phase: "connecting", Message: "正在读取集群凭据并连接 Kubernetes API。"})
	clusterRecord, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		progressMu.Lock()
		snapshot := record
		progressMu.Unlock()
		service.markSecurityScanFailedWithError(snapshot, mode, err)
		return
	}
	parsed, err := service.parsedCredentials(clusterRecord)
	if err != nil {
		progressMu.Lock()
		snapshot := record
		progressMu.Unlock()
		service.markSecurityScanFailedWithError(snapshot, mode, err)
		return
	}

	record.Status = SecurityScanStatusRunning
	record.Report.Progress = SecurityScanProgress{Phase: "discovering_nodes", Message: "连接成功，正在发现可扫描节点。", UpdatedAt: service.now().UTC()}
	if err := service.securityRepo.SaveSecurityBaseline(ctx, record); err != nil {
		return
	}

	scanner, ok := service.connector.(SecurityHostBenchmarkConnector)
	if !ok {
		service.markSecurityScanFailed(record, mode)
		return
	}
	ctx = withSecurityProgressReporter(ctx, updateProgress)
	ctx = WithSecurityBenchmark(ctx, record.Report.Benchmark)
	outputs, err := scanner.RunSecurityHostBenchmark(ctx, parsed)
	var report SecurityBaselineReport
	if err == nil {
		updateProgress(SecurityScanProgress{Phase: "collecting_report", Message: "节点扫描完成，正在汇总 kube-bench 报告。"})
		report, err = parseKubeBenchReports(outputs)
		if err == nil {
			report.Benchmark = record.Report.Benchmark
			if clusterScanner, ok := service.connector.(SecurityClusterScanConnector); ok {
				updateProgress(SecurityScanProgress{Phase: "collecting_report", Message: "节点扫描完成，正在执行集群侧只读 API 扫描。"})
				clusterReport, clusterErr := clusterScanner.RunSecurityClusterScan(ctx, parsed)
				if clusterErr == nil {
					report = mergeSecurityReports(report, clusterReport)
					report.Mode = "job-kube-bench+cluster-api"
				} else {
					report.Coverage.NotCovered = append(report.Coverage.NotCovered, "集群侧 API 扫描失败："+safeSecurityError(clusterErr))
					report.Findings = append(report.Findings, SecurityBaselineFinding{
						ControlID: "CLUSTER-000", Status: "WARN", Severity: "medium", Category: "集群侧配置",
						Title: "集群侧只读 API 扫描未完成", Affected: 1,
						Evidence: safeSecurityError(clusterErr), Remediation: "检查平台凭据是否具备 pods、services、namespaces、clusterroles、networkpolicies 的只读权限。",
					})
					recomputeSecuritySummary(&report)
				}
				report.Benchmark = record.Report.Benchmark
			}
		}
	}
	if err != nil {
		progressMu.Lock()
		snapshot := record
		progressMu.Unlock()
		service.markSecurityScanFailedWithError(snapshot, mode, err)
		return
	}

	finished := service.now().UTC()
	progressMu.Lock()
	record.Status = SecurityScanStatusCompleted
	record.FinishedAt = &finished
	report.Progress = record.Report.Progress
	report.Progress.Phase = "completed"
	report.Progress.Message = "报告已生成，临时 Job 和 Pod 已回收。"
	report.Progress.CompletedNodes = report.Progress.TotalNodes
	report.Progress.UpdatedAt = finished
	record.Report = report
	record.Error = ""
	progressMu.Unlock()
	service.persistSecurityRecord(record)
}

func (service *OnboardingService) markSecurityScanFailed(record SecurityBaselineRecord, mode SecurityScanMode) {
	service.markSecurityScanFailedWithError(record, mode, nil)
}

func (service *OnboardingService) markSecurityScanFailedWithError(record SecurityBaselineRecord, mode SecurityScanMode, cause error) {
	finished := service.now().UTC()
	record.Status = SecurityScanStatusFailed
	record.FinishedAt = &finished
	record.Report.Progress.FailedPhase = record.Report.Progress.Phase
	record.Report.Progress.Phase = "failed"
	record.Report.Progress.Message = "扫描已停止，平台已开始回收临时 Job 和 Pod。"
	record.Report.Progress.UpdatedAt = finished
	record.Error = "CIS 扫描失败，请检查 kube-bench 镜像、节点调度、hostPID/hostPath 策略与临时 Job 权限。"
	if cause != nil {
		record.Error += " 原因：" + safeSecurityError(cause)
	}
	service.persistSecurityRecord(record)
}

func (service *OnboardingService) persistSecurityProgress(record SecurityBaselineRecord) {
	if service.securityRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = service.securityRepo.SaveSecurityBaseline(ctx, record)
}

// safeSecurityError returns a report-safe description of a scan failure.
//
// The raw client-go error text must never reach the report, the persisted
// history row or an API response: it embeds the target API Server address, the
// requested path, object names and the platform ServiceAccount identity. Only
// the platform error code is surfaced; the underlying text stays in this
// process's log where operators can still correlate it.
func safeSecurityError(err error) string {
	if err == nil {
		return "未知原因。"
	}
	code := ErrorCode(err)
	log.Printf("security scan detail: code=%s error=%v", code, err)
	switch code {
	case CodeConnectionFailed:
		return "无法连接集群 API Server。"
	case CodeConnectionTimeout:
		return "连接集群 API Server 超时。"
	case CodeTLSVerificationFailed:
		return "API Server 的 TLS 证书校验失败。"
	case CodeAuthenticationFailed:
		return "平台凭据认证失败。"
	case CodeAuthorizationFailed:
		return "平台凭据没有读取集群资源的权限。"
	case CodeCollectionLimitExceeded:
		return "集群对象数量超过采集上限，本轮扫描未完成。"
	default:
		return "扫描失败，详情请查看平台日志。"
	}
}

func (service *OnboardingService) persistSecurityRecord(record SecurityBaselineRecord) {
	if service.securityRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if service.securityRepo.SaveSecurityBaseline(ctx, record) == nil && (record.Status == SecurityScanStatusCompleted || record.Status == SecurityScanStatusFailed) {
		_ = service.securityRepo.AppendSecurityBaselineHistory(ctx, record)
	}
}

func securityScannerName(mode SecurityScanMode) string {
	return "kube-bench"
}

func securityFrameworkName(mode SecurityScanMode) string {
	return "cis"
}

func (service *OnboardingService) GetSecurityBaselineHistory(ctx context.Context, clusterID string, limit int) (SecurityBaselineHistory, error) {
	if service.securityRepo == nil {
		return SecurityBaselineHistory{}, newError(CodePersistenceFailed)
	}
	if _, err := service.repository.GetRecord(ctx, clusterID); err != nil {
		return SecurityBaselineHistory{}, err
	}
	records, err := service.securityRepo.ListSecurityBaselineHistory(ctx, clusterID, limit)
	if err != nil {
		return SecurityBaselineHistory{}, err
	}
	if entries, whitelistErr := service.securityRepo.ListSecurityWhitelist(ctx, clusterID); whitelistErr == nil {
		for index := range records {
			applySecurityWhitelist(&records[index].Report, entries)
		}
	}
	result := SecurityBaselineHistory{Records: records}
	completed := make([]SecurityBaselineRecord, 0, 2)
	for _, record := range records {
		if record.Status == SecurityScanStatusCompleted {
			completed = append(completed, record)
			if len(completed) == 2 {
				break
			}
		}
	}
	if len(completed) == 2 {
		result.Comparison = compareSecurityReports(completed[1], completed[0])
	}
	return result, nil
}

func (service *OnboardingService) GetSecurityWhitelist(ctx context.Context, clusterID string) ([]SecurityWhitelistEntry, error) {
	if service.securityRepo == nil {
		return nil, newError(CodePersistenceFailed)
	}
	if _, err := service.repository.GetRecord(ctx, clusterID); err != nil {
		return nil, err
	}
	entries, err := service.securityRepo.ListSecurityWhitelist(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []SecurityWhitelistEntry{}
	}
	return entries, nil
}

func (service *OnboardingService) AddSecurityWhitelist(ctx context.Context, clusterID, controlID, reason string) (SecurityWhitelistEntry, error) {
	if service.securityRepo == nil {
		return SecurityWhitelistEntry{}, newError(CodePersistenceFailed)
	}
	controlID = strings.TrimSpace(controlID)
	reason = strings.TrimSpace(reason)
	if !securityControlIDPattern.MatchString(controlID) || len(reason) < 3 || len(reason) > 512 {
		return SecurityWhitelistEntry{}, newError(CodeSecurityWhitelistInvalid)
	}
	if _, err := service.repository.GetRecord(ctx, clusterID); err != nil {
		return SecurityWhitelistEntry{}, err
	}
	entry := SecurityWhitelistEntry{ClusterID: clusterID, ControlID: controlID, Reason: reason, CreatedAt: service.now().UTC()}
	if err := service.securityRepo.AddSecurityWhitelist(ctx, entry); err != nil {
		return SecurityWhitelistEntry{}, err
	}
	return entry, nil
}

func (service *OnboardingService) DeleteSecurityWhitelist(ctx context.Context, clusterID, controlID string) error {
	if service.securityRepo == nil {
		return newError(CodePersistenceFailed)
	}
	if _, err := service.repository.GetRecord(ctx, clusterID); err != nil {
		return err
	}
	return service.securityRepo.DeleteSecurityWhitelist(ctx, clusterID, controlID)
}

func applySecurityWhitelist(report *SecurityBaselineReport, entries []SecurityWhitelistEntry) {
	if report == nil || len(entries) == 0 {
		return
	}
	reasons := make(map[string]string, len(entries))
	for _, entry := range entries {
		reasons[entry.ControlID] = entry.Reason
	}
	for index := range report.Findings {
		if reason, ok := reasons[report.Findings[index].ControlID]; ok && report.Findings[index].Status != "PASS" {
			report.Findings[index].Status = "WHITELISTED"
			report.Findings[index].WhitelistReason = reason
		}
	}
	recomputeSecuritySummary(report)
}

func compareSecurityReports(previous, current SecurityBaselineRecord) SecurityBaselineComparison {
	result := SecurityBaselineComparison{PreviousScanID: previous.ID, CurrentScanID: current.ID, NewRisks: []SecurityBaselineChange{}, Resolved: []SecurityBaselineChange{}, Changed: []SecurityBaselineChange{}, Remediation: []SecurityBaselineRemediation{}}
	previousByID := make(map[string]SecurityBaselineFinding, len(previous.Report.Findings))
	currentByID := make(map[string]SecurityBaselineFinding, len(current.Report.Findings))
	for _, finding := range previous.Report.Findings {
		previousByID[finding.ControlID] = finding
	}
	for _, finding := range current.Report.Findings {
		currentByID[finding.ControlID] = finding
	}
	keys := make(map[string]struct{}, len(previousByID)+len(currentByID))
	for id := range previousByID {
		keys[id] = struct{}{}
	}
	for id := range currentByID {
		keys[id] = struct{}{}
	}
	ids := make([]string, 0, len(keys))
	for id := range keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		before, beforeOK := previousByID[id]
		after, afterOK := currentByID[id]
		beforeRisk := beforeOK && before.Status != "PASS" && before.Status != "WHITELISTED"
		afterRisk := afterOK && after.Status != "PASS" && after.Status != "WHITELISTED"
		title := after.Title
		if title == "" {
			title = before.Title
		}
		change := SecurityBaselineChange{ControlID: id, Title: title}
		if beforeOK {
			change.PreviousStatus = before.Status
		}
		if afterOK {
			change.CurrentStatus = after.Status
		}
		remediation := SecurityBaselineRemediation{ControlID: id, Title: title, PreviousStatus: change.PreviousStatus, CurrentStatus: change.CurrentStatus}
		switch {
		case !beforeRisk && afterRisk:
			result.NewRisks = append(result.NewRisks, change)
			remediation.Result = "new_risk"
			result.Remediation = append(result.Remediation, remediation)
		case beforeRisk && afterOK && after.Status == "PASS":
			result.Resolved = append(result.Resolved, change)
			remediation.Result = "remediated"
			result.Remediation = append(result.Remediation, remediation)
		case beforeRisk && afterOK && after.Status == "WHITELISTED":
			remediation.Result = "whitelisted"
			result.Remediation = append(result.Remediation, remediation)
		case beforeRisk && afterRisk:
			remediation.Result = "not_remediated"
			if before.Status != after.Status {
				result.Changed = append(result.Changed, change)
			}
			result.Remediation = append(result.Remediation, remediation)
		case beforeRisk && !afterOK:
			change.CurrentStatus = "NOT_RUN"
			remediation.CurrentStatus = "NOT_RUN"
			remediation.Result = "not_run"
			result.Changed = append(result.Changed, change)
			result.Remediation = append(result.Remediation, remediation)
		}
	}
	return result
}

var securityControlIDPattern = regexp.MustCompile(`^[A-Za-z]+-[0-9]+(?:\.[0-9]+)*$`)
