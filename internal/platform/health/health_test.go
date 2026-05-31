package health

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestReadinessHandler_LogsDependencyNamesAndStatuses(t *testing.T) {
	checker := NewChecker(
		Check{
			Name: "sqlite",
			Fn: func(context.Context) error {
				return context.DeadlineExceeded
			},
		},
		Check{
			Name: "ladybug_native",
			Fn: func(context.Context) error {
				return DependencyUnavailable("native library unavailable")
			},
			Optional: true,
		},
	)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{}))

	recorder := httptest.NewRecorder()
	ReadinessHandler(checker, logger).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected unavailable status, got %d", recorder.Code)
	}
	got := logs.String()
	for _, want := range []string{
		"readiness check failed",
		"dependency_names=ladybug_native,sqlite",
		"ladybug_native:optional_unavailable",
		"sqlite:unavailable",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected log to contain %q, got %s", want, got)
		}
	}
}
