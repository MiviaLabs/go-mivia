package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	"github.com/MiviaLabs/go-mivia/internal/projectdurable"
)

func TestBaselineGovernedCloseoutMetadataOnlySafety(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"raw_prompt",
		"raw_completion",
		"raw_stderr",
		"provider_payload",
		"source dump",
		"api_key",
	} {
		t.Run(value, func(t *testing.T) {
			if !unsafeCloseoutRefMaterial(value) {
				t.Fatalf("expected unsafe closeout ref material to reject %q", value)
			}
		})
	}
}

func TestBaselineGovernedCloseoutParserContract(t *testing.T) {
	t.Parallel()

	valid := `{"closeout_action":"needs_review","outcome":"metadata only","safe_next_action":"run verifier","evidence_refs":["evidence:baseline"],"verifier_result_refs":["verifier:baseline"],"child_tasks":[],"block_reason":"","failure_reason":""}`
	output, err := parseGovernedCloseoutOutput(valid)
	if err != nil {
		t.Fatalf("parse valid closeout: %v", err)
	}
	if output.CloseoutAction != "needs_review" || len(output.EvidenceRefs) != 1 || len(output.VerifierRefs) != 1 {
		t.Fatalf("unexpected valid closeout output: %+v", output)
	}

	for name, payload := range map[string]string{
		"missing":       "",
		"prose":         "closeout complete",
		"multiple_json": valid + "\n{}",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseGovernedCloseoutOutput(payload); err == nil {
				t.Fatalf("expected closeout parse to reject %s", name)
			}
		})
	}

	for name, payload := range map[string]string{
		"unsafe_ref":     strings.Replace(valid, "evidence:baseline", "raw_prompt", 1),
		"unsafe_path":    strings.Replace(valid, `"child_tasks":[]`, `"child_tasks":[{"task_ref":"child","title":"Child","description":"metadata","status":"planned","owner_agent":"worker","files_to_read":["../unsafe"],"verification_requirement":"go test ./cmd/mivia-automation-runner","decomposition_quality":"ready"}]`, 1),
		"unsafe_summary": strings.Replace(valid, "metadata only", "raw_prompt", 1),
	} {
		t.Run(name, func(t *testing.T) {
			output, err := parseGovernedCloseoutOutput(payload)
			if err != nil {
				t.Fatalf("parse %s before validation: %v", name, err)
			}
			if err := validateGovernedCloseoutOutput(output, runnerWorkTaskMetadata{}); err == nil {
				t.Fatalf("expected closeout validation to reject %s", name)
			}
		})
	}
}

func TestBaselineGovernedCloseoutFailureCategoryContract(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err         error
		want        string
		wantPrefix  string
		wantContain string
	}{
		"readback_failure": {
			err:  governedCloseoutError{category: governedCloseoutReadbackFailed, err: errors.New("wrapper_readback_failed: wrapper remained open after closeout apply")},
			want: "governed_closeout_readback_failed_wrapper_readback_failed",
		},
		"child_task_create_invalid_input": {
			err:         governedCloseoutError{category: governedCloseoutApplyFailed, err: errors.New("child_task_create_failed: server returned 400 Bad Request: invalid_project_work_task_input: resume_instructions contains unsafe content")},
			wantPrefix:  "governed_closeout_apply_failed_child_task_create_failed_",
			wantContain: "invalid_project_work_task_input",
		},
		"unsafe_child_task_path": {
			err:  governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("unsafe child task path /workspace/private/root")},
			want: "governed_closeout_validation_failed_unsafe_child_task_path",
		},
		"unsafe_ref": {
			err:  governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("unsafe ref raw_prompt")},
			want: "governed_closeout_validation_failed_unsafe_ref",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := governedCloseoutFailureCategory(tc.err)
			if tc.want != "" && got != tc.want {
				t.Fatalf("failure category mismatch: got %q want %q", got, tc.want)
			}
			if tc.wantPrefix != "" && !strings.HasPrefix(got, tc.wantPrefix) {
				t.Fatalf("failure category %q missing prefix %q", got, tc.wantPrefix)
			}
			if tc.wantContain != "" && !strings.Contains(got, tc.wantContain) {
				t.Fatalf("failure category %q missing %q", got, tc.wantContain)
			}
			for _, forbidden := range []string{"raw_prompt", "private/root", "resume_instructions contains unsafe content"} {
				if strings.Contains(got, forbidden) {
					t.Fatalf("failure category leaked unsafe detail %q in %q", forbidden, got)
				}
			}
		})
	}
}

func TestBaselineRunnerCodexCommandAndOutcomeContract(t *testing.T) {
	t.Parallel()

	inputPath := filepath.Join(t.TempDir(), "codex-input.json")
	outputPath := filepath.Join(t.TempDir(), "last-message.txt")
	command, err := buildRunnerCodexCommand(inputPath, outputPath, time.Minute, codexLaunchOptions{
		Path:             "/usr/local/bin/codex",
		Launcher:         "direct",
		WorkDir:          "/workspace/repo",
		Sandbox:          "workspace-write",
		OutputSchemaPath: "/workspace/repo/schema.json",
	})
	if err != nil {
		t.Fatalf("build runner codex command: %v", err)
	}
	joined := strings.Join(command.Args, " ")
	for _, want := range []string{
		"exec",
		"--sandbox workspace-write",
		"--output-schema /workspace/repo/schema.json",
		"--output-last-message " + outputPath,
		"--cd /workspace/repo",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("runner Codex command missing %q in %q", want, joined)
		}
	}
	if command.Path != "/usr/local/bin/codex" || command.Dir != "/workspace/repo" || command.Timeout != time.Minute {
		t.Fatalf("runner Codex command lost path/dir/timeout contract: %#v", command)
	}

	if _, err := buildRunnerCodexCommand(inputPath, outputPath, time.Minute, codexLaunchOptions{Path: "codex", Launcher: "windows-cmd", WorkDir: "/workspace/repo"}); !errors.Is(err, projectautomation.ErrInvalidInput) || !strings.Contains(err.Error(), "unknown codex launcher") {
		t.Fatalf("runner must reject unsupported launcher before execution, got %v", err)
	}
}

func TestBaselineRunnerClientCarriesRunnerIDOnClaimHeartbeatAndComplete(t *testing.T) {
	t.Parallel()

	var claimInput projectautomation.ClaimNextRunInput
	var heartbeatInput projectautomation.HeartbeatRunInput
	var completeInput projectautomation.CompleteAttemptInput
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/claim-next":
			if err := json.NewDecoder(r.Body).Decode(&claimInput); err != nil {
				t.Fatalf("decode claim input: %v", err)
			}
			writeJSON(t, w, projectautomation.ClaimedRun{Run: projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", ClaimID: "claim-1", RunnerID: "runner-baseline"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/heartbeat":
			if err := json.NewDecoder(r.Body).Decode(&heartbeatInput); err != nil {
				t.Fatalf("decode heartbeat input: %v", err)
			}
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", Status: projectautomation.RunStatusRunning, ClaimID: "claim-1", RunnerID: "runner-baseline"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			if err := json.NewDecoder(r.Body).Decode(&completeInput); err != nil {
				t.Fatalf("decode complete input: %v", err)
			}
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", Status: projectautomation.RunStatusCompleted, ClaimID: "claim-1", RunnerID: "runner-baseline"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1":
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", Status: projectautomation.RunStatusCompleted, ClaimID: "claim-1", RunnerID: "runner-baseline"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client(), runnerID: "runner-baseline"}
	claimed, ok, err := client.claimNext(t.Context(), "project-1", "worker")
	if err != nil || !ok {
		t.Fatalf("claim next: ok=%v err=%v", ok, err)
	}
	if err := client.heartbeatRun(t.Context(), "project-1", claimed.Run); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if _, err := client.completeAttempt(t.Context(), "project-1", claimed.Run, projectautomation.CompleteAttemptInput{Status: projectautomation.RunStatusCompleted}); err != nil {
		t.Fatalf("complete attempt: %v", err)
	}
	if claimInput.RunnerID != "runner-baseline" || claimInput.RunnerKind != projectautomation.RunnerKindCodexCLI {
		t.Fatalf("claim did not carry runner identity: %#v", claimInput)
	}
	if heartbeatInput.ClaimID != "claim-1" || heartbeatInput.RunnerID != "runner-baseline" {
		t.Fatalf("heartbeat did not carry claim and runner identity: %#v", heartbeatInput)
	}
	if completeInput.ClaimID != "claim-1" || completeInput.RunnerID != "runner-baseline" {
		t.Fatalf("complete did not carry claim and runner identity: %#v", completeInput)
	}
}

func TestPhase7RunnerShadowHooksPreserveAuthoritativeOrderAndSafeRefs(t *testing.T) {
	recorder := projectdurable.NewInMemoryRunnerShadowRecorder()
	restore := replaceDurableRunnerShadowRecorder(func(durable config.DurableWorkflows) projectdurable.RunnerShadowRecorder {
		if !durable.Enabled || !durable.WorkerEnabled || !durable.ShadowMode {
			return nil
		}
		return recorder
	})
	defer restore()

	var calls []string
	var completeInput projectautomation.CompleteAttemptInput
	completed := projectautomation.AutomationRun{
		ID:           "run-1",
		ProjectID:    "project-1",
		AutomationID: "automation-1",
		Status:       projectautomation.RunStatusCompleted,
		ClaimID:      "claim-1",
		RunnerID:     "runner-phase7",
		AttemptCount: 1,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/claim-next":
			calls = append(calls, "claim")
			writeJSON(t, w, projectautomation.ClaimedRun{Run: projectautomation.AutomationRun{
				ID:           "run-1",
				ProjectID:    "project-1",
				AutomationID: "automation-1",
				Status:       projectautomation.RunStatusRunning,
				ClaimID:      "claim-1",
				RunnerID:     "runner-phase7",
				AttemptCount: 1,
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			calls = append(calls, "report")
			if err := json.NewDecoder(r.Body).Decode(&completeInput); err != nil {
				t.Fatalf("decode complete input: %v", err)
			}
			writeJSON(t, w, completed)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1":
			calls = append(calls, "readback")
			writeJSON(t, w, completed)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client(), runnerID: "runner-phase7", heartbeatInterval: time.Hour}
	status, keepWatching, claimed := claimRunExecuteAndReport(t.Context(), client, config.Config{DurableWorkflows: config.DurableWorkflows{Enabled: true, ShadowMode: true, WorkerEnabled: true}}, "project-1", "", codexLaunchOptions{Path: "/bin/true", Launcher: "direct"})
	if status != 0 || !keepWatching || !claimed {
		t.Fatalf("runner result mismatch: status=%d keepWatching=%v claimed=%v", status, keepWatching, claimed)
	}
	if want := []string{"claim", "report", "readback"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("authoritative call order changed: got %#v want %#v", calls, want)
	}
	if completeInput.ClaimID != "claim-1" || completeInput.RunnerID != "runner-phase7" || completeInput.Status != projectautomation.RunStatusCompleted {
		t.Fatalf("authoritative complete input changed: %#v", completeInput)
	}

	var boundaries []string
	for _, event := range recorder.Events() {
		if err := event.Validate(); err != nil {
			t.Fatalf("shadow event failed safe metadata validation: %#v err=%v", event, err)
		}
		boundaries = append(boundaries, event.Boundary)
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		for _, forbidden := range []string{"raw_prompt", "raw_completion", "raw_stderr", "provider_payload", "/home/", "https://"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("shadow event leaked forbidden material %q in %s", forbidden, encoded)
			}
		}
	}
	wantBoundaries := []string{
		projectdurable.RunnerShadowBoundaryClaimStarted,
		projectdurable.RunnerShadowBoundaryClaimed,
		projectdurable.RunnerShadowBoundaryHeartbeatStarted,
		projectdurable.RunnerShadowBoundaryExecuteStarted,
		projectdurable.RunnerShadowBoundaryExecuteFinished,
		projectdurable.RunnerShadowBoundaryCloseoutFinished,
		projectdurable.RunnerShadowBoundaryReported,
	}
	if !reflect.DeepEqual(boundaries, wantBoundaries) {
		t.Fatalf("shadow boundaries mismatch: got %#v want %#v", boundaries, wantBoundaries)
	}
}

func TestPhase7RunnerShadowFailureIsNonFatalInShadowMode(t *testing.T) {
	restore := replaceDurableRunnerShadowRecorder(func(durable config.DurableWorkflows) projectdurable.RunnerShadowRecorder {
		if !durable.Enabled || !durable.WorkerEnabled || !durable.ShadowMode {
			return nil
		}
		return failingRunnerShadowRecorder{}
	})
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/claim-next":
			writeJSON(t, w, projectautomation.ClaimedRun{Run: projectautomation.AutomationRun{
				ID:           "run-1",
				ProjectID:    "project-1",
				AutomationID: "automation-1",
				Status:       projectautomation.RunStatusRunning,
				ClaimID:      "claim-1",
				RunnerID:     "runner-phase7",
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", Status: projectautomation.RunStatusCompleted, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1":
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", Status: projectautomation.RunStatusCompleted, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client(), runnerID: "runner-phase7", heartbeatInterval: time.Hour}
	status, keepWatching, claimed := claimRunExecuteAndReport(t.Context(), client, config.Config{DurableWorkflows: config.DurableWorkflows{Enabled: true, ShadowMode: true, WorkerEnabled: true}}, "project-1", "", codexLaunchOptions{Path: "/bin/true", Launcher: "direct"})
	if status != 0 || !keepWatching || !claimed {
		t.Fatalf("shadow recorder failure must not fail authoritative run: status=%d keepWatching=%v claimed=%v", status, keepWatching, claimed)
	}
}

func TestPhase7BlockingShadowRecorderDoesNotHangAuthoritativeRun(t *testing.T) {
	restore := replaceDurableRunnerShadowRecorder(func(durable config.DurableWorkflows) projectdurable.RunnerShadowRecorder {
		if !durable.Enabled || !durable.WorkerEnabled || !durable.ShadowMode {
			return nil
		}
		return blockingRunnerShadowRecorder{}
	})
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/claim-next":
			writeJSON(t, w, projectautomation.ClaimedRun{Run: projectautomation.AutomationRun{
				ID:           "run-1",
				ProjectID:    "project-1",
				AutomationID: "automation-1",
				Status:       projectautomation.RunStatusRunning,
				ClaimID:      "claim-1",
				RunnerID:     "runner-phase7",
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", Status: projectautomation.RunStatusCompleted, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1":
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", Status: projectautomation.RunStatusCompleted, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client(), runnerID: "runner-phase7", heartbeatInterval: time.Hour}
	done := make(chan struct{})
	var status int
	var keepWatching bool
	var claimed bool
	go func() {
		defer close(done)
		status, keepWatching, claimed = claimRunExecuteAndReport(t.Context(), client, config.Config{DurableWorkflows: config.DurableWorkflows{Enabled: true, ShadowMode: true, WorkerEnabled: true}}, "project-1", "", codexLaunchOptions{Path: "/bin/true", Launcher: "direct"})
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocking shadow recorder hung authoritative runner")
	}
	if status != 0 || !keepWatching || !claimed {
		t.Fatalf("blocking shadow recorder must not fail authoritative run: status=%d keepWatching=%v claimed=%v", status, keepWatching, claimed)
	}
}

func TestPhase7ContextIgnoringShadowRecorderIsBounded(t *testing.T) {
	var started int32
	recorder := contextIgnoringRunnerShadowRecorder{started: &started}
	restore := replaceDurableRunnerShadowRecorder(func(durable config.DurableWorkflows) projectdurable.RunnerShadowRecorder {
		if !durable.Enabled || !durable.WorkerEnabled || !durable.ShadowMode {
			return nil
		}
		return recorder
	})
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/claim-next":
			writeJSON(t, w, projectautomation.ClaimedRun{Run: projectautomation.AutomationRun{
				ID:           "run-1",
				ProjectID:    "project-1",
				AutomationID: "automation-1",
				Status:       projectautomation.RunStatusRunning,
				ClaimID:      "claim-1",
				RunnerID:     "runner-phase7",
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", Status: projectautomation.RunStatusCompleted, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1":
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", Status: projectautomation.RunStatusCompleted, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client(), runnerID: "runner-phase7", heartbeatInterval: time.Hour}
	done := make(chan struct{})
	var status int
	var keepWatching bool
	var claimed bool
	go func() {
		defer close(done)
		status, keepWatching, claimed = claimRunExecuteAndReport(t.Context(), client, config.Config{DurableWorkflows: config.DurableWorkflows{Enabled: true, ShadowMode: true, WorkerEnabled: true}}, "project-1", "", codexLaunchOptions{Path: "/bin/true", Launcher: "direct"})
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("context-ignoring shadow recorder hung authoritative runner")
	}
	if status != 0 || !keepWatching || !claimed {
		t.Fatalf("context-ignoring shadow recorder must not fail authoritative run: status=%d keepWatching=%v claimed=%v", status, keepWatching, claimed)
	}
	if got := atomic.LoadInt32(&started); got != 1 {
		t.Fatalf("context-ignoring recorder should start at most one shadow call, got %d", got)
	}
	healthy := projectdurable.NewInMemoryRunnerShadowRecorder()
	recordRunnerShadowEvent(t.Context(), newRunnerShadowRecorderState(healthy), projectdurable.RunnerShadowEvent{
		Boundary:   projectdurable.RunnerShadowBoundaryClaimStarted,
		ProjectID:  "project-1",
		ObservedAt: time.Now().UTC(),
	})
	if got := len(healthy.Events()); got != 1 {
		t.Fatalf("context-ignoring recorder timeout should not disable later healthy recorders, got %d events", got)
	}
}

func TestPhase7WorktreeResolveFailureRecordsReportShadowWithoutExecuting(t *testing.T) {
	recorder := projectdurable.NewInMemoryRunnerShadowRecorder()
	restore := replaceDurableRunnerShadowRecorder(func(durable config.DurableWorkflows) projectdurable.RunnerShadowRecorder {
		if !durable.Enabled || !durable.WorkerEnabled || !durable.ShadowMode {
			return nil
		}
		return recorder
	})
	defer restore()

	var calls []string
	var completeInput projectautomation.CompleteAttemptInput
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/claim-next":
			calls = append(calls, "claim")
			writeJSON(t, w, projectautomation.ClaimedRun{Run: projectautomation.AutomationRun{
				ID:           "run-1",
				ProjectID:    "project-1",
				AutomationID: "automation-1",
				PlanID:       "plan-1",
				Status:       projectautomation.RunStatusRunning,
				ClaimID:      "claim-1",
				RunnerID:     "runner-phase7",
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/work-plans/plan-1":
			calls = append(calls, "workplan")
			http.Error(w, "work plan unavailable", http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			calls = append(calls, "report")
			if err := json.NewDecoder(r.Body).Decode(&completeInput); err != nil {
				t.Fatalf("decode complete input: %v", err)
			}
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", PlanID: "plan-1", Status: projectautomation.RunStatusFailed, FailureCategory: "worktree_resolve_failed", ClaimID: "claim-1", RunnerID: "runner-phase7"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1":
			calls = append(calls, "readback")
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", PlanID: "plan-1", Status: projectautomation.RunStatusFailed, FailureCategory: "worktree_resolve_failed", ClaimID: "claim-1", RunnerID: "runner-phase7"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client(), runnerID: "runner-phase7", heartbeatInterval: time.Hour}
	status, keepWatching, claimed := claimRunExecuteAndReport(t.Context(), client, config.Config{DurableWorkflows: config.DurableWorkflows{Enabled: true, ShadowMode: true, WorkerEnabled: true}}, "project-1", "", codexLaunchOptions{Path: "/bin/true", Launcher: "direct"})
	if status != 1 || !keepWatching || !claimed {
		t.Fatalf("worktree resolve failure result mismatch: status=%d keepWatching=%v claimed=%v", status, keepWatching, claimed)
	}
	if want := []string{"claim", "workplan", "report", "readback"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("worktree resolve authoritative order changed: got %#v want %#v", calls, want)
	}
	if completeInput.Status != projectautomation.RunStatusFailed || completeInput.FailureCategory != "worktree_resolve_failed" || completeInput.ClaimID != "claim-1" || completeInput.RunnerID != "runner-phase7" {
		t.Fatalf("worktree resolve complete input mismatch: %#v", completeInput)
	}
	boundaries := runnerShadowBoundaries(recorder.Events())
	wantBoundaries := []string{
		projectdurable.RunnerShadowBoundaryClaimStarted,
		projectdurable.RunnerShadowBoundaryClaimed,
		projectdurable.RunnerShadowBoundaryHeartbeatStarted,
		projectdurable.RunnerShadowBoundaryReported,
	}
	if !reflect.DeepEqual(boundaries, wantBoundaries) {
		t.Fatalf("worktree resolve shadow boundaries mismatch: got %#v want %#v", boundaries, wantBoundaries)
	}
}

func TestPhase7GitOpsPreTaskFailureRecordsReportShadowWithoutExecuting(t *testing.T) {
	recorder := projectdurable.NewInMemoryRunnerShadowRecorder()
	restore := replaceDurableRunnerShadowRecorder(func(durable config.DurableWorkflows) projectdurable.RunnerShadowRecorder {
		if !durable.Enabled || !durable.WorkerEnabled || !durable.ShadowMode {
			return nil
		}
		return recorder
	})
	defer restore()

	var calls []string
	var completeInput projectautomation.CompleteAttemptInput
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/claim-next":
			calls = append(calls, "claim")
			writeJSON(t, w, projectautomation.ClaimedRun{Run: projectautomation.AutomationRun{
				ID:           "run-1",
				ProjectID:    "project-1",
				AutomationID: "automation-1",
				TaskID:       "task-1",
				Status:       projectautomation.RunStatusRunning,
				ClaimID:      "claim-1",
				RunnerID:     "runner-phase7",
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/work-tasks/task-1":
			calls = append(calls, "task")
			writeJSON(t, w, runnerWorkTaskMetadata{ID: "task-1", TaskRef: "implement-task", Status: "claimed", FilesToEdit: []string{"README.md"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			calls = append(calls, "report")
			if err := json.NewDecoder(r.Body).Decode(&completeInput); err != nil {
				t.Fatalf("decode complete input: %v", err)
			}
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", TaskID: "task-1", Status: projectautomation.RunStatusFailed, FailureCategory: completeInput.FailureCategory, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1":
			calls = append(calls, "readback")
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", TaskID: "task-1", Status: projectautomation.RunStatusFailed, FailureCategory: completeInput.FailureCategory, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client(), runnerID: "runner-phase7", heartbeatInterval: time.Hour}
	cfg := config.Config{
		DurableWorkflows: config.DurableWorkflows{Enabled: true, ShadowMode: true, WorkerEnabled: true},
		GitOperations: config.GitOperations{
			Enabled:                true,
			CommitAfterTask:        true,
			RequireCleanBeforeTask: true,
		},
	}
	status, keepWatching, claimed := claimRunExecuteAndReport(t.Context(), client, cfg, "project-1", "", codexLaunchOptions{Path: "/bin/true", Launcher: "direct", WorkDir: t.TempDir()})
	if status != 1 || !keepWatching || !claimed {
		t.Fatalf("gitops pre-task failure result mismatch: status=%d keepWatching=%v claimed=%v", status, keepWatching, claimed)
	}
	if want := []string{"claim", "task", "report", "readback"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("gitops pre-task authoritative order changed: got %#v want %#v", calls, want)
	}
	if completeInput.Status != projectautomation.RunStatusFailed || completeInput.FailureCategory == "" || completeInput.ClaimID != "claim-1" || completeInput.RunnerID != "runner-phase7" {
		t.Fatalf("gitops pre-task complete input mismatch: %#v", completeInput)
	}
	boundaries := runnerShadowBoundaries(recorder.Events())
	wantBoundaries := []string{
		projectdurable.RunnerShadowBoundaryClaimStarted,
		projectdurable.RunnerShadowBoundaryClaimed,
		projectdurable.RunnerShadowBoundaryHeartbeatStarted,
		projectdurable.RunnerShadowBoundaryReported,
	}
	if !reflect.DeepEqual(boundaries, wantBoundaries) {
		t.Fatalf("gitops pre-task shadow boundaries mismatch: got %#v want %#v", boundaries, wantBoundaries)
	}
}

func TestPhase7GitOpsPostTaskRecoveryFailureRecordsReportShadowWithoutExecuting(t *testing.T) {
	recorder := projectdurable.NewInMemoryRunnerShadowRecorder()
	restore := replaceDurableRunnerShadowRecorder(func(durable config.DurableWorkflows) projectdurable.RunnerShadowRecorder {
		if !durable.Enabled || !durable.WorkerEnabled || !durable.ShadowMode {
			return nil
		}
		return recorder
	})
	defer restore()

	var calls []string
	var completeInput projectautomation.CompleteAttemptInput
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/claim-next":
			calls = append(calls, "claim")
			writeJSON(t, w, projectautomation.ClaimedRun{Run: projectautomation.AutomationRun{
				ID:           "run-1",
				ProjectID:    "project-1",
				AutomationID: "automation-1",
				TaskID:       "task-1",
				Status:       projectautomation.RunStatusRunning,
				SafeSummary:  projectautomation.RunSafeSummaryGitOpsPostTaskRecovery,
				ClaimID:      "claim-1",
				RunnerID:     "runner-phase7",
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/work-tasks/task-1":
			calls = append(calls, "task")
			writeJSON(t, w, runnerWorkTaskMetadata{ID: "task-1", TaskRef: "implement-task", Status: "done", FilesToEdit: []string{"README.md"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			calls = append(calls, "report")
			if err := json.NewDecoder(r.Body).Decode(&completeInput); err != nil {
				t.Fatalf("decode complete input: %v", err)
			}
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", TaskID: "task-1", Status: projectautomation.RunStatusFailed, FailureCategory: completeInput.FailureCategory, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1":
			calls = append(calls, "readback")
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", TaskID: "task-1", Status: projectautomation.RunStatusFailed, FailureCategory: completeInput.FailureCategory, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client(), runnerID: "runner-phase7", heartbeatInterval: time.Hour}
	status, keepWatching, claimed := claimRunExecuteAndReport(t.Context(), client, config.Config{DurableWorkflows: config.DurableWorkflows{Enabled: true, ShadowMode: true, WorkerEnabled: true}}, "project-1", "", codexLaunchOptions{Path: "/bin/true", Launcher: "direct", WorkDir: t.TempDir()})
	if status != 1 || !keepWatching || !claimed {
		t.Fatalf("gitops recovery failure result mismatch: status=%d keepWatching=%v claimed=%v", status, keepWatching, claimed)
	}
	if want := []string{"claim", "task", "task", "report", "readback"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("gitops recovery authoritative order changed: got %#v want %#v", calls, want)
	}
	if completeInput.Status != projectautomation.RunStatusFailed || completeInput.FailureCategory != "automation_task_closeout_missing_verifier_refs" || completeInput.ClaimID != "claim-1" || completeInput.RunnerID != "runner-phase7" {
		t.Fatalf("gitops recovery complete input mismatch: %#v", completeInput)
	}
	boundaries := runnerShadowBoundaries(recorder.Events())
	wantBoundaries := []string{
		projectdurable.RunnerShadowBoundaryClaimStarted,
		projectdurable.RunnerShadowBoundaryClaimed,
		projectdurable.RunnerShadowBoundaryHeartbeatStarted,
		projectdurable.RunnerShadowBoundaryReported,
	}
	if !reflect.DeepEqual(boundaries, wantBoundaries) {
		t.Fatalf("gitops recovery shadow boundaries mismatch: got %#v want %#v", boundaries, wantBoundaries)
	}
}

func TestPhase7RunnerReportFailureRemainsAuthoritativeWhenShadowEnabled(t *testing.T) {
	recorder := projectdurable.NewInMemoryRunnerShadowRecorder()
	restore := replaceDurableRunnerShadowRecorder(func(durable config.DurableWorkflows) projectdurable.RunnerShadowRecorder {
		if !durable.Enabled || !durable.WorkerEnabled || !durable.ShadowMode {
			return nil
		}
		return recorder
	})
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/claim-next":
			writeJSON(t, w, projectautomation.ClaimedRun{Run: projectautomation.AutomationRun{
				ID:           "run-1",
				ProjectID:    "project-1",
				AutomationID: "automation-1",
				Status:       projectautomation.RunStatusRunning,
				ClaimID:      "claim-1",
				RunnerID:     "runner-phase7",
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			http.Error(w, "report failed", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client(), runnerID: "runner-phase7", heartbeatInterval: time.Hour}
	status, keepWatching, claimed := claimRunExecuteAndReport(t.Context(), client, config.Config{DurableWorkflows: config.DurableWorkflows{Enabled: true, ShadowMode: true, WorkerEnabled: true}}, "project-1", "", codexLaunchOptions{Path: "/bin/true", Launcher: "direct"})
	if status != 1 || keepWatching || !claimed {
		t.Fatalf("report failure contract changed: status=%d keepWatching=%v claimed=%v", status, keepWatching, claimed)
	}
	for _, event := range recorder.Events() {
		if event.Boundary == projectdurable.RunnerShadowBoundaryReported {
			t.Fatalf("reported shadow event must not be written after authoritative report failure: %#v", event)
		}
	}
}

func TestPhase7WorkerDisabledDoesNotRecordShadowEvents(t *testing.T) {
	recorder := projectdurable.NewInMemoryRunnerShadowRecorder()
	restore := replaceDurableRunnerShadowRecorder(func(durable config.DurableWorkflows) projectdurable.RunnerShadowRecorder {
		if !durable.Enabled || !durable.WorkerEnabled || !durable.ShadowMode {
			return nil
		}
		return recorder
	})
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/claim-next":
			writeJSON(t, w, projectautomation.ClaimedRun{Run: projectautomation.AutomationRun{
				ID:           "run-1",
				ProjectID:    "project-1",
				AutomationID: "automation-1",
				Status:       projectautomation.RunStatusRunning,
				ClaimID:      "claim-1",
				RunnerID:     "runner-phase7",
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", Status: projectautomation.RunStatusCompleted, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1":
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", Status: projectautomation.RunStatusCompleted, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client(), runnerID: "runner-phase7", heartbeatInterval: time.Hour}
	status, keepWatching, claimed := claimRunExecuteAndReport(t.Context(), client, config.Config{DurableWorkflows: config.DurableWorkflows{Enabled: true, ShadowMode: true, WorkerEnabled: false}}, "project-1", "", codexLaunchOptions{Path: "/bin/true", Launcher: "direct"})
	if status != 0 || !keepWatching || !claimed {
		t.Fatalf("worker-disabled runner result mismatch: status=%d keepWatching=%v claimed=%v", status, keepWatching, claimed)
	}
	if events := recorder.Events(); len(events) != 0 {
		t.Fatalf("worker_disabled must not record shadow events: %#v", events)
	}
}

func TestPhase7UnsafeRunMetadataSkipsShadowButNotAuthoritativeRun(t *testing.T) {
	recorder := projectdurable.NewInMemoryRunnerShadowRecorder()
	restore := replaceDurableRunnerShadowRecorder(func(durable config.DurableWorkflows) projectdurable.RunnerShadowRecorder {
		if !durable.Enabled || !durable.WorkerEnabled || !durable.ShadowMode {
			return nil
		}
		return recorder
	})
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/claim-next":
			writeJSON(t, w, projectautomation.ClaimedRun{Run: projectautomation.AutomationRun{
				ID:           "run-1",
				ProjectID:    "project-1",
				AutomationID: "automation-1",
				Status:       projectautomation.RunStatusRunning,
				SafeSummary:  "raw_prompt leaked from upstream",
				ClaimID:      "claim-1",
				RunnerID:     "runner-phase7",
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", Status: projectautomation.RunStatusCompleted, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1":
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", Status: projectautomation.RunStatusCompleted, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client(), runnerID: "runner-phase7", heartbeatInterval: time.Hour}
	status, keepWatching, claimed := claimRunExecuteAndReport(t.Context(), client, config.Config{DurableWorkflows: config.DurableWorkflows{Enabled: true, ShadowMode: true, WorkerEnabled: true}}, "project-1", "", codexLaunchOptions{Path: "/bin/true", Launcher: "direct"})
	if status != 0 || !keepWatching || !claimed {
		t.Fatalf("unsafe shadow metadata must not fail authoritative run: status=%d keepWatching=%v claimed=%v", status, keepWatching, claimed)
	}
	for _, event := range recorder.Events() {
		switch event.Boundary {
		case projectdurable.RunnerShadowBoundaryClaimStarted, projectdurable.RunnerShadowBoundaryReported:
		default:
			t.Fatalf("unsafe pre-report metadata should be skipped, got event %#v", event)
		}
		if event.SafeSummary != "" {
			t.Fatalf("unsafe summary should not be stored in shadow event: %#v", event)
		}
	}
}

func TestPhase7UnsafeReportEvidenceRefsSkipShadowButNotAuthoritativeReport(t *testing.T) {
	recorder := projectdurable.NewInMemoryRunnerShadowRecorder()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", Status: projectautomation.RunStatusFailed, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1":
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", Status: projectautomation.RunStatusFailed, ClaimID: "claim-1", RunnerID: "runner-phase7"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client(), runnerID: "runner-phase7", heartbeatInterval: time.Hour}
	run := projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", AutomationID: "automation-1", Status: projectautomation.RunStatusRunning, ClaimID: "claim-1", RunnerID: "runner-phase7"}
	completedRun, err := completeAttemptAndRecordShadow(t.Context(), client, newRunnerShadowRecorderState(recorder), "project-1", run, projectautomation.CompleteAttemptInput{
		Status:       projectautomation.RunStatusFailed,
		EvidenceRefs: []string{"https://example.invalid/raw_prompt"},
	})
	if err != nil {
		t.Fatalf("authoritative report should still succeed when shadow evidence ref is unsafe: %v", err)
	}
	if completedRun.Status != projectautomation.RunStatusFailed {
		t.Fatalf("authoritative completed status mismatch: %#v", completedRun)
	}
	if events := recorder.Events(); len(events) != 0 {
		t.Fatalf("unsafe report evidence ref must not be stored in shadow events: %#v", events)
	}
}

func TestPhase7WorkerEnabledPermitsOnlyShadowRunnerRecorder(t *testing.T) {
	if recorder := newDurableRunnerShadowRecorder(config.DurableWorkflows{Enabled: true, WorkerEnabled: true, ShadowMode: false}); recorder != nil {
		t.Fatalf("worker_enabled without shadow_mode must not construct authoritative durable runner recorder: %#v", recorder)
	}
	if recorder := newDurableRunnerShadowRecorder(config.DurableWorkflows{Enabled: true, WorkerEnabled: true, ShadowMode: true}); recorder == nil {
		t.Fatal("worker_enabled with shadow_mode should construct optional shadow recorder")
	}
}

type failingRunnerShadowRecorder struct{}

func (failingRunnerShadowRecorder) RecordRunnerShadowEvent(context.Context, projectdurable.RunnerShadowEvent) error {
	return fmt.Errorf("shadow recorder unavailable")
}

type blockingRunnerShadowRecorder struct{}

func (blockingRunnerShadowRecorder) RecordRunnerShadowEvent(ctx context.Context, _ projectdurable.RunnerShadowEvent) error {
	<-ctx.Done()
	return ctx.Err()
}

type contextIgnoringRunnerShadowRecorder struct {
	started *int32
}

func (r contextIgnoringRunnerShadowRecorder) RecordRunnerShadowEvent(context.Context, projectdurable.RunnerShadowEvent) error {
	atomic.AddInt32(r.started, 1)
	select {}
}

func runnerShadowBoundaries(events []projectdurable.RunnerShadowEvent) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Boundary)
	}
	return out
}

func replaceDurableRunnerShadowRecorder(replacement func(config.DurableWorkflows) projectdurable.RunnerShadowRecorder) func() {
	previous := newDurableRunnerShadowRecorder
	newDurableRunnerShadowRecorder = replacement
	return func() {
		newDurableRunnerShadowRecorder = previous
	}
}
