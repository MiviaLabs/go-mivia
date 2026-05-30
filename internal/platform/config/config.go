package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strconv"
	"time"
)

const (
	defaultConfigPath                 = "configs/agent-server.local.toml"
	defaultHTTPAddr                   = "127.0.0.1:8080"
	defaultLadybugPath                = "data/mivialabs.lbug"
	defaultSQLitePath                 = "data/mivialabs-config.sqlite"
	defaultMaxRequestBytes            = int64(1 << 20)
	defaultRequestTimeout             = 10 * time.Second
	defaultReadHeaderTimeout          = 5 * time.Second
	defaultShutdownTimeout            = 10 * time.Second
	defaultIngestionDebounceInterval  = 2 * time.Second
	defaultIngestionMaxFileBytes      = int64(1 << 20)
	defaultIngestionMaxChunkBytes     = 16 * 1024
	defaultIngestionQueueDepth        = 128
	defaultIngestionWorkerCount       = 2
	defaultIngestionGlobalWorkerCount = 4
	defaultIngestionPerProjectLimit   = 2
	defaultIngestionFullScanBatchSize = 500
	defaultIngestionTaskWarnAfter     = 30 * time.Second
	defaultSensitiveMarkerPolicy      = "skip_file"
	sensitiveMarkerPolicySkipFile     = "skip_file"
)

type Config struct {
	ConfigPath        string
	HTTPAddr          string
	LadybugPath       string
	SQLitePath        string
	MaxRequestBytes   int64
	RequestTimeout    time.Duration
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration
	Ingestion         Ingestion
	Projects          []Project
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
	DisplayName           string
	Description           string
	RootPath              string
	Enabled               bool
	Classification        string
	GraphNamespace        string
	GraphStorage          string
	DigestMode            string
	UpdatePolicy          string
	Include               []string
	Exclude               []string
	FollowSymlinks        bool
	MaxFileBytes          int64
	MaxChunkBytes         int
	SensitiveMarkerPolicy string
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

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func defaultConfig(configPath string) Config {
	return Config{
		ConfigPath:        configPath,
		HTTPAddr:          defaultHTTPAddr,
		LadybugPath:       defaultLadybugPath,
		SQLitePath:        defaultSQLitePath,
		MaxRequestBytes:   defaultMaxRequestBytes,
		RequestTimeout:    defaultRequestTimeout,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
		Ingestion:         defaultIngestion(),
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
		MaxFileBytes:             defaultIngestionMaxFileBytes,
		MaxChunkBytes:            defaultIngestionMaxChunkBytes,
		QueueDepth:               defaultIngestionQueueDepth,
		WorkerCount:              defaultIngestionWorkerCount,
		GlobalWorkerCount:        defaultIngestionGlobalWorkerCount,
		PerProjectWorkerLimit:    defaultIngestionPerProjectLimit,
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
	cfg.LadybugPath = getenv("MIVIA_LADYBUG_PATH", cfg.LadybugPath)
	cfg.SQLitePath = getenv("MIVIA_SQLITE_PATH", cfg.SQLitePath)
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
	if cfg.Ingestion.WorkerCount, err = getenvInt("MIVIA_INGESTION_WORKER_COUNT", cfg.Ingestion.WorkerCount); err != nil {
		return err
	}
	globalWorkerCountOverridden := os.Getenv("MIVIA_INGESTION_GLOBAL_WORKER_COUNT") != ""
	if cfg.Ingestion.GlobalWorkerCount, err = getenvInt("MIVIA_INGESTION_GLOBAL_WORKER_COUNT", cfg.Ingestion.GlobalWorkerCount); err != nil {
		return err
	}
	if workerCountOverridden && !globalWorkerCountOverridden {
		cfg.Ingestion.GlobalWorkerCount = cfg.Ingestion.WorkerCount
	}
	if cfg.Ingestion.PerProjectWorkerLimit, err = getenvInt("MIVIA_INGESTION_PER_PROJECT_WORKER_LIMIT", cfg.Ingestion.PerProjectWorkerLimit); err != nil {
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
	return nil
}

func (cfg Config) Validate() error {
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
	if ingestion.MaxFileBytes <= 0 {
		return errors.New("MIVIA_INGESTION_MAX_FILE_BYTES must be positive")
	}
	if ingestion.MaxChunkBytes <= 0 {
		return errors.New("MIVIA_INGESTION_MAX_CHUNK_BYTES must be positive")
	}
	if ingestion.QueueDepth <= 0 {
		return errors.New("MIVIA_INGESTION_QUEUE_DEPTH must be positive")
	}
	if ingestion.WorkerCount <= 0 {
		return errors.New("MIVIA_INGESTION_WORKER_COUNT must be positive")
	}
	if ingestion.GlobalWorkerCount <= 0 {
		return errors.New("MIVIA_INGESTION_GLOBAL_WORKER_COUNT must be positive")
	}
	if ingestion.PerProjectWorkerLimit <= 0 || ingestion.PerProjectWorkerLimit > ingestion.GlobalWorkerCount {
		return errors.New("MIVIA_INGESTION_PER_PROJECT_WORKER_LIMIT must be positive and <= MIVIA_INGESTION_GLOBAL_WORKER_COUNT")
	}
	if ingestion.LiveUpdatesEnabled && !ingestion.LivePathPriority {
		return errors.New("MIVIA_INGESTION_LIVE_PATH_PRIORITY must remain true while live updates are enabled")
	}
	if ingestion.MaxWatchedDirectoryCount < 0 {
		return errors.New("MIVIA_INGESTION_MAX_WATCHED_DIRECTORY_COUNT must be non-negative")
	}
	if ingestion.TaskWarnAfter <= 0 {
		return errors.New("MIVIA_INGESTION_TASK_WARN_AFTER must be positive")
	}
	if ingestion.FullScanBatchSize <= 0 || ingestion.FullScanBatchSize > 5000 {
		return errors.New("MIVIA_INGESTION_FULL_SCAN_BATCH_SIZE must be positive and <= 5000")
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
