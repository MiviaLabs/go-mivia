package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Options struct {
	WALEnabled               bool
	BusyTimeout              time.Duration
	Synchronous              string
	CheckpointAfterIngestion bool
}

type DB struct {
	db *sql.DB
}

func Open(path string) (*DB, error) {
	return OpenWithOptions(path, DefaultOptions(path))
}

func DefaultOptions(path string) Options {
	return Options{
		WALEnabled:               path != ":memory:",
		BusyTimeout:              5 * time.Second,
		Synchronous:              "NORMAL",
		CheckpointAfterIngestion: true,
	}
}

func OpenWithOptions(path string, options Options) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path must not be empty")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create sqlite directory: %w", err)
		}
	}
	dsn := openDSN(path, options)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(2)
		db.SetMaxIdleConns(2)
	}
	if err := applyPragmas(context.Background(), db, path, options); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &DB{db: db}, nil
}

func (db *DB) Ping(ctx context.Context) error {
	return db.db.PingContext(ctx)
}

func (db *DB) SQLDB() *sql.DB {
	return db.db
}

func (db *DB) Close() error {
	return db.db.Close()
}

func (db *DB) Checkpoint(ctx context.Context) error {
	_, err := db.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	if err != nil {
		return fmt.Errorf("checkpoint sqlite wal: %w", err)
	}
	return nil
}

func applyPragmas(ctx context.Context, db *sql.DB, path string, options Options) error {
	if options.BusyTimeout > 0 {
		ms := options.BusyTimeout.Milliseconds()
		if ms <= 0 {
			ms = 1
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", ms)); err != nil {
			return fmt.Errorf("configure sqlite busy_timeout: %w", err)
		}
	}
	synchronous := strings.ToUpper(strings.TrimSpace(options.Synchronous))
	if synchronous == "" {
		synchronous = "NORMAL"
	}
	switch synchronous {
	case "OFF", "NORMAL", "FULL", "EXTRA":
		if _, err := db.ExecContext(ctx, "PRAGMA synchronous = "+synchronous); err != nil {
			return fmt.Errorf("configure sqlite synchronous: %w", err)
		}
	default:
		return fmt.Errorf("invalid sqlite synchronous mode %q", options.Synchronous)
	}
	if options.WALEnabled && path != ":memory:" {
		var mode string
		if err := db.QueryRowContext(ctx, `PRAGMA journal_mode=WAL`).Scan(&mode); err != nil {
			return fmt.Errorf("enable sqlite wal: %w", err)
		}
		if !strings.EqualFold(mode, "wal") {
			return fmt.Errorf("enable sqlite wal: journal mode is %q", mode)
		}
	}
	return nil
}

func openDSN(path string, options Options) string {
	if path == ":memory:" {
		return path
	}
	values := url.Values{}
	if options.BusyTimeout > 0 {
		ms := options.BusyTimeout.Milliseconds()
		if ms <= 0 {
			ms = 1
		}
		values.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", ms))
	}
	synchronous := strings.ToUpper(strings.TrimSpace(options.Synchronous))
	if synchronous == "" {
		synchronous = "NORMAL"
	}
	values.Add("_pragma", "synchronous("+synchronous+")")
	if options.WALEnabled {
		values.Add("_pragma", "journal_mode(WAL)")
	}
	return "file:" + path + "?" + values.Encode()
}
