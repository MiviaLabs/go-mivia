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
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectintegrations"
	integrationconfluence "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectintegrations/confluence"
	integrationjira "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectintegrations/jira"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
	projecthttpapi "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry/httpapi"
	projectstore "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry/store"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectworkspace"
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
	projectIntegrationStore := projectintegrations.NewSQLiteStore(sqliteDB.SQLDB())
	projectIntegrationRunner, err := projectintegrations.NewRunner(projectintegrations.RunnerOptions{
		Projects:           cfg.Projects,
		Store:              projectIntegrationStore,
		CredentialResolver: projectintegrations.NewCredentialResolver(),
		JiraClient:         newJiraPollerByProject(cfg.Projects),
		ConfluenceClient:   newConfluencePollerByProject(cfg.Projects),
	})
	if err != nil {
		return err
	}
	projectIntegrationService, err := projectintegrations.NewServiceWithOptions(cfg.Projects, projectIntegrationStore, projectintegrations.ServiceOptions{Runner: projectIntegrationRunner})
	if err != nil {
		return err
	}
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
	var projectWorkspaceService projectworkspace.API
	if cfg.Workspace.Enabled {
		projectWorkspaceService = projectworkspace.NewService(projectRegistry, projectIngestionScheduler, projectworkspace.Options{Enabled: true})
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
	projecthttpapi.RegisterRoutesWithWorkspace(mux, projectRegistry, projectDigestService, projectIngestionScheduler, projectWorkspaceService)
	mux.Handle("/mcp", mcpapi.NewHandlerWithResearchProjectsIngestionWorkspaceAndIntegrations(agentService, researchService, projectRegistry, projectDigestService, projectIngestionScheduler, projectWorkspaceService, projectIntegrationService, logger))

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

type jiraPollerByProject struct {
	pollers map[string]projectintegrations.JiraPoller
}

func newJiraPollerByProject(projects []config.Project) jiraPollerByProject {
	pollers := make(map[string]projectintegrations.JiraPoller)
	for _, project := range projects {
		if project.Integrations.Jira == nil {
			continue
		}
		cfg := *project.Integrations.Jira
		pollers[project.ID] = integrationjira.NewPoller(integrationjira.NewClient(integrationjira.Options{
			BaseURL: cfg.SiteURL,
			Timeout: cfg.ReadTimeout,
		}))
	}
	return jiraPollerByProject{pollers: pollers}
}

func (poller jiraPollerByProject) PollJira(ctx context.Context, credentials projectintegrations.Credentials, plan projectintegrations.JiraQueryPlan) (projectintegrations.PollResult, error) {
	projectPoller, ok := poller.pollers[plan.ProjectID]
	if !ok {
		return projectintegrations.PollResult{}, projectintegrations.ErrNotFound
	}
	return projectPoller.PollJira(ctx, credentials, plan)
}

type confluencePollerByProject struct {
	pollers map[string]projectintegrations.ConfluencePoller
}

func newConfluencePollerByProject(projects []config.Project) confluencePollerByProject {
	pollers := make(map[string]projectintegrations.ConfluencePoller)
	for _, project := range projects {
		if project.Integrations.Confluence == nil {
			continue
		}
		cfg := *project.Integrations.Confluence
		pollers[project.ID] = integrationconfluence.NewPoller(integrationconfluence.NewClient(integrationconfluence.Options{
			BaseURL: cfg.SiteURL,
			Timeout: cfg.ReadTimeout,
		}))
	}
	return confluencePollerByProject{pollers: pollers}
}

func (poller confluencePollerByProject) PollConfluence(ctx context.Context, credentials projectintegrations.Credentials, plan projectintegrations.ConfluenceQueryPlan) (projectintegrations.PollResult, error) {
	projectPoller, ok := poller.pollers[plan.ProjectID]
	if !ok {
		return projectintegrations.PollResult{}, projectintegrations.ErrNotFound
	}
	return projectPoller.PollConfluence(ctx, credentials, plan)
}
