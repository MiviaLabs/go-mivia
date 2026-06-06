package projectautomation

import (
	"context"

	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

const (
	GovernanceActionKindAutomationRunStart = "automation_run_start"
	GovernanceOutcomeKindAutomationAttempt = "automation_attempt"
)

type GovernanceOptions struct {
	Evidence   EvidenceGovernance
	Confidence ConfidenceGovernance
	Knowledge  KnowledgeGovernance
}

type EvidenceGovernance interface {
	CreateActionRef(context.Context, GovernanceActionInput) (string, error)
	CreateOutcomeRef(context.Context, GovernanceOutcomeInput) (string, error)
}

type ConfidenceGovernance interface {
	RecordConfidenceRef(context.Context, GovernanceConfidenceInput) (string, error)
}

type KnowledgeGovernance interface {
	CreateCandidateRef(context.Context, GovernanceKnowledgeCandidateInput) (string, error)
}

type GovernanceActionInput struct {
	ProjectID    string
	AutomationID string
	RunID        string
	TaskID       string
	ActionKind   string
}

type GovernanceOutcomeInput struct {
	ProjectID     string
	AutomationID  string
	RunID         string
	TaskID        string
	AttemptID     string
	Status        string
	DurationMS    int64
	VerifierRefs  []string
	EvidenceRefs  []string
	ClaimRefs     []string
	ReviewRefs    []string
	KnowledgeRefs []string
}

type GovernanceConfidenceInput struct {
	ProjectID    string
	RunID        string
	TaskID       string
	ClaimRefs    []string
	EvidenceRefs []string
	VerifierRefs []string
	ReviewRefs   []string
	OutcomeRef   string
}

type GovernanceKnowledgeCandidateInput struct {
	ProjectID     string
	RunID         string
	TaskID        string
	ClaimRefs     []string
	EvidenceRefs  []string
	VerifierRefs  []string
	ReviewRefs    []string
	OutcomeRef    string
	ConfidenceRef string
}

type reviewResultAttacher interface {
	AttachReviewResult(context.Context, projectworkplan.AttachInput) (projectworkplan.Attachment, error)
}

type knowledgeCandidateAttacher interface {
	AttachKnowledgeCandidate(context.Context, projectworkplan.AttachInput) (projectworkplan.Attachment, error)
}

func (svc *Service) attachRunStartGovernance(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask) error {
	if svc.options.Governance.Evidence == nil || svc.workTasks == nil {
		return nil
	}
	actionRef, err := svc.options.Governance.Evidence.CreateActionRef(ctx, GovernanceActionInput{
		ProjectID:    run.ProjectID,
		AutomationID: run.AutomationID,
		RunID:        run.ID,
		TaskID:       task.ID,
		ActionKind:   GovernanceActionKindAutomationRunStart,
	})
	if err != nil || actionRef == "" {
		return err
	}
	_, err = svc.workTasks.AttachEvidence(ctx, projectworkplan.AttachInput{
		ProjectID:       run.ProjectID,
		TaskID:          task.ID,
		Ref:             actionRef,
		AttachedByRunID: run.ID,
		TraceID:         run.TraceID,
		Note:            "automation_run_start_action_ref",
	})
	return err
}

func (svc *Service) attachAttemptGovernance(ctx context.Context, run AutomationRun, attempt AutomationAttempt, refs attemptGovernanceRefs) ([]string, error) {
	if svc.workTasks == nil || run.TaskID == "" {
		return refs.KnowledgeRefs, nil
	}
	outcomeRef := ""
	if svc.options.Governance.Evidence != nil {
		ref, err := svc.options.Governance.Evidence.CreateOutcomeRef(ctx, GovernanceOutcomeInput{
			ProjectID:     run.ProjectID,
			AutomationID:  run.AutomationID,
			RunID:         run.ID,
			TaskID:        run.TaskID,
			AttemptID:     attempt.ID,
			Status:        attempt.Status,
			DurationMS:    attempt.DurationMS,
			VerifierRefs:  refs.VerifierRefs,
			EvidenceRefs:  refs.EvidenceRefs,
			ClaimRefs:     refs.ClaimRefs,
			ReviewRefs:    refs.ReviewRefs,
			KnowledgeRefs: refs.KnowledgeRefs,
		})
		if err != nil {
			return refs.KnowledgeRefs, err
		}
		outcomeRef = ref
		if outcomeRef != "" {
			_, err = svc.workTasks.AttachEvidence(ctx, projectworkplan.AttachInput{
				ProjectID:       run.ProjectID,
				TaskID:          run.TaskID,
				Ref:             outcomeRef,
				AttachedByRunID: run.ID,
				TraceID:         run.TraceID,
				Note:            "automation_attempt_outcome_ref",
			})
			if err != nil {
				return refs.KnowledgeRefs, err
			}
		}
	}
	for _, verifierRef := range refs.VerifierRefs {
		if _, err := svc.workTasks.AttachVerifierResult(ctx, projectworkplan.AttachInput{ProjectID: run.ProjectID, TaskID: run.TaskID, Ref: verifierRef, AttachedByRunID: run.ID, TraceID: run.TraceID, Note: "automation_attempt_verifier_ref"}); err != nil {
			return refs.KnowledgeRefs, err
		}
	}
	for _, evidenceRef := range refs.EvidenceRefs {
		if _, err := svc.workTasks.AttachEvidence(ctx, projectworkplan.AttachInput{ProjectID: run.ProjectID, TaskID: run.TaskID, Ref: evidenceRef, AttachedByRunID: run.ID, TraceID: run.TraceID, Note: "automation_attempt_evidence_ref"}); err != nil {
			return refs.KnowledgeRefs, err
		}
	}
	if len(refs.ReviewRefs) > 0 {
		reviewAttacher, ok := svc.workTasks.(reviewResultAttacher)
		if ok {
			for _, reviewRef := range refs.ReviewRefs {
				if _, err := reviewAttacher.AttachReviewResult(ctx, projectworkplan.AttachInput{ProjectID: run.ProjectID, TaskID: run.TaskID, Ref: reviewRef, AttachedByRunID: run.OrchestratorRunID, TraceID: run.TraceID, Note: "automation_attempt_review_ref"}); err != nil {
					return refs.KnowledgeRefs, err
				}
			}
		}
	}
	confidenceRef := ""
	if svc.options.Governance.Confidence != nil && len(refs.ClaimRefs) > 0 {
		ref, err := svc.options.Governance.Confidence.RecordConfidenceRef(ctx, GovernanceConfidenceInput{ProjectID: run.ProjectID, RunID: run.ID, TaskID: run.TaskID, ClaimRefs: refs.ClaimRefs, EvidenceRefs: append(refs.EvidenceRefs, outcomeRef), VerifierRefs: refs.VerifierRefs, ReviewRefs: refs.ReviewRefs, OutcomeRef: outcomeRef})
		if err != nil {
			return refs.KnowledgeRefs, err
		}
		confidenceRef = ref
	}
	if svc.options.Governance.Knowledge == nil || len(refs.ClaimRefs) == 0 || len(refs.VerifierRefs) == 0 || len(refs.ReviewRefs) == 0 {
		return refs.KnowledgeRefs, nil
	}
	candidateRef, err := svc.options.Governance.Knowledge.CreateCandidateRef(ctx, GovernanceKnowledgeCandidateInput{ProjectID: run.ProjectID, RunID: run.ID, TaskID: run.TaskID, ClaimRefs: refs.ClaimRefs, EvidenceRefs: append(refs.EvidenceRefs, outcomeRef), VerifierRefs: refs.VerifierRefs, ReviewRefs: refs.ReviewRefs, OutcomeRef: outcomeRef, ConfidenceRef: confidenceRef})
	if err != nil {
		return refs.KnowledgeRefs, err
	}
	if candidateRef == "" {
		return refs.KnowledgeRefs, nil
	}
	refs.KnowledgeRefs = append(refs.KnowledgeRefs, candidateRef)
	if knowledgeAttacher, ok := svc.workTasks.(knowledgeCandidateAttacher); ok {
		if _, err := knowledgeAttacher.AttachKnowledgeCandidate(ctx, projectworkplan.AttachInput{ProjectID: run.ProjectID, TaskID: run.TaskID, Ref: candidateRef, AttachedByRunID: run.ID, TraceID: run.TraceID, Note: "automation_attempt_knowledge_candidate_ref"}); err != nil {
			return refs.KnowledgeRefs, err
		}
	}
	return refs.KnowledgeRefs, nil
}

type attemptGovernanceRefs struct {
	VerifierRefs  []string
	EvidenceRefs  []string
	ClaimRefs     []string
	ReviewRefs    []string
	KnowledgeRefs []string
}
