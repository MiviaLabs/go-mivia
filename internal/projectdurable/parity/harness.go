package parity

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/MiviaLabs/go-mivia/internal/projectdurable"
)

var ErrParityMetadata = errors.New("invalid parity metadata")

type Snapshot struct {
	KnownRefs  []string
	Automation AutomationRunSnapshot
	WorkPlan   WorkPlanSnapshot
	WorkTasks  []WorkTaskSnapshot
	Chain      ChainSnapshot
	GitOps     GitOpsSnapshot
	Evidence   EvidenceSnapshot
	Confidence ConfidenceSnapshot
	Knowledge  KnowledgeSnapshot
	Control    ControlFlowSnapshot
}

type AutomationRunSnapshot struct {
	Status          string
	FailureCategory string
	SafeSummary     string
	AttemptCount    int
	ClaimRefs       []string
	RunnerRefs      []string
}

type WorkPlanSnapshot struct {
	Status         string
	SafeNextAction string
}

type WorkTaskSnapshot struct {
	TaskRef        string
	Status         string
	DependencyRefs []string
	VerifierRefs   []string
	ReviewRefs     []string
	EvidenceRefs   []string
	ContextRefs    []string
	ArtifactRefs   []string
}

type ChainSnapshot struct {
	Status          string
	StageStatuses   map[string]string
	StageRefs       []string
	CarriedTaskRefs []string
	GitOpsReady     bool
	PullRequestRefs []string
}

type GitOpsSnapshot struct {
	Refs              []string
	FailureCategories []string
}

type EvidenceSnapshot struct {
	Refs []string
}

type ConfidenceSnapshot struct {
	Refs []string
}

type KnowledgeSnapshot struct {
	Refs []string
}

type ControlFlowSnapshot struct {
	WorkerEnabled                 bool
	DurableExecutionAuthoritative bool
	ComparisonError               string
	AuthoritativeResultChanged    bool
	Events                        []TraceEvent
	DurableMutations              []Mutation
}

type TraceEvent struct {
	Kind string
}

type Mutation struct {
	Ref                string
	ApprovedShadowPort bool
	MatchesCurrentPath bool
}

type Comparison struct {
	Scenario    string
	Divergences []string
}

func (c Comparison) Equal() bool { return len(c.Divergences) == 0 }

func CompareSnapshots(scenario string, current Snapshot, durable Snapshot) (Comparison, error) {
	if err := current.Validate(); err != nil {
		return Comparison{}, fmt.Errorf("current snapshot: %w", err)
	}
	if err := durable.Validate(); err != nil {
		return Comparison{}, fmt.Errorf("durable snapshot: %w", err)
	}
	current = current.normalized()
	durable = durable.normalized()
	comparison := Comparison{Scenario: scenario}
	if !reflect.DeepEqual(current.Automation, durable.Automation) {
		comparison.Divergences = append(comparison.Divergences, "automation")
	}
	if !reflect.DeepEqual(current.WorkPlan, durable.WorkPlan) {
		comparison.Divergences = append(comparison.Divergences, "work_plan")
	}
	if !reflect.DeepEqual(current.WorkTasks, durable.WorkTasks) {
		comparison.Divergences = append(comparison.Divergences, "work_tasks")
	}
	if !reflect.DeepEqual(current.Chain, durable.Chain) {
		comparison.Divergences = append(comparison.Divergences, "workflow_chain")
	}
	if !reflect.DeepEqual(current.GitOps, durable.GitOps) {
		comparison.Divergences = append(comparison.Divergences, "gitops")
	}
	if !reflect.DeepEqual(current.Evidence, durable.Evidence) {
		comparison.Divergences = append(comparison.Divergences, "evidence")
	}
	if !reflect.DeepEqual(current.Confidence, durable.Confidence) {
		comparison.Divergences = append(comparison.Divergences, "confidence")
	}
	if !reflect.DeepEqual(current.Knowledge, durable.Knowledge) {
		comparison.Divergences = append(comparison.Divergences, "knowledge")
	}
	return comparison, nil
}

func (s Snapshot) Validate() error {
	known, err := knownRefSet(s.KnownRefs)
	if err != nil {
		return err
	}
	if err := validateRequiredStatus(s.Automation.Status, "automation.status"); err != nil {
		return err
	}
	if s.Automation.FailureCategory != "" {
		if err := projectdurable.DurableFailureCategory(s.Automation.FailureCategory).Validate(); err != nil {
			return fmt.Errorf("%w: automation failure category", err)
		}
	}
	if err := projectdurable.ValidateSafeSummary(s.Automation.SafeSummary); err != nil {
		return fmt.Errorf("%w: automation safe summary", err)
	}
	if s.Automation.AttemptCount < 0 {
		return fmt.Errorf("%w: automation attempt count", ErrParityMetadata)
	}
	if err := validateRefs("automation.claim_refs", s.Automation.ClaimRefs, known); err != nil {
		return err
	}
	if err := validateRefs("automation.runner_refs", s.Automation.RunnerRefs, known); err != nil {
		return err
	}
	if err := validateRequiredStatus(s.WorkPlan.Status, "work_plan.status"); err != nil {
		return err
	}
	if err := projectdurable.ValidateSafeSummary(s.WorkPlan.SafeNextAction); err != nil {
		return fmt.Errorf("%w: work plan next action", err)
	}
	if len(s.WorkTasks) == 0 {
		return fmt.Errorf("%w: at least one work task is required", ErrParityMetadata)
	}
	for _, task := range s.WorkTasks {
		if err := validateRequiredRef(task.TaskRef, "work_task.task_ref", known); err != nil {
			return err
		}
		if err := validateRequiredStatus(task.Status, "work_task.status"); err != nil {
			return err
		}
		for name, refs := range map[string][]string{
			"work_task.dependency_refs": task.DependencyRefs,
			"work_task.verifier_refs":   task.VerifierRefs,
			"work_task.review_refs":     task.ReviewRefs,
			"work_task.evidence_refs":   task.EvidenceRefs,
			"work_task.context_refs":    task.ContextRefs,
			"work_task.artifact_refs":   task.ArtifactRefs,
		} {
			if err := validateRefs(name, refs, known); err != nil {
				return err
			}
		}
	}
	if err := validateRequiredStatus(s.Chain.Status, "chain.status"); err != nil {
		return err
	}
	if len(s.Chain.StageStatuses) == 0 {
		return fmt.Errorf("%w: chain stage status is required", ErrParityMetadata)
	}
	for stageRef, status := range s.Chain.StageStatuses {
		if err := validateRequiredRef(stageRef, "chain.stage_ref", known); err != nil {
			return err
		}
		if err := validateRequiredStatus(status, "chain.stage_status"); err != nil {
			return err
		}
	}
	if err := validateRefs("chain.stage_refs", s.Chain.StageRefs, known); err != nil {
		return err
	}
	if err := validateRefs("chain.carried_task_refs", s.Chain.CarriedTaskRefs, known); err != nil {
		return err
	}
	if err := validateRefs("chain.pull_request_refs", s.Chain.PullRequestRefs, known); err != nil {
		return err
	}
	if err := validateRefs("gitops.refs", s.GitOps.Refs, known); err != nil {
		return err
	}
	if err := validateRefs("gitops.failure_categories", s.GitOps.FailureCategories, known); err != nil {
		return err
	}
	if err := validateRefs("evidence.refs", s.Evidence.Refs, known); err != nil {
		return err
	}
	if err := validateRefs("confidence.refs", s.Confidence.Refs, known); err != nil {
		return err
	}
	if err := validateRefs("knowledge.refs", s.Knowledge.Refs, known); err != nil {
		return err
	}
	return s.Control.Validate()
}

func (c ControlFlowSnapshot) Validate() error {
	if c.WorkerEnabled && c.DurableExecutionAuthoritative {
		return fmt.Errorf("%w: worker_enabled must not permit authoritative durable execution", ErrParityMetadata)
	}
	if c.ComparisonError != "" {
		if err := projectdurable.ValidateSafeSummary(c.ComparisonError); err != nil {
			return fmt.Errorf("%w: comparison error", err)
		}
		if c.AuthoritativeResultChanged {
			return fmt.Errorf("%w: comparison failure changed authoritative result", ErrParityMetadata)
		}
	}
	seenComparison := false
	for _, event := range c.Events {
		if err := projectdurable.ValidateSafeRef(event.Kind); err != nil {
			return fmt.Errorf("%w: control event", err)
		}
		if seenComparison {
			return fmt.Errorf("%w: durable comparison must be the last control event", ErrParityMetadata)
		}
		if event.Kind == "durable_comparison" {
			seenComparison = true
		}
	}
	if err := ValidateRunnerOrder(c.Events); err != nil {
		return err
	}
	for _, mutation := range c.DurableMutations {
		if err := projectdurable.ValidateSafeRef(mutation.Ref); err != nil {
			return fmt.Errorf("%w: mutation ref", err)
		}
		if !mutation.ApprovedShadowPort || !mutation.MatchesCurrentPath {
			return fmt.Errorf("%w: durable mutation is not approved shadow-port parity", ErrParityMetadata)
		}
	}
	return nil
}

func ValidateRunnerOrder(events []TraceEvent) error {
	want := []string{"runner_claim", "runner_execute", "runner_heartbeat", "runner_report"}
	position := 0
	for _, event := range events {
		if position < len(want) && event.Kind == want[position] {
			position++
		}
	}
	if position == 0 {
		return nil
	}
	if position != len(want) {
		return fmt.Errorf("%w: runner claim/execute/heartbeat/report order changed", ErrParityMetadata)
	}
	return nil
}

func (s Snapshot) normalized() Snapshot {
	s.KnownRefs = sortedStrings(s.KnownRefs)
	s.Automation.ClaimRefs = sortedStrings(s.Automation.ClaimRefs)
	s.Automation.RunnerRefs = sortedStrings(s.Automation.RunnerRefs)
	for i := range s.WorkTasks {
		s.WorkTasks[i].DependencyRefs = sortedStrings(s.WorkTasks[i].DependencyRefs)
		s.WorkTasks[i].VerifierRefs = sortedStrings(s.WorkTasks[i].VerifierRefs)
		s.WorkTasks[i].ReviewRefs = sortedStrings(s.WorkTasks[i].ReviewRefs)
		s.WorkTasks[i].EvidenceRefs = sortedStrings(s.WorkTasks[i].EvidenceRefs)
		s.WorkTasks[i].ContextRefs = sortedStrings(s.WorkTasks[i].ContextRefs)
		s.WorkTasks[i].ArtifactRefs = sortedStrings(s.WorkTasks[i].ArtifactRefs)
	}
	sort.Slice(s.WorkTasks, func(i, j int) bool { return s.WorkTasks[i].TaskRef < s.WorkTasks[j].TaskRef })
	s.Chain.StageRefs = sortedStrings(s.Chain.StageRefs)
	s.Chain.CarriedTaskRefs = sortedStrings(s.Chain.CarriedTaskRefs)
	s.Chain.PullRequestRefs = sortedStrings(s.Chain.PullRequestRefs)
	s.GitOps.Refs = sortedStrings(s.GitOps.Refs)
	s.GitOps.FailureCategories = sortedStrings(s.GitOps.FailureCategories)
	s.Evidence.Refs = sortedStrings(s.Evidence.Refs)
	s.Confidence.Refs = sortedStrings(s.Confidence.Refs)
	s.Knowledge.Refs = sortedStrings(s.Knowledge.Refs)
	return s
}

func knownRefSet(refs []string) (map[string]struct{}, error) {
	known := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if err := projectdurable.ValidateSafeRef(ref); err != nil {
			return nil, fmt.Errorf("%w: known ref", err)
		}
		if _, exists := known[ref]; exists {
			return nil, fmt.Errorf("%w: duplicate known ref", ErrParityMetadata)
		}
		known[ref] = struct{}{}
	}
	return known, nil
}

func validateRequiredStatus(status string, name string) error {
	return validateRequiredRef(status, name, nil)
}

func validateRequiredRef(ref string, name string, known map[string]struct{}) error {
	if err := projectdurable.ValidateSafeRef(ref); err != nil {
		return fmt.Errorf("%w: %s", err, name)
	}
	if len(known) > 0 {
		if _, ok := known[ref]; !ok {
			return fmt.Errorf("%w: stale %s", ErrParityMetadata, name)
		}
	}
	return nil
}

func validateRefs(name string, refs []string, known map[string]struct{}) error {
	seen := map[string]struct{}{}
	for _, ref := range refs {
		if err := validateRequiredRef(ref, name, known); err != nil {
			return err
		}
		if _, exists := seen[ref]; exists {
			return fmt.Errorf("%w: duplicate %s", ErrParityMetadata, name)
		}
		seen[ref] = struct{}{}
	}
	return nil
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
