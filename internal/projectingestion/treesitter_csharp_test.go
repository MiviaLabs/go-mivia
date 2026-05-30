package projectingestion

import (
	"context"
	"path/filepath"
	"testing"
)

func TestTreeSitterCSharpExtractsSymbols(t *testing.T) {
	result := parseWithExtractor(t, newTreeSitterCSharpExtractor(), "src/Service.cs", []byte(`
using System.Text;

namespace Example.Product;

public interface IWorker {}
public struct Job {}
public record Result(string ID);
public enum State { Ready }
public class Service
{
	public Service() {}
	public string Name { get; set; }
	public void Run() {}
}
`))

	assertSymbol(t, result.Symbols, SymbolKindImport, "System.Text", "System.Text", "", 2, 2)
	assertSymbol(t, result.Symbols, SymbolKindPackage, "Example.Product", "", "", 4, 4)
	assertSymbol(t, result.Symbols, SymbolKindType, "IWorker", "", "", 6, 6)
	assertSymbol(t, result.Symbols, SymbolKindType, "Job", "", "", 7, 7)
	assertSymbol(t, result.Symbols, SymbolKindType, "Result", "", "", 8, 8)
	assertSymbol(t, result.Symbols, SymbolKindType, "State", "", "", 9, 9)
	assertSymbol(t, result.Symbols, SymbolKindClass, "Service", "", "", 10, 15)
	assertSymbol(t, result.Symbols, SymbolKindMethod, "Service", "", "", 12, 12)
	assertSymbol(t, result.Symbols, SymbolKindMethod, "Name", "", "", 13, 13)
	assertSymbol(t, result.Symbols, SymbolKindMethod, "Run", "", "", 14, 14)
}

func TestTreeSitterCSharpExtractsReferencesAndCalls(t *testing.T) {
	result := parseWithExtractor(t, newTreeSitterCSharpExtractor(), "src/Service.cs", []byte(`
public class Service
{
	public void Helper() {}
	public void Run()
	{
		Helper();
		System.Console.WriteLine("ok");
	}
}
`))

	if !hasCall(result.Calls, "Run", "Helper") {
		t.Fatalf("expected Run -> Helper call, got %#v", result.Calls)
	}
	if !hasCall(result.Calls, "Run", "WriteLine") {
		t.Fatalf("expected Run -> WriteLine call, got %#v", result.Calls)
	}
	if !hasReference(result.References, "Run", "Helper") {
		t.Fatalf("expected Helper reference in Run, got %#v", result.References)
	}
	for _, call := range result.Calls {
		if call.StartByte <= 0 || call.EndByte <= call.StartByte {
			t.Fatalf("expected call byte span, got %#v", call)
		}
	}
}

func TestBadCSharpSyntaxRecordsParseError(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Good.cs"), "public class Good { public void Run() {} }\n")
	writeFile(t, filepath.Join(root, "Bad.cs"), "public class Bad { public void Broken( }\n")

	svc, _, state := newTestService(t, root)
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	if run.Status != RunStatusCompleted || run.ErrorCategory != "file_errors" {
		t.Fatalf("expected completed run with file errors, got %#v", run)
	}
	skipped, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusSkipped})
	if err != nil {
		t.Fatalf("list skipped states: %v", err)
	}
	if len(skipped) != 1 || skipped[0].SkippedReason != SkipReasonParseError || skipped[0].ContentSHA256 != "" {
		t.Fatalf("expected parse-error skip without content hash, got %#v", skipped)
	}
}
