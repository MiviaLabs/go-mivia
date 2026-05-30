package mcpapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/research"
	"github.com/MiviaLabs/go-mivia/internal/research/mcpapi"
	researchstore "github.com/MiviaLabs/go-mivia/internal/research/store"
)

func TestCallTool_CreateSource_RedactsResponse(t *testing.T) {
	svc := newService(t)
	result, err := mcpapi.CallTool(context.Background(), svc, "research_sources.create", json.RawMessage(`{"research_run_id":"research_run_test","artifact_ref":"https://example.test/doc?token=abc123","source_type":"web_fixture","summary":"Contact user@example.com"}`))
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	body := string(encoded)
	for _, forbidden := range []string{"abc123", "user@example.com"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("MCP result leaked %q: %s", forbidden, body)
		}
	}
}

func newService(t *testing.T) *research.Service {
	t.Helper()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	return research.NewService(researchstore.NewLadybugMetadataStore(graph))
}
