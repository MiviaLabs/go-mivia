package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	defaultConfigPath        = "configs/mivia-dashboard.local.toml"
	defaultHTTPAddr          = "127.0.0.1:8081"
	defaultUpstreamURL       = "http://127.0.0.1:8080"
	defaultMaxRequestBytes   = int64(1 << 20)
	defaultRequestTimeout    = 10 * time.Second
	defaultReadHeaderTimeout = 5 * time.Second
	defaultShutdownTimeout   = 10 * time.Second
	configVersion            = 1
)

var (
	ErrNonLoopbackBind     = errors.New("non_loopback_bind_attempt")
	ErrNonLoopbackUpstream = errors.New("non_loopback_upstream_attempt")
)

type Config struct {
	ConfigPath        string
	HTTPAddr          string
	UpstreamURL       string
	StaticDir         string
	MaxRequestBytes   int64
	RequestTimeout    time.Duration
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration
}

type fileConfig struct {
	Version int               `toml:"version"`
	Server  *fileServerConfig `toml:"server"`
}

type fileServerConfig struct {
	HTTPAddr          *string `toml:"http_addr"`
	UpstreamURL       *string `toml:"upstream_url"`
	StaticDir         *string `toml:"static_dir"`
	MaxRequestBytes   *int64  `toml:"max_request_bytes"`
	RequestTimeout    *string `toml:"request_timeout"`
	ReadHeaderTimeout *string `toml:"read_header_timeout"`
	ShutdownTimeout   *string `toml:"shutdown_timeout"`
}

func Load() (Config, error) {
	configPath, explicit := configPathFromEnv()
	return load(configPath, explicit)
}

func LoadPath(configPath string) (Config, error) {
	if strings.TrimSpace(configPath) == "" {
		return Config{}, errors.New("config path must not be empty")
	}
	return load(configPath, true)
}

func load(configPath string, explicit bool) (Config, error) {
	cfg := defaultConfig(configPath)
	fileCfg, err := loadFileConfig(configPath)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist) && !explicit:
		case errors.Is(err, fs.ErrNotExist):
			return Config{}, errors.New("MIVIA_DASHBOARD_CONFIG_PATH points to a missing config file")
		case explicit:
			return Config{}, fmt.Errorf("MIVIA_DASHBOARD_CONFIG_PATH config is invalid: %w", err)
		default:
			return Config{}, fmt.Errorf("default dashboard config is invalid: %w", err)
		}
	} else if cfg, err = fileCfg.applyTo(cfg); err != nil {
		if explicit {
			return Config{}, fmt.Errorf("MIVIA_DASHBOARD_CONFIG_PATH config is invalid: %w", err)
		}
		return Config{}, fmt.Errorf("default dashboard config is invalid: %w", err)
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
		UpstreamURL:       defaultUpstreamURL,
		MaxRequestBytes:   defaultMaxRequestBytes,
		RequestTimeout:    defaultRequestTimeout,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
	}
}

func loadFileConfig(path string) (fileConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileConfig{}, err
	}
	defer file.Close()

	var cfg fileConfig
	if err := toml.NewDecoder(file).DisallowUnknownFields().Decode(&cfg); err != nil {
		return fileConfig{}, err
	}
	if cfg.Version != configVersion {
		return fileConfig{}, fmt.Errorf("version must be %d", configVersion)
	}
	return cfg, nil
}

func (cfg fileConfig) applyTo(base Config) (Config, error) {
	if cfg.Server == nil {
		return base, nil
	}
	if cfg.Server.HTTPAddr != nil {
		base.HTTPAddr = *cfg.Server.HTTPAddr
	}
	if cfg.Server.UpstreamURL != nil {
		base.UpstreamURL = *cfg.Server.UpstreamURL
	}
	if cfg.Server.StaticDir != nil {
		base.StaticDir = *cfg.Server.StaticDir
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
	return base, nil
}

func applyEnvOverrides(cfg *Config) error {
	var err error
	cfg.HTTPAddr = getenv("MIVIA_DASHBOARD_HTTP_ADDR", cfg.HTTPAddr)
	cfg.UpstreamURL = getenv("MIVIA_DASHBOARD_UPSTREAM_URL", cfg.UpstreamURL)
	cfg.StaticDir = getenv("MIVIA_DASHBOARD_STATIC_DIR", cfg.StaticDir)
	if cfg.MaxRequestBytes, err = getenvInt64("MIVIA_DASHBOARD_MAX_REQUEST_BYTES", cfg.MaxRequestBytes); err != nil {
		return err
	}
	if cfg.RequestTimeout, err = getenvDuration("MIVIA_DASHBOARD_REQUEST_TIMEOUT", cfg.RequestTimeout); err != nil {
		return err
	}
	if cfg.ReadHeaderTimeout, err = getenvDuration("MIVIA_DASHBOARD_READ_HEADER_TIMEOUT", cfg.ReadHeaderTimeout); err != nil {
		return err
	}
	if cfg.ShutdownTimeout, err = getenvDuration("MIVIA_DASHBOARD_SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return err
	}
	return nil
}

func (cfg Config) Validate() error {
	if strings.TrimSpace(cfg.HTTPAddr) == "" {
		return errors.New("MIVIA_DASHBOARD_HTTP_ADDR must not be empty")
	}
	if !isLocalAddress(cfg.HTTPAddr) {
		return fmt.Errorf("%w: MIVIA_DASHBOARD_HTTP_ADDR must bind to localhost until authn/authz is approved", ErrNonLoopbackBind)
	}
	upstream, err := cfg.UpstreamBaseURL()
	if err != nil {
		return err
	}
	if !isLoopbackHost(upstream.Hostname()) {
		return fmt.Errorf("%w: MIVIA_DASHBOARD_UPSTREAM_URL must target localhost until authn/authz is approved", ErrNonLoopbackUpstream)
	}
	if strings.ContainsAny(cfg.StaticDir, "\x00\r\n") {
		return errors.New("MIVIA_DASHBOARD_STATIC_DIR must not contain control characters")
	}
	if cfg.MaxRequestBytes <= 0 {
		return errors.New("MIVIA_DASHBOARD_MAX_REQUEST_BYTES must be positive")
	}
	if cfg.RequestTimeout <= 0 {
		return errors.New("MIVIA_DASHBOARD_REQUEST_TIMEOUT must be positive")
	}
	if cfg.ReadHeaderTimeout <= 0 {
		return errors.New("MIVIA_DASHBOARD_READ_HEADER_TIMEOUT must be positive")
	}
	if cfg.ShutdownTimeout <= 0 {
		return errors.New("MIVIA_DASHBOARD_SHUTDOWN_TIMEOUT must be positive")
	}
	return nil
}

func (cfg Config) UpstreamBaseURL() (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(cfg.UpstreamURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("MIVIA_DASHBOARD_UPSTREAM_URL must be an absolute HTTP URL")
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return nil, errors.New("MIVIA_DASHBOARD_UPSTREAM_URL must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("MIVIA_DASHBOARD_UPSTREAM_URL must not include user info, query, or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return parsed, nil
}

func applyDuration(name string, raw *string, fallback time.Duration) (time.Duration, error) {
	if raw == nil {
		return fallback, nil
	}
	value, err := time.ParseDuration(*raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration", name)
	}
	return value, nil
}

func configPathFromEnv() (string, bool) {
	value := os.Getenv("MIVIA_DASHBOARD_CONFIG_PATH")
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
	return isLoopbackHost(host)
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
