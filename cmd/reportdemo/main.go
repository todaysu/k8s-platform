package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"example.com/k8s-platform/internal/reports"
)

func main() {
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	generatedAt := time.Date(2026, time.October, 24, 9, 30, 0, 0, zone)
	report := reports.Report{
		ID:    "demo-v1-operations-signal",
		Title: "2026.10.19 — 10.25 集群运行周报·高数据量演示",
		Request: reports.Request{
			Period: "weekly",
			UseAI:  true,
		},
		Window:      reports.Window{Start: time.Date(2026, 10, 19, 0, 0, 0, 0, zone), End: time.Date(2026, 10, 26, 0, 0, 0, 0, zone), Label: "周报"},
		GeneratedAt: generatedAt,
		AIStatus:    "completed",
		Model:       "演示分析器",
		Evidence: reports.Evidence{
			Normal:            18420,
			Warning:           217,
			PreviousWarning:   136,
			ObservedDays:      6,
			ExpectedDays:      7,
			EarliestEvent:     "2026-10-19",
			LatestEvent:       "2026-10-24",
			SecurityCompleted: 9,
			SecurityFailed:    3,
			Trend: []reports.Point{
				{Day: "2026-10-19", Normal: 2810, Warning: 18},
				{Day: "2026-10-20", Normal: 3042, Warning: 27},
				{Day: "2026-10-21", Normal: 3150, Warning: 63},
				{Day: "2026-10-22", Normal: 2978, Warning: 49},
				{Day: "2026-10-23", Normal: 3360, Warning: 38},
				{Day: "2026-10-24", Normal: 3080, Warning: 22},
			},
			Reasons: []reports.Reason{
				{Reason: "FailedScheduling", Count: 86},
				{Reason: "Failed / ImagePullBackOff", Count: 48},
				{Reason: "BackOff", Count: 31},
				{Reason: "Unhealthy", Count: 24},
				{Reason: "NodeNotReady", Count: 15},
				{Reason: "FailedMount", Count: 8},
				{Reason: "Evicted", Count: 5},
			},
			Clusters: []reports.ClusterSummary{
				{ID: "stp-g01-03", Name: "stp-g01-03", Environment: "stp", ClusterType: "G01", TechnologyStack: "TCS", Status: "warning", CheckedAt: generatedAt.Add(-40 * time.Second), Normal: 3210, Warning: 72},
				{ID: "stp-ocp-02", Name: "stp-ocp-02", Environment: "stp", ClusterType: "OCP", TechnologyStack: "OCP", Status: "warning", CheckedAt: generatedAt.Add(-55 * time.Second), Normal: 2890, Warning: 51},
				{ID: "uat-g01-04", Name: "uat-g01-04", Environment: "uat", ClusterType: "G01", TechnologyStack: "CMBK8S", Status: "warning", CheckedAt: generatedAt.Add(-72 * time.Second), Normal: 2480, Warning: 37},
				{ID: "st-g01-01", Name: "st-g01-01", Environment: "st", ClusterType: "G01", TechnologyStack: "TCS", Status: "warning", CheckedAt: generatedAt.Add(-80 * time.Second), Normal: 1980, Warning: 26},
				{ID: "stp-g01-01", Name: "stp-g01-01", Environment: "stp", ClusterType: "G01", TechnologyStack: "CMBK8S", Status: "warning", CheckedAt: generatedAt.Add(-95 * time.Second), Normal: 2310, Warning: 18},
				{ID: "uat-ocp-01", Name: "uat-ocp-01", Environment: "uat", ClusterType: "OCP", TechnologyStack: "OCP", Status: "healthy", CheckedAt: generatedAt.Add(-65 * time.Second), Normal: 1780, Warning: 8},
				{ID: "st-g01-02", Name: "st-g01-02", Environment: "st", ClusterType: "G01", TechnologyStack: "CMBK8S", Status: "healthy", CheckedAt: generatedAt.Add(-105 * time.Second), Normal: 1550, Warning: 5},
				{ID: "stp-ocp-01", Name: "stp-ocp-01", Environment: "stp", ClusterType: "OCP", TechnologyStack: "OCP", Status: "healthy", CheckedAt: generatedAt.Add(-62 * time.Second), Normal: 1220},
				{ID: "uat-g01-01", Name: "uat-g01-01", Environment: "uat", ClusterType: "G01", TechnologyStack: "TCS", Status: "healthy", CheckedAt: generatedAt.Add(-88 * time.Second), Normal: 1000},
				{ID: "uat-g01-02", Name: "uat-g01-02", Environment: "uat", ClusterType: "G01", TechnologyStack: "CMBK8S", Status: "healthy", CheckedAt: generatedAt.Add(-93 * time.Second)},
				{ID: "stp-g01-02", Name: "stp-g01-02", Environment: "stp", ClusterType: "G01", TechnologyStack: "TCS", Status: "healthy", CheckedAt: generatedAt.Add(-50 * time.Second)},
				{ID: "st-ocp-01", Name: "st-ocp-01", Environment: "st", ClusterType: "OCP", TechnologyStack: "OCP", Status: "healthy", CheckedAt: generatedAt.Add(-70 * time.Second)},
			},
			Pools: []reports.Pool{
				{Environment: "stp", ClusterType: "G01", Clusters: 3, Samples: 3, CPUCores: 960, MemoryGiB: 3072, CPURequested: 792, MemoryRequestedGiB: 2430, CollectedAt: generatedAt.Add(-2 * time.Minute)},
				{Environment: "stp", ClusterType: "OCP", Clusters: 2, Samples: 2, CPUCores: 640, MemoryGiB: 2048, CPURequested: 438, MemoryRequestedGiB: 1290, CollectedAt: generatedAt.Add(-3 * time.Minute)},
				{Environment: "uat", ClusterType: "G01", Clusters: 3, Samples: 3, CPUCores: 720, MemoryGiB: 2304, CPURequested: 382, MemoryRequestedGiB: 1100, CollectedAt: generatedAt.Add(-4 * time.Minute)},
				{Environment: "uat", ClusterType: "OCP", Clusters: 1, Samples: 1, CPUCores: 320, MemoryGiB: 1024, CPURequested: 146, MemoryRequestedGiB: 390, CollectedAt: generatedAt.Add(-3 * time.Minute)},
				{Environment: "st", ClusterType: "G01", Clusters: 2, Samples: 2, CPUCores: 480, MemoryGiB: 1536, CPURequested: 248, MemoryRequestedGiB: 680, CollectedAt: generatedAt.Add(-5 * time.Minute)},
			},
			Notes: []string{
				"演示数据为人工构造，只用于验证报告信息层级和分析方向。",
				"Warning 按 Kubernetes Event 发生次数累计，不等于独立故障数。",
				"容量是生成时 Requests 快照，不是周期内最大值。",
			},
		},
		Analysis: reports.Analysis{
			Summary: "本期 Warning 较前一等长周期上升 59.6%。主要增量集中在 stp-g01-03 和 stp-ocp-02，FailedScheduling 连续多日出现，且 stp/G01 的 CPU Requests 已达 82.5%；建议将调度容量作为本周首要运维问题，镜像拉取异常作为第二优先级。",
			Observations: []string{
				"FailedScheduling 占全部 Warning 的 39.6%，并在 10 月 21—22 日形成高峰。",
				"前两个热点集群贡献 56.7% 的 Warning，问题不是全域均匀发生。",
				"3 次安全扫描执行失败，需要与 CIS 控制项不通过分开处理。",
			},
			Actions: []string{
				"先下钻 stp-g01-03 的 FailedScheduling 事件，按命名空间统计 requests、taint/toleration 和亲和性冲突。",
				"对 stp/G01 启动容量扩容评估，在 CPU Requests 继续高于 80% 前确定节点增补或业务迁移方案。",
				"检查 stp-ocp-02 的镜像仓库可达性、imagePullSecret 和镜像 tag，将重复 ImagePullBackOff 合并为一个根因任务。",
				"分别查看 3 个失败的安全扫描任务，先排除镜像架构、调度和 Job 权限问题。",
			},
			Limitations: []string{
				"本演示不包含 Prometheus 时序利用率，因此不将 Requests 高直接判定为 CPU 压力。",
			},
		},
	}

	page, err := reports.RenderHTML(report)
	if err != nil {
		fatal(err)
	}
	path := filepath.Join("data", "demo", "operational-report-v1.html")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(path, page, 0o644); err != nil {
		fatal(err)
	}
	fmt.Println(path)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
