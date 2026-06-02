package projectingestion

import (
	"encoding/json"
	"io"
	"path"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func containsStructuredSensitiveContent(relativePath string, value string) (bool, bool) {
	switch structuredConfigKind(relativePath) {
	case "json":
		return containsJSONStructuredSensitiveContent(value)
	case "toml":
		return containsTOMLStructuredSensitiveContent(value)
	case "yaml":
		return containsYAMLStructuredSensitiveContent(value)
	default:
		return false, false
	}
}

func structuredConfigKind(relativePath string) string {
	extension := strings.ToLower(path.Ext(relativePath))
	base := strings.ToLower(path.Base(relativePath))
	switch base {
	case "openapi.json", "swagger.json":
		return "json"
	case "openapi.yaml", "openapi.yml", "swagger.yaml", "swagger.yml":
		return "yaml"
	}
	switch extension {
	case ".json":
		return "json"
	case ".toml":
		return "toml"
	case ".yaml", ".yml", ".asset", ".prefab", ".unity":
		return "yaml"
	default:
		return ""
	}
}

func containsJSONStructuredSensitiveContent(value string) (bool, bool) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return false, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return false, false
	}
	return structuredValueContainsSensitive("", decoded), true
}

func containsTOMLStructuredSensitiveContent(value string) (bool, bool) {
	var decoded map[string]any
	if err := toml.Unmarshal([]byte(value), &decoded); err != nil {
		return false, false
	}
	return structuredValueContainsSensitive("", decoded), true
}

func structuredValueContainsSensitive(key string, value any) bool {
	if keyLooksSensitive(key) || keyLooksPhoneField(key) {
		if structuredScalarLooksSensitive(key, value) {
			return true
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		for childKey, childValue := range typed {
			if structuredValueContainsSensitive(childKey, childValue) {
				return true
			}
		}
	case map[any]any:
		for childKey, childValue := range typed {
			if structuredValueContainsSensitive(formatAnyKey(childKey), childValue) {
				return true
			}
		}
	case []any:
		for _, childValue := range typed {
			if structuredValueContainsSensitive(key, childValue) {
				return true
			}
		}
	}
	return false
}

func formatAnyKey(key any) string {
	if text, ok := key.(string); ok {
		return text
	}
	return ""
}

func structuredScalarLooksSensitive(key string, value any) bool {
	switch typed := value.(type) {
	case nil, bool:
		return false
	case string:
		return scalarStringLooksSensitive(key, typed)
	case json.Number:
		return keyLooksPhoneField(key) && digitCount(typed.String()) >= 8
	case int:
		return keyLooksPhoneField(key) && digitCount(strconv.Itoa(typed)) >= 8
	case int64:
		return keyLooksPhoneField(key) && digitCount(strconv.FormatInt(typed, 10)) >= 8
	case float32:
		return keyLooksPhoneField(key) && digitCount(strconv.FormatFloat(float64(typed), 'f', -1, 32)) >= 8
	case float64:
		return keyLooksPhoneField(key) && digitCount(strconv.FormatFloat(typed, 'f', -1, 64)) >= 8
	default:
		return false
	}
}

func containsYAMLStructuredSensitiveContent(value string) (bool, bool) {
	if yamlRawCommentsContainSensitive(value) {
		return true, true
	}
	decoder := yaml.NewDecoder(strings.NewReader(value))
	parsed := false
	for {
		var document yaml.Node
		if err := decoder.Decode(&document); err != nil {
			if err == io.EOF {
				return false, parsed
			}
			return false, false
		}
		if document.Kind == 0 {
			continue
		}
		parsed = true
		if yamlNodeContainsSensitive("", &document, nil) {
			return true, true
		}
	}
}

func yamlNodeContainsSensitive(key string, node *yaml.Node, seen map[*yaml.Node]struct{}) bool {
	if node == nil {
		return false
	}
	if yamlNodeCommentsContainSensitive(node) {
		return true
	}
	if seen == nil {
		seen = make(map[*yaml.Node]struct{})
	}
	if _, ok := seen[node]; ok {
		return false
	}
	seen[node] = struct{}{}
	defer delete(seen, node)

	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if yamlNodeContainsSensitive(key, child, seen) {
				return true
			}
		}
	case yaml.MappingNode:
		return yamlMappingContainsSensitive(key, node, seen)
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if yamlNodeContainsSensitive(key, child, seen) {
				return true
			}
		}
	case yaml.AliasNode:
		return yamlNodeContainsSensitive(key, node.Alias, seen)
	case yaml.ScalarNode:
		return yamlScalarNodeLooksSensitive(key, node)
	}
	return false
}

func yamlNodeCommentsContainSensitive(node *yaml.Node) bool {
	return yamlCommentLooksSensitive(node.HeadComment) ||
		yamlCommentLooksSensitive(node.LineComment) ||
		yamlCommentLooksSensitive(node.FootComment)
}

func yamlCommentLooksSensitive(comment string) bool {
	if strings.TrimSpace(comment) == "" {
		return false
	}
	return containsPIIMarker(comment) ||
		containsSensitiveAssignment("", comment) ||
		containsContentMarkerPattern(comment, contentMarkerPatterns)
}

func yamlRawCommentsContainSensitive(value string) bool {
	blockScalarIndent := -1
	for _, line := range strings.Split(value, "\n") {
		if blockScalarIndent >= 0 {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if leadingYAMLIndent(line) > blockScalarIndent {
				continue
			}
			blockScalarIndent = -1
		}
		comment, ok := yamlLineComment(line)
		if ok && yamlCommentLooksSensitive(comment) {
			return true
		}
		if yamlLineStartsBlockScalar(line) {
			blockScalarIndent = leadingYAMLIndent(line)
		}
	}
	return false
}

func yamlLineComment(line string) (string, bool) {
	index, ok := yamlLineCommentIndex(line)
	if !ok {
		return "", false
	}
	return line[index+1:], true
}

func yamlLineCommentIndex(line string) (int, bool) {
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false
	for index, r := range line {
		if inDoubleQuote {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == '"' {
				inDoubleQuote = false
			}
			continue
		}
		if inSingleQuote {
			if r == '\'' {
				inSingleQuote = false
			}
			continue
		}
		switch r {
		case '\'':
			inSingleQuote = true
		case '"':
			inDoubleQuote = true
		case '#':
			if index == 0 || isYAMLCommentPrefix(line[index-1]) {
				return index, true
			}
		}
	}
	return -1, false
}

func isYAMLCommentPrefix(char byte) bool {
	return char == ' ' || char == '\t'
}

func yamlLineStartsBlockScalar(line string) bool {
	if index, ok := yamlLineCommentIndex(line); ok {
		line = line[:index]
	}
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return false
	}
	indicator := fields[len(fields)-1]
	if indicator == "" {
		return false
	}
	switch indicator[0] {
	case '|', '>':
		for _, r := range indicator[1:] {
			if r != '+' && r != '-' && (r < '0' || r > '9') {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func leadingYAMLIndent(line string) int {
	indent := 0
	for indent < len(line) {
		switch line[indent] {
		case ' ', '\t':
			indent++
		default:
			return indent
		}
	}
	return indent
}

func yamlMappingContainsSensitive(parentKey string, node *yaml.Node, seen map[*yaml.Node]struct{}) bool {
	schemaMapping := yamlMappingLooksSchemaLike(node)
	for index := 0; index+1 < len(node.Content); index += 2 {
		keyNode := node.Content[index]
		valueNode := node.Content[index+1]
		childKey := yamlKeyText(keyNode)
		if childKey == "" {
			childKey = parentKey
		}
		if keyLooksSensitive(childKey) || keyLooksPhoneField(childKey) {
			if yamlSensitiveKeyValueLooksSensitive(childKey, valueNode, seen) {
				return true
			}
			if yamlMappingLooksSchemaLikeValue(valueNode) {
				continue
			}
		}
		nextKey := childKey
		if schemaMapping && isOpenAPISchemaMetadataKey(childKey) {
			nextKey = ""
		}
		if yamlNodeContainsSensitive(nextKey, valueNode, seen) {
			return true
		}
	}
	return false
}

func yamlSensitiveKeyValueLooksSensitive(key string, node *yaml.Node, seen map[*yaml.Node]struct{}) bool {
	if node == nil {
		return false
	}
	switch node.Kind {
	case yaml.ScalarNode:
		return yamlScalarNodeLooksSensitive(key, node)
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if yamlNodeContainsSensitive(key, child, seen) {
				return true
			}
		}
	case yaml.AliasNode:
		return yamlSensitiveKeyValueLooksSensitive(key, node.Alias, seen)
	case yaml.MappingNode:
		if yamlMappingLooksSchemaLike(node) {
			return yamlSchemaMappingContainsSensitiveValue(key, node, seen)
		}
		for _, child := range node.Content {
			if yamlNodeContainsSensitive(key, child, seen) {
				return true
			}
		}
	}
	return false
}

func yamlScalarNodeLooksSensitive(key string, node *yaml.Node) bool {
	if node == nil || (!keyLooksSensitive(key) && !keyLooksPhoneField(key)) {
		return false
	}
	switch node.Tag {
	case "!!null", "!!bool":
		return false
	case "!!int", "!!float":
		return keyLooksPhoneField(key) && digitCount(node.Value) >= 8
	case "!!str", "":
		return scalarStringLooksSensitive(key, node.Value)
	default:
		return false
	}
}

func yamlKeyText(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

func yamlMappingLooksSchemaLikeValue(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml.AliasNode {
		return yamlMappingLooksSchemaLikeValue(node.Alias)
	}
	return node.Kind == yaml.MappingNode && yamlMappingLooksSchemaLike(node)
}

func yamlSchemaMappingContainsSensitiveValue(key string, node *yaml.Node, seen map[*yaml.Node]struct{}) bool {
	for index := 0; index+1 < len(node.Content); index += 2 {
		childKey := normalizeConfigKey(yamlKeyText(node.Content[index]))
		if childKey == "" || isOpenAPISchemaMetadataKey(childKey) {
			continue
		}
		switch childKey {
		case "default", "example", "examples", "const", "value":
			if yamlNodeContainsSensitive(key, node.Content[index+1], seen) {
				return true
			}
		default:
			if yamlNodeContainsSensitive(childKey, node.Content[index+1], seen) {
				return true
			}
		}
	}
	return false
}

func yamlMappingLooksSchemaLike(node *yaml.Node) bool {
	if node == nil || node.Kind != yaml.MappingNode {
		return false
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		key := normalizeConfigKey(yamlKeyText(node.Content[index]))
		switch key {
		case "type", "format", "nullable", "description", "title", "properties", "items", "required", "enum", "allof", "anyof", "oneof", "ref":
			return true
		}
	}
	return false
}

func isOpenAPISchemaMetadataKey(key string) bool {
	switch normalizeConfigKey(key) {
	case "type", "format", "nullable", "description", "title", "required", "enum", "ref":
		return true
	default:
		return false
	}
}

func scalarStringLooksSensitive(key string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || isEnvironmentReference(value) || assignmentValueIsSafeScalarOrType(value) || isNumericLikeValue(value) {
		return false
	}
	if keyLooksPhoneField(key) {
		return digitCount(value) >= 8 || len(value) >= 4
	}
	return len(value) >= 4
}

func keyLooksSensitive(key string) bool {
	return exactSensitiveAssignmentKeyPattern.MatchString(normalizeConfigKey(key))
}

func keyLooksPhoneField(key string) bool {
	return exactPhoneAssignmentKeyPattern.MatchString(normalizeConfigKey(key))
}

func normalizeConfigKey(key string) string {
	key = strings.Trim(strings.TrimSpace(key), `"'`)
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, ".", "_")
	var builder strings.Builder
	for _, r := range key {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func digitCount(value string) int {
	count := 0
	for _, r := range value {
		if r >= '0' && r <= '9' {
			count++
		}
	}
	return count
}

func containsSensitiveAssignment(relativePath string, value string) bool {
	mode := sensitiveAssignmentModeForPath(relativePath)
	for _, line := range strings.Split(value, "\n") {
		assignments := sensitiveAssignmentsInLine(line)
		for _, assignment := range assignments {
			key := sensitiveAssignmentKeyFromLeft(assignment.left)
			if key == "" || (!keyLooksSensitive(key) && !keyLooksPhoneField(key)) {
				continue
			}
			if assignmentValueLooksSensitive(mode, assignment.operator, assignment.right, key) {
				return true
			}
		}
	}
	return false
}

type sensitiveAssignmentMode int

const (
	sensitiveAssignmentModeGeneric sensitiveAssignmentMode = iota
	sensitiveAssignmentModeCode
	sensitiveAssignmentModeConfig
)

type sensitiveAssignmentCandidate struct {
	left     string
	operator string
	right    string
}

func sensitiveAssignmentModeForPath(relativePath string) sensitiveAssignmentMode {
	switch strings.ToLower(path.Ext(relativePath)) {
	case ".go", ".py", ".pyw", ".js", ".mjs", ".cjs", ".jsx", ".ts", ".mts", ".cts", ".tsx", ".cs", ".dart":
		return sensitiveAssignmentModeCode
	case ".json", ".yaml", ".yml", ".toml", ".asset", ".prefab", ".unity":
		return sensitiveAssignmentModeConfig
	default:
		return sensitiveAssignmentModeGeneric
	}
}

func sensitiveAssignmentsInLine(line string) []sensitiveAssignmentCandidate {
	var assignments []sensitiveAssignmentCandidate
	segmentStart := 0
	inQuote := rune(0)
	escaped := false
	for index, r := range line {
		if inQuote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' && inQuote != '`' {
				escaped = true
				continue
			}
			if r == inQuote {
				inQuote = 0
			}
			continue
		}
		switch r {
		case '\'', '"', '`':
			inQuote = r
			continue
		case '{', '[', '(', ',', ';':
			segmentStart = index + 1
			continue
		case ':', '=':
			operator := string(r)
			rightStart := index + 1
			if r == ':' && index+1 < len(line) && line[index+1] == '=' {
				operator = ":="
				rightStart = index + 2
			}
			assignments = append(assignments, sensitiveAssignmentCandidate{
				left:     line[segmentStart:index],
				operator: operator,
				right:    line[rightStart:],
			})
		}
	}
	return assignments
}

func sensitiveAssignmentKeyFromLeft(left string) string {
	left = strings.TrimSpace(left)
	left = strings.TrimRight(left, " \t?")
	if left == "" {
		return ""
	}
	if key := trailingQuotedKey(left); key != "" {
		return key
	}
	for end := len(left); end > 0; {
		for end > 0 {
			r := rune(left[end-1])
			if isSensitiveIdentifierRune(r) {
				break
			}
			end--
		}
		start := end
		for start > 0 {
			r := rune(left[start-1])
			if !isSensitiveIdentifierRune(r) {
				break
			}
			start--
		}
		if start >= end {
			break
		}
		key := strings.Trim(left[start:end], "_-")
		if keyLooksSensitive(key) || keyLooksPhoneField(key) {
			return key
		}
		end = start
	}
	return ""
}

func trailingQuotedKey(left string) string {
	if left == "" {
		return ""
	}
	quote := left[len(left)-1]
	if quote != '\'' && quote != '"' && quote != '`' {
		return ""
	}
	for index := len(left) - 2; index >= 0; index-- {
		if left[index] == quote {
			return strings.TrimSpace(left[index+1 : len(left)-1])
		}
	}
	return ""
}

func isSensitiveIdentifierRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
}

func assignmentValueLooksSensitive(mode sensitiveAssignmentMode, operator string, rawValue string, key string) bool {
	value, quoted := normalizeAssignedValue(rawValue)
	if value == "" {
		return false
	}
	if isEnvironmentReference(value) {
		return false
	}
	if assignmentValueIsSafeScalarOrType(value) {
		return false
	}
	if keyLooksPhoneField(key) {
		return digitCount(value) >= 8
	}
	if quoted {
		return true
	}
	if isNumericLikeValue(value) || !containsASCIILetter(value) {
		return false
	}
	if mode == sensitiveAssignmentModeCode {
		if operator == ":" || valueLooksLikeCodeExpression(value) {
			return false
		}
		return len(value) >= 4
	}
	if operator == ":" && len(value) < 6 {
		return false
	}
	return len(value) >= 4
}

func assignmentValueIsSafeScalarOrType(value string) bool {
	lower := strings.Trim(strings.ToLower(value), "{}[](),;")
	switch lower {
	case "0", "1", "true", "false", "null", "nil", "none", "n/a", "na", "undefined",
		"string", "str", "number", "int", "int32", "int64", "uint", "uint32", "uint64",
		"float", "float32", "float64", "double", "decimal", "bool", "boolean", "any",
		"unknown", "never", "object", "record", "dynamic", "array", "uuid", "uri", "url",
		"email", "date", "date-time", "binary", "byte", "password", "phone":
		return true
	default:
		return false
	}
}

func valueLooksLikeCodeExpression(value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.ContainsAny(trimmed, "()[]{}") {
		return true
	}
	for _, prefix := range []string{"new ", "typeof ", "await "} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func isEnvironmentReference(value string) bool {
	normalized := strings.TrimRight(strings.TrimSpace(value), ".,;")
	for _, prefix := range []string{
		"process.env.",
		"import.meta.env.",
		"env.",
		"ENV.",
		"os.Getenv(",
		"os.getenv(",
		"os.environ[",
		"Environment.GetEnvironmentVariable(",
		"String.fromEnvironment(",
		"${",
		"$",
	} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func normalizeAssignedValue(rawValue string) (string, bool) {
	value := strings.TrimSpace(rawValue)
	if strings.HasPrefix(value, "{") {
		if inner := strings.TrimSpace(strings.TrimPrefix(value, "{")); inner != "" {
			value = strings.TrimSpace(strings.TrimRight(inner, "}"))
		}
	}
	if len(value) >= 2 {
		quote := value[0]
		if quote == '\'' || quote == '"' || quote == '`' {
			end := closingQuoteIndex(value, quote)
			if end > 0 {
				return strings.TrimSpace(value[1:end]), true
			}
		}
	}
	value = truncateAssignedValueToken(value)
	return value, false
}

func closingQuoteIndex(value string, quote byte) int {
	escaped := false
	for index := 1; index < len(value); index++ {
		if escaped {
			escaped = false
			continue
		}
		if value[index] == '\\' && quote != '`' {
			escaped = true
			continue
		}
		if value[index] == quote {
			return index
		}
	}
	return -1
}

func truncateAssignedValueToken(value string) string {
	for index, r := range value {
		switch r {
		case ',', ';', '#', '\r', '\n':
			return strings.TrimSpace(value[:index])
		case '}':
			return strings.TrimSpace(value[:index])
		}
		if r == '/' && index+1 < len(value) && value[index+1] == '/' {
			return strings.TrimSpace(value[:index])
		}
	}
	return strings.TrimSpace(value)
}

func isNumericLikeValue(value string) bool {
	hasDigit := false
	for _, r := range value {
		if r >= '0' && r <= '9' {
			hasDigit = true
			continue
		}
		switch r {
		case '+', '-', '.', '_', ':', ' ', '\t', '(', ')', '[', ']', '{', '}':
			continue
		default:
			return false
		}
	}
	return hasDigit
}

func containsASCIILetter(value string) bool {
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}
