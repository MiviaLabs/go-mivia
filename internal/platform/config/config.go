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
	defaultConfigPath        = "configs/agent-server.local.toml"
	defaultHTTPAddr          = "127.0.0.1:8080"
	defaultLadybugPath       = "data/mivialabs.lbug"
	defaultSQLitePath        = "data/mivialabs-config.sqlite"
	defaultMaxRequestBytes   = int64(1 << 20)
	defaultRequestTimeout    = 10 * time.Second
	defaultReadHeaderTimeout = 5 * time.Second
	defaultShutdownTimeout   = 10 * time.Second
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
	Projects          []Project
}

type Project struct {
	ID             string
	DisplayName    string
	Description    string
	RootPath       string
	Enabled        bool
	Classification string
	GraphNamespace string
	DigestMode     string
	UpdatePolicy   string
	Include        []string
	Exclude        []string
	FollowSymlinks bool
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
		Projects:          nil,
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
