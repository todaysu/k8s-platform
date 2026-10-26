package reports

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

type Source interface {
	ListRecords(context.Context) ([]cluster.ClusterRecord, error)
	ListFleetCapacitySnapshots(context.Context) (cluster.FleetCapacity, error)
}

type Service struct {
	db, events          *sql.DB
	sqlite, eventSQLite bool
	source              Source
	cipher              cluster.CredentialCipher
	slots               chan struct{}
	jobSignal           chan struct{}
	workerOnce          sync.Once
}

func New(ctx context.Context, db, events *sql.DB, sqlite, eventSQLite bool, source Source, cipher cluster.CredentialCipher) (*Service, error) {
	service := &Service{db: db, events: events, sqlite: sqlite, eventSQLite: eventSQLite, source: source, cipher: cipher, slots: make(chan struct{}, 1), jobSignal: make(chan struct{}, 1)}
	for _, query := range []string{
		`CREATE TABLE IF NOT EXISTS operational_reports(id VARCHAR(32) PRIMARY KEY,title VARCHAR(255) NOT NULL,period VARCHAR(16) NOT NULL,environment VARCHAR(64) NOT NULL,cluster_type VARCHAR(32) NOT NULL,generated_at VARCHAR(40) NOT NULL,ai_status VARCHAR(16) NOT NULL,payload LONGTEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS report_model_config(id INTEGER PRIMARY KEY,endpoint VARCHAR(1024) NOT NULL,model VARCHAR(255) NOT NULL,encrypted_key TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS operational_report_jobs(id VARCHAR(32) PRIMARY KEY,status VARCHAR(16) NOT NULL,stage VARCHAR(32) NOT NULL,message VARCHAR(512) NOT NULL,error_message VARCHAR(1024) NOT NULL,report_id VARCHAR(32) NOT NULL,request_json LONGTEXT NOT NULL,created_at VARCHAR(40) NOT NULL,started_at VARCHAR(40) NOT NULL,finished_at VARCHAR(40) NOT NULL)`,
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			return nil, fmt.Errorf("initialize reports: %w", err)
		}
	}
	return service, nil
}

func (s *Service) List(ctx context.Context) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,title,period,environment,cluster_type,generated_at,ai_status FROM operational_reports ORDER BY generated_at DESC,id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		var item Item
		var at string
		if err := rows.Scan(&item.ID, &item.Title, &item.Period, &item.Environment, &item.ClusterType, &at, &item.AIStatus); err != nil {
			return nil, err
		}
		item.GeneratedAt, err = time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, err
		}
		item.URL = reportURL(item.ID)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) Get(ctx context.Context, id string) (Report, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM operational_reports WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Report{}, ErrNotFound
	}
	if err != nil {
		return Report{}, err
	}
	var report Report
	err = json.Unmarshal([]byte(raw), &report)
	return report, err
}

// Delete removes one saved report. Deleting an id that does not exist is a
// no-op only if nothing was affected — otherwise the caller can rely on
// ErrNotFound to distinguish an already-removed record from a storage failure.
func (s *Service) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM operational_reports WHERE id=?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) Generate(ctx context.Context, request Request, now time.Time) (Report, error) {
	return s.generateWithProgress(ctx, request, now, nil)
}

func (s *Service) generateWithProgress(ctx context.Context, request Request, now time.Time, progress func(string, string)) (Report, error) {
	window, err := PeriodWindow(request.Period, request.Date, now)
	if err != nil {
		return Report{}, err
	}
	if len(request.Environment) > 64 || len(request.ClusterType) > 32 {
		return Report{}, errors.New("筛选范围无效")
	}
	if request.ClusterType != "" && request.ClusterType != "G01" && request.ClusterType != "OCP" && request.ClusterType != "未标记" {
		return Report{}, errors.New("资源池类型无效")
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return Report{}, ErrBusy
	}
	if progress != nil {
		progress("collecting", "正在汇总已保存的告警、Event、容量与安全数据")
	}
	evidence, err := s.collect(ctx, request, window, now)
	if err != nil {
		return Report{}, err
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return Report{}, err
	}
	title := window.Start.Format("2006.01.02") + " — " + window.End.AddDate(0, 0, -1).Format("01.02") + " 集群运行" + window.Label
	report := Report{ID: hex.EncodeToString(id), Title: title, Request: request, Window: window, GeneratedAt: now, AIStatus: "disabled", Evidence: evidence, Analysis: ruleAnalysis(evidence)}
	report.URL = reportURL(report.ID)
	if request.UseAI {
		if progress != nil {
			progress("analyzing", "事实数据已完成，正在调用 AI 报告模型")
		}
		config, encrypted, err := s.loadModelConfig(ctx)
		if err != nil {
			return Report{}, err
		}
		if config.Endpoint == "" || config.Model == "" {
			report.AIStatus = "unconfigured"
			report.AIMessage = "尚未配置模型，已保留事实图表与规则建议。"
		} else {
			report.Model = config.Model
			analysis, err := s.analyze(ctx, config, encrypted, evidence, window)
			if err != nil {
				report.AIStatus = "failed"
				report.AIMessage = ModelFailureMessage(err)
			} else {
				report.AIStatus = "completed"
				report.Analysis = analysis
			}
		}
	}
	if progress != nil {
		progress("saving", "分析已完成，正在保存 HTML 报告")
	}
	payload, err := json.Marshal(report)
	if err != nil {
		return Report{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO operational_reports(id,title,period,environment,cluster_type,generated_at,ai_status,payload) VALUES(?,?,?,?,?,?,?,?)`, report.ID, report.Title, request.Period, request.Environment, request.ClusterType, now.UTC().Format(time.RFC3339Nano), report.AIStatus, string(payload))
	return report, err
}

func (s *Service) collect(ctx context.Context, request Request, window Window, now time.Time) (Evidence, error) {
	e := Evidence{Clusters: []ClusterSummary{}, Trend: []Point{}, Reasons: []Reason{}, Pools: []Pool{}, Notes: []string{
		"事件按增量发生次数统计；同一事件多次重试会累计，不等于故障数量或根因数量。",
		"无事件记录不能证明无故障；趋势中的空白日期代表没有保留记录，采集状态和保留周期都会影响覆盖。",
		"健康状态与资源池容量为生成时最新快照，不代表报告周期内的可用率或容量变化。",
		"集群范围依据生成时纳管清单；已移除集群的历史事件未纳入。",
	}}
	if window.End.After(now) {
		e.Notes = append(e.Notes, "所选周期尚未结束，本报告统计截至生成时间。")
	}
	records, err := s.source.ListRecords(ctx)
	if err != nil {
		return e, errors.New("读取集群清单失败")
	}
	ids := []string{}
	byID := map[string]int{}
	for _, r := range records {
		kind := r.ClusterType
		if kind == "" {
			kind = "未标记"
		}
		if request.Environment != "" && r.Environment != request.Environment {
			continue
		}
		if request.ClusterType != "" && kind != request.ClusterType {
			continue
		}
		byID[r.ID] = len(e.Clusters)
		ids = append(ids, r.ID)
		e.Clusters = append(e.Clusters, ClusterSummary{ID: r.ID, Name: r.Name, Environment: r.Environment, ClusterType: kind, TechnologyStack: r.TechnologyStack, Status: r.ConnectionStatus, CheckedAt: r.LastCheckedAt})
	}
	if len(ids) == 0 {
		return e, errors.New("当前筛选范围没有已纳管集群")
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	where := `cluster_id IN (` + marks + `) AND bucket_start>=? AND bucket_start<?`
	args := make([]any, 0, len(ids)+2)
	for _, id := range ids {
		args = append(args, id)
	}
	end := window.End
	if end.After(now) {
		end = now
	}
	args = append(args, s.eventTime(window.Start), s.eventTime(end))
	dayExpression := `DATE(DATE_ADD(bucket_start,INTERVAL 8 HOUR))`
	if s.eventSQLite {
		dayExpression = `strftime('%Y-%m-%d',bucket_start,'+8 hours')`
	}
	rows, err := s.events.QueryContext(ctx, `SELECT `+dayExpression+`,cluster_id,type,SUM(occurrence_count) FROM event_issue_hourly WHERE `+where+` GROUP BY `+dayExpression+`,cluster_id,type ORDER BY 1`, args...)
	if err != nil {
		return e, errors.New("读取事件小时聚合失败")
	}
	points := map[string]*Point{}
	for rows.Next() {
		var day, id, kind string
		var count int64
		if err = rows.Scan(&day, &id, &kind, &count); err != nil {
			rows.Close()
			return e, err
		}
		if points[day] == nil {
			points[day] = &Point{Day: day}
		}
		if kind == "Warning" {
			points[day].Warning += count
			e.Warning += count
			e.Clusters[byID[id]].Warning += count
		} else {
			points[day].Normal += count
			e.Normal += count
			e.Clusters[byID[id]].Normal += count
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return e, err
	}
	for _, p := range points {
		e.Trend = append(e.Trend, *p)
	}
	sort.Slice(e.Trend, func(i, j int) bool { return e.Trend[i].Day < e.Trend[j].Day })
	e.ObservedDays = len(e.Trend)
	e.ExpectedDays = int(window.End.Sub(window.Start).Hours() / 24)
	if e.ObservedDays > 0 {
		e.EarliestEvent = e.Trend[0].Day
		e.LatestEvent = e.Trend[len(e.Trend)-1].Day
	}
	if e.Warning+e.Normal == 0 {
		e.Notes = append(e.Notes, "本周期没有可用事件聚合，无法给出周期故障结论。")
	}
	rows, err = s.events.QueryContext(ctx, `SELECT reason,SUM(occurrence_count) AS total FROM event_issue_hourly WHERE `+where+` AND type='Warning' GROUP BY reason ORDER BY total DESC,reason LIMIT 12`, args...)
	if err != nil {
		return e, err
	}
	for rows.Next() {
		var r Reason
		if err = rows.Scan(&r.Reason, &r.Count); err != nil {
			rows.Close()
			return e, err
		}
		e.Reasons = append(e.Reasons, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return e, err
	}
	// Reason x cluster lets the report say "this problem is worst on that
	// cluster" instead of only "this reason happened a lot somewhere".
	rows, err = s.events.QueryContext(ctx, `SELECT reason,cluster_id,SUM(occurrence_count) AS total FROM event_issue_hourly WHERE `+where+` AND type='Warning' GROUP BY reason,cluster_id ORDER BY total DESC,reason LIMIT 40`, args...)
	if err != nil {
		return e, err
	}
	for rows.Next() {
		var item ReasonCluster
		if err = rows.Scan(&item.Reason, &item.ClusterID, &item.Count); err != nil {
			rows.Close()
			return e, err
		}
		if index, ok := byID[item.ClusterID]; ok {
			item.ClusterName = e.Clusters[index].Name
			item.Environment = e.Clusters[index].Environment
			item.ClusterType = e.Clusters[index].ClusterType
		}
		e.ReasonClusters = append(e.ReasonClusters, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return e, err
	}
	previous := append([]any{}, args...)
	previous[len(previous)-2] = s.eventTime(window.Start.Add(-window.End.Sub(window.Start)))
	previous[len(previous)-1] = s.eventTime(window.Start)
	if err = s.events.QueryRowContext(ctx, `SELECT COALESCE(SUM(occurrence_count),0) FROM event_issue_hourly WHERE `+where+` AND type='Warning'`, previous...).Scan(&e.PreviousWarning); err != nil {
		return e, err
	}
	currentFingerprints, err := s.warningFingerprints(ctx, where, args)
	if err != nil {
		return e, errors.New("读取本期问题指纹失败")
	}
	previousFingerprints, err := s.warningFingerprints(ctx, where, previous)
	if err != nil {
		return e, errors.New("读取上期问题指纹失败")
	}
	e.ProblemClusters = len(currentFingerprints)
	e.PreviousProblemClusters = len(previousFingerprints)
	for fingerprint := range currentFingerprints {
		if _, existed := previousFingerprints[fingerprint]; existed {
			e.OngoingProblemClusters++
		} else {
			e.NewProblemClusters++
		}
	}
	rows, err = s.events.QueryContext(ctx, `SELECT reason,SUM(occurrence_count) AS total FROM event_issue_hourly WHERE `+where+` AND type='Warning' GROUP BY reason ORDER BY total DESC,reason LIMIT 12`, previous...)
	if err != nil {
		return e, err
	}
	for rows.Next() {
		var reason Reason
		if err = rows.Scan(&reason.Reason, &reason.Count); err != nil {
			rows.Close()
			return e, err
		}
		e.PreviousReasons = append(e.PreviousReasons, reason)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return e, err
	}
	scanArgs := append([]any{}, args...)
	scanArgs[len(scanArgs)-2] = s.platformTime(window.Start)
	scanArgs[len(scanArgs)-1] = s.platformTime(end)
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),0) FROM security_baseline_history WHERE cluster_id IN (`+marks+`) AND started_at>=? AND started_at<?`, scanArgs...).Scan(&e.SecurityCompleted, &e.SecurityFailed)
	if err != nil {
		return e, errors.New("读取安全扫描记录失败")
	}
	if alertEvidence, readErr := s.collectAlerts(ctx, ids, window.Start, end); readErr != nil {
		e.Notes = append(e.Notes, "告警汇总数据不可用，本期报告未统计告警趋势。")
	} else {
		e.Alerts = alertEvidence
		if alertEvidence.CollectionSuccessPct < 99.9 {
			e.Notes = append(e.Notes, fmt.Sprintf("告警采集成功率约 %.1f%%；采集缺口期间的告警可能未被保留。", alertEvidence.CollectionSuccessPct))
		}
	}
	fleet, err := s.source.ListFleetCapacitySnapshots(ctx)
	if err != nil {
		e.Notes = append(e.Notes, "生成时容量快照不可用，容量表为空。 ")
		return e, nil
	}
	pools := map[string]*Pool{}
	for _, c := range e.Clusters {
		key := c.Environment + "/" + c.ClusterType
		if pools[key] == nil {
			pools[key] = &Pool{Environment: c.Environment, ClusterType: c.ClusterType}
		}
		pools[key].Clusters++
	}
	for _, item := range fleet.Items {
		index, ok := byID[item.ClusterID]
		if !ok {
			continue
		}
		c := e.Clusters[index]
		p := pools[c.Environment+"/"+c.ClusterType]
		v := item.Capacity
		p.Samples++
		p.CPUCores += float64(v.CPUAllocatableMilli) / 1000
		p.MemoryGiB += float64(v.MemoryAllocatable) / (1 << 30)
		p.CPURequested += float64(v.Platform.CPURequestsMilli+v.Business.CPURequestsMilli+v.Unclassified.CPURequestsMilli) / 1000
		p.MemoryRequestedGiB += float64(v.Platform.MemoryRequests+v.Business.MemoryRequests+v.Unclassified.MemoryRequests) / (1 << 30)
		if p.CollectedAt.IsZero() || v.CollectedAt.Before(p.CollectedAt) {
			p.CollectedAt = v.CollectedAt
		}
	}
	keys := []string{}
	for k := range pools {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e.Pools = append(e.Pools, *pools[k])
	}
	sort.Slice(e.Clusters, func(i, j int) bool {
		if e.Clusters[i].Warning == e.Clusters[j].Warning {
			return e.Clusters[i].Name < e.Clusters[j].Name
		}
		return e.Clusters[i].Warning > e.Clusters[j].Warning
	})
	return e, nil
}

func (s *Service) collectAlerts(ctx context.Context, ids []string, start, end time.Time) (AlertEvidence, error) {
	result := AlertEvidence{Top: []AlertProblem{}}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	where := `cluster_id IN (` + marks + `) AND bucket_start>=? AND bucket_start<?`
	args := make([]any, 0, len(ids)+2)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano))
	var durations, durationCount int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN severity='critical' THEN firing_count ELSE 0 END),0),COALESCE(SUM(CASE WHEN severity='warning' THEN firing_count ELSE 0 END),0),COALESCE(SUM(CASE WHEN severity='info' THEN firing_count ELSE 0 END),0),COALESCE(SUM(resolved_count),0),COUNT(DISTINCT CASE WHEN resolved_count>0 THEN fingerprint ELSE NULL END),COUNT(DISTINCT CASE WHEN firing_count>0 THEN cluster_id ELSE NULL END),COALESCE(SUM(affected_resources),0),COALESCE(SUM(total_duration_seconds),0),COALESCE(SUM(resolved_count),0) FROM alert_rollup_hourly WHERE `+where, args...).Scan(&result.Critical, &result.Warning, &result.Info, &result.Resolved, &result.ResolvedGroups, &result.AffectedClusters, &result.AffectedResources, &durations, &durationCount)
	if err != nil {
		return result, err
	}
	if durationCount > 0 {
		result.AverageDurationSec = durations / durationCount
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name,MAX(severity),SUM(firing_count),SUM(resolved_count),SUM(total_duration_seconds),COUNT(DISTINCT cluster_id) AS clusters FROM alert_rollup_hourly WHERE `+where+` GROUP BY name HAVING SUM(firing_count)>0 ORDER BY SUM(firing_count) DESC,SUM(total_duration_seconds) DESC,name LIMIT 10`, args...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item AlertProblem
		if err = rows.Scan(&item.Name, &item.Severity, &item.Occurrences, &item.Resolved, &item.DurationSeconds, &item.AffectedClusters); err != nil {
			rows.Close()
			return result, err
		}
		if item.Occurrences > 1 {
			result.RepeatedProblemTypes++
		}
		result.Top = append(result.Top, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	var success, failure int64
	if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(success_count),0),COALESCE(SUM(failure_count),0) FROM alert_collection_history WHERE `+where, args...).Scan(&success, &failure); err != nil {
		return result, err
	}
	if success+failure > 0 {
		result.CollectionSuccessPct = float64(success) / float64(success+failure) * 100
	}
	return result, nil
}

// warningFingerprints counts stable Event identities instead of occurrence
// retries. One problem can emit thousands of Warning records, so this is the
// report's operator-facing "problem cluster" unit.
func (s *Service) warningFingerprints(ctx context.Context, where string, args []any) (map[string]struct{}, error) {
	rows, err := s.events.QueryContext(ctx, `SELECT DISTINCT fingerprint FROM event_issue_hourly WHERE `+where+` AND type='Warning' AND fingerprint<>''`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]struct{}{}
	for rows.Next() {
		var fingerprint string
		if err := rows.Scan(&fingerprint); err != nil {
			return nil, err
		}
		result[fingerprint] = struct{}{}
	}
	return result, rows.Err()
}

func (s *Service) eventTime(t time.Time) any {
	if s.eventSQLite {
		return t.UTC().Format(time.RFC3339Nano)
	}
	return t.UTC()
}
func (s *Service) platformTime(t time.Time) any {
	if s.sqlite {
		return t.UTC().Format(time.RFC3339Nano)
	}
	return t.UTC()
}
func reportURL(id string) string { return "/api/v1/reports/" + id + "/html" }

func ruleAnalysis(e Evidence) Analysis {
	a := Analysis{Summary: fmt.Sprintf("纳入 %d 套集群，本期识别 %d 个独立问题簇，其中新增 %d 个、跨周期持续 %d 个。", len(e.Clusters), e.ProblemClusters, e.NewProblemClusters, e.OngoingProblemClusters), Observations: []string{}, Actions: []string{}, Limitations: []string{"规则摘要基于已保留证据，不能替代根因验证。"}}
	if e.Warning+e.Normal == 0 {
		a.Summary = "本周期没有可用事件聚合数据，暂不能判断周期运行质量。"
	}
	for _, r := range e.Reasons {
		if len(a.Observations) >= 3 {
			break
		}
		a.Observations = append(a.Observations, fmt.Sprintf("%s 累计发生 %d 次，应结合相关集群与资源排查。", r.Reason, r.Count))
	}
	for _, alert := range e.Alerts.Top {
		if len(a.Observations) >= 3 {
			break
		}
		a.Observations = append(a.Observations, fmt.Sprintf("告警 %s 在 %d 套集群中触发 %d 次，其中已恢复 %d 次。", alert.Name, alert.AffectedClusters, alert.Occurrences, alert.Resolved))
	}
	if e.Alerts.Critical > 0 {
		a.Actions = append(a.Actions, "优先处理报告中持续时间最长的 Critical 告警，沿资源、监控和 Event 链路验证。")
	}
	if e.Alerts.RepeatedProblemTypes > 0 {
		a.Actions = append(a.Actions, "检查重复告警的规则、受影响资源和恢复后再次触发记录，识别抖动与未根治问题。")
	}
	if e.Warning > 0 {
		a.Actions = append(a.Actions, "优先打开 Warning 次数最高的集群事件页，检查持续重试、调度、镜像与探针异常。")
	}
	if e.SecurityFailed > 0 && len(a.Actions) < 3 {
		a.Actions = append(a.Actions, "检查本周期执行失败的安全扫描任务，处理镜像、权限或调度失败后重新扫描。")
	}
	if e.ObservedDays < e.ExpectedDays && len(a.Actions) < 3 {
		a.Actions = append(a.Actions, "核对 Event 采集器与小时汇总保留周期；无记录日期不能直接认定为健康。")
	}
	if len(a.Actions) < 3 {
		a.Actions = append(a.Actions, "按环境 × G01/OCP 分别核对资源池余量，结合容量页面的业务和平台组件分类安排扩容。")
	}
	return a
}
