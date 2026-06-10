package projectdurable

import "testing"

func TestRunnerShadowRecorderRejectsUnsafeMetadata(t *testing.T) {
	t.Parallel()

	recorder := NewInMemoryRunnerShadowRecorder()
	err := recorder.RecordRunnerShadowEvent(t.Context(), RunnerShadowEvent{
		Boundary: RunnerShadowBoundaryExecuteFinished,
		Run: SafeAutomationRunRef{
			ProjectID:    "project-1",
			AutomationID: "automation-1",
			RunID:        "run-1",
		},
		Status:      "completed",
		SafeSummary: "raw_prompt leaked",
	})
	if err == nil {
		t.Fatal("expected unsafe runner shadow metadata to be rejected")
	}
	if len(recorder.Events()) != 0 {
		t.Fatalf("unsafe event was stored: %#v", recorder.Events())
	}
}

func TestRunnerShadowRecorderStoresCopies(t *testing.T) {
	t.Parallel()

	recorder := NewInMemoryRunnerShadowRecorder()
	event := RunnerShadowEvent{
		Boundary: RunnerShadowBoundaryReported,
		Run: SafeAutomationRunRef{
			ProjectID:    "project-1",
			AutomationID: "automation-1",
			RunID:        "run-1",
		},
		Status:       "completed",
		EvidenceRefs: []string{"evidence:one"},
	}
	if err := recorder.RecordRunnerShadowEvent(t.Context(), event); err != nil {
		t.Fatalf("record shadow event: %v", err)
	}
	event.EvidenceRefs[0] = "evidence:mutated"
	events := recorder.Events()
	events[0].EvidenceRefs[0] = "evidence:mutated-again"
	if got := recorder.Events()[0].EvidenceRefs[0]; got != "evidence:one" {
		t.Fatalf("recorder did not isolate stored event refs, got %q", got)
	}
}
