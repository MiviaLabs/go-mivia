package diagnostics

import (
	"net"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
)

type IngestionSnapshotter interface {
	IngestionDiagnostics() projectingestion.DiagnosticsSnapshot
}

type RuntimeOptions struct {
	Enabled bool
}

type Service struct {
	ingestion IngestionSnapshotter
	runtime   RuntimeOptions
}

type Snapshot struct {
	Ingestion projectingestion.DiagnosticsSnapshot `json:"ingestion"`
	Runtime   RuntimeSnapshot                      `json:"runtime,omitempty"`
}

type RuntimeSnapshot struct {
	Goroutines      int    `json:"goroutines"`
	HeapAllocBytes  uint64 `json:"heap_alloc_bytes"`
	HeapInuseBytes  uint64 `json:"heap_inuse_bytes"`
	StackInuseBytes uint64 `json:"stack_inuse_bytes"`
}

func NewService(ingestion IngestionSnapshotter, runtimeOptions RuntimeOptions) *Service {
	return &Service{ingestion: ingestion, runtime: runtimeOptions}
}

func (svc *Service) Snapshot() Snapshot {
	var snapshot Snapshot
	if svc != nil && svc.ingestion != nil {
		snapshot.Ingestion = svc.ingestion.IngestionDiagnostics()
	}
	sort.Slice(snapshot.Ingestion.Watchers, func(i, j int) bool {
		return snapshot.Ingestion.Watchers[i].ProjectID < snapshot.Ingestion.Watchers[j].ProjectID
	})
	if svc != nil && svc.runtime.Enabled {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		snapshot.Runtime = RuntimeSnapshot{
			Goroutines:      runtime.NumGoroutine(),
			HeapAllocBytes:  mem.HeapAlloc,
			HeapInuseBytes:  mem.HeapInuse,
			StackInuseBytes: mem.StackInuse,
		}
	}
	return snapshot
}

func RegisterRoutes(mux *http.ServeMux, svc *Service) {
	if mux == nil || svc == nil {
		return
	}
	mux.Handle("GET /api/v1/diagnostics/ingestion", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpserver.WriteJSON(w, http.StatusOK, svc.Snapshot())
	}))
}

func Enabled(debugEnabled bool, httpAddr string) bool {
	return debugEnabled && IsLoopbackBind(httpAddr)
}

func IsLoopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		host = strings.TrimSpace(addr)
	}
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func DurationMillis(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return duration.Milliseconds()
}
