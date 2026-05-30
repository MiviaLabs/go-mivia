package health

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
)

type Check struct {
	Name     string
	Fn       func(context.Context) error
	Optional bool
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
		err := check.Fn(ctx)
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

func LivenessHandler(w http.ResponseWriter, r *http.Request) {
	httpserver.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func ReadinessHandler(checker Checker, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, ready := checker.Ready(r.Context())
		code := http.StatusOK
		if !ready {
			code = http.StatusServiceUnavailable
			logger.Warn("readiness check failed", slog.String("error_category", "dependency"))
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
