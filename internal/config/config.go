package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const defaultSessionTTL = 8 * time.Hour

type Config struct {
	CollectionShards          int
	HTTPQueryConcurrency      int
	HTTPRealtimeConcurrency   int
	HTTPMonitoringConcurrency int
	HTTPSessionConcurrency    int
	HTTPClusterConcurrency    int
	RuntimeRole               string
	ListenAddr                string
	TLSCertFile               string
	TLSKeyFile                string
	HealthAddr                string
	MetricsAddr               string
	DatabasePath              string
	PlatformDatabaseDSN       string
	DatabaseHost              string
	DatabasePort              string
	DatabaseUser              string
	DatabasePassword          string
	DatabaseName              string
	DatabaseTLS               string
	SetupMode                 bool
	SetupToken                string
	SetupRuntimeSecret        string
	SetupDefaultDBHost        string
	SetupDefaultDBName        string
	EventDatabaseDSN          string
	BootstrapTokenPath        string
	WebDistDir                string
	PodExecEnabled            bool
	AdminUsername             string
	AdminPasswordHash         string
	CredentialEncryptionKey   string
	SecureCookies             bool
	// AllowLoopbackWSOrigin re-enables cross-port loopback WebSocket origins
	// for local development. It must stay false outside a developer machine:
	// WebSocket handshakes are not covered by the same-origin policy, so an
	// allowed origin can drive Pod Exec and kubectl TTY on behalf of a session.
	AllowLoopbackWSOrigin bool
	// TrustProxyHeaders enables X-Forwarded-For parsing for login throttling.
	TrustProxyHeaders          bool
	SessionTTL                 time.Duration
	SystemNamespaces           []string
	HealthCollectionInterval   time.Duration
	HealthCollectionTimeout    time.Duration
	HealthCollectionWorkers    int
	HealthInstanceSource       string
	CapacityCollectionInterval time.Duration
	CapacityCollectionTimeout  time.Duration
	CapacityCollectionWorkers  int
	ResourceIndexInterval      time.Duration
	ResourceIndexTimeout       time.Duration
	ResourceIndexWorkers       int
	LeaderElectionEnabled      bool
	LeaderElectionNamespace    string
	LeaderElectionName         string
	SecurityScannerNamespace   string
	SecurityScannerPullSecret  string
	SecurityScanWorkers        int
	SecurityScanTimeout        time.Duration
	SecurityPodStartupTimeout  time.Duration
	KubeBenchImage             string
	KubeBenchImageAMD64        string
	KubeBenchImageARM64        string
	SecurityHostScanWorkers    int
}

// ServesAPI and Collects keep runtime role decisions in one place.
func (cfg Config) ServesAPI() bool {
	return cfg.RuntimeRole == "" || cfg.RuntimeRole == "all" || cfg.RuntimeRole == "api"
}
func (cfg Config) Collects() bool {
	return cfg.RuntimeRole == "" || cfg.RuntimeRole == "all" || cfg.RuntimeRole == "collector"
}
func (cfg Config) RunsWorker() bool {
	return cfg.RuntimeRole == "" || cfg.RuntimeRole == "all" || cfg.RuntimeRole == "worker"
}

func Load() (Config, error) {
	role := strings.ToLower(strings.TrimSpace(envOrDefault("PLATFORM_ROLE", "all")))
	if role != "all" && role != "api" && role != "collector" && role != "worker" {
		return Config{}, errors.New("PLATFORM_ROLE must be all, api, collector or worker")
	}

	querySlots, err := envPositiveInt("HTTP_QUERY_CONCURRENCY", 64)
	if err != nil {
		return Config{}, err
	}
	realtimeSlots, err := envPositiveInt("HTTP_REALTIME_CONCURRENCY", 20)
	if err != nil {
		return Config{}, err
	}
	monitoringSlots, err := envPositiveInt("HTTP_MONITORING_CONCURRENCY", 8)
	if err != nil {
		return Config{}, err
	}
	sessionSlots, err := envPositiveInt("HTTP_SESSION_CONCURRENCY", 10)
	if err != nil {
		return Config{}, err
	}
	clusterSlots, err := envPositiveInt("HTTP_CLUSTER_CONCURRENCY", 2)
	if err != nil {
		return Config{}, err
	}
	shardCount, err := strconv.Atoi(envOrDefault("COLLECTION_SHARDS", "0"))
	if err != nil || shardCount < 0 || shardCount > 64 {
		return Config{}, errors.New("COLLECTION_SHARDS must be 0 (legacy) or 1..64")
	}
	secureCookies, err := envBool("SECURE_COOKIES", true)
	if err != nil {
		return Config{}, err
	}
	podExecEnabled, err := envBool("ENABLE_POD_EXEC", false)
	if err != nil {
		return Config{}, err
	}
	allowLoopbackWSOrigin, err := envBool("ALLOW_LOOPBACK_WS_ORIGIN", false)
	if err != nil {
		return Config{}, err
	}
	trustProxyHeaders, err := envBool("TRUST_PROXY_HEADERS", false)
	if err != nil {
		return Config{}, err
	}
	sessionTTL, err := envDuration("SESSION_TTL", defaultSessionTTL)
	if err != nil {
		return Config{}, err
	}
	healthInterval, err := envDuration("HEALTH_COLLECTION_INTERVAL", time.Minute)
	if err != nil {
		return Config{}, err
	}
	healthTimeout, err := envDuration("HEALTH_COLLECTION_TIMEOUT", 20*time.Second)
	if err != nil {
		return Config{}, err
	}
	healthWorkers, err := envPositiveInt("HEALTH_COLLECTION_WORKERS", 5)
	if err != nil {
		return Config{}, err
	}
	healthInstanceSource := strings.ToLower(envOrDefault("HEALTH_INSTANCE_SOURCE", "pods"))
	if healthInstanceSource != "pods" && healthInstanceSource != "workloads" {
		return Config{}, errors.New("HEALTH_INSTANCE_SOURCE must be pods or workloads")
	}
	capacityInterval, err := envDuration("CAPACITY_COLLECTION_INTERVAL", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	capacityTimeout, err := envDuration("CAPACITY_COLLECTION_TIMEOUT", 45*time.Second)
	if err != nil {
		return Config{}, err
	}
	capacityWorkers, err := envPositiveInt("CAPACITY_COLLECTION_WORKERS", 3)
	if err != nil {
		return Config{}, err
	}
	resourceIndexInterval, err := envDuration("RESOURCE_INDEX_INTERVAL", 10*time.Minute)
	if err != nil {
		return Config{}, err
	}
	resourceIndexTimeout, err := envDuration("RESOURCE_INDEX_TIMEOUT", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	resourceIndexWorkers, err := envPositiveInt("RESOURCE_INDEX_WORKERS", 2)
	if err != nil {
		return Config{}, err
	}
	securityScanWorkers, err := envPositiveInt("SECURITY_SCAN_WORKERS", 2)
	if err != nil {
		return Config{}, err
	}
	securityHostScanWorkers, err := envPositiveInt("SECURITY_HOST_SCAN_WORKERS", 5)
	if err != nil {
		return Config{}, err
	}
	securityScanTimeout, err := envDuration("SECURITY_SCAN_TIMEOUT", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	securityPodStartupTimeout, err := envDuration("SECURITY_POD_STARTUP_TIMEOUT", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	leaderElectionEnabled, err := envBool("LEADER_ELECTION_ENABLED", true)
	if err != nil {
		return Config{}, err
	}
	setupMode, err := envBool("SETUP_MODE", false)
	if err != nil {
		return Config{}, err
	}

	databasePort, err := envPositiveInt("PLATFORM_DB_PORT", 3306)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		CollectionShards:     shardCount,
		HTTPQueryConcurrency: querySlots, HTTPRealtimeConcurrency: realtimeSlots, HTTPMonitoringConcurrency: monitoringSlots, HTTPSessionConcurrency: sessionSlots, HTTPClusterConcurrency: clusterSlots,
		RuntimeRole: role,
		ListenAddr:  envOrDefault("LISTEN_ADDR", ":8080"),
		TLSCertFile: os.Getenv("TLS_CERT_FILE"), TLSKeyFile: os.Getenv("TLS_KEY_FILE"), HealthAddr: os.Getenv("HEALTH_ADDR"),
		MetricsAddr:                os.Getenv("METRICS_ADDR"),
		DatabasePath:               envOrDefault("DATABASE_PATH", "./data/platform.db"),
		PlatformDatabaseDSN:        os.Getenv("PLATFORM_DATABASE_DSN"),
		DatabaseHost:               strings.TrimSpace(os.Getenv("PLATFORM_DB_HOST")),
		DatabasePort:               strconv.Itoa(databasePort),
		DatabaseUser:               os.Getenv("PLATFORM_DB_USER"),
		DatabasePassword:           os.Getenv("PLATFORM_DB_PASSWORD"),
		DatabaseName:               os.Getenv("PLATFORM_DB_NAME"),
		DatabaseTLS:                os.Getenv("PLATFORM_DB_TLS"),
		SetupMode:                  setupMode,
		SetupToken:                 os.Getenv("SETUP_TOKEN"),
		SetupRuntimeSecret:         envOrDefault("SETUP_RUNTIME_SECRET", "ftc-platform-runtime"),
		SetupDefaultDBHost:         envOrDefault("SETUP_DEFAULT_DB_HOST", "platform-mysql.ftc-platform.svc"),
		SetupDefaultDBName:         envOrDefault("SETUP_DEFAULT_DB_NAME", "ftc_platform"),
		EventDatabaseDSN:           os.Getenv("EVENT_DATABASE_DSN"),
		BootstrapTokenPath:         envOrDefault("BOOTSTRAP_TOKEN_PATH", "./data/bootstrap-token"),
		WebDistDir:                 os.Getenv("WEB_DIST_DIR"),
		PodExecEnabled:             podExecEnabled,
		AdminUsername:              envOrDefault("ADMIN_USERNAME", "admin"),
		AdminPasswordHash:          os.Getenv("ADMIN_PASSWORD_HASH"),
		CredentialEncryptionKey:    os.Getenv("CREDENTIAL_ENCRYPTION_KEY"),
		SecureCookies:              secureCookies,
		AllowLoopbackWSOrigin:      allowLoopbackWSOrigin,
		TrustProxyHeaders:          trustProxyHeaders,
		SessionTTL:                 sessionTTL,
		SystemNamespaces:           csvList(envOrDefault("SYSTEM_NAMESPACES", "kube-system,cmb-monitoring")),
		HealthCollectionInterval:   healthInterval,
		HealthCollectionTimeout:    healthTimeout,
		HealthCollectionWorkers:    healthWorkers,
		HealthInstanceSource:       healthInstanceSource,
		CapacityCollectionInterval: capacityInterval,
		CapacityCollectionTimeout:  capacityTimeout,
		CapacityCollectionWorkers:  capacityWorkers,
		ResourceIndexInterval:      resourceIndexInterval,
		ResourceIndexTimeout:       resourceIndexTimeout,
		ResourceIndexWorkers:       resourceIndexWorkers,
		LeaderElectionEnabled:      leaderElectionEnabled,
		LeaderElectionNamespace:    envOrDefault("POD_NAMESPACE", "kubescope"),
		LeaderElectionName:         envOrDefault("LEADER_ELECTION_NAME", "ftc-platform-collectors"),
		SecurityScannerNamespace:   envOrDefault("SECURITY_SCANNER_NAMESPACE", "kube-system"),
		SecurityScannerPullSecret:  strings.TrimSpace(os.Getenv("SECURITY_SCANNER_IMAGE_PULL_SECRET")),
		SecurityScanWorkers:        securityScanWorkers,
		SecurityScanTimeout:        securityScanTimeout,
		SecurityPodStartupTimeout:  securityPodStartupTimeout,
		KubeBenchImage:             envOrDefault("SECURITY_KUBE_BENCH_IMAGE", "docker.io/aquasec/kube-bench:v0.16.0"),
		KubeBenchImageAMD64:        strings.TrimSpace(os.Getenv("SECURITY_KUBE_BENCH_IMAGE_AMD64")),
		KubeBenchImageARM64:        strings.TrimSpace(os.Getenv("SECURITY_KUBE_BENCH_IMAGE_ARM64")),
		SecurityHostScanWorkers:    securityHostScanWorkers,
	}

	if cfg.PlatformDatabaseDSN == "" && cfg.DatabaseHost != "" {
		if cfg.DatabaseUser == "" || cfg.DatabaseName == "" {
			return Config{}, errors.New("PLATFORM_DB_USER and PLATFORM_DB_NAME are required when PLATFORM_DB_HOST is set")
		}
		mysqlConfig := mysql.NewConfig()
		mysqlConfig.User = cfg.DatabaseUser
		mysqlConfig.Passwd = cfg.DatabasePassword
		mysqlConfig.Net = "tcp"
		mysqlConfig.Addr = net.JoinHostPort(cfg.DatabaseHost, cfg.DatabasePort)
		mysqlConfig.DBName = cfg.DatabaseName
		mysqlConfig.ParseTime = true
		mysqlConfig.Loc = time.UTC
		mysqlConfig.Params = map[string]string{"charset": "utf8mb4"}
		if cfg.DatabaseTLS != "" {
			mysqlConfig.TLSConfig = cfg.DatabaseTLS
		}
		cfg.PlatformDatabaseDSN = mysqlConfig.FormatDSN()
	}

	if cfg.Collects() && cfg.CollectionShards > 0 && cfg.PlatformDatabaseDSN == "" {
		return Config{}, errors.New("sharded collection requires shared MySQL")
	}
	if cfg.Collects() && cfg.CollectionShards > 0 && cfg.HealthInstanceSource != "workloads" {
		return Config{}, errors.New("sharded collection requires HEALTH_INSTANCE_SOURCE=workloads")
	}
	setupWithoutDatabase := cfg.SetupMode && cfg.PlatformDatabaseDSN == ""
	if setupWithoutDatabase && cfg.SetupToken == "" {
		return Config{}, errors.New("SETUP_TOKEN is required when SETUP_MODE is enabled without a database")
	}
	if cfg.RuntimeRole != "all" && cfg.PlatformDatabaseDSN == "" && !(cfg.RuntimeRole == "api" && setupWithoutDatabase) {
		return Config{}, errors.New("split roles require PLATFORM_DATABASE_DSN or PLATFORM_DB_HOST")
	}
	if cfg.AdminPasswordHash == "" && cfg.ServesAPI() && !setupWithoutDatabase {
		return Config{}, errors.New("ADMIN_PASSWORD_HASH is required")
	}
	if (cfg.TLSCertFile == "") != (cfg.TLSKeyFile == "") {
		return Config{}, errors.New("TLS_CERT_FILE and TLS_KEY_FILE must both be configured")
	}
	if cfg.CredentialEncryptionKey == "" && !setupWithoutDatabase {
		return Config{}, errors.New("CREDENTIAL_ENCRYPTION_KEY is required")
	}

	return cfg, nil
}

func envPositiveInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func csvList(value string) []string {
	items := make([]string, 0)
	seen := make(map[string]struct{})
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		items = append(items, item)
	}
	return items
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return parsed, nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}
	return parsed, nil
}
