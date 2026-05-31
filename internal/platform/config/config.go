package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultConfigPath                 = "configs/mivia-server.local.toml"
	defaultHTTPAddr                   = "127.0.0.1:8080"
	defaultLadybugPath                = "data/mivialabs.lbug"
	defaultSQLitePath                 = "data/mivialabs-config.sqlite"
	defaultMaxRequestBytes            = int64(1 << 20)
	defaultRequestTimeout             = 10 * time.Second
	defaultReadHeaderTimeout          = 5 * time.Second
	defaultShutdownTimeout            = 10 * time.Second
	defaultIngestionDebounceInterval  = 2 * time.Second
	defaultIngestionMaxChunkBytes     = 16 * 1024
	defaultIngestionQueueDepth        = 10000
	defaultIngestionFullScanBatchSize = 500
	defaultIngestionTaskWarnAfter     = 30 * time.Second
	defaultIntegrationIncremental     = time.Minute
	defaultIntegrationEmptyPollSleep  = 10 * time.Minute
	defaultIntegrationMaxIdleSleep    = 30 * time.Minute
	defaultIntegrationOverlapWindow   = 2 * time.Minute
	defaultIntegrationReadTimeout     = 30 * time.Second
	defaultIntegrationPageSize        = 100
	defaultConfluencePageSize         = 50
	defaultIntegrationMaxResults      = 0
	defaultSQLiteBusyTimeout          = 5 * time.Second
	defaultSQLiteSynchronous          = "NORMAL"
	defaultSensitiveMarkerPolicy      = "skip_file"
	sensitiveMarkerPolicySkipFile     = "skip_file"
)

type Config struct {
	ConfigPath        string
	CPUCount          int
	HTTPAddr          string
	LadybugPath       string
	SQLitePath        string
	SQLite            SQLite
	Debug             Debug
	Logging           Logging
	MaxRequestBytes   int64
	RequestTimeout    time.Duration
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration
	Ingestion         Ingestion
	Workspace         Workspace
	Projects          []Project
}

type Logging struct {
	FileEnabled bool
	FilePath    string
}

type Debug struct {
	Enabled               bool
	PprofEnabled          bool
	ExpvarEnabled         bool
	RuntimeMetricsEnabled bool
}

type SQLite struct {
	WALEnabled               bool
	BusyTimeout              time.Duration
	Synchronous              string
	CheckpointAfterIngestion bool
}

type Workspace struct {
	Enabled bool
}

type Ingestion struct {
	ContentGraphEnabled      bool
	LiveUpdatesEnabled       bool
	ASTExtractionEnabled     bool
	ExtractorCacheEnabled    bool
	DebounceInterval         time.Duration
	MaxFileBytes             int64
	MaxChunkBytes            int
	QueueDepth               int
	WorkerCount              int
	GlobalWorkerCount        int
	PerProjectWorkerLimit    int
	LivePathPriority         bool
	MaxWatchedDirectoryCount int
	TaskWarnAfter            time.Duration
	FullScanBatchSize        int
	InitialScanOnStart       bool
	SensitiveMarkerPolicy    string
}

type Project struct {
	ID                    string
	Aliases               []string
	DisplayName           string
	Description           string
	RootPath              string
	Enabled               bool
	Classification        string
	GraphNamespace        string
	GraphStorage          string
	DigestMode            string
	UpdatePolicy          string
	WorkspaceMode         string
	Include               []string
	Exclude               []string
	FollowSymlinks        bool
	MaxFileBytes          int64
	MaxChunkBytes         int
	SensitiveMarkerPolicy string
	Integrations          IntegrationConfig
}

type IntegrationConfig struct {
	Jira       *JiraIntegration
	Confluence *ConfluenceIntegration
}

type AtlassianCredentialRefs struct {
	EmailEnv        string
	EmailFile       string
	APITokenEnv     string
	APITokenFile    string
	CredentialsFile string
}

type IntegrationPolling struct {
	IngestionEnabled    bool
	InitialFullSync     string
	IncrementalInterval time.Duration
	EmptyPollSleep      time.Duration
	MaxIdleSleep        time.Duration
	OverlapWindow       time.Duration
	InitialPageSize     int
	IncrementalPageSize int
}

type JiraIntegration struct {
	Enabled           bool
	SiteURL           string
	CloudID           string
	AuthMode          string
	CredentialRefs    AtlassianCredentialRefs
	ReadTimeout       time.Duration
	MaxResults        int
	Polling           IntegrationPolling
	ProjectKeys       []string
	DefaultFields     []string
	AllowedFields     []string
	IncludeRichFields bool
	IncludeComments   bool
	JQLExtraFilter    string
}

type ConfluenceIntegration struct {
	Enabled            bool
	SiteURL            string
	CloudID            string
	AuthMode           string
	CredentialRefs     AtlassianCredentialRefs
	ReadTimeout        time.Duration
	MaxResults         int
	Polling            IntegrationPolling
	SpaceKeys          []string
	BodyRepresentation string
	IncludeBody        bool
	IncludeComments    bool
	IncludeLabels      bool
	IncludeProperties  bool
	RootPageIDs        []string
	CQLExtraFilter     string
}

func Load() (Config, error) {
	configPath, explicitConfigPath := configPathFromEnv()
	cfg := defaultConfig(configPath)

	fileCfg, err := loadFileConfig(configPath)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist) && !explicitConfigPath:
		case errors.Is(err, fs.ErrNotExist):
			return Config{}, errors.New("MIVIA_CONFIG_PATH points to a missing config file")
		case explicitConfigPath:
			return Config{}, fmt.Errorf("MIVIA_CONFIG_PATH config is invalid: %w", err)
		default:
			return Config{}, fmt.Errorf("default local config is invalid: %w", err)
		}
	} else if cfg, err = fileCfg.applyTo(cfg); err != nil {
		if explicitConfigPath {
			return Config{}, fmt.Errorf("MIVIA_CONFIG_PATH config is invalid: %w", err)
		}
		return Config{}, fmt.Errorf("default local config is invalid: %w", err)
	}

	if err := applyEnvOverrides(&cfg); err != nil {
		return Config{}, err
	}
	cfg.resolveAutoSettings(runtime.NumCPU())

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func defaultConfig(configPath string) Config {
	return Config{
		ConfigPath:  configPath,
		CPUCount:    0,
		HTTPAddr:    defaultHTTPAddr,
		LadybugPath: defaultLadybugPath,
		SQLitePath:  defaultSQLitePath,
		SQLite: SQLite{
			WALEnabled:               true,
			BusyTimeout:              defaultSQLiteBusyTimeout,
			Synchronous:              defaultSQLiteSynchronous,
			CheckpointAfterIngestion: true,
		},
		Debug:             Debug{},
		Logging:           Logging{},
		MaxRequestBytes:   defaultMaxRequestBytes,
		RequestTimeout:    defaultRequestTimeout,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
		Ingestion:         defaultIngestion(),
		Workspace:         Workspace{Enabled: false},
		Projects:          nil,
	}
}

func defaultIngestion() Ingestion {
	return Ingestion{
		ContentGraphEnabled:      false,
		LiveUpdatesEnabled:       false,
		ASTExtractionEnabled:     true,
		ExtractorCacheEnabled:    true,
		DebounceInterval:         defaultIngestionDebounceInterval,
		MaxFileBytes:             0,
		MaxChunkBytes:            defaultIngestionMaxChunkBytes,
		QueueDepth:               defaultIngestionQueueDepth,
		WorkerCount:              0,
		GlobalWorkerCount:        0,
		PerProjectWorkerLimit:    0,
		LivePathPriority:         true,
		MaxWatchedDirectoryCount: 0,
		TaskWarnAfter:            defaultIngestionTaskWarnAfter,
		FullScanBatchSize:        defaultIngestionFullScanBatchSize,
		InitialScanOnStart:       false,
		SensitiveMarkerPolicy:    defaultSensitiveMarkerPolicy,
	}
}

func applyEnvOverrides(cfg *Config) error {
	var err error
	cfg.HTTPAddr = getenv("MIVIA_HTTP_ADDR", cfg.HTTPAddr)
	if cfg.CPUCount, err = getenvAutoInt("MIVIA_CPU_COUNT", cfg.CPUCount); err != nil {
		return err
	}
	cfg.LadybugPath = getenv("MIVIA_LADYBUG_PATH", cfg.LadybugPath)
	cfg.SQLitePath = getenv("MIVIA_SQLITE_PATH", cfg.SQLitePath)
	if cfg.Debug.Enabled, err = getenvBool("MIVIA_DEBUG_ENABLED", cfg.Debug.Enabled); err != nil {
		return err
	}
	if cfg.Debug.PprofEnabled, err = getenvBool("MIVIA_DEBUG_PPROF_ENABLED", cfg.Debug.PprofEnabled); err != nil {
		return err
	}
	if cfg.Debug.ExpvarEnabled, err = getenvBool("MIVIA_DEBUG_EXPVAR_ENABLED", cfg.Debug.ExpvarEnabled); err != nil {
		return err
	}
	if cfg.Debug.RuntimeMetricsEnabled, err = getenvBool("MIVIA_DEBUG_RUNTIME_METRICS_ENABLED", cfg.Debug.RuntimeMetricsEnabled); err != nil {
		return err
	}
	if cfg.SQLite.WALEnabled, err = getenvBool("MIVIA_SQLITE_WAL_ENABLED", cfg.SQLite.WALEnabled); err != nil {
		return err
	}
	if cfg.SQLite.BusyTimeout, err = getenvDuration("MIVIA_SQLITE_BUSY_TIMEOUT", cfg.SQLite.BusyTimeout); err != nil {
		return err
	}
	cfg.SQLite.Synchronous = getenv("MIVIA_SQLITE_SYNCHRONOUS", cfg.SQLite.Synchronous)
	if cfg.SQLite.CheckpointAfterIngestion, err = getenvBool("MIVIA_SQLITE_CHECKPOINT_AFTER_INGESTION", cfg.SQLite.CheckpointAfterIngestion); err != nil {
		return err
	}
	if cfg.Logging.FileEnabled, err = getenvBool("MIVIA_LOG_FILE_ENABLED", cfg.Logging.FileEnabled); err != nil {
		return err
	}
	cfg.Logging.FilePath = getenv("MIVIA_LOG_FILE_PATH", cfg.Logging.FilePath)
	if cfg.MaxRequestBytes, err = getenvInt64("MIVIA_MAX_REQUEST_BYTES", cfg.MaxRequestBytes); err != nil {
		return err
	}
	if cfg.RequestTimeout, err = getenvDuration("MIVIA_REQUEST_TIMEOUT", cfg.RequestTimeout); err != nil {
		return err
	}
	if cfg.ReadHeaderTimeout, err = getenvDuration("MIVIA_READ_HEADER_TIMEOUT", cfg.ReadHeaderTimeout); err != nil {
		return err
	}
	if cfg.ShutdownTimeout, err = getenvDuration("MIVIA_SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return err
	}
	if cfg.Ingestion.ContentGraphEnabled, err = getenvBool("MIVIA_INGESTION_CONTENT_GRAPH_ENABLED", cfg.Ingestion.ContentGraphEnabled); err != nil {
		return err
	}
	if cfg.Ingestion.LiveUpdatesEnabled, err = getenvBool("MIVIA_INGESTION_LIVE_UPDATES_ENABLED", cfg.Ingestion.LiveUpdatesEnabled); err != nil {
		return err
	}
	if cfg.Ingestion.ASTExtractionEnabled, err = getenvBool("MIVIA_INGESTION_AST_EXTRACTION_ENABLED", cfg.Ingestion.ASTExtractionEnabled); err != nil {
		return err
	}
	if cfg.Ingestion.ExtractorCacheEnabled, err = getenvBool("MIVIA_INGESTION_EXTRACTOR_CACHE_ENABLED", cfg.Ingestion.ExtractorCacheEnabled); err != nil {
		return err
	}
	if cfg.Ingestion.DebounceInterval, err = getenvDuration("MIVIA_INGESTION_DEBOUNCE_INTERVAL", cfg.Ingestion.DebounceInterval); err != nil {
		return err
	}
	if cfg.Ingestion.MaxFileBytes, err = getenvInt64("MIVIA_INGESTION_MAX_FILE_BYTES", cfg.Ingestion.MaxFileBytes); err != nil {
		return err
	}
	if cfg.Ingestion.MaxChunkBytes, err = getenvInt("MIVIA_INGESTION_MAX_CHUNK_BYTES", cfg.Ingestion.MaxChunkBytes); err != nil {
		return err
	}
	if cfg.Ingestion.QueueDepth, err = getenvInt("MIVIA_INGESTION_QUEUE_DEPTH", cfg.Ingestion.QueueDepth); err != nil {
		return err
	}
	workerCountOverridden := os.Getenv("MIVIA_INGESTION_WORKER_COUNT") != ""
	if cfg.Ingestion.WorkerCount, err = getenvAutoInt("MIVIA_INGESTION_WORKER_COUNT", cfg.Ingestion.WorkerCount); err != nil {
		return err
	}
	globalWorkerCountOverridden := os.Getenv("MIVIA_INGESTION_GLOBAL_WORKER_COUNT") != ""
	if cfg.Ingestion.GlobalWorkerCount, err = getenvAutoInt("MIVIA_INGESTION_GLOBAL_WORKER_COUNT", cfg.Ingestion.GlobalWorkerCount); err != nil {
		return err
	}
	if workerCountOverridden && !globalWorkerCountOverridden {
		cfg.Ingestion.GlobalWorkerCount = cfg.Ingestion.WorkerCount
	}
	if cfg.Ingestion.PerProjectWorkerLimit, err = getenvAutoInt("MIVIA_INGESTION_PER_PROJECT_WORKER_LIMIT", cfg.Ingestion.PerProjectWorkerLimit); err != nil {
		return err
	}
	if cfg.Ingestion.LivePathPriority, err = getenvBool("MIVIA_INGESTION_LIVE_PATH_PRIORITY", cfg.Ingestion.LivePathPriority); err != nil {
		return err
	}
	if cfg.Ingestion.MaxWatchedDirectoryCount, err = getenvInt("MIVIA_INGESTION_MAX_WATCHED_DIRECTORY_COUNT", cfg.Ingestion.MaxWatchedDirectoryCount); err != nil {
		return err
	}
	if cfg.Ingestion.TaskWarnAfter, err = getenvDuration("MIVIA_INGESTION_TASK_WARN_AFTER", cfg.Ingestion.TaskWarnAfter); err != nil {
		return err
	}
	if cfg.Ingestion.FullScanBatchSize, err = getenvInt("MIVIA_INGESTION_FULL_SCAN_BATCH_SIZE", cfg.Ingestion.FullScanBatchSize); err != nil {
		return err
	}
	if cfg.Ingestion.InitialScanOnStart, err = getenvBool("MIVIA_INGESTION_INITIAL_SCAN_ON_START", cfg.Ingestion.InitialScanOnStart); err != nil {
		return err
	}
	cfg.Ingestion.SensitiveMarkerPolicy = getenv("MIVIA_INGESTION_SENSITIVE_MARKER_POLICY", cfg.Ingestion.SensitiveMarkerPolicy)
	if cfg.Workspace.Enabled, err = getenvBool("MIVIA_WORKSPACE_ENABLED", cfg.Workspace.Enabled); err != nil {
		return err
	}
	return nil
}

func (cfg *Config) resolveAutoSettings(maxCPU int) {
	if maxCPU <= 0 {
		maxCPU = 1
	}
	if cfg.CPUCount <= 0 {
		cfg.CPUCount = maxCPU
	}
	if cfg.Ingestion.WorkerCount <= 0 {
		cfg.Ingestion.WorkerCount = cfg.CPUCount
	}
	if cfg.Ingestion.GlobalWorkerCount <= 0 {
		cfg.Ingestion.GlobalWorkerCount = cfg.CPUCount
	}
	if cfg.Ingestion.PerProjectWorkerLimit <= 0 {
		cfg.Ingestion.PerProjectWorkerLimit = cfg.Ingestion.GlobalWorkerCount
	}
	if cfg.SQLitePath == ":memory:" {
		cfg.SQLite.WALEnabled = false
	}
}

func (cfg Config) Validate() error {
	if cfg.CPUCount <= 0 {
		return errors.New("MIVIA_CPU_COUNT must be \"auto\" or a positive integer")
	}
	if cfg.HTTPAddr == "" {
		return errors.New("MIVIA_HTTP_ADDR must not be empty")
	}
	if !isLocalAddress(cfg.HTTPAddr) {
		return fmt.Errorf("MIVIA_HTTP_ADDR must bind to localhost until authn/authz is approved: %s", cfg.HTTPAddr)
	}
	if cfg.LadybugPath == "" {
		return errors.New("MIVIA_LADYBUG_PATH must not be empty")
	}
	if cfg.SQLitePath == "" {
		return errors.New("MIVIA_SQLITE_PATH must not be empty")
	}
	if err := cfg.Debug.Validate(); err != nil {
		return err
	}
	if err := cfg.SQLite.Validate(); err != nil {
		return err
	}
	if err := cfg.Logging.Validate(); err != nil {
		return err
	}
	if cfg.MaxRequestBytes <= 0 {
		return errors.New("MIVIA_MAX_REQUEST_BYTES must be positive")
	}
	if cfg.RequestTimeout <= 0 {
		return errors.New("MIVIA_REQUEST_TIMEOUT must be positive")
	}
	if cfg.ReadHeaderTimeout <= 0 {
		return errors.New("MIVIA_READ_HEADER_TIMEOUT must be positive")
	}
	if cfg.ShutdownTimeout <= 0 {
		return errors.New("MIVIA_SHUTDOWN_TIMEOUT must be positive")
	}
	if err := cfg.Ingestion.Validate(); err != nil {
		return err
	}
	return nil
}

func (logging Logging) Validate() error {
	if logging.FileEnabled && strings.TrimSpace(logging.FilePath) == "" {
		return errors.New("MIVIA_LOG_FILE_PATH must not be empty when MIVIA_LOG_FILE_ENABLED is true")
	}
	return nil
}

func (debug Debug) Validate() error {
	if !debug.Enabled && (debug.PprofEnabled || debug.ExpvarEnabled || debug.RuntimeMetricsEnabled) {
		return errors.New("MIVIA_DEBUG_ENABLED must be true before enabling debug diagnostics")
	}
	return nil
}

func (sqlite SQLite) Validate() error {
	if sqlite.BusyTimeout <= 0 {
		return errors.New("MIVIA_SQLITE_BUSY_TIMEOUT must be positive")
	}
	switch strings.ToUpper(strings.TrimSpace(sqlite.Synchronous)) {
	case "OFF", "NORMAL", "FULL", "EXTRA":
	default:
		return errors.New("MIVIA_SQLITE_SYNCHRONOUS must be OFF, NORMAL, FULL, or EXTRA")
	}
	return nil
}

func (ingestion Ingestion) Validate() error {
	if ingestion.LiveUpdatesEnabled && !ingestion.ContentGraphEnabled {
		return errors.New("MIVIA_INGESTION_LIVE_UPDATES_ENABLED requires MIVIA_INGESTION_CONTENT_GRAPH_ENABLED")
	}
	if ingestion.ContentGraphEnabled && !ingestion.ASTExtractionEnabled {
		return errors.New("MIVIA_INGESTION_AST_EXTRACTION_ENABLED must be true when content graph is enabled")
	}
	if ingestion.ASTExtractionEnabled && !ingestion.ExtractorCacheEnabled {
		return errors.New("MIVIA_INGESTION_EXTRACTOR_CACHE_ENABLED must be true when AST extraction is enabled")
	}
	if ingestion.DebounceInterval <= 0 {
		return errors.New("MIVIA_INGESTION_DEBOUNCE_INTERVAL must be positive")
	}
	if ingestion.MaxFileBytes < 0 {
		return errors.New("MIVIA_INGESTION_MAX_FILE_BYTES must be non-negative")
	}
	if ingestion.MaxChunkBytes <= 0 {
		return errors.New("MIVIA_INGESTION_MAX_CHUNK_BYTES must be positive")
	}
	if ingestion.QueueDepth <= 0 {
		return errors.New("MIVIA_INGESTION_QUEUE_DEPTH must be positive")
	}
	if ingestion.WorkerCount <= 0 {
		return errors.New("MIVIA_INGESTION_WORKER_COUNT must be \"auto\" or a positive integer")
	}
	if ingestion.GlobalWorkerCount <= 0 {
		return errors.New("MIVIA_INGESTION_GLOBAL_WORKER_COUNT must be \"auto\" or a positive integer")
	}
	if ingestion.PerProjectWorkerLimit <= 0 || ingestion.PerProjectWorkerLimit > ingestion.GlobalWorkerCount {
		return errors.New("MIVIA_INGESTION_PER_PROJECT_WORKER_LIMIT must be \"auto\" or a positive integer <= MIVIA_INGESTION_GLOBAL_WORKER_COUNT")
	}
	if ingestion.MaxWatchedDirectoryCount < 0 {
		return errors.New("MIVIA_INGESTION_MAX_WATCHED_DIRECTORY_COUNT must be non-negative")
	}
	if ingestion.TaskWarnAfter <= 0 {
		return errors.New("MIVIA_INGESTION_TASK_WARN_AFTER must be positive")
	}
	if ingestion.FullScanBatchSize <= 0 {
		return errors.New("MIVIA_INGESTION_FULL_SCAN_BATCH_SIZE must be positive")
	}
	if ingestion.SensitiveMarkerPolicy != sensitiveMarkerPolicySkipFile {
		return fmt.Errorf("MIVIA_INGESTION_SENSITIVE_MARKER_POLICY must be %q", sensitiveMarkerPolicySkipFile)
	}
	return nil
}

func configPathFromEnv() (string, bool) {
	value := os.Getenv("MIVIA_CONFIG_PATH")
	if value == "" {
		return defaultConfigPath, false
	}
	return value, true
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func getenvInt64(key string, fallback int64) (int64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return value, nil
}

func getenvInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return value, nil
}

func getenvAutoInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	return parseAutoIntString(key, raw)
}

func parseAutoIntString(name string, raw string) (int, error) {
	value := strings.TrimSpace(raw)
	if strings.EqualFold(value, "auto") {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be %q or a positive integer", name, "auto")
	}
	return parsed, nil
}

func getenvBool(key string, fallback bool) (bool, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return value, nil
}

func getenvDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return value, nil
}

func isLocalAddress(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
