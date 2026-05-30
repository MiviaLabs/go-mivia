package projectingestion

import "testing"

func TestParseGoFile_ExtractsPackagesImportsTypesFunctionsAndMethods(t *testing.T) {
	source := []byte(`package sample

import (
	"context"
	alias "io"
)

type Service struct{}

type Worker interface {
	Run() error
}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Run(ctx context.Context) error {
	return nil
}
`)

	symbols, err := ParseGoFile("synthetic.go", source)
	if err != nil {
		t.Fatalf("parse go file: %v", err)
	}

	assertSymbol(t, symbols, SymbolKindPackage, "sample", "", "", 1, 1)
	assertSymbol(t, symbols, SymbolKindImport, "context", "context", "", 4, 4)
	assertSymbol(t, symbols, SymbolKindImport, "alias", "io", "", 5, 5)
	assertSymbol(t, symbols, SymbolKindType, "Service", "", "", 8, 8)
	assertSymbol(t, symbols, SymbolKindType, "Worker", "", "", 10, 12)
	assertSymbol(t, symbols, SymbolKindFunction, "NewService", "", "", 14, 16)
	assertSymbol(t, symbols, SymbolKindMethod, "Run", "", "*Service", 18, 20)
}

func TestParseGoFile_RejectsInvalidUTF8(t *testing.T) {
	if _, err := ParseGoFile("synthetic.go", []byte{0xff, 0xfe}); err == nil {
		t.Fatal("expected invalid utf-8 parse error")
	}
}

func assertSymbol(t *testing.T, symbols []Symbol, kind SymbolKind, name string, importPath string, receiver string, startLine int, endLine int) {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.Kind != kind || symbol.Name != name {
			continue
		}
		if importPath != "" && symbol.ImportPath != importPath {
			t.Fatalf("expected import path %q for %#v", importPath, symbol)
		}
		if receiver != "" && symbol.Receiver != receiver {
			t.Fatalf("expected receiver %q for %#v", receiver, symbol)
		}
		if symbol.StartLine != startLine || symbol.EndLine != endLine {
			t.Fatalf("expected %s %s lines %d-%d, got %#v", kind, name, startLine, endLine, symbol)
		}
		return
	}
	t.Fatalf("missing symbol %s %s in %#v", kind, name, symbols)
}
