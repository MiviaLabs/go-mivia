package projectingestion

import (
	"context"
	_ "embed"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_c_sharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

const (
	ExtractorTreeSitterJavaScript ExtractorName = "treesitter-javascript"
	ExtractorTreeSitterTypeScript ExtractorName = "treesitter-typescript"
	ExtractorTreeSitterTSX        ExtractorName = "treesitter-tsx"
	ExtractorTreeSitterCSharp     ExtractorName = "treesitter-csharp"
)

//go:embed queries/javascript.scm
var javascriptQuery string

//go:embed queries/typescript.scm
var typescriptQuery string

//go:embed queries/tsx.scm
var tsxQuery string

//go:embed queries/csharp.scm
var csharpQuery string

type treeSitterExtractor struct {
	name         string
	version      string
	extensions   map[string]struct{}
	query        string
	languageFunc func() *tree_sitter.Language
}

func newTreeSitterJavaScriptExtractor() Extractor {
	return treeSitterExtractor{
		name:       string(ExtractorTreeSitterJavaScript),
		version:    extractorVersionOne,
		extensions: extensionSet(".js", ".mjs", ".cjs"),
		query:      javascriptQuery,
		languageFunc: func() *tree_sitter.Language {
			return tree_sitter.NewLanguage(tree_sitter_javascript.Language())
		},
	}
}

func newTreeSitterCSharpExtractor() Extractor {
	return treeSitterExtractor{
		name:       string(ExtractorTreeSitterCSharp),
		version:    extractorVersionOne,
		extensions: extensionSet(".cs"),
		query:      csharpQuery,
		languageFunc: func() *tree_sitter.Language {
			return tree_sitter.NewLanguage(tree_sitter_c_sharp.Language())
		},
	}
}

func newTreeSitterTypeScriptExtractor() Extractor {
	return treeSitterExtractor{
		name:       string(ExtractorTreeSitterTypeScript),
		version:    extractorVersionOne,
		extensions: extensionSet(".ts", ".mts", ".cts"),
		query:      typescriptQuery,
		languageFunc: func() *tree_sitter.Language {
			return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
		},
	}
}

func newTreeSitterTSXExtractor() Extractor {
	return treeSitterExtractor{
		name:       string(ExtractorTreeSitterTSX),
		version:    extractorVersionOne,
		extensions: extensionSet(".tsx", ".jsx"),
		query:      tsxQuery,
		languageFunc: func() *tree_sitter.Language {
			return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX())
		},
	}
}

func extensionSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func (extractor treeSitterExtractor) Name() string {
	return extractor.name
}

func (extractor treeSitterExtractor) Version() string {
	return extractor.version
}

func (extractor treeSitterExtractor) Supports(relative string) bool {
	_, ok := extractor.extensions[strings.ToLower(path.Ext(relative))]
	return ok
}

func (extractor treeSitterExtractor) Validate() error {
	if extractor.name == "" || extractor.version == "" || extractor.languageFunc == nil || strings.TrimSpace(extractor.query) == "" {
		return fmt.Errorf("invalid tree-sitter extractor")
	}
	language := extractor.languageFunc()
	if language == nil {
		return fmt.Errorf("missing tree-sitter language")
	}
	query, err := tree_sitter.NewQuery(language, extractor.query)
	if err != nil {
		return fmt.Errorf("invalid tree-sitter query")
	}
	query.Close()

	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(language); err != nil {
		return fmt.Errorf("invalid tree-sitter language")
	}
	return nil
}

func (extractor treeSitterExtractor) Parse(ctx context.Context, relative string, content []byte) (ExtractorResult, error) {
	if !utf8.Valid(content) {
		return ExtractorResult{}, fmt.Errorf("invalid utf-8 content")
	}
	language := extractor.languageFunc()
	if language == nil {
		return ExtractorResult{}, fmt.Errorf("tree-sitter language unavailable")
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(language); err != nil {
		return ExtractorResult{}, fmt.Errorf("tree-sitter language unavailable")
	}
	tree := parser.ParseCtx(ctx, content, nil)
	if tree == nil {
		return ExtractorResult{}, fmt.Errorf("tree-sitter parse failed")
	}
	defer tree.Close()
	root := tree.RootNode()
	if root == nil || root.HasError() {
		return ExtractorResult{}, fmt.Errorf("tree-sitter parse error")
	}
	if err := runTreeSitterQuery(language, extractor.query, root, content); err != nil {
		return ExtractorResult{}, err
	}
	var symbols []Symbol
	if extractor.name == string(ExtractorTreeSitterCSharp) {
		symbols = extractCSharpSymbols(root, content)
	} else {
		symbols = extractJavaScriptFamilySymbols(root, content)
	}
	return ExtractorResult{Symbols: dedupeSymbols(symbols)}, nil
}

func runTreeSitterQuery(language *tree_sitter.Language, querySource string, root *tree_sitter.Node, content []byte) error {
	query, queryErr := tree_sitter.NewQuery(language, querySource)
	if queryErr != nil {
		return fmt.Errorf("tree-sitter query unavailable")
	}
	defer query.Close()
	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	matches := cursor.Matches(query, root, content)
	for matches.Next() != nil {
		break
	}
	return nil
}

func extractJavaScriptFamilySymbols(root *tree_sitter.Node, content []byte) []Symbol {
	if root == nil {
		return nil
	}
	cursor := root.Walk()
	defer cursor.Close()
	var symbols []Symbol
	var visit func(node *tree_sitter.Node)
	visit = func(node *tree_sitter.Node) {
		if node == nil {
			return
		}
		switch node.Kind() {
		case "import_statement":
			if symbol, ok := importSymbolFromNode(node, content); ok {
				symbols = append(symbols, symbol)
			}
		case "function_declaration", "generator_function_declaration":
			if symbol, ok := namedSymbolFromNode(node, content, SymbolKindFunction); ok {
				symbols = append(symbols, symbol)
			}
		case "class_declaration":
			if symbol, ok := namedSymbolFromNode(node, content, SymbolKindClass); ok {
				symbols = append(symbols, symbol)
			}
		case "interface_declaration", "type_alias_declaration", "enum_declaration":
			if symbol, ok := namedSymbolFromNode(node, content, SymbolKindType); ok {
				symbols = append(symbols, symbol)
			}
		case "variable_declarator":
			if symbol, ok := functionVariableSymbolFromNode(node, content); ok {
				symbols = append(symbols, symbol)
			}
		}
		childCursor := node.Walk()
		defer childCursor.Close()
		for _, child := range node.NamedChildren(childCursor) {
			child := child
			visit(&child)
		}
	}
	visit(root)
	return symbols
}

func namedSymbolFromNode(node *tree_sitter.Node, content []byte, kind SymbolKind) (Symbol, bool) {
	name := nameTextFromNode(node, content)
	if name == "" {
		return Symbol{}, false
	}
	return Symbol{
		Kind:      kind,
		Name:      name,
		StartLine: int(node.StartPosition().Row) + 1,
		EndLine:   int(node.EndPosition().Row) + 1,
	}, true
}

func extractCSharpSymbols(root *tree_sitter.Node, content []byte) []Symbol {
	if root == nil {
		return nil
	}
	var symbols []Symbol
	var visit func(node *tree_sitter.Node)
	visit = func(node *tree_sitter.Node) {
		if node == nil {
			return
		}
		switch node.Kind() {
		case "namespace_declaration", "file_scoped_namespace_declaration":
			if symbol, ok := namedSymbolFromNode(node, content, SymbolKindPackage); ok {
				symbols = append(symbols, symbol)
			}
		case "using_directive":
			if symbol, ok := csharpImportSymbolFromNode(node, content); ok {
				symbols = append(symbols, symbol)
			}
		case "class_declaration":
			if symbol, ok := namedSymbolFromNode(node, content, SymbolKindClass); ok {
				symbols = append(symbols, symbol)
			}
		case "interface_declaration", "struct_declaration", "record_declaration", "record_struct_declaration", "enum_declaration":
			if symbol, ok := namedSymbolFromNode(node, content, SymbolKindType); ok {
				symbols = append(symbols, symbol)
			}
		case "method_declaration", "constructor_declaration", "property_declaration":
			if symbol, ok := namedSymbolFromNode(node, content, SymbolKindMethod); ok {
				symbols = append(symbols, symbol)
			}
		}
		childCursor := node.Walk()
		defer childCursor.Close()
		for _, child := range node.NamedChildren(childCursor) {
			child := child
			visit(&child)
		}
	}
	visit(root)
	return symbols
}

func csharpImportSymbolFromNode(node *tree_sitter.Node, content []byte) (Symbol, bool) {
	name := nameTextFromNode(node, content)
	if name == "" {
		return Symbol{}, false
	}
	return Symbol{
		Kind:       SymbolKindImport,
		Name:       name,
		ImportPath: name,
		StartLine:  int(node.StartPosition().Row) + 1,
		EndLine:    int(node.EndPosition().Row) + 1,
	}, true
}

func nameTextFromNode(node *tree_sitter.Node, content []byte) string {
	nameNode := node.ChildByFieldName("name")
	if nameNode != nil {
		return strings.TrimSpace(nameNode.Utf8Text(content))
	}
	cursor := node.Walk()
	defer cursor.Close()
	for _, child := range node.NamedChildren(cursor) {
		switch child.Kind() {
		case "identifier", "type_identifier", "qualified_name", "generic_name":
			return strings.TrimSpace(child.Utf8Text(content))
		}
	}
	return ""
}

func importSymbolFromNode(node *tree_sitter.Node, content []byte) (Symbol, bool) {
	sourceNode := node.ChildByFieldName("source")
	if sourceNode == nil {
		return Symbol{}, false
	}
	importPath := trimTreeSitterString(sourceNode.Utf8Text(content))
	if importPath == "" {
		return Symbol{}, false
	}
	return Symbol{
		Kind:       SymbolKindImport,
		Name:       importPath,
		ImportPath: importPath,
		StartLine:  int(node.StartPosition().Row) + 1,
		EndLine:    int(node.EndPosition().Row) + 1,
	}, true
}

func functionVariableSymbolFromNode(node *tree_sitter.Node, content []byte) (Symbol, bool) {
	valueNode := node.ChildByFieldName("value")
	if valueNode == nil {
		return Symbol{}, false
	}
	switch valueNode.Kind() {
	case "arrow_function", "function_expression":
	default:
		return Symbol{}, false
	}
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return Symbol{}, false
	}
	name := strings.TrimSpace(nameNode.Utf8Text(content))
	if name == "" {
		return Symbol{}, false
	}
	return Symbol{
		Kind:      SymbolKindFunction,
		Name:      name,
		StartLine: int(node.StartPosition().Row) + 1,
		EndLine:   int(node.EndPosition().Row) + 1,
	}, true
}

func trimTreeSitterString(value string) string {
	return strings.Trim(strings.TrimSpace(value), "`\"'")
}

func dedupeSymbols(symbols []Symbol) []Symbol {
	seen := make(map[string]struct{}, len(symbols))
	deduped := make([]Symbol, 0, len(symbols))
	for _, symbol := range symbols {
		key := string(symbol.Kind) + "\x00" + symbol.Name + "\x00" + symbol.ImportPath + "\x00" + fmt.Sprint(symbol.StartLine)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, symbol)
	}
	return deduped
}
