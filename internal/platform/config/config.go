package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

const (
	defaultHTTPAddr          = "127.0.0.1:8080"
	defaultLadybugPath       = "data/mivialabs.lbug"
	defaultSQLitePath        = "data/mivialabs-config.sqlite"
	defaultMaxRequestBytes   = int64(1 << 20)
	defaultRequestTimeout    = 10 * time.Second
	defaultReadHeaderTimeout = 5 * time.Second
	defaultShutdownTimeout   = 10 * time.Second
)

type Config struct {
	HTTPAddr          string
	LadybugPath       string
	SQLitePath        string
	MaxRequestBytes   int64
	RequestTimeout    time.Duration
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:          getenv("MIVIA_HTTP_ADDR", defaultHTTPAddr),
		LadybugPath:       getenv("MIVIA_LADYBUG_PATH", defaultLadybugPath),
		SQLitePath:        getenv("MIVIA_SQLITE_PATH", defaultSQLitePath),
		MaxRequestBytes:   defaultMaxRequestBytes,
		RequestTimeout:    defaultRequestTimeout,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
	}

	var err error
	if cfg.MaxRequestBytes, err = getenvInt64("MIVIA_MAX_REQUEST_BYTES", cfg.MaxRequestBytes); err != nil {
		return Config{}, err
	}
	if cfg.RequestTimeout, err = getenvDuration("MIVIA_REQUEST_TIMEOUT", cfg.RequestTimeout); err != nil {
		return Config{}, err
	}
	if cfg.ReadHeaderTimeout, err = getenvDuration("MIVIA_READ_HEADER_TIMEOUT", cfg.ReadHeaderTimeout); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = getenvDuration("MIVIA_SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
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
