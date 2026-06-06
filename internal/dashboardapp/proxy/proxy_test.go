package proxy

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAllowedReadOnlyPathSeparatesMutatingRoutes(t *testing.T) {
	allowed := []string{
		"/api/v1/projects",
		"/api/v1/projects/example",
		"/api/v1/projects/example/dashboard-summary",
		"/api/v1/projects/example/agent-activity/stream",
		"/api/v1/projects/example/work-plans/plan-1/resume",
		"/api/v1/projects/example/work-tasks/open",
		"/api/v1/projects/example/automation-runs/run-1",
		"/api/v1/projects/example/workflows/workflow-1/agent-definitions/worker",
		"/api/v1/projects/example/evidence-graph/claims/claim-1",
		"/api/v1/projects/example/confidence/claims/claim-1",
		"/api/v1/projects/example/knowledge/knowledge-1",
		"/api/v1/orgs/default/knowledge",
		"/api/v1/projects/example/integrations/jira/issues/MIVIA-1",
	}
	for _, path := range allowed {
		if !AllowedReadOnlyPath(path) {
			t.Fatalf("expected path to be allowed: %s", path)
		}
	}

	forbidden := []string{
		"/mcp",
		"/api/v1/tasks",
		"/api/v1/projects/example/digest-runs",
		"/api/v1/projects/example/ingestion-runs",
		"/api/v1/projects/example/search-index/rebuild",
		"/api/v1/projects/example/context-pack",
		"/api/v1/projects/example/workflows/workflow-1/compile",
		"/api/v1/projects/example/confidence/claims/claim-1/score",
		"/api/v1/projects/example/knowledge/knowledge-1/reuse-events",
		"/api/v1/projects/example/workspace/files/edit",
		"/api/v1/projects/example/symbols/symbol-1/source",
		"/api/v1/projects//dashboard-summary",
		"/api/v1/projects/example%2fother/dashboard-summary",
		"/api/v1/projects/example:other/dashboard-summary",
		"/api/v1/projects/example other/dashboard-summary",
	}
	for _, path := range forbidden {
		if AllowedReadOnlyPath(path) {
			t.Fatalf("expected path to be forbidden: %s", path)
		}
	}
}

func TestProxyForbidsMutatingMethodsAndRoutes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not receive forbidden request")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	handler := New(target)

	post := httptest.NewRecorder()
	handler.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/api/v1/projects/example/work-plans", strings.NewReader("{}")))
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected method not allowed, got %d", post.Code)
	}

	route := httptest.NewRecorder()
	handler.ServeHTTP(route, httptest.NewRequest(http.MethodGet, "/api/v1/projects/example/workflows/workflow-1/compile", nil))
	if route.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden route, got %d", route.Code)
	}
}

func TestProxyForwardsOnlySafeHeadersAndDoesNotAddCORS(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("authorization header was forwarded: %q", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Fatalf("cookie header was forwarded: %q", got)
		}
		if got := r.Header.Get("Last-Event-ID"); got != "42" {
			t.Fatalf("expected Last-Event-ID to be forwarded, got %q", got)
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/example/context-health", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Cookie", "session=value")
	req.Header.Set("Last-Event-ID", "42")

	rec := httptest.NewRecorder()
	New(target).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("proxy should not add or forward CORS headers, got %q", got)
	}
}

func TestProxyFlushesSSEWithoutWaitingForUpstreamEnd(t *testing.T) {
	firstSent := make(chan struct{})
	releaseSecond := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("upstream test server does not support flushing")
		}
		fmt.Fprint(w, "event: message\ndata: first\n\n")
		flusher.Flush()
		close(firstSent)
		<-releaseSecond
		fmt.Fprint(w, "event: message\ndata: second\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	proxyServer := httptest.NewServer(New(target))
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodGet, proxyServer.URL+"/api/v1/projects/example/agent-activity/stream", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	<-firstSent

	reader := bufio.NewReader(resp.Body)
	first := readUntilAsync(t, reader, "\n\n")
	if !strings.Contains(first, "data: first") {
		t.Fatalf("expected first SSE event before upstream completed, got %q", first)
	}
	close(releaseSecond)
	second := readUntilAsync(t, reader, "\n\n")
	if !strings.Contains(second, "data: second") {
		t.Fatalf("expected second SSE event, got %q", second)
	}
}

func readUntilAsync(t *testing.T, reader *bufio.Reader, delim string) string {
	t.Helper()
	type result struct {
		value string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		var builder strings.Builder
		for {
			value, err := reader.ReadString(delim[len(delim)-1])
			builder.WriteString(value)
			if err != nil || strings.Contains(builder.String(), delim) {
				ch <- result{value: builder.String(), err: err}
				return
			}
		}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("read SSE event: %v", res.err)
		}
		return res.value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for streamed SSE event")
		return ""
	}
}
