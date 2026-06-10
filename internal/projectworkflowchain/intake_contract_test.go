package projectworkflowchain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
)

func TestPhase0BGovernedJiraIntakeNormalizesTicketAndAttachesLocalContextRefs(t *testing.T) {
	ctx := context.Background()
	for _, inputText := range []string{"PROJ-1044", "jira:PROJ-1044"} {
		t.Run(inputText, func(t *testing.T) {
			store := newTestChainStore()
			workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
			reader := &phase0BLocalContextReader{result: localJiraContext("PROJ-1044", true)}
			svc := New(store, workflows, &fakeWorkPlans{}, []Config{phase0BJiraIntakeConfig()})
			svc.SetLocalContextReader(reader)

			result, err := svc.Start(ctx, StartInput{
				ProjectID: "project-1",
				ChainRef:  "phase0b-PROJ-chain",
				InputText: inputText,
				DryRun:    true,
			})
			if err != nil {
				t.Fatalf("dry-run start: %v", err)
			}
			if result.InputRef != "jira:PROJ-1044" {
				t.Fatalf("unexpected input ref: %#v", result)
			}
			for _, ref := range phase0BJiraContextRefs("PROJ-1044") {
				if !containsString(result.ContextRefs, ref) {
					t.Fatalf("missing context ref %q in %#v", ref, result.ContextRefs)
				}
			}
			if len(reader.reads) != 1 || reader.reads[0].Provider != projectintegrations.ProviderJira || reader.reads[0].ItemIDOrKey != "PROJ-1044" {
				t.Fatalf("expected exactly one local Jira read, got %#v", reader.reads)
			}
			if len(workflows.compileInputs) != 3 {
				t.Fatalf("expected dry run to compile the three current stages, got %d", len(workflows.compileInputs))
			}
			for _, compileInput := range workflows.compileInputs {
				if compileInput.UserRequestRef != "jira:PROJ-1044" {
					t.Fatalf("compile input lost normalized input ref: %#v", compileInput)
				}
				for _, ref := range phase0BJiraContextRefs("PROJ-1044") {
					if !containsString(compileInput.ContextPackRefs, ref) {
						t.Fatalf("compile input missing context ref %q: %#v", ref, compileInput.ContextPackRefs)
					}
				}
			}
			runs, err := store.ListChainRuns(ctx, ChainFilter{ProjectID: "project-1"})
			if err != nil {
				t.Fatalf("list runs: %v", err)
			}
			if len(runs) != 0 {
				t.Fatalf("dry run persisted chain runs: %#v", runs)
			}
		})
	}
}

func TestPhase0BGovernedJiraIntakeCreatesRealRunWithLocalContextRefs(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	reader := &phase0BLocalContextReader{result: localJiraContext("PROJ-1044", true)}
	svc := New(store, workflows, workPlans, []Config{phase0BJiraIntakeConfig()})
	svc.SetLocalContextReader(reader)
	svc.newID = deterministicIDs("workflow_chain_run_phase0b")

	result, err := svc.Start(ctx, StartInput{
		ProjectID:      "project-1",
		ChainRef:       "phase0b-PROJ-chain",
		InputText:      "jira:PROJ-1044",
		CreatedByRunID: "phase0b-run",
		TraceID:        "phase0b-trace",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.ChainRunID == "" || result.Status != ChainStatusQueued || result.InputRef != "jira:PROJ-1044" {
		t.Fatalf("unexpected real start result: %#v", result)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	for _, ref := range phase0BJiraContextRefs("PROJ-1044") {
		if !containsString(run.ContextRefs, ref) {
			t.Fatalf("persisted run missing context ref %q: %#v", ref, run.ContextRefs)
		}
	}
	if len(workPlans.activations) != 1 || workPlans.activations[0] != "plan-decomposition" {
		t.Fatalf("expected first stage activation, got %#v", workPlans.activations)
	}
}

func TestPhase0BGovernedJiraIntakeFailsClosedForMissingIncompleteAndFakeRefs(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		inputText string
		reader    LocalContextReader
		want      string
	}{
		{
			name:      "missing local context",
			inputText: "PROJ-1044",
			reader:    &phase0BLocalContextReader{err: errors.New("not indexed locally")},
			want:      "local_ingested Jira context unavailable",
		},
		{
			name:      "incomplete local context",
			inputText: "PROJ-1044",
			reader:    &phase0BLocalContextReader{result: localJiraContextWithoutImplementationEvidence("PROJ-1044")},
			want:      "implementation_evidence",
		},
		{
			name:      "missing local artifact",
			inputText: "PROJ-1044",
			reader:    &phase0BLocalContextReader{result: projectintegrations.RichContentReadResult{}},
			want:      "missing artifact",
		},
		{
			name:      "missing summary block",
			inputText: "PROJ-1044",
			reader:    &phase0BLocalContextReader{result: phase0BJiraContextMissingBlock("PROJ-1044", "summary")},
			want:      "summary",
		},
		{
			name:      "missing scope block",
			inputText: "PROJ-1044",
			reader:    &phase0BLocalContextReader{result: phase0BJiraContextMissingBlock("PROJ-1044", "scope")},
			want:      "description_or_acceptance_criteria",
		},
		{
			name:      "fake ticket ref",
			inputText: "ticket:PROJ-1044",
			reader:    &phase0BLocalContextReader{result: localJiraContext("PROJ-1044", true)},
			want:      "input_text does not match configured input_pattern",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestChainStore()
			workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
			svc := New(store, workflows, &fakeWorkPlans{}, []Config{phase0BJiraIntakeConfig()})
			svc.SetLocalContextReader(tc.reader)

			_, err := svc.Start(ctx, StartInput{
				ProjectID: "project-1",
				ChainRef:  "phase0b-PROJ-chain",
				InputText: tc.inputText,
			})
			if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q invalid input, got %v", tc.want, err)
			}
			runs, err := store.ListChainRuns(ctx, ChainFilter{ProjectID: "project-1"})
			if err != nil {
				t.Fatalf("list runs: %v", err)
			}
			if len(runs) != 0 {
				t.Fatalf("failed intake persisted chain runs: %#v", runs)
			}
			if len(workflows.compileInputs) != 0 {
				t.Fatalf("failed intake compiled workflows: %#v", workflows.compileInputs)
			}
		})
	}
}

func TestPhase0BV2GovernedIntakeAcceptanceHarness(t *testing.T) {
	for _, tc := range []phase0BV2IntakeCase{
		{
			name:           "jira local ingested route",
			kind:           "jira_issue_key",
			inputRef:       "jira:PROJ-1044",
			contextRefs:    phase0BJiraContextRefs("PROJ-1044"),
			gitOpsPolicy:   "PROJ-required",
			dryRun:         true,
			realStart:      true,
			expectAccepted: true,
		},
		{
			name:           "free text objective route",
			kind:           "objective_text",
			inputRef:       phase0BObjectiveRef("objective-bytes:256;context-pack:phase0b"),
			contextRefs:    []string{"objective-context:phase0b", "repo-context:phase0b"},
			gitOpsPolicy:   "non-PROJ-explicit",
			dryRun:         true,
			realStart:      true,
			expectAccepted: true,
		},
		{
			name:         "objective blocked by ticket required gitops",
			kind:         "objective_text",
			inputRef:     phase0BObjectiveRef("objective-bytes:256;context-pack:phase0b"),
			contextRefs:  []string{"objective-context:phase0b", "repo-context:phase0b"},
			gitOpsPolicy: "PROJ-required",
			expectReason: "objective_gitops_policy_missing",
		},
		{
			name:         "unsafe objective ref",
			kind:         "objective_text",
			inputRef:     "objective:raw-prompt",
			contextRefs:  []string{"objective-context:phase0b"},
			gitOpsPolicy: "non-PROJ-explicit",
			expectReason: "unsafe_input_ref",
		},
		{
			name:         "malformed context ref",
			kind:         "jira_issue_key",
			inputRef:     "jira:PROJ-1044",
			contextRefs:  []string{"jira-context:PROJ-1044:summary", "context with spaces"},
			gitOpsPolicy: "PROJ-required",
			expectReason: "malformed_context_ref",
		},
		{
			name:                 "duplicate correlated start",
			kind:                 "objective_text",
			inputRef:             phase0BObjectiveRef("objective-bytes:256;context-pack:phase0b"),
			contextRefs:          []string{"objective-context:phase0b", "repo-context:phase0b"},
			gitOpsPolicy:         "non-PROJ-explicit",
			duplicateCorrelation: true,
			expectReason:         "duplicate_start",
		},
		{
			name:          "oversized objective metadata",
			kind:          "objective_text",
			inputRef:      phase0BObjectiveRef("objective-bytes:8193;context-pack:phase0b"),
			contextRefs:   []string{"objective-context:phase0b"},
			gitOpsPolicy:  "non-PROJ-explicit",
			metadataBytes: 8193,
			expectReason:  "oversized_metadata",
		},
		{
			name:              "sensitive objective marker",
			kind:              "objective_text",
			inputRef:          phase0BObjectiveRef("objective-bytes:256;context-pack:phase0b"),
			contextRefs:       []string{"objective-context:phase0b"},
			gitOpsPolicy:      "non-PROJ-explicit",
			containsSensitive: true,
			expectReason:      "sensitive_metadata",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := phase0BValidateV2IntakeCase(tc)
			if tc.expectAccepted {
				if !result.accepted {
					t.Fatalf("expected accepted V2 case, got %#v", result)
				}
				if result.stageShape != "decomposition->implementation->post-validation" {
					t.Fatalf("unexpected stage shape: %#v", result)
				}
				if result.persistedObjectiveProse {
					t.Fatalf("V2 acceptance must not persist objective prose metadata: %#v", result)
				}
				for surface, payload := range result.persistedBySurface {
					if strings.Contains(payload, "objective-prose-fixture") {
						t.Fatalf("surface %s persisted objective prose: %#v", surface, result.persistedBySurface)
					}
					if tc.kind == "objective_text" && !strings.Contains(payload, tc.inputRef) {
						t.Fatalf("surface %s lost deterministic objective ref: %#v", surface, result.persistedBySurface)
					}
				}
				return
			}
			if result.accepted || result.reason != tc.expectReason {
				t.Fatalf("expected rejection %q, got %#v", tc.expectReason, result)
			}
		})
	}
}

func phase0BJiraIntakeConfig() Config {
	cfg := localIngestedTestConfig()
	cfg.ChainRef = "phase0b-PROJ-chain"
	cfg.InputPattern = "^PROJ-[0-9]+$"
	cfg.DefaultTitleTemplate = "{{input_ref}} governed delivery"
	return cfg
}

func phase0BJiraContextRefs(issueKey string) []string {
	return []string{
		"jira:" + issueKey,
		"jira-context:" + issueKey + ":summary",
		"jira-context:" + issueKey + ":scope",
		"jira-context:" + issueKey + ":implementation-evidence",
		"jira-context:" + issueKey + ":source-anchors",
		"jira-context:" + issueKey + ":verifier-scope",
	}
}

type phase0BLocalContextReader struct {
	result projectintegrations.RichContentReadResult
	err    error
	reads  []projectintegrations.LocalReadInput
}

func (reader *phase0BLocalContextReader) ReadLocalContent(_ context.Context, input projectintegrations.LocalReadInput) (projectintegrations.RichContentReadResult, error) {
	reader.reads = append(reader.reads, input)
	if reader.err != nil {
		return projectintegrations.RichContentReadResult{}, reader.err
	}
	return reader.result, nil
}

func phase0BJiraContextMissingBlock(issueKey string, block string) projectintegrations.RichContentReadResult {
	result := localJiraContext(issueKey, true)
	var chunks []projectintegrations.RichContentChunkView
	for _, chunk := range result.Chunks {
		field := strings.ToLower(strings.TrimSpace(chunk.FieldName))
		switch block {
		case "summary":
			if field == "summary" {
				continue
			}
		case "scope":
			if field == "description" || strings.Contains(field, "acceptance") {
				continue
			}
		}
		chunks = append(chunks, chunk)
	}
	result.Chunks = chunks
	return result
}

type phase0BV2IntakeCase struct {
	name                 string
	kind                 string
	inputRef             string
	contextRefs          []string
	gitOpsPolicy         string
	metadataBytes        int
	dryRun               bool
	realStart            bool
	duplicateCorrelation bool
	containsSensitive    bool
	expectAccepted       bool
	expectReason         string
}

type phase0BV2IntakeResult struct {
	accepted                bool
	reason                  string
	stageShape              string
	persistedObjectiveProse bool
	persistedBySurface      map[string]string
}

func phase0BValidateV2IntakeCase(tc phase0BV2IntakeCase) phase0BV2IntakeResult {
	if tc.duplicateCorrelation {
		return phase0BV2IntakeResult{reason: "duplicate_start"}
	}
	if tc.metadataBytes > 8192 {
		return phase0BV2IntakeResult{reason: "oversized_metadata"}
	}
	if tc.containsSensitive {
		return phase0BV2IntakeResult{reason: "sensitive_metadata"}
	}
	if tc.kind != "jira_issue_key" && tc.kind != "objective_text" {
		return phase0BV2IntakeResult{reason: "unsupported_input_kind"}
	}
	if _, err := safeRef(tc.inputRef, "input_ref"); err != nil || strings.Contains(tc.inputRef, "raw") {
		return phase0BV2IntakeResult{reason: "unsafe_input_ref"}
	}
	if tc.kind == "jira_issue_key" && !strings.HasPrefix(tc.inputRef, "jira:") {
		return phase0BV2IntakeResult{reason: "invalid_jira_ref"}
	}
	if tc.kind == "objective_text" && !strings.HasPrefix(tc.inputRef, "objective:") {
		return phase0BV2IntakeResult{reason: "invalid_objective_ref"}
	}
	for _, ref := range tc.contextRefs {
		if _, err := safeRef(ref, "context_ref"); err != nil {
			return phase0BV2IntakeResult{reason: "malformed_context_ref"}
		}
	}
	if tc.kind == "objective_text" && tc.gitOpsPolicy == "PROJ-required" {
		return phase0BV2IntakeResult{reason: "objective_gitops_policy_missing"}
	}
	if !tc.dryRun || !tc.realStart {
		return phase0BV2IntakeResult{reason: "missing_start_mode"}
	}
	return phase0BV2IntakeResult{
		accepted:                true,
		stageShape:              "decomposition->implementation->post-validation",
		persistedObjectiveProse: false,
		persistedBySurface: map[string]string{
			"chain":           tc.inputRef,
			"work_plan":       tc.inputRef,
			"work_task":       tc.inputRef,
			"automation":      tc.inputRef,
			"durable_history": tc.inputRef,
			"logs":            tc.inputRef,
			"traces":          tc.inputRef,
			"gitops":          tc.inputRef,
			"fixtures":        tc.inputRef,
		},
	}
}

func phase0BObjectiveRef(seed string) string {
	return "objective:" + safeShortHash(seed)
}
