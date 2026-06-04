package projectworkflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

var ErrInvalidInput = errors.New("invalid project workflow input")

type Store interface {
	CreateWorkflow(context.Context, WorkflowDefinition) (WorkflowDefinition, error)
	GetWorkflow(context.Context, string, string) (WorkflowDefinition, error)
	ListWorkflows(context.Context, WorkflowFilter) ([]WorkflowDefinition, error)
	UpdateWorkflow(context.Context, WorkflowDefinition) (WorkflowDefinition, error)
	CreatePermissionSnapshot(context.Context, WorkflowPermissionSnapshot) (WorkflowPermissionSnapshot, error)
	GetPermissionSnapshot(context.Context, string, string) (WorkflowPermissionSnapshot, error)
	ListPermissionSnapshots(context.Context, PermissionSnapshotFilter) ([]WorkflowPermissionSnapshot, error)
}

type WorkPlanAPI interface {
	CreateWorkPlan(context.Context, projectworkplan.CreateWorkPlanInput) (projectworkplan.WorkPlan, error)
	CreateWorkTask(context.Context, projectworkplan.CreateWorkTaskInput) (projectworkplan.WorkTask, error)
}

type AutomationAPI interface {
	CreateAutomation(context.Context, projectautomation.CreateAutomationInput) (projectautomation.Automation, error)
}

type Service struct {
	store       Store
	workPlans   WorkPlanAPI
	automations AutomationAPI
	now         func() time.Time
	newID       func(string) string
}

type CreateWorkflowInput struct {
	Definition     WorkflowDefinition
	CreatedByRunID string
	TraceID        string
}

type ValidateWorkflowTOMLInput struct {
	Data []byte
}

type ValidateWorkflowTOMLResult struct {
	Workflows []WorkflowDefinition      `json:"workflows,omitempty"`
	Issues    []WorkflowValidationIssue `json:"issues,omitempty"`
}

type ImportWorkflowTOMLInput struct {
	ProjectID      string
	Data           []byte
	CreatedByRunID string
	TraceID        string
}

type ImportWorkflowTOMLResult struct {
	Workflows             []WorkflowDefinition         `json:"workflows,omitempty"`
	PermissionSnapshots   []WorkflowPermissionSnapshot `json:"permission_snapshots,omitempty"`
	ValidationIssues      []WorkflowValidationIssue    `json:"validation_issues,omitempty"`
	PermissionSnapshotIDs []string                     `json:"permission_snapshot_ids,omitempty"`
}

type UpdateWorkflowStatusInput struct {
	ProjectID  string
	WorkflowID string
	Status     string
}

type WorkflowFilter struct {
	ProjectID   string `json:"project_id,omitempty"`
	Status      string `json:"status,omitempty"`
	WorkflowRef string `json:"workflow_ref,omitempty"`
}

type PermissionSnapshotFilter struct {
	ProjectID  string `json:"project_id,omitempty"`
	WorkflowID string `json:"workflow_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
}

func New(store Store) *Service {
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }, newID: newID}
}

func (svc *Service) SetCompilerDependencies(workPlans WorkPlanAPI, automations AutomationAPI) {
	svc.workPlans = workPlans
	svc.automations = automations
}

func (svc *Service) CreateWorkflow(ctx context.Context, input CreateWorkflowInput) (WorkflowDefinition, error) {
	if svc.store == nil {
		return WorkflowDefinition{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	workflow, err := svc.prepareWorkflow(input.Definition, input.CreatedByRunID, input.TraceID, false)
	if err != nil {
		return WorkflowDefinition{}, err
	}
	if issues := ValidateWorkflow(workflow); hasErrorIssues(issues) {
		return WorkflowDefinition{}, fmt.Errorf("%w: workflow validation failed", ErrInvalidInput)
	}
	return svc.store.CreateWorkflow(ctx, workflow)
}

func (svc *Service) ValidateWorkflowTOML(_ context.Context, input ValidateWorkflowTOMLInput) (ValidateWorkflowTOMLResult, error) {
	defs, issues, err := ParseWorkflowTOML(input.Data)
	if err != nil {
		return ValidateWorkflowTOMLResult{}, err
	}
	if hasErrorIssues(issues) {
		return ValidateWorkflowTOMLResult{Issues: issues}, nil
	}
	return ValidateWorkflowTOMLResult{Workflows: defs, Issues: issues}, nil
}

func (svc *Service) ImportWorkflowTOML(ctx context.Context, input ImportWorkflowTOMLInput) (ImportWorkflowTOMLResult, error) {
	if svc.store == nil {
		return ImportWorkflowTOMLResult{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	projectID, err := safeRequiredWorkflowRef(input.ProjectID, "project_id")
	if err != nil {
		return ImportWorkflowTOMLResult{}, err
	}
	defs, issues, err := ParseWorkflowTOML(input.Data)
	if err != nil {
		return ImportWorkflowTOMLResult{}, err
	}
	if hasErrorIssues(issues) {
		return ImportWorkflowTOMLResult{ValidationIssues: issues}, fmt.Errorf("%w: workflow validation failed", ErrInvalidInput)
	}

	prepared := make([]WorkflowDefinition, 0, len(defs))
	snapshotsByWorkflow := make([][]WorkflowPermissionSnapshot, 0, len(defs))
	for _, def := range defs {
		if projectID != "" {
			// Checked-in workflow TOML is a reusable template. The request
			// project is the import authority, so a project-scoped import
			// deliberately rebinds workflow metadata to that project.
			def.ProjectID = projectID
		}
		workflow, err := svc.prepareWorkflow(def, input.CreatedByRunID, input.TraceID, true)
		if err != nil {
			return ImportWorkflowTOMLResult{}, err
		}
		snapshots, err := svc.permissionSnapshotsForWorkflow(workflow)
		if err != nil {
			return ImportWorkflowTOMLResult{}, err
		}
		workflow.PermissionSnapshots = append([]WorkflowPermissionSnapshot(nil), snapshots...)
		prepared = append(prepared, workflow)
		snapshotsByWorkflow = append(snapshotsByWorkflow, snapshots)
	}

	result := ImportWorkflowTOMLResult{ValidationIssues: issues}
	for i, workflow := range prepared {
		created, err := svc.store.CreateWorkflow(ctx, workflow)
		if err != nil {
			return result, err
		}
		result.Workflows = append(result.Workflows, created)
		for _, snapshot := range snapshotsByWorkflow[i] {
			createdSnapshot, err := svc.store.CreatePermissionSnapshot(ctx, snapshot)
			if err != nil {
				return result, err
			}
			result.PermissionSnapshots = append(result.PermissionSnapshots, createdSnapshot)
			result.PermissionSnapshotIDs = append(result.PermissionSnapshotIDs, createdSnapshot.ID)
		}
	}
	return result, nil
}

func (svc *Service) GetWorkflow(ctx context.Context, projectID, workflowID string) (WorkflowDefinition, error) {
	projectID, workflowID, err := safeWorkflowObject(projectID, workflowID, "workflow_id")
	if err != nil {
		return WorkflowDefinition{}, err
	}
	return svc.store.GetWorkflow(ctx, projectID, workflowID)
}

func (svc *Service) ListWorkflows(ctx context.Context, filter WorkflowFilter) ([]WorkflowDefinition, error) {
	projectID, err := safeRequiredWorkflowRef(filter.ProjectID, "project_id")
	if err != nil {
		return nil, err
	}
	filter.ProjectID = projectID
	if filter.Status != "" {
		if filter.Status, err = safeWorkflowStatus(filter.Status); err != nil {
			return nil, err
		}
	}
	if filter.WorkflowRef != "" {
		if filter.WorkflowRef, err = safeOptionalWorkflowRef(filter.WorkflowRef, "workflow_ref"); err != nil {
			return nil, err
		}
	}
	return svc.store.ListWorkflows(ctx, filter)
}

func (svc *Service) UpdateWorkflowStatus(ctx context.Context, input UpdateWorkflowStatusInput) (WorkflowDefinition, error) {
	projectID, workflowID, err := safeWorkflowObject(input.ProjectID, input.WorkflowID, "workflow_id")
	if err != nil {
		return WorkflowDefinition{}, err
	}
	next, err := safeWorkflowStatus(input.Status)
	if err != nil {
		return WorkflowDefinition{}, err
	}
	workflow, err := svc.store.GetWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return WorkflowDefinition{}, err
	}
	if err := validateWorkflowStatusTransition(workflow.Status, next); err != nil {
		return WorkflowDefinition{}, err
	}
	workflow.Status = next
	workflow.UpdatedAt = svc.now()
	return svc.store.UpdateWorkflow(ctx, workflow)
}

func (svc *Service) prepareWorkflow(def WorkflowDefinition, runID string, traceID string, importMode bool) (WorkflowDefinition, error) {
	projectID, err := safeRequiredWorkflowRef(def.ProjectID, "project_id")
	if err != nil {
		return WorkflowDefinition{}, err
	}
	workflowID, err := safeOptionalWorkflowRef(def.ID, "id")
	if err != nil {
		return WorkflowDefinition{}, err
	}
	if workflowID == "" {
		workflowID = svc.newID("workflow")
	}
	workflowRef, err := safeOptionalWorkflowRef(def.WorkflowRef, "workflow_ref")
	if err != nil {
		return WorkflowDefinition{}, err
	}
	if workflowRef == "" {
		workflowRef = workflowID
	}
	createdByRunID, err := safeOptionalWorkflowRef(firstNonEmpty(runID, def.CreatedByRunID), "created_by_run_id")
	if err != nil {
		return WorkflowDefinition{}, err
	}
	trace, err := safeOptionalWorkflowRef(firstNonEmpty(traceID, def.TraceID), "trace_id")
	if err != nil {
		return WorkflowDefinition{}, err
	}
	status := WorkflowStatusDraft
	if !importMode && def.Status != "" {
		if status, err = safeWorkflowStatus(def.Status); err != nil {
			return WorkflowDefinition{}, err
		}
	}
	if importMode && strings.TrimSpace(def.Status) == WorkflowStatusEnabled {
		status = WorkflowStatusEnabled
	}

	now := svc.now()
	out := def
	out.ID = workflowID
	out.ProjectID = projectID
	out.WorkflowRef = workflowRef
	out.Status = status
	out.CreatedByRunID = createdByRunID
	out.TraceID = trace
	if out.CreatedAt.IsZero() {
		out.CreatedAt = now
	}
	out.UpdatedAt = now
	for i := range out.Agents {
		if out.Agents[i].CreatedAt.IsZero() {
			out.Agents[i].CreatedAt = now
		}
		out.Agents[i].UpdatedAt = now
	}
	return out, nil
}

func (svc *Service) permissionSnapshotsForWorkflow(workflow WorkflowDefinition) ([]WorkflowPermissionSnapshot, error) {
	snapshots := make([]WorkflowPermissionSnapshot, 0, len(workflow.Agents))
	for _, agent := range workflow.Agents {
		snapshot, err := svc.permissionSnapshotForAgent(workflow, agent)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func (svc *Service) permissionSnapshotForAgent(workflow WorkflowDefinition, agent WorkflowAgentDefinition) (WorkflowPermissionSnapshot, error) {
	now := svc.now()
	snapshot := WorkflowPermissionSnapshot{
		ID:              permissionSnapshotID(workflow.ID, agent.ID),
		ProjectID:       workflow.ProjectID,
		AgentID:         agent.ID,
		WorkflowID:      workflow.ID,
		AllowedSkills:   sortedCopy(agent.AllowedSkills),
		AllowedTools:    sortedCopy(agent.AllowedTools),
		AllowedCommands: sortedCopy(agent.AllowedCommands),
		DeniedCommands:  sortedCopy(agent.DeniedCommands),
		WorkspaceMode:   strings.TrimSpace(agent.WorkspaceMode),
		NetworkPolicy:   strings.TrimSpace(agent.NetworkPolicy),
		SecretPolicy:    firstNonEmpty(agent.SecretPolicy, "deny"),
		LogPolicy:       firstNonEmpty(agent.LogPolicy, "metadata_only"),
		MaxRuntime:      strings.TrimSpace(agent.MaxRuntime),
		MaxRetries:      agent.MaxRetries,
		CreatedByRunID:  workflow.CreatedByRunID,
		TraceID:         workflow.TraceID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	snapshot.ContentHash = permissionSnapshotContentHash(snapshot)
	if issues := ValidateWorkflow(WorkflowDefinition{ID: workflow.ID, ProjectID: workflow.ProjectID, Title: workflow.Title, Purpose: workflow.Purpose, Status: workflow.Status, Agents: workflow.Agents, Steps: workflow.Steps, ReviewGates: workflow.ReviewGates, PermissionSnapshots: []WorkflowPermissionSnapshot{snapshot}}); hasErrorIssues(issues) {
		return WorkflowPermissionSnapshot{}, fmt.Errorf("%w: permission snapshot validation failed: %v", ErrInvalidInput, issues)
	}
	return snapshot, nil
}

func (svc *Service) CheckAutomationPermission(ctx context.Context, input projectautomation.PermissionCheckInput) (projectautomation.PermissionSnapshotMetadata, error) {
	if svc.store == nil {
		return projectautomation.PermissionSnapshotMetadata{}, fmt.Errorf("%w: permission store is required", ErrInvalidInput)
	}
	projectID, err := safeRequiredWorkflowRef(input.ProjectID, "project_id")
	if err != nil {
		return projectautomation.PermissionSnapshotMetadata{}, err
	}
	permissionRef := strings.TrimSpace(input.PermissionRef)
	if !strings.HasPrefix(permissionRef, projectautomation.PermissionSnapshotRefPrefix) {
		return projectautomation.PermissionSnapshotMetadata{}, fmt.Errorf("%w: malformed_permission_snapshot_ref", ErrInvalidInput)
	}
	snapshotID, err := safeRequiredWorkflowRef(strings.TrimPrefix(permissionRef, projectautomation.PermissionSnapshotRefPrefix), "permission_snapshot_id")
	if err != nil {
		return projectautomation.PermissionSnapshotMetadata{}, fmt.Errorf("%w: malformed_permission_snapshot_ref", ErrInvalidInput)
	}
	snapshot, err := svc.store.GetPermissionSnapshot(ctx, projectID, snapshotID)
	if err != nil {
		return projectautomation.PermissionSnapshotMetadata{}, fmt.Errorf("%w: permission_snapshot_unavailable", ErrInvalidInput)
	}
	if snapshot.AgentID != strings.TrimSpace(input.AgentID) {
		return projectautomation.PermissionSnapshotMetadata{}, fmt.Errorf("%w: permission_agent_mismatch", ErrInvalidInput)
	}
	return projectautomation.PermissionSnapshotMetadata{
		PermissionRef:      permissionRef,
		AgentID:            snapshot.AgentID,
		AllowedRunnerKinds: []string{projectautomation.RunnerKindCodexCLI},
		DeniedCommands:     append([]string(nil), snapshot.DeniedCommands...),
	}, nil
}

func permissionSnapshotContentHash(snapshot WorkflowPermissionSnapshot) string {
	payload := struct {
		AgentID         string   `json:"agent_id"`
		AllowedSkills   []string `json:"allowed_skills,omitempty"`
		AllowedTools    []string `json:"allowed_tools,omitempty"`
		AllowedCommands []string `json:"allowed_commands,omitempty"`
		DeniedCommands  []string `json:"denied_commands,omitempty"`
		WorkspaceMode   string   `json:"workspace_mode,omitempty"`
		NetworkPolicy   string   `json:"network_policy,omitempty"`
		SecretPolicy    string   `json:"secret_policy,omitempty"`
		LogPolicy       string   `json:"log_policy,omitempty"`
		MaxRuntime      string   `json:"max_runtime,omitempty"`
		MaxRetries      int      `json:"max_retries,omitempty"`
	}{
		AgentID: snapshot.AgentID, AllowedSkills: sortedCopy(snapshot.AllowedSkills), AllowedTools: sortedCopy(snapshot.AllowedTools),
		AllowedCommands: sortedCopy(snapshot.AllowedCommands), DeniedCommands: sortedCopy(snapshot.DeniedCommands),
		WorkspaceMode: snapshot.WorkspaceMode, NetworkPolicy: snapshot.NetworkPolicy, SecretPolicy: snapshot.SecretPolicy,
		LogPolicy: snapshot.LogPolicy, MaxRuntime: snapshot.MaxRuntime, MaxRetries: snapshot.MaxRetries,
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return "sha256-" + safeHashText(hex.EncodeToString(sum[:]))
}

func permissionSnapshotID(workflowID string, agentID string) string {
	workflowID = safeRefToken(workflowID)
	agentID = safeRefToken(agentID)
	if workflowID == "" {
		workflowID = "workflow"
	}
	if agentID == "" {
		agentID = "agent"
	}
	return "permission-snapshot-" + workflowID + "-" + agentID
}

func safeRefToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteByte(byte('a' + (r - '0')))
		case r == '.':
			builder.WriteString("-dot-")
		case r == '_':
			builder.WriteString("-under-")
		case r == ':':
			builder.WriteString("-colon-")
		case r == '/':
			builder.WriteString("-slash-")
		case r == '@':
			builder.WriteString("-at-")
		case r == '+':
			builder.WriteString("-plus-")
		case r == '-':
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

func hasErrorIssues(issues []WorkflowValidationIssue) bool {
	for _, issue := range issues {
		if issue.Severity == workflowIssueError || issue.Severity == "" {
			return true
		}
	}
	return false
}

func safeWorkflowObject(projectID, objectID, field string) (string, string, error) {
	projectID, err := safeRequiredWorkflowRef(projectID, "project_id")
	if err != nil {
		return "", "", err
	}
	objectID, err = safeRequiredWorkflowRef(objectID, field)
	if err != nil {
		return "", "", err
	}
	return projectID, objectID, nil
}

func safeRequiredWorkflowRef(value, field string) (string, error) {
	value, err := safeOptionalWorkflowRef(value, field)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidInput, field)
	}
	return value, nil
}

func safeOptionalWorkflowRef(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !safeWorkflowRef(value) {
		return "", fmt.Errorf("%w: %s is unsafe", ErrInvalidInput, field)
	}
	return value, nil
}

func safeWorkflowStatus(status string) (string, error) {
	switch strings.TrimSpace(status) {
	case WorkflowStatusDraft, WorkflowStatusEnabled, WorkflowStatusDisabled, WorkflowStatusSuperseded:
		return strings.TrimSpace(status), nil
	default:
		return "", fmt.Errorf("%w: invalid workflow status", ErrInvalidInput)
	}
}

func validateWorkflowStatusTransition(from, to string) error {
	if from == to {
		return nil
	}
	allowed := map[string][]string{
		WorkflowStatusDraft:      {WorkflowStatusEnabled, WorkflowStatusDisabled, WorkflowStatusSuperseded},
		WorkflowStatusEnabled:    {WorkflowStatusDisabled, WorkflowStatusSuperseded},
		WorkflowStatusDisabled:   {WorkflowStatusEnabled, WorkflowStatusSuperseded},
		WorkflowStatusSuperseded: {},
	}
	for _, candidate := range allowed[from] {
		if candidate == to {
			return nil
		}
	}
	return fmt.Errorf("%w: invalid workflow transition %s -> %s", ErrInvalidInput, from, to)
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func safeHashText(value string) string {
	replacer := strings.NewReplacer(
		"0", "g",
		"1", "h",
		"2", "i",
		"3", "j",
		"4", "k",
		"5", "l",
		"6", "m",
		"7", "n",
		"8", "o",
		"9", "p",
	)
	return replacer.Replace(value)
}

func newID(prefix string) string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(buf[:])
}
