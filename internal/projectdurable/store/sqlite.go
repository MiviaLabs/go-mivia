package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cschleiden/go-workflows/backend"
	"github.com/cschleiden/go-workflows/backend/sqlite"
)

// NewSQLiteBackend opens a local go-workflows SQLite backend for the durable
// pilot. The path is intentionally restricted to ignored repo-local data/.
func NewSQLiteBackend(sqlitePath string) (b backend.Backend, err error) {
	clean, err := ValidateSQLitePath(sqlitePath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(clean), 0o700); err != nil {
		return nil, fmt.Errorf("create durable workflow sqlite directory: %w", err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			b = nil
			err = errors.New("open durable workflow sqlite backend failed")
		}
	}()
	return sqlite.NewSqliteBackend(clean), nil
}

func ValidateSQLitePath(sqlitePath string) (string, error) {
	trimmed := strings.TrimSpace(sqlitePath)
	if trimmed == "" {
		return "", errors.New("durable workflow sqlite path must not be empty")
	}
	if filepath.IsAbs(trimmed) || strings.Contains(trimmed, "\\") || strings.Contains(trimmed, ":") || strings.ContainsAny(trimmed, "\x00\r\n") {
		return "", errors.New("durable workflow sqlite path must be a safe data/ relative SQLite path")
	}
	clean := filepath.ToSlash(filepath.Clean(trimmed))
	if clean == "." || clean != trimmed || clean == "data" || !strings.HasPrefix(clean, "data/") || strings.Contains(clean, "../") || strings.HasPrefix(clean, "../") {
		return "", errors.New("durable workflow sqlite path must be a safe data/ relative SQLite path")
	}
	if filepath.Ext(clean) != ".sqlite" {
		return "", errors.New("durable workflow sqlite path must end with .sqlite")
	}
	return clean, nil
}
