package health

import (
	"context"
	"testing"
	"time"
)

func TestCheckerReadyBoundsSlowChecks(t *testing.T) {
	checker := NewChecker(Check{
		Name:    "sqlite",
		Timeout: 10 * time.Millisecond,
		Fn: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})

	start := time.Now()
	status, ready := checker.Ready(context.Background())
	if ready {
		t.Fatalf("expected readiness failure")
	}
	if status["sqlite"] != "unavailable" {
		t.Fatalf("expected sqlite unavailable, got %#v", status)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("readiness check was not bounded: %s", elapsed)
	}
}
