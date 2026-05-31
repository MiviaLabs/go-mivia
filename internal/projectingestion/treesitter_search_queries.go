package projectingestion

import "strings"

const astSearchQueryVersion = "1"

type astSearchQuery struct {
	ID         string
	Language   string
	Version    string
	Query      string
	Captures   []string
	Extensions []string
}

func astSearchCatalogEntry(language string, queryID string) (astSearchQuery, bool) {
	language = normalizeASTLanguage(language)
	queryID = strings.TrimSpace(queryID)
	for _, entry := range astSearchCatalog {
		if entry.Language == language && entry.ID == queryID {
			return entry, true
		}
	}
	return astSearchQuery{}, false
}

func astSearchCatalogMetadata() []ASTQueryMetadata {
	out := make([]ASTQueryMetadata, 0, len(astSearchCatalog))
	for _, entry := range astSearchCatalog {
		out = append(out, astSearchQueryMetadata(entry))
	}
	return out
}

func astSearchQueryMetadata(entry astSearchQuery) ASTQueryMetadata {
	return ASTQueryMetadata{
		ID:         entry.ID,
		Language:   entry.Language,
		Version:    entry.Version,
		Captures:   append([]string(nil), entry.Captures...),
		Extensions: append([]string(nil), entry.Extensions...),
	}
}

func astSearchLanguageSupported(language string) bool {
	_, ok := astSearchLanguageExtensions[normalizeASTLanguage(language)]
	return ok
}

func normalizeASTLanguage(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "js":
		return "javascript"
	case "ts":
		return "typescript"
	case "cs", "c#":
		return "csharp"
	default:
		return strings.ToLower(strings.TrimSpace(language))
	}
}

func validASTQueryID(queryID string) bool {
	for _, r := range queryID {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func validASTCaptureName(name string) bool {
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

var astSearchLanguageExtensions = map[string][]string{
	"go":         {".go"},
	"python":     {".py", ".pyw"},
	"javascript": {".js", ".mjs", ".cjs"},
	"jsx":        {".jsx"},
	"typescript": {".ts", ".mts", ".cts"},
	"tsx":        {".tsx"},
	"csharp":     {".cs"},
	"dart":       {".dart"},
}

var astSearchCatalog = []astSearchQuery{
	{ID: "function_declarations", Language: "go", Version: astSearchQueryVersion, Query: `[(function_declaration name: (identifier) @name) (method_declaration name: (field_identifier) @name)] @definition`, Captures: []string{"definition", "name"}, Extensions: astSearchLanguageExtensions["go"]},
	{ID: "type_declarations", Language: "go", Version: astSearchQueryVersion, Query: `(type_declaration (type_spec name: (type_identifier) @name)) @type`, Captures: []string{"type", "name"}, Extensions: astSearchLanguageExtensions["go"]},
	{ID: "call_expressions", Language: "go", Version: astSearchQueryVersion, Query: `(call_expression function: [(identifier) @callee (selector_expression field: (field_identifier) @callee)]) @call`, Captures: []string{"call", "callee"}, Extensions: astSearchLanguageExtensions["go"]},
	{ID: "imports", Language: "go", Version: astSearchQueryVersion, Query: `(import_spec path: (interpreted_string_literal) @path) @import`, Captures: []string{"import", "path"}, Extensions: astSearchLanguageExtensions["go"]},
	{ID: "assignments", Language: "go", Version: astSearchQueryVersion, Query: `[(assignment_statement) (short_var_declaration) (var_declaration)] @assignment`, Captures: []string{"assignment"}, Extensions: astSearchLanguageExtensions["go"]},

	{ID: "function_declarations", Language: "python", Version: astSearchQueryVersion, Query: `(function_definition name: (identifier) @name) @definition`, Captures: []string{"definition", "name"}, Extensions: astSearchLanguageExtensions["python"]},
	{ID: "class_declarations", Language: "python", Version: astSearchQueryVersion, Query: `(class_definition name: (identifier) @name) @class`, Captures: []string{"class", "name"}, Extensions: astSearchLanguageExtensions["python"]},
	{ID: "call_expressions", Language: "python", Version: astSearchQueryVersion, Query: `(call function: [(identifier) @callee (attribute attribute: (identifier) @callee)]) @call`, Captures: []string{"call", "callee"}, Extensions: astSearchLanguageExtensions["python"]},
	{ID: "imports", Language: "python", Version: astSearchQueryVersion, Query: `[(import_statement) (import_from_statement)] @import`, Captures: []string{"import"}, Extensions: astSearchLanguageExtensions["python"]},
	{ID: "test_functions", Language: "python", Version: astSearchQueryVersion, Query: `(function_definition name: (identifier) @name (#match? @name "^test_")) @test`, Captures: []string{"test", "name"}, Extensions: astSearchLanguageExtensions["python"]},
	{ID: "assignments", Language: "python", Version: astSearchQueryVersion, Query: `[(assignment) (augmented_assignment)] @assignment`, Captures: []string{"assignment"}, Extensions: astSearchLanguageExtensions["python"]},
	{ID: "error_handling", Language: "python", Version: astSearchQueryVersion, Query: `[(try_statement) (except_clause) (raise_statement)] @error`, Captures: []string{"error"}, Extensions: astSearchLanguageExtensions["python"]},

	{ID: "function_declarations", Language: "javascript", Version: astSearchQueryVersion, Query: `[(function_declaration name: (identifier) @name) (method_definition name: (property_identifier) @name) (variable_declarator name: (identifier) @name value: [(arrow_function) (function_expression)])] @definition`, Captures: []string{"definition", "name"}, Extensions: astSearchLanguageExtensions["javascript"]},
	{ID: "class_declarations", Language: "javascript", Version: astSearchQueryVersion, Query: `(class_declaration name: (identifier) @name) @class`, Captures: []string{"class", "name"}, Extensions: astSearchLanguageExtensions["javascript"]},
	{ID: "call_expressions", Language: "javascript", Version: astSearchQueryVersion, Query: `(call_expression function: [(identifier) @callee (member_expression property: (property_identifier) @callee)]) @call`, Captures: []string{"call", "callee"}, Extensions: astSearchLanguageExtensions["javascript"]},
	{ID: "imports", Language: "javascript", Version: astSearchQueryVersion, Query: `[(import_statement) (call_expression function: (identifier) @callee arguments: (arguments (string) @path) (#eq? @callee "require"))] @import`, Captures: []string{"import", "callee", "path"}, Extensions: astSearchLanguageExtensions["javascript"]},
	{ID: "test_functions", Language: "javascript", Version: astSearchQueryVersion, Query: `(call_expression function: (identifier) @test_name arguments: (arguments) (#match? @test_name "^(it|test|describe)$")) @test`, Captures: []string{"test", "test_name"}, Extensions: astSearchLanguageExtensions["javascript"]},
	{ID: "assignments", Language: "javascript", Version: astSearchQueryVersion, Query: `[(assignment_expression) (variable_declarator)] @assignment`, Captures: []string{"assignment"}, Extensions: astSearchLanguageExtensions["javascript"]},
	{ID: "error_handling", Language: "javascript", Version: astSearchQueryVersion, Query: `[(try_statement) (catch_clause) (throw_statement)] @error`, Captures: []string{"error"}, Extensions: astSearchLanguageExtensions["javascript"]},

	{ID: "function_declarations", Language: "typescript", Version: astSearchQueryVersion, Query: `[(function_declaration name: (identifier) @name) (method_definition name: (property_identifier) @name) (variable_declarator name: (identifier) @name value: [(arrow_function) (function_expression)])] @definition`, Captures: []string{"definition", "name"}, Extensions: astSearchLanguageExtensions["typescript"]},
	{ID: "class_declarations", Language: "typescript", Version: astSearchQueryVersion, Query: `[(class_declaration name: (type_identifier) @name) (interface_declaration name: (type_identifier) @name) (type_alias_declaration name: (type_identifier) @name)] @class`, Captures: []string{"class", "name"}, Extensions: astSearchLanguageExtensions["typescript"]},
	{ID: "call_expressions", Language: "typescript", Version: astSearchQueryVersion, Query: `(call_expression function: [(identifier) @callee (member_expression property: (property_identifier) @callee)]) @call`, Captures: []string{"call", "callee"}, Extensions: astSearchLanguageExtensions["typescript"]},
	{ID: "imports", Language: "typescript", Version: astSearchQueryVersion, Query: `[(import_statement) (call_expression function: (identifier) @callee arguments: (arguments (string) @path) (#eq? @callee "require"))] @import`, Captures: []string{"import", "callee", "path"}, Extensions: astSearchLanguageExtensions["typescript"]},
	{ID: "test_functions", Language: "typescript", Version: astSearchQueryVersion, Query: `(call_expression function: (identifier) @test_name arguments: (arguments) (#match? @test_name "^(it|test|describe)$")) @test`, Captures: []string{"test", "test_name"}, Extensions: astSearchLanguageExtensions["typescript"]},
	{ID: "assignments", Language: "typescript", Version: astSearchQueryVersion, Query: `[(assignment_expression) (variable_declarator)] @assignment`, Captures: []string{"assignment"}, Extensions: astSearchLanguageExtensions["typescript"]},
	{ID: "error_handling", Language: "typescript", Version: astSearchQueryVersion, Query: `[(try_statement) (catch_clause) (throw_statement)] @error`, Captures: []string{"error"}, Extensions: astSearchLanguageExtensions["typescript"]},

	{ID: "function_declarations", Language: "tsx", Version: astSearchQueryVersion, Query: `[(function_declaration name: (identifier) @name) (method_definition name: (property_identifier) @name) (variable_declarator name: (identifier) @name value: [(arrow_function) (function_expression)])] @definition`, Captures: []string{"definition", "name"}, Extensions: astSearchLanguageExtensions["tsx"]},
	{ID: "class_declarations", Language: "tsx", Version: astSearchQueryVersion, Query: `[(class_declaration name: (type_identifier) @name) (interface_declaration name: (type_identifier) @name) (type_alias_declaration name: (type_identifier) @name)] @class`, Captures: []string{"class", "name"}, Extensions: astSearchLanguageExtensions["tsx"]},
	{ID: "call_expressions", Language: "tsx", Version: astSearchQueryVersion, Query: `(call_expression function: [(identifier) @callee (member_expression property: (property_identifier) @callee)]) @call`, Captures: []string{"call", "callee"}, Extensions: astSearchLanguageExtensions["tsx"]},
	{ID: "imports", Language: "tsx", Version: astSearchQueryVersion, Query: `[(import_statement) (call_expression function: (identifier) @callee arguments: (arguments (string) @path) (#eq? @callee "require"))] @import`, Captures: []string{"import", "callee", "path"}, Extensions: astSearchLanguageExtensions["tsx"]},
	{ID: "test_functions", Language: "tsx", Version: astSearchQueryVersion, Query: `(call_expression function: (identifier) @test_name arguments: (arguments) (#match? @test_name "^(it|test|describe)$")) @test`, Captures: []string{"test", "test_name"}, Extensions: astSearchLanguageExtensions["tsx"]},
	{ID: "assignments", Language: "tsx", Version: astSearchQueryVersion, Query: `[(assignment_expression) (variable_declarator)] @assignment`, Captures: []string{"assignment"}, Extensions: astSearchLanguageExtensions["tsx"]},
	{ID: "error_handling", Language: "tsx", Version: astSearchQueryVersion, Query: `[(try_statement) (catch_clause) (throw_statement)] @error`, Captures: []string{"error"}, Extensions: astSearchLanguageExtensions["tsx"]},

	{ID: "function_declarations", Language: "jsx", Version: astSearchQueryVersion, Query: `[(function_declaration name: (identifier) @name) (method_definition name: (property_identifier) @name) (variable_declarator name: (identifier) @name value: [(arrow_function) (function_expression)])] @definition`, Captures: []string{"definition", "name"}, Extensions: astSearchLanguageExtensions["jsx"]},
	{ID: "class_declarations", Language: "jsx", Version: astSearchQueryVersion, Query: `(class_declaration name: (type_identifier) @name) @class`, Captures: []string{"class", "name"}, Extensions: astSearchLanguageExtensions["jsx"]},
	{ID: "call_expressions", Language: "jsx", Version: astSearchQueryVersion, Query: `(call_expression function: [(identifier) @callee (member_expression property: (property_identifier) @callee)]) @call`, Captures: []string{"call", "callee"}, Extensions: astSearchLanguageExtensions["jsx"]},
	{ID: "imports", Language: "jsx", Version: astSearchQueryVersion, Query: `[(import_statement) (call_expression function: (identifier) @callee arguments: (arguments (string) @path) (#eq? @callee "require"))] @import`, Captures: []string{"import", "callee", "path"}, Extensions: astSearchLanguageExtensions["jsx"]},
	{ID: "test_functions", Language: "jsx", Version: astSearchQueryVersion, Query: `(call_expression function: (identifier) @test_name arguments: (arguments) (#match? @test_name "^(it|test|describe)$")) @test`, Captures: []string{"test", "test_name"}, Extensions: astSearchLanguageExtensions["jsx"]},
	{ID: "assignments", Language: "jsx", Version: astSearchQueryVersion, Query: `[(assignment_expression) (variable_declarator)] @assignment`, Captures: []string{"assignment"}, Extensions: astSearchLanguageExtensions["jsx"]},
	{ID: "error_handling", Language: "jsx", Version: astSearchQueryVersion, Query: `[(try_statement) (catch_clause) (throw_statement)] @error`, Captures: []string{"error"}, Extensions: astSearchLanguageExtensions["jsx"]},

	{ID: "function_declarations", Language: "csharp", Version: astSearchQueryVersion, Query: `[(method_declaration name: (identifier) @name) (constructor_declaration name: (identifier) @name)] @definition`, Captures: []string{"definition", "name"}, Extensions: astSearchLanguageExtensions["csharp"]},
	{ID: "class_declarations", Language: "csharp", Version: astSearchQueryVersion, Query: `[(class_declaration name: (identifier) @name) (interface_declaration name: (identifier) @name) (struct_declaration name: (identifier) @name) (record_declaration name: (identifier) @name)] @class`, Captures: []string{"class", "name"}, Extensions: astSearchLanguageExtensions["csharp"]},
	{ID: "call_expressions", Language: "csharp", Version: astSearchQueryVersion, Query: `(invocation_expression) @call`, Captures: []string{"call"}, Extensions: astSearchLanguageExtensions["csharp"]},
	{ID: "imports", Language: "csharp", Version: astSearchQueryVersion, Query: `(using_directive) @import`, Captures: []string{"import"}, Extensions: astSearchLanguageExtensions["csharp"]},
	{ID: "test_functions", Language: "csharp", Version: astSearchQueryVersion, Query: `[(method_declaration (attribute_list) @attribute name: (identifier) @name)] @test`, Captures: []string{"test", "attribute", "name"}, Extensions: astSearchLanguageExtensions["csharp"]},
	{ID: "assignments", Language: "csharp", Version: astSearchQueryVersion, Query: `[(assignment_expression) (variable_declaration)] @assignment`, Captures: []string{"assignment"}, Extensions: astSearchLanguageExtensions["csharp"]},
	{ID: "error_handling", Language: "csharp", Version: astSearchQueryVersion, Query: `[(try_statement) (catch_clause) (throw_statement)] @error`, Captures: []string{"error"}, Extensions: astSearchLanguageExtensions["csharp"]},

	{ID: "function_declarations", Language: "dart", Version: astSearchQueryVersion, Query: `[(function_signature name: (identifier) @name) (method_signature (function_signature name: (identifier) @name)) (method_signature (getter_signature name: (identifier) @name)) (method_signature (setter_signature name: (identifier) @name))] @definition`, Captures: []string{"definition", "name"}, Extensions: astSearchLanguageExtensions["dart"]},
	{ID: "class_declarations", Language: "dart", Version: astSearchQueryVersion, Query: `(class_definition name: (identifier) @name) @class`, Captures: []string{"class", "name"}, Extensions: astSearchLanguageExtensions["dart"]},
	{ID: "type_declarations", Language: "dart", Version: astSearchQueryVersion, Query: `[(mixin_declaration (identifier) @name) (extension_declaration name: (identifier) @name) (extension_type_declaration name: (identifier) @name) (enum_declaration name: (identifier) @name) (type_alias (type_identifier) @name)] @type`, Captures: []string{"type", "name"}, Extensions: astSearchLanguageExtensions["dart"]},
	{ID: "call_expressions", Language: "dart", Version: astSearchQueryVersion, Query: `(selector (argument_part) @callee) @call`, Captures: []string{"call", "callee"}, Extensions: astSearchLanguageExtensions["dart"]},
	{ID: "imports", Language: "dart", Version: astSearchQueryVersion, Query: `(import_or_export [(library_import) (library_export)] @import)`, Captures: []string{"import"}, Extensions: astSearchLanguageExtensions["dart"]},
	{ID: "test_functions", Language: "dart", Version: astSearchQueryVersion, Query: `((identifier) @test_name (selector (argument_part)) @test (#match? @test_name "^(test|group|testWidgets)$"))`, Captures: []string{"test", "test_name"}, Extensions: astSearchLanguageExtensions["dart"]},
	{ID: "assignments", Language: "dart", Version: astSearchQueryVersion, Query: `[(assignment_expression) (initialized_identifier)] @assignment`, Captures: []string{"assignment"}, Extensions: astSearchLanguageExtensions["dart"]},
	{ID: "error_handling", Language: "dart", Version: astSearchQueryVersion, Query: `[(try_statement) (catch_clause) (throw_expression)] @error`, Captures: []string{"error"}, Extensions: astSearchLanguageExtensions["dart"]},
	{ID: "flutter_widgets", Language: "dart", Version: astSearchQueryVersion, Query: `(class_definition name: (identifier) @name superclass: (superclass) @base (#match? @base "(StatelessWidget|StatefulWidget|State<)")) @widget`, Captures: []string{"widget", "name", "base"}, Extensions: astSearchLanguageExtensions["dart"]},
	{ID: "flutter_build_methods", Language: "dart", Version: astSearchQueryVersion, Query: `(method_signature (function_signature (type_identifier) @return_type name: (identifier) @name (#eq? @return_type "Widget") (#eq? @name "build"))) @build_method`, Captures: []string{"build_method", "return_type", "name"}, Extensions: astSearchLanguageExtensions["dart"]},
}
