package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewWithOptionsDoesNotCreateFileByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mivia-server.log")
	logger, closer, err := NewWithOptions("test-service", Options{FilePath: path})
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })

	logger.Info("stdout only", slog.String("error_category", "test"))

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected log file not to be created, stat err=%v", err)
	}
}

func TestNewWithOptionsWritesFileWhenEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "mivia-server.log")
	logger, closer, err := NewWithOptions("test-service", Options{FileEnabled: true, FilePath: path})
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	logger.Info("file enabled", slog.String("error_category", "test"))
	if err := closer.Close(); err != nil {
		t.Fatalf("close logger: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	body := string(content)
	if !strings.Contains(body, `"service":"test-service"`) || !strings.Contains(body, `"msg":"file enabled"`) {
		t.Fatalf("unexpected log content: %s", body)
	}
}
