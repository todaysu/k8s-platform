package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"example.com/k8s-platform/internal/auth"
	"example.com/k8s-platform/internal/cluster"
	"example.com/k8s-platform/internal/config"
	"example.com/k8s-platform/internal/httpapi"
	"example.com/k8s-platform/internal/leadership"
	"example.com/k8s-platform/internal/store"
)

const platformVersion = "0.1.0-dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
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
	cipher, err := cluster.NewAESGCMCipher(cfg.CredentialEncryptionKey)
	if err != nil {
		log.Fatal(err)
	}
	manager := cluster.NewOnboardingService(
		cluster.NewKubernetesConnector(20*time.Second),
		cipher,
		database,
	)
	manager.SetSystemNamespaces(cfg.SystemNamespaces)
	var eventRepository cluster.EventRepository = database
	var closeEvents func() error
	if cfg.PlatformDatabaseDSN == "" && cfg.EventDatabaseDSN != "" {
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

	authentication, err := auth.NewWithDB(database.SQLDB(), cfg.BootstrapTokenPath, cfg.AdminUsername, cfg.AdminPasswordHash, cfg.SecureCookies, cfg.SessionTTL)
	if err != nil {
		log.Fatal(err)
	}
	defer authentication.Close()
	applicationContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go leadership.Run(applicationContext, leadership.Config{
		Enabled: cfg.LeaderElectionEnabled, Namespace: cfg.LeaderElectionNamespace,
		Name: cfg.LeaderElectionName, Identity: os.Getenv("POD_NAME"),
	}, func(leaderContext context.Context) {
		var collectors sync.WaitGroup
		collectors.Add(4)
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
			cluster.NewEventCollector(manager, eventRepository).Run(leaderContext)
		}()
		go func() {
			defer collectors.Done()
			cluster.NewResourceIndexCollector(manager, database, cfg.ResourceIndexInterval, cfg.ResourceIndexWorkers, cfg.ResourceIndexTimeout).Run(leaderContext)
		}()
		collectors.Wait()
	})
	apiHandler := authentication.Wrap(httpapi.NewRouterWithOptions(
		platformVersion,
		httpapi.RouterOptions{PodExecEnabled: cfg.PodExecEnabled},
		manager,
	))
	var handler http.Handler = apiHandler
	if cfg.WebDistDir != "" {
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

	log.Printf("platform listening on %s", cfg.ListenAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
