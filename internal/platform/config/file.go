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
	updatePolicyManual     = "manual"
)

type fileConfig struct {
	Version  int                 `toml:"version"`
	Server   *fileServerConfig   `toml:"server"`
	Storage  *fileStorageConfig  `toml:"storage"`
	Projects []fileProjectConfig `toml:"projects"`
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

type fileProjectConfig struct {
	ID             string   `toml:"id"`
	DisplayName    string   `toml:"display_name"`
	Description    string   `toml:"description"`
	RootPath       string   `toml:"root_path"`
	Enabled        bool     `toml:"enabled"`
	Classification string   `toml:"classification"`
	GraphNamespace string   `toml:"graph_namespace"`
	DigestMode     string   `toml:"digest_mode"`
	UpdatePolicy   string   `toml:"update_policy"`
	Include        []string `toml:"include"`
	Exclude        []string `toml:"exclude"`
	FollowSymlinks bool     `toml:"follow_symlinks"`
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
	for i, project := range cfg.Projects {
		if project.DigestMode != "" && project.DigestMode != digestModeMetadataOnly {
			return fmt.Errorf("projects[%d].digest_mode must be %q", i, digestModeMetadataOnly)
		}
		if project.UpdatePolicy != "" && project.UpdatePolicy != updatePolicyManual {
			return fmt.Errorf("projects[%d].update_policy must be %q", i, updatePolicyManual)
		}
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

	return Project{
		ID:             project.ID,
		DisplayName:    project.DisplayName,
		Description:    project.Description,
		RootPath:       project.RootPath,
		Enabled:        project.Enabled,
		Classification: project.Classification,
		GraphNamespace: project.GraphNamespace,
		DigestMode:     digestMode,
		UpdatePolicy:   updatePolicy,
		Include:        append([]string(nil), project.Include...),
		Exclude:        append([]string(nil), project.Exclude...),
		FollowSymlinks: project.FollowSymlinks,
	}
}
