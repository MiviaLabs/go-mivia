package projectingestion

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestASTSearchNamedQueriesAcrossSupportedLanguages(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "main.go"), "package main\n\nfunc Helper() {}\nfunc Run() { Helper() }\n")
	writeFile(t, filepath.Join(root, "py", "worker.py"), "def helper():\n    return 1\n\ndef run():\n    return helper()\n")
	writeFile(t, filepath.Join(root, "web", "app.js"), "export function helper() { return 1; }\nexport function run() { return helper(); }\n")
	writeFile(t, filepath.Join(root, "web", "app.ts"), "export function helper(): number { return 1; }\nexport function run(): number { return helper(); }\n")
	writeFile(t, filepath.Join(root, "web", "component.tsx"), "export function Widget() { return <div />; }\n")
	writeFile(t, filepath.Join(root, "src", "Worker.cs"), "namespace Demo;\nclass Worker {\n  int Helper() { return 1; }\n  int Run() { return Helper(); }\n}\n")

	svc, _, _ := newTestService(t, root)
	if run, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil || run.Status != RunStatusCompleted {
		t.Fatalf("ingest project: %#v %v", run, err)
	}

	for _, tc := range []struct {
		name     string
		language string
		query    string
		capture  string
		want     string
	}{
		{name: "go function", language: "go", query: "function_declarations", capture: "name", want: "Run"},
		{name: "python function", language: "python", query: "function_declarations", capture: "name", want: "run"},
		{name: "javascript call", language: "javascript", query: "call_expressions", capture: "callee", want: "helper"},
		{name: "typescript function", language: "typescript", query: "function_declarations", capture: "name", want: "run"},
		{name: "tsx function", language: "tsx", query: "function_declarations", capture: "name", want: "Widget"},
		{name: "csharp method", language: "csharp", query: "function_declarations", capture: "name", want: "Run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results, err := svc.SearchAST(ctx, "example-service", ASTSearchOptions{
				Language:        tc.language,
				Query:           tc.query,
				Captures:        []string{tc.capture},
				PageSize:        20,
				MaxSnippetBytes: 80,
			})
			if err != nil {
				t.Fatalf("search ast: %v", err)
			}
			if results.QueryLanguage != normalizeASTLanguage(tc.language) || results.QueryVersion == "" {
				t.Fatalf("missing query metadata: %#v", results)
			}
			if !astSearchResultsContain(results.Results, tc.capture, tc.want) {
				t.Fatalf("expected capture %s=%q, got %#v", tc.capture, tc.want, results.Results)
			}
			body := marshalASTResults(t, results)
			if strings.Contains(body, root) || strings.Contains(body, "content_sha256") {
				t.Fatalf("AST search leaked internal data: %s", body)
			}
		})
	}
}

func TestASTSearchPaginationFiltersAndPrivacy(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "safe", "one.ts"), "export function alpha() { return beta(); }\nfunction beta() { return 1; }\n")
	writeFile(t, filepath.Join(root, "other", "two.ts"), "export function gamma() { return 2; }\n")
	writeFile(t, filepath.Join(root, "safe", "secret.ts"), "const api_key = \"placeholder\";\nexport function shouldNotLeak() { return 1; }\n")

	svc, _, _ := newTestService(t, root)
	if run, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil || run.Status != RunStatusCompleted {
		t.Fatalf("ingest project: %#v %v", run, err)
	}

	results, err := svc.SearchAST(ctx, "example-service", ASTSearchOptions{
		Language:        "typescript",
		Query:           "function_declarations",
		Captures:        []string{"name"},
		PathPrefix:      "safe/",
		Extension:       ".ts",
		PageSize:        1,
		MaxSnippetBytes: 12,
	})
	if err != nil {
		t.Fatalf("search ast: %v", err)
	}
	if len(results.Results) != 1 || results.NextPageToken == "" || !results.ResultTruncated {
		t.Fatalf("expected paged/truncated result, got %#v", results)
	}
	body := marshalASTResults(t, results)
	if strings.Contains(body, "shouldNotLeak") || strings.Contains(body, "api_key") || strings.Contains(body, root) || strings.Contains(body, "content_sha256") {
		t.Fatalf("AST search leaked skipped or internal data: %s", body)
	}

	if _, err := svc.SearchAST(ctx, "example-service", ASTSearchOptions{
		Language: "typescript",
		Query:    "(function_declaration) @raw",
	}); err == nil {
		t.Fatal("expected raw query syntax to be rejected")
	}
}

func TestASTSearchParsesWholeFileAcrossChunkBoundaries(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	filler := "export const filler = `" + strings.Repeat("x", 1500) + "`;\n"
	writeFile(t, filepath.Join(root, "web", "large.ts"), filler+"export function lateMatch(): number { return 1; }\n")

	svc, _, _ := newTestServiceWithMaxChunkBytes(t, root, 128)
	if run, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil || run.Status != RunStatusCompleted {
		t.Fatalf("ingest project: %#v %v", run, err)
	}

	results, err := svc.SearchAST(ctx, "example-service", ASTSearchOptions{
		Language:        "typescript",
		Query:           "function_declarations",
		Captures:        []string{"name"},
		PageSize:        20,
		MaxSnippetBytes: 80,
	})
	if err != nil {
		t.Fatalf("search ast: %v", err)
	}
	if !astSearchResultsContain(results.Results, "name", "lateMatch") {
		t.Fatalf("expected AST result across chunk boundary, got %#v", results.Results)
	}
	for _, result := range results.Results {
		if result.CaptureText == "lateMatch" && result.Chunk.Index == 0 {
			t.Fatalf("expected late match to map to a later chunk, got %#v", result)
		}
	}
}

func astSearchResultsContain(results []ASTSearchResult, captureName string, captureText string) bool {
	for _, result := range results {
		if result.CaptureName == captureName && result.CaptureText == captureText {
			return true
		}
	}
	return false
}

func marshalASTResults(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal ast results: %v", err)
	}
	return string(body)
}
