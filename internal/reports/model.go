package reports

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("报告不存在")
	ErrBusy     = errors.New("已有报告正在生成，请稍后重试")
)

var reportZone = time.FixedZone("Asia/Shanghai", 8*60*60)

type Request struct {
	Period      string `json:"period"`
	Date        string `json:"date"`
	Environment string `json:"environment"`
	ClusterType string `json:"cluster_type"`
	UseAI       bool   `json:"use_ai"`
}

type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Label string    `json:"label"`
}

// PeriodWindow uses a half-open [start,end) window in the bank's time zone.
func PeriodWindow(period, date string, now time.Time) (Window, error) {
	day, err := time.ParseInLocation("2006-01-02", date, reportZone)
	if err != nil {
		return Window{}, errors.New("请选择有效的报告日期")
	}
	var end time.Time
	label := ""
	switch period {
	case "weekly":
		day = day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
		end = day.AddDate(0, 0, 7)
		label = "周报"
	case "monthly":
		day = time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, reportZone)
		end = day.AddDate(0, 1, 0)
		label = "月报"
	case "quarterly":
		day = time.Date(day.Year(), time.Month((int(day.Month())-1)/3*3+1), 1, 0, 0, 0, 0, reportZone)
		end = day.AddDate(0, 3, 0)
		label = "季报"
	default:
		return Window{}, errors.New("报告周期仅支持 weekly、monthly、quarterly")
	}
	if !day.Before(now) {
		return Window{}, errors.New("不能生成未来周期的报告")
	}
	if day.Before(now.AddDate(-3, 0, 0)) {
		return Window{}, errors.New("请选择最近三年内的周期")
	}
	return Window{Start: day, End: end, Label: label}, nil
}

type Point struct {
	Day     string `json:"day"`
	Normal  int64  `json:"normal"`
	Warning int64  `json:"warning"`
}
type Reason struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}
type ClusterSummary struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Environment     string    `json:"environment"`
	ClusterType     string    `json:"cluster_type"`
	TechnologyStack string    `json:"technology_stack"`
	Status          string    `json:"status"`
	CheckedAt       time.Time `json:"checked_at"`
	Normal          int64     `json:"normal"`
	Warning         int64     `json:"warning"`
}
type Pool struct {
	Environment        string    `json:"environment"`
	ClusterType        string    `json:"cluster_type"`
	Clusters           int       `json:"clusters"`
	Samples            int       `json:"samples"`
	CPUCores           float64   `json:"cpu_cores"`
	MemoryGiB          float64   `json:"memory_gib"`
	CPURequested       float64   `json:"cpu_requested"`
	MemoryRequestedGiB float64   `json:"memory_requested_gib"`
	CollectedAt        time.Time `json:"collected_at"`
}
type Evidence struct {
	Clusters          []ClusterSummary `json:"clusters"`
	Trend             []Point          `json:"trend"`
	Reasons           []Reason         `json:"reasons"`
	Pools             []Pool           `json:"pools"`
	Normal            int64            `json:"normal"`
	Warning           int64            `json:"warning"`
	PreviousWarning   int64            `json:"previous_warning"`
	ObservedDays      int              `json:"observed_days"`
	ExpectedDays      int              `json:"expected_days"`
	EarliestEvent     string           `json:"earliest_event"`
	LatestEvent       string           `json:"latest_event"`
	SecurityCompleted int              `json:"security_completed"`
	SecurityFailed    int              `json:"security_failed"`
	Notes             []string         `json:"notes"`
}
type Analysis struct {
	Summary      string   `json:"summary"`
	Observations []string `json:"observations"`
	Actions      []string `json:"actions"`
	Limitations  []string `json:"limitations"`
}
type Report struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Request     Request   `json:"request"`
	Window      Window    `json:"window"`
	GeneratedAt time.Time `json:"generated_at"`
	AIStatus    string    `json:"ai_status"`
	AIMessage   string    `json:"ai_message"`
	Model       string    `json:"model"`
	Evidence    Evidence  `json:"evidence"`
	Analysis    Analysis  `json:"analysis"`
	URL         string    `json:"url"`
}
type Item struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Period      string    `json:"period"`
	Environment string    `json:"environment"`
	ClusterType string    `json:"cluster_type"`
	GeneratedAt time.Time `json:"generated_at"`
	AIStatus    string    `json:"ai_status"`
	URL         string    `json:"url"`
}
type ModelConfig struct {
	Endpoint    string `json:"endpoint"`
	Model       string `json:"model"`
	APIKey      string `json:"api_key,omitempty"`
	HasAPIKey   bool   `json:"has_api_key"`
	ClearAPIKey bool   `json:"clear_api_key,omitempty"`
}
