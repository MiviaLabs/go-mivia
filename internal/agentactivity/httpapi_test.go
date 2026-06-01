package agentactivity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
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
