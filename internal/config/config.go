package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultSessionTTL = 8 * time.Hour

type Config struct {
	ListenAddr                 string
	MetricsAddr                string
	DatabasePath               string
	PlatformDatabaseDSN        string
	EventDatabaseDSN           string
	BootstrapTokenPath         string
	WebDistDir                 string
	PodExecEnabled             bool
	AdminUsername              string
	AdminPasswordHash          string
	CredentialEncryptionKey    string
	SecureCookies              bool
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
}

func Load() (Config, error) {
	secureCookies, err := envBool("SECURE_COOKIES", true)
	if err != nil {
		return Config{}, err
	}
	podExecEnabled, err := envBool("ENABLE_POD_EXEC", false)
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
	leaderElectionEnabled, err := envBool("LEADER_ELECTION_ENABLED", true)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		ListenAddr:                 envOrDefault("LISTEN_ADDR", ":8080"),
		MetricsAddr:                os.Getenv("METRICS_ADDR"),
		DatabasePath:               envOrDefault("DATABASE_PATH", "./data/platform.db"),
		PlatformDatabaseDSN:        os.Getenv("PLATFORM_DATABASE_DSN"),
		EventDatabaseDSN:           os.Getenv("EVENT_DATABASE_DSN"),
		BootstrapTokenPath:         envOrDefault("BOOTSTRAP_TOKEN_PATH", "./data/bootstrap-token"),
		WebDistDir:                 os.Getenv("WEB_DIST_DIR"),
		PodExecEnabled:             podExecEnabled,
		AdminUsername:              envOrDefault("ADMIN_USERNAME", "admin"),
		AdminPasswordHash:          os.Getenv("ADMIN_PASSWORD_HASH"),
		CredentialEncryptionKey:    os.Getenv("CREDENTIAL_ENCRYPTION_KEY"),
		SecureCookies:              secureCookies,
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
	}

	if cfg.AdminPasswordHash == "" {
		return Config{}, errors.New("ADMIN_PASSWORD_HASH is required")
	}
	if cfg.CredentialEncryptionKey == "" {
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
