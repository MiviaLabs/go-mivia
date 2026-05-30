package sqlite

import (
	"context"
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
