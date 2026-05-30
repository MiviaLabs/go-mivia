package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/httpapi"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/mcpapi"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/service"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/store"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/health"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/httpserver"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/logging"
	sqliteplatform "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite/schema"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectingestion"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
	projecthttpapi "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry/httpapi"
	projectstore "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry/store"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research"
	researchhttpapi "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research/httpapi"
	researchstore "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research/store"
)

const serviceName = "agent-server"

func main() {
	if err := run(); err != nil {
		logger := logging.New(serviceName)
		logger.Error("service stopped", slog.String("error_category", "startup"), slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.New(serviceName)
	ctx := context.Background()

	sqliteDB, err := sqliteplatform.Open(cfg.SQLitePath)
	if err != nil {
		return err
	}
	defer sqliteDB.Close()
	if err := sqliteschema.Bootstrap(ctx, sqliteDB.SQLDB()); err != nil {
		return err
	}

	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(ctx, ladybugschema.BootstrapSchema()); err != nil {
		return err
	}
	projectRegistry, err := projectregistry.NewRegistry(cfg.Projects, projectregistry.Options{
		LadybugPath:                  cfg.LadybugPath,
		SQLitePath:                   cfg.SQLitePath,
		ContentGraphEnabled:          cfg.Ingestion.ContentGraphEnabled,
		LiveUpdatesEnabled:           cfg.Ingestion.LiveUpdatesEnabled,
		ContentGraphApprovalAccepted: true,
	})
	if err != nil {
		return err
	}
	if err := projectstore.NewSQLiteStore(sqliteDB.SQLDB()).SaveProjects(ctx, projectRegistry.List()); err != nil {
		return err
	}
	if cfg.Ingestion.ContentGraphEnabled {
		if err := projectingestion.ValidateDefaultExtractorRegistry(); err != nil {
			return err
		}
	}
	projectPersistentGraph, err := ladybug.OpenPersistentGraph(cfg.LadybugPath)
	if err != nil {
		return err
	}
	projectGraph := projectregistry.NewProjectGraphRouter(projectRegistry, ladybug.NewMemoryGraph(), projectPersistentGraph)
	if err := projectGraph.Bootstrap(ctx, ladybugschema.BootstrapSchema()); err != nil {
		return err
	}
	agentStore := store.NewLadybugStore(graph)
	researchService := research.NewService(researchstore.NewLadybugMetadataStore(graph))
	projectDigestService := projectregistry.NewDigestService(projectRegistry, projectGraph)
	projectIngestionService := projectingestion.NewService(projectRegistry, projectingestion.NewGraphStore(projectGraph), projectingestion.NewSQLiteStore(sqliteDB.SQLDB()))
	projectIngestionService.SetFullScanBatchSize(cfg.Ingestion.FullScanBatchSize)
	projectIngestionService.SetFullScanWorkerCount(cfg.Ingestion.PerProjectWorkerLimit)
	projectIngestionService.SetExtractorCacheEnabled(cfg.Ingestion.ExtractorCacheEnabled)
	if failed, err := projectIngestionService.FailInterruptedRuns(ctx, "server_restarted"); err != nil {
		return err
	} else if failed > 0 {
		logger.Warn("failed interrupted ingestion runs after server restart", slog.Int("run_count", failed))
	}
	projectIngestionScheduler := projectingestion.NewScheduler(projectIngestionService, projectingestion.SchedulerOptions{
		QueueDepth:            cfg.Ingestion.QueueDepth,
		GlobalWorkerCount:     cfg.Ingestion.GlobalWorkerCount,
		PerProjectWorkerLimit: cfg.Ingestion.PerProjectWorkerLimit,
		LivePathPriority:      cfg.Ingestion.LivePathPriority,
	})
	if err := projectIngestionScheduler.Start(ctx); err != nil {
		return err
	}
	configStore := store.NewSQLiteConfigStore(sqliteDB.SQLDB())
	if err := configStore.SetRuntimeFlag(ctx, "research.live_providers_enabled", false, "disabled until provider ADR approval"); err != nil {
		return err
	}
	projectIngestionOrchestrator := projectingestion.NewOrchestrator(projectRegistry, projectIngestionScheduler, projectingestion.OrchestratorOptions{
		LiveUpdatesEnabled:       cfg.Ingestion.LiveUpdatesEnabled,
		DebounceInterval:         cfg.Ingestion.DebounceInterval,
		QueueDepth:               cfg.Ingestion.QueueDepth,
		WorkerCount:              cfg.Ingestion.WorkerCount,
		GlobalWorkerCount:        cfg.Ingestion.GlobalWorkerCount,
		PerProjectWorkerLimit:    cfg.Ingestion.PerProjectWorkerLimit,
		LivePathPriority:         cfg.Ingestion.LivePathPriority,
		InitialScanOnStart:       cfg.Ingestion.InitialScanOnStart,
		MaxWatchedDirectoryCount: cfg.Ingestion.MaxWatchedDirectoryCount,
		TaskWarnAfter:            cfg.Ingestion.TaskWarnAfter,
		Logger:                   logger,
	})
	if err := projectIngestionOrchestrator.Start(ctx); err != nil {
		return err
	}
	agentService := service.New(agentStore, agentStore)

	checker := health.NewChecker(
		health.Check{
			Name: "sqlite",
			Fn:   sqliteDB.Ping,
		},
		health.Check{
			Name: "ladybug_native",
			Fn: func(ctx context.Context) error {
				status := ladybug.NativeStatus()
				if status.Available {
					return nil
				}
				return health.DependencyUnavailable(status.Reason)
			},
			Optional: true,
		},
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.LivenessHandler)
	mux.Handle("GET /readyz", health.ReadinessHandler(checker, logger))
	httpapi.RegisterRoutes(mux, agentService)
	researchhttpapi.RegisterRoutes(mux, researchService)
	projecthttpapi.RegisterRoutesWithIngestion(mux, projectRegistry, projectDigestService, projectIngestionScheduler)
	mux.Handle("/mcp", mcpapi.NewHandlerWithResearchProjectsAndIngestion(agentService, researchService, projectRegistry, projectDigestService, projectIngestionScheduler, logger))

	handler := httpserver.Chain(
		mux,
		httpserver.RequestID,
		httpserver.Recover(logger),
		httpserver.Timeout(cfg.RequestTimeout),
		httpserver.MaxBytes(cfg.MaxRequestBytes),
	)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("service listening", slog.String("addr", cfg.HTTPAddr))
		errCh <- server.ListenAndServe()
	}()

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-signalCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := projectIngestionOrchestrator.Stop(shutdownCtx); err != nil {
			return err
		}
		if err := projectIngestionScheduler.Stop(shutdownCtx); err != nil {
			return err
		}
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		_ = projectIngestionOrchestrator.Stop(shutdownCtx)
		_ = projectIngestionScheduler.Stop(shutdownCtx)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
