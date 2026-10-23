package reports

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"math"
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
	TopReasons        []Reason
	TopClusters       []ClusterSummary
}

func RenderHTML(report Report) ([]byte, error) {
	view := reportView{Report: report, Start: report.Window.Start.In(reportZone).Format("2006.01.02"), End: report.Window.End.In(reportZone).AddDate(0, 0, -1).Format("2006.01.02"), Scope: "全部环境 / 全部资源池", AIName: "规则摘要"}
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
	view.Status = "暂未发现已记录异常"
	view.StatusClass = "stable"
	if report.Evidence.ObservedDays == 0 {
		view.Status = "数据不足，暂无法判断"
		view.StatusClass = "unknown"
	} else if report.Evidence.Warning > 0 || report.Evidence.SecurityFailed > 0 {
		view.Status = "存在需要优先处理的问题"
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
		view.Bars = append(view.Bars, chartBar{Day: key, Label: day.Format("01/02"), Count: p.Warning, HasData: ok})
	}
	for i := range view.Bars {
		b := &view.Bars[i]
		b.X = 55 + float64(i)*880/float64(max(1, len(view.Bars)))
		b.Height = 170 * float64(b.Count) / float64(view.Max)
		b.Y = 210 - b.Height
	}
	funcs := template.FuncMap{
		"rank":    func(v int) int { return v + 1 },
		"number":  func(v int64) string { return fmt.Sprintf("%d", v) },
		"decimal": func(v float64) string { return fmt.Sprintf("%.1f", v) },
		"percent": func(v, total float64) string {
			if total <= 0 {
				return "—"
			}
			return fmt.Sprintf("%.1f%%", v/total*100)
		},
		"barWidth": func() float64 { return math.Max(2, 880/float64(max(1, len(view.Bars)))-3) },
		"reasonWidth": func(v int64) float64 {
			if len(report.Evidence.Reasons) == 0 || report.Evidence.Reasons[0].Count <= 0 {
				return 0
			}
			return 280 * float64(v) / float64(report.Evidence.Reasons[0].Count)
		},
		"reasonPercent": func(v int64) float64 {
			if report.Evidence.Warning <= 0 {
				return 0
			}
			return math.Min(100, float64(v)/float64(report.Evidence.Warning)*100)
		},
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
	}
	t, err := template.New("report").Funcs(funcs).Parse(htmlSource)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	err = t.Execute(&buffer, view)
	return buffer.Bytes(), err
}
