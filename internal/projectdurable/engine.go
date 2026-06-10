// Package projectdurable hosts the go-workflows durable execution pilot.
//
// Pilot boundary (see .ai/tasks/go-workflows-durable-automation-pilot/IMPLEMENTATION_PLAN.md):
// nothing in this package is wired into production paths. Durable workflow
// history must contain safe refs and bounded metadata only - never raw
// prompts, completions, stderr, source dumps, provider payloads, secrets,
// roots, external URLs, or PII.
package projectdurable

import (
	"github.com/cschleiden/go-workflows/backend"
	"github.com/cschleiden/go-workflows/backend/sqlite"
	"github.com/cschleiden/go-workflows/worker"

	durablestore "github.com/MiviaLabs/go-mivia/internal/projectdurable/store"
)

// Engine is a minimal wrapper around a go-workflows orchestrator. It exists
// so pilot tests construct durable execution through one seam instead of
// touching backend/worker wiring directly. No production code may construct
// an Engine while the pilot is disabled.
type Engine struct {
	// Backend is the durable history store. Memory engines use an
	// ephemeral in-memory SQLite backend.
	Backend backend.Backend
	// Orchestrator combines worker and client for single-process use.
	Orchestrator *worker.WorkflowOrchestrator
}

// NewMemoryEngine returns an Engine backed by an ephemeral in-memory store.
// Test and local pilot use only; state is lost on Close.
func NewMemoryEngine() *Engine {
	b := sqlite.NewInMemoryBackend()
	return &Engine{
		Backend:      b,
		Orchestrator: worker.NewWorkflowOrchestrator(b, nil),
	}
}

// NewSQLiteEngine returns an Engine backed by a safe repo-local SQLite file.
// It does not start workers; callers must explicitly start the orchestrator.
func NewSQLiteEngine(sqlitePath string) (*Engine, error) {
	b, err := durablestore.NewSQLiteBackend(sqlitePath)
	if err != nil {
		return nil, err
	}
	return &Engine{
		Backend:      b,
		Orchestrator: worker.NewWorkflowOrchestrator(b, nil),
	}, nil
}

// Close releases the backend. The engine is unusable afterwards.
func (e *Engine) Close() error {
	if e == nil || e.Backend == nil {
		return nil
	}
	return e.Backend.Close()
}
