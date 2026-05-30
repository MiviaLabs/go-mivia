package store

import (
	"context"
	"errors"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/model"
)

var ErrNotFound = errors.New("not found")

type TaskStore interface {
	CreateTask(context.Context, model.Task) (model.Task, error)
	GetTask(context.Context, string) (model.Task, error)
}

type ResearchRunStore interface {
	CreateResearchRun(context.Context, model.ResearchRun) (model.ResearchRun, error)
	GetResearchRun(context.Context, string) (model.ResearchRun, error)
}

type AppConfigStore interface {
	SetAppSetting(context.Context, string, string, string) error
	GetAppSetting(context.Context, string) (string, string, error)
	SetRuntimeFlag(context.Context, string, bool, string) error
	GetRuntimeFlag(context.Context, string) (bool, string, error)
}
