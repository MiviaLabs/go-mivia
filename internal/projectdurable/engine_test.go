package projectdurable

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cschleiden/go-workflows/client"
	"github.com/cschleiden/go-workflows/workflow"
)

// pilotActivityExecutions counts real activity side effects so tests can
// prove activities are not re-executed when workflow history is replayed.
var pilotActivityExecutions atomic.Int64

// pilotRecordRefActivity is the single side-effect boundary for the spike.
// It receives and returns safe string refs only.
func pilotRecordRefActivity(_ context.Context, ref string) (string, error) {
	pilotActivityExecutions.Add(1)
	return ref + ":observed", nil
}

// pilotShadowWorkflow is deterministic workflow code: no time.Now, no
// randomness, no I/O. It yields three times (activity, timer, activity), so
// completing it forces the engine to suspend and resume the workflow from
// persisted history at each boundary.
func pilotShadowWorkflow(ctx workflow.Context, inputRef string) (string, error) {
	first, err := workflow.ExecuteActivity[string](ctx, workflow.DefaultActivityOptions, pilotRecordRefActivity, inputRef).Get(ctx)
	if err != nil {
		return "", err
	}
	if err := workflow.Sleep(ctx, 25*time.Millisecond); err != nil {
		return "", err
	}
	second, err := workflow.ExecuteActivity[string](ctx, workflow.DefaultActivityOptions, pilotRecordRefActivity, first).Get(ctx)
	if err != nil {
		return "", err
	}
	return second, nil
}

func TestMemoryEngineCompletesDeterministicWorkflowAcrossSuspensions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pilotActivityExecutions.Store(0)

	engine := NewMemoryEngine()
	defer func() {
		if err := engine.Close(); err != nil {
			t.Fatalf("close engine: %v", err)
		}
	}()

	engine.Orchestrator.RegisterWorkflow(pilotShadowWorkflow)
	engine.Orchestrator.RegisterActivity(pilotRecordRefActivity)

	if err := engine.Orchestrator.Start(ctx); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}

	instance, err := engine.Orchestrator.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{
		InstanceID: "pilot-instance-0001",
	}, pilotShadowWorkflow, "evidence:pilot-0001")
	if err != nil {
		t.Fatalf("create workflow instance: %v", err)
	}

	result, err := client.GetWorkflowResult[string](ctx, engine.Orchestrator.Client, instance, 10*time.Second)
	if err != nil {
		t.Fatalf("get workflow result: %v", err)
	}
	if result != "evidence:pilot-0001:observed:observed" {
		t.Fatalf("unexpected workflow result %q", result)
	}

	// Resume proof: the workflow yielded at two activity boundaries and one
	// timer, yet each activity side effect ran exactly once per call site.
	// Replayed history must reuse recorded activity results instead of
	// re-executing the side effect.
	if got := pilotActivityExecutions.Load(); got != 2 {
		t.Fatalf("expected exactly 2 activity executions across replays, got %d", got)
	}
}

func TestMemoryEngineWorkflowResultIsSafeSerializableMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pilotActivityExecutions.Store(0)

	engine := NewMemoryEngine()
	defer func() {
		if err := engine.Close(); err != nil {
			t.Fatalf("close engine: %v", err)
		}
	}()

	engine.Orchestrator.RegisterWorkflow(pilotShadowWorkflow)
	engine.Orchestrator.RegisterActivity(pilotRecordRefActivity)

	if err := engine.Orchestrator.Start(ctx); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}

	instance, err := engine.Orchestrator.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{
		InstanceID: "pilot-instance-0002",
	}, pilotShadowWorkflow, "claim:pilot-0002")
	if err != nil {
		t.Fatalf("create workflow instance: %v", err)
	}

	result, err := client.GetWorkflowResult[string](ctx, engine.Orchestrator.Client, instance, 10*time.Second)
	if err != nil {
		t.Fatalf("get workflow result: %v", err)
	}

	// Workflow inputs/results travel through the backend as serialized
	// payloads. Pin that the spike payload round-trips JSON and stays a
	// bounded safe ref: no raw prompt/source markers, no path roots.
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal workflow result: %v", err)
	}
	var roundTripped string
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatalf("unmarshal workflow result: %v", err)
	}
	if roundTripped != result {
		t.Fatalf("result did not round-trip: %q vs %q", roundTripped, result)
	}
	for _, forbidden := range []string{"raw_prompt", "raw_completion", "raw_stderr", "/home/", "api_key"} {
		if strings.Contains(result, forbidden) {
			t.Fatalf("workflow result contains forbidden material %q: %q", forbidden, result)
		}
	}
	if len(result) > 256 {
		t.Fatalf("workflow result is not a bounded safe ref: %d bytes", len(result))
	}
}

func TestSQLiteEngineConstructorCreatesLocalBackendWithoutUnsafeFixtures(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	engine, err := NewSQLiteEngine("data/durable-workflows.sqlite")
	if err != nil {
		t.Fatalf("open sqlite engine: %v", err)
	}
	if engine.Backend == nil || engine.Orchestrator == nil {
		t.Fatalf("expected backend and orchestrator")
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	dbPath := filepath.Join(dir, "data", "durable-workflows.sqlite")
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read sqlite backend file: %v", err)
	}
	for _, forbidden := range []string{"raw_prompt", "raw_completion", "raw_stderr", "api_key", "secret", "person@example.test", "/home/mac"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("sqlite backend contains forbidden fixture marker %q", forbidden)
		}
	}
}

func TestSQLiteEngineConstructorRejectsUnsafePaths(t *testing.T) {
	for _, path := range []string{
		"data/../durable-workflows.sqlite",
		"/tmp/durable-workflows.sqlite",
		"tmp/durable-workflows.sqlite",
		"data/durable-workflows.db",
		`data\durable-workflows.sqlite`,
	} {
		t.Run(path, func(t *testing.T) {
			if engine, err := NewSQLiteEngine(path); err == nil {
				_ = engine.Close()
				t.Fatalf("expected unsafe path %q to be rejected", path)
			}
		})
	}
}
