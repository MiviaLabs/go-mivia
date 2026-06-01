package projectingestion

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

func TestASTSearchCatalogQueriesCompile(t *testing.T) {
	for _, entry := range astSearchCatalog {
		language, err := astSearchLanguage(entry.Language)
		if err != nil {
			t.Fatalf("language %s: %v", entry.Language, err)
		}
		query, queryErr := tree_sitter.NewQuery(language, entry.Query)
		if queryErr != nil {
			t.Fatalf("query %s/%s: %v", entry.Language, entry.ID, queryErr)
		}
		query.Close()
	}
}

func TestASTSearchNamedQueriesAcrossSupportedLanguages(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "main.go"), "package main\n\nfunc Helper() {}\nfunc Run() { Helper() }\n")
	writeFile(t, filepath.Join(root, "py", "worker.py"), "def helper():\n    return 1\n\ndef run():\n    return helper()\n")
	writeFile(t, filepath.Join(root, "web", "app.js"), "export function helper() { return 1; }\nexport function run() { return helper(); }\n")
	writeFile(t, filepath.Join(root, "web", "app.ts"), "export function helper(): number { return 1; }\nexport function run(): number { return helper(); }\n")
	writeFile(t, filepath.Join(root, "web", "component.tsx"), "export function Widget() { return <div />; }\n")
	writeFile(t, filepath.Join(root, "src", "Worker.cs"), "namespace Demo;\nclass Worker {\n  int Helper() { return 1; }\n  int Run() { return Helper(); }\n}\n")
	writeFile(t, filepath.Join(root, "lib", "home.dart"), "import 'package:flutter/widgets.dart';\nclass Home extends StatelessWidget {\n  Widget build(BuildContext context) => Text('home');\n}\nvoid main() { runApp(Home()); }\n")

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
		{name: "dart function", language: "dart", query: "function_declarations", capture: "name", want: "build"},
		{name: "flutter widget", language: "dart", query: "flutter_widgets", capture: "name", want: "Home"},
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

func TestASTQueryCatalogAndCoverage(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "safe", "one.ts"), "export function visible() { return 1; }\n")
	writeFile(t, filepath.Join(root, "safe", "large.ts"), "export function hiddenBySize() { return `"+strings.Repeat("x", 5000)+"`; }\n")

	svc, _, _ := newTestService(t, root)
	if run, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil || run.Status != RunStatusCompleted {
		t.Fatalf("ingest project: %#v %v", run, err)
	}

	catalog, err := svc.ListASTQueries(ctx, "example-service")
	if err != nil {
		t.Fatalf("list ast queries: %v", err)
	}
	if !astCatalogContains(catalog.Queries, "typescript", "function_declarations", "name") {
		t.Fatalf("missing expected typescript query metadata: %#v", catalog.Queries)
	}
	if !astCatalogContains(catalog.Queries, "dart", "flutter_widgets", "name") {
		t.Fatalf("missing expected dart query metadata: %#v", catalog.Queries)
	}
	coverage := astCoverageForLanguage(t, catalog.Coverage, "typescript")
	if coverage.EligibleFiles != 2 || coverage.SkippedFileTooLarge != 1 || coverage.CoverageScope != string(SkipReasonFileTooLarge) || coverage.CoverageStatus != "partial" || coverage.CoveragePartialCause != string(SkipReasonFileTooLarge) {
		t.Fatalf("unexpected AST catalog coverage: %#v", coverage)
	}
	body := marshalASTResults(t, catalog)
	if strings.Contains(body, "(function_declaration") || strings.Contains(body, "hiddenBySize") || strings.Contains(body, root) || strings.Contains(body, "content_sha256") {
		t.Fatalf("AST catalog leaked raw query or skipped/internal data: %s", body)
	}

	results, err := svc.SearchAST(ctx, "example-service", ASTSearchOptions{
		Language: "typescript",
		Query:    "function_declarations",
		Captures: []string{"name"},
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("search ast: %v", err)
	}
	if results.Coverage == nil || results.Coverage.SkippedFileTooLarge != 1 || results.Coverage.CoverageStatus != "partial" {
		t.Fatalf("expected AST search coverage metadata, got %#v", results.Coverage)
	}
	body = marshalASTResults(t, results)
	if strings.Contains(body, "hiddenBySize") || strings.Contains(body, root) || strings.Contains(body, "content_sha256") {
		t.Fatalf("AST search leaked skipped/internal data: %s", body)
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

func astCatalogContains(queries []ASTQueryMetadata, language string, id string, capture string) bool {
	for _, query := range queries {
		if query.Language != language || query.ID != id {
			continue
		}
		for _, candidate := range query.Captures {
			if candidate == capture {
				return true
			}
		}
	}
	return false
}

func astCoverageForLanguage(t *testing.T, coverage []ASTCoverageMetadata, language string) ASTCoverageMetadata {
	t.Helper()
	for _, item := range coverage {
		if item.Language == language {
			return item
		}
	}
	t.Fatalf("missing coverage for %s: %#v", language, coverage)
	return ASTCoverageMetadata{}
}

func marshalASTResults(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal ast results: %v", err)
	}
	return string(body)
}
