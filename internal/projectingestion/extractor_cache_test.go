package projectingestion

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestExtractorCache(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := filepath.Join(root, "src", "cached.count")
	writeFile(t, target, "safe content\n")

	svc, _, state := newTestService(t, root)
	extractor := &countingExtractor{name: "counting", version: "1"}
	svc.extractors = NewExtractorRegistry(extractor)

	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("initial ingest: %v", err)
	}
	if extractor.calls != 1 {
		t.Fatalf("expected first ingest to parse once, got %d", extractor.calls)
	}
	states, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusEligible})
	if err != nil {
		t.Fatalf("list file states: %v", err)
	}
	fileState := findState(t, states, "src/cached.count")
	entry, err := state.GetExtractorCache(ctx, "example-service", fileState.RelativePathHash, fileState.ContentSHA256, "counting", "1")
	if err != nil {
		t.Fatalf("expected extractor cache row: %v", err)
	}
	if len(entry.Symbols) != 1 || entry.Symbols[0].Name != "parse-1" || len(entry.Headings) != 0 {
		t.Fatalf("unexpected cache payload: %#v", entry)
	}

	secondRun, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if secondRun.FilesUnchanged != 1 || secondRun.FilesIngested != 0 {
		t.Fatalf("expected unchanged second ingest, got %#v", secondRun)
	}
	if extractor.calls != 1 {
		t.Fatalf("expected cache hit to avoid parse, got %d calls", extractor.calls)
	}

	extractor.version = "2"
	versionRun, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("version-changed ingest: %v", err)
	}
	if versionRun.FilesIngested != 1 || versionRun.FilesUnchanged != 0 {
		t.Fatalf("expected extractor version change to reingest, got %#v", versionRun)
	}
	if extractor.calls != 2 {
		t.Fatalf("expected version change to reparse, got %d calls", extractor.calls)
	}

	writeFile(t, target, syntheticSensitiveMarker())
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("sensitive ingest: %v", err)
	}
	if _, err := state.GetExtractorCache(ctx, "example-service", fileState.RelativePathHash, fileState.ContentSHA256, "counting", "1"); err != ErrExtractorCacheMiss {
		t.Fatalf("expected skipped file to delete cache rows, got %v", err)
	}
	skipped, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusSkipped})
	if err != nil {
		t.Fatalf("list skipped states: %v", err)
	}
	if len(skipped) != 1 || skipped[0].SkippedReason != SkipReasonSensitiveContent || skipped[0].ContentSHA256 != "" {
		t.Fatalf("expected sensitive skip without content hash, got %#v", skipped)
	}
}

func TestExtractorCacheFingerprintInvalidatesSameVersion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "src", "cached.count"), "safe content\n")

	svc, _, _ := newTestService(t, root)
	extractor := &countingExtractor{name: "counting", version: "1", fingerprint: "fp-1"}
	svc.extractors = NewExtractorRegistry(extractor)

	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		t.Fatalf("initial ingest: %v", err)
	}
	if extractor.calls != 1 {
		t.Fatalf("expected first ingest to parse once, got %d", extractor.calls)
	}

	extractor.fingerprint = "fp-2"
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("fingerprint-changed ingest: %v", err)
	}
	if run.FilesIngested != 1 || run.FilesUnchanged != 0 {
		t.Fatalf("expected extractor fingerprint change to reingest, got %#v", run)
	}
	if extractor.calls != 2 {
		t.Fatalf("expected fingerprint change to reparse, got %d calls", extractor.calls)
	}
}

func syntheticSensitiveMarker() string {
	return "access" + "_token = placeholder\n"
}

type countingExtractor struct {
	name        string
	version     string
	fingerprint string
	calls       int
}

func (extractor *countingExtractor) Name() string {
	return extractor.name
}

func (extractor *countingExtractor) Version() string {
	return extractor.version
}

func (extractor *countingExtractor) Fingerprint() string {
	return extractor.fingerprint
}

func (extractor *countingExtractor) Supports(relative string) bool {
	return filepath.Ext(relative) == ".count"
}

func (extractor *countingExtractor) Validate() error {
	return nil
}

func (extractor *countingExtractor) Parse(context.Context, string, []byte) (ExtractorResult, error) {
	extractor.calls++
	return ExtractorResult{
		Symbols: []Symbol{{
			Kind:      SymbolKindFunction,
			Name:      fmt.Sprintf("parse-%d", extractor.calls),
			StartLine: 1,
			EndLine:   1,
		}},
	}, nil
}
