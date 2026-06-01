package projectingestion

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExtractorRegistryDispatchesSupportedFiles(t *testing.T) {
	ctx := context.Background()
	registry := NewDefaultExtractorRegistry()

	tests := []struct {
		relative string
		content  string
		name     string
	}{
		{relative: "cmd/main.go", content: "package main\n\nfunc Run() {}\n", name: string(ExtractorGoStdlibAST)},
		{relative: "docs/guide.md", content: "# Guide\n", name: string(ExtractorMarkdownHeading)},
		{relative: "web/app.tsx", content: "export function App() { return <main /> }\n", name: string(ExtractorTreeSitterTSX)},
		{relative: "lib/home.dart", content: "class Home {}\n", name: string(ExtractorTreeSitterDart)},
		{relative: "Dockerfile", content: "FROM scratch AS runtime\n", name: string(ExtractorInfraLightweight)},
		{relative: "Assets/Scripts/Game.asmdef", content: `{"name":"Game.Runtime"}`, name: string(ExtractorInfraLightweight)},
	}

	for _, tt := range tests {
		t.Run(tt.relative, func(t *testing.T) {
			result, err := registry.Extract(ctx, tt.relative, []byte(tt.content))
			if err != nil {
				t.Fatalf("extract %s: %v", tt.relative, err)
			}
			if result.ExtractorName != tt.name {
				t.Fatalf("expected extractor %q, got %#v", tt.name, result)
			}
		})
	}
}

func TestExtractorRegistryValidationFailureIsSanitized(t *testing.T) {
	registry := NewExtractorRegistry(staticExtractor{
		name:     "broken-extractor",
		version:  extractorVersionOne,
		supports: func(string) bool { return true },
		parse:    func(context.Context, string, []byte) (ExtractorResult, error) { return ExtractorResult{}, nil },
		validate: func() error { return errors.New("raw validation detail") },
	})

	err := registry.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	message := err.Error()
	if !strings.Contains(message, extractorInitErrorCategory) || !strings.Contains(message, "broken-extractor") {
		t.Fatalf("expected sanitized category and extractor name, got %q", message)
	}
	if strings.Contains(message, "validation detail") {
		t.Fatalf("validation error leaked raw detail: %q", message)
	}
}

func TestTreeSitterCSharpExtractorAssignsNamespaceMetadata(t *testing.T) {
	ctx := context.Background()
	registry := NewDefaultExtractorRegistry()

	result, err := registry.Extract(ctx, "Assets/Scripts/App.cs", []byte("namespace Demo.Core { class App { void Run() {} } }\n"))
	if err != nil {
		t.Fatalf("extract csharp: %v", err)
	}
	hasNamespace := false
	for _, symbol := range result.Symbols {
		if symbol.Kind == SymbolKindPackage && symbol.Name == "Demo.Core" {
			hasNamespace = true
			continue
		}
		if (symbol.Kind == SymbolKindClass || symbol.Kind == SymbolKindMethod) && symbol.PackageName != "Demo.Core" {
			t.Fatalf("expected namespace package metadata on C# symbol: %#v", symbol)
		}
	}
	if !hasNamespace {
		t.Fatalf("expected namespace declaration symbol: %#v", result.Symbols)
	}
}

func TestTreeSitterCSharpExtractorAssignsFileScopedNamespaceToFollowingSymbols(t *testing.T) {
	ctx := context.Background()
	registry := NewDefaultExtractorRegistry()

	result, err := registry.Extract(ctx, "Assets/Scripts/App.cs", []byte("namespace Demo.Core;\nclass App { void Run() {} }\n"))
	if err != nil {
		t.Fatalf("extract csharp: %v", err)
	}
	for _, symbol := range result.Symbols {
		if symbol.Kind == SymbolKindClass || symbol.Kind == SymbolKindMethod {
			if symbol.PackageName != "Demo.Core" {
				t.Fatalf("expected file-scoped namespace package metadata on C# symbol: %#v", symbol)
			}
		}
	}
}
