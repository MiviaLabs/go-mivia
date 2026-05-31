package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestOpen_InMemory_Pings(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if err := db.Ping(context.Background()); err != nil {
		t.Fatalf("ping sqlite: %v", err)
	}
}

func TestOpen_SerializesConcurrentWrites(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := db.SQLDB().ExecContext(ctx, `CREATE TABLE writes (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			<-start
			tx, err := db.SQLDB().BeginTx(ctx, nil)
			if err != nil {
				errs <- err
				return
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(ctx, `INSERT INTO writes (value) VALUES (?)`, value); err != nil {
				errs <- err
				return
			}
			errs <- tx.Commit()
		}(string(rune('a' + i)))
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent write failed: %v", err)
		}
	}
}

func TestOpen_FileBackedDefaultsToWAL(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "mivia.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	var journalMode string
	if err := db.SQLDB().QueryRowContext(context.Background(), `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("expected wal journal mode, got %q", journalMode)
	}
	var busyTimeout int
	if err := db.SQLDB().QueryRowContext(context.Background(), `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("expected 5000ms busy timeout, got %d", busyTimeout)
	}
	if maxOpen := db.SQLDB().Stats().MaxOpenConnections; maxOpen != 2 {
		t.Fatalf("expected file-backed sqlite to use two connections, max open conns=%d", maxOpen)
	}
	if err := db.Checkpoint(context.Background()); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
}

func TestOpen_InMemoryDisablesWAL(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	var journalMode string
	if err := db.SQLDB().QueryRowContext(context.Background(), `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if journalMode == "wal" {
		t.Fatalf("expected in-memory sqlite not to use wal")
	}
	if maxOpen := db.SQLDB().Stats().MaxOpenConnections; maxOpen != 1 {
		t.Fatalf("expected in-memory sqlite to use one connection, max open conns=%d", maxOpen)
	}
}

func TestOpenWithOptions_DisablesWALRollback(t *testing.T) {
	db, err := OpenWithOptions(filepath.Join(t.TempDir(), "mivia.sqlite"), Options{
		WALEnabled:  false,
		BusyTimeout: time.Second,
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	var journalMode string
	if err := db.SQLDB().QueryRowContext(context.Background(), `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if journalMode == "wal" {
		t.Fatalf("expected wal rollback option to disable wal")
	}
}
