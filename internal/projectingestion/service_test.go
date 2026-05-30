package projectingestion

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
	sqliteplatform "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite/schema"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

func TestIngestProject_StoresEligibleContentGraphState(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "main.go"), "package main\n\nfunc Run() {}\n")
	writeFile(t, filepath.Join(root, "docs", "guide.md"), "# Guide\n\n## Setup\n")

	svc, graph, state := newTestService(t, root)
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	if run.Status != RunStatusCompleted {
		t.Fatalf("expected completed run, got %#v", run)
	}
	if run.FilesIngested != 2 || run.ChunksStored != 2 || run.SymbolsStored == 0 {
		t.Fatalf("unexpected run counts: %#v", run)
	}

	states, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusEligible})
	if err != nil {
		t.Fatalf("list states: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("expected two eligible states, got %#v", states)
	}
	for _, fileState := range states {
		if fileState.ContentSHA256 == "" {
			t.Fatalf("expected content hash for eligible file: %#v", fileState)
		}
		if strings.Contains(fileState.RelativePath, root) {
			t.Fatalf("relative path leaked root: %#v", fileState)
		}
	}

	goState := findState(t, states, "cmd/main.go")
	repoFileID := repoFileID("example_ns", goState.RelativePathHash)
	versionID := fileVersionID(repoFileID, goState.ContentSHA256)
	chunkNode, err := graph.GetNode(ctx, "ContentChunk", contentChunkID(versionID, 0))
	if err != nil {
		t.Fatalf("get content chunk: %v", err)
	}
	if !strings.Contains(chunkNode.Properties["text"], "func Run") {
		t.Fatalf("expected stored eligible chunk text, got %#v", chunkNode.Properties)
	}
	assertNoPropertyContains(t, chunkNode.Properties, root)

	symbolID := codeSymbolID(repoFileID, Symbol{
		Kind:      SymbolKindFunction,
		Name:      "Run",
		StartLine: 3,
	})
	symbolNode, err := graph.GetNode(ctx, "CodeSymbol", symbolID)
	if err != nil {
		t.Fatalf("get function symbol: %v", err)
	}
	if symbolNode.Properties["name"] != "Run" {
		t.Fatalf("expected Run symbol, got %#v", symbolNode.Properties)
	}
}

func TestIngestProject_ProcessesFilesConcurrentlyWithinFullScan(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "one.slow"), "one\n")
	writeFile(t, filepath.Join(root, "two.slow"), "two\n")

	svc, _, _ := newTestService(t, root)
	svc.SetFullScanWorkerCount(2)
	started := make(chan string, 2)
	release := make(chan struct{})
	svc.extractors = NewExtractorRegistry(staticExtractor{
		name:    "blocking-test",
		version: "1",
		supports: func(relative string) bool {
			return strings.HasSuffix(relative, ".slow")
		},
		parse: func(ctx context.Context, relative string, _ []byte) (ExtractorResult, error) {
			started <- relative
			select {
			case <-release:
				return ExtractorResult{}, nil
			case <-ctx.Done():
				return ExtractorResult{}, ctx.Err()
			}
		},
	})

	done := make(chan error, 1)
	go func() {
		run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
		if err == nil && run.FilesIngested != 2 {
			err = fmt.Errorf("expected two ingested files, got %#v", run)
		}
		done <- err
	}()

	first := receiveStartedFile(t, started)
	second := receiveStartedFile(t, started)
	if first == second {
		t.Fatalf("expected two distinct files to start, got %q and %q", first, second)
	}
	close(release)
	if err := receiveRunResult(t, done); err != nil {
		t.Fatalf("ingest project: %v", err)
	}
}

func TestIngestProject_PersistsRunningProgressDuringFullScan(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for i := 0; i < fullScanProgressFlushFiles+1; i++ {
		writeFile(t, filepath.Join(root, fmt.Sprintf("%02d.slow", i)), "content\n")
	}

	svc, _, _ := newTestService(t, root)
	svc.SetFullScanWorkerCount(1)
	blocked := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	started := 0
	svc.extractors = NewExtractorRegistry(staticExtractor{
		name:    "progress-test",
		version: "1",
		supports: func(relative string) bool {
			return strings.HasSuffix(relative, ".slow")
		},
		parse: func(ctx context.Context, _ string, _ []byte) (ExtractorResult, error) {
			mu.Lock()
			started++
			current := started
			mu.Unlock()
			if current == fullScanProgressFlushFiles+1 {
				close(blocked)
				select {
				case <-release:
				case <-ctx.Done():
					return ExtractorResult{}, ctx.Err()
				}
			}
			return ExtractorResult{}, nil
		},
	})

	done := make(chan error, 1)
	go func() {
		_, err := svc.IngestProject(ctx, "example-service", TriggerManual)
		done <- err
	}()
	waitForSignal(t, blocked)

	metadata := waitForRunProgress(t, ctx, svc, "example-service", "ingest_run_1", fullScanProgressFlushFiles)
	if metadata.Status != string(RunStatusRunning) || metadata.FilesSeen < fullScanProgressFlushFiles {
		t.Fatalf("expected persisted running progress, got %#v", metadata)
	}
	close(release)
	if err := receiveRunResult(t, done); err != nil {
		t.Fatalf("ingest project: %v", err)
	}
}

func TestSymbolSemanticQueriesReturnBoundedSourceReferencesAndCallEdges(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "main.go"), `package main

func helper() {}

func Run() {
	helper()
}
`)

	svc, _, _ := newTestService(t, root)
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	symbols, err := svc.ListSymbols(ctx, "example-service", SymbolFilter{}, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list symbols: %v", err)
	}
	runSymbol := findSymbolMetadata(t, symbols.Symbols, "Run")
	helperSymbol := findSymbolMetadata(t, symbols.Symbols, "helper")

	source, err := svc.GetSymbolSource(ctx, "example-service", runSymbol.ID, SymbolSourceOptions{MaxSourceBytes: 12})
	if err != nil {
		t.Fatalf("get symbol source: %v", err)
	}
	if !strings.Contains(source.Text, "func Run") || !source.TextTruncated {
		t.Fatalf("expected bounded Run source, got %#v", source)
	}
	if strings.Contains(source.Text, root) {
		t.Fatalf("source leaked root: %#v", source)
	}

	refs, err := svc.ListSymbolReferences(ctx, "example-service", helperSymbol.ID, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list symbol references: %v", err)
	}
	if len(refs.References) == 0 || refs.References[0].TargetSymbolID != helperSymbol.ID || refs.References[0].ResolutionStatus != "resolved" {
		t.Fatalf("expected resolved helper reference, got %#v", refs)
	}

	callees, err := svc.ListSymbolCallees(ctx, "example-service", runSymbol.ID, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list symbol callees: %v", err)
	}
	if len(callees.Edges) != 1 || callees.Edges[0].CalleeSymbolID != helperSymbol.ID {
		t.Fatalf("expected Run -> helper edge, got %#v", callees)
	}
	callers, err := svc.ListSymbolCallers(ctx, "example-service", helperSymbol.ID, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list symbol callers: %v", err)
	}
	if len(callers.Edges) != 1 || callers.Edges[0].CallerSymbolID != runSymbol.ID {
		t.Fatalf("expected helper caller Run, got %#v", callers)
	}
	graph, err := svc.GetSymbolCallGraph(ctx, "example-service", runSymbol.ID, CallGraphOptions{Direction: "callees", MaxDepth: 1, MaxNodes: 10})
	if err != nil {
		t.Fatalf("get symbol call graph: %v", err)
	}
	if len(graph.Nodes) != 2 || len(graph.Edges) != 1 {
		t.Fatalf("expected two-node one-edge graph, got %#v", graph)
	}
}

func TestSearchQueriesReturnBoundedEligibleIndexedResults(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "main.go"), `package main

func helperAlpha() {}
func helperBeta() {}

func Run() {
	helperAlpha()
	helperBeta()
}
`)
	writeFile(t, filepath.Join(root, "secrets", "token.go"), "package main\nvar access_token = placeholder\n")

	svc, _, _ := newTestService(t, root)
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("ingest project: %v", err)
	}

	text, err := svc.SearchText(ctx, "example-service", TextSearchOptions{
		Query:           "helper",
		Extension:       ".go",
		PageSize:        1,
		MaxSnippetBytes: 18,
	})
	if err != nil {
		t.Fatalf("search text: %v", err)
	}
	if len(text.Results) != 1 || text.NextPageToken == "" || len(text.Results[0].Snippet) > 18 || text.Results[0].Chunk.Text != "" || !text.Results[0].SnippetTruncated {
		t.Fatalf("expected capped paginated text result without chunk text, got %#v", text)
	}
	assertNoSearchLeak(t, root, text)

	files, err := svc.SearchFiles(ctx, "example-service", FileSearchOptions{PathContains: "main", PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("search files: %v", err)
	}
	if len(files.Files) != 1 || files.Files[0].RelativePath != "cmd/main.go" {
		t.Fatalf("expected only eligible main file, got %#v", files)
	}

	symbols, err := svc.SearchSymbols(ctx, "example-service", SymbolFilter{NameContains: "Beta"}, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("search symbols: %v", err)
	}
	if len(symbols.Symbols) != 1 || symbols.Symbols[0].Name != "helperBeta" {
		t.Fatalf("expected helperBeta symbol, got %#v", symbols)
	}

	refs, err := svc.SearchReferences(ctx, "example-service", ReferenceSearchOptions{TargetNameContains: "Alpha", PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("search references: %v", err)
	}
	if len(refs.References) != 1 || refs.References[0].TargetName != "helperAlpha" || refs.References[0].ResolutionStatus != "resolved" {
		t.Fatalf("expected resolved helperAlpha reference, got %#v", refs)
	}

	calls, err := svc.SearchCalls(ctx, "example-service", ReferenceSearchOptions{CallerNameContains: "Run", CalleeNameContains: "Beta", PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("search calls: %v", err)
	}
	if len(calls.Edges) != 1 || calls.Edges[0].CallerName != "Run" || calls.Edges[0].CalleeName != "helperBeta" {
		t.Fatalf("expected Run -> helperBeta call, got %#v", calls)
	}

	secretText, err := svc.SearchText(ctx, "example-service", TextSearchOptions{Query: "access_token", PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("search secret text: %v", err)
	}
	if len(secretText.Results) != 0 {
		t.Fatalf("expected skipped sensitive content to be excluded, got %#v", secretText)
	}
	assertNoSearchLeak(t, root, secretText, "access_token", "placeholder", "content_sha256", "secrets/token.go")
}

func TestSymbolSemanticEdgesDeletedWhenFileSkipped(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	file := filepath.Join(root, "cmd", "main.go")
	writeFile(t, file, `package main

func helper() {}

func Run() {
	helper()
}
`)

	svc, graph, state := newTestService(t, root)
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	initialRefs, err := graph.ListNodes(ctx, "CodeReference", map[string]string{"project_id": "example-service"})
	if err != nil {
		t.Fatalf("list references: %v", err)
	}
	initialCalls, err := graph.ListNodes(ctx, "CodeCall", map[string]string{"project_id": "example-service"})
	if err != nil {
		t.Fatalf("list calls: %v", err)
	}
	if len(initialRefs) == 0 || len(initialCalls) == 0 {
		t.Fatalf("expected semantic nodes before skip, refs=%d calls=%d", len(initialRefs), len(initialCalls))
	}

	writeFile(t, file, "package main\n\n// password = \"redacted\"\nfunc Run() {}\n")
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("reingest project: %v", err)
	}
	refs, err := graph.ListNodes(ctx, "CodeReference", map[string]string{"project_id": "example-service"})
	if err != nil {
		t.Fatalf("list references after skip: %v", err)
	}
	calls, err := graph.ListNodes(ctx, "CodeCall", map[string]string{"project_id": "example-service"})
	if err != nil {
		t.Fatalf("list calls after skip: %v", err)
	}
	if len(refs) != 0 || len(calls) != 0 {
		t.Fatalf("expected stale semantic nodes deleted after sensitive skip, refs=%#v calls=%#v", refs, calls)
	}
	states, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusSkipped})
	if err != nil {
		t.Fatalf("list skipped states: %v", err)
	}
	if len(states) != 1 || states[0].SkippedReason != SkipReasonSensitiveContent || states[0].ContentSHA256 != "" {
		t.Fatalf("expected sensitive skip without content hash, got %#v", states)
	}
}

func TestIngestProjectBatchesGraphWrites(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "a.go"), "package main\n\nfunc A() {}\n")
	writeFile(t, filepath.Join(root, "cmd", "b.go"), "package main\n\nfunc B() {}\n")
	writeFile(t, filepath.Join(root, "cmd", "c.go"), "package main\n\nfunc C() {}\n")

	svc, _, _ := newTestService(t, root)
	counter := &batchCountingGraph{MemoryGraph: ladybug.NewMemoryGraph()}
	if err := counter.Bootstrap(ctx, ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	svc.graph = NewGraphStore(counter)
	svc.SetFullScanBatchSize(2)

	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	if run.FilesIngested != 3 {
		t.Fatalf("expected three ingested files, got %#v", run)
	}
	if counter.batchCalls < 3 {
		t.Fatalf("expected per-file graph batches, got %d", counter.batchCalls)
	}
	if counter.maxRepoFileNodesPerBatch > 1 {
		t.Fatalf("full scan graph batch included too many repo files: %d", counter.maxRepoFileNodesPerBatch)
	}
}

func TestIngestProject_InvalidGoSyntaxDoesNotFailFullScan(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "good.go"), "package main\n\nfunc Run() {}\n")
	writeFile(t, filepath.Join(root, "cmd", "bad.go"), "package main\n\nfunc Broken(\n")

	svc, _, state := newTestService(t, root)
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	if run.Status != RunStatusCompleted || run.ErrorCategory != "file_errors" {
		t.Fatalf("expected completed run with file-local errors, got %#v", run)
	}
	if run.FilesIngested != 1 || run.FilesSkipped != 1 {
		t.Fatalf("unexpected run counts: %#v", run)
	}

	skipped, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusSkipped})
	if err != nil {
		t.Fatalf("list skipped states: %v", err)
	}
	if len(skipped) != 1 || skipped[0].SkippedReason != SkipReasonParseError {
		t.Fatalf("expected one parse-error state, got %#v", skipped)
	}
	if skipped[0].ContentSHA256 != "" {
		t.Fatalf("parse-error state must not store content hash: %#v", skipped[0])
	}
}

func TestRunMetadata_ExposesSafeReasonCounts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "bad.go"), "package main\n\nfunc Broken(\n")
	writeFile(t, filepath.Join(root, "cmd", "secret.go"), "package main\nvar access_token = placeholder\n")

	svc, _, _ := newTestService(t, root)
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	metadata, err := svc.RunMetadata(ctx, "example-service", run.ID)
	if err != nil {
		t.Fatalf("run metadata: %v", err)
	}
	if metadata.ReasonCounts[string(SkipReasonParseError)] != 1 || metadata.ReasonCounts[string(SkipReasonSensitiveContent)] != 1 {
		t.Fatalf("expected safe reason counts, got %#v", metadata.ReasonCounts)
	}
	for key := range metadata.ReasonCounts {
		if strings.Contains(key, root) || strings.Contains(key, "access_token") {
			t.Fatalf("reason count key leaked unsafe detail: %q", key)
		}
	}
}

func TestIngestProject_FileLocalReadAndChunkErrorsDoNotBlockUsefulFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "good.go"), "package main\n\nfunc Run() {}\n")
	unreadable := filepath.Join(root, "docs", "unreadable.txt")
	writeFile(t, unreadable, "safe text\n")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatalf("chmod unreadable file: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) })

	svc, _, state := newTestServiceWithMaxChunkBytes(t, root, 1)
	writeFile(t, filepath.Join(root, "docs", "wide.txt"), "é\n")
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	if run.Status != RunStatusCompleted || run.ErrorCategory != "file_errors" {
		t.Fatalf("expected completed run with file-local errors, got %#v", run)
	}
	if run.FilesIngested != 1 || run.FilesSkipped != 2 {
		t.Fatalf("unexpected run counts: %#v", run)
	}

	skipped, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusSkipped})
	if err != nil {
		t.Fatalf("list skipped states: %v", err)
	}
	reasons := map[SkipReason]bool{}
	for _, fileState := range skipped {
		reasons[fileState.SkippedReason] = true
		if fileState.ContentSHA256 != "" {
			t.Fatalf("file-local error state must not store content hash: %#v", fileState)
		}
	}
	for _, reason := range []SkipReason{SkipReasonReadError, SkipReasonChunkError} {
		if !reasons[reason] {
			t.Fatalf("expected skip reason %q in %#v", reason, skipped)
		}
	}
}

func TestIngestProject_WalkErrorsAreFileLocal(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "good.go"), "package main\n\nfunc Run() {}\n")
	lockedDir := filepath.Join(root, "locked")
	if err := os.MkdirAll(lockedDir, 0o700); err != nil {
		t.Fatalf("create locked dir: %v", err)
	}
	if err := os.Chmod(lockedDir, 0); err != nil {
		t.Fatalf("chmod locked dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockedDir, 0o700) })

	svc, _, state := newTestService(t, root)
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	if run.Status != RunStatusCompleted || run.FilesIngested != 1 {
		t.Fatalf("expected useful file to ingest despite walk error, got %#v", run)
	}

	skipped, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusSkipped})
	if err != nil {
		t.Fatalf("list skipped states: %v", err)
	}
	if len(skipped) != 1 || skipped[0].SkippedReason != SkipReasonStatError {
		t.Fatalf("expected one stat-error state, got %#v", skipped)
	}
}

func TestListFiles_FiltersByExtensionBeforePagination(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "main.go"), "package main\n\nfunc Run() {}\n")
	writeFile(t, filepath.Join(root, "internal", "helper.go"), "package internal\n\nfunc Help() {}\n")
	writeFile(t, filepath.Join(root, "docs", "guide.md"), "# Guide\n\n## Setup\n")

	svc, _, _ := newTestService(t, root)
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("ingest project: %v", err)
	}

	first, err := svc.ListFiles(ctx, "example-service", FileStateFilter{
		Status:    FileStatusEligible,
		Extension: "GO",
	}, Pagination{PageSize: 1})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(first.Files) != 1 || !strings.HasSuffix(first.Files[0].RelativePath, ".go") || first.NextPageToken == "" {
		t.Fatalf("unexpected first page: %#v", first)
	}
	if first.Files[0].Extension != ".go" {
		t.Fatalf("expected normalized extension in metadata, got %#v", first.Files[0])
	}

	second, err := svc.ListFiles(ctx, "example-service", FileStateFilter{
		Status:    FileStatusEligible,
		Extension: ".go",
	}, Pagination{PageSize: 1, PageToken: first.NextPageToken})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(second.Files) != 1 || !strings.HasSuffix(second.Files[0].RelativePath, ".go") || second.NextPageToken != "" {
		t.Fatalf("unexpected second page: %#v", second)
	}

	if _, err := svc.ListFiles(ctx, "example-service", FileStateFilter{Extension: "bad/path"}, Pagination{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid extension input, got %v", err)
	}
}

func TestGetFile_UsesDirectStateLookup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	svc, _, _ := newTestService(t, root)
	fileHash := hashValue("cmd/main.go")
	svc.state = &lookupOnlyStateStore{state: FileState{
		ProjectID:        "example-service",
		RelativePathHash: fileHash,
		RelativePath:     "cmd/main.go",
		RelativePathSafe: true,
		Status:           FileStatusEligible,
		Present:          true,
	}}

	metadata, err := svc.GetFile(ctx, "example-service", repoFileID("example_ns", fileHash))
	if err != nil {
		t.Fatalf("get file: %v", err)
	}
	if metadata.RelativePath != "cmd/main.go" {
		t.Fatalf("expected direct lookup metadata, got %#v", metadata)
	}
}

func TestNormalizeFileExtension(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: ""},
		{name: "without dot", raw: "GO", want: ".go"},
		{name: "with dot", raw: ".Md", want: ".md"},
		{name: "digits", raw: "ts1", want: ".ts1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeFileExtension(tc.raw)
			if err != nil {
				t.Fatalf("normalize extension: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestNormalizeFileExtensionRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{".", "bad/path", `bad\path`, "go test", " go", "go ", "g_o", "g%", "g*", "go.md"} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := NormalizeFileExtension(raw); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected ErrInvalidInput for %q, got %v", raw, err)
			}
		})
	}
}

func TestIngestProject_SensitiveContentSkipIsHashOnly(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "main.go"), "package main\nvar access_token = placeholder\n")

	svc, graph, state := newTestService(t, root)
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	if run.FilesSkipped != 1 || run.FilesIngested != 0 {
		t.Fatalf("unexpected run counts: %#v", run)
	}

	states, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusSkipped})
	if err != nil {
		t.Fatalf("list states: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected one skipped state, got %#v", states)
	}
	skipped := states[0]
	if skipped.SkippedReason != SkipReasonSensitiveContent {
		t.Fatalf("expected sensitive-content skip, got %#v", skipped)
	}
	if skipped.RelativePath != "" || skipped.RelativePathSafe || skipped.ContentSHA256 != "" {
		t.Fatalf("sensitive skip must be hash-only without content hash: %#v", skipped)
	}

	repoFileNode, err := graph.GetNode(ctx, "RepoFile", repoFileID("example_ns", hashValue("cmd/main.go")))
	if err != nil {
		t.Fatalf("get skipped repo file: %v", err)
	}
	if _, ok := repoFileNode.Properties["relative_path"]; ok {
		t.Fatalf("skipped sensitive file leaked path: %#v", repoFileNode.Properties)
	}
	if _, ok := repoFileNode.Properties["content_sha256"]; ok {
		t.Fatalf("skipped sensitive file stored content hash: %#v", repoFileNode.Properties)
	}
}

func TestIngestProject_DeletedSkippedSensitiveFileBecomesHashOnlyAbsent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	filePath := filepath.Join(root, "cmd", "main.go")
	writeFile(t, filePath, "package main\nvar access_token = placeholder\n")

	svc, graph, state := newTestService(t, root)
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("initial ingest: %v", err)
	}
	if err := os.Remove(filePath); err != nil {
		t.Fatalf("remove sensitive file: %v", err)
	}
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("second ingest: %v", err)
	}

	states, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusAbsent})
	if err != nil {
		t.Fatalf("list absent states: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected one absent state, got %#v", states)
	}
	absent := states[0]
	if absent.Present || absent.RelativePath != "" || absent.RelativePathSafe || absent.ContentSHA256 != "" {
		t.Fatalf("deleted sensitive file must stay hash-only absent: %#v", absent)
	}
	repoFileNode, err := graph.GetNode(ctx, "RepoFile", repoFileID("example_ns", hashValue("cmd/main.go")))
	if err != nil {
		t.Fatalf("get absent repo file: %v", err)
	}
	if _, ok := repoFileNode.Properties["relative_path"]; ok {
		t.Fatalf("absent sensitive file leaked path: %#v", repoFileNode.Properties)
	}
}

func TestIngestProject_DeletedDeniedPathStateBecomesHashOnlyAbsent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	filePath := filepath.Join(root, ".env.local")
	writeFile(t, filePath, "placeholder=value\n")

	svc, _, state := newTestService(t, root)
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("initial ingest: %v", err)
	}
	if err := os.Remove(filePath); err != nil {
		t.Fatalf("remove denied file: %v", err)
	}
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("second ingest: %v", err)
	}

	states, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusAbsent})
	if err != nil {
		t.Fatalf("list absent states: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected one absent state, got %#v", states)
	}
	if states[0].Present || states[0].RelativePath != "" || states[0].RelativePathSafe || states[0].ContentSHA256 != "" {
		t.Fatalf("deleted denied file must stay hash-only absent: %#v", states[0])
	}
}

func TestIngestProject_EligibleToSkippedRemovesDerivedGraphEntries(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	filePath := filepath.Join(root, "cmd", "main.go")
	writeFile(t, filePath, "package main\n\nfunc Run() {}\n")

	svc, _, state := newTestService(t, root)
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("initial ingest: %v", err)
	}
	eligible, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusEligible})
	if err != nil {
		t.Fatalf("list eligible states: %v", err)
	}
	goState := findState(t, eligible, "cmd/main.go")
	fileID := repoFileID("example_ns", goState.RelativePathHash)
	writeFile(t, filePath, "package main\nvar access_token = placeholder\n")
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("second ingest: %v", err)
	}

	chunks, err := svc.ListChunks(ctx, "example-service", fileID, Pagination{}, 0)
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(chunks.Chunks) != 0 {
		t.Fatalf("eligible-to-skipped retained stale chunks: %#v", chunks.Chunks)
	}
	symbols, err := svc.ListSymbols(ctx, "example-service", SymbolFilter{}, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list symbols: %v", err)
	}
	for _, symbol := range symbols.Symbols {
		if symbol.FileID == fileID {
			t.Fatalf("eligible-to-skipped retained stale symbol: %#v", symbol)
		}
	}
}

func TestIngestProject_SkippedToEligibleStoresDerivedGraphEntries(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	filePath := filepath.Join(root, "cmd", "main.go")
	writeFile(t, filePath, "package main\nvar access_token = placeholder\n")

	svc, _, _ := newTestService(t, root)
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("initial ingest: %v", err)
	}
	writeFile(t, filePath, "package main\n\nfunc Run() {}\n")
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("second ingest: %v", err)
	}

	files, err := svc.ListFiles(ctx, "example-service", FileStateFilter{Status: FileStatusEligible, Extension: ".go"}, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	if len(files.Files) != 1 || files.Files[0].RelativePath != "cmd/main.go" {
		t.Fatalf("expected skipped file to become eligible, got %#v", files.Files)
	}
	chunks, err := svc.ListChunks(ctx, "example-service", files.Files[0].ID, Pagination{}, 0)
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(chunks.Chunks) != 1 || !strings.Contains(chunks.Chunks[0].Text, "func Run") {
		t.Fatalf("expected eligible chunks after skipped-to-eligible transition, got %#v", chunks.Chunks)
	}
	symbols, err := svc.ListSymbols(ctx, "example-service", SymbolFilter{}, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list symbols: %v", err)
	}
	found := false
	for _, symbol := range symbols.Symbols {
		found = found || symbol.FileID == files.Files[0].ID && symbol.Name == "Run"
	}
	if !found {
		t.Fatalf("expected Run symbol after skipped-to-eligible transition, got %#v", symbols.Symbols)
	}
}

func TestIngestProject_SkipsBinaryInvalidUTF8AndSymlinks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeBytes(t, filepath.Join(root, "binary.txt"), []byte{0, 1, 2, 3})
	writeBytes(t, filepath.Join(root, "invalid.txt"), []byte{0xff, 0xfe, 0xfd})
	writeFile(t, filepath.Join(root, "target.txt"), "safe text\n")
	if err := os.Symlink(filepath.Join(root, "target.txt"), filepath.Join(root, "linked.txt")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	svc, _, state := newTestService(t, root)
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	if run.FilesIngested != 1 || run.FilesSkipped != 3 {
		t.Fatalf("unexpected run counts: %#v", run)
	}

	states, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusSkipped})
	if err != nil {
		t.Fatalf("list skipped states: %v", err)
	}
	reasons := map[SkipReason]bool{}
	for _, fileState := range states {
		reasons[fileState.SkippedReason] = true
		if fileState.ContentSHA256 != "" {
			t.Fatalf("skipped file stored content hash: %#v", fileState)
		}
	}
	for _, reason := range []SkipReason{SkipReasonNULByte, SkipReasonInvalidUTF8, SkipReasonUnsafePath} {
		if !reasons[reason] {
			t.Fatalf("expected skip reason %q in %#v", reason, states)
		}
	}
}

func TestIngestPath_DeletedFileCreatesTombstone(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	filePath := filepath.Join(root, "docs", "guide.md")
	writeFile(t, filePath, "# Guide\n")

	svc, _, state := newTestService(t, root)
	if _, err := svc.IngestPath(ctx, "example-service", "docs/guide.md", TriggerManual); err != nil {
		t.Fatalf("ingest path: %v", err)
	}
	if err := os.Remove(filePath); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	if _, err := svc.IngestPath(ctx, "example-service", "docs/guide.md", TriggerManual); err != nil {
		t.Fatalf("ingest deleted path: %v", err)
	}

	states, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusAbsent})
	if err != nil {
		t.Fatalf("list absent states: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected absent state, got %#v", states)
	}
	if states[0].Present || states[0].ContentSHA256 != "" {
		t.Fatalf("deleted file must be tombstoned without content hash: %#v", states[0])
	}
	fileID := repoFileID("example_ns", states[0].RelativePathHash)
	chunks, err := svc.ListChunks(ctx, "example-service", fileID, Pagination{}, 0)
	if err != nil {
		t.Fatalf("list chunks for tombstoned file: %v", err)
	}
	if len(chunks.Chunks) != 0 {
		t.Fatalf("deleted file retained stale chunks: %#v", chunks)
	}
}

func TestIngestProject_ReplacesStaleGraphEntriesForChangedFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	filePath := filepath.Join(root, "cmd", "main.go")
	writeFile(t, filePath, "package main\n\nfunc OldName() {}\n")

	svc, _, state := newTestService(t, root)
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("initial ingest: %v", err)
	}
	writeFile(t, filePath, "package main\n\nfunc NewName() {}\n")
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("second ingest: %v", err)
	}

	symbols, err := svc.ListSymbols(ctx, "example-service", SymbolFilter{}, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list symbols: %v", err)
	}
	names := map[string]bool{}
	for _, symbol := range symbols.Symbols {
		names[symbol.Name] = true
	}
	if names["OldName"] || !names["NewName"] {
		t.Fatalf("expected stale symbol removal and new symbol, got %#v", symbols.Symbols)
	}

	states, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusEligible})
	if err != nil {
		t.Fatalf("list states: %v", err)
	}
	goState := findState(t, states, "cmd/main.go")
	chunks, err := svc.ListChunks(ctx, "example-service", repoFileID("example_ns", goState.RelativePathHash), Pagination{}, 0)
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(chunks.Chunks) != 1 || strings.Contains(chunks.Chunks[0].Text, "OldName") || !strings.Contains(chunks.Chunks[0].Text, "NewName") {
		t.Fatalf("expected stale chunk replacement, got %#v", chunks.Chunks)
	}
}

func TestIngestProject_ManualFallbackWorksForLiveProject(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "main.go"), "package main\n\nfunc Run() {}\n")

	svc, _, _ := newTestServiceWithUpdatePolicy(t, root, projectregistry.UpdatePolicyLive)
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("manual ingest fallback: %v", err)
	}
	if run.Status != RunStatusCompleted || run.FilesIngested != 1 {
		t.Fatalf("unexpected manual fallback run: %#v", run)
	}
}

func TestIngestPath_RejectsPathEscape(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	svc, _, _ := newTestService(t, root)
	if _, err := svc.IngestPath(ctx, "example-service", "../outside.go", TriggerManual); !errors.Is(err, ErrPathNotProjectLocal) {
		t.Fatalf("expected path-local error, got %v", err)
	}
}

func TestListFiles_P2FiltersUseSafeStateMetadata(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "src", "app.go"), "package main\n")
	writeFile(t, filepath.Join(root, "docs", "guide.md"), "# Guide\n")
	writeFile(t, filepath.Join(root, "docs", "secret.txt"), "access_token = placeholder\n")

	svc, _, _ := newTestService(t, root)
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	present := true
	files, err := svc.ListFiles(ctx, "example-service", FileStateFilter{
		PathPrefix: "docs/",
		Present:    &present,
	}, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list docs files: %v", err)
	}
	if len(files.Files) != 1 || files.Files[0].RelativePath != "docs/guide.md" {
		t.Fatalf("expected docs prefix filter to return safe present docs file, got %#v", files.Files)
	}
	skipped, err := svc.ListFiles(ctx, "example-service", FileStateFilter{
		Status:        FileStatusSkipped,
		SkippedReason: SkipReasonSensitiveContent,
	}, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list sensitive skips: %v", err)
	}
	if len(skipped.Files) != 1 || skipped.Files[0].RelativePath != "" || skipped.Files[0].SkippedReason != string(SkipReasonSensitiveContent) {
		t.Fatalf("expected one safe sensitive skip without relative path, got %#v", skipped.Files)
	}
	recent, err := svc.ListFiles(ctx, "example-service", FileStateFilter{ModifiedSince: time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC)}, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list recently modified files: %v", err)
	}
	if len(recent.Files) == 0 {
		t.Fatalf("expected modified-since filter to keep ingested files")
	}
}

func TestSymbolsHeadingsAndOutline_P2DiscoveryFlows(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "main.go"), "package main\n\nfunc Run() {}\n")
	writeFile(t, filepath.Join(root, "docs", "guide.md"), "# Guide\n\n## Setup\n")
	writeFile(t, filepath.Join(root, "web", "app.ts"), "export class Widget {}\nexport const load = () => true\n")
	writeFile(t, filepath.Join(root, "scripts", "worker.py"), "import os\nclass Worker:\n    def run(self):\n        return True\n")

	svc, _, state := newTestService(t, root)
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	states, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusEligible})
	if err != nil {
		t.Fatalf("list states: %v", err)
	}
	goFileID := repoFileID("example_ns", findState(t, states, "cmd/main.go").RelativePathHash)
	mdFileID := repoFileID("example_ns", findState(t, states, "docs/guide.md").RelativePathHash)

	filtered, err := svc.ListSymbols(ctx, "example-service", SymbolFilter{
		Kind:       SymbolKindFunction,
		NamePrefix: "R",
		FileID:     goFileID,
		Extension:  ".go",
		Package:    "main",
	}, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list filtered symbols: %v", err)
	}
	if len(filtered.Symbols) != 1 || filtered.Symbols[0].Name != "Run" {
		t.Fatalf("expected filtered Run symbol, got %#v", filtered.Symbols)
	}
	tsSymbols, err := svc.ListSymbols(ctx, "example-service", SymbolFilter{Extension: ".ts"}, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list ts symbols: %v", err)
	}
	if len(tsSymbols.Symbols) < 2 {
		t.Fatalf("expected TypeScript class/function symbols, got %#v", tsSymbols.Symbols)
	}
	pySymbols, err := svc.ListSymbols(ctx, "example-service", SymbolFilter{Extension: ".py", NamePrefix: "Worker"}, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list python symbols: %v", err)
	}
	if len(pySymbols.Symbols) != 1 || pySymbols.Symbols[0].Kind != string(SymbolKindClass) {
		t.Fatalf("expected Python class symbol, got %#v", pySymbols.Symbols)
	}
	headings, err := svc.ListHeadings(ctx, "example-service", mdFileID, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list headings: %v", err)
	}
	if len(headings.Headings) != 2 || headings.Headings[0].Text != "Guide" {
		t.Fatalf("expected markdown headings, got %#v", headings.Headings)
	}
	outline, err := svc.GetFileOutline(ctx, "example-service", mdFileID, FileOutlineOptions{})
	if err != nil {
		t.Fatalf("get file outline: %v", err)
	}
	if outline.File.ID != mdFileID || len(outline.Headings) != 2 || len(outline.Chunks) == 0 {
		t.Fatalf("unexpected outline: %#v", outline)
	}
}

func TestService_LatestRunMetadataIsSafe(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "main.go"), "package main\nfunc Run() {}\n")
	svc, _, _ := newTestService(t, root)

	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	latest, err := svc.LatestRunMetadata(ctx, "example-service")
	if err != nil {
		t.Fatalf("latest run metadata: %v", err)
	}
	if latest.ID != run.ID || latest.ProjectID != "example-service" || latest.Status != string(RunStatusCompleted) {
		t.Fatalf("unexpected latest run: %#v", latest)
	}
	encoded := fmt.Sprintf("%#v", latest)
	for _, forbidden := range []string{root, "cmd/main.go", "package main", "func Run", "content_sha256"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("latest metadata leaked %q: %#v", forbidden, latest)
		}
	}
}

func TestService_FileOutlineFiltersPaginatesAndOptionallyIncludesChunkText(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "main.go"), "package main\n\nfunc Alpha() {}\nfunc Beta() {}\nfunc Alfred() {}\n")
	svc, _, _ := newTestService(t, root)
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	states, err := svc.ListFileStates(ctx, "example-service", FileStateFilter{})
	if err != nil {
		t.Fatalf("list states: %v", err)
	}
	fileID := repoFileID("example_ns", findState(t, states, "cmd/main.go").RelativePathHash)

	outline, err := svc.GetFileOutline(ctx, "example-service", fileID, FileOutlineOptions{
		SymbolFilter:     SymbolFilter{Kind: SymbolKindFunction, NamePrefix: "Al"},
		SymbolPagination: Pagination{PageSize: 1},
	})
	if err != nil {
		t.Fatalf("get filtered outline: %v", err)
	}
	if len(outline.Symbols) != 1 || !strings.HasPrefix(outline.Symbols[0].Name, "Al") || outline.SymbolsNextPageToken == "" {
		t.Fatalf("expected first filtered symbol page, got %#v", outline)
	}
	encoded := fmt.Sprintf("%#v", outline)
	for _, forbidden := range []string{"func Alpha", "func Beta", "package main"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("outline included source text without opt-in %q: %#v", forbidden, outline)
		}
	}
	withText, err := svc.GetFileOutline(ctx, "example-service", fileID, FileOutlineOptions{
		IncludeChunkText: true,
		MaxChunkBytes:    32,
	})
	if err != nil {
		t.Fatalf("get text outline: %v", err)
	}
	if len(withText.Chunks) == 0 || !strings.Contains(withText.Chunks[0].Text, "package main") || !withText.Chunks[0].TextTruncated {
		t.Fatalf("expected bounded chunk text in outline, got %#v", withText.Chunks)
	}
}

func TestInfraParsers_ExtractDependencyFreeSymbols(t *testing.T) {
	cases := []struct {
		name     string
		parse    func() ([]Symbol, error)
		want     string
		wantKind SymbolKind
	}{
		{name: "docker", parse: func() ([]Symbol, error) { return ParseDockerfileSymbols([]byte("FROM alpine AS build\n")) }, want: "build", wantKind: SymbolKindStage},
		{name: "make", parse: func() ([]Symbol, error) { return ParseMakefileSymbols([]byte("test:\n\tgo test ./...\n")) }, want: "test", wantKind: SymbolKindTarget},
		{name: "openapi", parse: func() ([]Symbol, error) {
			return ParseOpenAPIPathSymbols([]byte("openapi: 3.0.0\npaths:\n  /healthz:\n    get: {}\n"))
		}, want: "/healthz", wantKind: SymbolKindPath},
		{name: "json", parse: func() ([]Symbol, error) { return ParseJSONTopLevelKeys([]byte(`{"scripts":{},"name":"app"}`)) }, want: "name", wantKind: SymbolKindKey},
		{name: "config", parse: func() ([]Symbol, error) { return ParseConfigTopLevelKeys([]byte("services:\n  api: {}\n")) }, want: "services", wantKind: SymbolKindKey},
		{name: "sql", parse: func() ([]Symbol, error) {
			return ParseSQLMigrationSymbols("db/001_create_users.sql", []byte("create table users(id int);\n"))
		}, want: "001_create_users", wantKind: SymbolKindMigration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			symbols, err := tc.parse()
			if err != nil {
				t.Fatalf("parse symbols: %v", err)
			}
			if len(symbols) == 0 || symbols[0].Name != tc.want || symbols[0].Kind != tc.wantKind {
				t.Fatalf("expected %s/%s, got %#v", tc.wantKind, tc.want, symbols)
			}
		})
	}
}

func newTestService(t *testing.T, root string) (*Service, *ladybug.MemoryGraph, *SQLiteStore) {
	t.Helper()
	return newTestServiceWithOptions(t, root, projectregistry.UpdatePolicyManual, 1024)
}

func newTestServiceWithUpdatePolicy(t *testing.T, root string, updatePolicy string) (*Service, *ladybug.MemoryGraph, *SQLiteStore) {
	t.Helper()
	return newTestServiceWithOptions(t, root, updatePolicy, 1024)
}

func newTestServiceWithMaxChunkBytes(t *testing.T, root string, maxChunkBytes int) (*Service, *ladybug.MemoryGraph, *SQLiteStore) {
	t.Helper()
	return newTestServiceWithOptions(t, root, projectregistry.UpdatePolicyManual, maxChunkBytes)
}

func newTestServiceWithOptions(t *testing.T, root string, updatePolicy string, maxChunkBytes int) (*Service, *ladybug.MemoryGraph, *SQLiteStore) {
	t.Helper()
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:                    "example-service",
		DisplayName:           "Example Service",
		RootPath:              root,
		Enabled:               true,
		Classification:        projectregistry.ClassificationInternal,
		GraphNamespace:        "example_ns",
		DigestMode:            projectregistry.DigestModeContentGraph,
		UpdatePolicy:          updatePolicy,
		Include:               []string{"**/*"},
		FollowSymlinks:        false,
		MaxFileBytes:          4096,
		MaxChunkBytes:         maxChunkBytes,
		SensitiveMarkerPolicy: SensitiveMarkerPolicySkipFile,
	}}, projectregistry.Options{
		ContentGraphEnabled:          true,
		LiveUpdatesEnabled:           updatePolicy == projectregistry.UpdatePolicyLive,
		ContentGraphApprovalAccepted: true,
	})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteschema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}
	state := NewSQLiteStore(db.SQLDB())
	svc := NewService(registry, NewGraphStore(graph), state)
	svc.now = func() time.Time { return time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC) }
	svc.newID = func(projectregistry.Project, time.Time) string { return "ingest_run_1" }
	return svc, graph, state
}

func findState(t *testing.T, states []FileState, relativePath string) FileState {
	t.Helper()
	for _, state := range states {
		if state.RelativePath == relativePath {
			return state
		}
	}
	t.Fatalf("state for %q not found in %#v", relativePath, states)
	return FileState{}
}

func findSymbolMetadata(t *testing.T, symbols []SymbolMetadata, name string) SymbolMetadata {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.Name == name {
			return symbol
		}
	}
	t.Fatalf("symbol %q not found in %#v", name, symbols)
	return SymbolMetadata{}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	writeBytes(t, path, []byte(content))
}

func writeBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func receiveStartedFile(t *testing.T, started <-chan string) string {
	t.Helper()
	select {
	case relative := <-started:
		return relative
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for file processing to start")
		return ""
	}
}

func receiveRunResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ingestion run")
		return nil
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for signal")
	}
}

func waitForRunProgress(t *testing.T, ctx context.Context, svc *Service, projectID string, runID string, minFilesSeen int) RunMetadata {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last RunMetadata
	for time.Now().Before(deadline) {
		metadata, err := svc.RunMetadata(ctx, projectID, runID)
		if err != nil {
			t.Fatalf("run metadata: %v", err)
		}
		last = metadata
		if metadata.FilesSeen >= minFilesSeen {
			return metadata
		}
		time.Sleep(10 * time.Millisecond)
	}
	return last
}

func assertNoPropertyContains(t *testing.T, properties map[string]string, forbidden string) {
	t.Helper()
	for key, value := range properties {
		if strings.Contains(value, forbidden) {
			t.Fatalf("property %q leaked forbidden value %q in %#v", key, forbidden, properties)
		}
	}
}

func assertNoSearchLeak(t *testing.T, root string, value any, extraForbidden ...string) {
	t.Helper()
	encoded := fmt.Sprintf("%#v", value)
	forbidden := append([]string{root, "content_sha256"}, extraForbidden...)
	for _, item := range forbidden {
		if item != "" && strings.Contains(encoded, item) {
			t.Fatalf("search result leaked %q: %#v", item, value)
		}
	}
}

type lookupOnlyStateStore struct {
	state FileState
}

func (store *lookupOnlyStateStore) SaveRun(context.Context, Run) error {
	return errors.New("unexpected SaveRun")
}

func (store *lookupOnlyStateStore) GetRun(context.Context, string, string) (Run, error) {
	return Run{}, errors.New("unexpected GetRun")
}

func (store *lookupOnlyStateStore) ListLatestRuns(context.Context, string, int) ([]Run, error) {
	return nil, errors.New("unexpected ListLatestRuns")
}

func (store *lookupOnlyStateStore) SaveFileState(context.Context, FileState) error {
	return errors.New("unexpected SaveFileState")
}

func (store *lookupOnlyStateStore) ListFileStates(context.Context, string, FileStateFilter) ([]FileState, error) {
	return nil, errors.New("unexpected ListFileStates")
}

func (store *lookupOnlyStateStore) ListFileStatesPage(context.Context, string, FileStateFilter, Pagination) ([]FileState, string, error) {
	return nil, "", errors.New("unexpected ListFileStatesPage")
}

func (store *lookupOnlyStateStore) GetFileStateByHash(_ context.Context, projectID string, relativePathHash string) (FileState, error) {
	if store.state.ProjectID != projectID || store.state.RelativePathHash != relativePathHash {
		return FileState{}, ErrIngestionNotFound
	}
	return store.state, nil
}

func (store *lookupOnlyStateStore) GetExtractorCache(context.Context, string, string, string, string, string) (ExtractorCacheEntry, error) {
	return ExtractorCacheEntry{}, errors.New("unexpected GetExtractorCache")
}

func (store *lookupOnlyStateStore) SaveExtractorCache(context.Context, ExtractorCacheEntry) error {
	return errors.New("unexpected SaveExtractorCache")
}

func (store *lookupOnlyStateStore) DeleteExtractorCacheForFile(context.Context, string, string) error {
	return errors.New("unexpected DeleteExtractorCacheForFile")
}

type batchCountingGraph struct {
	*ladybug.MemoryGraph
	batchCalls               int
	currentRepoFileNodes     int
	maxRepoFileNodesPerBatch int
}

func (graph *batchCountingGraph) Batch(ctx context.Context, fn func(ladybug.Graph) error) error {
	graph.batchCalls++
	graph.currentRepoFileNodes = 0
	if err := fn(graph); err != nil {
		return err
	}
	if graph.currentRepoFileNodes > graph.maxRepoFileNodesPerBatch {
		graph.maxRepoFileNodesPerBatch = graph.currentRepoFileNodes
	}
	return nil
}

func (graph *batchCountingGraph) PutNode(ctx context.Context, node ladybug.Node) error {
	if node.Label == "RepoFile" {
		graph.currentRepoFileNodes++
	}
	return graph.MemoryGraph.PutNode(ctx, node)
}
