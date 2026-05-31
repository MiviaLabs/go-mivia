package health

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
)

type Check struct {
	Name     string
	Fn       func(context.Context) error
	Optional bool
	Timeout  time.Duration
}

type Checker struct {
	checks []Check
}

type dependencyUnavailable struct {
	reason string
}

func (err dependencyUnavailable) Error() string {
	return err.reason
}

func DependencyUnavailable(reason string) error {
	return dependencyUnavailable{reason: reason}
}

func NewChecker(checks ...Check) Checker {
	return Checker{checks: checks}
}

func (checker Checker) Ready(ctx context.Context) (map[string]string, bool) {
	status := make(map[string]string, len(checker.checks))
	ready := true
	for _, check := range checker.checks {
		checkCtx, cancel := checkContext(ctx, check.Timeout)
		err := check.Fn(checkCtx)
		cancel()
		if err == nil {
			status[check.Name] = "ok"
			continue
		}
		if check.Optional && errors.As(err, &dependencyUnavailable{}) {
			status[check.Name] = "optional_unavailable"
			continue
		}
		status[check.Name] = "unavailable"
		ready = false
	}
	return status, ready
}

func checkContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}

func LivenessHandler(w http.ResponseWriter, r *http.Request) {
	httpserver.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func ReadinessHandler(checker Checker, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, ready := checker.Ready(r.Context())
		code := http.StatusOK
		if !ready {
			code = http.StatusServiceUnavailable
			logger.Warn(
				"readiness check failed",
				slog.String("error_category", "dependency"),
				slog.String("dependency_names", dependencyNames(status)),
				slog.Any("dependency_status", status),
			)
		}
		httpserver.WriteJSON(w, code, map[string]any{
			"status":       statusText(ready),
			"dependencies": status,
		})
	})
}

func statusText(ready bool) string {
	if ready {
		return "ready"
	}
	return "not_ready"
}

func dependencyNames(status map[string]string) string {
	if len(status) == 0 {
		return ""
	}
	names := make([]string, 0, len(status))
	for name := range status {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
