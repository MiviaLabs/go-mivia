package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/agentactivity"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/httpapi"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/mcpapi"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/service"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/store"
	dashboardhttpapi "github.com/MiviaLabs/go-mivia/internal/dashboard/httpapi"
	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/platform/diagnostics"
	"github.com/MiviaLabs/go-mivia/internal/platform/health"
	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/platform/logging"
	sqliteplatform "github.com/MiviaLabs/go-mivia/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/go-mivia/internal/platform/sqlite/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	automationhttpapi "github.com/MiviaLabs/go-mivia/internal/projectautomation/httpapi"
	automationstore "github.com/MiviaLabs/go-mivia/internal/projectautomation/store"
	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
	confidencehttpapi "github.com/MiviaLabs/go-mivia/internal/projectconfidence/httpapi"
	confidencestore "github.com/MiviaLabs/go-mivia/internal/projectconfidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	evidencehttpapi "github.com/MiviaLabs/go-mivia/internal/projectevidence/httpapi"
	evidencestore "github.com/MiviaLabs/go-mivia/internal/projectevidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
	integrationconfluence "github.com/MiviaLabs/go-mivia/internal/projectintegrations/confluence"
	integrationjira "github.com/MiviaLabs/go-mivia/internal/projectintegrations/jira"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge"
	knowledgehttpapi "github.com/MiviaLabs/go-mivia/internal/projectknowledge/httpapi"
	knowledgestore "github.com/MiviaLabs/go-mivia/internal/projectknowledge/store"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
	projecthttpapi "github.com/MiviaLabs/go-mivia/internal/projectregistry/httpapi"
	projectstore "github.com/MiviaLabs/go-mivia/internal/projectregistry/store"
	"github.com/MiviaLabs/go-mivia/internal/projectreliability"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
	workflowhttpapi "github.com/MiviaLabs/go-mivia/internal/projectworkflow/httpapi"
	workflowstore "github.com/MiviaLabs/go-mivia/internal/projectworkflow/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workplanhttpapi "github.com/MiviaLabs/go-mivia/internal/projectworkplan/httpapi"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkspace"
	"github.com/MiviaLabs/go-mivia/internal/research"
	researchhttpapi "github.com/MiviaLabs/go-mivia/internal/research/httpapi"
	researchstore "github.com/MiviaLabs/go-mivia/internal/research/store"
)

const serviceName = "mivia-server"

func main() {
	if handled, exitCode := runCLI(os.Args[1:], os.Stdout, os.Stderr); handled {
		os.Exit(exitCode)
	}
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
	previousMaxProcs := runtime.GOMAXPROCS(cfg.CPUCount)

	logger, logCloser, err := logging.NewWithOptions(serviceName, logging.Options{
		FileEnabled: cfg.Logging.FileEnabled,
		FilePath:    cfg.Logging.FilePath,
	})
	if err != nil {
		return err
	}
	defer logCloser.Close()
	logger.Info("runtime CPU configuration applied", slog.Int("cpu_count", cfg.CPUCount), slog.Int("previous_gomaxprocs", previousMaxProcs))
	ctx := context.Background()

	sqliteDB, err := sqliteplatform.OpenWithOptions(cfg.SQLitePath, sqliteplatform.Options{
		WALEnabled:               cfg.SQLite.WALEnabled,
		BusyTimeout:              cfg.SQLite.BusyTimeout,
		Synchronous:              cfg.SQLite.Synchronous,
		CheckpointAfterIngestion: cfg.SQLite.CheckpointAfterIngestion,
	})
	if err != nil {
		return err
	}
	defer sqliteDB.Close()
	if err := sqliteschema.Bootstrap(ctx, sqliteDB.SQLDB()); err != nil {
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
	metadataPersistentGraph, err := ladybug.OpenPebbleGraph(ladybug.PebbleGraphPath(cfg.LadybugPath + ".metadata"))
	if err != nil {
		return err
	}
	projectPersistentGraphs, closeProjectGraphs, err := openProjectPersistentGraphs(projectRegistry, cfg.LadybugPath)
	if err != nil {
		return err
	}
	defer closeProjectGraphs()
	projectGraph := projectregistry.NewProjectScopedGraphRouter(projectRegistry, ladybug.NewMemoryGraph(), projectPersistentGraphs)
	if err := projectGraph.Bootstrap(ctx, ladybugschema.BootstrapSchema()); err != nil {
		return err
	}
	if err := metadataPersistentGraph.Bootstrap(ctx, ladybugschema.BootstrapSchema()); err != nil {
		return err
	}
	if err := workplanstore.MigrateLadybugMetadata(ctx, projectGraph, metadataPersistentGraph, automationProjectIDs(projectRegistry.List())); err != nil {
		return err
	}
	agentStore := store.NewLadybugStore(metadataPersistentGraph)
	researchService := research.NewService(researchstore.NewLadybugMetadataStore(metadataPersistentGraph))
	projectDigestService := projectregistry.NewDigestService(projectRegistry, projectGraph)
	projectIngestionStateStore := projectingestion.NewSQLiteStore(sqliteDB.SQLDB())
	projectSearchBackends, closeProjectSearchStores, err := openProjectSearchStores(projectRegistry, cfg.LadybugPath, cfg.SQLite, projectIngestionStateStore)
	if err != nil {
		return err
	}
	defer closeProjectSearchStores()
	projectSearchStore := projectingestion.NewProjectScopedSearchStoreRouter(projectRegistry, projectIngestionStateStore, projectSearchBackends)
	projectIngestionService := projectingestion.NewService(projectRegistry, projectingestion.NewGraphStore(projectGraph), projectIngestionStateStore)
	projectIngestionService.SetSearchStore(projectSearchStore)
	projectIngestionService.SetFullScanBatchSize(cfg.Ingestion.FullScanBatchSize)
	projectIngestionService.SetFullScanWorkerLimits(cfg.Ingestion.GlobalWorkerCount, cfg.Ingestion.PerProjectWorkerLimit)
	projectIngestionService.SetExtractorCacheEnabled(cfg.Ingestion.ExtractorCacheEnabled)
	if cfg.SQLite.CheckpointAfterIngestion {
		projectIngestionService.SetCheckpointFunc(sqliteDB.Checkpoint)
	}
	projectIntegrationStore := projectintegrations.NewSQLiteStore(sqliteDB.SQLDB())
	interruptedIntegrationRuns, err := projectIntegrationStore.FailActiveSyncRuns(ctx, time.Now().UTC(), string(projectintegrations.ErrorCategoryInterrupted))
	if err != nil {
		return err
	} else if interruptedIntegrationRuns > 0 {
		logger.Warn("failed interrupted integration runs after server restart", slog.Int("run_count", interruptedIntegrationRuns))
	}
	projectIntegrationRunner, err := projectintegrations.NewRunner(projectintegrations.RunnerOptions{
		Projects:           cfg.Projects,
		Store:              projectIntegrationStore,
		RichContentStore:   projectintegrations.NewRichContentGraphStore(projectGraph),
		CredentialResolver: projectintegrations.NewCredentialResolver(),
		JiraClient:         newJiraPollerByProject(cfg.Projects),
		ConfluenceClient:   newConfluencePollerByProject(cfg.Projects),
	})
	if err != nil {
		return err
	}
	projectIntegrationScheduler, err := projectintegrations.NewScheduler(cfg.Projects, projectIntegrationRunner, projectintegrations.SchedulerOptions{Logger: logger})
	if err != nil {
		return err
	}
	if err := projectIntegrationScheduler.Start(ctx); err != nil {
		return err
	}
	defer func() { _ = projectIntegrationScheduler.Stop(context.Background()) }()
	projectIntegrationService, err := projectintegrations.NewServiceWithOptions(cfg.Projects, projectIntegrationStore, projectintegrations.ServiceOptions{
		Runner:      projectIntegrationScheduler,
		RichContent: projectintegrations.NewRichContentGraphStore(projectGraph),
	})
	if err != nil {
		return err
	}
	interruptedIngestionRuns := 0
	if failed, err := projectIngestionService.FailInterruptedRuns(ctx, "server_restarted"); err != nil {
		return err
	} else if failed > 0 {
		interruptedIngestionRuns = failed
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
	if interruptedIngestionRuns > 0 {
		for _, project := range projectRegistry.List() {
			if !project.Enabled || project.DigestMode != projectregistry.DigestModeContentGraph {
				continue
			}
			if _, err := projectIngestionScheduler.SubmitFullScanAsync(ctx, project.ID, projectingestion.TriggerManual); err != nil {
				logger.Warn("failed to enqueue restart recovery ingestion scan", slog.String("project_id", project.ID), slog.String("error_category", "restart_recovery_enqueue_failed"))
				continue
			}
			logger.Info("enqueued restart recovery ingestion scan", slog.String("project_id", project.ID))
		}
	}
	initialScanOnStart := effectiveInitialScanOnStart(cfg.Ingestion.InitialScanOnStart, interruptedIngestionRuns)
	if cfg.Ingestion.InitialScanOnStart && !initialScanOnStart {
		logger.Info("skipping live initial ingestion scans because restart recovery scans were queued")
	}
	configStore := store.NewSQLiteConfigStore(sqliteDB.SQLDB())
	if err := configStore.SetRuntimeFlag(ctx, "research.live_providers_enabled", false, "disabled until provider ADR approval"); err != nil {
		return err
	}
	projectEvidenceService := projectevidence.New(evidencestore.NewLadybugStore(projectGraph))
	projectConfidenceService := projectconfidence.New(confidencestore.NewLadybugStore(projectGraph))
	projectKnowledgeService := projectknowledge.New(knowledgestore.NewLadybugStore(projectGraph))
	projectWorkPlanService := projectworkplan.New(workplanstore.NewLadybugStore(metadataPersistentGraph))
	projectWorkflowService := projectworkflow.New(workflowstore.NewMemoryStore())
	projectAutomationService := projectautomation.New(automationstore.NewLadybugStore(metadataPersistentGraph), projectWorkPlanService, projectautomation.Options{
		Enabled:                   cfg.Automation.Enabled,
		RunnerEnabled:             cfg.Automation.RunnerEnabled,
		RequireCodexWhenAvailable: cfg.Automation.RequireCodexWhenAvailable,
		AllowManualRunner:         cfg.Automation.AllowManualRunner,
		RunnerExecution:           cfg.Automation.RunnerExecution,
		MaxParallelTasks:          cfg.Automation.MaxParallelTasks,
		DefaultMaxRuntime:         cfg.Automation.DefaultMaxRuntime,
		CodexBinaryPath:           cfg.Automation.CodexBinaryPath,
		Agents:                    automationAgents(cfg.Automation.Agents),
		PermissionResolver:        projectWorkflowService,
		DirtyScopeRecovery: projectautomation.DirtyScopeRecoveryOptions{
			AllowedSupportPathspecs: cfg.GitOperations.DirtyScopeRecovery.AllowedSupportPathspecs,
			PathspecResolver:        dirtyScopeRecoveryResolver(cfg),
		},
		WorkPlanStatusTrigger: projectautomation.WorkPlanStatusTriggerOptions{
			Enabled:  cfg.Automation.WorkPlanStatusTrigger.Enabled,
			Statuses: append([]string(nil), cfg.Automation.WorkPlanStatusTrigger.Statuses...),
		},
	})
	projectWorkPlanService.SetStatusChangeHandler(projectAutomationService)
	projectAutomationExecutor := projectautomation.NewExecutor(projectAutomationService, projectautomation.ExecutorOptions{
		Enabled:               cfg.Automation.Enabled,
		RunnerEnabled:         cfg.Automation.RunnerEnabled,
		RunnerExecution:       cfg.Automation.RunnerExecution,
		PollInterval:          cfg.Automation.PollInterval,
		GlobalWorkerCount:     cfg.Automation.GlobalWorkerCount,
		PerProjectWorkerLimit: cfg.Automation.PerProjectWorkerLimit,
		PerAgentWorkerLimit:   cfg.Automation.PerAgentWorkerLimit,
		ProjectIDs:            automationProjectIDs(projectRegistry.List()),
	})
	projectWorkflowService.SetCompilerDependencies(projectWorkPlanService, projectAutomationService)
	if err := loadConfiguredWorkflows(ctx, cfg, projectWorkflowService, logger); err != nil {
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
		InitialScanOnStart:       initialScanOnStart,
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
	projectReliabilityService := projectreliability.NewServiceFromAPIs(projectRegistry, projectIngestionScheduler, projectWorkspaceService, projectreliability.Options{})
	projectConfidenceInputs := projectconfidence.NewReliabilityInputAdapter(
		projectEvidenceService,
		projectReliabilityService,
		projectreliability.NewClaimChecker(projectWorkspaceService),
		projectreliability.NewImpactAnalyzerWithGraph(projectWorkspaceService, projectIngestionScheduler),
	)
	projectKnowledgeInputs := projectknowledge.NewPromotionInputAdapter(projectEvidenceService, projectConfidenceService)
	agentService := service.New(agentStore, agentStore)
	activityStore := agentactivity.NewSQLiteStore(sqliteDB.SQLDB(), agentactivity.SQLiteStoreOptions{
		RetainRawPayloads: cfg.AgentActivity.RetainRawPayloads,
	})
	activityRecorder := agentactivity.NewRecorderWithStore(500, activityStore)
	agentService.SetPolicyRecorder(activityRecorder)
	projectIngestionService.SetPolicyRecorder(activityRecorder)
	if workPlanRecorder, ok := any(projectWorkPlanService).(interface{ SetActivityRecorder(*agentactivity.Recorder) }); ok {
		workPlanRecorder.SetActivityRecorder(activityRecorder)
	}
	if workspaceRecorder, ok := projectWorkspaceService.(interface{ SetPolicyRecorder(*agentactivity.Recorder) }); ok {
		workspaceRecorder.SetPolicyRecorder(activityRecorder)
	}

	checker := health.NewChecker(
		health.Check{
			Name:    "sqlite",
			Fn:      sqliteDB.Ready,
			Timeout: 50 * time.Millisecond,
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
	dashboardhttpapi.RegisterRoutes(mux)
	mux.HandleFunc("GET /healthz", health.LivenessHandler)
	mux.Handle("GET /readyz", health.ReadinessHandler(checker, logger))
	httpapi.RegisterRoutes(mux, agentService)
	researchhttpapi.RegisterRoutes(mux, researchService)
	projecthttpapi.RegisterRoutesWithWorkspaceIntegrationsAndActivity(mux, projectRegistry, projectDigestService, projectIngestionScheduler, projectWorkspaceService, projectIntegrationService, activityRecorder)
	evidencehttpapi.RegisterRoutes(mux, projectEvidenceService)
	confidencehttpapi.RegisterRoutes(mux, projectConfidenceService, projectConfidenceInputs)
	knowledgehttpapi.RegisterRoutes(mux, projectKnowledgeService, projectKnowledgeInputs)
	workplanhttpapi.RegisterRoutes(mux, projectWorkPlanService)
	automationhttpapi.RegisterRoutes(mux, projectAutomationService)
	workflowhttpapi.RegisterRoutes(mux, projectWorkflowService)
	var diagnosticsService *diagnostics.Service
	if diagnostics.Enabled(cfg.Debug.Enabled, cfg.HTTPAddr) {
		diagnosticsService = diagnostics.NewService(projectingestion.DiagnosticsSource{
			Scheduler:     projectIngestionScheduler,
			Orchestrator:  projectIngestionOrchestrator,
			Service:       projectIngestionService,
			GraphStorage:  projectGraph,
			SearchStorage: projectSearchStore,
		}, diagnostics.RuntimeOptions{Enabled: cfg.Debug.RuntimeMetricsEnabled})
		diagnostics.RegisterRoutes(mux, diagnosticsService)
	}
	mux.Handle("/mcp", mcpapi.NewHandlerWithActivityEvidenceGraphConfidenceKnowledgeWorkPlansAutomationAndWorkflow(agentService, researchService, projectRegistry, projectDigestService, projectIngestionScheduler, projectWorkspaceService, projectEvidenceService, projectConfidenceService, projectConfidenceInputs, projectKnowledgeService, projectKnowledgeInputs, projectWorkPlanService, projectAutomationService, projectWorkflowService, projectIntegrationService, diagnosticsService, activityRecorder, logger))

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

	if err := projectAutomationExecutor.Start(ctx); err != nil {
		return err
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
		if err := projectIntegrationScheduler.Stop(shutdownCtx); err != nil {
			return err
		}
		if err := projectAutomationExecutor.Stop(shutdownCtx); err != nil {
			return err
		}
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		_ = projectIngestionOrchestrator.Stop(shutdownCtx)
		_ = projectIngestionScheduler.Stop(shutdownCtx)
		_ = projectIntegrationScheduler.Stop(shutdownCtx)
		_ = projectAutomationExecutor.Stop(shutdownCtx)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func loadConfiguredWorkflows(ctx context.Context, cfg config.Config, svc *projectworkflow.Service, logger *slog.Logger) error {
	if !cfg.Workflows.Enabled || svc == nil {
		return nil
	}
	for _, configuredPath := range cfg.Workflows.DefinitionPaths {
		cleanPath, err := resolveWorkflowDefinitionPath(cfg.ConfigPath, configuredPath)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(cleanPath)
		if err != nil {
			return errors.New("load workflow definition failed")
		}
		defs, issues, err := projectworkflow.ParseWorkflowTOML(data)
		if err != nil {
			return err
		}
		if len(defs) == 0 {
			return errors.New("workflow definition file contained no workflows")
		}
		for _, issue := range issues {
			if issue.Severity == "error" {
				return errors.New("workflow definition validation failed")
			}
		}
		result, err := svc.ImportWorkflowTOML(ctx, projectworkflow.ImportWorkflowTOMLInput{ProjectID: defs[0].ProjectID, Data: data, CreatedByRunID: "server-startup"})
		if err != nil {
			return err
		}
		logger.Info("workflow definitions loaded", slog.Int("workflow_count", len(result.Workflows)), slog.Int("permission_snapshot_count", len(result.PermissionSnapshotIDs)))
	}
	return nil
}

func resolveWorkflowDefinitionPath(configPath string, path string) (string, error) {
	trimmedPath := strings.TrimSpace(path)
	if strings.Contains(trimmedPath, "..") {
		return "", errors.New("workflow definition path cannot contain traversal")
	}
	cleanPath := filepath.Clean(trimmedPath)
	if cleanPath == "." || cleanPath == "" || filepath.IsAbs(cleanPath) {
		return "", errors.New("workflow definition path must be relative")
	}
	for _, part := range strings.Split(cleanPath, string(filepath.Separator)) {
		if part == ".." {
			return "", errors.New("workflow definition path cannot contain traversal")
		}
	}
	if configPath != "" {
		configRelative := filepath.Join(filepath.Dir(configPath), cleanPath)
		if _, err := os.Stat(configRelative); err == nil {
			return configRelative, nil
		}
	}
	return cleanPath, nil
}

func openProjectPersistentGraphs(registry *projectregistry.Registry, baseLadybugPath string) ([]projectregistry.ProjectGraphBackend, func(), error) {
	backends := make([]projectregistry.ProjectGraphBackend, 0)
	graphLRU := ladybug.NewPebbleGraphLRU(projectPersistentGraphMaxOpen(registry))
	closeGraphs := func() { _ = graphLRU.CloseAll() }
	for _, project := range registry.List() {
		if !project.Enabled || project.DigestMode != projectregistry.DigestModeContentGraph || project.GraphStorage != projectregistry.GraphStoragePersistent {
			continue
		}
		path, err := projectregistry.ProjectGraphPath(baseLadybugPath, project.ID)
		if err != nil {
			closeGraphs()
			return nil, nil, err
		}
		storageKey, err := projectregistry.ProjectGraphStorageKey(project.ID)
		if err != nil {
			closeGraphs()
			return nil, nil, err
		}
		backends = append(backends, projectregistry.ProjectGraphBackend{
			ProjectID:  project.ID,
			Graph:      ladybug.NewLazyPebbleGraph(ladybug.PebbleGraphPath(path), graphLRU),
			StorageKey: storageKey,
		})
	}
	return backends, closeGraphs, nil
}

func projectPersistentGraphMaxOpen(registry *projectregistry.Registry) int {
	if registry == nil {
		return 1
	}
	count := 0
	for _, project := range registry.List() {
		if project.Enabled && project.DigestMode == projectregistry.DigestModeContentGraph && project.GraphStorage == projectregistry.GraphStoragePersistent {
			count++
		}
	}
	if count <= 0 {
		return 1
	}
	if count <= ladybug.DefaultPebbleGraphMaxOpen {
		return count
	}
	return ladybug.DefaultPebbleGraphMaxOpen
}

func openProjectSearchStores(registry *projectregistry.Registry, baseLadybugPath string, sqliteCfg config.SQLite, stateStore *projectingestion.SQLiteStore) ([]projectingestion.SearchStoreBackend, func(), error) {
	backends := make([]projectingestion.SearchStoreBackend, 0)
	closers := make([]func() error, 0)
	closeStores := func() {
		for index := len(closers) - 1; index >= 0; index-- {
			_ = closers[index]()
		}
	}
	options := sqliteplatform.Options{
		WALEnabled:               sqliteCfg.WALEnabled,
		BusyTimeout:              sqliteCfg.BusyTimeout,
		Synchronous:              sqliteCfg.Synchronous,
		CheckpointAfterIngestion: sqliteCfg.CheckpointAfterIngestion,
	}
	for _, project := range registry.List() {
		if !project.Enabled || project.DigestMode != projectregistry.DigestModeContentGraph || project.GraphStorage != projectregistry.GraphStoragePersistent {
			continue
		}
		path, err := projectregistry.ProjectSearchPath(baseLadybugPath, project.ID)
		if err != nil {
			closeStores()
			return nil, nil, err
		}
		storageKey, err := projectregistry.ProjectGraphStorageKey(project.ID)
		if err != nil {
			closeStores()
			return nil, nil, err
		}
		db, err := sqliteplatform.OpenWithOptions(path, options)
		if err != nil {
			closeStores()
			return nil, nil, err
		}
		closers = append(closers, db.Close)
		if err := sqliteschema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
			closeStores()
			return nil, nil, err
		}
		searchStore := projectingestion.NewSQLiteStore(db.SQLDB())
		searchStore.SetSearchStateStore(stateStore)
		backends = append(backends, projectingestion.SearchStoreBackend{
			ProjectID:  project.ID,
			Store:      searchStore,
			StorageKey: storageKey,
		})
	}
	return backends, closeStores, nil
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

func effectiveInitialScanOnStart(configured bool, interruptedIngestionRuns int) bool {
	if interruptedIngestionRuns > 0 {
		return false
	}
	return configured
}

func automationAgents(configAgents []config.AutomationAgent) []projectautomation.AutomationAgent {
	if len(configAgents) == 0 {
		return nil
	}
	agents := make([]projectautomation.AutomationAgent, 0, len(configAgents))
	for _, agent := range configAgents {
		commands := make([]projectautomation.CommandSpec, 0, len(agent.AllowedCommands))
		for _, command := range agent.AllowedCommands {
			commands = append(commands, projectautomation.CommandSpec{
				Command: command.Command,
				Args:    append([]string(nil), command.Args...),
			})
		}
		agents = append(agents, projectautomation.AutomationAgent{
			ID:              agent.ID,
			DisplayName:     agent.DisplayName,
			Purpose:         agent.Purpose,
			Enabled:         agent.Enabled,
			AllowedSkills:   append([]string(nil), agent.AllowedSkills...),
			AllowedTools:    append([]string(nil), agent.AllowedTools...),
			AllowedCommands: commands,
			DeniedCommands:  append([]string(nil), agent.DeniedCommands...),
			WorkspaceMode:   agent.WorkspaceMode,
			NetworkPolicy:   agent.NetworkPolicy,
			SecretPolicy:    agent.SecretPolicy,
			LogPolicy:       agent.LogPolicy,
			MaxRuntime:      agent.MaxRuntime,
			MaxRetries:      agent.MaxRetries,
		})
	}
	return agents
}

func dirtyScopeRecoveryResolver(cfg config.Config) func(projectID string) []string {
	byProject := make(map[string][]string, len(cfg.Projects))
	for _, project := range cfg.Projects {
		if project.GitOperations == nil {
			continue
		}
		byProject[project.ID] = append([]string(nil), project.GitOperations.DirtyScopeRecovery.AllowedSupportPathspecs...)
		for _, alias := range project.Aliases {
			byProject[alias] = append([]string(nil), project.GitOperations.DirtyScopeRecovery.AllowedSupportPathspecs...)
		}
	}
	return func(projectID string) []string {
		return append([]string(nil), byProject[projectID]...)
	}
}

func automationProjectIDs(projects []projectregistry.Project) []string {
	ids := make([]string, 0, len(projects))
	for _, project := range projects {
		if project.Enabled {
			ids = append(ids, project.ID)
		}
	}
	return ids
}
