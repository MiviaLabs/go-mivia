package store

import (
	"context"
	"errors"

	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
)

var ErrNotFound = errors.New("project workflow resource not found")
var ErrDuplicate = errors.New("project workflow resource already exists")

type Store interface {
	CreateWorkflow(context.Context, projectworkflow.WorkflowDefinition) (projectworkflow.WorkflowDefinition, error)
	GetWorkflow(context.Context, string, string) (projectworkflow.WorkflowDefinition, error)
	ListWorkflows(context.Context, WorkflowFilter) ([]projectworkflow.WorkflowDefinition, error)
	UpdateWorkflow(context.Context, projectworkflow.WorkflowDefinition) (projectworkflow.WorkflowDefinition, error)
	CreatePermissionSnapshot(context.Context, projectworkflow.WorkflowPermissionSnapshot) (projectworkflow.WorkflowPermissionSnapshot, error)
	GetPermissionSnapshot(context.Context, string, string) (projectworkflow.WorkflowPermissionSnapshot, error)
	ListPermissionSnapshots(context.Context, PermissionSnapshotFilter) ([]projectworkflow.WorkflowPermissionSnapshot, error)
}

type WorkflowFilter = projectworkflow.WorkflowFilter

type PermissionSnapshotFilter = projectworkflow.PermissionSnapshotFilter
