package model

import "time"

const (
	TaskStatusPending = "pending"
	TaskStatusRunning = "running"
	TaskStatusDone    = "done"
	TaskStatusFailed  = "failed"

	ResearchRunStatusPending = "pending"

	AgentRunStatusRunning   = "running"
	AgentRunStatusCompleted = "completed"
	AgentRunStatusFailed    = "failed"
)

type Task struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateTaskInput struct {
	Title string `json:"title"`
}

type ResearchRun struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	GoalSummary string    `json:"goal_summary"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateResearchRunInput struct {
	TaskID      string `json:"task_id"`
	GoalSummary string `json:"goal_summary"`
}

type AgentRun struct {
	ID              string          `json:"id"`
	ProjectID       string          `json:"project_id"`
	TaskID          string          `json:"task_id,omitempty"`
	Status          string          `json:"status"`
	StartedAt       time.Time       `json:"started_at"`
	CompletedAt     time.Time       `json:"completed_at,omitempty"`
	FailureCategory string          `json:"failure_category,omitempty"`
	Summary         string          `json:"summary,omitempty"`
	ChangedFiles    []string        `json:"changed_files,omitempty"`
	Verifiers       []AgentVerifier `json:"verifiers,omitempty"`
	Artifacts       []AgentArtifact `json:"artifacts,omitempty"`
	Steps           []AgentStep     `json:"steps,omitempty"`
}

type AgentStep struct {
	ID              string          `json:"id"`
	ToolName        string          `json:"tool_name,omitempty"`
	ToolCategory    string          `json:"tool_category,omitempty"`
	Status          string          `json:"status"`
	StartedAt       time.Time       `json:"started_at"`
	CompletedAt     time.Time       `json:"completed_at,omitempty"`
	FailureCategory string          `json:"failure_category,omitempty"`
	Notes           string          `json:"notes,omitempty"`
	ChangedFiles    []string        `json:"changed_files,omitempty"`
	Verifiers       []AgentVerifier `json:"verifiers,omitempty"`
	Artifacts       []AgentArtifact `json:"artifacts,omitempty"`
}

type AgentVerifier struct {
	Command    string   `json:"command"`
	Args       []string `json:"args,omitempty"`
	Scope      string   `json:"scope,omitempty"`
	Status     string   `json:"status,omitempty"`
	ExitStatus int      `json:"exit_status,omitempty"`
}

type AgentArtifact struct {
	Ref  string `json:"ref"`
	Kind string `json:"kind,omitempty"`
}

type CreateAgentRunInput struct {
	ProjectID    string          `json:"project_id"`
	TaskID       string          `json:"task_id,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	ChangedFiles []string        `json:"changed_files,omitempty"`
	Verifiers    []AgentVerifier `json:"verifiers,omitempty"`
	Artifacts    []AgentArtifact `json:"artifacts,omitempty"`
}

type AppendAgentStepInput struct {
	RunID           string          `json:"run_id,omitempty"`
	ToolName        string          `json:"tool_name,omitempty"`
	ToolCategory    string          `json:"tool_category,omitempty"`
	Status          string          `json:"status"`
	FailureCategory string          `json:"failure_category,omitempty"`
	Notes           string          `json:"notes,omitempty"`
	ChangedFiles    []string        `json:"changed_files,omitempty"`
	Verifiers       []AgentVerifier `json:"verifiers,omitempty"`
	Artifacts       []AgentArtifact `json:"artifacts,omitempty"`
}

type CompleteAgentRunInput struct {
	RunID           string          `json:"run_id,omitempty"`
	Status          string          `json:"status"`
	FailureCategory string          `json:"failure_category,omitempty"`
	Summary         string          `json:"summary,omitempty"`
	ChangedFiles    []string        `json:"changed_files,omitempty"`
	Verifiers       []AgentVerifier `json:"verifiers,omitempty"`
	Artifacts       []AgentArtifact `json:"artifacts,omitempty"`
}
