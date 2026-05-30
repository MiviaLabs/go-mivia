package config

import (
	"fmt"
	"os"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	configVersion          = 1
	digestModeMetadataOnly = "metadata_only"
	digestModeContentGraph = "content_graph"
	updatePolicyManual     = "manual"
	updatePolicyLive       = "live"
	graphStoragePersistent = "persistent"
	graphStorageInMemory   = "in_memory"
)

type fileConfig struct {
	Version   int                  `toml:"version"`
	Server    *fileServerConfig    `toml:"server"`
	Storage   *fileStorageConfig   `toml:"storage"`
	Ingestion *fileIngestionConfig `toml:"ingestion"`
	Projects  []fileProjectConfig  `toml:"projects"`
}

type fileServerConfig struct {
	HTTPAddr          *string `toml:"http_addr"`
	MaxRequestBytes   *int64  `toml:"max_request_bytes"`
	RequestTimeout    *string `toml:"request_timeout"`
	ReadHeaderTimeout *string `toml:"read_header_timeout"`
	ShutdownTimeout   *string `toml:"shutdown_timeout"`
}

type fileStorageConfig struct {
	LadybugPath *string `toml:"ladybug_path"`
	SQLitePath  *string `toml:"sqlite_path"`
}

type fileIngestionConfig struct {
	ContentGraphEnabled      *bool   `toml:"content_graph_enabled"`
	LiveUpdatesEnabled       *bool   `toml:"live_updates_enabled"`
	ASTExtractionEnabled     *bool   `toml:"ast_extraction_enabled"`
	ExtractorCacheEnabled    *bool   `toml:"extractor_cache_enabled"`
	DebounceInterval         *string `toml:"debounce_interval"`
	MaxFileBytes             *int64  `toml:"max_file_bytes"`
	MaxChunkBytes            *int    `toml:"max_chunk_bytes"`
	QueueDepth               *int    `toml:"queue_depth"`
	WorkerCount              *int    `toml:"worker_count"`
	GlobalWorkerCount        *int    `toml:"global_worker_count"`
	PerProjectWorkerLimit    *int    `toml:"per_project_worker_limit"`
	LivePathPriority         *bool   `toml:"live_path_priority"`
	MaxWatchedDirectoryCount *int    `toml:"max_watched_directory_count"`
	TaskWarnAfter            *string `toml:"task_warn_after"`
	FullScanBatchSize        *int    `toml:"full_scan_batch_size"`
	InitialScanOnStart       *bool   `toml:"initial_scan_on_start"`
	SensitiveMarkerPolicy    string  `toml:"sensitive_marker_policy"`
}

type fileProjectConfig struct {
	ID                    string   `toml:"id"`
	DisplayName           string   `toml:"display_name"`
	Description           string   `toml:"description"`
	RootPath              string   `toml:"root_path"`
	Enabled               bool     `toml:"enabled"`
	Classification        string   `toml:"classification"`
	GraphNamespace        string   `toml:"graph_namespace"`
	GraphStorage          string   `toml:"graph_storage"`
	DigestMode            string   `toml:"digest_mode"`
	UpdatePolicy          string   `toml:"update_policy"`
	Include               []string `toml:"include"`
	Exclude               []string `toml:"exclude"`
	FollowSymlinks        bool     `toml:"follow_symlinks"`
	MaxFileBytes          *int64   `toml:"max_file_bytes"`
	MaxChunkBytes         *int     `toml:"max_chunk_bytes"`
	SensitiveMarkerPolicy string   `toml:"sensitive_marker_policy"`
}

func loadFileConfig(path string) (fileConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileConfig{}, err
	}
	defer file.Close()

	var cfg fileConfig
	decoder := toml.NewDecoder(file).DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return fileConfig{}, err
	}
	if err := cfg.validate(); err != nil {
		return fileConfig{}, err
	}
	return cfg, nil
}

func (cfg fileConfig) validate() error {
	if cfg.Version != configVersion {
		return fmt.Errorf("version must be %d", configVersion)
	}
	if cfg.Ingestion != nil {
		if err := cfg.Ingestion.validate(); err != nil {
			return err
		}
	}
	for i, project := range cfg.Projects {
		switch project.DigestMode {
		case "", digestModeMetadataOnly, digestModeContentGraph:
		default:
			return fmt.Errorf("projects[%d].digest_mode must be %q or %q", i, digestModeMetadataOnly, digestModeContentGraph)
		}
		switch project.UpdatePolicy {
		case "", updatePolicyManual, updatePolicyLive:
		default:
			return fmt.Errorf("projects[%d].update_policy must be %q or %q", i, updatePolicyManual, updatePolicyLive)
		}
		switch project.GraphStorage {
		case "", graphStoragePersistent, graphStorageInMemory:
		default:
			return fmt.Errorf("projects[%d].graph_storage must be %q or %q", i, graphStoragePersistent, graphStorageInMemory)
		}
		if project.MaxFileBytes != nil && *project.MaxFileBytes <= 0 {
			return fmt.Errorf("projects[%d].max_file_bytes must be positive", i)
		}
		if project.MaxChunkBytes != nil && *project.MaxChunkBytes <= 0 {
			return fmt.Errorf("projects[%d].max_chunk_bytes must be positive", i)
		}
		if project.SensitiveMarkerPolicy != "" && project.SensitiveMarkerPolicy != sensitiveMarkerPolicySkipFile {
			return fmt.Errorf("projects[%d].sensitive_marker_policy must be %q", i, sensitiveMarkerPolicySkipFile)
		}
	}
	return nil
}

func (cfg fileIngestionConfig) validate() error {
	if cfg.MaxFileBytes != nil && *cfg.MaxFileBytes <= 0 {
		return fmt.Errorf("ingestion.max_file_bytes must be positive")
	}
	if cfg.MaxChunkBytes != nil && *cfg.MaxChunkBytes <= 0 {
		return fmt.Errorf("ingestion.max_chunk_bytes must be positive")
	}
	if cfg.QueueDepth != nil && *cfg.QueueDepth <= 0 {
		return fmt.Errorf("ingestion.queue_depth must be positive")
	}
	if cfg.WorkerCount != nil && *cfg.WorkerCount <= 0 {
		return fmt.Errorf("ingestion.worker_count must be positive")
	}
	if cfg.GlobalWorkerCount != nil && *cfg.GlobalWorkerCount <= 0 {
		return fmt.Errorf("ingestion.global_worker_count must be positive")
	}
	if cfg.PerProjectWorkerLimit != nil && *cfg.PerProjectWorkerLimit <= 0 {
		return fmt.Errorf("ingestion.per_project_worker_limit must be positive")
	}
	if cfg.MaxWatchedDirectoryCount != nil && *cfg.MaxWatchedDirectoryCount < 0 {
		return fmt.Errorf("ingestion.max_watched_directory_count must be non-negative")
	}
	if cfg.TaskWarnAfter != nil {
		if _, err := time.ParseDuration(*cfg.TaskWarnAfter); err != nil {
			return fmt.Errorf("ingestion.task_warn_after must be a valid duration")
		}
	}
	if cfg.FullScanBatchSize != nil && (*cfg.FullScanBatchSize <= 0 || *cfg.FullScanBatchSize > 5000) {
		return fmt.Errorf("ingestion.full_scan_batch_size must be positive and <= 5000")
	}
	if cfg.SensitiveMarkerPolicy != "" && cfg.SensitiveMarkerPolicy != sensitiveMarkerPolicySkipFile {
		return fmt.Errorf("ingestion.sensitive_marker_policy must be %q", sensitiveMarkerPolicySkipFile)
	}
	return nil
}

func (cfg fileConfig) applyTo(base Config) (Config, error) {
	if cfg.Server != nil {
		if cfg.Server.HTTPAddr != nil {
			base.HTTPAddr = *cfg.Server.HTTPAddr
		}
		if cfg.Server.MaxRequestBytes != nil {
			base.MaxRequestBytes = *cfg.Server.MaxRequestBytes
		}
		var err error
		if base.RequestTimeout, err = applyDuration("server.request_timeout", cfg.Server.RequestTimeout, base.RequestTimeout); err != nil {
			return Config{}, err
		}
		if base.ReadHeaderTimeout, err = applyDuration("server.read_header_timeout", cfg.Server.ReadHeaderTimeout, base.ReadHeaderTimeout); err != nil {
			return Config{}, err
		}
		if base.ShutdownTimeout, err = applyDuration("server.shutdown_timeout", cfg.Server.ShutdownTimeout, base.ShutdownTimeout); err != nil {
			return Config{}, err
		}
	}

	if cfg.Storage != nil {
		if cfg.Storage.LadybugPath != nil {
			base.LadybugPath = *cfg.Storage.LadybugPath
		}
		if cfg.Storage.SQLitePath != nil {
			base.SQLitePath = *cfg.Storage.SQLitePath
		}
	}

	if cfg.Ingestion != nil {
		if cfg.Ingestion.ContentGraphEnabled != nil {
			base.Ingestion.ContentGraphEnabled = *cfg.Ingestion.ContentGraphEnabled
		}
		if cfg.Ingestion.LiveUpdatesEnabled != nil {
			base.Ingestion.LiveUpdatesEnabled = *cfg.Ingestion.LiveUpdatesEnabled
		}
		if cfg.Ingestion.ASTExtractionEnabled != nil {
			base.Ingestion.ASTExtractionEnabled = *cfg.Ingestion.ASTExtractionEnabled
		}
		if cfg.Ingestion.ExtractorCacheEnabled != nil {
			base.Ingestion.ExtractorCacheEnabled = *cfg.Ingestion.ExtractorCacheEnabled
		}
		var err error
		if base.Ingestion.DebounceInterval, err = applyDuration("ingestion.debounce_interval", cfg.Ingestion.DebounceInterval, base.Ingestion.DebounceInterval); err != nil {
			return Config{}, err
		}
		if cfg.Ingestion.MaxFileBytes != nil {
			base.Ingestion.MaxFileBytes = *cfg.Ingestion.MaxFileBytes
		}
		if cfg.Ingestion.MaxChunkBytes != nil {
			base.Ingestion.MaxChunkBytes = *cfg.Ingestion.MaxChunkBytes
		}
		if cfg.Ingestion.QueueDepth != nil {
			base.Ingestion.QueueDepth = *cfg.Ingestion.QueueDepth
		}
		if cfg.Ingestion.WorkerCount != nil {
			base.Ingestion.WorkerCount = *cfg.Ingestion.WorkerCount
		}
		if cfg.Ingestion.GlobalWorkerCount != nil {
			base.Ingestion.GlobalWorkerCount = *cfg.Ingestion.GlobalWorkerCount
		} else if cfg.Ingestion.WorkerCount != nil {
			base.Ingestion.GlobalWorkerCount = *cfg.Ingestion.WorkerCount
		}
		if cfg.Ingestion.PerProjectWorkerLimit != nil {
			base.Ingestion.PerProjectWorkerLimit = *cfg.Ingestion.PerProjectWorkerLimit
		}
		if cfg.Ingestion.LivePathPriority != nil {
			base.Ingestion.LivePathPriority = *cfg.Ingestion.LivePathPriority
		}
		if cfg.Ingestion.MaxWatchedDirectoryCount != nil {
			base.Ingestion.MaxWatchedDirectoryCount = *cfg.Ingestion.MaxWatchedDirectoryCount
		}
		if base.Ingestion.TaskWarnAfter, err = applyDuration("ingestion.task_warn_after", cfg.Ingestion.TaskWarnAfter, base.Ingestion.TaskWarnAfter); err != nil {
			return Config{}, err
		}
		if cfg.Ingestion.FullScanBatchSize != nil {
			base.Ingestion.FullScanBatchSize = *cfg.Ingestion.FullScanBatchSize
		}
		if cfg.Ingestion.InitialScanOnStart != nil {
			base.Ingestion.InitialScanOnStart = *cfg.Ingestion.InitialScanOnStart
		}
		if cfg.Ingestion.SensitiveMarkerPolicy != "" {
			base.Ingestion.SensitiveMarkerPolicy = cfg.Ingestion.SensitiveMarkerPolicy
		}
	}

	base.Projects = make([]Project, 0, len(cfg.Projects))
	for _, project := range cfg.Projects {
		base.Projects = append(base.Projects, project.toProject())
	}
	return base, nil
}

func applyDuration(name string, value *string, fallback time.Duration) (time.Duration, error) {
	if value == nil {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(*value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration: %w", name, err)
	}
	return parsed, nil
}

func (project fileProjectConfig) toProject() Project {
	digestMode := project.DigestMode
	if digestMode == "" {
		digestMode = digestModeMetadataOnly
	}
	updatePolicy := project.UpdatePolicy
	if updatePolicy == "" {
		updatePolicy = updatePolicyManual
	}
	graphStorage := project.GraphStorage
	if graphStorage == "" {
		graphStorage = graphStoragePersistent
	}

	cfgProject := Project{
		ID:             project.ID,
		DisplayName:    project.DisplayName,
		Description:    project.Description,
		RootPath:       project.RootPath,
		Enabled:        project.Enabled,
		Classification: project.Classification,
		GraphNamespace: project.GraphNamespace,
		GraphStorage:   graphStorage,
		DigestMode:     digestMode,
		UpdatePolicy:   updatePolicy,
		Include:        append([]string(nil), project.Include...),
		Exclude:        append([]string(nil), project.Exclude...),
		FollowSymlinks: project.FollowSymlinks,
	}
	if project.MaxFileBytes != nil {
		cfgProject.MaxFileBytes = *project.MaxFileBytes
	}
	if project.MaxChunkBytes != nil {
		cfgProject.MaxChunkBytes = *project.MaxChunkBytes
	}
	cfgProject.SensitiveMarkerPolicy = project.SensitiveMarkerPolicy
	return cfgProject
}
