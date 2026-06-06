package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad_DefaultConfigMissingUsesLoopbackDefaults(t *testing.T) {
	chdir(t, t.TempDir())
	clearEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if cfg.ConfigPath != defaultConfigPath {
		t.Fatalf("expected default config path %q, got %q", defaultConfigPath, cfg.ConfigPath)
	}
	if cfg.HTTPAddr != defaultHTTPAddr {
		t.Fatalf("expected default HTTP addr %q, got %q", defaultHTTPAddr, cfg.HTTPAddr)
	}
	if cfg.UpstreamURL != defaultUpstreamURL {
		t.Fatalf("expected default upstream %q, got %q", defaultUpstreamURL, cfg.UpstreamURL)
	}
}

func TestLoad_ExplicitConfigMissingReturnsSafeError(t *testing.T) {
	clearEnv(t)
	t.Setenv("MIVIA_DASHBOARD_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.toml"))

	_, err := Load()
	if err == nil {
		t.Fatal("expected missing explicit config to fail")
	}
	if !strings.Contains(err.Error(), "MIVIA_DASHBOARD_CONFIG_PATH points to a missing config file") {
		t.Fatalf("expected safe missing config error, got %v", err)
	}
}

func TestLoad_FileValuesAndEnvOverrides(t *testing.T) {
	clearEnv(t)
	configPath := filepath.Join(t.TempDir(), "mivia-dashboard.local.toml")
	writeFile(t, configPath, `
version = 1

[server]
http_addr = "127.0.0.1:8181"
upstream_url = "http://localhost:8080"
static_dir = "web/dashboard"
max_request_bytes = 2048
request_timeout = "11s"
read_header_timeout = "6s"
shutdown_timeout = "12s"
`)
	t.Setenv("MIVIA_DASHBOARD_CONFIG_PATH", configPath)
	t.Setenv("MIVIA_DASHBOARD_HTTP_ADDR", "localhost:8282")
	t.Setenv("MIVIA_DASHBOARD_UPSTREAM_URL", "http://127.0.0.1:18080")
	t.Setenv("MIVIA_DASHBOARD_REQUEST_TIMEOUT", "13s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load merged config: %v", err)
	}
	if cfg.HTTPAddr != "localhost:8282" {
		t.Fatalf("expected env HTTP addr, got %q", cfg.HTTPAddr)
	}
	if cfg.UpstreamURL != "http://127.0.0.1:18080" {
		t.Fatalf("expected env upstream URL, got %q", cfg.UpstreamURL)
	}
	if cfg.StaticDir != "web/dashboard" {
		t.Fatalf("expected static dir from file, got %q", cfg.StaticDir)
	}
	if cfg.MaxRequestBytes != 2048 {
		t.Fatalf("expected max request bytes from file, got %d", cfg.MaxRequestBytes)
	}
	if cfg.RequestTimeout != 13*time.Second || cfg.ReadHeaderTimeout != 6*time.Second || cfg.ShutdownTimeout != 12*time.Second {
		t.Fatalf("unexpected timeouts: %+v", cfg)
	}
}

func TestValidateRejectsNonLoopbackBindWithoutEchoingAddress(t *testing.T) {
	cfg := defaultConfig("test.toml")
	cfg.HTTPAddr = "0.0.0.0:8081"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected non-loopback bind to fail")
	}
	if !errors.Is(err, ErrNonLoopbackBind) || strings.Contains(err.Error(), "0.0.0.0") {
		t.Fatalf("expected categorized non-loopback bind without raw addr, got %v", err)
	}
}

func TestValidateRejectsNonLoopbackUpstreamWithoutEchoingURL(t *testing.T) {
	cfg := defaultConfig("test.toml")
	cfg.UpstreamURL = "http://192.0.2.10:8080"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected non-loopback upstream to fail")
	}
	if !errors.Is(err, ErrNonLoopbackUpstream) || strings.Contains(err.Error(), "192.0.2.10") {
		t.Fatalf("expected categorized non-loopback upstream without raw URL, got %v", err)
	}
}

func TestUpstreamBaseURLRejectsUnsafeParts(t *testing.T) {
	for _, raw := range []string{
		"ftp://127.0.0.1:8080",
		"http://user:pass@127.0.0.1:8080",
		"http://127.0.0.1:8080?token=value",
		"http://127.0.0.1:8080#fragment",
	} {
		cfg := defaultConfig("test.toml")
		cfg.UpstreamURL = raw
		if _, err := cfg.UpstreamBaseURL(); err == nil {
			t.Fatalf("expected unsafe upstream URL %q to fail", raw)
		}
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, env := range os.Environ() {
		key, _, ok := strings.Cut(env, "=")
		if ok && strings.HasPrefix(key, "MIVIA_DASHBOARD_") {
			t.Setenv(key, "")
		}
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
}

func writeFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
