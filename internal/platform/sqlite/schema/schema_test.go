package schema_test

import (
	"context"
	"testing"

	sqliteplatform "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite/schema"
)

func TestBootstrap_Idempotent(t *testing.T) {
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if err := schema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	if err := schema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}

	var version int
	if err := db.SQLDB().QueryRowContext(context.Background(), `SELECT version FROM schema_versions WHERE component = ?`, schema.Component).Scan(&version); err != nil {
		t.Fatalf("query schema version: %v", err)
	}
	if version != schema.Version {
		t.Fatalf("expected version %d, got %d", schema.Version, version)
	}
}
