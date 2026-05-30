package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type SQLiteConfigStore struct {
	db *sql.DB
}

func NewSQLiteConfigStore(db *sql.DB) *SQLiteConfigStore {
	return &SQLiteConfigStore{db: db}
}

func (store *SQLiteConfigStore) SetAppSetting(ctx context.Context, key string, value string, valueType string) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO app_settings (key, value, value_type, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, value_type = excluded.value_type, updated_at = excluded.updated_at`,
		key, value, valueType, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (store *SQLiteConfigStore) GetAppSetting(ctx context.Context, key string) (string, string, error) {
	var value string
	var valueType string
	err := store.db.QueryRowContext(ctx, `SELECT value, value_type FROM app_settings WHERE key = ?`, key).Scan(&value, &valueType)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return value, valueType, err
}

func (store *SQLiteConfigStore) SetRuntimeFlag(ctx context.Context, key string, enabled bool, description string) error {
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO runtime_flags (key, enabled, description, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET enabled = excluded.enabled, description = excluded.description, updated_at = excluded.updated_at`,
		key, enabledInt, description, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (store *SQLiteConfigStore) GetRuntimeFlag(ctx context.Context, key string) (bool, string, error) {
	var enabled int
	var description string
	err := store.db.QueryRowContext(ctx, `SELECT enabled, description FROM runtime_flags WHERE key = ?`, key).Scan(&enabled, &description)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", ErrNotFound
	}
	return enabled == 1, description, err
}
