package sqlite

import (
	"context"
	"testing"
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
