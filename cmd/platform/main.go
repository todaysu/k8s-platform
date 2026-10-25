package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"example.com/k8s-platform/internal/alerts"
	"example.com/k8s-platform/internal/auth"
	"example.com/k8s-platform/internal/cluster"
	"example.com/k8s-platform/internal/config"
	"example.com/k8s-platform/internal/httpapi"
	"example.com/k8s-platform/internal/leadership"
	"example.com/k8s-platform/internal/metrics"
	"example.com/k8s-platform/internal/platformsettings"
	"example.com/k8s-platform/internal/reports"
	"example.com/k8s-platform/internal/runtimecontrol"
	"example.com/k8s-platform/internal/setup"
	"example.com/k8s-platform/internal/store"
	"example.com/k8s-platform/internal/tlsserver"
)

var platformVersion = "v1.0.1"

type alertCollectorSource struct {
	repository cluster.ClusterRepository
	manager    *cluster.OnboardingService
}

func (source alertCollectorSource) ListRecords(ctx context.Context) ([]cluster.ClusterRecord, error) {
	return source.repository.ListRecords(ctx)
}
func (source alertCollectorSource) GetClusterAlerts(ctx context.Context, id string) (cluster.AlertSnapshot, error) {
	return source.manager.GetClusterAlerts(ctx, id)
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "renew-certificate" {
		if err := setup.RunCertificateRenewal(context.Background()); err != nil {
			log.Fatal(err)
		}
		return
	}
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	if cfg.SetupMode && cfg.PlatformDatabaseDSN == "" {
		runSetupServer(cfg)
		return
	}
	var database store.PlatformStore
	if cfg.PlatformDatabaseDSN != "" {
		database, err = store.OpenMySQL(cfg.PlatformDatabaseDSN)
	} else {
		database, err = store.OpenSQLite(cfg.DatabasePath)
	}
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	settingsContext, cancelSettings := context.WithTimeout(context.Background(), 5*time.Second)
	storedSettings, settingsErr := platformsettings.Load(settingsContext, database)
	cancelSettings()
	if settingsErr != nil {
		log.Printf("platform settings: using environment defaults because persisted settings could not be loaded: %v", settingsErr)
		storedSettings = platformsettings.Defaults()
	} else {
		cfg = applySavedPlatformSettings(cfg, storedSettings)
	}
	cipher, err := cluster.NewAESGCMCipher(cfg.CredentialEncryptionKey)
	if err != nil {
		log.Fatal(err)
	}
	connector := cluster.NewKubernetesConnectorWithInstanceSource(20*time.Second, cfg.HealthInstanceSource)
	connector.ConfigureHostSecurityScanner(cfg.KubeBenchImage, cfg.KubeBenchImageAMD64, cfg.KubeBenchImageARM64, cfg.SecurityScannerNamespace, cfg.SecurityScannerPullSecret, cfg.SecurityHostScanWorkers)
	connector.SetSecurityPodStartupTimeout(cfg.SecurityPodStartupTimeout)
	manager := cluster.NewOnboardingService(
		connector,
		cipher,
		database,
	)
	manager.SetSystemNamespaces(cfg.SystemNamespaces)
	manager.SetSecurityScanWorkers(cfg.SecurityScanWorkers)
	manager.SetSecurityScanTimeout(cfg.SecurityScanTimeout)
	manager.EnableQueuedSecurityScans()
	var eventRepository cluster.EventRepository = database
	var closeEvents func() error
	if cfg.EventDatabaseDSN != "" {
		mysqlEvents, openErr := store.OpenMySQLEvents(cfg.EventDatabaseDSN)
		if openErr != nil {
			log.Fatal(openErr)
		}
		eventRepository = mysqlEvents
		closeEvents = mysqlEvents.Close
	}
	if closeEvents != nil {
		defer func() { _ = closeEvents() }()
	}
	manager.SetEventRepository(eventRepository)

	applicationContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	alertsContext, cancelAlerts := context.WithTimeout(applicationContext, 10*time.Second)
	alertService, alertsErr := alerts.New(alertsContext, database.SQLDB(), alertCollectorSource{database, manager})
	cancelAlerts()
	if alertsErr != nil {
		log.Fatalf("initialize alerts: %v", alertsErr)
	}
	alertService.ConfigureRetention(storedSettings.Alerts.ResolvedRetentionDays, storedSettings.Alerts.CleanupBatchSize)
	if cfg.RunsWorker() {
		go manager.RunSecurityWorker(applicationContext, os.Getenv("POD_NAME"))
	}
	if cfg.Collects() {
		check, cancel := context.WithTimeout(applicationContext, 5*time.Second)
		err := database.VerifyCollectionMode(check, cfg.CollectionShards)
		cancel()
		if err != nil {
			log.Fatalf("collector mode check failed: %v", err)
		}
		runCollectors := func(leaderContext context.Context) {
			var collectors sync.WaitGroup
			collectors.Add(5)
			go func() { defer collectors.Done(); alertService.Run(leaderContext) }()
			go func() {
				defer collectors.Done()
				cluster.NewHealthCollector(manager, cfg.HealthCollectionInterval, cfg.HealthCollectionWorkers, cfg.HealthCollectionTimeout).Run(leaderContext)
			}()
			go func() {
				defer collectors.Done()
				cluster.NewCapacityCollector(manager, cfg.CapacityCollectionInterval, cfg.CapacityCollectionWorkers, cfg.CapacityCollectionTimeout).Run(leaderContext)
			}()
			go func() {
				defer collectors.Done()
				eventCollector := cluster.NewEventCollector(manager, eventRepository)
				if storedSettings.UpdatedAt[platformsettings.SectionEvents] != "" {
					eventCollector.ConfigureRetention(
						time.Duration(storedSettings.Events.NormalRetentionDays)*24*time.Hour,
						time.Duration(storedSettings.Events.WarningRetentionDays)*24*time.Hour,
						time.Duration(storedSettings.Events.RollupRetentionDays)*24*time.Hour,
						time.Duration(storedSettings.Events.DailyRetentionDays)*24*time.Hour,
						storedSettings.Events.CleanupBatchSize,
					)
				}
				eventCollector.Run(leaderContext)
			}()
			go func() {
				defer collectors.Done()
				cluster.NewResourceIndexCollector(manager, database, cfg.ResourceIndexInterval, cfg.ResourceIndexWorkers, cfg.ResourceIndexTimeout).Run(leaderContext)
			}()
			collectors.Wait()
		}
		if cfg.CollectionShards > 0 {
			go cluster.RunCollectionShards(applicationContext, database, cfg.CollectionShards, runCollectors)
		} else {
			go leadership.Run(applicationContext, leadership.Config{Enabled: cfg.LeaderElectionEnabled, Namespace: cfg.LeaderElectionNamespace, Name: cfg.LeaderElectionName, Identity: os.Getenv("POD_NAME")}, runCollectors)
		}
	}

	var apiHandler http.Handler
	if cfg.ServesAPI() {
		authentication, err := auth.NewWithDB(database.SQLDB(), cfg.BootstrapTokenPath, cfg.AdminUsername, cfg.AdminPasswordHash, cfg.SecureCookies, cfg.SessionTTL)
		if err != nil {
			log.Fatal(err)
		}
		defer authentication.Close()
		// Only trust X-Forwarded-For when the deployment puts a proxy in front;
		// otherwise any client could forge it and defeat login throttling.
		authentication.TrustProxyHeaders(cfg.TrustProxyHeaders)
		// The loopback WebSocket origin exception is for local SPA development
		// and must stay disabled in deployed environments.
		httpapi.SetLoopbackWebSocketOrigin(cfg.AllowLoopbackWSOrigin)
		restarter, restartErr := runtimecontrol.NewInCluster(cfg.LeaderElectionNamespace)
		if restartErr != nil {
			log.Printf("platform settings: automatic Kubernetes restart disabled: %v", restartErr)
		}
		eventDBProvider, ok := eventRepository.(interface{ SQLDB() *sql.DB })
		if !ok {
			log.Fatal("event repository does not expose its database connection")
		}
		reportContext, cancelReports := context.WithTimeout(applicationContext, 10*time.Second)
		reportService, reportErr := reports.New(
			reportContext,
			database.SQLDB(),
			eventDBProvider.SQLDB(),
			cfg.PlatformDatabaseDSN == "",
			cfg.PlatformDatabaseDSN == "" && cfg.EventDatabaseDSN == "",
			database,
			cipher,
		)
		cancelReports()
		if reportErr != nil {
			log.Fatalf("initialize operational reports: %v", reportErr)
		}
		routerOptions := httpapi.RouterOptions{PodExecEnabled: cfg.PodExecEnabled, Reports: reportService, Alerts: alertService, Authenticator: authentication, SettingsRepository: database, Admission: httpapi.AdmissionLimits{Queries: cfg.HTTPQueryConcurrency, Realtime: cfg.HTTPRealtimeConcurrency, Monitoring: cfg.HTTPMonitoringConcurrency, Sessions: cfg.HTTPSessionConcurrency, PerCluster: cfg.HTTPClusterConcurrency}}
		if restartErr == nil {
			routerOptions.SettingsRestarter = restarter
		}
		if quotas, ok := database.(cluster.RequestQuotaRepository); ok {
			routerOptions.QuotaRepository = quotas
		}
		apiHandler = metrics.Middleware(authentication.Wrap(httpapi.NewRouterWithOptions(platformVersion, routerOptions, manager)))
	} else {
		apiHandler = collectorHealth(database.SQLDB())
	}
	var handler http.Handler = apiHandler
	if cfg.ServesAPI() && cfg.WebDistDir != "" {
		mux := http.NewServeMux()
		mux.Handle("/api/", apiHandler)
		mux.Handle("/", httpapi.NewSPAHandler(cfg.WebDistDir))
		handler = mux
	}
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-applicationContext.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()

	if cfg.MetricsAddr != "" {
		metricsServer := &http.Server{Addr: cfg.MetricsAddr, Handler: metrics.Default.Handler(), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			<-applicationContext.Done()
			shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = metricsServer.Shutdown(shutdownContext)
		}()
		go func() {
			log.Printf("metrics listening on %s", cfg.MetricsAddr)
			if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("metrics server stopped: %v", err)
			}
		}()
	}

	log.Printf("platform listening on %s", cfg.ListenAddr)
	if cfg.HealthAddr != "" {
		startInternalHealth(applicationContext, cfg.HealthAddr, database.SQLDB())
	}
	if cfg.TLSCertFile != "" && cfg.ServesAPI() {
		server.TLSConfig, err = tlsserver.FromFiles(applicationContext, cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			log.Fatal(err)
		}
		err = server.ListenAndServeTLS("", "")
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func runSetupServer(cfg config.Config) {
	setupServer, err := setup.New(setup.Config{
		Token:         cfg.SetupToken,
		Namespace:     cfg.LeaderElectionNamespace,
		RuntimeSecret: cfg.SetupRuntimeSecret,
		DefaultDBHost: cfg.SetupDefaultDBHost,
		DefaultDBName: cfg.SetupDefaultDBName,
		SecureCookies: cfg.SecureCookies,
		SessionTTL:    cfg.SessionTTL,
	})
	if err != nil {
		log.Fatal(err)
	}
	tlsConfig, err := setupServer.BootstrapTLS(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/health", setupServer)
	mux.Handle("/api/v1/setup/", setupServer)
	if cfg.WebDistDir != "" {
		mux.Handle("/", httpapi.NewSPAHandler(cfg.WebDistDir))
	} else {
		mux.HandleFunc("/", func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "setup API only: WEB_DIST_DIR is not set", http.StatusNotFound)
		})
	}
	applicationContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Addr: cfg.ListenAddr, Handler: mux, TLSConfig: tlsConfig, ReadHeaderTimeout: 5 * time.Second}
	if cfg.HealthAddr != "" {
		startInternalHealth(applicationContext, cfg.HealthAddr, nil)
	}
	go func() {
		<-applicationContext.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()
	log.Printf("setup server listening on %s", cfg.ListenAddr)
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
