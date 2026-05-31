package projectingestion

import (
	"context"
	"path/filepath"
	"testing"
)

func TestTreeSitterDartExtractsSymbols(t *testing.T) {
	result := parseWithExtractor(t, newTreeSitterDartExtractor(), "lib/home.dart", []byte(`
import 'package:flutter/material.dart';
export 'src/routes.dart';

mixin AnalyticsMixin { void track() {} }
extension BuildContextRoutes on BuildContext { void goHome() => Navigator.of(this).pushNamed('/'); }
extension type UserId(String value) {}
typedef WidgetBuilderAlias = Widget Function(BuildContext context);
enum Flavor { dev, prod }

class HomePage extends StatefulWidget {
  const HomePage({super.key});
  @override
  State<HomePage> createState() => _HomePageState();
}

class _HomePageState extends State<HomePage> {
  int count = 0;
  Widget get title => const Text('Home');
  set value(int next) { count = next; }
  @override
  Widget build(BuildContext context) {
    setState(() { count++; });
    Navigator.of(context).pushNamed('/details');
    return Scaffold(body: Text('$count'));
  }
}

void main() { runApp(const MaterialApp(home: HomePage())); }
`))

	assertSymbol(t, result.Symbols, SymbolKindImport, "package:flutter/material.dart", "package:flutter/material.dart", "", 2, 2)
	assertSymbol(t, result.Symbols, SymbolKindExport, "src/routes.dart", "src/routes.dart", "", 3, 3)
	assertSymbol(t, result.Symbols, SymbolKindType, "AnalyticsMixin", "", "", 5, 5)
	assertSymbol(t, result.Symbols, SymbolKindType, "BuildContextRoutes", "", "", 6, 6)
	assertSymbol(t, result.Symbols, SymbolKindType, "UserId", "", "", 7, 7)
	assertSymbol(t, result.Symbols, SymbolKindType, "WidgetBuilderAlias", "", "", 8, 8)
	assertSymbol(t, result.Symbols, SymbolKindType, "Flavor", "", "", 9, 9)
	assertSymbol(t, result.Symbols, SymbolKindClass, "HomePage", "", "", 11, 15)
	assertSymbol(t, result.Symbols, SymbolKindFlutterWidget, "HomePage", "", "", 11, 15)
	assertSymbol(t, result.Symbols, SymbolKindMethod, "HomePage", "", "HomePage", 12, 12)
	assertSymbol(t, result.Symbols, SymbolKindMethod, "createState", "", "HomePage", 14, 14)
	assertSymbol(t, result.Symbols, SymbolKindFlutterState, "_HomePageState", "", "", 17, 27)
	assertSymbol(t, result.Symbols, SymbolKindMethod, "title", "", "_HomePageState", 19, 19)
	assertSymbol(t, result.Symbols, SymbolKindMethod, "value", "", "_HomePageState", 20, 20)
	assertSymbol(t, result.Symbols, SymbolKindMethod, "build", "", "_HomePageState", 22, 22)
	assertSymbol(t, result.Symbols, SymbolKindFlutterBuildMethod, "build", "", "_HomePageState", 22, 22)
	assertSymbol(t, result.Symbols, SymbolKindFunction, "main", "", "", 29, 29)
}

func TestTreeSitterDartExtractsFlutterCallsAndReferences(t *testing.T) {
	result := parseWithExtractor(t, newTreeSitterDartExtractor(), "lib/home.dart", []byte(`
class _HomePageState extends State<HomePage> {
  Widget build(BuildContext context) {
    setState(() { count++; });
    Navigator.of(context).pushNamed('/details');
    return Scaffold(body: Text('$count'));
  }
}
`))

	for _, callee := range []string{"setState", "of", "pushNamed", "Scaffold", "Text"} {
		if !hasCall(result.Calls, "build", callee) {
			t.Fatalf("expected build -> %s call, got %#v", callee, result.Calls)
		}
	}
	if !hasReference(result.References, "build", "Navigator") {
		t.Fatalf("expected Navigator reference in build, got %#v", result.References)
	}
	for _, call := range result.Calls {
		if call.ResolutionStatus != "unresolved" || call.Confidence != "candidate" || call.StartByte <= 0 || call.EndByte <= call.StartByte {
			t.Fatalf("expected unresolved candidate call with byte span, got %#v", call)
		}
	}
}

func TestTreeSitterDartExtractorSupportsGeneratedFiles(t *testing.T) {
	for _, relative := range []string{
		"lib/model.g.dart",
		"lib/model.freezed.dart",
		"test/router.mocks.dart",
		"lib/other.generated.dart",
	} {
		result := parseWithExtractor(t, newTreeSitterDartExtractor(), relative, []byte(`class GeneratedModel { GeneratedModel(); }`))
		assertSymbol(t, result.Symbols, SymbolKindClass, "GeneratedModel", "", "", 1, 1)
	}
}

func TestTreeSitterDartExtractorLifecycleValidation(t *testing.T) {
	if err := newTreeSitterDartExtractor().Validate(); err != nil {
		t.Fatalf("validate dart extractor: %v", err)
	}
}

func TestDartContentGraphIndexesGeneratedFilesAndSkipsSensitiveContent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "lib", "model.g.dart"), "class GeneratedModel { GeneratedModel(); }\n")
	writeFile(t, filepath.Join(root, "lib", "secret.dart"), "final api_key = 'not-real-test-secret';\n")

	svc, _, state := newTestService(t, root)
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	if run.Status != RunStatusCompleted {
		t.Fatalf("expected completed run, got %#v", run)
	}
	symbols, err := svc.ListSymbols(ctx, "example-service", SymbolFilter{NameContains: "GeneratedModel"}, Pagination{PageSize: 10})
	if err != nil {
		t.Fatalf("list symbols: %v", err)
	}
	if len(symbols.Symbols) == 0 {
		t.Fatalf("expected generated Dart symbol, got %#v", symbols)
	}
	skipped, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusSkipped})
	if err != nil {
		t.Fatalf("list skipped states: %v", err)
	}
	if len(skipped) != 1 || skipped[0].SkippedReason != SkipReasonSensitiveContent || skipped[0].ContentSHA256 != "" {
		t.Fatalf("expected sensitive Dart skip without content hash, got %#v", skipped)
	}
}
