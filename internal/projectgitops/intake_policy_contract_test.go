package projectgitops

import (
	"errors"
	"strings"
	"testing"
)

func TestPhase0BGitOpsTicketRequiredPolicyBlocksObjectiveWork(t *testing.T) {
	_, err := Render(PostTaskInput{
		ProjectID:       "project-1",
		PlanID:          "work_plan_phase0b",
		TaskID:          "work_task_phase0b",
		TaskRef:         "objective-phase0b",
		TaskTitle:       "Objective Phase0B",
		BranchName:      "feat-objective-phase0b",
		AutomationID:    "automation_phase0b",
		AutomationRunID: "automation_run_phase0b",
		OperatorID:      "operator_phase0b",
	}, Conventions{
		CommitType:               "feat",
		RequireTicket:            true,
		TicketRefPattern:         `^PROJ-[0-9]+$`,
		PullRequestTitleTemplate: "{{change_type}}({{ticket_ref}}): complete {{work_task_title}}",
		WhatChangedTemplate:      "Changed {{work_task_ref}}",
		HowVerifiedTemplate:      "Verified {{verifier_refs}}",
		TestsTemplate:            "go test ./internal/projectgitops",
	})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "ticket_ref is required") {
		t.Fatalf("expected required-ticket policy to block objective work, got %v", err)
	}
}

func TestPhase0BGitOpsExplicitNonTicketPolicyCanRenderObjectiveRefs(t *testing.T) {
	rendered, err := Render(PostTaskInput{
		ProjectID:       "project-1",
		PlanID:          "work_plan_phase0b",
		TaskID:          "work_task_phase0b",
		TaskRef:         "objective-phase0b",
		TaskTitle:       "Objective Phase0B",
		BranchName:      "feat-objective-phase0b",
		AutomationID:    "automation_phase0b",
		AutomationRunID: "automation_run_phase0b",
		OperatorID:      "operator_phase0b",
		VerifierRefs:    []string{"verifier:phase0b"},
		TestResults:     []string{"go test ./internal/projectgitops: passed"},
	}, Conventions{
		CommitType:               "feat",
		RequireTicket:            false,
		PullRequestTitleTemplate: "{{change_type}}: complete {{work_task_ref}}",
		WhatChangedTemplate:      "Changed {{work_task_ref}}",
		HowVerifiedTemplate:      "Verified {{verifier_refs}}",
		TestsTemplate:            "{{test_results}}",
	})
	if err != nil {
		t.Fatalf("expected explicit non-ticket policy to render objective work: %v", err)
	}
	payload := rendered.CommitSubject + "\n" + rendered.CommitBody + "\n" + rendered.PullRequestTitle + "\n" + rendered.PullRequestBody
	for _, forbidden := range []string{"objective prose", "prompt material", "source material", "ticket_ref is required"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("rendered objective metadata leaked forbidden marker %q: %s", forbidden, payload)
		}
	}
	if strings.Contains(payload, "PROJ-") {
		t.Fatalf("non-ticket objective render invented ticket metadata: %s", payload)
	}
}
