package projectintegrations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultRichContentMaxChunkBytes = 16 * 1024
)

var (
	emailAddressPattern = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	bearerTokenPattern  = regexp.MustCompile(`(?i)\b(Basic|Bearer)\s+[A-Za-z0-9+/._~=-]{8,}\b`)
	envNamePattern      = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{5,}\b`)
)

type RichContentOptions struct {
	MaxItemTextBytes int
	MaxChunkBytes    int
}

type RichContentField struct {
	Name  string
	Label string
	Text  string
}

type RichContentItem struct {
	ProjectID string
	Provider  Provider
	ItemID    string
	ItemKey   string
	ItemType  string
	UpdatedAt time.Time
	Fields    []RichContentField
}

type RichContentChunk struct {
	ID        string
	ProjectID string
	Provider  Provider
	ItemID    string
	ItemKey   string
	ItemType  string
	FieldName string
	Label     string
	Index     int
	ByteStart int
	ByteEnd   int
	Text      string
	UpdatedAt time.Time
}

func ExtractJiraRichContent(plan JiraQueryPlan, raw json.RawMessage, options RichContentOptions) (RichContentItem, []RichContentChunk, error) {
	if plan.Provider != ProviderJira || strings.TrimSpace(plan.ProjectID) == "" {
		return RichContentItem{}, nil, ErrInvalidInput
	}
	var issue struct {
		ID     string                     `json:"id"`
		Key    string                     `json:"key"`
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(raw, &issue); err != nil {
		return RichContentItem{}, nil, DecodeError("jira", "extract_rich_content")
	}
	id := strings.TrimSpace(issue.ID)
	if id == "" {
		return RichContentItem{}, nil, ErrInvalidInput
	}
	item := RichContentItem{
		ProjectID: strings.TrimSpace(plan.ProjectID),
		Provider:  ProviderJira,
		ItemID:    id,
		ItemKey:   strings.TrimSpace(issue.Key),
		ItemType:  "issue",
	}
	if issue.Fields != nil {
		if value, ok := issue.Fields["issuetype"]; ok {
			if text := textFromNamedValue(value); text != "" {
				item.ItemType = text
			}
		}
		if value, ok := issue.Fields["updated"]; ok {
			item.UpdatedAt = parseRichProviderTime(textFromJSONScalar(value))
		}
		item.Fields = appendJiraField(item.Fields, "summary", "summary", issue.Fields["summary"], true)
		if plan.IncludeRichFields {
			for _, field := range plan.Fields {
				name := strings.TrimSpace(field)
				if name == "" || isDefaultJiraContentField(name) || isSensitiveFieldName(name) {
					continue
				}
				if strings.EqualFold(name, "comment") {
					continue
				}
				item.Fields = appendJiraField(item.Fields, name, name, issue.Fields[name], true)
			}
		}
		if plan.IncludeComments && containsField(plan.Fields, "comment") {
			item.Fields = appendJiraField(item.Fields, "comment", "comment", issue.Fields["comment"], true)
		}
	}
	item.Fields = boundRichFields(item.Fields, normalizeRichContentOptions(options).MaxItemTextBytes)
	chunks, err := ChunkRichContentItem(item, options)
	if err != nil {
		return RichContentItem{}, nil, err
	}
	return item, chunks, nil
}

func ExtractConfluenceRichContent(plan ConfluenceQueryPlan, raw json.RawMessage, options RichContentOptions) (RichContentItem, []RichContentChunk, error) {
	if plan.Provider != ProviderConfluence || strings.TrimSpace(plan.ProjectID) == "" {
		return RichContentItem{}, nil, ErrInvalidInput
	}
	var page map[string]json.RawMessage
	if err := json.Unmarshal(raw, &page); err != nil {
		return RichContentItem{}, nil, DecodeError("confluence", "extract_rich_content")
	}
	id := firstJSONText(page, "id", "content.id")
	if id == "" {
		return RichContentItem{}, nil, ErrInvalidInput
	}
	item := RichContentItem{
		ProjectID: strings.TrimSpace(plan.ProjectID),
		Provider:  ProviderConfluence,
		ItemID:    id,
		ItemType:  firstNonEmptyText(firstJSONText(page, "type", "content.type"), "page"),
		UpdatedAt: parseRichProviderTime(firstJSONText(page, "lastModified", "version.when", "version.createdAt", "createdAt", "content.version.when", "content.version.createdAt", "content.history.lastUpdated.when")),
	}
	item.Fields = appendConfluenceField(item.Fields, "title", "title", page["title"], true)
	if plan.IncludeBody {
		bodyText := confluenceBodyText(page, plan.BodyRepresentation)
		item.Fields = appendTextField(item.Fields, "body", richFieldLabel("body", plan.BodyRepresentation), bodyText)
	}
	if plan.IncludeComments {
		item.Fields = appendConfluenceField(item.Fields, "comments", "comments", firstExistingRaw(page, "comments", "footerComments", "inlineComments"), true)
	}
	if plan.IncludeLabels {
		item.Fields = appendConfluenceField(item.Fields, "labels", "labels", page["labels"], true)
	}
	if plan.IncludeProperties {
		item.Fields = appendConfluenceField(item.Fields, "properties", "properties", page["properties"], true)
	}
	item.Fields = boundRichFields(item.Fields, normalizeRichContentOptions(options).MaxItemTextBytes)
	chunks, err := ChunkRichContentItem(item, options)
	if err != nil {
		return RichContentItem{}, nil, err
	}
	return item, chunks, nil
}

func ChunkRichContentItem(item RichContentItem, options RichContentOptions) ([]RichContentChunk, error) {
	options = normalizeRichContentOptions(options)
	if strings.TrimSpace(item.ProjectID) == "" || item.Provider == "" || strings.TrimSpace(item.ItemID) == "" {
		return nil, ErrInvalidInput
	}
	var chunks []RichContentChunk
	for _, field := range boundRichFields(item.Fields, options.MaxItemTextBytes) {
		fieldName := strings.TrimSpace(field.Name)
		if fieldName == "" || isSensitiveFieldName(fieldName) {
			continue
		}
		text := sanitizeRichText(field.Text)
		if text == "" {
			continue
		}
		parts, err := splitRichText(text, options.MaxChunkBytes)
		if err != nil {
			return nil, err
		}
		offset := 0
		for _, part := range parts {
			chunk := RichContentChunk{
				ID:        richChunkID(item, fieldName, len(chunks)),
				ProjectID: item.ProjectID,
				Provider:  item.Provider,
				ItemID:    item.ItemID,
				ItemKey:   item.ItemKey,
				ItemType:  item.ItemType,
				FieldName: fieldName,
				Label:     firstNonEmptyText(strings.TrimSpace(field.Label), fieldName),
				Index:     len(chunks),
				ByteStart: offset,
				ByteEnd:   offset + len([]byte(part)),
				Text:      part,
				UpdatedAt: item.UpdatedAt,
			}
			chunks = append(chunks, chunk)
			offset = chunk.ByteEnd
		}
	}
	return chunks, nil
}

func appendJiraField(fields []RichContentField, name, label string, raw json.RawMessage, include bool) []RichContentField {
	if !include || len(raw) == 0 {
		return fields
	}
	return appendTextField(fields, name, label, textFromJSON(raw))
}

func appendConfluenceField(fields []RichContentField, name, label string, raw json.RawMessage, include bool) []RichContentField {
	if !include || len(raw) == 0 {
		return fields
	}
	return appendTextField(fields, name, label, textFromJSON(raw))
}

func appendTextField(fields []RichContentField, name, label, text string) []RichContentField {
	name = strings.TrimSpace(name)
	if name == "" || isSensitiveFieldName(name) {
		return fields
	}
	text = sanitizeRichText(text)
	if text == "" {
		return fields
	}
	return append(fields, RichContentField{Name: name, Label: strings.TrimSpace(label), Text: text})
}

func boundRichFields(fields []RichContentField, maxItemBytes int) []RichContentField {
	if maxItemBytes <= 0 {
		bounded := make([]RichContentField, 0, len(fields))
		for _, field := range fields {
			text := sanitizeRichText(field.Text)
			if text == "" {
				continue
			}
			field.Text = text
			bounded = append(bounded, field)
		}
		return bounded
	}
	remaining := maxItemBytes
	bounded := make([]RichContentField, 0, len(fields))
	for _, field := range fields {
		if remaining <= 0 {
			break
		}
		text := truncateUTF8Bytes(sanitizeRichText(field.Text), remaining)
		if text == "" {
			continue
		}
		field.Text = text
		remaining -= len([]byte(text))
		bounded = append(bounded, field)
	}
	return bounded
}

func splitRichText(text string, maxChunkBytes int) ([]string, error) {
	if maxChunkBytes <= 0 {
		return nil, fmt.Errorf("%w: max chunk bytes must be positive", ErrInvalidInput)
	}
	text = sanitizeRichText(text)
	if text == "" {
		return nil, nil
	}
	var chunks []string
	var current []byte
	for len(text) > 0 {
		r, width := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError && width == 1 {
			return nil, fmt.Errorf("%w: invalid utf-8 content", ErrInvalidInput)
		}
		if width > maxChunkBytes {
			return nil, fmt.Errorf("%w: rune exceeds max chunk bytes", ErrInvalidInput)
		}
		if len(current)+width > maxChunkBytes {
			chunks = append(chunks, string(current))
			current = nil
		}
		current = append(current, text[:width]...)
		text = text[width:]
	}
	if len(current) > 0 {
		chunks = append(chunks, string(current))
	}
	return chunks, nil
}

func normalizeRichContentOptions(options RichContentOptions) RichContentOptions {
	if options.MaxChunkBytes <= 0 {
		options.MaxChunkBytes = defaultRichContentMaxChunkBytes
	}
	if options.MaxItemTextBytes > 0 && options.MaxChunkBytes > options.MaxItemTextBytes {
		options.MaxChunkBytes = options.MaxItemTextBytes
	}
	return options
}

func richChunkID(item RichContentItem, field string, index int) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		item.ProjectID,
		string(item.Provider),
		item.ItemID,
		item.ItemKey,
		field,
		strconv.Itoa(index),
	}, "\x00")))
	return "integration-chunk-" + hex.EncodeToString(sum[:12])
}

func textFromJSON(raw json.RawMessage) string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	values := collectJSONText(value, "")
	return strings.Join(values, "\n")
}

func textFromJSONScalar(raw json.RawMessage) string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func textFromNamedValue(raw json.RawMessage) string {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	for _, key := range []string{"name", "displayName", "value"} {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return sanitizeRichText(text)
		}
	}
	return ""
}

func collectJSONText(value any, parentKey string) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		text := sanitizeRichText(typed)
		if text == "" {
			return nil
		}
		return []string{text}
	case bool:
		return []string{strconv.FormatBool(typed)}
	case float64:
		return []string{strconv.FormatFloat(typed, 'f', -1, 64)}
	case []any:
		var values []string
		for _, entry := range typed {
			values = append(values, collectJSONText(entry, parentKey)...)
		}
		return values
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var values []string
		for _, key := range keys {
			if shouldSkipRichJSONKey(key) {
				continue
			}
			values = append(values, collectJSONText(typed[key], key)...)
		}
		return values
	default:
		return nil
	}
}

func shouldSkipRichJSONKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return true
	}
	if isSensitiveFieldName(key) {
		return true
	}
	for _, marker := range []string{"email", "avatar", "icon", "self", "url", "href", "path", "credential", "authorization", "auth"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func confluenceBodyText(page map[string]json.RawMessage, representation string) string {
	bodyRaw := firstExistingRaw(page, "body")
	if len(bodyRaw) == 0 {
		return ""
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(bodyRaw, &body); err != nil {
		return ""
	}
	for _, key := range preferredBodyKeys(representation) {
		if raw, ok := body[key]; ok {
			if text := bodyRepresentationValue(raw); text != "" {
				return text
			}
		}
	}
	return ""
}

func preferredBodyKeys(representation string) []string {
	representation = strings.TrimSpace(representation)
	keys := []string{}
	if representation != "" {
		keys = append(keys, representation)
	}
	return append(keys, "storage", "atlas_doc_format", "view")
}

func bodyRepresentationValue(raw json.RawMessage) string {
	var wrapped map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		if value, ok := wrapped["value"]; ok {
			return textFromJSON(value)
		}
	}
	return textFromJSON(raw)
}

func firstExistingRaw(page map[string]json.RawMessage, keys ...string) json.RawMessage {
	for _, key := range keys {
		if raw, ok := page[key]; ok && len(raw) > 0 {
			return raw
		}
	}
	return nil
}

func firstJSONText(page map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if text := textForJSONPath(page, strings.Split(key, ".")); text != "" {
			return text
		}
	}
	return ""
}

func textForJSONPath(value any, path []string) string {
	if len(path) == 0 {
		switch typed := value.(type) {
		case json.RawMessage:
			return textFromJSONScalar(typed)
		case map[string]json.RawMessage:
			return ""
		default:
			return ""
		}
	}
	switch typed := value.(type) {
	case map[string]json.RawMessage:
		raw, ok := typed[path[0]]
		if !ok {
			return ""
		}
		if len(path) == 1 {
			return textFromJSONScalar(raw)
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err != nil {
			return ""
		}
		return textForJSONPath(nested, path[1:])
	default:
		return ""
	}
}

func parseRichProviderTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.000-0700",
		"2006-01-02T15:04:05-0700",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func sanitizeRichText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if bearerTokenPattern.MatchString(text) || looksLikeLocalRoot(text) || looksLikeCredentialValue(text) {
		return ""
	}
	text = emailAddressPattern.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return text
}

func looksLikeLocalRoot(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "/home/") ||
		strings.Contains(lower, "\\wsl.localhost\\") ||
		strings.Contains(lower, "c:\\") ||
		strings.Contains(lower, "file://")
}

func looksLikeCredentialValue(text string) bool {
	trimmed := strings.TrimSpace(text)
	if strings.Contains(trimmed, " ") || len(trimmed) < 20 {
		return false
	}
	if envNamePattern.MatchString(trimmed) && strings.Contains(trimmed, "_") {
		return true
	}
	hasDigit := false
	hasLetter := false
	for _, r := range trimmed {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			hasLetter = true
		case strings.ContainsRune("-_./+=:", r):
		default:
			return false
		}
	}
	return hasDigit && hasLetter
}

func truncateUTF8Bytes(text string, maxBytes int) string {
	if maxBytes <= 0 || len([]byte(text)) <= maxBytes {
		return text
	}
	for len(text) > 0 && len([]byte(text)) > maxBytes {
		_, size := utf8.DecodeLastRuneInString(text)
		text = text[:len(text)-size]
	}
	return strings.TrimSpace(text)
}

func isDefaultJiraContentField(field string) bool {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "summary", "status", "updated", "issuetype", "project":
		return true
	default:
		return false
	}
}

func richFieldLabel(prefix, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return prefix
	}
	return prefix + ":" + value
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}
