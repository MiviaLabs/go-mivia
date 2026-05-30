package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/research"
	"github.com/MiviaLabs/go-mivia/internal/research/httpapi"
	"github.com/MiviaLabs/go-mivia/internal/research/provider"
	researchstore "github.com/MiviaLabs/go-mivia/internal/research/store"
)

func TestSourceRoutes_RedactRawContentFromResponses(t *testing.T) {
	mux := newMux(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/research-runs/research_run_test/sources", bytes.NewBufferString(`{"artifact_ref":"https://example.test/doc?token=abc123","source_type":"web_fixture","summary":"Contact user@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, forbidden := range []string{"abc123", "user@example.com"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, body)
		}
	}
}

func TestSourceRoutes_RejectRawContentField(t *testing.T) {
	mux := newMux(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/research-runs/research_run_test/sources", bytes.NewBufferString(`{"artifact_ref":"fixture://source","source_type":"web_fixture","summary":"ok","raw_content":"not allowed"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected raw_content field rejection, got %d", res.Code)
	}
}

func TestSourceRoutes_GetSource(t *testing.T) {
	mux := newMux(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/research-runs/research_run_test/sources", bytes.NewBufferString(`{"artifact_ref":"fixture://source","source_type":"web_fixture","summary":"ok"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	var source provider.SourceMetadata
	if err := json.Unmarshal(res.Body.Bytes(), &source); err != nil {
		t.Fatalf("decode source: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/research-runs/research_run_test/sources/"+source.ID, nil)
	getRes := httptest.NewRecorder()
	mux.ServeHTTP(getRes, getReq)

	if getRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", getRes.Code, getRes.Body.String())
	}
}

func TestSourceRoutes_GetSourceWrongResearchRun_ReturnsNotFound(t *testing.T) {
	mux := newMux(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/research-runs/research_run_test/sources", bytes.NewBufferString(`{"artifact_ref":"fixture://source","source_type":"web_fixture","summary":"ok"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	var source provider.SourceMetadata
	if err := json.Unmarshal(res.Body.Bytes(), &source); err != nil {
		t.Fatalf("decode source: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/research-runs/other_run/sources/"+source.ID, nil)
	getRes := httptest.NewRecorder()
	mux.ServeHTTP(getRes, getReq)

	if getRes.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for mismatched research run, got %d", getRes.Code)
	}
}

func newMux(t *testing.T) *http.ServeMux {
	t.Helper()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	svc := research.NewService(researchstore.NewLadybugMetadataStore(graph))
	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, svc)
	return mux
}
