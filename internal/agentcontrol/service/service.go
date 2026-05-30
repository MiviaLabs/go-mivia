package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/model"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/store"
)

var ErrInvalidInput = errors.New("invalid input")

var emailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
var phonePattern = regexp.MustCompile(`\+?[0-9][0-9 .()\-]{7,}[0-9]`)

type Service struct {
	tasks        store.TaskStore
	researchRuns store.ResearchRunStore
	now          func() time.Time
	newID        func(string) string
}

func New(tasks store.TaskStore, researchRuns store.ResearchRunStore) *Service {
	return &Service{
		tasks:        tasks,
		researchRuns: researchRuns,
		now:          func() time.Time { return time.Now().UTC() },
		newID:        newID,
	}
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
		"begin private key",
		"api_key=",
		"token=",
		"secret=",
	}
	for _, marker := range disallowed {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return emailPattern.MatchString(value) || phonePattern.MatchString(value)
}

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("generate id: %w", err))
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
