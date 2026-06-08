package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
)

const (
	governedCloseoutOutputMissing    = "governed_closeout_output_missing"
	governedCloseoutInvalidJSON      = "governed_closeout_invalid_json"
	governedCloseoutValidationFailed = "governed_closeout_validation_failed"
	governedCloseoutApplyFailed      = "governed_closeout_apply_failed"
	governedCloseoutReadbackFailed   = "governed_closeout_readback_failed"

	closeoutChildTaskDescriptionMax = 1000
	closeoutWorkTaskTextMax         = 500
	closeoutWorkTaskShortTextMax    = 200
)

var (
	closeoutEmailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	closeoutPhonePattern = regexp.MustCompile(`\+?[0-9][0-9 .()\-]{7,}[0-9]`)
	closeoutDrivePattern = regexp.MustCompile(`^[a-z]:`)
	closeoutRefPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,199}$`)
)

type governedCloseoutError struct {
	category string
	err      error
}

func (err governedCloseoutError) Error() string {
	if err.err == nil {
		return err.category
	}
	return err.category + ": " + err.err.Error()
}

func (err governedCloseoutError) Unwrap() error { return err.err }

func governedCloseoutFailureCategory(err error) string {
	var closeoutErr governedCloseoutError
	if errors.As(err, &closeoutErr) && closeoutErr.category != "" {
		if detail := governedCloseoutSafeFailureDetail(closeoutErr.category, closeoutErr.err); detail != "" {
			return closeoutErr.category + "_" + detail
		}
		return closeoutErr.category
	}
	return governedCloseoutApplyFailed
}

func governedCloseoutSafeFailureDetail(category string, err error) string {
	if err == nil {
		return ""
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	for _, marker := range []string{
		"child_task_create_failed",
		"wrapper_evidence_attach_failed",
		"wrapper_status_update_failed",
		"wrapper_verifier_attach_failed",
		"wrapper_block_failed",
		"wrapper_fail_failed",
		"wrapper_readback_failed",
		"invalid_project_workplan_input",
		"invalid_project_work_task_input",
		"invalid_project_automation_input",
		"invalid_project_workflow_input",
		"invalid_project_gitops_input",
	} {
		if strings.Contains(text, marker) {
			return marker
		}
	}
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
			lastUnderscore = false
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore && builder.Len() > 0 {
				builder.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	detail := strings.Trim(builder.String(), "_")
	if detail == "" {
		return ""
	}
	max := 100 - len(category) - 1
	if max < 20 {
		max = 20
	}
	if len(detail) > max {
		detail = strings.TrimRight(detail[:max], "_")
	}
	return detail
}

type governedCloseoutOutput struct {
	CloseoutAction string                     `json:"closeout_action"`
	Outcome        string                     `json:"outcome"`
	SafeNextAction string                     `json:"safe_next_action"`
	EvidenceRefs   []string                   `json:"evidence_refs"`
	VerifierRefs   []string                   `json:"verifier_result_refs"`
	ChildTasks     []governedCloseoutWorkTask `json:"child_tasks"`
	BlockReason    string                     `json:"block_reason"`
	FailureReason  string                     `json:"failure_reason"`
}

type governedCloseoutWorkTask struct {
	TaskRef                 string   `json:"task_ref"`
	Title                   string   `json:"title"`
	Description             string   `json:"description"`
	Status                  string   `json:"status"`
	OwnerAgent              string   `json:"owner_agent"`
	EvidenceNeeded          []string `json:"evidence_needed"`
	ContextPackRefs         []string `json:"context_pack_refs"`
	FilesToRead             []string `json:"files_to_read"`
	FilesToEdit             []string `json:"files_to_edit"`
	LikelyFilesAffected     []string `json:"likely_files_affected"`
	DependencyTaskIDs       []string `json:"dependency_task_ids"`
	VerificationRequirement string   `json:"verification_requirement"`
	ExpectedOutput          string   `json:"expected_output"`
	FailureCriteria         string   `json:"failure_criteria"`
	ReviewGate              string   `json:"review_gate"`
	ResumeInstructions      string   `json:"resume_instructions"`
	DecompositionQuality    string   `json:"decomposition_quality"`
	AcceptanceCriteria      []string `json:"acceptance_criteria"`
	StopConditions          []string `json:"stop_conditions"`
	VerifierLadder          []string `json:"verifier_ladder"`
	RegressionApplicability string   `json:"regression_test_applicability"`
	DownstreamImpactRefs    []string `json:"downstream_impact_refs"`
	OutputContract          string   `json:"output_contract"`
}

func createGovernedCloseoutSchemaFile() (string, func(), error) {
	childTaskProperties := map[string]any{
		"task_ref":                      map[string]any{"type": "string", "maxLength": 200},
		"title":                         map[string]any{"type": "string", "maxLength": closeoutWorkTaskShortTextMax},
		"description":                   map[string]any{"type": "string", "maxLength": closeoutChildTaskDescriptionMax},
		"status":                        map[string]any{"type": "string", "maxLength": 40},
		"owner_agent":                   map[string]any{"type": "string", "maxLength": 120},
		"evidence_needed":               closeoutRefArraySchema(),
		"context_pack_refs":             closeoutRefArraySchema(),
		"files_to_read":                 closeoutPathArraySchema(),
		"files_to_edit":                 closeoutPathArraySchema(),
		"likely_files_affected":         closeoutPathArraySchema(),
		"dependency_task_ids":           closeoutRefArraySchema(),
		"verification_requirement":      map[string]any{"type": "string", "maxLength": closeoutWorkTaskTextMax},
		"expected_output":               map[string]any{"type": "string", "maxLength": closeoutWorkTaskTextMax},
		"failure_criteria":              map[string]any{"type": "string", "maxLength": closeoutWorkTaskTextMax},
		"review_gate":                   map[string]any{"type": "string", "maxLength": closeoutWorkTaskTextMax},
		"resume_instructions":           map[string]any{"type": "string"},
		"decomposition_quality":         map[string]any{"type": "string", "maxLength": 80},
		"acceptance_criteria":           closeoutTextArraySchema(),
		"stop_conditions":               closeoutTextArraySchema(),
		"verifier_ladder":               closeoutTextArraySchema(),
		"regression_test_applicability": map[string]any{"type": "string", "maxLength": closeoutWorkTaskTextMax},
		"downstream_impact_refs":        closeoutRefArraySchema(),
		"output_contract":               map[string]any{"type": "string", "maxLength": closeoutWorkTaskTextMax},
	}
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"closeout_action", "outcome", "safe_next_action", "evidence_refs", "verifier_result_refs", "child_tasks", "block_reason", "failure_reason"},
		"properties": map[string]any{
			"closeout_action":      map[string]any{"type": "string", "enum": []string{"needs_review", "block", "fail"}},
			"outcome":              map[string]any{"type": "string", "minLength": 1, "maxLength": 1200},
			"safe_next_action":     map[string]any{"type": "string", "maxLength": 1200},
			"evidence_refs":        closeoutRefArraySchema(),
			"verifier_result_refs": closeoutRefArraySchema(),
			"block_reason":         map[string]any{"type": "string", "maxLength": 1200},
			"failure_reason":       map[string]any{"type": "string", "maxLength": 1200},
			"changed_files":        closeoutPathArraySchema(),
			"files_changed":        closeoutPathArraySchema(),
			"modified_files":       closeoutPathArraySchema(),
			"created_files":        closeoutPathArraySchema(),
			"deleted_files":        closeoutPathArraySchema(),
			"child_tasks": map[string]any{
				"type":     "array",
				"maxItems": 50,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{},
					"properties":           childTaskProperties,
				},
			},
		},
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "mivia-governed-closeout-schema-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "governed-closeout.schema.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func closeoutRefSchema() map[string]any {
	return map[string]any{"type": "string", "maxLength": 200, "pattern": "^[A-Za-z0-9][A-Za-z0-9._:/@+-]*$"}
}

func closeoutRefArraySchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 100, "items": closeoutRefSchema()}
}

func closeoutPathArraySchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "string", "minLength": 1, "pattern": "^[A-Za-z0-9][A-Za-z0-9._/@+-]*$"}}
}

func closeoutTextArraySchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 50, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 500}}
}

func sortedSchemaKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func parseGovernedCloseoutOutput(message string) (governedCloseoutOutput, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return governedCloseoutOutput{}, governedCloseoutError{category: governedCloseoutOutputMissing, err: errors.New("empty codex final message")}
	}
	jsonText, err := extractSingleJSONObject(message)
	if err != nil {
		return governedCloseoutOutput{}, governedCloseoutError{category: governedCloseoutInvalidJSON, err: err}
	}
	jsonText, err = normalizeGovernedCloseoutJSON(jsonText)
	if err != nil {
		return governedCloseoutOutput{}, err
	}
	if err := validateGovernedCloseoutTopLevelFields(jsonText); err != nil {
		return governedCloseoutOutput{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(jsonText))
	var output governedCloseoutOutput
	if err := decoder.Decode(&output); err != nil {
		return governedCloseoutOutput{}, governedCloseoutError{category: governedCloseoutInvalidJSON, err: err}
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err == nil {
		return governedCloseoutOutput{}, governedCloseoutError{category: governedCloseoutInvalidJSON, err: errors.New("multiple json values")}
	} else if !errors.Is(err, io.EOF) {
		return governedCloseoutOutput{}, governedCloseoutError{category: governedCloseoutInvalidJSON, err: err}
	}
	return output, nil
}

func normalizeGovernedCloseoutJSON(jsonText string) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonText), &fields); err != nil {
		return "", governedCloseoutError{category: governedCloseoutInvalidJSON, err: err}
	}
	if rawSummary, ok := fields["summary"]; ok {
		if _, hasOutcome := fields["outcome"]; !hasOutcome {
			fields["outcome"] = rawSummary
		}
		delete(fields, "summary")
		jsonText = ""
	}
	for _, field := range []string{"changed_files", "files_changed", "modified_files", "created_files", "deleted_files"} {
		if _, ok := fields[field]; ok {
			delete(fields, field)
			jsonText = ""
		}
	}
	if normalizeGovernedCloseoutBoundedTextField(fields, "outcome", closeoutWorkTaskTextMax) {
		jsonText = ""
	}
	if normalizeGovernedCloseoutBoundedTextField(fields, "safe_next_action", closeoutWorkTaskTextMax) {
		jsonText = ""
	}
	rawTasks, ok := fields["child_tasks"]
	if !ok || len(bytes.TrimSpace(rawTasks)) == 0 || string(bytes.TrimSpace(rawTasks)) == "null" {
		if jsonText != "" {
			return jsonText, nil
		}
		encoded, err := json.Marshal(fields)
		if err != nil {
			return "", governedCloseoutError{category: governedCloseoutInvalidJSON, err: err}
		}
		return string(encoded), nil
	}
	var tasks []map[string]json.RawMessage
	if err := json.Unmarshal(rawTasks, &tasks); err != nil {
		return "", governedCloseoutError{category: governedCloseoutInvalidJSON, err: err}
	}
	changed := jsonText == ""
	textArrayFields := []string{
		"evidence_needed",
		"context_pack_refs",
		"files_to_read",
		"files_to_edit",
		"likely_files_affected",
		"dependency_task_ids",
		"acceptance_criteria",
		"stop_conditions",
		"verifier_ladder",
		"downstream_impact_refs",
	}
	for index, task := range tasks {
		if normalizeGovernedCloseoutObjectTextField(task, "decomposition_quality") {
			changed = true
		}
		if normalizeGovernedCloseoutObjectTextField(task, "regression_test_applicability") {
			changed = true
		}
		if normalizeGovernedCloseoutObjectTextField(task, "output_contract") {
			changed = true
		}
		for _, field := range textArrayFields {
			if normalizeGovernedCloseoutObjectTextArrayField(task, field) {
				changed = true
			}
		}
		tasks[index] = task
	}
	if !changed {
		return jsonText, nil
	}
	encodedTasks, err := json.Marshal(tasks)
	if err != nil {
		return "", governedCloseoutError{category: governedCloseoutInvalidJSON, err: err}
	}
	fields["child_tasks"] = encodedTasks
	encoded, err := json.Marshal(fields)
	if err != nil {
		return "", governedCloseoutError{category: governedCloseoutInvalidJSON, err: err}
	}
	return string(encoded), nil
}

func normalizeGovernedCloseoutObjectTextArrayField(task map[string]json.RawMessage, field string) bool {
	rawValue, ok := task[field]
	if !ok {
		return false
	}
	var values []json.RawMessage
	if err := json.Unmarshal(rawValue, &values); err != nil {
		return false
	}
	changed := false
	normalized := make([]json.RawMessage, 0, len(values))
	for _, rawItem := range values {
		if rawJSONValueIsStringOrNull(rawItem) {
			normalized = append(normalized, rawItem)
			continue
		}
		value, err := closeoutTextFromObject(rawItem)
		if err != nil {
			normalized = append(normalized, rawItem)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			normalized = append(normalized, rawItem)
			continue
		}
		normalized = append(normalized, encoded)
		changed = true
	}
	if !changed {
		return false
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return false
	}
	task[field] = encoded
	return true
}

func normalizeGovernedCloseoutBoundedTextField(fields map[string]json.RawMessage, field string, max int) bool {
	rawValue, ok := fields[field]
	if !ok {
		return false
	}
	var value string
	if err := json.Unmarshal(rawValue, &value); err != nil {
		if string(bytes.TrimSpace(rawValue)) == "null" {
			value = ""
		} else {
			extracted, extractErr := closeoutTextFromObject(rawValue)
			if extractErr != nil {
				return false
			}
			value = extracted
		}
	}
	value = strings.TrimSpace(value)
	if len(value) <= max && rawJSONValueIsStringOrNull(rawValue) {
		return false
	}
	if len(value) > max {
		value = value[:max]
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	fields[field] = encoded
	return true
}

func normalizeGovernedCloseoutObjectTextField(task map[string]json.RawMessage, field string) bool {
	rawValue, ok := task[field]
	if !ok || rawJSONValueIsStringOrNull(rawValue) {
		return false
	}
	value, err := closeoutTextFromObject(rawValue)
	if err != nil {
		return false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	task[field] = encoded
	return true
}

func closeoutTextFromObject(raw json.RawMessage) (string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", err
	}
	for _, key := range []string{"status", "quality", "value", "state", "decision", "applicability", "summary", "assessment", "result", "reason", "rationale", "confidence", "ref", "path", "file", "criterion", "condition", "command"} {
		rawValue, ok := object[key]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(rawValue, &value); err == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
	}
	if value := firstNestedString(object); value != "" {
		return value, nil
	}
	return "", errors.New("closeout text object lacks string value")
}

func firstNestedString(object map[string]json.RawMessage) string {
	for _, rawValue := range object {
		var value string
		if err := json.Unmarshal(rawValue, &value); err == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(rawValue, &nested); err == nil {
			if value := firstNestedString(nested); value != "" {
				return value
			}
		}
	}
	return ""
}

func validateGovernedCloseoutTopLevelFields(jsonText string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonText), &fields); err != nil {
		return governedCloseoutError{category: governedCloseoutInvalidJSON, err: err}
	}
	allowed := map[string]struct{}{
		"closeout_action":      {},
		"outcome":              {},
		"safe_next_action":     {},
		"evidence_refs":        {},
		"verifier_result_refs": {},
		"child_tasks":          {},
		"block_reason":         {},
		"failure_reason":       {},
	}
	for field := range fields {
		if _, ok := allowed[field]; !ok {
			return governedCloseoutError{category: governedCloseoutInvalidJSON, err: fmt.Errorf("unknown top-level field %q", field)}
		}
	}
	if raw, ok := fields["child_tasks"]; ok {
		if err := validateGovernedCloseoutChildTaskFieldTypes(raw); err != nil {
			return err
		}
	}
	return nil
}

func validateGovernedCloseoutChildTaskFieldTypes(raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	var tasks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &tasks); err != nil {
		return governedCloseoutError{category: governedCloseoutInvalidJSON, err: err}
	}
	stringFields := []string{
		"task_ref",
		"title",
		"description",
		"status",
		"owner_agent",
		"verification_requirement",
		"expected_output",
		"failure_criteria",
		"review_gate",
		"resume_instructions",
		"decomposition_quality",
		"regression_test_applicability",
		"output_contract",
	}
	textArrayFields := []string{
		"evidence_needed",
		"context_pack_refs",
		"files_to_read",
		"files_to_edit",
		"likely_files_affected",
		"dependency_task_ids",
		"acceptance_criteria",
		"stop_conditions",
		"verifier_ladder",
		"downstream_impact_refs",
	}
	for _, task := range tasks {
		for _, field := range stringFields {
			if rawField, ok := task[field]; ok && !rawJSONValueIsStringOrNull(rawField) {
				return governedCloseoutError{category: governedCloseoutValidationFailed, err: fmt.Errorf("child_tasks.%s must be string", field)}
			}
		}
		for _, field := range textArrayFields {
			rawField, ok := task[field]
			if !ok {
				continue
			}
			if err := validateGovernedCloseoutTextArrayFieldTypes(rawField, field); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateGovernedCloseoutTextArrayFieldTypes(raw json.RawMessage, field string) error {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: fmt.Errorf("child_tasks.%s must be array of strings", field)}
	}
	for _, rawValue := range values {
		if !rawJSONValueIsStringOrNull(rawValue) {
			return governedCloseoutError{category: governedCloseoutValidationFailed, err: fmt.Errorf("child_tasks.%s must be array of strings", field)}
		}
	}
	return nil
}

func rawJSONValueIsStringOrNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] == '"'
}

func extractSingleJSONObject(message string) (string, error) {
	message = strings.TrimSpace(message)
	if strings.HasPrefix(message, "{") && strings.HasSuffix(message, "}") {
		return message, nil
	}
	fenced := extractFencedJSONObjects(message)
	if len(fenced) == 1 {
		return fenced[0], nil
	}
	if len(fenced) > 1 {
		return "", errors.New("final message contains multiple json fences")
	}
	objects := extractBalancedJSONObjects(message)
	if len(objects) == 1 {
		return objects[0], nil
	}
	if len(objects) > 1 {
		return "", errors.New("final message contains multiple json objects")
	}
	return "", errors.New("final message must contain exactly one json object")
}

func extractFencedJSONObjects(message string) []string {
	lines := strings.Split(message, "\n")
	var objects []string
	for i := 0; i < len(lines); i++ {
		open := strings.TrimSpace(lines[i])
		if open != "```" && !strings.HasPrefix(strings.ToLower(open), "```json") {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) != "```" {
				continue
			}
			body := strings.TrimSpace(strings.Join(lines[i+1:j], "\n"))
			if strings.HasPrefix(body, "{") && strings.HasSuffix(body, "}") {
				objects = append(objects, body)
			}
			i = j
			break
		}
	}
	return objects
}

func extractBalancedJSONObjects(message string) []string {
	var objects []string
	inString := false
	escaped := false
	depth := 0
	start := -1
	for i, r := range message {
		if depth > 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == '"' {
				inString = !inString
				continue
			}
			if inString {
				continue
			}
		}
		if r == '{' && !inString {
			if depth == 0 {
				start = i
			}
			depth++
			continue
		}
		if r == '}' && depth > 0 && !inString {
			depth--
			if depth == 0 && start >= 0 {
				objects = append(objects, message[start:i+1])
				start = -1
			}
		}
	}
	return objects
}

func validateGovernedCloseoutOutput(output governedCloseoutOutput, wrapper runnerWorkTaskMetadata) error {
	action := strings.TrimSpace(output.CloseoutAction)
	switch action {
	case "needs_review", "block", "fail":
	default:
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("invalid closeout_action")}
	}
	if unsafeText(output.Outcome) || unsafeText(output.SafeNextAction) || unsafeText(output.BlockReason) || unsafeText(output.FailureReason) {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("unsafe closeout text")}
	}
	if err := validateGovernedCloseoutTextLimit(output.Outcome, "outcome"); err != nil {
		return err
	}
	if err := validateGovernedCloseoutTextLimit(output.BlockReason, "block_reason"); err != nil {
		return err
	}
	if err := validateGovernedCloseoutTextLimit(output.FailureReason, "failure_reason"); err != nil {
		return err
	}
	if action == "block" && strings.TrimSpace(output.BlockReason) == "" {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("block_reason required")}
	}
	if action == "fail" && strings.TrimSpace(output.FailureReason) == "" {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("failure_reason required")}
	}
	if action == "needs_review" && len(output.EvidenceRefs) == 0 {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("evidence_refs required")}
	}
	for _, ref := range append(append([]string{}, output.EvidenceRefs...), output.VerifierRefs...) {
		if !safeCloseoutRef(ref) {
			return governedCloseoutError{category: governedCloseoutValidationFailed, err: fmt.Errorf("unsafe ref %q", ref)}
		}
	}
	if strings.TrimSpace(wrapper.TaskRef) == "decompose-work-plan" && action == "needs_review" && len(output.ChildTasks) == 0 {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("decompose-work-plan requires child_tasks or block")}
	}
	for _, task := range output.ChildTasks {
		if err := validateGovernedChildTask(task); err != nil {
			return err
		}
	}
	return nil
}

func validateGovernedCloseoutTextLimit(value string, name string) error {
	if len(strings.TrimSpace(value)) > closeoutWorkTaskTextMax {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: fmt.Errorf("%s is too long", name)}
	}
	return nil
}

func validateGovernedChildTask(task governedCloseoutWorkTask) error {
	required := map[string]string{
		"task_ref":                 task.TaskRef,
		"title":                    task.Title,
		"description":              task.Description,
		"verification_requirement": task.VerificationRequirement,
		"expected_output":          task.ExpectedOutput,
		"failure_criteria":         task.FailureCriteria,
		"resume_instructions":      task.ResumeInstructions,
		"decomposition_quality":    task.DecompositionQuality,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return governedCloseoutError{category: governedCloseoutValidationFailed, err: fmt.Errorf("child task %s required", name)}
		}
		if unsafeText(value) {
			return governedCloseoutError{category: governedCloseoutValidationFailed, err: fmt.Errorf("unsafe child task %s", name)}
		}
	}
	if len(task.Description) > closeoutChildTaskDescriptionMax ||
		len(task.VerificationRequirement) > closeoutWorkTaskTextMax ||
		len(task.ExpectedOutput) > closeoutWorkTaskTextMax ||
		len(task.FailureCriteria) > closeoutWorkTaskTextMax ||
		len(task.ReviewGate) > closeoutWorkTaskTextMax ||
		len(task.RegressionApplicability) > closeoutWorkTaskTextMax ||
		len(task.OutputContract) > closeoutWorkTaskTextMax {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("child task metadata exceeds Work Task REST limits")}
	}
	if len(task.Title) > 200 {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("child task title exceeds Work Task REST limits")}
	}
	if strings.TrimSpace(task.OwnerAgent) != "" && !safeCloseoutRef(task.OwnerAgent) {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("unsafe child task owner_agent")}
	}
	if !safeCloseoutRef(task.TaskRef) {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("unsafe child task_ref")}
	}
	switch strings.TrimSpace(task.Status) {
	case "", "planned", "ready", "blocked":
	default:
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("child task status must be planned, ready, or blocked")}
	}
	if len(task.EvidenceNeeded) == 0 {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("child task evidence_needed required")}
	}
	if err := validateGovernedChildTextList(task.EvidenceNeeded, "evidence_needed", closeoutWorkTaskShortTextMax); err != nil {
		return err
	}
	if err := validateGovernedChildTextList(task.AcceptanceCriteria, "acceptance_criteria", closeoutWorkTaskTextMax); err != nil {
		return err
	}
	if err := validateGovernedChildTextList(task.StopConditions, "stop_conditions", closeoutWorkTaskTextMax); err != nil {
		return err
	}
	if err := validateGovernedChildTextList(task.VerifierLadder, "verifier_ladder", closeoutWorkTaskTextMax); err != nil {
		return err
	}
	if len(task.AcceptanceCriteria) == 0 || len(task.StopConditions) == 0 || len(task.VerifierLadder) == 0 ||
		strings.TrimSpace(task.RegressionApplicability) == "" || len(task.DownstreamImpactRefs) == 0 ||
		strings.TrimSpace(task.OutputContract) == "" {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("child task missing governance metadata")}
	}
	if len(task.FilesToRead) == 0 && len(task.FilesToEdit) == 0 && !strings.Contains(strings.ToLower(task.Description), "discovery") {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("child task requires files_to_read, files_to_edit, or explicit discovery scope")}
	}
	for _, value := range append(append(append([]string{}, task.FilesToRead...), task.FilesToEdit...), task.LikelyFilesAffected...) {
		if !safeProjectPath(value) {
			return governedCloseoutError{category: governedCloseoutValidationFailed, err: fmt.Errorf("unsafe child task path %q", value)}
		}
	}
	for _, ref := range append(append([]string{}, task.ContextPackRefs...), task.DependencyTaskIDs...) {
		if strings.TrimSpace(ref) != "" && !safeCloseoutRef(ref) {
			return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("unsafe child task ref")}
		}
	}
	for _, ref := range task.DownstreamImpactRefs {
		if !safeCloseoutRef(ref) {
			return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("unsafe child task downstream impact ref")}
		}
	}
	return nil
}

func validateGovernedChildTextList(values []string, name string, max int) error {
	for _, value := range values {
		if len(strings.TrimSpace(value)) > max {
			return governedCloseoutError{category: governedCloseoutValidationFailed, err: fmt.Errorf("child task %s exceeds Work Task REST limits", name)}
		}
		if unsafeText(value) {
			return governedCloseoutError{category: governedCloseoutValidationFailed, err: fmt.Errorf("unsafe child task %s", name)}
		}
	}
	return nil
}

func unsafeText(value string) bool {
	value = strings.TrimSpace(value)
	lower := redactCloseoutSafeProhibitionPhrases(strings.ToLower(value))
	return strings.ContainsAny(value, "\x00\r\n") ||
		closeoutEmailPattern.MatchString(value) ||
		closeoutPhonePattern.MatchString(value) ||
		strings.Contains(lower, "secret=") ||
		strings.Contains(lower, "token=") ||
		strings.Contains(lower, "credential=") ||
		strings.Contains(lower, "api_key") ||
		strings.Contains(lower, "openai_api_key") ||
		strings.Contains(lower, "anthropic_api_key") ||
		strings.Contains(lower, "provider_payload") ||
		strings.Contains(lower, "provider payload") ||
		strings.Contains(lower, "raw_prompt") ||
		strings.Contains(lower, "raw_completion") ||
		strings.Contains(lower, "raw_stderr") ||
		strings.Contains(lower, "raw stderr") ||
		strings.Contains(lower, "raw log") ||
		strings.Contains(lower, "raw source") ||
		strings.Contains(lower, "source dump") ||
		strings.Contains(lower, "begin private key") ||
		strings.Contains(lower, "ghp_") ||
		strings.HasPrefix(lower, "sk-") ||
		strings.Contains(lower, " sk-") ||
		strings.Contains(lower, "=sk-") ||
		containsCloseoutRootMarker(value) ||
		strings.HasPrefix(value, "/") ||
		(len(value) >= 3 && value[1] == ':' && (value[2] == '\\' || value[2] == '/'))
}

func redactCloseoutSafeProhibitionPhrases(value string) string {
	if strings.Contains(value, "no raw prompt") || strings.Contains(value, "never store") || strings.Contains(value, "must not store") || strings.Contains(value, "do not store") || strings.Contains(value, "must not expose") || strings.Contains(value, "do not expose") || strings.Contains(value, "must not include") || strings.Contains(value, "do not include") {
		for _, marker := range []string{
			"raw prompts",
			"raw prompt",
			"raw completions",
			"raw completion",
			"raw_prompt",
			"raw_completion",
			"raw source",
			"source dumps",
			"source dump",
			"raw stderr",
			"raw_stderr",
			"provider payloads",
			"provider payload",
			"provider_payload",
			"credentials",
			"credential",
			"secrets",
			"secret",
			"roots",
			"root",
			"paths",
			"path",
		} {
			value = strings.ReplaceAll(value, marker, "")
		}
	}
	return value
}

func containsCloseoutRootMarker(value string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(value, "\\", "/"))
	return strings.Contains(normalized, "/home/") ||
		strings.Contains(normalized, "/users/") ||
		strings.Contains(normalized, "wsl.localhost/") ||
		strings.Contains(normalized, "c:/") ||
		closeoutDrivePattern.MatchString(normalized)
}

func safeCloseoutRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	return !strings.Contains(ref, "\\") &&
		!strings.Contains(ref, "..") &&
		!strings.HasPrefix(ref, "/") &&
		!filepath.IsAbs(ref) &&
		!unsafeText(ref) &&
		closeoutRefPattern.MatchString(ref)
}

func safeProjectPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || unsafeText(path) || strings.HasPrefix(path, "~") {
		return false
	}
	clean := filepath.Clean(filepath.ToSlash(path))
	return clean != "." && clean == filepath.ToSlash(path) && !strings.HasPrefix(clean, "../") && clean != ".."
}

func (client *runnerClient) applyGovernedCloseoutFromOutput(ctx context.Context, projectID string, claimed projectautomation.ClaimedRun, wrapper runnerWorkTaskMetadata, message string) error {
	output, err := parseGovernedCloseoutOutput(message)
	if err != nil {
		return err
	}
	if err := validateGovernedCloseoutOutput(output, wrapper); err != nil {
		return err
	}
	if err := client.applyGovernedCloseout(ctx, projectID, claimed, output); err != nil {
		return governedCloseoutError{category: governedCloseoutApplyFailed, err: err}
	}
	task, err := client.getWorkTaskMetadata(ctx, projectID, claimed.Run.TaskID)
	if err != nil {
		return governedCloseoutError{category: governedCloseoutReadbackFailed, err: err}
	}
	if !taskHasGovernedCloseout(task) {
		return governedCloseoutError{category: governedCloseoutReadbackFailed, err: errors.New("wrapper remained open after closeout apply")}
	}
	return nil
}

func (client *runnerClient) applyGovernedCloseout(ctx context.Context, projectID string, claimed projectautomation.ClaimedRun, output governedCloseoutOutput) error {
	runID := strings.TrimSpace(claimed.Run.ID)
	traceID := firstNonEmpty(claimed.Run.TraceID, runID)
	planID := firstNonEmpty(claimed.Run.PlanID, claimed.CodexInput.PlanID)
	taskID := firstNonEmpty(claimed.Run.TaskID, claimed.CodexInput.TaskID)
	if taskID == "" || runID == "" {
		return fmt.Errorf("%w: governed closeout requires task and run ids", projectautomation.ErrInvalidInput)
	}
	if len(output.ChildTasks) > 0 {
		if planID == "" {
			return fmt.Errorf("%w: child tasks require plan id", projectautomation.ErrInvalidInput)
		}
		for _, child := range output.ChildTasks {
			input := governedChildTaskCreateInput(child, runID, traceID)
			if _, err := client.post(ctx, fmt.Sprintf("/api/v1/projects/%s/work-plans/%s/tasks", url.PathEscape(projectID), url.PathEscape(planID)), input, nil); err != nil {
				return fmt.Errorf("child_task_create_failed: %w", err)
			}
		}
	}
	pathBase := fmt.Sprintf("/api/v1/projects/%s/work-tasks/%s", url.PathEscape(projectID), url.PathEscape(taskID))
	for _, ref := range output.EvidenceRefs {
		input := struct {
			Ref             string `json:"ref"`
			AttachedByRunID string `json:"attached_by_run_id,omitempty"`
			TraceID         string `json:"trace_id,omitempty"`
			Note            string `json:"note,omitempty"`
		}{Ref: ref, AttachedByRunID: runID, TraceID: traceID, Note: "runner-applied governed closeout evidence"}
		if _, err := client.post(ctx, pathBase+"/evidence", input, nil); err != nil {
			return fmt.Errorf("wrapper_evidence_attach_failed: %w", err)
		}
	}
	switch strings.TrimSpace(output.CloseoutAction) {
	case "needs_review":
		input := struct {
			RunID              string   `json:"run_id,omitempty"`
			TraceID            string   `json:"trace_id,omitempty"`
			Status             string   `json:"status"`
			SafeNextAction     string   `json:"safe_next_action,omitempty"`
			Outcome            string   `json:"outcome,omitempty"`
			VerifierResultRefs []string `json:"verifier_result_refs,omitempty"`
		}{RunID: runID, TraceID: traceID, Status: "needs_review", SafeNextAction: output.SafeNextAction, Outcome: output.Outcome, VerifierResultRefs: output.VerifierRefs}
		if _, err := client.post(ctx, pathBase+"/status", input, nil); err != nil {
			return fmt.Errorf("wrapper_status_update_failed: %w", err)
		}
		if err := client.attachGovernedCloseoutVerifierRefs(ctx, pathBase, runID, traceID, output.VerifierRefs); err != nil {
			return fmt.Errorf("wrapper_verifier_attach_failed: %w", err)
		}
		return nil
	case "block":
		if err := client.attachGovernedCloseoutVerifierRefs(ctx, pathBase, runID, traceID, output.VerifierRefs); err != nil {
			return fmt.Errorf("wrapper_verifier_attach_failed: %w", err)
		}
		input := struct {
			RunID              string `json:"run_id,omitempty"`
			TraceID            string `json:"trace_id,omitempty"`
			BlockedReason      string `json:"blocked_reason"`
			ResumeInstructions string `json:"resume_instructions"`
			SafeNextAction     string `json:"safe_next_action,omitempty"`
		}{RunID: runID, TraceID: traceID, BlockedReason: output.BlockReason, ResumeInstructions: output.SafeNextAction, SafeNextAction: output.SafeNextAction}
		_, err := client.post(ctx, pathBase+"/block", input, nil)
		if err != nil {
			return fmt.Errorf("wrapper_block_failed: %w", err)
		}
		return nil
	case "fail":
		if err := client.attachGovernedCloseoutVerifierRefs(ctx, pathBase, runID, traceID, output.VerifierRefs); err != nil {
			return fmt.Errorf("wrapper_verifier_attach_failed: %w", err)
		}
		input := struct {
			RunID              string `json:"run_id,omitempty"`
			TraceID            string `json:"trace_id,omitempty"`
			Outcome            string `json:"outcome"`
			ResumeInstructions string `json:"resume_instructions,omitempty"`
			SafeNextAction     string `json:"safe_next_action,omitempty"`
		}{RunID: runID, TraceID: traceID, Outcome: firstNonEmpty(output.FailureReason, output.Outcome), ResumeInstructions: output.SafeNextAction, SafeNextAction: output.SafeNextAction}
		_, err := client.post(ctx, pathBase+"/fail", input, nil)
		if err != nil {
			return fmt.Errorf("wrapper_fail_failed: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("%w: invalid closeout action", projectautomation.ErrInvalidInput)
	}
}

func (client *runnerClient) attachGovernedCloseoutVerifierRefs(ctx context.Context, pathBase string, runID string, traceID string, refs []string) error {
	for _, ref := range refs {
		input := struct {
			Ref             string `json:"ref"`
			AttachedByRunID string `json:"attached_by_run_id,omitempty"`
			TraceID         string `json:"trace_id,omitempty"`
			Note            string `json:"note,omitempty"`
		}{Ref: ref, AttachedByRunID: runID, TraceID: traceID, Note: "runner-applied governed closeout verifier"}
		if _, err := client.post(ctx, pathBase+"/verifier-results", input, nil); err != nil {
			return err
		}
	}
	return nil
}

func governedChildTaskCreateInput(child governedCloseoutWorkTask, runID string, traceID string) any {
	return struct {
		TaskRef                 string   `json:"task_ref"`
		Title                   string   `json:"title"`
		Description             string   `json:"description,omitempty"`
		Status                  string   `json:"status,omitempty"`
		OwnerAgent              string   `json:"owner_agent,omitempty"`
		RunID                   string   `json:"run_id,omitempty"`
		TraceID                 string   `json:"trace_id,omitempty"`
		EvidenceNeeded          []string `json:"evidence_needed,omitempty"`
		ContextPackRefs         []string `json:"context_pack_refs,omitempty"`
		FilesToRead             []string `json:"files_to_read,omitempty"`
		FilesToEdit             []string `json:"files_to_edit,omitempty"`
		LikelyFilesAffected     []string `json:"likely_files_affected,omitempty"`
		DependencyTaskIDs       []string `json:"dependency_task_ids,omitempty"`
		VerificationRequirement string   `json:"verification_requirement"`
		ExpectedOutput          string   `json:"expected_output,omitempty"`
		FailureCriteria         string   `json:"failure_criteria,omitempty"`
		ReviewGate              string   `json:"review_gate,omitempty"`
		ResumeInstructions      string   `json:"resume_instructions,omitempty"`
		DecompositionQuality    string   `json:"decomposition_quality,omitempty"`
		AcceptanceCriteria      []string `json:"acceptance_criteria,omitempty"`
		StopConditions          []string `json:"stop_conditions,omitempty"`
		VerifierLadder          []string `json:"verifier_ladder,omitempty"`
		RegressionApplicability string   `json:"regression_test_applicability,omitempty"`
		DownstreamImpactRefs    []string `json:"downstream_impact_refs,omitempty"`
		OutputContract          string   `json:"output_contract,omitempty"`
	}{
		TaskRef: child.TaskRef, Title: child.Title, Description: child.Description, Status: child.Status, OwnerAgent: child.OwnerAgent,
		RunID: runID, TraceID: traceID, EvidenceNeeded: child.EvidenceNeeded, ContextPackRefs: child.ContextPackRefs,
		FilesToRead: child.FilesToRead, FilesToEdit: child.FilesToEdit, LikelyFilesAffected: child.LikelyFilesAffected,
		DependencyTaskIDs: child.DependencyTaskIDs, VerificationRequirement: child.VerificationRequirement, ExpectedOutput: child.ExpectedOutput,
		FailureCriteria: child.FailureCriteria, ReviewGate: child.ReviewGate, ResumeInstructions: child.ResumeInstructions,
		DecompositionQuality: child.DecompositionQuality, AcceptanceCriteria: child.AcceptanceCriteria,
		StopConditions: child.StopConditions, VerifierLadder: child.VerifierLadder,
		RegressionApplicability: child.RegressionApplicability, DownstreamImpactRefs: child.DownstreamImpactRefs,
		OutputContract: child.OutputContract,
	}
}

func governedCloseoutJSONPromptExample() string {
	var buffer bytes.Buffer
	_ = json.NewEncoder(&buffer).Encode(governedCloseoutOutput{
		CloseoutAction: "needs_review",
		Outcome:        "bounded outcome summary",
		SafeNextAction: "review generated child tasks",
		EvidenceRefs:   []string{"evidence.governed-closeout"},
		VerifierRefs:   []string{"verifier.governed-closeout"},
		ChildTasks: []governedCloseoutWorkTask{{
			TaskRef: "implement-ticket-slice", Title: "Implement Ticket Slice", Description: "Implement one bounded code slice from source-verified ticket evidence.",
			OwnerAgent: "implementation-worker", EvidenceNeeded: []string{"source-evidence"}, FilesToRead: []string{"apps/example/file.ts"},
			FilesToEdit: []string{"apps/example/file.ts"}, LikelyFilesAffected: []string{"apps/example"},
			VerificationRequirement: "focused regression test", ExpectedOutput: "code change and focused test",
			FailureCriteria: "block on missing source evidence", ReviewGate: "implementation-review",
			ResumeInstructions: "claim this task and read listed files", DecompositionQuality: "ready",
		}},
	})
	return strings.TrimSpace(buffer.String())
}
