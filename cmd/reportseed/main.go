package main

// reportseed fills the platform with a believable week of operational evidence
// so the AI report can be reviewed without waiting for a real incident.
//
// Everything it writes is tagged ([FTC-DEMO] messages, report-demo- ids and
// demo- cluster ids), so -clean removes exactly what it created and nothing
// else. The scenario is deliberately realistic rather than random: a release
// spike mid-week, capacity pressure concentrated in one environment, OOM
// restarts behind a memory alert, one alert collection gap, and one failed
// security scan.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"example.com/k8s-platform/internal/cluster"
	"example.com/k8s-platform/internal/platformsettings"
	"example.com/k8s-platform/internal/store"
)

const (
	demoPrefix  = "report-demo-"
	demoCluster = "demo-"
	demoMessage = "[FTC-DEMO] "
)

type target struct {
	ID, Name, Environment, ClusterType, TechnologyStack string
	Index                                               int
	Workers                                             int
	Cores                                               int
	CPUUtilisation                                      float64
	MemoryUtilisation                                   float64
	// CPURequestRatio drives both the capacity snapshot and the scheduling
	// pressure, so the report can connect "requests are full" to "pods cannot
	// be scheduled" instead of reporting two unrelated facts.
	CPURequestRatio float64
}

type eventScript struct {
	Reason, Kind, Namespace, Name, Message string
	Daily                                  []int64
	// Only in these environments; empty means every cluster.
	Environments []string
	// Extra counts for clusters matching these environments.
	Bias map[string]int64
	Normal bool
}

type alertScript struct {
	Name, Severity, Kind, Namespace, Resource, SourceType string
	Firing, Resolved, DurationMinutes                     int64
	Daily                                                 []int64
	Environments                                          []string
}

func main() {
	path := flag.String("db", "./data/platform.db", "SQLite database path")
	anchorText := flag.String("anchor", "", "any date inside the target week (defaults to last week, Asia/Shanghai)")
	extra := flag.Int("clusters", 3, "how many demo clusters to create alongside the real ones (0 keeps only real clusters)")
	keyText := flag.String("encryption-key", os.Getenv("CREDENTIAL_ENCRYPTION_KEY"), "credential encryption key, so demo clusters store a well-formed credential instead of a corrupt one")
	cleanOnly := flag.Bool("clean", false, "remove every demo record and exit")
	flag.Parse()

	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	anchor := time.Now().In(zone)
	if strings.TrimSpace(*anchorText) != "" {
		parsed, err := time.ParseInLocation("2006-01-02", *anchorText, zone)
		must(err)
		anchor = parsed
	}
	day := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, zone)
	weekStart := day.AddDate(0, 0, -(int(day.Weekday())+6)%7-7)
	previousStart := weekStart.AddDate(0, 0, -7)
	// The report needs one complete week, but the alert and event pages default
	// to "current" and "last 24h". Seeding only the finished week would leave
	// both pages empty, so the evidence runs from weekStart up to the anchor day.
	days := int(day.Sub(weekStart).Hours()/24) + 1
	if days < 7 {
		days = 7
	}
	if days > 14 {
		days = 14
	}
	// Anything scheduled after this point would be invented future evidence.
	horizon := day.AddDate(0, 0, 1)

	repository, err := store.OpenSQLite(*path)
	must(err)
	defer repository.Close()
	db := repository.SQLDB()
	ctx := context.Background()

	must(clearDemo(ctx, db))
	if *cleanOnly {
		fmt.Println("removed all demo evidence; real records were untouched")
		return
	}

	must(seedDemoClusters(ctx, db, *extra, weekStart, *keyText))
	targets, err := loadTargets(ctx, db)
	must(err)
	if len(targets) == 0 {
		fatal("no managed clusters found")
	}
	must(seedEvents(ctx, repository, targets, previousStart, weekStart, days, horizon))
	must(seedAlerts(ctx, db, targets, weekStart, days, horizon))
	must(seedCapacity(ctx, db, targets, weekStart))
	must(seedSecurityHistory(ctx, db, targets, weekStart))
	must(enableReports(ctx, db))

	fmt.Printf("\nseeded %d cluster(s); evidence %s → %s (%d days)\n",
		len(targets), weekStart.Format("2006-01-02"), day.Format("2006-01-02"), days)
	for _, t := range targets {
		fmt.Printf("  %-14s %-4s %-4s %-7s cpu-request=%.0f%%\n", t.Name, t.Environment, t.ClusterType, t.TechnologyStack, t.CPURequestRatio*100)
	}
	fmt.Println("\nregenerate the weekly report in the UI, or use -clean to roll back.")
}

// ---- demo clusters -------------------------------------------------------

type demoClusterSpec struct {
	Name, Environment, ClusterType, TechnologyStack string
	Workers, Cores                                  int
	CPUUtilisation, MemoryUtilisation               float64
	CPURequestRatio                                 float64
}

var demoClusters = []demoClusterSpec{
	// Capacity pressure lives in stp/G01: that is the story the report should find.
	{Name: "stp-g01-01", Environment: "stp", ClusterType: "G01", TechnologyStack: "TCS", Workers: 24, Cores: 384, CPUUtilisation: 68.4, MemoryUtilisation: 74.1, CPURequestRatio: 0.86},
	{Name: "stp-ocp-01", Environment: "stp", ClusterType: "OCP", TechnologyStack: "OCP", Workers: 12, Cores: 192, CPUUtilisation: 41.2, MemoryUtilisation: 52.8, CPURequestRatio: 0.47},
	{Name: "prd-g01-01", Environment: "prd", ClusterType: "G01", TechnologyStack: "CMBK8S", Workers: 32, Cores: 512, CPUUtilisation: 57.9, MemoryUtilisation: 81.3, CPURequestRatio: 0.64},
}

func seedDemoClusters(ctx context.Context, db *sql.DB, count int, weekStart time.Time, keyText string) error {
	if count <= 0 {
		return nil
	}
	if count > len(demoClusters) {
		count = len(demoClusters)
	}
	var cipher cluster.CredentialCipher
	if strings.TrimSpace(keyText) != "" {
		if created, err := cluster.NewAESGCMCipher(keyText); err == nil {
			cipher = created
		}
	}
	for index := 0; index < count; index++ {
		spec := demoClusters[index]
		id := demoClusterID(spec.Name)
		// A well-formed envelope keeps the collector failing with a plain
		// connection error. An empty credential would instead log
		// ENCRYPTION_FAILED every cycle, which reads like a platform bug.
		credentials := ""
		if cipher != nil {
			envelope, _ := json.Marshal(map[string]string{"token": "demo-evidence"})
			if sealed, err := cipher.Encrypt(envelope, id); err == nil {
				credentials = sealed
			}
		}
		instances := spec.Workers * 6
		// Park the check inside the report window: discovery never touches these
		// clusters, so a timestamp before the window would read as "unknown for
		// the whole period" instead of "was unhealthy during it".
		checked := weekStart.AddDate(0, 0, 3).Add(9 * time.Hour).UTC().Format(time.RFC3339Nano)
		if _, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO clusters(id,name,api_server,context_name,kubernetes_version,authentication_type,connection_status,encrypted_credentials,fingerprint,created_at,last_checked_at,environment,technology_stack,cluster_type,nodes_ready,nodes_total,instances_ready,instances_total,core_components_ready,core_components_total,worker_nodes,worker_cores,api_latency_ms,cpu_utilization,memory_utilization,metrics_available) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, spec.Name, "https://demo.invalid:6443", "demo-"+spec.Name, "v1.26.8", "Bearer Token", "connected", credentials,
			digest("demo-cluster", spec.Name)[:32], checked, checked, spec.Environment, spec.TechnologyStack, spec.ClusterType,
			spec.Workers, spec.Workers, instances-2, instances, 12, 12, spec.Workers, spec.Cores, 23,
			spec.CPUUtilisation, spec.MemoryUtilisation, 1); err != nil {
			return err
		}
	}
	return nil
}

func demoClusterID(name string) string { return demoCluster + digest("cluster", name)[:30] }

// loadTargets returns real clusters first, then the demo ones, so a seed run
// always enriches the existing fleet instead of replacing it.
func loadTargets(ctx context.Context, db *sql.DB) ([]target, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,name,environment,cluster_type,technology_stack FROM clusters ORDER BY CASE WHEN id LIKE 'demo-%' THEN 1 ELSE 0 END, created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []target{}
	for rows.Next() {
		var t target
		if err = rows.Scan(&t.ID, &t.Name, &t.Environment, &t.ClusterType, &t.TechnologyStack); err != nil {
			return nil, err
		}
		t.Index = len(result)
		for _, spec := range demoClusters {
			if t.Name == spec.Name {
				t.Workers, t.Cores = spec.Workers, spec.Cores
				t.CPUUtilisation, t.MemoryUtilisation, t.CPURequestRatio = spec.CPUUtilisation, spec.MemoryUtilisation, spec.CPURequestRatio
			}
		}
		if t.Workers == 0 {
			t.Workers, t.Cores = 3, 16
		}
		if t.CPURequestRatio == 0 {
			// The single real cluster carries the same pressure story as stp.
			t.CPURequestRatio = 0.78
			t.CPUUtilisation, t.MemoryUtilisation = 44.6, 61.2
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

// ---- events --------------------------------------------------------------

var eventScripts = []eventScript{
	{
		Reason: "FailedScheduling", Kind: "Pod", Namespace: "myapp", Name: "tcab-order-202610-7d9c8",
		Message:      "0/24 nodes are available: 24 Insufficient cpu. preemption: 0/24 nodes are available: 24 Preemption is not helpful for scheduling.",
		Daily:        []int64{186, 214, 268, 712, 396, 241, 96},
		Environments: []string{"stp"},
	},
	{
		Reason: "Unhealthy", Kind: "Pod", Namespace: "myapp", Name: "tcab-account-202612-589d4",
		Message: "Readiness probe failed: HTTP probe failed with statuscode: 503",
		Daily:   []int64{212, 236, 254, 508, 322, 268, 148},
	},
	{
		Reason: "BackOff", Kind: "Pod", Namespace: "myapp", Name: "tcab-customer-202608-5cf86",
		Message:      "Back-off restarting failed container java-app in pod tcab-customer-202608-5cf86-2x9kd_myapp",
		Daily:        []int64{64, 78, 96, 142, 118, 84, 41},
		Environments: []string{"prd"},
		Bias:         map[string]int64{"prd": 52},
	},
	{
		Reason: "ImagePullBackOff", Kind: "Pod", Namespace: "myapp", Name: "tcab-payment-202611-6bd57",
		Message: "Back-off pulling image \"registry.internal:5000/myapp/tcab-payment:2026111912\": rpc error: code = Unknown desc = manifest unknown",
		// A registry hiccup on the release day only.
		Daily: []int64{0, 0, 3, 214, 96, 12, 0},
	},
	{
		Reason: "OOMKilling", Kind: "Pod", Namespace: "myapp", Name: "tcab-ledger-202607-77cc9",
		Message:      "Memory cgroup out of memory: Killed process 24871 (java) total-vm:8412340kB",
		Daily:        []int64{4, 6, 9, 18, 14, 7, 3},
		Environments: []string{"prd"},
	},
	{
		Reason: "FailedMount", Kind: "Pod", Namespace: "myapp", Name: "tcab-report-202609-4ad2f",
		Message:      "Unable to attach or mount volumes: unmounted volumes=[data], unattached volumes=[data]: timed out waiting for the condition",
		Daily:        []int64{2, 3, 5, 9, 6, 3, 1},
		Environments: []string{"prd"},
	},
	{
		Reason: "Evicted", Kind: "Pod", Namespace: "myapp", Name: "tcab-batch-202605-91c7b",
		Message:      "The node was low on resource: memory. Threshold quantity: 4Gi, available: 1832Mi.",
		Daily:        []int64{0, 1, 2, 6, 11, 3, 0},
		Environments: []string{"prd"},
	},
	{
		Reason: "NodeNotReady", Kind: "Node", Namespace: "", Name: "worker-017",
		Message:      "Node worker-017 status is now: NodeNotReady",
		Daily:        []int64{0, 0, 0, 1, 1, 0, 0},
		Environments: []string{"stp"},
	},
	{
		Reason: "FailedCreatePodSandBox", Kind: "Pod", Namespace: "myapp", Name: "tcab-gateway-202604-3fe81",
		Message:      "Failed to create pod sandbox: rpc error: code = DeadlineExceeded desc = failed to set up sandbox container network: plugin type=\"calico\" failed",
		Daily:        []int64{3, 2, 6, 14, 8, 4, 1},
		Environments: []string{"stp"},
	},
	{
		Reason: "Preempted", Kind: "Pod", Namespace: "myapp", Name: "tcab-batch-202605-91c7b",
		Message:      "Preempted by myapp/tcab-order-202610-7d9c8-x4m2p on node worker-008",
		Daily:        []int64{2, 4, 6, 22, 14, 8, 2},
		Environments: []string{"stp"},
	},
	{
		Reason: "Failed", Kind: "Job", Namespace: "myapp", Name: "tcab-settle-20261026-daily",
		Message:      "Job has reached the specified backoff limit",
		Daily:        []int64{1, 1, 2, 5, 3, 2, 1},
		Environments: []string{"prd"},
	},
	{
		Reason: "Pulled", Kind: "Pod", Namespace: "myapp", Name: "tcab-order-202610-7d9c8",
		Message: "Successfully pulled image \"registry.internal:5000/myapp/tcab-order:2026101912\"",
		Daily:   []int64{640, 712, 758, 1180, 864, 706, 512}, Normal: true,
	},
}

func seedEvents(ctx context.Context, repository *store.SQLite, targets []target, previousStart, weekStart time.Time, days int, horizon time.Time) error {
	events := make([]cluster.ClusterEvent, 0, len(targets)*140)
	for _, t := range targets {
		for day := 0; day < days; day++ {
			// Reuse the 7-day shape so the weekly rhythm (quiet weekend, release
			// spike midweek) stays readable past the first week.
			pattern := day % 7
			for scriptIndex, script := range eventScripts {
				if len(script.Environments) > 0 && !contains(script.Environments, t.Environment) {
					continue
				}
				count := script.Daily[pattern]
				if count == 0 {
					continue
				}
				// Same failure reason scales with cluster size, which keeps the
				// per-cluster ranking meaningful instead of uniform.
				count += script.Bias[t.Environment] + int64(t.Workers/8)
				at := weekStart.AddDate(0, 0, day).Add(time.Duration(8+scriptIndex) * time.Hour).Add(time.Duration(t.Index*11) * time.Minute)
				if !at.Before(horizon) {
					continue
				}
				events = append(events, demoEvent(t.ID, fmt.Sprintf("current-%d-%d", day, scriptIndex), script, count, at))
			}
			// Previous period is lower on purpose: the report should be able to
			// say whether this week got worse. It only needs one week, and a
			// repeated event_uid would be merged by the upsert.
			if day < 7 {
				previous := eventScripts[0]
				previousCount := previous.Daily[day]*6/10 + int64(t.Workers/10)
				events = append(events, demoEvent(t.ID, fmt.Sprintf("previous-%d", day), previous, previousCount, previousStart.AddDate(0, 0, day).Add(10*time.Hour)))
			}
		}
	}
	return repository.UpsertEvents(ctx, events)
}

func demoEvent(clusterID, suffix string, script eventScript, count int64, at time.Time) cluster.ClusterEvent {
	typeName := "Warning"
	if script.Normal {
		typeName = "Normal"
	}
	uid := demoPrefix + clusterID + "-" + suffix
	return cluster.ClusterEvent{
		ClusterID: clusterID, EventUID: uid, Namespace: script.Namespace, Type: typeName, Reason: script.Reason, Action: "DemoEvidence",
		Message: demoMessage + script.Message, RegardingAPIVersion: "v1", RegardingKind: script.Kind, RegardingNamespace: script.Namespace,
		RegardingName: script.Name, RegardingUID: demoPrefix + "object-" + suffix, ReportingController: "ftc-report-demo", ReportingInstance: "reportseed",
		OccurrenceCount: count, FirstObservedAt: at.Add(-40 * time.Minute), LastObservedAt: at, ResourceVersion: "demo",
	}
}

// ---- alerts --------------------------------------------------------------

var alertScripts = []alertScript{
	{
		Name: "KubePodCrashLooping", Severity: "critical", Kind: "Pod", Namespace: "myapp", Resource: "tcab-customer-202608-5cf86", SourceType: "business",
		Firing: 6, Resolved: 5, DurationMinutes: 142,
		Daily:        []int64{3, 4, 5, 9, 7, 4, 2},
		Environments: []string{"prd"},
	},
	{
		Name: "KubePodMemoryPressure", Severity: "warning", Kind: "Pod", Namespace: "myapp", Resource: "tcab-payment-202611-6bd57", SourceType: "business",
		Firing: 8, Resolved: 7, DurationMinutes: 96,
		Daily:        []int64{5, 6, 7, 12, 10, 6, 3},
		Environments: []string{"prd"},
	},
	{
		Name: "KubeDeploymentReplicasMismatch", Severity: "warning", Kind: "Deployment", Namespace: "myapp", Resource: "tcab-order-202610", SourceType: "business",
		Firing: 9, Resolved: 8, DurationMinutes: 74,
		Daily:        []int64{6, 7, 9, 16, 11, 8, 4},
	},
	{
		Name: "KubeNodeNotReady", Severity: "critical", Kind: "Node", Resource: "worker-017", SourceType: "platform",
		Firing: 2, Resolved: 2, DurationMinutes: 31,
		Daily:        []int64{0, 0, 1, 1, 1, 0, 0},
		Environments: []string{"stp"},
	},
	{
		Name: "KubePersistentVolumeFillingUp", Severity: "critical", Kind: "PersistentVolumeClaim", Namespace: "myapp", Resource: "data-tcab-report-202609-0", SourceType: "business",
		Firing: 3, Resolved: 1, DurationMinutes: 640,
		Daily:        []int64{1, 1, 2, 2, 2, 2, 1},
		Environments: []string{"prd"},
	},
	{
		Name: "KubeSchedulerLatencyHigh", Severity: "warning", Kind: "Scheduler", Resource: "kube-scheduler", SourceType: "platform",
		Firing: 5, Resolved: 5, DurationMinutes: 38,
		Daily:        []int64{2, 3, 4, 11, 7, 4, 1},
		Environments: []string{"stp"},
	},
	{
		Name: "KubeAPIServerLatencyHigh", Severity: "warning", Kind: "APIServer", Resource: "kube-apiserver", SourceType: "platform",
		Firing: 7, Resolved: 6, DurationMinutes: 46,
		Daily: []int64{4, 4, 5, 8, 6, 5, 3},
	},
	{
		Name: "TargetDown", Severity: "critical", Kind: "Service", Namespace: "myapp", Resource: "tcab-gateway-metrics", SourceType: "business",
		Firing: 2, Resolved: 2, DurationMinutes: 22,
		Daily:        []int64{0, 0, 1, 2, 1, 0, 0},
		Environments: []string{"stp"},
	},
	{
		Name: "KubeletTooManyPods", Severity: "warning", Kind: "Node", Resource: "worker-008", SourceType: "platform",
		Firing: 4, Resolved: 4, DurationMinutes: 58,
		Daily:        []int64{2, 2, 3, 5, 4, 3, 1},
		Environments: []string{"stp"},
	},
}

func seedAlerts(ctx context.Context, db *sql.DB, targets []target, weekStart time.Time, days int, horizon time.Time) error {
	for _, t := range targets {
		for day := 0; day < days; day++ {
			pattern := day % 7
			for scriptIndex, script := range alertScripts {
				if len(script.Environments) > 0 && !contains(script.Environments, t.Environment) {
					continue
				}
				daily := script.Daily[pattern]
				if daily == 0 {
					continue
				}
				bucket := weekStart.AddDate(0, 0, day).Add(time.Duration(9+scriptIndex) * time.Hour).UTC()
				if !bucket.Before(horizon) {
					continue
				}
				fingerprint := fmt.Sprintf("%s%s-%s-%d", demoPrefix, script.Name, t.ID[:8], day)
				id := digest("hourly", t.ID, fingerprint, bucket.Format(time.RFC3339Nano))
				firing := daily + script.Firing
				resolved := min(script.Resolved, firing)
				if _, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO alert_rollup_hourly(id,bucket_start,cluster_id,fingerprint,name,severity,source_type,resource_kind,firing_count,suppressed_count,resolved_count,total_duration_seconds,max_duration_seconds,affected_resources,last_observed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
					id, stamp(bucket), t.ID, fingerprint, script.Name, script.Severity, script.SourceType, script.Kind,
					firing, 0, resolved, int64(script.DurationMinutes)*60*resolved, int64(script.DurationMinutes)*60, firing, stamp(bucket.Add(45*time.Minute))); err != nil {
					return err
				}
				// The alert centre opens on "current", which only lists firing
				// alerts. Criticals in the last two days stay open so the default
				// view is not empty after seeding.
				keepFiring := script.Severity == "critical" && day >= days-2
				if day >= 3 || keepFiring {
					if err := insertAlertInstance(ctx, db, t, script, day, bucket, keepFiring); err != nil {
						return err
					}
				}
			}
			// A single collection gap mid-week: the report must notice that the
			// alert counts for that day are not trustworthy.
			status, success, failure, message := "ok", 24, 0, ""
			if pattern == 3 {
				status, success, failure, message = "error", 21, 3, demoMessage+"Alertmanager proxy timeout after 10s"
			}
			coverageID := fmt.Sprintf("%scoverage-%s-%d", demoPrefix, t.ID[:8], day)
			bucket := weekStart.AddDate(0, 0, day).Add(8 * time.Hour).UTC()
			if !bucket.Before(horizon) {
				continue
			}
			if _, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO alert_collection_history(id,cluster_id,bucket_start,status,success_count,failure_count,message_sample) VALUES(?,?,?,?,?,?,?)`,
				coverageID, t.ID, stamp(bucket), status, success, failure, message); err != nil {
				return err
			}
		}
	}
	return nil
}

func insertAlertInstance(ctx context.Context, db *sql.DB, t target, script alertScript, day int, started time.Time, keepFiring bool) error {
	id := demoPrefix + digest(t.ID, strings.ToLower(script.Name), fmt.Sprint(day))[:32]
	state, endsAt, lastSeen := "resolved", started.Add(time.Duration(script.DurationMinutes)*time.Minute), started.Add(time.Duration(script.DurationMinutes)*time.Minute)
	if keepFiring {
		// Still open: an empty ends_at is what the UI reads as "ongoing".
		state, endsAt, lastSeen = "firing", started.Add(50*time.Minute), time.Now().UTC()
	}
	summary := fmt.Sprintf("%s%s 在 %s 上重复触发", demoMessage, script.Name, script.Resource)
	if _, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO alert_instances(id,cluster_id,fingerprint,name,severity,namespace,resource_kind,resource_name,summary,state,source,starts_at,last_seen_at,ends_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, t.ID, demoPrefix+script.Name, script.Name, script.Severity, script.Namespace, script.Kind, script.Resource, summary, state, "cmb-monitoring/alertmanager", stamp(started), stamp(lastSeen), stampUnlessOpen(endsAt, keepFiring)); err != nil {
		return err
	}
	labels, _ := json.Marshal(map[string]string{"alertname": script.Name, "severity": script.Severity, "namespace": script.Namespace, "demo": "ftc-report"})
	_, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO alert_metadata(alert_id,labels_json,description,runbook_url,rule_url,owner_kind,owner_name,source_type,occurrence_count) VALUES(?,?,?,?,?,?,?,?,?)`,
		id, string(labels), summary, "", "", script.Kind, script.Resource, script.SourceType, script.Firing)
	return err
}

func stampUnlessOpen(value time.Time, open bool) string {
	if open {
		return ""
	}
	return stamp(value)
}

// ---- capacity ------------------------------------------------------------

func seedCapacity(ctx context.Context, db *sql.DB, targets []target, weekStart time.Time) error {
	for _, t := range targets {
		cpuAllocatable := int64(t.Cores * 1000)
		memoryAllocatable := int64(t.Workers) * 32 << 30
		cpuRequests := int64(float64(cpuAllocatable) * t.CPURequestRatio)
		memoryRequests := int64(float64(memoryAllocatable) * (t.CPURequestRatio + 0.06))
		cpuUsage := int64(float64(cpuAllocatable) * t.CPUUtilisation / 100)
		memoryUsage := int64(float64(memoryAllocatable) * t.MemoryUtilisation / 100)
		collected := weekStart.AddDate(0, 0, 6).Add(22 * time.Hour).UTC()
		capacity := cluster.ClusterCapacity{
			CollectedAt: collected, SystemNamespaces: []string{"default", "kube-node-lease", "kube-public", "kube-system"},
			WorkerNodes: t.Workers, CPUAllocatableMilli: cpuAllocatable, MemoryAllocatable: memoryAllocatable,
			SafetyReservePercent: 15, MetricsAvailable: true,
			Platform:     cluster.CapacityValues{CPURequestsMilli: cpuRequests / 10, MemoryRequests: memoryRequests / 10},
			Business:     cluster.CapacityValues{CPURequestsMilli: cpuRequests * 8 / 10, MemoryRequests: memoryRequests * 8 / 10, CPUUsageMilli: &cpuUsage, MemoryUsage: &memoryUsage},
			Unclassified: cluster.CapacityValues{CPURequestsMilli: cpuRequests / 9, MemoryRequests: memoryRequests / 9},
		}
		payload, err := json.Marshal(capacity)
		if err != nil {
			return err
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO fleet_capacity_snapshots(cluster_id,captured_at,last_attempt_at,payload,failure_code) VALUES(?,?,?,?,?) ON CONFLICT(cluster_id) DO UPDATE SET captured_at=excluded.captured_at,last_attempt_at=excluded.last_attempt_at,payload=excluded.payload,failure_code=''`,
			t.ID, stamp(collected), stamp(collected), string(payload), ""); err != nil {
			return err
		}
	}
	return nil
}

// ---- security ------------------------------------------------------------

func seedSecurityHistory(ctx context.Context, db *sql.DB, targets []target, weekStart time.Time) error {
	for index, t := range targets {
		for run := 0; run < 3; run++ {
			started := weekStart.AddDate(0, 0, 1+run*2).Add(time.Duration(index+2) * time.Hour).UTC()
			status, message := "completed", ""
			if run == 1 && index%2 == 0 {
				status, message = "failed", demoMessage+"kube-bench Job exceeded pod startup timeout"
			}
			if _, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO security_baseline_history(cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message) VALUES(?,?,?,?,?,?,?,?)`,
				t.ID, status, "kube-bench", "CIS", stamp(started), stamp(started.Add(8*time.Minute)), `{}`, message); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- housekeeping --------------------------------------------------------

func clearDemo(ctx context.Context, db *sql.DB) error {
	statements := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM alert_metadata WHERE alert_id LIKE ?`, []any{demoPrefix + "%"}},
		{`DELETE FROM alert_state_transitions WHERE id LIKE ?`, []any{demoPrefix + "%"}},
		{`DELETE FROM alert_instances WHERE id LIKE ?`, []any{demoPrefix + "%"}},
		{`DELETE FROM alert_rollup_hourly WHERE fingerprint LIKE ?`, []any{demoPrefix + "%"}},
		{`DELETE FROM alert_rollup_daily WHERE fingerprint LIKE ?`, []any{demoPrefix + "%"}},
		{`DELETE FROM alert_collection_history WHERE id LIKE ?`, []any{demoPrefix + "%"}},
		{`DELETE FROM cluster_events WHERE event_uid LIKE ?`, []any{demoPrefix + "%"}},
		{`DELETE FROM event_issue_hourly WHERE message_sample LIKE ?`, []any{demoMessage + "%"}},
		{`DELETE FROM event_daily WHERE message_sample LIKE ?`, []any{demoMessage + "%"}},
		{`DELETE FROM security_baseline_history WHERE error_message LIKE ?`, []any{demoMessage + "%"}},
		{`DELETE FROM security_jobs WHERE cluster_id LIKE ?`, []any{demoCluster + "%"}},
		{`DELETE FROM fleet_capacity_snapshots WHERE cluster_id LIKE ?`, []any{demoCluster + "%"}},
		{`DELETE FROM clusters WHERE id LIKE ?`, []any{demoCluster + "%"}},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}
	return nil
}

func enableReports(ctx context.Context, db *sql.DB) error {
	value := platformsettings.Defaults().Runtime
	var payload string
	err := db.QueryRowContext(ctx, `SELECT payload FROM platform_settings WHERE section='runtime'`).Scan(&payload)
	if err == nil {
		_ = json.Unmarshal([]byte(payload), &value)
	} else if err != sql.ErrNoRows {
		return err
	}
	value.OperationalReportsEnabled = true
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO platform_settings(section,payload,updated_at) VALUES('runtime',?,?) ON CONFLICT(section) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, string(raw), stamp(time.Now()))
	return err
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func digest(parts ...string) string {
	value := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(value[:])
}

func stamp(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func must(err error) {
	if err != nil {
		fatal(err.Error())
	}
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
