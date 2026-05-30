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

func TestParseGoFileSemantic_ExtractsReferencesCallsAndSourceSpans(t *testing.T) {
	source := []byte(`package sample

type Service struct{}

func helper() {}

func Run() {
	helper()
}
`)

	result, err := ParseGoFileSemantic("synthetic.go", source)
	if err != nil {
		t.Fatalf("parse go semantic file: %v", err)
	}
	assertSymbol(t, result.Symbols, SymbolKindFunction, "Run", "", "", 7, 9)
	run := findSymbol(t, result.Symbols, SymbolKindFunction, "Run")
	if run.StartByte <= 0 || run.EndByte <= run.StartByte {
		t.Fatalf("expected Run byte span, got %#v", run)
	}
	if !hasCall(result.Calls, "Run", "helper") {
		t.Fatalf("expected Run -> helper call, got %#v", result.Calls)
	}
	if !hasReference(result.References, "Run", "helper") {
		t.Fatalf("expected helper reference in Run, got %#v", result.References)
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

func findSymbol(t *testing.T, symbols []Symbol, kind SymbolKind, name string) Symbol {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.Kind == kind && symbol.Name == name {
			return symbol
		}
	}
	t.Fatalf("missing symbol %s %s in %#v", kind, name, symbols)
	return Symbol{}
}

func assertSymbolHasByteSpan(t *testing.T, symbols []Symbol, kind SymbolKind, name string) {
	t.Helper()
	symbol := findSymbol(t, symbols, kind, name)
	if symbol.EndByte <= symbol.StartByte {
		t.Fatalf("expected byte span for %s %s, got %#v", kind, name, symbol)
	}
}

func hasCall(calls []Call, caller string, callee string) bool {
	for _, call := range calls {
		if call.CallerName == caller && call.CalleeName == callee {
			return true
		}
	}
	return false
}

func hasReference(references []Reference, enclosing string, target string) bool {
	for _, ref := range references {
		if ref.EnclosingSymbolName == enclosing && ref.TargetName == target {
			return true
		}
	}
	return false
}
