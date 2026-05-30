package config

import (
	"testing"
	"time"
)

func TestConfigValidate_NonLocalBind_ReturnsError(t *testing.T) {
	cfg := Config{
		HTTPAddr:          "0.0.0.0:8080",
		LadybugPath:       "data/test.lbug",
		SQLitePath:        "data/test.sqlite",
		MaxRequestBytes:   1024,
		RequestTimeout:    time.Second,
		ReadHeaderTimeout: time.Second,
		ShutdownTimeout:   time.Second,
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected non-local bind to be rejected")
	}
}

func TestConfigValidate_LocalBind_ReturnsNil(t *testing.T) {
	cfg := Config{
		HTTPAddr:          "127.0.0.1:8080",
		LadybugPath:       "data/test.lbug",
		SQLitePath:        "data/test.sqlite",
		MaxRequestBytes:   1024,
		RequestTimeout:    time.Second,
		ReadHeaderTimeout: time.Second,
		ShutdownTimeout:   time.Second,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected local bind to be valid: %v", err)
	}
}
