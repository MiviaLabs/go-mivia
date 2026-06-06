package projectworkflow

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	workflowIssueError   = "error"
	workflowIssueWarning = "warning"
)

var (
	workflowSafeRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,199}$`)
	workflowEmailPattern   = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	workflowPhonePattern   = regexp.MustCompile(`\+?[0-9][0-9 .()\-]{7,}[0-9]`)
)

// ValidateWorkflow checks workflow metadata for unsafe content and references.
func ValidateWorkflow(def WorkflowDefinition) []WorkflowValidationIssue {
	v := workflowValidator{}
	v.validateRequired(def)
	v.validateWorkflowRefs(def)
	v.validateAgents(def.Agents)
	v.validateSteps(def.Agents, def.Steps)
	v.validateGates(def)
	v.validatePermissionSnapshots(def.PermissionSnapshots)
	return v.issues
}

type workflowValidator struct {
	issues []WorkflowValidationIssue
}

func (v *workflowValidator) validateRequired(def WorkflowDefinition) {
	v.requireRef(def.ID, "id")
	v.requireText(def.Title, "title", 200)
	v.requireText(def.Purpose, "purpose", 500)
	if len(def.Agents) == 0 {
		v.add("required", "agents", "workflow must declare at least one agent")
	}
	if len(def.Steps) == 0 {
		v.add("required", "steps", "workflow must declare at least one step")
	}
}

func (v *workflowValidator) validateWorkflowRefs(def WorkflowDefinition) {
	v.optionalRef(def.ProjectID, "project_id")
	v.optionalRef(def.WorkflowRef, "workflow_ref")
	v.optionalRef(def.Status, "status")
	v.optionalRef(def.CreatedByRunID, "created_by_run_id")
	v.optionalRef(def.TraceID, "trace_id")
}

func (v *workflowValidator) validateAgents(agents []WorkflowAgentDefinition) {
	seen := map[string]bool{}
	for i, agent := range agents {
		base := "agents[" + strconv.Itoa(i) + "]"
		if v.requireRef(agent.ID, base+".id") {
			if seen[agent.ID] {
				v.add("duplicate_ref", base+".id", "agent id must be unique")
			}
			seen[agent.ID] = true
		}
		v.optionalText(agent.DisplayName, base+".display_name", 200)
		v.requireText(agent.Purpose, base+".purpose", 500)
		v.optionalText(agent.Instructions, base+".instructions", 2000)
		v.refList(agent.AllowedSkills, base+".allowed_skills")
		v.refList(agent.AllowedTools, base+".allowed_tools")
		v.textList(agent.AllowedCommands, base+".allowed_commands", 200)
		v.textList(agent.DeniedCommands, base+".denied_commands", 200)
		v.optionalRef(agent.WorkspaceMode, base+".workspace_mode")
		v.optionalRef(agent.NetworkPolicy, base+".network_policy")
		v.optionalRef(agent.MaxRuntime, base+".max_runtime")
		if agent.SecretPolicy == "" {
			v.add("defaulted", base+".secret_policy", "agent secret_policy defaults to deny", workflowIssueWarning)
		} else if agent.SecretPolicy != "deny" {
			v.add("unsafe_policy", base+".secret_policy", "agent secret_policy must be deny")
		}
		if agent.LogPolicy == "" {
			v.add("defaulted", base+".log_policy", "agent log_policy defaults to metadata_only", workflowIssueWarning)
		} else if agent.LogPolicy != "metadata_only" {
			v.add("unsafe_policy", base+".log_policy", "agent log_policy must be metadata_only")
		}
		if agent.MaxRetries < 0 {
			v.add("invalid_value", base+".max_retries", "agent max_retries cannot be negative")
		}
	}
}

func (v *workflowValidator) validateSteps(agents []WorkflowAgentDefinition, steps []WorkflowStep) {
	stepIDs := map[string]bool{}
	agentIDs := map[string]bool{}
	for _, agent := range agents {
		if agent.ID != "" {
			agentIDs[agent.ID] = true
		}
	}
	for i, step := range steps {
		base := "steps[" + strconv.Itoa(i) + "]"
		if v.requireRef(step.ID, base+".id") {
			if stepIDs[step.ID] {
				v.add("duplicate_ref", base+".id", "step id must be unique")
			}
			stepIDs[step.ID] = true
		}
		if !knownStepKind(step.Kind) {
			v.add("unknown_step_kind", base+".kind", "step kind is not supported")
		}
		v.requireText(step.Title, base+".title", 200)
		v.optionalRef(step.Agent, base+".agent")
		v.refList(step.DependsOn, base+".depends_on")
		v.optionalText(step.Description, base+".description", 1000)
		v.textList(step.EvidenceNeeded, base+".evidence_needed", 200)
		v.refList(step.ContextPackRefs, base+".context_pack_refs")
		v.safePathList(step.FilesToRead, base+".files_to_read")
		v.safePathList(step.FilesToEdit, base+".files_to_edit")
		v.safePathList(step.LikelyFilesAffected, base+".likely_files_affected")
		v.optionalText(step.VerificationRequirement, base+".verification_requirement", 500)
		v.optionalText(step.ExpectedOutput, base+".expected_output", 500)
		v.optionalText(step.FailureCriteria, base+".failure_criteria", 500)
		v.optionalText(step.ReviewGate, base+".review_gate", 500)
		v.resumeInstructions(step.ResumeInstructions, base+".resume_instructions")
		v.optionalRef(step.AutomationStatus, base+".automation_status")
		v.optionalRef(step.TriggerKind, base+".trigger_kind")
		v.optionalRef(step.SchedulePolicy, base+".schedule_policy")
		if step.MaxParallelTasks < 0 {
			v.add("invalid_value", base+".max_parallel_tasks", "max_parallel_tasks must be positive when set")
		}
	}
	for i, step := range steps {
		base := "steps[" + strconv.Itoa(i) + "]"
		if step.Kind == WorkflowStepKindAutomation || step.Kind == WorkflowStepKindAutomationBatch {
			if step.Agent == "" {
				v.add("required", base+".agent", "automation steps must reference a declared agent")
			}
		}
		if step.Agent != "" && !agentIDs[step.Agent] {
			v.add("unknown_agent", base+".agent", "step references an unknown agent")
		}
		for _, dep := range step.DependsOn {
			if dep != "" && !stepIDs[dep] {
				v.add("unknown_dependency", base+".depends_on", "dependency references an unknown step")
			}
		}
	}
	if hasDependencyCycle(steps) {
		v.add("dependency_cycle", "steps", "step dependencies must not contain a cycle")
	}
}

func (v *workflowValidator) validateGates(def WorkflowDefinition) {
	stepIDs := map[string]bool{}
	for _, step := range def.Steps {
		stepIDs[step.ID] = true
	}
	agentIDs := map[string]bool{}
	for _, agent := range def.Agents {
		agentIDs[agent.ID] = true
	}
	seen := map[string]bool{}
	for i, gate := range def.ReviewGates {
		base := "review_gates[" + strconv.Itoa(i) + "]"
		if v.requireRef(gate.ID, base+".id") {
			if seen[gate.ID] {
				v.add("duplicate_ref", base+".id", "review gate id must be unique")
			}
			seen[gate.ID] = true
		}
		v.refList(gate.AppliesTo, base+".applies_to")
		for _, stepID := range gate.AppliesTo {
			if stepID != "" && !stepIDs[stepID] {
				v.add("unknown_step", base+".applies_to", "review gate applies_to references an unknown step")
			}
		}
		if v.requireRef(gate.ReviewerAgent, base+".reviewer_agent") && !agentIDs[gate.ReviewerAgent] {
			v.add("unknown_reviewer_agent", base+".reviewer_agent", "review gate reviewer_agent references an unknown agent")
		}
		if gate.Required {
			v.requireText(gate.Instructions, base+".instructions", 2000)
		} else {
			v.optionalText(gate.Instructions, base+".instructions", 2000)
		}
		v.refList(gate.RequiredArtifacts, base+".required_artifacts")
		for _, action := range gate.AllowedActions {
			if !knownReviewAction(action) {
				v.add("unknown_allowed_action", base+".allowed_actions", "allowed action is not supported")
			}
		}
	}
}

func (v *workflowValidator) validatePermissionSnapshots(snapshots []WorkflowPermissionSnapshot) {
	for i, snapshot := range snapshots {
		base := "permission_snapshots[" + strconv.Itoa(i) + "]"
		v.requireRef(snapshot.ID, base+".id")
		v.optionalRef(snapshot.ProjectID, base+".project_id")
		v.optionalRef(snapshot.AgentID, base+".agent_id")
		v.optionalRef(snapshot.WorkflowID, base+".workflow_id")
		v.optionalText(snapshot.Instructions, base+".instructions", 2000)
		v.refList(snapshot.AllowedSkills, base+".allowed_skills")
		v.refList(snapshot.AllowedTools, base+".allowed_tools")
		v.textList(snapshot.AllowedCommands, base+".allowed_commands", 200)
		v.textList(snapshot.DeniedCommands, base+".denied_commands", 200)
		v.optionalRef(snapshot.WorkspaceMode, base+".workspace_mode")
		v.optionalRef(snapshot.NetworkPolicy, base+".network_policy")
		v.optionalRef(snapshot.SecretPolicy, base+".secret_policy")
		v.optionalRef(snapshot.LogPolicy, base+".log_policy")
		v.optionalRef(snapshot.MaxRuntime, base+".max_runtime")
		v.optionalRef(snapshot.ContentHash, base+".content_hash")
		v.optionalRef(snapshot.CreatedByRunID, base+".created_by_run_id")
		v.optionalRef(snapshot.TraceID, base+".trace_id")
	}
}

func (v *workflowValidator) requireRef(value, field string) bool {
	if strings.TrimSpace(value) == "" {
		v.add("required", field, "field is required")
		return false
	}
	return v.optionalRef(value, field)
}

func (v *workflowValidator) optionalRef(value, field string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	if !safeWorkflowRef(value) {
		v.add("unsafe_ref", field, "field must be a safe metadata ref")
		return false
	}
	return true
}

func (v *workflowValidator) requireText(value, field string, max int) bool {
	if strings.TrimSpace(value) == "" {
		v.add("required", field, "field is required")
		return false
	}
	return v.optionalText(value, field, max)
}

func (v *workflowValidator) optionalText(value, field string, max int) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	if len(value) > max {
		v.add("too_long", field, "field is too long")
		return false
	}
	if containsUnsafeWorkflowText(value) || containsRootMarker(value) {
		v.add("unsafe_text", field, "field contains unsafe text marker")
		return false
	}
	return true
}

func (v *workflowValidator) resumeInstructions(value, field string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	if containsUnsafeWorkflowText(value) || containsRootMarker(value) {
		v.add("unsafe_text", field, "field contains unsafe text marker")
		return false
	}
	return true
}

func (v *workflowValidator) refList(values []string, field string) {
	for i, value := range values {
		v.optionalRef(value, field+"["+strconv.Itoa(i)+"]")
	}
}

func (v *workflowValidator) textList(values []string, field string, max int) {
	for i, value := range values {
		v.optionalText(value, field+"["+strconv.Itoa(i)+"]", max)
	}
}

func (v *workflowValidator) safePathList(values []string, field string) {
	for i, value := range values {
		pathField := field + "[" + strconv.Itoa(i) + "]"
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "\\") || strings.Contains(trimmed, "..") || strings.HasPrefix(trimmed, "/") || filepath.IsAbs(trimmed) || containsUnsafeWorkflowText(trimmed) || containsRootMarker(trimmed) {
			v.add("unsafe_path", pathField, "path must be relative metadata without roots or traversal")
		}
	}
}

func (v *workflowValidator) add(code, field, message string, severity ...string) {
	level := workflowIssueError
	if len(severity) > 0 {
		level = severity[0]
	}
	v.issues = append(v.issues, WorkflowValidationIssue{Code: code, Severity: level, FieldPath: field, Message: message})
}

func safeWorkflowRef(value string) bool {
	value = strings.TrimSpace(value)
	return workflowSafeRefPattern.MatchString(value) &&
		!strings.Contains(value, "\\") &&
		!strings.Contains(value, "..") &&
		!strings.HasPrefix(value, "/") &&
		!filepath.IsAbs(value) &&
		!workflowEmailPattern.MatchString(value) &&
		!containsRootMarker(value) &&
		!containsUnsafeWorkflowText(value)
}

func containsUnsafeWorkflowText(value string) bool {
	if workflowEmailPattern.MatchString(value) || workflowPhonePattern.MatchString(value) {
		return true
	}
	lower := strings.ToLower(value)
	markers := []string{
		"begin ",
		"api_key",
		"token=",
		"password=",
		"secret=",
		"credential=",
		"raw_prompt",
		"raw prompt",
		"raw_completion",
		"raw completion",
		"raw_stderr",
		"raw stderr",
		"raw_source",
		"raw source",
		"source dump",
		"provider_payload",
		"provider payload",
		"ghp_",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func containsRootMarker(value string) bool {
	lower := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"))
	return strings.Contains(lower, "c:/") ||
		strings.Contains(lower, "//wsl.localhost/") ||
		strings.Contains(lower, "/home/") ||
		strings.Contains(lower, "/users/") ||
		strings.Contains(lower, "/var/") ||
		strings.Contains(lower, "/tmp/") ||
		strings.Contains(lower, "/etc/")
}

func knownStepKind(kind string) bool {
	switch kind {
	case WorkflowStepKindWorkPlan, WorkflowStepKindWorkTask, WorkflowStepKindAutomation, WorkflowStepKindAutomationBatch, WorkflowStepKindReviewGate:
		return true
	default:
		return false
	}
}

func knownReviewAction(action string) bool {
	switch action {
	case ReviewGateDecisionApproved, ReviewGateDecisionRejected, ReviewGateDecisionNeedsChanges, ReviewGateDecisionBlocked:
		return true
	default:
		return false
	}
}

func hasDependencyCycle(steps []WorkflowStep) bool {
	graph := map[string][]string{}
	for _, step := range steps {
		graph[step.ID] = append([]string(nil), step.DependsOn...)
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return true
		}
		if visited[id] {
			return false
		}
		visiting[id] = true
		for _, dep := range graph[id] {
			if _, ok := graph[dep]; ok && visit(dep) {
				return true
			}
		}
		visiting[id] = false
		visited[id] = true
		return false
	}
	for id := range graph {
		if visit(id) {
			return true
		}
	}
	return false
}
