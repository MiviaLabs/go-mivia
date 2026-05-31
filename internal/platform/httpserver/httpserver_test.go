package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTimeoutWritesJSONContentType(t *testing.T) {
	handler := Timeout(time.Millisecond)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(20 * time.Millisecond)
	}))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}")))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected service unavailable, got %d", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("expected JSON content type, got %q", contentType)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"code":"timeout"`) {
		t.Fatalf("expected timeout JSON body, got %q", body)
	}
}
