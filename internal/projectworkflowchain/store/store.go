package store

import (
	"context"
	"errors"

	"github.com/MiviaLabs/go-mivia/internal/projectworkflowchain"
)

var ErrNotFound = errors.New("project workflow chain resource not found")
var ErrDuplicate = errors.New("project workflow chain resource already exists")

type Store interface {
	CreateChainRun(context.Context, projectworkflowchain.ChainRun) (projectworkflowchain.ChainRun, error)
	GetChainRun(context.Context, string, string) (projectworkflowchain.ChainRun, error)
	ListChainRuns(context.Context, projectworkflowchain.ChainFilter) ([]projectworkflowchain.ChainRun, error)
	UpdateChainRun(context.Context, projectworkflowchain.ChainRun) (projectworkflowchain.ChainRun, error)
	FindChainRunByWorkPlan(context.Context, string, string) (projectworkflowchain.ChainRun, error)
}
