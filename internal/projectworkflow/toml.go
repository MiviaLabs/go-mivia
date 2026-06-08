package projectworkflow

import (
	"bytes"
	"strconv"

	"github.com/pelletier/go-toml/v2"
)

type workflowTOMLDocument struct {
	ID                  string                           `toml:"id"`
	ProjectID           string                           `toml:"project_id"`
	WorkflowRef         string                           `toml:"workflow_ref"`
	Title               string                           `toml:"title"`
	Purpose             string                           `toml:"purpose"`
	Status              string                           `toml:"status"`
	Agents              []workflowAgentDefinitionTOML    `toml:"agents"`
	Steps               []workflowStepTOML               `toml:"steps"`
	ReviewGatesRaw      []workflowReviewGateTOML         `toml:"review_gates"`
	PermissionSnapshots []workflowPermissionSnapshotTOML `toml:"permission_snapshots"`
	CreatedByRunID      string                           `toml:"created_by_run_id"`
	TraceID             string                           `toml:"trace_id"`
	Workflows           []workflowTOML                   `toml:"workflows"`
}

type workflowTOML struct {
	ID                  string                           `toml:"id"`
	ProjectID           string                           `toml:"project_id"`
	WorkflowRef         string                           `toml:"workflow_ref"`
	Title               string                           `toml:"title"`
	Purpose             string                           `toml:"purpose"`
	Status              string                           `toml:"status"`
	Agents              []workflowAgentDefinitionTOML    `toml:"agents"`
	Steps               []workflowStepTOML               `toml:"steps"`
	ReviewGatesRaw      []workflowReviewGateTOML         `toml:"review_gates"`
	PermissionSnapshots []workflowPermissionSnapshotTOML `toml:"permission_snapshots"`
	CreatedByRunID      string                           `toml:"created_by_run_id"`
	TraceID             string                           `toml:"trace_id"`
}

type workflowAgentDefinitionTOML struct {
	ID              string   `toml:"id"`
	DisplayName     string   `toml:"display_name"`
	Purpose         string   `toml:"purpose"`
	Instructions    string   `toml:"instructions"`
	AllowedSkills   []string `toml:"allowed_skills"`
	AllowedTools    []string `toml:"allowed_tools"`
	AllowedCommands []string `toml:"allowed_commands"`
	DeniedCommands  []string `toml:"denied_commands"`
	WorkspaceMode   string   `toml:"workspace_mode"`
	NetworkPolicy   string   `toml:"network_policy"`
	SecretPolicy    string   `toml:"secret_policy"`
	LogPolicy       string   `toml:"log_policy"`
	MaxRuntime      string   `toml:"max_runtime"`
	MaxRetries      int      `toml:"max_retries"`
}

type workflowStepTOML struct {
	ID                      string   `toml:"id"`
	Kind                    string   `toml:"kind"`
	Title                   string   `toml:"title"`
	Agent                   string   `toml:"agent"`
	DependsOn               []string `toml:"depends_on"`
	Description             string   `toml:"description"`
	EvidenceNeeded          []string `toml:"evidence_needed"`
	ContextPackRefs         []string `toml:"context_pack_refs"`
	FilesToRead             []string `toml:"files_to_read"`
	FilesToEdit             []string `toml:"files_to_edit"`
	LikelyFilesAffected     []string `toml:"likely_files_affected"`
	VerificationRequirement string   `toml:"verification_requirement"`
	GitOpsVerificationMode  string   `toml:"gitops_verification_mode"`
	ExpectedOutput          string   `toml:"expected_output"`
	FailureCriteria         string   `toml:"failure_criteria"`
	ReviewGate              string   `toml:"review_gate"`
	ResumeInstructions      string   `toml:"resume_instructions"`
	AcceptanceCriteria      []string `toml:"acceptance_criteria"`
	StopConditions          []string `toml:"stop_conditions"`
	VerifierLadder          []string `toml:"verifier_ladder"`
	RegressionApplicability string   `toml:"regression_test_applicability"`
	DownstreamImpactRefs    []string `toml:"downstream_impact_refs"`
	OutputContract          string   `toml:"output_contract"`
	MaxParallelTasks        *int     `toml:"max_parallel_tasks"`
	AutomationStatus        string   `toml:"automation_status"`
	TriggerKind             string   `toml:"trigger_kind"`
	SchedulePolicy          string   `toml:"schedule_policy"`
}

type workflowReviewGateTOML struct {
	ID                   string   `toml:"id"`
	AppliesTo            []string `toml:"applies_to"`
	ReviewerAgent        string   `toml:"reviewer_agent"`
	Required             bool     `toml:"required"`
	IndependentFromOwner *bool    `toml:"independent_from_owner"`
	RequiredArtifacts    []string `toml:"required_artifacts"`
	AllowedActions       []string `toml:"allowed_actions"`
	Instructions         string   `toml:"instructions"`
}

type workflowPermissionSnapshotTOML struct {
	ID              string   `toml:"id"`
	ProjectID       string   `toml:"project_id"`
	AgentID         string   `toml:"agent_id"`
	WorkflowID      string   `toml:"workflow_id"`
	Instructions    string   `toml:"instructions"`
	AllowedSkills   []string `toml:"allowed_skills"`
	AllowedTools    []string `toml:"allowed_tools"`
	AllowedCommands []string `toml:"allowed_commands"`
	DeniedCommands  []string `toml:"denied_commands"`
	WorkspaceMode   string   `toml:"workspace_mode"`
	NetworkPolicy   string   `toml:"network_policy"`
	SecretPolicy    string   `toml:"secret_policy"`
	LogPolicy       string   `toml:"log_policy"`
	MaxRuntime      string   `toml:"max_runtime"`
	MaxRetries      int      `toml:"max_retries"`
	ContentHash     string   `toml:"content_hash"`
	CreatedByRunID  string   `toml:"created_by_run_id"`
	TraceID         string   `toml:"trace_id"`
}

// ParseWorkflowTOML decodes workflow metadata from TOML and validates every
// decoded workflow. It accepts either one top-level workflow or [[workflows]].
func ParseWorkflowTOML(data []byte) ([]WorkflowDefinition, []WorkflowValidationIssue, error) {
	var doc workflowTOMLDocument
	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return nil, nil, err
	}

	var definitions []WorkflowDefinition
	if len(doc.Workflows) > 0 {
		definitions = make([]WorkflowDefinition, 0, len(doc.Workflows))
		for _, workflow := range doc.Workflows {
			definitions = append(definitions, workflow.toDefinition())
		}
	} else {
		definitions = []WorkflowDefinition{doc.toWorkflowTOML().toDefinition()}
	}

	var issues []WorkflowValidationIssue
	for index, def := range definitions {
		for _, issue := range ValidateWorkflow(def) {
			if len(definitions) > 1 {
				issue.FieldPath = workflowFieldPath(index, issue.FieldPath)
			}
			issues = append(issues, issue)
		}
	}
	return definitions, issues, nil
}

func (doc workflowTOMLDocument) toWorkflowTOML() workflowTOML {
	return workflowTOML{
		ID: doc.ID, ProjectID: doc.ProjectID, WorkflowRef: doc.WorkflowRef,
		Title: doc.Title, Purpose: doc.Purpose, Status: doc.Status,
		Agents: doc.Agents, Steps: doc.Steps, ReviewGatesRaw: doc.ReviewGatesRaw,
		PermissionSnapshots: doc.PermissionSnapshots,
		CreatedByRunID:      doc.CreatedByRunID, TraceID: doc.TraceID,
	}
}

func (w workflowTOML) toDefinition() WorkflowDefinition {
	def := WorkflowDefinition{
		ID:                  w.ID,
		ProjectID:           w.ProjectID,
		WorkflowRef:         w.WorkflowRef,
		Title:               w.Title,
		Purpose:             w.Purpose,
		Status:              w.Status,
		Agents:              make([]WorkflowAgentDefinition, 0, len(w.Agents)),
		Steps:               make([]WorkflowStep, 0, len(w.Steps)),
		ReviewGates:         make([]WorkflowReviewGate, 0, len(w.ReviewGatesRaw)),
		PermissionSnapshots: make([]WorkflowPermissionSnapshot, 0, len(w.PermissionSnapshots)),
		CreatedByRunID:      w.CreatedByRunID,
		TraceID:             w.TraceID,
	}
	for _, agent := range w.Agents {
		def.Agents = append(def.Agents, WorkflowAgentDefinition{
			ID: agent.ID, DisplayName: agent.DisplayName, Purpose: agent.Purpose,
			Instructions:  agent.Instructions,
			AllowedSkills: agent.AllowedSkills, AllowedTools: agent.AllowedTools,
			AllowedCommands: agent.AllowedCommands, DeniedCommands: agent.DeniedCommands,
			WorkspaceMode: agent.WorkspaceMode, NetworkPolicy: agent.NetworkPolicy,
			SecretPolicy: agent.SecretPolicy, LogPolicy: agent.LogPolicy,
			MaxRuntime: agent.MaxRuntime, MaxRetries: agent.MaxRetries,
		})
	}
	for _, step := range w.Steps {
		maxParallelTasks := 0
		if step.MaxParallelTasks != nil {
			maxParallelTasks = *step.MaxParallelTasks
			if maxParallelTasks <= 0 {
				maxParallelTasks = -1
			}
		}
		def.Steps = append(def.Steps, WorkflowStep{
			ID: step.ID, Kind: step.Kind, Title: step.Title, Agent: step.Agent,
			DependsOn: step.DependsOn, Description: step.Description,
			EvidenceNeeded: step.EvidenceNeeded, ContextPackRefs: step.ContextPackRefs,
			FilesToRead: step.FilesToRead, FilesToEdit: step.FilesToEdit,
			LikelyFilesAffected:     step.LikelyFilesAffected,
			VerificationRequirement: step.VerificationRequirement, ExpectedOutput: step.ExpectedOutput,
			GitOpsVerificationMode: step.GitOpsVerificationMode,
			FailureCriteria:        step.FailureCriteria, ReviewGate: step.ReviewGate, ResumeInstructions: step.ResumeInstructions,
			AcceptanceCriteria: step.AcceptanceCriteria, StopConditions: step.StopConditions,
			VerifierLadder: step.VerifierLadder, RegressionApplicability: step.RegressionApplicability,
			DownstreamImpactRefs: step.DownstreamImpactRefs, OutputContract: step.OutputContract,
			MaxParallelTasks: maxParallelTasks, AutomationStatus: step.AutomationStatus,
			TriggerKind: step.TriggerKind, SchedulePolicy: step.SchedulePolicy,
		})
	}
	for _, gate := range w.ReviewGatesRaw {
		independent := true
		if gate.IndependentFromOwner != nil {
			independent = *gate.IndependentFromOwner
		}
		def.ReviewGates = append(def.ReviewGates, WorkflowReviewGate{
			ID: gate.ID, AppliesTo: gate.AppliesTo, ReviewerAgent: gate.ReviewerAgent,
			Required: gate.Required, IndependentFromOwner: independent,
			RequiredArtifacts: gate.RequiredArtifacts, AllowedActions: gate.AllowedActions,
			Instructions: gate.Instructions,
		})
	}
	for _, snapshot := range w.PermissionSnapshots {
		def.PermissionSnapshots = append(def.PermissionSnapshots, WorkflowPermissionSnapshot{
			ID: snapshot.ID, ProjectID: snapshot.ProjectID, AgentID: snapshot.AgentID,
			WorkflowID: snapshot.WorkflowID, Instructions: snapshot.Instructions,
			AllowedSkills: snapshot.AllowedSkills,
			AllowedTools:  snapshot.AllowedTools, AllowedCommands: snapshot.AllowedCommands,
			DeniedCommands: snapshot.DeniedCommands, WorkspaceMode: snapshot.WorkspaceMode,
			NetworkPolicy: snapshot.NetworkPolicy, SecretPolicy: snapshot.SecretPolicy,
			LogPolicy: snapshot.LogPolicy, MaxRuntime: snapshot.MaxRuntime,
			MaxRetries: snapshot.MaxRetries, ContentHash: snapshot.ContentHash,
			CreatedByRunID: snapshot.CreatedByRunID, TraceID: snapshot.TraceID,
		})
	}
	for i := range def.Agents {
		if def.Agents[i].SecretPolicy == "" {
			def.Agents[i].SecretPolicy = "deny"
		}
		if def.Agents[i].LogPolicy == "" {
			def.Agents[i].LogPolicy = "metadata_only"
		}
	}
	return def
}

func workflowFieldPath(index int, field string) string {
	prefix := "workflows[" + itoa(index) + "]"
	if field == "" {
		return prefix
	}
	return prefix + "." + field
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
