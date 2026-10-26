package reports

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"math"
	"sort"
	"strings"
	"time"
)

//go:embed report.html
var htmlSource string

type chartBar struct {
	Day, Label   string
	X, Y, Height float64
	Count        int64
	HasData      bool
}

// problem is one ranked finding. The report's first job is to answer "what do I
// open first", so every entry carries a plain-language title, the evidence it
// came from and exactly one next action.
type problem struct {
	Rank       int
	Tier       string // P1 / P2 / P3
	Reason     string
	Title      string
	Impact     string // 阻断运行 / 部署受阻 / 服务降级
	Happened   string
	Where      string
	Action     string
	Count      int64
	Share      float64
	BarWidth   float64
	State      string
	StateClass string
	DeltaText  string
}

// impactWeight separates "something broke" from "something looked unhealthy".
// Frequency alone would rank a noisy probe failure above a crash loop, which is
// the opposite of what an operator should open first.
type impact struct {
	Weight int
	Level  string
	Title  string
	Action string
}

var impactByReason = map[string]impact{
	"FailedScheduling":       {4, "阻断运行", "调度容量不足，Pod 排不进去", "核对节点可分配资源与 requests，确认是否需要扩容或调整亲和性"},
	"OOMKilling":             {4, "阻断运行", "容器内存溢出被内核杀死", "调高内存 limit 或修复泄漏，并核对同节点的内存压力"},
	"Evicted":                {4, "阻断运行", "节点内存压力触发 Pod 驱逐", "排查节点内存水位与没有 limit 的工作负载"},
	"NodeNotReady":           {4, "阻断运行", "节点失联，其上工作负载受影响", "检查节点 kubelet、网络与硬件状态"},
	"ImagePullBackOff":       {3, "部署受阻", "镜像拉取失败，新版本无法启动", "核对镜像仓库可达性、凭据与镜像 tag 是否存在"},
	"FailedMount":            {3, "部署受阻", "存储卷挂载失败", "检查 PVC 绑定状态、CSI 驱动与存储后端连通性"},
	"FailedCreatePodSandBox": {3, "部署受阻", "Pod 沙箱创建失败", "检查 CNI 插件与节点网络组件"},
	"FailedAttachVolume":     {3, "部署受阻", "云盘挂载失败", "检查 CSI attach 配额与节点所在可用区"},
	"Failed":                 {3, "部署受阻", "Job 达到重试上限", "查看 Job 失败原因与重试策略"},
	"BackOff":                {2, "服务降级", "容器反复重启", "查看容器退出码与重启前的日志"},
	"Unhealthy":              {2, "服务降级", "健康探针反复失败", "核对探针阈值与实际启动耗时，避免探针成为新故障源"},
	"Preempted":              {2, "服务降级", "低优先级 Pod 被抢占", "确认抢占是否符合业务优先级预期"},
	"FailedKillPod":          {2, "服务降级", "Pod 终止失败", "检查节点 kubelet 状态与残留进程"},
}

func impactOf(reason string) impact {
	if known, ok := impactByReason[reason]; ok {
		return known
	}
	// Unknown reasons still matter; keep them visible but below the known ones.
	return impact{1, "待确认", reason + " 事件重复出现", "按事件明细确认受影响对象与实际影响"}
}

type capacityRisk struct {
	Label    string
	CPU      float64
	Memory   float64
	Severity string // high / medium / low
	Note     string
}

type reportView struct {
	Report
	Bars              []chartBar
	Max               int64
	Start, End        string
	Scope             string
	AIName            string
	Status            string
	StatusClass       string
	AttentionClusters int
	RepeatedReasons   int
	ProblemClusters   int
	NewProblems       int
	OngoingProblems   int
	PreviousProblems  int
	ProblemUnitNote   string
	TopReasons        []Reason
	TopClusters       []ClusterSummary
	TopAlerts         []AlertProblem

	// Editorial layer: what happened, how bad, what to do first.
	Verdict       string
	VerdictNote   string
	Delta         string
	DeltaClass    string
	DeltaNote     string
	Primary       []problem
	Secondary     []problem
	Capacity      []capacityRisk
	CapacityWarn  int
	CoverageNote  string
	AlertsByLevel []alertLine
	HighestDay    string
	HighestCount  int64
	// Not everything can be a card; the rest stays readable but demoted.
	SecondaryTitle string
	AIInsights     []string
	AIActions      []string
	AILimitations  []string
}

type alertLine struct {
	Name     string
	Severity string
	Count    int64
	Share    float64
	Resolved int64
	Clusters int
}

func RenderHTML(report Report) ([]byte, error) {
	view := reportView{
		Report: report, Start: report.Window.Start.In(reportZone).Format("2006.01.02"), End: report.Window.End.In(reportZone).AddDate(0, 0, -1).Format("2006.01.02"),
		Scope: "全部环境 / 全部资源池", AIName: "规则摘要",
	}
	view.ProblemClusters = report.Evidence.ProblemClusters
	view.NewProblems = report.Evidence.NewProblemClusters
	view.OngoingProblems = report.Evidence.OngoingProblemClusters
	view.PreviousProblems = report.Evidence.PreviousProblemClusters
	view.ProblemUnitNote = "按 Event fingerprint 去重"
	// Reports generated before problem fingerprints were added remain readable.
	// Their reason types are an explicit fallback, not silently presented as
	// exact problem identities.
	if view.ProblemClusters == 0 && report.Evidence.Warning > 0 {
		view.ProblemClusters = len(report.Evidence.Reasons)
		view.ProblemUnitNote = "历史报告未保留问题指纹，按 Reason 类型估算"
	}
	view.AIInsights = firstStrings(report.Analysis.Observations, 3)
	view.AIActions = firstStrings(report.Analysis.Actions, 3)
	view.AILimitations = report.Analysis.Limitations
	if report.Request.Environment != "" {
		view.Scope = report.Request.Environment + " / 全部资源池"
	}
	if report.Request.ClusterType != "" {
		if report.Request.Environment != "" {
			view.Scope = report.Request.Environment + " / " + report.Request.ClusterType
		} else {
			view.Scope = "全部环境 / " + report.Request.ClusterType
		}
	}
	if report.AIStatus == "completed" {
		view.AIName = "AI 解读 · " + report.Model
	}
	view.Status = "本期未发现已记录异常"
	view.StatusClass = "stable"
	if report.Evidence.ObservedDays == 0 {
		view.Status = "数据不足，暂无法判断"
		view.StatusClass = "unknown"
	} else if report.Evidence.Warning > 0 || report.Evidence.Alerts.Critical > 0 || report.Evidence.Alerts.Warning > 0 || report.Evidence.SecurityFailed > 0 {
		view.Status = "需要优先处理"
		view.StatusClass = "attention"
	}
	for _, cluster := range report.Evidence.Clusters {
		if cluster.Warning > 0 {
			view.AttentionClusters++
		}
	}
	view.TopReasons = report.Evidence.Reasons
	if len(view.TopReasons) > 5 {
		view.TopReasons = view.TopReasons[:5]
	}
	for _, reason := range report.Evidence.Reasons {
		if reason.Count > 1 {
			view.RepeatedReasons++
		}
	}
	view.TopClusters = report.Evidence.Clusters
	if len(view.TopClusters) > 5 {
		view.TopClusters = view.TopClusters[:5]
	}
	view.TopAlerts = report.Evidence.Alerts.Top
	if len(view.TopAlerts) > 5 {
		view.TopAlerts = view.TopAlerts[:5]
	}
	buildProblems(&view)
	buildCapacity(&view)
	buildVerdict(&view)
	buildAlerts(&view)
	buildBars(&view)
	return executeTemplate(view)
}

// buildProblems ranks every recorded reason by impact first, frequency second,
// then splits the result into a small "act now" set and a demoted remainder.
func buildProblems(view *reportView) {
	e := view.Report.Evidence
	// Where each reason hurts most, taken from the reason x cluster evidence.
	spots := map[string][]ReasonCluster{}
	previous := map[string]int64{}
	for _, reason := range e.PreviousReasons {
		previous[reason.Reason] = reason.Count
	}
	for _, rc := range e.ReasonClusters {
		spots[rc.Reason] = append(spots[rc.Reason], rc)
	}
	for _, rc := range spots {
		sort.Slice(rc, func(i, j int) bool { return rc[i].Count > rc[j].Count })
	}
	type scored struct {
		reason string
		count  int64
		weight int
		score  float64
	}
	list := make([]scored, 0, len(e.Reasons))
	for _, r := range e.Reasons {
		w := impactOf(r.Reason).Weight
		// log keeps a 9,805-count probe failure from burying a 89-count OOM
		// kill: frequency still matters, but only as a multiplier.
		list = append(list, scored{r.Reason, r.Count, w, float64(w) * math.Log10(float64(r.Count)+9)})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].score == list[j].score {
			return list[i].count > list[j].count
		}
		return list[i].score > list[j].score
	})
	maxCount := int64(0)
	for _, item := range list {
		if item.count > maxCount {
			maxCount = item.count
		}
	}
	for index, item := range list {
		info := impactOf(item.reason)
		p := problem{
			Rank: index + 1, Reason: item.reason, Count: item.count,
			Title: info.Title, Action: info.Action, Impact: info.Level,
		}
		before := previous[item.reason]
		switch {
		case before == 0:
			p.State, p.StateClass, p.DeltaText = "本期新增", "new", "上期无记录"
		case item.count > before && float64(item.count) >= float64(before)*1.2:
			change := (float64(item.count) - float64(before)) / float64(before) * 100
			p.State, p.StateClass, p.DeltaText = "较上期恶化", "worse", fmt.Sprintf("重复触发 +%.0f%%", change)
		case item.count < before && float64(item.count) <= float64(before)*0.8:
			change := (float64(before) - float64(item.count)) / float64(before) * 100
			p.State, p.StateClass, p.DeltaText = "较上期改善", "better", fmt.Sprintf("重复触发 -%.0f%%", change)
		default:
			p.State, p.StateClass, p.DeltaText = "跨周期持续", "ongoing", "与上期基本持平"
		}
		if e.Warning > 0 {
			p.Share = float64(item.count) / float64(e.Warning) * 100
			p.Happened = fmt.Sprintf("占本期 Warning 的 %.1f%%", p.Share)
		}
		if maxCount > 0 {
			p.BarWidth = math.Max(4, float64(item.count)/float64(maxCount)*100)
		}
		p.Where = describeSpots(spots[item.reason])
		switch {
		case index < 3:
			p.Tier = fmt.Sprintf("P%d", index+1)
			view.Primary = append(view.Primary, p)
		case index < 7:
			p.Tier = "P4+"
			// The count already has its own column, so the location should only
			// name the cluster instead of repeating the number.
			p.Where = spotNames(spots[item.reason])
			view.Secondary = append(view.Secondary, p)
		}
	}
	if len(view.Secondary) > 0 {
		view.SecondaryTitle = "其余需要关注的问题"
	}
}

// describeSpots names the top clusters behind a reason so the reader knows where
// to look instead of only what happened.
func describeSpots(items []ReasonCluster) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, 3)
	var rest int64
	for index, item := range items {
		if index < 2 {
			name := item.ClusterName
			if name == "" {
				name = item.ClusterID
			}
			parts = append(parts, fmt.Sprintf("%s %s", name, thousands(item.Count)))
			continue
		}
		rest += item.Count
	}
	if rest > 0 {
		parts = append(parts, fmt.Sprintf("其余 %s", thousands(rest)))
	}
	// Two clusters spread evenly is worth saying explicitly, because it changes
	// whether this is a per-cluster fix or a platform-wide one.
	if len(items) == 2 && items[0].Count > 0 && float64(items[1].Count)/float64(items[0].Count) > 0.85 {
		return strings.Join(parts, " · ") + "（两套集群基本持平）"
	}
	return strings.Join(parts, " · ")
}

// spotNames names the affected clusters only, for rows that already show a count.
func spotNames(items []ReasonCluster) string {
	names := make([]string, 0, 3)
	for index, item := range items {
		if index >= 3 {
			break
		}
		name := item.ClusterName
		if name == "" {
			name = item.ClusterID
		}
		names = append(names, name)
	}
	return strings.Join(names, "、")
}

func buildCapacity(view *reportView) {
	for _, pool := range view.Report.Evidence.Pools {
		if pool.Samples == 0 || pool.CPUCores <= 0 || pool.MemoryGiB <= 0 {
			continue
		}
		cpu := pool.CPURequested / pool.CPUCores * 100
		memory := pool.MemoryRequestedGiB / pool.MemoryGiB * 100
		worst := math.Max(cpu, memory)
		risk := capacityRisk{Label: pool.Environment + " / " + pool.ClusterType, CPU: cpu, Memory: memory, Severity: "low"}
		switch {
		case worst >= 85:
			risk.Severity = "high"
			risk.Note = "Requests 接近打满，新 Pod 可能排不进去"
		case worst >= 70:
			risk.Severity = "medium"
			risk.Note = "余量偏紧，建议提前规划扩容"
		default:
			risk.Note = "余量充足"
		}
		if risk.Severity == "high" {
			view.CapacityWarn++
		}
		view.Capacity = append(view.Capacity, risk)
	}
	sort.Slice(view.Capacity, func(i, j int) bool {
		left, right := math.Max(view.Capacity[i].CPU, view.Capacity[i].Memory), math.Max(view.Capacity[j].CPU, view.Capacity[j].Memory)
		return left > right
	})
}

// buildVerdict writes the one paragraph a reader should be able to stop at.
func buildVerdict(view *reportView) {
	e := view.Report.Evidence
	if e.ObservedDays == 0 {
		view.Verdict = "本期没有可用的聚合记录，无法判断运行质量。"
		view.VerdictNote = "请先确认事件采集任务与保留周期，再重新生成报告。"
		return
	}
	head := fmt.Sprintf("本期识别 %d 个独立问题簇，其中新增 %d 个、跨周期持续 %d 个。", view.ProblemClusters, view.NewProblems, view.OngoingProblems)
	if len(view.Primary) > 0 {
		top := view.Primary[0]
		head = fmt.Sprintf("本期识别 %d 个独立问题簇，最需要先处理的是「%s」。",
			view.ProblemClusters, top.Title)
	}
	view.Verdict = head

	notes := []string{}
	// Week-over-week is the single most useful number and it was previously
	// buried in the appendix.
	if view.PreviousProblems > 0 {
		change := (float64(view.ProblemClusters) - float64(view.PreviousProblems)) / float64(view.PreviousProblems) * 100
		view.DeltaNote = fmt.Sprintf("上一等长周期为 %d 个问题簇", view.PreviousProblems)
		switch {
		case view.PreviousProblems < 5 && view.ProblemClusters > view.PreviousProblems:
			view.Delta = fmt.Sprintf("净增 %d 个", view.ProblemClusters-view.PreviousProblems)
			view.DeltaClass = "worse"
		case view.PreviousProblems < 5 && view.ProblemClusters < view.PreviousProblems:
			view.Delta = fmt.Sprintf("减少 %d 个", view.PreviousProblems-view.ProblemClusters)
			view.DeltaClass = "better"
		case change > 5:
			view.Delta = fmt.Sprintf("↑ %.0f%%", change)
			view.DeltaClass = "worse"
		case change < -5:
			view.Delta = fmt.Sprintf("↓ %.0f%%", math.Abs(change))
			view.DeltaClass = "better"
		default:
			view.Delta = "基本持平"
			view.DeltaClass = "flat"
		}
	} else {
		view.Delta = "无上期数据"
		view.DeltaClass = "unknown"
		view.DeltaNote = "上一等长周期没有问题指纹，无法对比"
	}
	// Peak day is the second useful anchor: it tells the reader when to look.
	for _, point := range e.Trend {
		if point.Warning > view.HighestCount {
			view.HighestCount = point.Warning
			view.HighestDay = point.Day
		}
	}
	if view.HighestCount > 0 && len(e.Trend) > 1 {
		avg := float64(e.Warning) / float64(len(e.Trend))
		if avg > 0 && float64(view.HighestCount) > avg*1.3 {
			notes = append(notes, fmt.Sprintf("%s 单日 %s 次，约为日均的 %.1f 倍，建议与当天的变更或发布记录对照",
				shortDay(view.HighestDay), thousands(view.HighestCount), float64(view.HighestCount)/avg))
		}
	}
	if view.CapacityWarn > 0 {
		worst := view.Capacity[0]
		notes = append(notes, fmt.Sprintf("%s 的资源 Requests 已达 CPU %.0f%% / 内存 %.0f%%，容量可能很快成为瓶颈",
			worst.Label, worst.CPU, worst.Memory))
	}
	if e.Alerts.CollectionSuccessPct > 0 && e.Alerts.CollectionSuccessPct < 99.9 {
		view.CoverageNote = fmt.Sprintf("告警采集成功率 %.1f%%，存在缺口；缺口时段的告警可能没有被保留。", e.Alerts.CollectionSuccessPct)
		notes = append(notes, view.CoverageNote)
	}
	if e.SecurityFailed > 0 {
		notes = append(notes, fmt.Sprintf("安全扫描有 %d 次执行失败，未能完成检查，需先修通扫描链路", e.SecurityFailed))
	}
	if len(notes) > 3 {
		notes = notes[:3]
	}
	for index, note := range notes {
		notes[index] = strings.TrimRight(note, "。；，、 ")
	}
	view.VerdictNote = strings.Join(notes, "；") + "。"
	if len(notes) == 0 {
		view.VerdictNote = "未发现容量、采集覆盖或安全扫描层面的阻塞。"
	}
}

func firstStrings(items []string, limit int) []string {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func buildAlerts(view *reportView) {
	alerts := view.Report.Evidence.Alerts
	maxCount := int64(0)
	for _, item := range view.TopAlerts {
		if item.Occurrences > maxCount {
			maxCount = item.Occurrences
		}
	}
	total := alerts.Critical + alerts.Warning + alerts.Info
	for _, item := range view.TopAlerts {
		line := alertLine{Name: item.Name, Severity: item.Severity, Count: item.Occurrences, Resolved: item.Resolved, Clusters: item.AffectedClusters}
		// One bar scale for the whole report: share of all alert firings.
		if total > 0 {
			line.Share = math.Max(4, float64(item.Occurrences)/float64(total)*100)
		} else if maxCount > 0 {
			line.Share = math.Max(4, float64(item.Occurrences)/float64(maxCount)*100)
		}
		view.AlertsByLevel = append(view.AlertsByLevel, line)
	}
}

func buildBars(view *reportView) {
	report := view.Report
	byDay := map[string]Point{}
	view.Max = 1
	for _, p := range report.Evidence.Trend {
		byDay[p.Day] = p
		if p.Warning > view.Max {
			view.Max = p.Warning
		}
	}
	end := report.Window.End
	if report.GeneratedAt.Before(end) {
		end = report.GeneratedAt
	}
	for day := report.Window.Start; day.Before(end); day = day.AddDate(0, 0, 1) {
		key := day.In(reportZone).Format("2006-01-02")
		p, ok := byDay[key]
		// Days without records must be visually distinct from zero-trouble days.
		view.Bars = append(view.Bars, chartBar{Day: key, Label: day.Format("01/02"), Count: p.Warning, HasData: ok})
	}
	for i := range view.Bars {
		b := &view.Bars[i]
		b.X = 55 + float64(i)*880/float64(max(1, len(view.Bars)))
		b.Height = 170 * float64(b.Count) / float64(view.Max)
		b.Y = 210 - b.Height
	}
}

func executeTemplate(view reportView) ([]byte, error) {
	report := view.Report
	funcs := template.FuncMap{
		"number":  func(v int64) string { return thousands(v) },
		"decimal": func(v float64) string { return fmt.Sprintf("%.1f", v) },
		"percent": func(v, total float64) string {
			if total <= 0 {
				return "—"
			}
			return fmt.Sprintf("%.1f%%", v/total*100)
		},
		"barWidth": func() float64 { return math.Max(2, 880/float64(max(1, len(view.Bars)))-3) },
		"clock": func(t time.Time) string {
			if t.IsZero() {
				return "尚无快照"
			}
			return t.In(reportZone).Format("2006-01-02 15:04")
		},
		"shortID": func(id string) string {
			if len(id) > 8 {
				return id[:8]
			}
			return id
		},
		"day": shortDay,
		"reasonWidth": func(v int64) float64 {
			if len(report.Evidence.Reasons) == 0 || report.Evidence.Reasons[0].Count <= 0 {
				return 0
			}
			return 280 * float64(v) / float64(report.Evidence.Reasons[0].Count)
		},
	}
	t, err := template.New("report").Funcs(funcs).Parse(htmlSource)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	err = t.Execute(&buffer, view)
	return buffer.Bytes(), err
}

// thousands keeps long counters scannable: 17086 reads as 17,086.
func thousands(v int64) string {
	text := fmt.Sprintf("%d", v)
	if v < 1000 {
		return text
	}
	var out []byte
	for i, ch := range []byte(text) {
		if i > 0 && (len(text)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, ch)
	}
	return string(out)
}

func shortDay(key string) string {
	parsed, err := time.ParseInLocation("2006-01-02", key, reportZone)
	if err != nil {
		return key
	}
	return parsed.Format("01/02")
}
