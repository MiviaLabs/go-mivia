package projectingestion

import (
	"regexp"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

var dartCallPattern = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s*\(`)

func extractDartSymbols(root *tree_sitter.Node, content []byte) []Symbol {
	if root == nil {
		return nil
	}
	var symbols []Symbol
	var visit func(node *tree_sitter.Node, className string, classFlutterKind SymbolKind)
	visit = func(node *tree_sitter.Node, className string, classFlutterKind SymbolKind) {
		if node == nil {
			return
		}
		currentClass := className
		currentFlutterKind := classFlutterKind
		switch node.Kind() {
		case "import_or_export":
			if symbol, ok := dartImportOrExportSymbolFromNode(node, content); ok {
				symbols = append(symbols, symbol)
			}
		case "class_definition":
			if symbol, ok := namedSymbolFromNode(node, content, SymbolKindClass); ok {
				symbols = append(symbols, symbol)
				currentClass = symbol.Name
				currentFlutterKind = dartFlutterClassKind(node, content)
				if currentFlutterKind != "" {
					widgetSymbol := symbol
					widgetSymbol.Kind = currentFlutterKind
					symbols = append(symbols, widgetSymbol)
				}
			}
		case "mixin_declaration", "extension_declaration", "extension_type_declaration", "enum_declaration", "type_alias":
			if symbol, ok := namedSymbolFromNode(node, content, SymbolKindType); ok {
				symbols = append(symbols, symbol)
			}
		case "method_signature":
			if symbol, ok := dartMethodSymbolFromNode(node, content, currentClass); ok {
				symbols = append(symbols, symbol)
				if symbol.Name == "build" && currentFlutterKind != "" && strings.Contains(node.Utf8Text(content), "Widget") {
					buildSymbol := symbol
					buildSymbol.Kind = SymbolKindFlutterBuildMethod
					symbols = append(symbols, buildSymbol)
				}
			}
		case "declaration":
			if symbol, ok := dartConstructorDeclarationSymbolFromNode(node, content, currentClass); ok {
				symbols = append(symbols, symbol)
			}
		case "function_signature":
			if parent := node.Parent(); parent == nil || parent.Kind() != "method_signature" {
				if symbol, ok := namedSymbolFromNode(node, content, SymbolKindFunction); ok {
					symbols = append(symbols, symbol)
				}
			}
		}
		cursor := node.Walk()
		defer cursor.Close()
		for _, child := range node.NamedChildren(cursor) {
			child := child
			visit(&child, currentClass, currentFlutterKind)
		}
	}
	visit(root, "", "")
	return symbols
}

func dartConstructorDeclarationSymbolFromNode(node *tree_sitter.Node, content []byte, receiver string) (Symbol, bool) {
	if receiver == "" || !dartNodeContainsKind(node, "constructor_signature", "constant_constructor_signature", "factory_constructor_signature") {
		return Symbol{}, false
	}
	return Symbol{
		Kind:        SymbolKindMethod,
		Name:        receiver,
		Receiver:    receiver,
		StartLine:   int(node.StartPosition().Row) + 1,
		EndLine:     int(node.EndPosition().Row) + 1,
		StartByte:   int(node.StartByte()),
		EndByte:     int(node.EndByte()),
		StartColumn: int(node.StartPosition().Column) + 1,
		EndColumn:   int(node.EndPosition().Column) + 1,
	}, true
}

func dartNodeContainsKind(node *tree_sitter.Node, kinds ...string) bool {
	if node == nil {
		return false
	}
	for _, kind := range kinds {
		if node.Kind() == kind {
			return true
		}
	}
	cursor := node.Walk()
	defer cursor.Close()
	for _, child := range node.NamedChildren(cursor) {
		child := child
		if dartNodeContainsKind(&child, kinds...) {
			return true
		}
	}
	return false
}

func dartImportOrExportSymbolFromNode(node *tree_sitter.Node, content []byte) (Symbol, bool) {
	value := strings.TrimSpace(node.Utf8Text(content))
	if value == "" {
		return Symbol{}, false
	}
	kind := SymbolKindImport
	if strings.HasPrefix(value, "export ") {
		kind = SymbolKindExport
	}
	importPath := firstDartStringLiteral(node, content)
	if importPath == "" {
		return Symbol{}, false
	}
	return Symbol{
		Kind:        kind,
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

func firstDartStringLiteral(node *tree_sitter.Node, content []byte) string {
	var out string
	var visit func(candidate *tree_sitter.Node)
	visit = func(candidate *tree_sitter.Node) {
		if candidate == nil || out != "" {
			return
		}
		if candidate.Kind() == "string_literal" {
			out = trimTreeSitterString(candidate.Utf8Text(content))
			return
		}
		cursor := candidate.Walk()
		defer cursor.Close()
		for _, child := range candidate.NamedChildren(cursor) {
			child := child
			visit(&child)
		}
	}
	visit(node)
	return out
}

func dartMethodSymbolFromNode(node *tree_sitter.Node, content []byte, receiver string) (Symbol, bool) {
	name := dartNameFromNode(node, content)
	if name == "" {
		name = dartConstructorName(node, content, receiver)
	}
	if name == "" {
		return Symbol{}, false
	}
	return Symbol{
		Kind:        SymbolKindMethod,
		Name:        name,
		Receiver:    receiver,
		StartLine:   int(node.StartPosition().Row) + 1,
		EndLine:     int(node.EndPosition().Row) + 1,
		StartByte:   int(node.StartByte()),
		EndByte:     int(node.EndByte()),
		StartColumn: int(node.StartPosition().Column) + 1,
		EndColumn:   int(node.EndPosition().Column) + 1,
	}, true
}

func dartNameFromNode(node *tree_sitter.Node, content []byte) string {
	if name := nameTextFromNode(node, content); name != "" {
		return name
	}
	var out string
	var visit func(candidate *tree_sitter.Node)
	visit = func(candidate *tree_sitter.Node) {
		if candidate == nil || out != "" {
			return
		}
		if name := nameTextFromNode(candidate, content); name != "" {
			out = name
			return
		}
		cursor := candidate.Walk()
		defer cursor.Close()
		for _, child := range candidate.NamedChildren(cursor) {
			child := child
			visit(&child)
		}
	}
	visit(node)
	return out
}

func dartConstructorName(node *tree_sitter.Node, content []byte, receiver string) string {
	if receiver == "" {
		return ""
	}
	var found string
	var visit func(candidate *tree_sitter.Node)
	visit = func(candidate *tree_sitter.Node) {
		if candidate == nil || found != "" {
			return
		}
		switch candidate.Kind() {
		case "constructor_signature", "constant_constructor_signature", "factory_constructor_signature":
			value := strings.TrimSpace(candidate.Utf8Text(content))
			if strings.Contains(value, receiver) {
				found = receiver
			}
			return
		}
		cursor := candidate.Walk()
		defer cursor.Close()
		for _, child := range candidate.NamedChildren(cursor) {
			child := child
			visit(&child)
		}
	}
	visit(node)
	return found
}

func dartFlutterClassKind(node *tree_sitter.Node, content []byte) SymbolKind {
	superclass := node.ChildByFieldName("superclass")
	if superclass == nil {
		return ""
	}
	value := strings.TrimSpace(superclass.Utf8Text(content))
	switch {
	case strings.Contains(value, "StatelessWidget"), strings.Contains(value, "StatefulWidget"):
		return SymbolKindFlutterWidget
	case strings.Contains(value, "State<"):
		return SymbolKindFlutterState
	default:
		return ""
	}
}

func extractDartOccurrences(root *tree_sitter.Node, content []byte) ([]Reference, []Call) {
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
		switch node.Kind() {
		case "program", "class_body", "extension_body":
			current := enclosing
			cursor := node.Walk()
			defer cursor.Close()
			for _, child := range node.NamedChildren(cursor) {
				child := child
				switch child.Kind() {
				case "function_signature", "method_signature":
					if name := dartNameFromNode(&child, content); name != "" {
						current = name
					}
					visit(&child, current)
				case "function_body":
					visit(&child, current)
				default:
					visit(&child, enclosing)
				}
			}
			return
		}
		current := enclosing
		switch node.Kind() {
		case "function_signature", "method_signature":
			if name := dartNameFromNode(node, content); name != "" {
				current = name
			}
		case "function_body":
			if current != "" {
				calls = append(calls, dartCallsFromBody(node, content, current)...)
			}
		case "identifier", "type_identifier":
			if current != "" && dartIdentifierReferenceNode(node) {
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
		cursor := node.Walk()
		defer cursor.Close()
		for _, child := range node.NamedChildren(cursor) {
			child := child
			visit(&child, current)
		}
	}
	visit(root, "")
	return references, calls
}

func dartCallsFromBody(node *tree_sitter.Node, content []byte, caller string) []Call {
	body := node.Utf8Text(content)
	offset := int(node.StartByte())
	matches := dartCallPattern.FindAllStringSubmatchIndex(body, -1)
	out := make([]Call, 0, len(matches))
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		target := body[match[2]:match[3]]
		callee, receiver := splitDartCallTarget(target)
		if callee == "" || dartCallKeyword(callee) {
			continue
		}
		startByte := offset + match[2]
		endByte := offset + match[3]
		startLine, startColumn := lineColumnForOffset(content, startByte)
		endLine, endColumn := lineColumnForOffset(content, endByte)
		out = append(out, Call{
			CallerName:       caller,
			CalleeName:       callee,
			Receiver:         receiver,
			StartLine:        startLine,
			EndLine:          endLine,
			StartByte:        startByte,
			EndByte:          endByte,
			StartColumn:      startColumn,
			EndColumn:        endColumn,
			ResolutionStatus: "unresolved",
			Confidence:       "candidate",
		})
	}
	return out
}

func splitDartCallTarget(target string) (string, string) {
	target = strings.TrimSpace(target)
	parts := strings.Split(target, ".")
	callee := strings.TrimSpace(parts[len(parts)-1])
	if len(parts) == 1 {
		return callee, ""
	}
	return callee, strings.TrimSpace(strings.Join(parts[:len(parts)-1], "."))
}

func dartCallKeyword(name string) bool {
	switch name {
	case "if", "for", "while", "switch", "catch", "return", "throw", "new", "const", "super":
		return true
	default:
		return false
	}
}

func dartIdentifierReferenceNode(node *tree_sitter.Node) bool {
	parent := node.Parent()
	if parent == nil {
		return true
	}
	switch parent.Kind() {
	case "class_definition", "mixin_declaration", "extension_declaration", "extension_type_declaration",
		"enum_declaration", "enum_constant", "type_alias", "function_signature", "getter_signature",
		"setter_signature", "constructor_signature", "constant_constructor_signature", "factory_constructor_signature",
		"formal_parameter", "typed_identifier", "import_or_export", "library_import", "library_export",
		"configurable_uri", "uri", "annotation":
		return false
	default:
		return true
	}
}

func lineColumnForOffset(content []byte, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(content) {
		offset = len(content)
	}
	line := 1
	column := 1
	for i := 0; i < offset; i++ {
		if content[i] == '\n' {
			line++
			column = 1
			continue
		}
		column++
	}
	return line, column
}
