package projectingestion

import (
	"context"
	_ "embed"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	tree_sitter_dart "github.com/UserNobody14/tree-sitter-dart/bindings/go"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_c_sharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

const (
	ExtractorTreeSitterJavaScript ExtractorName = "treesitter-javascript"
	ExtractorTreeSitterTypeScript ExtractorName = "treesitter-typescript"
	ExtractorTreeSitterTSX        ExtractorName = "treesitter-tsx"
	ExtractorTreeSitterCSharp     ExtractorName = "treesitter-csharp"
	ExtractorTreeSitterPython     ExtractorName = "treesitter-python"
	ExtractorTreeSitterDart       ExtractorName = "treesitter-dart"
)

//go:embed queries/javascript.scm
var javascriptQuery string

//go:embed queries/typescript.scm
var typescriptQuery string

//go:embed queries/tsx.scm
var tsxQuery string

//go:embed queries/csharp.scm
var csharpQuery string

//go:embed queries/python.scm
var pythonQuery string

//go:embed queries/dart.scm
var dartQuery string

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
		version:    extractorVersionTwo,
		extensions: extensionSet(".cs"),
		query:      csharpQuery,
		languageFunc: func() *tree_sitter.Language {
			return tree_sitter.NewLanguage(tree_sitter_c_sharp.Language())
		},
	}
}

func newTreeSitterPythonExtractor() Extractor {
	return treeSitterExtractor{
		name:       string(ExtractorTreeSitterPython),
		version:    extractorVersionTwo,
		extensions: extensionSet(".py", ".pyw"),
		query:      pythonQuery,
		languageFunc: func() *tree_sitter.Language {
			return tree_sitter.NewLanguage(tree_sitter_python.Language())
		},
	}
}

func newTreeSitterDartExtractor() Extractor {
	return treeSitterExtractor{
		name:       string(ExtractorTreeSitterDart),
		version:    extractorVersionOne,
		extensions: extensionSet(".dart"),
		query:      dartQuery,
		languageFunc: func() *tree_sitter.Language {
			return tree_sitter.NewLanguage(tree_sitter_dart.Language())
		},
	}
}

func newTreeSitterTypeScriptExtractor() Extractor {
	return treeSitterExtractor{
		name:       string(ExtractorTreeSitterTypeScript),
		version:    extractorVersionTwo,
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
		version:    extractorVersionTwo,
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
	var references []Reference
	var calls []Call
	var implementations []Implementation
	if extractor.name == string(ExtractorTreeSitterCSharp) {
		symbols = extractCSharpSymbols(root, content)
		references, calls = extractCSharpOccurrences(root, content)
		implementations = extractCSharpImplementations(root, content)
	} else if extractor.name == string(ExtractorTreeSitterPython) {
		symbols = extractPythonSymbols(root, content)
		references, calls = extractPythonOccurrences(root, content)
	} else if extractor.name == string(ExtractorTreeSitterDart) {
		symbols = extractDartSymbols(root, content)
		references, calls = extractDartOccurrences(root, content)
	} else {
		symbols = extractJavaScriptFamilySymbols(root, content)
		references, calls = extractJavaScriptFamilyOccurrences(root, content)
		implementations = extractJavaScriptFamilyImplementations(root, content)
	}
	return ExtractorResult{Symbols: dedupeSymbols(symbols), References: dedupeReferences(references), Calls: dedupeCalls(calls), Implementations: dedupeImplementations(implementations)}, nil
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
		case "method_definition":
			if symbol, ok := namedSymbolFromNode(node, content, SymbolKindMethod); ok {
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

func extractJavaScriptFamilyImplementations(root *tree_sitter.Node, content []byte) []Implementation {
	return extractClassImplementations(root, content, map[string]string{
		"extends_clause":    "extends",
		"implements_clause": "implements",
	})
}

func namedSymbolFromNode(node *tree_sitter.Node, content []byte, kind SymbolKind) (Symbol, bool) {
	name := nameTextFromNode(node, content)
	if name == "" {
		return Symbol{}, false
	}
	return Symbol{
		Kind:        kind,
		Name:        name,
		StartLine:   int(node.StartPosition().Row) + 1,
		EndLine:     int(node.EndPosition().Row) + 1,
		StartByte:   int(node.StartByte()),
		EndByte:     int(node.EndByte()),
		StartColumn: int(node.StartPosition().Column) + 1,
		EndColumn:   int(node.EndPosition().Column) + 1,
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

func extractCSharpImplementations(root *tree_sitter.Node, content []byte) []Implementation {
	return extractClassImplementations(root, content, map[string]string{
		"base_list": "implements",
	})
}

func csharpImportSymbolFromNode(node *tree_sitter.Node, content []byte) (Symbol, bool) {
	name := nameTextFromNode(node, content)
	if name == "" {
		return Symbol{}, false
	}
	return Symbol{
		Kind:        SymbolKindImport,
		Name:        name,
		ImportPath:  name,
		StartLine:   int(node.StartPosition().Row) + 1,
		EndLine:     int(node.EndPosition().Row) + 1,
		StartByte:   int(node.StartByte()),
		EndByte:     int(node.EndByte()),
		StartColumn: int(node.StartPosition().Column) + 1,
		EndColumn:   int(node.EndPosition().Column) + 1,
	}, true
}

func extractCSharpOccurrences(root *tree_sitter.Node, content []byte) ([]Reference, []Call) {
	if root == nil {
		return nil, nil
	}
	var references []Reference
	var calls []Call
	var visit func(node *tree_sitter.Node, enclosing string)
	visit = func(node *tree_sitter.Node, enclosing string) {
		if node == nil {
			return
		}
		current := enclosing
		switch node.Kind() {
		case "method_declaration", "constructor_declaration", "property_declaration":
			if name := nameTextFromNode(node, content); name != "" {
				current = name
			}
		case "invocation_expression":
			if current != "" {
				callee, receiver := csharpInvocationTarget(node, content)
				if callee != "" {
					calls = append(calls, Call{
						CallerName:       current,
						CalleeName:       callee,
						Receiver:         receiver,
						StartLine:        int(node.StartPosition().Row) + 1,
						EndLine:          int(node.EndPosition().Row) + 1,
						StartByte:        int(node.StartByte()),
						EndByte:          int(node.EndByte()),
						StartColumn:      int(node.StartPosition().Column) + 1,
						EndColumn:        int(node.EndPosition().Column) + 1,
						ResolutionStatus: "unresolved",
						Confidence:       "candidate",
					})
				}
			}
		case "identifier":
			if current != "" && csharpIdentifierReferenceNode(node) {
				name := strings.TrimSpace(node.Utf8Text(content))
				if name != "" {
					references = append(references, Reference{
						Kind:                "identifier",
						Name:                name,
						TargetName:          name,
						EnclosingSymbolName: current,
						StartLine:           int(node.StartPosition().Row) + 1,
						EndLine:             int(node.EndPosition().Row) + 1,
						StartByte:           int(node.StartByte()),
						EndByte:             int(node.EndByte()),
						StartColumn:         int(node.StartPosition().Column) + 1,
						EndColumn:           int(node.EndPosition().Column) + 1,
						ResolutionStatus:    "unresolved",
						Confidence:          "candidate",
					})
				}
			}
		}
		childCursor := node.Walk()
		defer childCursor.Close()
		for _, child := range node.NamedChildren(childCursor) {
			child := child
			visit(&child, current)
		}
	}
	visit(root, "")
	return references, calls
}

func csharpInvocationTarget(node *tree_sitter.Node, content []byte) (string, string) {
	value := strings.TrimSpace(node.Utf8Text(content))
	if paren := strings.Index(value, "("); paren >= 0 {
		value = strings.TrimSpace(value[:paren])
	}
	if value == "" {
		return "", ""
	}
	parts := strings.Split(value, ".")
	if len(parts) == 1 {
		return parts[0], ""
	}
	callee := strings.TrimSpace(parts[len(parts)-1])
	receiver := strings.TrimSpace(strings.Join(parts[:len(parts)-1], "."))
	if callee == "" {
		return "", ""
	}
	return callee, receiver
}

func csharpIdentifierReferenceNode(node *tree_sitter.Node) bool {
	parent := node.Parent()
	if parent == nil {
		return true
	}
	switch parent.Kind() {
	case "method_declaration", "constructor_declaration", "property_declaration", "class_declaration",
		"interface_declaration", "struct_declaration", "record_declaration", "enum_declaration",
		"namespace_declaration", "file_scoped_namespace_declaration", "using_directive", "parameter":
		return false
	default:
		return true
	}
}

func extractPythonSymbols(root *tree_sitter.Node, content []byte) []Symbol {
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
		case "import_statement", "import_from_statement":
			if symbol, ok := pythonImportSymbolFromNode(node, content); ok {
				symbols = append(symbols, symbol)
			}
		case "function_definition":
			if symbol, ok := namedSymbolFromNode(node, content, SymbolKindFunction); ok {
				symbols = append(symbols, symbol)
			}
		case "class_definition":
			if symbol, ok := namedSymbolFromNode(node, content, SymbolKindClass); ok {
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

func pythonImportSymbolFromNode(node *tree_sitter.Node, content []byte) (Symbol, bool) {
	value := strings.TrimSpace(node.Utf8Text(content))
	if value == "" {
		return Symbol{}, false
	}
	name := value
	importPath := value
	if strings.HasPrefix(value, "from ") {
		rest := strings.TrimSpace(strings.TrimPrefix(value, "from "))
		if idx := strings.Index(rest, " import "); idx >= 0 {
			importPath = strings.TrimSpace(rest[:idx])
			name = importPath
		}
	} else if strings.HasPrefix(value, "import ") {
		rest := strings.TrimSpace(strings.TrimPrefix(value, "import "))
		first := strings.Split(rest, ",")[0]
		first = strings.TrimSpace(first)
		if fields := strings.Fields(first); len(fields) > 0 {
			importPath = fields[0]
			name = fields[0]
		}
	}
	if name == "" {
		return Symbol{}, false
	}
	return Symbol{
		Kind:        SymbolKindImport,
		Name:        name,
		ImportPath:  importPath,
		StartLine:   int(node.StartPosition().Row) + 1,
		EndLine:     int(node.EndPosition().Row) + 1,
		StartByte:   int(node.StartByte()),
		EndByte:     int(node.EndByte()),
		StartColumn: int(node.StartPosition().Column) + 1,
		EndColumn:   int(node.EndPosition().Column) + 1,
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

func extractClassImplementations(root *tree_sitter.Node, content []byte, relationByKind map[string]string) []Implementation {
	if root == nil {
		return nil
	}
	var implementations []Implementation
	var visit func(node *tree_sitter.Node)
	visit = func(node *tree_sitter.Node) {
		if node == nil {
			return
		}
		if node.Kind() == "class_declaration" || node.Kind() == "record_declaration" || node.Kind() == "record_struct_declaration" || node.Kind() == "struct_declaration" {
			implementer := nameTextFromNode(node, content)
			if implementer != "" {
				implementations = append(implementations, implementationTargetsFromClass(node, content, implementer, relationByKind)...)
			}
		}
		cursor := node.Walk()
		defer cursor.Close()
		for _, child := range node.NamedChildren(cursor) {
			child := child
			visit(&child)
		}
	}
	visit(root)
	return implementations
}

func implementationTargetsFromClass(node *tree_sitter.Node, content []byte, implementer string, relationByKind map[string]string) []Implementation {
	var out []Implementation
	cursor := node.Walk()
	defer cursor.Close()
	for _, child := range node.NamedChildren(cursor) {
		relation := relationByKind[child.Kind()]
		if relation == "" {
			continue
		}
		targets := implementationTargetNames(&child, content, implementer)
		for _, target := range targets {
			out = append(out, Implementation{
				Kind:             relation,
				ImplementerName:  implementer,
				ImplementedName:  target,
				StartLine:        int(child.StartPosition().Row) + 1,
				EndLine:          int(child.EndPosition().Row) + 1,
				StartByte:        int(child.StartByte()),
				EndByte:          int(child.EndByte()),
				StartColumn:      int(child.StartPosition().Column) + 1,
				EndColumn:        int(child.EndPosition().Column) + 1,
				ResolutionStatus: "unresolved",
				Confidence:       "candidate",
			})
		}
	}
	if len(out) == 0 {
		out = append(out, implementationTargetsFromClassText(node, content, implementer, relationByKind)...)
	}
	return out
}

func implementationTargetsFromClassText(node *tree_sitter.Node, content []byte, implementer string, relationByKind map[string]string) []Implementation {
	text := node.Utf8Text(content)
	var out []Implementation
	for _, relation := range []string{"extends", "implements"} {
		if relationByKind[relation+"_clause"] == "" && !(relation == "implements" && relationByKind["base_list"] != "") {
			continue
		}
		segment := classRelationSegment(text, relation)
		for _, target := range implementationTargetNamesFromText(segment) {
			if target == "" || target == implementer {
				continue
			}
			out = append(out, Implementation{
				Kind:             relation,
				ImplementerName:  implementer,
				ImplementedName:  target,
				StartLine:        int(node.StartPosition().Row) + 1,
				EndLine:          int(node.EndPosition().Row) + 1,
				StartByte:        int(node.StartByte()),
				EndByte:          int(node.EndByte()),
				StartColumn:      int(node.StartPosition().Column) + 1,
				EndColumn:        int(node.EndPosition().Column) + 1,
				ResolutionStatus: "unresolved",
				Confidence:       "candidate",
			})
		}
	}
	return out
}

func classRelationSegment(text string, relation string) string {
	needle := " " + relation + " "
	index := strings.Index(text, needle)
	if index < 0 {
		return ""
	}
	segment := text[index+len(needle):]
	for _, terminator := range []string{" implements ", " extends ", "{"} {
		if terminator == needle {
			continue
		}
		if end := strings.Index(segment, terminator); end >= 0 {
			segment = segment[:end]
		}
	}
	return segment
}

func implementationTargetNamesFromText(value string) []string {
	parts := strings.Split(value, ",")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		name := implementationTargetName(part)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func implementationTargetNames(node *tree_sitter.Node, content []byte, implementer string) []string {
	seen := make(map[string]struct{})
	var names []string
	var visit func(*tree_sitter.Node)
	visit = func(current *tree_sitter.Node) {
		if current == nil {
			return
		}
		switch current.Kind() {
		case "identifier", "type_identifier", "qualified_name", "generic_name":
			name := implementationTargetName(current.Utf8Text(content))
			if name != "" && name != implementer {
				if _, ok := seen[name]; !ok {
					seen[name] = struct{}{}
					names = append(names, name)
				}
			}
		}
		cursor := current.Walk()
		defer cursor.Close()
		for _, child := range current.NamedChildren(cursor) {
			child := child
			visit(&child)
		}
	}
	visit(node)
	return names
}

func implementationTargetName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, "<"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	if idx := strings.LastIndex(value, "."); idx >= 0 {
		value = strings.TrimSpace(value[idx+1:])
	}
	return value
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
		Kind:        SymbolKindImport,
		Name:        importPath,
		ImportPath:  importPath,
		StartLine:   int(node.StartPosition().Row) + 1,
		EndLine:     int(node.EndPosition().Row) + 1,
		StartByte:   int(node.StartByte()),
		EndByte:     int(node.EndByte()),
		StartColumn: int(node.StartPosition().Column) + 1,
		EndColumn:   int(node.EndPosition().Column) + 1,
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
		Kind:        SymbolKindFunction,
		Name:        name,
		StartLine:   int(node.StartPosition().Row) + 1,
		EndLine:     int(node.EndPosition().Row) + 1,
		StartByte:   int(node.StartByte()),
		EndByte:     int(node.EndByte()),
		StartColumn: int(node.StartPosition().Column) + 1,
		EndColumn:   int(node.EndPosition().Column) + 1,
	}, true
}

func extractJavaScriptFamilyOccurrences(root *tree_sitter.Node, content []byte) ([]Reference, []Call) {
	if root == nil {
		return nil, nil
	}
	var references []Reference
	var calls []Call
	var visit func(node *tree_sitter.Node, enclosing string)
	visit = func(node *tree_sitter.Node, enclosing string) {
		if node == nil {
			return
		}
		current := enclosing
		switch node.Kind() {
		case "function_declaration", "generator_function_declaration", "method_definition":
			if name := nameTextFromNode(node, content); name != "" {
				current = name
			}
		case "variable_declarator":
			if _, ok := functionVariableSymbolFromNode(node, content); ok {
				if name := nameTextFromNode(node, content); name != "" {
					current = name
				}
			}
		case "call_expression":
			if current != "" {
				if functionNode := node.ChildByFieldName("function"); functionNode != nil {
					callee, receiver := javascriptCallTarget(functionNode, content)
					if callee != "" {
						calls = append(calls, Call{
							CallerName:       current,
							CalleeName:       callee,
							Receiver:         receiver,
							StartLine:        int(node.StartPosition().Row) + 1,
							EndLine:          int(node.EndPosition().Row) + 1,
							StartByte:        int(node.StartByte()),
							EndByte:          int(node.EndByte()),
							StartColumn:      int(node.StartPosition().Column) + 1,
							EndColumn:        int(node.EndPosition().Column) + 1,
							ResolutionStatus: "unresolved",
							Confidence:       "candidate",
						})
					}
				}
			}
		case "identifier", "property_identifier":
			if current != "" && javascriptIdentifierReferenceNode(node) {
				name := strings.TrimSpace(node.Utf8Text(content))
				if name != "" {
					references = append(references, Reference{
						Kind:                node.Kind(),
						Name:                name,
						TargetName:          name,
						EnclosingSymbolName: current,
						StartLine:           int(node.StartPosition().Row) + 1,
						EndLine:             int(node.EndPosition().Row) + 1,
						StartByte:           int(node.StartByte()),
						EndByte:             int(node.EndByte()),
						StartColumn:         int(node.StartPosition().Column) + 1,
						EndColumn:           int(node.EndPosition().Column) + 1,
						ResolutionStatus:    "unresolved",
						Confidence:          "candidate",
					})
				}
			}
		}
		childCursor := node.Walk()
		defer childCursor.Close()
		for _, child := range node.NamedChildren(childCursor) {
			child := child
			visit(&child, current)
		}
	}
	visit(root, "")
	return references, calls
}

func javascriptCallTarget(node *tree_sitter.Node, content []byte) (string, string) {
	value := strings.TrimSpace(node.Utf8Text(content))
	if value == "" {
		return "", ""
	}
	value = strings.TrimPrefix(value, "await ")
	parts := strings.Split(value, ".")
	if len(parts) == 1 {
		return parts[0], ""
	}
	callee := strings.TrimSpace(parts[len(parts)-1])
	receiver := strings.TrimSpace(strings.Join(parts[:len(parts)-1], "."))
	if callee == "" {
		return "", ""
	}
	return callee, receiver
}

func javascriptIdentifierReferenceNode(node *tree_sitter.Node) bool {
	parent := node.Parent()
	if parent == nil {
		return true
	}
	switch parent.Kind() {
	case "function_declaration", "generator_function_declaration", "method_definition", "class_declaration",
		"interface_declaration", "type_alias_declaration", "enum_declaration", "import_statement", "formal_parameters":
		return false
	case "variable_declarator":
		nameNode := parent.ChildByFieldName("name")
		return nameNode == nil || nameNode.StartByte() != node.StartByte() || nameNode.EndByte() != node.EndByte()
	default:
		return true
	}
}

func extractPythonOccurrences(root *tree_sitter.Node, content []byte) ([]Reference, []Call) {
	if root == nil {
		return nil, nil
	}
	var references []Reference
	var calls []Call
	var visit func(node *tree_sitter.Node, enclosing string)
	visit = func(node *tree_sitter.Node, enclosing string) {
		if node == nil {
			return
		}
		current := enclosing
		switch node.Kind() {
		case "function_definition":
			if name := nameTextFromNode(node, content); name != "" {
				current = name
			}
		case "call":
			if current != "" {
				if functionNode := node.ChildByFieldName("function"); functionNode != nil {
					callee, receiver := pythonCallTarget(functionNode, content)
					if callee != "" {
						calls = append(calls, Call{
							CallerName:       current,
							CalleeName:       callee,
							Receiver:         receiver,
							StartLine:        int(node.StartPosition().Row) + 1,
							EndLine:          int(node.EndPosition().Row) + 1,
							StartByte:        int(node.StartByte()),
							EndByte:          int(node.EndByte()),
							StartColumn:      int(node.StartPosition().Column) + 1,
							EndColumn:        int(node.EndPosition().Column) + 1,
							ResolutionStatus: "unresolved",
							Confidence:       "candidate",
						})
					}
				}
			}
		case "identifier":
			if current != "" && pythonIdentifierReferenceNode(node) {
				name := strings.TrimSpace(node.Utf8Text(content))
				references = append(references, Reference{
					Kind:                "identifier",
					Name:                name,
					TargetName:          name,
					EnclosingSymbolName: current,
					StartLine:           int(node.StartPosition().Row) + 1,
					EndLine:             int(node.EndPosition().Row) + 1,
					StartByte:           int(node.StartByte()),
					EndByte:             int(node.EndByte()),
					StartColumn:         int(node.StartPosition().Column) + 1,
					EndColumn:           int(node.EndPosition().Column) + 1,
					ResolutionStatus:    "unresolved",
					Confidence:          "candidate",
				})
			}
		}
		childCursor := node.Walk()
		defer childCursor.Close()
		for _, child := range node.NamedChildren(childCursor) {
			child := child
			visit(&child, current)
		}
	}
	visit(root, "")
	return references, calls
}

func pythonCallTarget(node *tree_sitter.Node, content []byte) (string, string) {
	value := strings.TrimSpace(node.Utf8Text(content))
	if value == "" {
		return "", ""
	}
	parts := strings.Split(value, ".")
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[len(parts)-1], strings.Join(parts[:len(parts)-1], ".")
}

func pythonIdentifierReferenceNode(node *tree_sitter.Node) bool {
	parent := node.Parent()
	if parent == nil {
		return true
	}
	switch parent.Kind() {
	case "function_definition", "class_definition", "import_statement", "import_from_statement", "parameters":
		return false
	default:
		return true
	}
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
