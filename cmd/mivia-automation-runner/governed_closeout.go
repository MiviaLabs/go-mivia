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
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
)

const (
	governedCloseoutOutputMissing    = "governed_closeout_output_missing"
	governedCloseoutInvalidJSON      = "governed_closeout_invalid_json"
	governedCloseoutValidationFailed = "governed_closeout_validation_failed"
	governedCloseoutApplyFailed      = "governed_closeout_apply_failed"
	governedCloseoutReadbackFailed   = "governed_closeout_readback_failed"
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
		return closeoutErr.category
	}
	return governedCloseoutApplyFailed
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
}

func createGovernedCloseoutSchemaFile() (string, func(), error) {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"closeout_action", "outcome", "safe_next_action"},
		"properties": map[string]any{
			"closeout_action":      map[string]any{"type": "string", "enum": []string{"needs_review", "block", "fail"}},
			"outcome":              map[string]any{"type": "string", "minLength": 1, "maxLength": 1200},
			"safe_next_action":     map[string]any{"type": "string", "maxLength": 1200},
			"evidence_refs":        closeoutRefArraySchema(),
			"verifier_result_refs": closeoutRefArraySchema(),
			"block_reason":         map[string]any{"type": "string", "maxLength": 1200},
			"failure_reason":       map[string]any{"type": "string", "maxLength": 1200},
			"child_tasks": map[string]any{
				"type":     "array",
				"maxItems": 0,
				// Child-task-producing closeouts skip Codex output-schema enforcement.
				// The runner owns that validation because the API's strict schema
				// subset cannot express permissive nested task packets.
				"items": map[string]any{"type": "string"},
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
	return map[string]any{"type": "string", "maxLength": 200, "pattern": "^[A-Za-z0-9][A-Za-z0-9._:-]*$"}
}

func closeoutRefArraySchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 100, "items": closeoutRefSchema()}
}

func closeoutPathArraySchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 300, "pattern": "^[A-Za-z0-9][A-Za-z0-9._/@+-]*$"}}
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
	decoder := json.NewDecoder(strings.NewReader(jsonText))
	decoder.DisallowUnknownFields()
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

func validateGovernedChildTask(task governedCloseoutWorkTask) error {
	required := []string{task.TaskRef, task.Title, task.Description, task.VerificationRequirement, task.ExpectedOutput, task.FailureCriteria, task.ResumeInstructions, task.DecompositionQuality}
	for _, value := range required {
		if strings.TrimSpace(value) == "" || unsafeText(value) {
			return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("child task missing required safe metadata")}
		}
	}
	if !safeCloseoutRef(task.TaskRef) {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("unsafe child task_ref")}
	}
	if len(task.EvidenceNeeded) == 0 {
		return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("child task evidence_needed required")}
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
		if strings.TrimSpace(ref) != "" && unsafeText(ref) {
			return governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("unsafe child task ref")}
		}
	}
	return nil
}

func unsafeText(value string) bool {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	return strings.ContainsAny(value, "\x00\r\n") ||
		strings.Contains(lower, "secret=") ||
		strings.Contains(lower, "token=") ||
		strings.Contains(lower, "raw log") ||
		strings.Contains(lower, "raw source") ||
		strings.Contains(value, "://") ||
		strings.HasPrefix(value, "/") ||
		(len(value) >= 3 && value[1] == ':' && (value[2] == '\\' || value[2] == '/'))
}

func safeCloseoutRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > 160 {
		return false
	}
	for _, r := range ref {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
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
				return err
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
			return err
		}
	}
	for _, ref := range output.VerifierRefs {
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
		_, err := client.post(ctx, pathBase+"/status", input, nil)
		return err
	case "block":
		input := struct {
			RunID              string `json:"run_id,omitempty"`
			TraceID            string `json:"trace_id,omitempty"`
			BlockedReason      string `json:"blocked_reason"`
			ResumeInstructions string `json:"resume_instructions"`
			SafeNextAction     string `json:"safe_next_action,omitempty"`
		}{RunID: runID, TraceID: traceID, BlockedReason: output.BlockReason, ResumeInstructions: output.SafeNextAction, SafeNextAction: output.SafeNextAction}
		_, err := client.post(ctx, pathBase+"/block", input, nil)
		return err
	case "fail":
		input := struct {
			RunID              string `json:"run_id,omitempty"`
			TraceID            string `json:"trace_id,omitempty"`
			Outcome            string `json:"outcome"`
			ResumeInstructions string `json:"resume_instructions,omitempty"`
			SafeNextAction     string `json:"safe_next_action,omitempty"`
		}{RunID: runID, TraceID: traceID, Outcome: firstNonEmpty(output.FailureReason, output.Outcome), ResumeInstructions: output.SafeNextAction, SafeNextAction: output.SafeNextAction}
		_, err := client.post(ctx, pathBase+"/fail", input, nil)
		return err
	default:
		return fmt.Errorf("%w: invalid closeout action", projectautomation.ErrInvalidInput)
	}
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
	}{
		TaskRef: child.TaskRef, Title: child.Title, Description: child.Description, Status: child.Status, OwnerAgent: child.OwnerAgent,
		RunID: runID, TraceID: traceID, EvidenceNeeded: child.EvidenceNeeded, ContextPackRefs: child.ContextPackRefs,
		FilesToRead: child.FilesToRead, FilesToEdit: child.FilesToEdit, LikelyFilesAffected: child.LikelyFilesAffected,
		DependencyTaskIDs: child.DependencyTaskIDs, VerificationRequirement: child.VerificationRequirement, ExpectedOutput: child.ExpectedOutput,
		FailureCriteria: child.FailureCriteria, ReviewGate: child.ReviewGate, ResumeInstructions: child.ResumeInstructions,
		DecompositionQuality: child.DecompositionQuality,
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
