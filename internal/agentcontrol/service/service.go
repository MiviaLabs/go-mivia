package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/model"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/store"
)

var ErrInvalidInput = errors.New("invalid input")

var emailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
var phonePattern = regexp.MustCompile(`\+?[0-9][0-9 .()\-]{7,}[0-9]`)

type Service struct {
	tasks        store.TaskStore
	researchRuns store.ResearchRunStore
	agentRuns    store.AgentRunStore
	now          func() time.Time
	newID        func(string) string
}

func New(tasks store.TaskStore, researchRuns store.ResearchRunStore) *Service {
	agentRuns, _ := tasks.(store.AgentRunStore)
	if agentRuns == nil {
		agentRuns, _ = researchRuns.(store.AgentRunStore)
	}
	return &Service{
		tasks:        tasks,
		researchRuns: researchRuns,
		agentRuns:    agentRuns,
		now:          func() time.Time { return time.Now().UTC() },
		newID:        newID,
	}
}

func NewWithAgentRuns(tasks store.TaskStore, researchRuns store.ResearchRunStore, agentRuns store.AgentRunStore) *Service {
	svc := New(tasks, researchRuns)
	svc.agentRuns = agentRuns
	return svc
}

func (svc *Service) CreateTask(ctx context.Context, input model.CreateTaskInput) (model.Task, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return model.Task{}, fmt.Errorf("%w: title is required", ErrInvalidInput)
	}
	if len(title) > 200 {
		return model.Task{}, fmt.Errorf("%w: title exceeds 200 characters", ErrInvalidInput)
	}
	if containsProhibitedData(title) {
		return model.Task{}, fmt.Errorf("%w: title must not contain raw queries, prompts, secrets, tokens, or personal data", ErrInvalidInput)
	}
	now := svc.now()
	task := model.Task{
		ID:        svc.newID("task"),
		Title:     title,
		Status:    model.TaskStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	return svc.tasks.CreateTask(ctx, task)
}

func (svc *Service) GetTask(ctx context.Context, id string) (model.Task, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.Task{}, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	return svc.tasks.GetTask(ctx, id)
}

func (svc *Service) StartTask(ctx context.Context, id string) (model.Task, error) {
	task, err := svc.GetTask(ctx, id)
	if err != nil {
		return model.Task{}, err
	}
	if task.Status != model.TaskStatusPending {
		return model.Task{}, fmt.Errorf("%w: task can start only from pending", ErrInvalidInput)
	}
	return svc.updateTaskStatus(ctx, task, model.TaskStatusRunning)
}

func (svc *Service) CompleteTask(ctx context.Context, id string) (model.Task, error) {
	task, err := svc.GetTask(ctx, id)
	if err != nil {
		return model.Task{}, err
	}
	if task.Status != model.TaskStatusRunning {
		return model.Task{}, fmt.Errorf("%w: task can complete only from running", ErrInvalidInput)
	}
	return svc.updateTaskStatus(ctx, task, model.TaskStatusDone)
}

func (svc *Service) FailTask(ctx context.Context, id string) (model.Task, error) {
	task, err := svc.GetTask(ctx, id)
	if err != nil {
		return model.Task{}, err
	}
	if task.Status != model.TaskStatusPending && task.Status != model.TaskStatusRunning {
		return model.Task{}, fmt.Errorf("%w: task can fail only from pending or running", ErrInvalidInput)
	}
	return svc.updateTaskStatus(ctx, task, model.TaskStatusFailed)
}

func (svc *Service) CreateResearchRun(ctx context.Context, input model.CreateResearchRunInput) (model.ResearchRun, error) {
	taskID := strings.TrimSpace(input.TaskID)
	if taskID == "" {
		return model.ResearchRun{}, fmt.Errorf("%w: task_id is required", ErrInvalidInput)
	}
	if _, err := svc.tasks.GetTask(ctx, taskID); err != nil {
		return model.ResearchRun{}, err
	}
	goalSummary := strings.TrimSpace(input.GoalSummary)
	if goalSummary == "" {
		return model.ResearchRun{}, fmt.Errorf("%w: goal_summary is required", ErrInvalidInput)
	}
	if len(goalSummary) > 500 {
		return model.ResearchRun{}, fmt.Errorf("%w: goal_summary exceeds 500 characters", ErrInvalidInput)
	}
	if containsProhibitedData(goalSummary) {
		return model.ResearchRun{}, fmt.Errorf("%w: goal_summary must not contain raw queries, prompts, secrets, tokens, or personal data", ErrInvalidInput)
	}
	now := svc.now()
	run := model.ResearchRun{
		ID:          svc.newID("research_run"),
		TaskID:      taskID,
		GoalSummary: goalSummary,
		Status:      model.ResearchRunStatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	return svc.researchRuns.CreateResearchRun(ctx, run)
}

func (svc *Service) GetResearchRun(ctx context.Context, id string) (model.ResearchRun, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.ResearchRun{}, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	return svc.researchRuns.GetResearchRun(ctx, id)
}

func (svc *Service) CreateAgentRun(ctx context.Context, input model.CreateAgentRunInput) (model.AgentRun, error) {
	if svc.agentRuns == nil {
		return model.AgentRun{}, fmt.Errorf("%w: agent run store is not configured", ErrInvalidInput)
	}
	projectID, err := safeIdentifier(input.ProjectID, "project_id")
	if err != nil {
		return model.AgentRun{}, err
	}
	taskID := strings.TrimSpace(input.TaskID)
	if taskID != "" {
		if _, err := svc.tasks.GetTask(ctx, taskID); err != nil {
			return model.AgentRun{}, err
		}
	}
	summary, err := safeOptionalText(input.Summary, "summary", 500)
	if err != nil {
		return model.AgentRun{}, err
	}
	changedFiles, err := safeRelativePaths(input.ChangedFiles)
	if err != nil {
		return model.AgentRun{}, err
	}
	verifiers, err := safeVerifiers(input.Verifiers)
	if err != nil {
		return model.AgentRun{}, err
	}
	artifacts, err := safeArtifacts(input.Artifacts)
	if err != nil {
		return model.AgentRun{}, err
	}
	now := svc.now()
	run := model.AgentRun{
		ID:           svc.newID("agent_run"),
		ProjectID:    projectID,
		TaskID:       taskID,
		Status:       model.AgentRunStatusRunning,
		StartedAt:    now,
		Summary:      summary,
		ChangedFiles: changedFiles,
		Verifiers:    verifiers,
		Artifacts:    artifacts,
	}
	return svc.agentRuns.CreateAgentRun(ctx, run)
}

func (svc *Service) AppendAgentStep(ctx context.Context, runID string, input model.AppendAgentStepInput) (model.AgentRun, error) {
	if svc.agentRuns == nil {
		return model.AgentRun{}, fmt.Errorf("%w: agent run store is not configured", ErrInvalidInput)
	}
	runID, err := safeIdentifier(firstNonEmpty(runID, input.RunID), "run_id")
	if err != nil {
		return model.AgentRun{}, err
	}
	status, err := safeAgentRunStatus(firstNonEmpty(input.Status, model.AgentRunStatusRunning), true)
	if err != nil {
		return model.AgentRun{}, err
	}
	toolName, err := safeOptionalIdentifier(input.ToolName, "tool_name")
	if err != nil {
		return model.AgentRun{}, err
	}
	toolCategory, err := safeOptionalIdentifier(input.ToolCategory, "tool_category")
	if err != nil {
		return model.AgentRun{}, err
	}
	failureCategory, err := safeOptionalIdentifier(input.FailureCategory, "failure_category")
	if err != nil {
		return model.AgentRun{}, err
	}
	notes, err := safeOptionalText(input.Notes, "notes", 500)
	if err != nil {
		return model.AgentRun{}, err
	}
	changedFiles, err := safeRelativePaths(input.ChangedFiles)
	if err != nil {
		return model.AgentRun{}, err
	}
	verifiers, err := safeVerifiers(input.Verifiers)
	if err != nil {
		return model.AgentRun{}, err
	}
	artifacts, err := safeArtifacts(input.Artifacts)
	if err != nil {
		return model.AgentRun{}, err
	}
	now := svc.now()
	step := model.AgentStep{
		ID:              svc.newID("agent_step"),
		ToolName:        toolName,
		ToolCategory:    toolCategory,
		Status:          status,
		StartedAt:       now,
		CompletedAt:     now,
		FailureCategory: failureCategory,
		Notes:           notes,
		ChangedFiles:    changedFiles,
		Verifiers:       verifiers,
		Artifacts:       artifacts,
	}
	return svc.agentRuns.AppendAgentStep(ctx, runID, step)
}

func (svc *Service) CompleteAgentRun(ctx context.Context, runID string, input model.CompleteAgentRunInput) (model.AgentRun, error) {
	if svc.agentRuns == nil {
		return model.AgentRun{}, fmt.Errorf("%w: agent run store is not configured", ErrInvalidInput)
	}
	runID, err := safeIdentifier(firstNonEmpty(runID, input.RunID), "run_id")
	if err != nil {
		return model.AgentRun{}, err
	}
	status, err := safeAgentRunStatus(input.Status, false)
	if err != nil {
		return model.AgentRun{}, err
	}
	failureCategory, err := safeOptionalIdentifier(input.FailureCategory, "failure_category")
	if err != nil {
		return model.AgentRun{}, err
	}
	summary, err := safeOptionalText(input.Summary, "summary", 500)
	if err != nil {
		return model.AgentRun{}, err
	}
	changedFiles, err := safeRelativePaths(input.ChangedFiles)
	if err != nil {
		return model.AgentRun{}, err
	}
	verifiers, err := safeVerifiers(input.Verifiers)
	if err != nil {
		return model.AgentRun{}, err
	}
	artifacts, err := safeArtifacts(input.Artifacts)
	if err != nil {
		return model.AgentRun{}, err
	}
	run, err := svc.agentRuns.GetAgentRun(ctx, runID)
	if err != nil {
		return model.AgentRun{}, err
	}
	run.Status = status
	run.CompletedAt = svc.now()
	run.FailureCategory = failureCategory
	if summary != "" {
		run.Summary = summary
	}
	if len(changedFiles) > 0 {
		run.ChangedFiles = changedFiles
	}
	if len(verifiers) > 0 {
		run.Verifiers = verifiers
	}
	if len(artifacts) > 0 {
		run.Artifacts = artifacts
	}
	return svc.agentRuns.CompleteAgentRun(ctx, run)
}

func (svc *Service) GetAgentRun(ctx context.Context, id string) (model.AgentRun, error) {
	if svc.agentRuns == nil {
		return model.AgentRun{}, fmt.Errorf("%w: agent run store is not configured", ErrInvalidInput)
	}
	id, err := safeIdentifier(id, "id")
	if err != nil {
		return model.AgentRun{}, err
	}
	return svc.agentRuns.GetAgentRun(ctx, id)
}

func (svc *Service) updateTaskStatus(ctx context.Context, task model.Task, status string) (model.Task, error) {
	task.Status = status
	task.UpdatedAt = svc.now()
	return svc.tasks.UpdateTask(ctx, task)
}

func containsProhibitedData(value string) bool {
	normalized := strings.ToLower(value)
	disallowed := []string{
		"match (",
		"select ",
		"insert into",
		"update ",
		"delete from",
		"pragma ",
		"raw prompt",
		"raw completion",
		"raw source",
		"raw stderr",
		"provider payload",
		"authorization:",
		"bearer ",
		"begin private key",
		"api_key=",
		"token=",
		"secret=",
		"password=",
	}
	for _, marker := range disallowed {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return emailPattern.MatchString(value) || phonePattern.MatchString(value)
}

func safeIdentifier(value string, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidInput, field)
	}
	if len(value) > 200 {
		return "", fmt.Errorf("%w: %s is unsafe", ErrInvalidInput, field)
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == '/' {
			continue
		}
		return "", fmt.Errorf("%w: %s is unsafe", ErrInvalidInput, field)
	}
	return value, nil
}

func safeOptionalIdentifier(value string, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	return safeIdentifier(value, field)
}

func safeOptionalText(value string, field string, maxLength int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > maxLength || containsProhibitedData(value) || looksLikeSourceDump(value) {
		return "", fmt.Errorf("%w: %s is unsafe", ErrInvalidInput, field)
	}
	return value, nil
}

func safeRelativePaths(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		if path == "" {
			continue
		}
		if len(path) > 300 || strings.HasPrefix(path, "/") || strings.Contains(path, "..") || filepath.IsAbs(path) || containsProhibitedData(path) {
			return nil, fmt.Errorf("%w: changed file path is unsafe", ErrInvalidInput)
		}
		out = append(out, path)
	}
	return out, nil
}

func safeVerifiers(verifiers []model.AgentVerifier) ([]model.AgentVerifier, error) {
	out := make([]model.AgentVerifier, 0, len(verifiers))
	for _, verifier := range verifiers {
		command, err := safeIdentifier(verifier.Command, "verifier.command")
		if err != nil {
			return nil, err
		}
		args := make([]string, 0, len(verifier.Args))
		for _, arg := range verifier.Args {
			clean, err := safeVerifierArg(arg)
			if err != nil {
				return nil, err
			}
			args = append(args, clean)
		}
		scope, err := safeOptionalIdentifier(verifier.Scope, "verifier.scope")
		if err != nil {
			return nil, err
		}
		status, err := safeOptionalIdentifier(verifier.Status, "verifier.status")
		if err != nil {
			return nil, err
		}
		out = append(out, model.AgentVerifier{
			Command:    command,
			Args:       args,
			Scope:      scope,
			Status:     status,
			ExitStatus: verifier.ExitStatus,
		})
	}
	return out, nil
}

func safeVerifierArg(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return "", fmt.Errorf("%w: verifier arg is required", ErrInvalidInput)
	}
	if len(value) > 200 || strings.HasPrefix(value, "/") || filepath.IsAbs(value) || containsProhibitedData(value) {
		return "", fmt.Errorf("%w: verifier arg is unsafe", ErrInvalidInput)
	}
	return value, nil
}

func safeArtifacts(artifacts []model.AgentArtifact) ([]model.AgentArtifact, error) {
	out := make([]model.AgentArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		ref, err := safeIdentifier(artifact.Ref, "artifact.ref")
		if err != nil {
			return nil, err
		}
		kind, err := safeOptionalIdentifier(artifact.Kind, "artifact.kind")
		if err != nil {
			return nil, err
		}
		out = append(out, model.AgentArtifact{Ref: ref, Kind: kind})
	}
	return out, nil
}

func safeAgentRunStatus(status string, allowRunning bool) (string, error) {
	status = strings.TrimSpace(status)
	switch status {
	case model.AgentRunStatusCompleted, model.AgentRunStatusFailed:
		return status, nil
	case model.AgentRunStatusRunning:
		if allowRunning {
			return status, nil
		}
	}
	return "", fmt.Errorf("%w: status is invalid", ErrInvalidInput)
}

func looksLikeSourceDump(value string) bool {
	normalized := strings.ToLower(value)
	return strings.Count(value, "\n") > 4 ||
		strings.Contains(normalized, "package main") ||
		strings.Contains(normalized, "func ") ||
		strings.Contains(normalized, "-----begin") ||
		strings.Contains(normalized, "traceback (most recent call last)")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("generate id: %w", err))
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
