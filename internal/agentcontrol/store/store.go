package store

import (
	"context"
	"errors"

	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/model"
)

var ErrNotFound = errors.New("not found")

type TaskStore interface {
	CreateTask(context.Context, model.Task) (model.Task, error)
	GetTask(context.Context, string) (model.Task, error)
	UpdateTask(context.Context, model.Task) (model.Task, error)
}

type ResearchRunStore interface {
	CreateResearchRun(context.Context, model.ResearchRun) (model.ResearchRun, error)
	GetResearchRun(context.Context, string) (model.ResearchRun, error)
}

type AgentRunStore interface {
	CreateAgentRun(context.Context, model.AgentRun) (model.AgentRun, error)
	AppendAgentStep(context.Context, string, model.AgentStep) (model.AgentRun, error)
	PromoteAgentArtifact(context.Context, string, model.AgentPromotion) (model.AgentRun, error)
	CompleteAgentRun(context.Context, model.AgentRun) (model.AgentRun, error)
	GetAgentRun(context.Context, string) (model.AgentRun, error)
}

type AppConfigStore interface {
	SetAppSetting(context.Context, string, string, string) error
	GetAppSetting(context.Context, string) (string, string, error)
	SetRuntimeFlag(context.Context, string, bool, string) error
	GetRuntimeFlag(context.Context, string) (bool, string, error)
}
