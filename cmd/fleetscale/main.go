// fleetscale measures the central control plane against independent synthetic
// Kubernetes TLS endpoints. It never deploys workloads to real clusters.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"example.com/k8s-platform/internal/auth"
	"example.com/k8s-platform/internal/cluster"
	"example.com/k8s-platform/internal/httpapi"
	"example.com/k8s-platform/internal/store"
	mysql "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

type measurements struct {
	mu             sync.Mutex
	latency        map[string][]float64
	statuses       map[int]int
	peakHeap       atomic.Uint64
	peakGoroutines atomic.Int64
}

func (m *measurements) record(path string, status int, elapsed time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[status]++
	if len(m.latency[path]) < 100000 {
		m.latency[path] = append(m.latency[path], float64(elapsed.Microseconds())/1000)
	}
}
func (m *measurements) snapshot() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	routes := map[string]any{}
	for route, values := range m.latency {
		sort.Float64s(values)
		pct := func(p float64) float64 {
			if len(values) == 0 {
				return 0
			}
			return values[min(int(float64(len(values)-1)*p), len(values)-1)]
		}
		routes[route] = map[string]any{"samples": len(values), "p50_ms": pct(.5), "p95_ms": pct(.95), "p99_ms": pct(.99)}
	}
	return map[string]any{"routes": routes, "status_counts": m.statuses, "peak_go_heap_alloc_bytes": m.peakHeap.Load(), "peak_goroutines": m.peakGoroutines.Load()}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "scale verification failed:", err)
		os.Exit(1)
	}
}
func run() error {
	clusters := flag.Int("clusters", 100, "independent synthetic TLS endpoints (1..100)")
	pods := flag.Int("pods", 6000, "Pods per endpoint (1..25000)")
	nodes := flag.Int("nodes", 200, "Nodes per endpoint")
	workloads := flag.Int("workloads", 1000, "Deployments per endpoint")
	users := flag.Int("users", 10, "authenticated users (1..10)")
	duration := flag.Duration("duration", 30*time.Second, "mixed user load duration after warmup")
	warmTimeout := flag.Duration("warm-timeout", 8*time.Minute, "wait for complete initial capacity and index snapshots")
	fast := flag.Bool("fast-start", false, "use CollectOnce for accelerated warmup; does NOT verify production Run startup staggering")
	output := flag.String("output", "", "JSON report path (optional)")
	flag.Parse()
	if *clusters < 1 || *clusters > 100 || *pods < 1 || *pods > 25000 || *nodes < 1 || *nodes > 1000 || *workloads < 1 || *workloads > *pods || *users < 1 || *users > 10 || *duration < time.Second || *duration > 24*time.Hour {
		return errors.New("invalid scale bounds")
	}
	directory, err := os.MkdirTemp("", "ftc-fleetscale-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	var database store.PlatformStore
	backend := "sqlite (functional only)"
	if dsn := os.Getenv("FLEETSCALE_MYSQL_DSN"); dsn != "" {
		cfg, e := mysql.ParseDSN(dsn)
		if e != nil || len(cfg.DBName) < 10 || cfg.DBName[:10] != "ftc_scale_" {
			return errors.New("MySQL database must be a disposable ftc_scale_* database")
		}
		database, err = store.OpenMySQL(dsn)
		backend = "mysql"
	} else {
		database, err = store.OpenSQLite(directory + "/platform.db")
	}
	if err != nil {
		return err
	}
	defer database.Close()
	var count int
	if err = database.SQLDB().QueryRow(`SELECT COUNT(*) FROM clusters`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("test database is not empty; refusing to overwrite data")
	}
	seed := make([]byte, 32)
	if _, err = rand.Read(seed); err != nil {
		return err
	}
	cipher, err := cluster.NewAESGCMCipher(base64.StdEncoding.EncodeToString(seed))
	if err != nil {
		return err
	}
	sim := &simulator{pods: *pods, nodes: *nodes, workloads: *workloads}
	servers := []*httptest.Server{}
	defer func() {
		for _, server := range servers {
			server.Close()
		}
	}()
	for i := 0; i < *clusters; i++ {
		server := sim.server()
		servers = append(servers, server)
		id := fmt.Sprintf("scale-%03d", i)
		ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
		raw, _ := json.Marshal(map[string]any{"ca_data": ca, "token": "synthetic-only"})
		encrypted, e := cipher.Encrypt(raw, id)
		if e != nil {
			return e
		}
		now := time.Now().UTC()
		if _, err = database.Create(context.Background(), cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: id, Name: id, Environment: "test", ClusterType: "G01", TechnologyStack: "TCS", APIServer: server.URL, ConnectionStatus: "pending", CreatedAt: now, LastCheckedAt: now}, EncryptedCredentials: encrypted, Fingerprint: id}); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "seeded %d independent TLS endpoints, %d Nodes and %d Pods; warming collectors\n", *clusters, *clusters**nodes, *clusters**pods)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	connector := cluster.NewKubernetesConnectorWithInstanceSource(20*time.Second, "workloads")
	newManager := func() *cluster.OnboardingService {
		s := cluster.NewOnboardingService(connector, cipher, database)
		s.SetEventRepository(database)
		return s
	}
	managers := []*cluster.OnboardingService{newManager(), newManager()}
	runCollectors := func(manager *cluster.OnboardingService) func(context.Context) {
		return func(work context.Context) {
			health := cluster.NewHealthCollector(manager, time.Minute, 5, 20*time.Second)
			capacity := cluster.NewCapacityCollector(manager, 5*time.Minute, 3, 45*time.Second)
			index := cluster.NewResourceIndexCollector(manager, database, 10*time.Minute, 2, 30*time.Second)
			events := cluster.NewEventCollector(manager, database)
			var wg sync.WaitGroup
			wg.Add(4)
			go func() {
				defer wg.Done()
				if *fast {
					health.CollectOnce(work)
				}
				health.Run(work)
			}()
			go func() {
				defer wg.Done()
				if *fast {
					capacity.CollectOnce(work)
				}
				capacity.Run(work)
			}()
			go func() { defer wg.Done(); index.Run(work) }()
			go func() { defer wg.Done(); events.Run(work) }()
			wg.Wait()
		}
	}
	first, stopFirst := context.WithCancel(ctx)
	second, stopSecond := context.WithCancel(ctx)
	doneFirst := make(chan struct{})
	doneSecond := make(chan struct{})
	go func() {
		defer close(doneFirst)
		cluster.RunCollectionShards(first, database, 16, runCollectors(managers[0]))
	}()
	go func() {
		defer close(doneSecond)
		cluster.RunCollectionShards(second, database, 16, runCollectors(managers[1]))
	}()
	defer func() { stopFirst(); stopSecond(); <-doneFirst; <-doneSecond }()
	measurements := &measurements{latency: map[string][]float64{}, statuses: map[int]int{}}
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var mem runtime.MemStats
				runtime.ReadMemStats(&mem)
				for {
					old := measurements.peakHeap.Load()
					if mem.HeapAlloc <= old || measurements.peakHeap.CompareAndSwap(old, mem.HeapAlloc) {
						break
					}
				}
				n := int64(runtime.NumGoroutine())
				for {
					old := measurements.peakGoroutines.Load()
					if n <= old || measurements.peakGoroutines.CompareAndSwap(old, n) {
						break
					}
				}
			}
		}
	}()
	started := time.Now()
	deadline := started.Add(*warmTimeout)
	expected := int64(*clusters * (*pods + *workloads))
	warmed := false
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var indexed, capacities int64
		if err = database.SQLDB().QueryRow(`SELECT COUNT(*) FROM cluster_resources`).Scan(&indexed); err != nil {
			return err
		}
		if err = database.SQLDB().QueryRow(`SELECT COUNT(*) FROM fleet_capacity_snapshots WHERE payload IS NOT NULL AND failure_code=''`).Scan(&capacities); err != nil {
			return err
		}
		if indexed >= expected && capacities == int64(*clusters) {
			warmed = true
			break
		}
		fmt.Fprintf(os.Stderr, "warmup: resources=%d/%d capacity=%d/%d elapsed=%s\n", indexed, expected, capacities, *clusters, time.Since(started).Round(time.Second))
		time.Sleep(5 * time.Second)
	}
	if !warmed {
		return errors.New("initial collection did not complete before warm timeout")
	}
	warmDuration := time.Since(started)
	// Two logical API instances, independent managers/caches and shared SQL quotas.
	apiServers := []*httptest.Server{}
	defer func() {
		for _, server := range apiServers {
			server.Close()
		}
	}()
	hash, _ := bcrypt.GenerateFromPassword([]byte("ScaleTestOnly-NotAProductionPassword"), bcrypt.MinCost)
	authService, err := auth.NewWithDB(database.SQLDB(), directory+"/bootstrap", "scale-admin", string(hash), false, time.Hour)
	if err != nil {
		return err
	}
	defer authService.Close()
	for i := 0; i < 2; i++ {
		apiDatabase := database
		if dsn := os.Getenv("FLEETSCALE_MYSQL_DSN"); dsn != "" {
			independent, e := store.OpenMySQL(dsn)
			if e != nil {
				return e
			}
			defer independent.Close()
			apiDatabase = independent
		}
		manager := cluster.NewOnboardingService(connector, cipher, apiDatabase)
		manager.SetEventRepository(apiDatabase)
		apiAuth, e := auth.NewWithDB(apiDatabase.SQLDB(), directory+"/bootstrap", "scale-admin", string(hash), false, time.Hour)
		if e != nil {
			return e
		}
		defer apiAuth.Close()
		handler := apiAuth.Wrap(httpapi.NewRouterWithOptions("scale-test", httpapi.RouterOptions{QuotaRepository: apiDatabase}, manager))
		apiServers = append(apiServers, httptest.NewServer(handler))
	}
	cookies := make([]string, *users)
	for i := 0; i < *users; i++ {
		result, e := database.SQLDB().Exec(`INSERT INTO auth_users(username,password_hash,role,disabled) VALUES(?,?,'admin',false)`, fmt.Sprintf("scale-user-%02d", i), string(hash))
		if e != nil {
			return e
		}
		id, e := result.LastInsertId()
		if e != nil {
			return e
		}
		token := make([]byte, 32)
		rand.Read(token)
		cookie := hex.EncodeToString(token)
		digest := sha256.Sum256([]byte(cookie))
		cookies[i] = cookie
		if _, err = database.SQLDB().Exec(`INSERT INTO auth_sessions(token_hash,user_id,expires_at) VALUES(?,?,?)`, hex.EncodeToString(digest[:]), id, time.Now().Add(time.Hour).Unix()); err != nil {
			return err
		}
	}
	paths := []string{"/api/v1/clusters", "/api/v1/capacity", "/api/v1/search/resources?q=java-api", "/api/v1/events?view=issues&range=24h"}
	now := time.Now().UTC().Truncate(time.Minute)
	monitorPath := func(id int) string {
		return fmt.Sprintf("/api/v1/clusters/scale-%03d/monitoring?view=pods&namespace=app-00&start=%s&end=%s", id, url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339)), url.QueryEscape(now.Format(time.RFC3339)))
	}
	paths = append(paths, monitorPath(0))
	client := &http.Client{Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	load, stopLoad := context.WithTimeout(ctx, *duration)
	defer stopLoad()
	var clients sync.WaitGroup
	for user := 0; user < *users; user++ {
		clients.Add(1)
		go func(user int) {
			defer clients.Done()
			for n := 0; load.Err() == nil; n++ {
				path := paths[(n+user)%len(paths)]
				if (n+user)%len(paths) == len(paths)-1 {
					path = monitorPath(user % *clusters)
				}
				name := []string{"clusters", "capacity", "search", "events", "monitoring"}[(n+user)%len(paths)]
				req, _ := http.NewRequestWithContext(load, "GET", apiServers[user%2].URL+path, nil)
				req.AddCookie(&http.Cookie{Name: "kubescope_session", Value: cookies[user]})
				begin := time.Now()
				response, e := client.Do(req)
				if load.Err() != nil {
					if response != nil {
						response.Body.Close()
					}
					return
				}
				status := 0
				if e == nil {
					status = response.StatusCode
					_, e = io.Copy(io.Discard, io.LimitReader(response.Body, 8<<20))
					response.Body.Close()
					if e != nil {
						status = 0
					}
				}
				measurements.record(name, status, time.Since(begin))
				timer := time.NewTimer(500 * time.Millisecond)
				select {
				case <-load.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}(user)
	}
	clients.Wait()
	fmt.Fprintln(os.Stderr, "mixed load complete; verifying graceful collector takeover")
	takeover := time.Now()
	stopFirst()
	<-doneFirst
	adopted := false
	for time.Since(takeover) < 60*time.Second {
		var owners, shards int
		if err = database.SQLDB().QueryRow(`SELECT COUNT(DISTINCT owner),COUNT(*) FROM collection_shards WHERE owner<>''`).Scan(&owners, &shards); err != nil {
			return err
		}
		if owners == 1 && shards == 16 {
			adopted = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	report := map[string]any{
		"scope":                                "synthetic Kubernetes/Prometheus endpoints; two logical API and collector instances in one Go process, not independent production Pods; no etcd/Prometheus TSDB or browser rendering certification",
		"database":                             backend,
		"api_database_pools_independent":       backend == "mysql",
		"gomaxprocs":                           runtime.GOMAXPROCS(0),
		"host_logical_cpus":                    runtime.NumCPU(),
		"clusters":                             *clusters,
		"nodes_per_cluster":                    *nodes,
		"pods_per_cluster":                     *pods,
		"deployments_per_cluster":              *workloads,
		"monitoring_distribution":              "one cluster per user (modulo cluster count); same-cluster overload is a separate scenario",
		"authenticated_users":                  *users,
		"initial_index_resources":              expected,
		"warmup_ms":                            warmDuration.Milliseconds(),
		"accelerated_initial_sweep":            *fast,
		"load_duration":                        duration.String(),
		"steady_load":                          measurements.snapshot(),
		"graceful_takeover":                    adopted,
		"takeover_ms":                          time.Since(takeover).Milliseconds(),
		"list_requests":                        sim.lists.Load(),
		"list_objects":                         sim.objects.Load(),
		"synthetic_response_bytes":             sim.bytes.Load(),
		"peak_inflight_cluster_wide_pod_lists": sim.podMax.Load(),
		"not_verified":                         []string{"24h stability", "abrupt process kill/real network partition", "event storm", "10 simultaneous real terminal sessions", "real 200-node Kubernetes API and real Prometheus", "browser rendering"},
	}

	failures := measurements.assessment(adopted)
	report["synthetic_check_failures"] = failures
	report["synthetic_checks_passed"] = len(failures) == 0
	encoded, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(encoded))
	if *output != "" {
		if err = os.WriteFile(*output, append(encoded, '\n'), 0600); err != nil {
			return err
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("synthetic checks failed: %v", failures)
	}
	return nil
}
