package agentactivity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProjectStreamHandlerReplaysProjectEventsAsSSE(t *testing.T) {
	recorder := NewRecorder(10)
	recorder.Record(Event{ProjectID: "alpha", Method: "tools/call", Status: "ok", RawArgs: json.RawMessage(`{"id":"alpha"}`)})
	recorder.Record(Event{ProjectID: "beta", Method: "tools/call", Status: "ok", RawArgs: json.RawMessage(`{"id":"beta"}`)})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/alpha/agent-activity/stream?recent=50", nil).WithContext(ctx)
	req.SetPathValue("id", "alpha")
	req.Header.Set("Accept", "text/event-stream")
	res := httptest.NewRecorder()

	ProjectStreamHandler(recorder).ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("expected SSE content type, got %q", got)
	}
	body := res.Body.String()
	if !strings.Contains(body, "event: mcp_activity") || !strings.Contains(body, `"project_id":"alpha"`) {
		t.Fatalf("expected alpha SSE event, got %s", body)
	}
	if strings.Contains(body, `"project_id":"beta"`) {
		t.Fatalf("unexpected beta event in alpha stream: %s", body)
	}
}

func TestProjectStreamHandlerReplaysEventsAfterLastEventID(t *testing.T) {
	recorder := NewRecorder(10)
	first := recorder.Record(Event{ProjectID: "alpha", Method: "tools/list", Status: "ok"})
	recorder.Record(Event{ProjectID: "alpha", Method: "tools/call", ToolName: "projects.get", Status: "ok"})
	recorder.Record(Event{ProjectID: "beta", Method: "tools/call", Status: "ok"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/alpha/agent-activity/stream?recent=50", nil).WithContext(ctx)
	req.SetPathValue("id", "alpha")
	req.Header.Set("Last-Event-ID", strconv.FormatInt(first.ID, 10))
	res := httptest.NewRecorder()

	ProjectStreamHandler(recorder).ServeHTTP(res, req)

	body := res.Body.String()
	if strings.Contains(body, "tools/list") {
		t.Fatalf("unexpected already-seen event in cursor replay: %s", body)
	}
	if !strings.Contains(body, "projects.get") || strings.Contains(body, `"project_id":"beta"`) {
		t.Fatalf("expected only alpha events after cursor, got %s", body)
	}
}

func TestProjectStreamHandlerAcceptsAfterIDQueryCursor(t *testing.T) {
	recorder := NewRecorder(10)
	first := recorder.Record(Event{ProjectID: "alpha", Method: "tools/list", Status: "ok"})
	recorder.Record(Event{ProjectID: "alpha", Method: "tools/call", ToolName: "projects.get", Status: "ok"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/alpha/agent-activity/stream?after_id="+strconv.FormatInt(first.ID, 10), nil).WithContext(ctx)
	req.SetPathValue("id", "alpha")
	res := httptest.NewRecorder()

	ProjectStreamHandler(recorder).ServeHTTP(res, req)

	body := res.Body.String()
	if strings.Contains(body, "tools/list") || !strings.Contains(body, "projects.get") {
		t.Fatalf("expected query cursor replay after first event, got %s", body)
	}
}

func TestProjectStreamHandlerRejectsInvalidCursor(t *testing.T) {
	recorder := NewRecorder(10)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/alpha/agent-activity/stream?after_id=bad", nil)
	req.SetPathValue("id", "alpha")
	res := httptest.NewRecorder()

	ProjectStreamHandler(recorder).ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d: %s", res.Code, res.Body.String())
	}
}

func TestProjectStreamHandlerDrainsMissedPersistedEventsAfterLiveWakeup(t *testing.T) {
	store, _ := newTestSQLiteStore(t, SQLiteStoreOptions{})
	recorder := NewRecorderWithStore(10, store)
	first := recorder.Record(Event{ProjectID: "alpha", Method: "tools/list", Status: "ok"})

	res := newBlockingResponseRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/alpha/agent-activity/stream?after_id="+strconv.FormatInt(first.ID, 10), nil).WithContext(res.ctx)
	req.SetPathValue("id", "alpha")
	done := make(chan struct{})
	go func() {
		ProjectStreamHandler(recorder).ServeHTTP(res, req)
		close(done)
	}()
	res.waitForFlush(t)

	recorder.Record(Event{ProjectID: "alpha", Method: "tools/call", ToolName: "projects.get", Status: "ok"})

	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(res.bodyString(), "projects.get") {
			res.cancel()
			<-done
			return
		}
		select {
		case <-deadline:
			res.cancel()
			<-done
			t.Fatalf("expected cursor drain to stream persisted event, got %s", res.bodyString())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

type blockingResponseRecorder struct {
	mu         sync.Mutex
	header     http.Header
	chunks     strings.Builder
	statusCode int
	flushCh    chan struct{}
	ctx        context.Context
	cancelFunc context.CancelFunc
}

func newBlockingResponseRecorder() *blockingResponseRecorder {
	ctx, cancel := context.WithCancel(context.Background())
	return &blockingResponseRecorder{
		header:     make(http.Header),
		statusCode: http.StatusOK,
		flushCh:    make(chan struct{}, 16),
		ctx:        ctx,
		cancelFunc: cancel,
	}
}

func (recorder *blockingResponseRecorder) Header() http.Header {
	return recorder.header
}

func (recorder *blockingResponseRecorder) Write(bytes []byte) (int, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.chunks.Write(bytes)
}

func (recorder *blockingResponseRecorder) WriteHeader(statusCode int) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.statusCode = statusCode
}

func (recorder *blockingResponseRecorder) Flush() {
	select {
	case recorder.flushCh <- struct{}{}:
	default:
	}
}

func (recorder *blockingResponseRecorder) waitForFlush(t *testing.T) {
	t.Helper()
	select {
	case <-recorder.flushCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream flush")
	}
}

func (recorder *blockingResponseRecorder) bodyString() string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.chunks.String()
}

func (recorder *blockingResponseRecorder) cancel() {
	recorder.cancelFunc()
}
