package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MonitoringError struct{ Code, Message string }

func (e *MonitoringError) Error() string { return e.Message }
func monitoringError(code, message string) error {
	return &MonitoringError{Code: code, Message: message}
}
func monitoringConnectionError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return monitoringError("MONITORING_TIMEOUT", "监控查询超时或已取消，请缩小时间范围后重试。")
	}
	if apierrors.IsForbidden(err) {
		return monitoringError("MONITORING_FORBIDDEN", "纳管凭据无权读取 Service 或访问 services/proxy，请检查对应权限。")
	}
	if apierrors.IsUnauthorized(err) {
		return monitoringError("MONITORING_UNAUTHORIZED", "集群认证失败，请检查纳管凭据是否过期。")
	}
	return monitoringError("MONITORING_CONNECTION_FAILED", "无法连接集群 API Server 或发现监控服务，请检查集群连接。")
}

type MonitoringQuery struct {
	View, Namespace, Pod string
	Start, End           time.Time
}
type MonitoringPoint struct {
	Time  float64  `json:"time"`
	Value *float64 `json:"value"`
}
type MonitoringSeries struct {
	Labels map[string]string `json:"labels"`
	Points []MonitoringPoint `json:"points"`
}
type MonitoringPanel struct {
	ID      string             `json:"id"`
	Title   string             `json:"title"`
	Unit    string             `json:"unit"`
	Hint    string             `json:"hint"`
	Status  string             `json:"status"`
	Message string             `json:"message,omitempty"`
	Series  []MonitoringSeries `json:"series"`
}
type MonitoringSnapshot struct {
	Source      string            `json:"source"`
	Start       time.Time         `json:"start"`
	End         time.Time         `json:"end"`
	Step        int               `json:"step"`
	Panels      []MonitoringPanel `json:"panels"`
	CollectedAt time.Time         `json:"collected_at"`
}
type monitoringSpec struct{ id, title, unit, hint, query string }

var monitoringSlots = make(chan struct{}, 4)

type monitorCacheEntry struct {
	value   MonitoringSnapshot
	expires time.Time
}

var monitorCache = struct {
	sync.Mutex
	items map[string]monitorCacheEntry
}{items: map[string]monitorCacheEntry{}}

func ValidateMonitoringQuery(q MonitoringQuery) error {
	if !contains([]string{"overview", "pods", "nodes", "apiserver", "etcd"}, q.View) || q.Start.IsZero() || q.End.IsZero() || !q.End.After(q.Start) || q.End.Sub(q.Start) > 7*24*time.Hour || q.End.After(time.Now().Add(time.Minute)) {
		return fmt.Errorf("invalid monitoring time range or view")
	}
	if q.Namespace != "" && len(validation.IsDNS1123Label(q.Namespace)) > 0 {
		return fmt.Errorf("invalid namespace")
	}
	if q.Pod != "" && (q.Namespace == "" || len(validation.IsDNS1123Subdomain(q.Pod)) > 0) {
		return fmt.Errorf("pod requires namespace")
	}
	return nil
}
func monitoringSpecs(q MonitoringQuery) []monitoringSpec {
	selector := `container!="",container!="POD",namespace!="",pod!=""`
	if q.Namespace != "" {
		selector += `,namespace=` + strconv.Quote(q.Namespace)
	}
	if q.Pod != "" {
		selector += `,pod=` + strconv.Quote(q.Pod)
	}
	cpu := `sum by (namespace,pod) (rate(container_cpu_usage_seconds_total{` + selector + `}[5m]))`
	memory := `sum by (namespace,pod) (container_memory_working_set_bytes{` + selector + `})`
	api := []monitoringSpec{
		{"api_rate", "API Server 请求速率", "req/s", "按实例区分；包含健康检查等请求。", `sum by (instance) (rate(apiserver_request_total[5m]))`},
		{"api_errors", "API Server 5xx 比例", "%", "无请求时显示缺口；不将缺口当作 0%。", `100 * sum by(instance)(rate(apiserver_request_total{code=~"5.."}[5m])) / sum by(instance)(rate(apiserver_request_total[5m]))`},
		{"api_latency", "API Server P99 延迟", "s", "排除 WATCH/CONNECT 长连接；基于直方图估算。", `histogram_quantile(0.99,sum by(le,instance)(rate(apiserver_request_duration_seconds_bucket{verb!~"WATCH|CONNECT"}[5m])))`},
		{"api_inflight", "API Server 在途请求", "requests", "请求数不是容量利用率，应结合延迟及错误趋势判断。", `sum by(instance,request_kind)(apiserver_current_inflight_requests)`},
		{"api_throttle", "API Server 429 速率", "req/s", "持续限流时检查客户端请求及 API Priority and Fairness。", `sum by(instance)(rate(apiserver_request_total{code="429"}[5m]))`},
	}
	etcd := []monitoringSpec{
		{"etcd_leader", "etcd Leader 可见性", "state", "每个成员应为 1；缺数据无法判断成员健康。", `max by(instance)(etcd_server_has_leader)`},
		{"etcd_changes", "etcd Leader 切换速率", "changes/s", "关注持续或频繁切换，结合磁盘与网络排查。", `max by(instance)(rate(etcd_server_leader_changes_seen_total[5m]))`},
		{"etcd_failed", "etcd 失败提案速率", "proposals/s", "持续增加时检查 quorum、成员及磁盘。", `max by(instance)(rate(etcd_server_proposals_failed_total[5m]))`},
		{"etcd_fsync", "etcd WAL fsync P99", "s", "磁盘写入延迟；需要 etcd 自身暴露的指标。", `histogram_quantile(0.99,sum by(le,instance)(rate(etcd_disk_wal_fsync_duration_seconds_bucket[5m])))`},
		{"etcd_commit", "etcd Backend Commit P99", "s", "后端事务提交延迟，结合 WAL 延迟判断磁盘压力。", `histogram_quantile(0.99,sum by(le,instance)(rate(etcd_disk_backend_commit_duration_seconds_bucket[5m])))`},
		{"etcd_quota", "etcd 数据库配额使用率", "%", "按成员比较数据库大小与配额，不能用 API Server 的客户端指标代替。", `100 * max by(instance)(etcd_mvcc_db_total_size_in_bytes) / max by(instance)(etcd_server_quota_backend_bytes)`},
	}
	if q.View == "apiserver" {
		return api
	}
	if q.View == "etcd" {
		return etcd
	}
	if q.View == "nodes" {
		return nodeSpecs()
	}
	if q.View == "overview" {
		return []monitoringSpec{{"cpu", "Pod CPU 使用总量", "cores", "容器 CPU 使用核数合计，不是主机 CPU 利用率。", `sum(` + cpu + `)`}, {"memory", "Pod 内存工作集总量", "bytes", "容器工作集合计，不是主机内存利用率。", `sum(` + memory + `)`}, api[1], etcd[0]}
	}
	return []monitoringSpec{
		{"pod_cpu", "Pod CPU 实际使用", "cores", "每条曲线为一个 Pod 的容器合计；最多展示 40 条，请按命名空间或 Pod 缩小范围。", cpu},
		{"pod_memory", "Pod 内存工作集", "bytes", "展示实际内存工作集；缺口不按零填充。", memory},
		{"pod_disk_read", "Pod 磁盘读取吞吐量", "bytes/s", "5 分钟平均速率，按 Pod 汇总可观测容器磁盘读取；不保证覆盖全部 PVC 或网络存储 I/O。", `sum by(namespace,pod)(rate(container_fs_reads_bytes_total{` + selector + `}[5m]))`},
		{"pod_disk_write", "Pod 磁盘写入吞吐量", "bytes/s", "5 分钟平均速率，按 Pod 汇总可观测容器磁盘写入；与应用写入字节数可能不同。", `sum by(namespace,pod)(rate(container_fs_writes_bytes_total{` + selector + `}[5m]))`},
		{"pod_disk_read_iops", "Pod 磁盘读取 IOPS", "IOPS", "每秒读取操作次数，取 5 分钟平均；需 cAdvisor 暴露对应计数器。", `sum by(namespace,pod)(rate(container_fs_reads_total{` + selector + `}[5m]))`},
		{"pod_disk_write_iops", "Pod 磁盘写入 IOPS", "IOPS", "每秒写入操作次数，取 5 分钟平均；未采集不按零显示。", `sum by(namespace,pod)(rate(container_fs_writes_total{` + selector + `}[5m]))`},
	}

}

// nodeSpecs returns host-level panels backed by node-exporter.
//
// The queries deliberately omit a job selector. node-exporter is deployed under
// different job names across environments (kube-prometheus-stack uses its own),
// and the node_* metric names are unique to node-exporter, so filtering on job
// would make these panels silently empty on otherwise healthy clusters.
func nodeSpecs() []monitoringSpec {
	// Virtual filesystems report memory usage or container layers rather than
	// real disk pressure; counting them would distort the usage percentage.
	const realFilesystems = `fstype!~"tmpfs|ramfs|overlay|squashfs|iso9660|nfs.*|cifs|autofs"`
	// Loopback and CNI devices carry node-local traffic only. Counting them
	// would multiply real throughput by the number of local pods.
	const realDevices = `device!~"lo|veth.*|tunl.*|erspan.*|gre.*|sit.*|ip6.*|docker.*|br-.*|cni.*|flannel.*|cali.*"`
	byNode := "by(node,instance)"
	return []monitoringSpec{
		{"node_cpu_usage", "节点 CPU 使用率", "%", "按节点汇总所有核心的非 idle 占比；与 Pod CPU 之和不相等，差值来自系统进程。", `100 - (avg ` + byNode + `(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`},
		{"node_load1", "节点 1 分钟负载", "load", "负载包含不可中断 I/O 等待；需结合核心数判断，16 核负载 8 与 2 核负载 8 含义不同。", `max ` + byNode + `(node_load1)`},
		{"node_memory_usage", "节点内存使用率", "%", "基于 MemAvailable 而非 MemFree，已计入可回收缓存，比 free 更接近真实压力。", `100 * (1 - sum ` + byNode + `(node_memory_MemAvailable_bytes) / sum ` + byNode + `(node_memory_MemTotal_bytes))`},
		{"node_filesystem_usage", "节点磁盘空间使用率", "%", "排除 tmpfs/overlay 等虚拟文件系统后按节点合计；单分区环境即根分区使用率。", `100 * (1 - sum ` + byNode + `(node_filesystem_avail_bytes{` + realFilesystems + `}) / sum ` + byNode + `(node_filesystem_size_bytes{` + realFilesystems + `}))`},
		{"node_disk_read", "节点磁盘读取吞吐量", "bytes/s", "5 分钟平均，按节点汇总全部块设备；含内核预读，可能高于应用实际读取量。", `sum ` + byNode + `(rate(node_disk_read_bytes_total[5m]))`},
		{"node_disk_write", "节点磁盘写入吞吐量", "bytes/s", "5 分钟平均，按节点汇总全部块设备。", `sum ` + byNode + `(rate(node_disk_written_bytes_total[5m]))`},
		{"node_disk_iops", "节点磁盘 IOPS", "IOPS", "读写操作合计速率；小文件密集场景比吞吐量更能反映压力。", `sum ` + byNode + `(rate(node_disk_reads_completed_total[5m]) + rate(node_disk_writes_completed_total[5m]))`},
		{"node_disk_utilization", "节点磁盘 I/O 利用率", "%", "设备处于忙碌状态的时间占比；持续接近 100% 表示磁盘成为瓶颈，多盘软 RAID 会相互掩盖。", `100 * max ` + byNode + `(rate(node_disk_io_time_seconds_total[5m]))`},
		{"node_network_receive", "节点网络接收速率", "bytes/s", "排除回环与 CNI 虚拟设备，避免把 Pod 间流量重复计入物理网卡。", `sum ` + byNode + `(rate(node_network_receive_bytes_total{` + realDevices + `}[5m]))`},
		{"node_network_transmit", "节点网络发送速率", "bytes/s", "排除回环与 CNI 虚拟设备。", `sum ` + byNode + `(rate(node_network_transmit_bytes_total{` + realDevices + `}[5m]))`},
		{"node_network_errors", "节点网络错误速率", "pps", "收发错误与丢包合计；持续非零需检查网卡、MTU 与对端链路。", `sum ` + byNode + `(rate(node_network_receive_errs_total{` + realDevices + `}[5m]) + rate(node_network_transmit_errs_total{` + realDevices + `}[5m]))`},
	}
}
func (service *OnboardingService) GetMonitoring(ctx context.Context, id string, q MonitoringQuery) (MonitoringSnapshot, error) {
	if err := ValidateMonitoringQuery(q); err != nil {
		return MonitoringSnapshot{}, err
	}
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return MonitoringSnapshot{}, err
	}
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return MonitoringSnapshot{}, err
	}
	key := fmt.Sprintf("%s/%s/%s/%s/%d/%d", id, q.View, q.Namespace, q.Pod, q.Start.Unix(), q.End.Unix())
	monitorCache.Lock()
	entry, ok := monitorCache.items[key]
	monitorCache.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.value, nil
	}
	select {
	case monitoringSlots <- struct{}{}:
		defer func() { <-monitoringSlots }()
	case <-ctx.Done():
		return MonitoringSnapshot{}, monitoringConnectionError(ctx.Err())
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	connector := NewKubernetesConnector(10 * time.Second)
	client, err := connector.listClientset(parsed)
	if err != nil {
		return MonitoringSnapshot{}, err
	}
	services, err := boundedServices(ctx, client)
	if err != nil {
		return MonitoringSnapshot{}, monitoringConnectionError(err)
	}
	candidates := discoverPrometheusServices(services.Items)
	if len(candidates) == 0 {
		return MonitoringSnapshot{}, monitoringError("MONITORING_NOT_CONFIGURED", "该集群未发现 Prometheus 数据源。请选择已接入监控的集群，或检查集群内 Prometheus Service 的名称、标签与端口配置；不需要新增对外 Service。")
	}
	specs := monitoringSpecs(q)
	result := MonitoringSnapshot{Start: q.Start, End: q.End, Step: int(math.Max(30, math.Ceil(q.End.Sub(q.Start).Seconds()/360))), Panels: make([]MonitoringPanel, len(specs)), CollectedAt: time.Now().UTC()}
	// Pin one data source per request so panels never mix different Prometheus servers.
	source := candidates[0]
	result.Source = source.namespace + "/" + strings.SplitN(source.proxyName, ":", 3)[1]
	slots := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Add(1)
		go func(i int, spec monitoringSpec) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				result.Panels[i] = MonitoringPanel{ID: spec.id, Title: spec.title, Unit: spec.unit, Hint: spec.hint, Status: "error", Message: "查询超时或已取消", Series: []MonitoringSeries{}}
				return
			}
			result.Panels[i] = queryMonitoringPanel(ctx, client, source, spec, q, result.Step)
		}(i, spec)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return result, monitoringConnectionError(ctx.Err())
	}
	monitorCache.Lock()
	if len(monitorCache.items) >= 32 {
		for k, v := range monitorCache.items {
			if time.Now().After(v.expires) {
				delete(monitorCache.items, k)
			}
		}
		if len(monitorCache.items) >= 32 {
			monitorCache.items = map[string]monitorCacheEntry{}
		}
	}
	monitorCache.items[key] = monitorCacheEntry{result, time.Now().Add(30 * time.Second)}
	monitorCache.Unlock()
	return result, nil
}
func queryMonitoringPanel(ctx context.Context, client *kubernetes.Clientset, source prometheusService, spec monitoringSpec, q MonitoringQuery, step int) MonitoringPanel {
	panel := MonitoringPanel{ID: spec.id, Title: spec.title, Unit: spec.unit, Hint: spec.hint, Status: "error", Series: []MonitoringSeries{}}
	stream, err := client.CoreV1().RESTClient().Get().Namespace(source.namespace).Resource("services").Name(source.proxyName).SubResource("proxy").Suffix("api", "v1", "query_range").Param("query", spec.query).Param("start", strconv.FormatInt(q.Start.Unix(), 10)).Param("end", strconv.FormatInt(q.End.Unix(), 10)).Param("step", strconv.Itoa(step)).Param("timeout", "8s").Param("limit", "41").Stream(ctx)
	if err != nil {
		switch {
		case apierrors.IsForbidden(err):
			panel.Message = "纳管凭据缺少目标 Service 的 services/proxy 访问权限。"
		case apierrors.IsUnauthorized(err):
			panel.Message = "集群或 Prometheus 认证失败，请检查凭据。"
		case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
			panel.Message = "该面板查询超时或已取消，请缩小范围后重试。"
		default:
			panel.Message = "Prometheus 代理查询失败，请检查服务端点、连接或查询超时。"
		}
		return panel
	}
	defer stream.Close()
	raw, err := io.ReadAll(io.LimitReader(stream, 8*1024*1024+1))
	if err != nil || len(raw) > 8*1024*1024 {
		panel.Message = "响应过大或读取失败，请缩小查询范围。"
		return panel
	}
	series, err := decodeMonitoring(raw)
	if err != nil {
		panel.Message = err.Error()
		return panel
	}
	panel.Series = series
	if len(series) == 0 {
		panel.Status = "missing"
		panel.Message = "当前范围没有有效样本。请检查指标采集、标签、保留期或所选资源。"
	} else {
		panel.Status = "available"
	}
	return panel
}
func decodeMonitoring(raw []byte) ([]MonitoringSeries, error) {
	var response struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string   `json:"metric"`
				Values [][]json.RawMessage `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Status != "success" || response.Data.ResultType != "matrix" {
		return nil, fmt.Errorf("Prometheus 返回异常，未获取到历史序列")
	}
	if len(response.Data.Result) > 40 {
		return nil, fmt.Errorf("曲线超过 40 条，请选择命名空间或指定 Pod")
	}
	result := []MonitoringSeries{}
	for _, item := range response.Data.Result {
		if len(item.Values) > 1000 {
			return nil, fmt.Errorf("采样点过多，请缩小时间范围")
		}
		series := MonitoringSeries{Labels: item.Metric, Points: []MonitoringPoint{}}
		finite := false
		for _, pair := range item.Values {
			if len(pair) != 2 {
				continue
			}
			var timestamp float64
			var value string
			if json.Unmarshal(pair[0], &timestamp) != nil || json.Unmarshal(pair[1], &value) != nil {
				continue
			}
			number, err := strconv.ParseFloat(value, 64)
			point := MonitoringPoint{Time: timestamp}
			if err == nil && !math.IsNaN(number) && !math.IsInf(number, 0) {
				point.Value = &number
				finite = true
			}
			series.Points = append(series.Points, point)
		}
		if finite {
			result = append(result, series)
		}
	}
	return result, nil
}
