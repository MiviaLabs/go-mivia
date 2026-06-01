package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestConfigValidate_NonLocalBind_ReturnsError(t *testing.T) {
	cfg := defaultConfig("test.toml")
	cfg.resolveAutoSettings(runtime.NumCPU())
	cfg.HTTPAddr = "0.0.0.0:8080"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected non-local bind to be rejected")
	} else if !errors.Is(err, ErrNonLoopbackBind) || strings.Contains(err.Error(), "0.0.0.0") {
		t.Fatalf("expected categorized non-loopback bind without raw addr, got %v", err)
	}
}

func TestConfigValidate_LocalBind_ReturnsNil(t *testing.T) {
	cfg := defaultConfig("test.toml")
	cfg.resolveAutoSettings(runtime.NumCPU())
	cfg.HTTPAddr = "127.0.0.1:8080"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected local bind to be valid: %v", err)
	}
}

func TestConfigValidate_LiveUpdatesRequireContentGraph(t *testing.T) {
	cfg := defaultConfig("test.toml")
	cfg.resolveAutoSettings(runtime.NumCPU())
	cfg.Ingestion.LiveUpdatesEnabled = true

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected live updates without content graph to fail")
	}
}

func TestConfigValidate_LiveUpdatesAllowDisabledLivePathPriority(t *testing.T) {
	cfg := defaultConfig("test.toml")
	cfg.resolveAutoSettings(runtime.NumCPU())
	cfg.Ingestion.ContentGraphEnabled = true
	cfg.Ingestion.LiveUpdatesEnabled = true
	cfg.Ingestion.LivePathPriority = false

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected disabled live path priority to be valid: %v", err)
	}
}

func TestConfigValidate_AgentActivityRawRetentionRequiresDebug(t *testing.T) {
	cfg := defaultConfig("test.toml")
	cfg.resolveAutoSettings(runtime.NumCPU())
	cfg.AgentActivity.RetainRawPayloads = true

	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "MIVIA_AGENT_ACTIVITY_RETAIN_RAW_PAYLOADS") {
		t.Fatalf("expected raw retention debug guard, got %v", err)
	}

	cfg.Debug.Enabled = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected debug-enabled raw retention to validate: %v", err)
	}
}

func TestLoad_AgentActivityRawRetentionEnvRequiresDebug(t *testing.T) {
	chdir(t, t.TempDir())
	clearConfigEnv(t)
	t.Setenv("MIVIA_AGENT_ACTIVITY_RETAIN_RAW_PAYLOADS", "true")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MIVIA_AGENT_ACTIVITY_RETAIN_RAW_PAYLOADS") {
		t.Fatalf("expected raw retention env guard, got %v", err)
	}

	t.Setenv("MIVIA_DEBUG_ENABLED", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected raw retention with debug to load: %v", err)
	}
	if !cfg.AgentActivity.RetainRawPayloads {
		t.Fatal("expected raw retention env override")
	}
}

func TestLoad_DefaultConfigMissing_UsesEnvOnlyDefaults(t *testing.T) {
	chdir(t, t.TempDir())
	clearConfigEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected env-only defaults to load: %v", err)
	}

	if cfg.ConfigPath != defaultConfigPath {
		t.Fatalf("expected default config path %q, got %q", defaultConfigPath, cfg.ConfigPath)
	}
	if cfg.HTTPAddr != defaultHTTPAddr {
		t.Fatalf("expected default HTTP addr %q, got %q", defaultHTTPAddr, cfg.HTTPAddr)
	}
	if len(cfg.Projects) != 0 {
		t.Fatalf("expected no projects when default config is absent, got %d", len(cfg.Projects))
	}
	if cfg.CPUCount != runtime.NumCPU() {
		t.Fatalf("expected auto CPU count %d, got %d", runtime.NumCPU(), cfg.CPUCount)
	}
	if cfg.Ingestion.WorkerCount != defaultIngestionWorkerCount ||
		cfg.Ingestion.GlobalWorkerCount != defaultIngestionGlobalWorkerCount ||
		cfg.Ingestion.PerProjectWorkerLimit != defaultIngestionPerProjectLimit {
		t.Fatalf("expected default scheduler worker settings, got %+v", cfg.Ingestion)
	}
}

func TestLoad_ExplicitConfigMissing_ReturnsError(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("MIVIA_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.toml"))

	_, err := Load()
	if err == nil {
		t.Fatal("expected missing explicit config to fail")
	}
	if !strings.Contains(err.Error(), "MIVIA_CONFIG_PATH points to a missing config file") {
		t.Fatalf("expected non-sensitive missing config error, got %v", err)
	}
}

func TestLoad_FileValuesAndEnvOverrides_ReturnsMergedConfig(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "mivia-server.local.toml")
	writeFile(t, path, `
version = 1

[server]
http_addr = "127.0.0.1:9090"
cpu_count = "4"
max_request_bytes = 2048
request_timeout = "11s"
read_header_timeout = "6s"
shutdown_timeout = "12s"

[storage]
ladybug_path = "data/from-file.lbug"
sqlite_path = "data/from-file.sqlite"

[sqlite]
wal_enabled = true
busy_timeout = "6s"
synchronous = "FULL"
checkpoint_after_ingestion = false

[debug]
enabled = true
runtime_metrics_enabled = true

[logging]
file_enabled = true
file_path = "data/from-file.log"

[ingestion]
content_graph_enabled = false
live_updates_enabled = false
ast_extraction_enabled = true
extractor_cache_enabled = true
debounce_interval = "3s"
max_file_bytes = 2097152
max_chunk_bytes = 8192
queue_depth = 64
worker_count = 1
full_scan_batch_size = 250
per_project_worker_limit = 1
live_path_priority = true
max_watched_directory_count = 32
task_warn_after = "15s"
initial_scan_on_start = false
sensitive_marker_policy = "skip_file"

[[projects]]
id = "example"
display_name = "Example"
description = "Synthetic project"
root_path = "/absolute/path/to/project"
enabled = true
classification = "internal"
graph_namespace = "example"
graph_storage = "in_memory"
digest_mode = "metadata_only"
update_policy = "manual"
include = ["**/*.go"]
exclude = [".git/**"]
follow_symlinks = false
max_file_bytes = 1048576
max_chunk_bytes = 4096
sensitive_marker_policy = "skip_file"
`)

	t.Setenv("MIVIA_CONFIG_PATH", path)
	t.Setenv("MIVIA_HTTP_ADDR", "localhost:8081")
	t.Setenv("MIVIA_LADYBUG_PATH", "data/from-env.lbug")
	t.Setenv("MIVIA_LOG_FILE_ENABLED", "false")
	t.Setenv("MIVIA_LOG_FILE_PATH", "data/from-env.log")
	t.Setenv("MIVIA_REQUEST_TIMEOUT", "13s")
	t.Setenv("MIVIA_INGESTION_QUEUE_DEPTH", "32")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected merged config to load: %v", err)
	}

	if cfg.ConfigPath != path {
		t.Fatalf("expected config path %q, got %q", path, cfg.ConfigPath)
	}
	if cfg.HTTPAddr != "localhost:8081" {
		t.Fatalf("expected env HTTP override, got %q", cfg.HTTPAddr)
	}
	if cfg.CPUCount != 4 {
		t.Fatalf("expected file CPU count, got %d", cfg.CPUCount)
	}
	if cfg.LadybugPath != "data/from-env.lbug" {
		t.Fatalf("expected env Ladybug path override, got %q", cfg.LadybugPath)
	}
	if cfg.SQLitePath != "data/from-file.sqlite" {
		t.Fatalf("expected file SQLite path, got %q", cfg.SQLitePath)
	}
	if !cfg.Debug.Enabled || !cfg.Debug.RuntimeMetricsEnabled {
		t.Fatalf("expected file debug config, got %+v", cfg.Debug)
	}
	if !cfg.SQLite.WALEnabled || cfg.SQLite.BusyTimeout != 6*time.Second || cfg.SQLite.Synchronous != "FULL" || cfg.SQLite.CheckpointAfterIngestion {
		t.Fatalf("expected file SQLite pragmas, got %+v", cfg.SQLite)
	}
	if cfg.Logging.FileEnabled || cfg.Logging.FilePath != "data/from-env.log" {
		t.Fatalf("expected env logging overrides, got %+v", cfg.Logging)
	}
	if cfg.RequestTimeout != 13*time.Second {
		t.Fatalf("expected env request timeout override, got %s", cfg.RequestTimeout)
	}
	if cfg.ReadHeaderTimeout != 6*time.Second {
		t.Fatalf("expected file read header timeout, got %s", cfg.ReadHeaderTimeout)
	}
	if cfg.Ingestion.DebounceInterval != 3*time.Second {
		t.Fatalf("expected file ingestion debounce interval, got %s", cfg.Ingestion.DebounceInterval)
	}
	if cfg.Ingestion.MaxFileBytes != 2097152 {
		t.Fatalf("expected file ingestion max file bytes, got %d", cfg.Ingestion.MaxFileBytes)
	}
	if cfg.Ingestion.QueueDepth != 32 {
		t.Fatalf("expected env ingestion queue depth override, got %d", cfg.Ingestion.QueueDepth)
	}
	if cfg.Ingestion.FullScanBatchSize != 250 {
		t.Fatalf("expected file full scan batch size, got %d", cfg.Ingestion.FullScanBatchSize)
	}
	if cfg.Ingestion.GlobalWorkerCount != 1 || cfg.Ingestion.PerProjectWorkerLimit != 1 {
		t.Fatalf("expected scheduler worker settings, got %+v", cfg.Ingestion)
	}
	if cfg.Ingestion.MaxWatchedDirectoryCount != 32 || cfg.Ingestion.TaskWarnAfter != 15*time.Second {
		t.Fatalf("expected watcher/task settings, got %+v", cfg.Ingestion)
	}
	if len(cfg.Projects) != 1 {
		t.Fatalf("expected one project, got %d", len(cfg.Projects))
	}
	if cfg.Projects[0].ID != "example" || cfg.Projects[0].DigestMode != digestModeMetadataOnly {
		t.Fatalf("unexpected project config: %+v", cfg.Projects[0])
	}
	if cfg.Projects[0].GraphStorage != graphStorageInMemory {
		t.Fatalf("expected project graph storage override, got %q", cfg.Projects[0].GraphStorage)
	}
	if cfg.Projects[0].MaxChunkBytes != 4096 {
		t.Fatalf("expected project max chunk bytes, got %d", cfg.Projects[0].MaxChunkBytes)
	}
}

func TestLoad_EnvOverridesIngestion_ReturnsMergedConfig(t *testing.T) {
	chdir(t, t.TempDir())
	clearConfigEnv(t)
	t.Setenv("MIVIA_INGESTION_CONTENT_GRAPH_ENABLED", "true")
	t.Setenv("MIVIA_INGESTION_LIVE_UPDATES_ENABLED", "true")
	t.Setenv("MIVIA_INGESTION_AST_EXTRACTION_ENABLED", "true")
	t.Setenv("MIVIA_INGESTION_EXTRACTOR_CACHE_ENABLED", "true")
	t.Setenv("MIVIA_INGESTION_DEBOUNCE_INTERVAL", "4s")
	t.Setenv("MIVIA_INGESTION_MAX_FILE_BYTES", "1024")
	t.Setenv("MIVIA_INGESTION_MAX_CHUNK_BYTES", "512")
	t.Setenv("MIVIA_INGESTION_QUEUE_DEPTH", "8")
	t.Setenv("MIVIA_INGESTION_WORKER_COUNT", "3")
	t.Setenv("MIVIA_INGESTION_PER_PROJECT_WORKER_LIMIT", "2")
	t.Setenv("MIVIA_INGESTION_LIVE_PATH_PRIORITY", "true")
	t.Setenv("MIVIA_INGESTION_MAX_WATCHED_DIRECTORY_COUNT", "9")
	t.Setenv("MIVIA_INGESTION_TASK_WARN_AFTER", "7s")
	t.Setenv("MIVIA_INGESTION_FULL_SCAN_BATCH_SIZE", "20000")
	t.Setenv("MIVIA_INGESTION_INITIAL_SCAN_ON_START", "true")
	t.Setenv("MIVIA_INGESTION_SENSITIVE_MARKER_POLICY", "skip_file")
	t.Setenv("MIVIA_DEBUG_ENABLED", "true")
	t.Setenv("MIVIA_DEBUG_PPROF_ENABLED", "true")
	t.Setenv("MIVIA_DEBUG_EXPVAR_ENABLED", "true")
	t.Setenv("MIVIA_DEBUG_RUNTIME_METRICS_ENABLED", "true")
	t.Setenv("MIVIA_SQLITE_WAL_ENABLED", "false")
	t.Setenv("MIVIA_SQLITE_BUSY_TIMEOUT", "8s")
	t.Setenv("MIVIA_SQLITE_SYNCHRONOUS", "NORMAL")
	t.Setenv("MIVIA_SQLITE_CHECKPOINT_AFTER_INGESTION", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected env ingestion settings to load: %v", err)
	}

	if !cfg.Ingestion.ContentGraphEnabled || !cfg.Ingestion.LiveUpdatesEnabled {
		t.Fatalf("expected ingestion booleans enabled: %+v", cfg.Ingestion)
	}
	if cfg.Ingestion.DebounceInterval != 4*time.Second {
		t.Fatalf("expected debounce override, got %s", cfg.Ingestion.DebounceInterval)
	}
	if cfg.Ingestion.MaxFileBytes != 1024 || cfg.Ingestion.MaxChunkBytes != 512 {
		t.Fatalf("expected ingestion byte overrides: %+v", cfg.Ingestion)
	}
	if cfg.Ingestion.QueueDepth != 8 || cfg.Ingestion.WorkerCount != 3 || cfg.Ingestion.GlobalWorkerCount != 3 || cfg.Ingestion.PerProjectWorkerLimit != 2 {
		t.Fatalf("expected queue/worker overrides: %+v", cfg.Ingestion)
	}
	if cfg.Ingestion.MaxWatchedDirectoryCount != 9 || cfg.Ingestion.TaskWarnAfter != 7*time.Second {
		t.Fatalf("expected watcher/task env overrides: %+v", cfg.Ingestion)
	}
	if cfg.Ingestion.FullScanBatchSize != 20000 {
		t.Fatalf("expected full scan batch size override, got %d", cfg.Ingestion.FullScanBatchSize)
	}
	if !cfg.Debug.Enabled || !cfg.Debug.PprofEnabled || !cfg.Debug.ExpvarEnabled || !cfg.Debug.RuntimeMetricsEnabled {
		t.Fatalf("expected debug env overrides: %+v", cfg.Debug)
	}
	if cfg.SQLite.WALEnabled || cfg.SQLite.BusyTimeout != 8*time.Second || cfg.SQLite.Synchronous != "NORMAL" || cfg.SQLite.CheckpointAfterIngestion {
		t.Fatalf("expected SQLite env overrides: %+v", cfg.SQLite)
	}
	if !cfg.Ingestion.InitialScanOnStart {
		t.Fatal("expected initial scan override")
	}
}

func TestLoad_DefaultIngestionCoverageCapsAreUnlimited(t *testing.T) {
	chdir(t, t.TempDir())
	clearConfigEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected defaults to load: %v", err)
	}
	if cfg.Ingestion.MaxFileBytes != 0 {
		t.Fatalf("expected default max file bytes to be unlimited, got %d", cfg.Ingestion.MaxFileBytes)
	}
}

func TestConfigValidate_FullScanBatchSizeAllowsLargePositiveValues(t *testing.T) {
	cfg := defaultConfig("test.toml")
	cfg.resolveAutoSettings(runtime.NumCPU())
	cfg.Ingestion.FullScanBatchSize = 20000

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected large positive full scan batch size to be valid: %v", err)
	}
}

func TestConfigValidate_FullScanBatchSizeRejectsNonPositiveValues(t *testing.T) {
	for _, value := range []int{0, -1} {
		t.Run(strconv.Itoa(value), func(t *testing.T) {
			cfg := defaultConfig("test.toml")
			cfg.resolveAutoSettings(runtime.NumCPU())
			cfg.Ingestion.FullScanBatchSize = value

			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "FULL_SCAN_BATCH_SIZE") {
				t.Fatalf("expected full scan batch size validation error, got %v", err)
			}
		})
	}
}

func TestLoad_CPUCountAutoAndIngestionAutoUseSQLiteFriendlyDefaults(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "mivia-server.local.toml")
	writeFile(t, path, `
version = 1

[server]
http_addr = "127.0.0.1:9090"
cpu_count = "auto"

[ingestion]
worker_count = "auto"
global_worker_count = "auto"
per_project_worker_limit = "auto"
`)
	t.Setenv("MIVIA_CONFIG_PATH", path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected auto CPU config to load: %v", err)
	}
	if cfg.CPUCount != runtime.NumCPU() {
		t.Fatalf("expected runtime CPU count %d, got %d", runtime.NumCPU(), cfg.CPUCount)
	}
	if cfg.Ingestion.WorkerCount != defaultIngestionWorkerCount ||
		cfg.Ingestion.GlobalWorkerCount != defaultIngestionGlobalWorkerCount ||
		cfg.Ingestion.PerProjectWorkerLimit != defaultIngestionPerProjectLimit {
		t.Fatalf("expected ingestion auto worker counts to use SQLite-friendly defaults, got %+v", cfg.Ingestion)
	}
}

func TestLoad_CPUEnvOverrideDoesNotDriveIngestionDefaults(t *testing.T) {
	chdir(t, t.TempDir())
	clearConfigEnv(t)
	t.Setenv("MIVIA_CPU_COUNT", "3")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected CPU env override to load: %v", err)
	}
	if cfg.CPUCount != 3 {
		t.Fatalf("expected CPU count override, got %d", cfg.CPUCount)
	}
	if cfg.Ingestion.WorkerCount != defaultIngestionWorkerCount ||
		cfg.Ingestion.GlobalWorkerCount != defaultIngestionGlobalWorkerCount ||
		cfg.Ingestion.PerProjectWorkerLimit != defaultIngestionPerProjectLimit {
		t.Fatalf("expected ingestion defaults to ignore CPU count, got %+v", cfg.Ingestion)
	}
}

func TestLoad_CPUCountRejectsInvalidValues(t *testing.T) {
	chdir(t, t.TempDir())
	clearConfigEnv(t)
	t.Setenv("MIVIA_CPU_COUNT", "0")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "MIVIA_CPU_COUNT") {
		t.Fatalf("expected invalid CPU count error, got %v", err)
	}
}

func TestLoad_LoggingFileRequiresExplicitOptInAndPath(t *testing.T) {
	chdir(t, t.TempDir())
	clearConfigEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected defaults to load: %v", err)
	}
	if cfg.Logging.FileEnabled || cfg.Logging.FilePath != "" {
		t.Fatalf("expected file logging disabled by default, got %+v", cfg.Logging)
	}

	t.Setenv("MIVIA_LOG_FILE_ENABLED", "true")
	_, err = Load()
	if err == nil || !strings.Contains(err.Error(), "MIVIA_LOG_FILE_PATH") {
		t.Fatalf("expected missing log file path error, got %v", err)
	}

	t.Setenv("MIVIA_LOG_FILE_PATH", "data/mivia-server.log")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("expected file logging override to load: %v", err)
	}
	if !cfg.Logging.FileEnabled || cfg.Logging.FilePath != "data/mivia-server.log" {
		t.Fatalf("expected file logging env overrides, got %+v", cfg.Logging)
	}
}

func TestLoad_DefaultConfigPresent_LoadsFile(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	writeFile(t, filepath.Join(dir, defaultConfigPath), `
version = 1

[server]
http_addr = "localhost:8082"

[storage]
ladybug_path = "data/default-file.lbug"
sqlite_path = "data/default-file.sqlite"
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected default file to load: %v", err)
	}
	if cfg.HTTPAddr != "localhost:8082" {
		t.Fatalf("expected default file HTTP addr, got %q", cfg.HTTPAddr)
	}
	if cfg.SQLitePath != "data/default-file.sqlite" {
		t.Fatalf("expected default file SQLite path, got %q", cfg.SQLitePath)
	}
}

func TestLoad_ExplicitInvalidConfig_ReturnsError(t *testing.T) {
	clearConfigEnv(t)
	path := filepath.Join(t.TempDir(), "mivia-server.local.toml")
	writeFile(t, path, "not toml")
	t.Setenv("MIVIA_CONFIG_PATH", path)

	_, err := Load()
	if err == nil {
		t.Fatal("expected invalid explicit config to fail")
	}
	if !strings.Contains(err.Error(), "MIVIA_CONFIG_PATH config is invalid") {
		t.Fatalf("expected explicit invalid config error, got %v", err)
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"MIVIA_CONFIG_PATH",
		"MIVIA_CPU_COUNT",
		"MIVIA_HTTP_ADDR",
		"MIVIA_LADYBUG_PATH",
		"MIVIA_SQLITE_PATH",
		"MIVIA_SQLITE_WAL_ENABLED",
		"MIVIA_SQLITE_BUSY_TIMEOUT",
		"MIVIA_SQLITE_SYNCHRONOUS",
		"MIVIA_SQLITE_CHECKPOINT_AFTER_INGESTION",
		"MIVIA_DEBUG_ENABLED",
		"MIVIA_DEBUG_PPROF_ENABLED",
		"MIVIA_DEBUG_EXPVAR_ENABLED",
		"MIVIA_DEBUG_RUNTIME_METRICS_ENABLED",
		"MIVIA_LOG_FILE_ENABLED",
		"MIVIA_LOG_FILE_PATH",
		"MIVIA_MAX_REQUEST_BYTES",
		"MIVIA_REQUEST_TIMEOUT",
		"MIVIA_READ_HEADER_TIMEOUT",
		"MIVIA_SHUTDOWN_TIMEOUT",
		"MIVIA_INGESTION_CONTENT_GRAPH_ENABLED",
		"MIVIA_INGESTION_LIVE_UPDATES_ENABLED",
		"MIVIA_INGESTION_AST_EXTRACTION_ENABLED",
		"MIVIA_INGESTION_EXTRACTOR_CACHE_ENABLED",
		"MIVIA_INGESTION_DEBOUNCE_INTERVAL",
		"MIVIA_INGESTION_MAX_FILE_BYTES",
		"MIVIA_INGESTION_MAX_CHUNK_BYTES",
		"MIVIA_INGESTION_QUEUE_DEPTH",
		"MIVIA_INGESTION_WORKER_COUNT",
		"MIVIA_INGESTION_GLOBAL_WORKER_COUNT",
		"MIVIA_INGESTION_PER_PROJECT_WORKER_LIMIT",
		"MIVIA_INGESTION_LIVE_PATH_PRIORITY",
		"MIVIA_INGESTION_MAX_WATCHED_DIRECTORY_COUNT",
		"MIVIA_INGESTION_TASK_WARN_AFTER",
		"MIVIA_INGESTION_FULL_SCAN_BATCH_SIZE",
		"MIVIA_INGESTION_INITIAL_SCAN_ON_START",
		"MIVIA_INGESTION_SENSITIVE_MARKER_POLICY",
	} {
		t.Setenv(key, "")
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimLeft(content, "\n")), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
