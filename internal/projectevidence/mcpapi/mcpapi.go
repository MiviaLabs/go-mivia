package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence/store"
)

var evidenceGraphTools = []string{
	"projects.evidence_graph.claims.create",
	"projects.evidence_graph.claims.get",
	"projects.evidence_graph.claims.list",
	"projects.evidence_graph.evidence.append",
	"projects.evidence_graph.decisions.create",
	"projects.evidence_graph.actions.create",
	"projects.evidence_graph.outcomes.create",
	"projects.evidence_graph.artifacts.link",
	"projects.evidence_graph.promotions.link",
}

const (
	defaultPageSize = 50
	maxPageSize     = 100
)

type ClaimList struct {
	Claims        []projectevidence.Claim `json:"claims"`
	NextPageToken string                  `json:"next_page_token,omitempty"`
}

func ToolDefinitions() []map[string]any {
	ref := map[string]any{"type": "string", "minLength": 1, "maxLength": 200}
	text := map[string]any{"type": "string", "minLength": 1, "maxLength": 500}
	optionalText := map[string]any{"type": "string", "maxLength": 500}
	return []map[string]any{
		{
			"name":        "projects.evidence_graph.claims.create",
			"title":       "Create Evidence Graph Claim",
			"description": "Create one project-scoped metadata-only Evidence Graph claim without raw prompts, source, provider payloads, secrets, roots, or personal data.",
			"inputSchema": objectSchema(map[string]any{
				"id":        ref,
				"run_id":    map[string]any{"type": "string", "maxLength": 200},
				"trace_id":  map[string]any{"type": "string", "maxLength": 200},
				"claim_ref": ref,
				"summary":   text,
				"status":    map[string]any{"type": "string", "enum": []string{projectevidence.ClaimStatusCandidate, projectevidence.ClaimStatusValidated, projectevidence.ClaimStatusPromoted, projectevidence.ClaimStatusRejected}},
			}, []string{"id", "claim_ref", "summary"}),
		},
		{
			"name":        "projects.evidence_graph.claims.get",
			"title":       "Get Evidence Graph Claim",
			"description": "Fetch one project-scoped Evidence Graph claim chain as metadata only.",
			"inputSchema": objectSchema(map[string]any{
				"id":       ref,
				"claim_id": ref,
			}, []string{"id", "claim_id"}),
		},
		{
			"name":        "projects.evidence_graph.claims.list",
			"title":       "List Evidence Graph Claims",
			"description": "List project-scoped Evidence Graph claims with safe metadata filters only.",
			"inputSchema": objectSchema(map[string]any{
				"id":              ref,
				"artifact_ref":    map[string]any{"type": "string", "maxLength": 200},
				"promotion_state": map[string]any{"type": "string", "enum": []string{projectevidence.PromotionStateCandidate, projectevidence.PromotionStateValidated, projectevidence.PromotionStatePromoted, projectevidence.PromotionStateRejected}},
				"outcome_status":  map[string]any{"type": "string", "enum": []string{projectevidence.OutcomeStatusPassed, projectevidence.OutcomeStatusFailed, projectevidence.OutcomeStatusBlocked, projectevidence.OutcomeStatusUnknown}},
				"run_id":          map[string]any{"type": "string", "maxLength": 200},
				"trace_id":        map[string]any{"type": "string", "maxLength": 200},
				"page_size":       map[string]any{"type": "integer", "minimum": 1, "maximum": maxPageSize},
				"page_token":      map[string]any{"type": "string", "maxLength": 20},
			}, []string{"id"}),
		},
		{
			"name":        "projects.evidence_graph.evidence.append",
			"title":       "Append Evidence Graph Evidence",
			"description": "Append one safe evidence reference to a project-scoped claim.",
			"inputSchema": objectSchema(map[string]any{
				"id":            ref,
				"claim_id":      ref,
				"evidence_ref":  ref,
				"evidence_kind": map[string]any{"type": "string", "enum": []string{projectevidence.EvidenceKindContextPack, projectevidence.EvidenceKindFile, projectevidence.EvidenceKindChunk, projectevidence.EvidenceKindSymbol, projectevidence.EvidenceKindVerifier, projectevidence.EvidenceKindClaimCheck, projectevidence.EvidenceKindArtifact, projectevidence.EvidenceKindOther}},
				"source_ref":    map[string]any{"type": "string", "maxLength": 200},
				"summary":       optionalText,
			}, []string{"id", "claim_id", "evidence_ref", "evidence_kind"}),
		},
		{
			"name":        "projects.evidence_graph.decisions.create",
			"title":       "Create Evidence Graph Decision",
			"description": "Create one safe decision linked to an Evidence Graph claim.",
			"inputSchema": objectSchema(map[string]any{
				"id":           ref,
				"claim_id":     ref,
				"decision_ref": ref,
				"state":        map[string]any{"type": "string", "enum": []string{projectevidence.DecisionStateValidated, projectevidence.DecisionStatePromoted, projectevidence.DecisionStateRejected}},
				"verifier_ref": ref,
				"rationale":    text,
			}, []string{"id", "claim_id", "decision_ref", "state", "verifier_ref", "rationale"}),
		},
		{
			"name":        "projects.evidence_graph.actions.create",
			"title":       "Create Evidence Graph Action",
			"description": "Create one safe action linked to an Evidence Graph decision.",
			"inputSchema": objectSchema(map[string]any{
				"id":            ref,
				"claim_id":      ref,
				"decision_id":   ref,
				"action_ref":    ref,
				"action_kind":   map[string]any{"type": "string", "enum": []string{projectevidence.ActionKindCodeChange, projectevidence.ActionKindDocChange, projectevidence.ActionKindVerifierRun, projectevidence.ActionKindConfigChange, projectevidence.ActionKindReviewComment, projectevidence.ActionKindOther}},
				"summary":       optionalText,
				"changed_files": map[string]any{"type": "array", "items": map[string]any{"type": "string", "maxLength": 300}, "maxItems": 100},
				"run_id":        map[string]any{"type": "string", "maxLength": 200},
			}, []string{"id", "claim_id", "decision_id", "action_ref", "action_kind"}),
		},
		{
			"name":        "projects.evidence_graph.outcomes.create",
			"title":       "Create Evidence Graph Outcome",
			"description": "Create one safe outcome linked to an Evidence Graph action.",
			"inputSchema": objectSchema(map[string]any{
				"id":           ref,
				"claim_id":     ref,
				"action_id":    ref,
				"outcome_ref":  ref,
				"outcome_kind": map[string]any{"type": "string", "enum": []string{projectevidence.OutcomeKindTest, projectevidence.OutcomeKindBuild, projectevidence.OutcomeKindClaimCheck, projectevidence.OutcomeKindManualReview, projectevidence.OutcomeKindPromotion, projectevidence.OutcomeKindFailure, projectevidence.OutcomeKindOther}},
				"status":       map[string]any{"type": "string", "enum": []string{projectevidence.OutcomeStatusPassed, projectevidence.OutcomeStatusFailed, projectevidence.OutcomeStatusBlocked, projectevidence.OutcomeStatusUnknown}},
				"verifier_ref": map[string]any{"type": "string", "maxLength": 200},
				"summary":      optionalText,
			}, []string{"id", "claim_id", "action_id", "outcome_ref", "outcome_kind", "status"}),
		},
		{
			"name":        "projects.evidence_graph.artifacts.link",
			"title":       "Link Evidence Graph Artifact",
			"description": "Link a safe artifact ref to an Evidence Graph claim.",
			"inputSchema": objectSchema(map[string]any{
				"id":            ref,
				"claim_id":      ref,
				"artifact_ref":  ref,
				"artifact_kind": map[string]any{"type": "string", "maxLength": 200},
				"run_id":        map[string]any{"type": "string", "maxLength": 200},
			}, []string{"id", "claim_id", "artifact_ref"}),
		},
		{
			"name":        "projects.evidence_graph.promotions.link",
			"title":       "Link Evidence Graph Promotion",
			"description": "Link a safe promotion decision ref to an Evidence Graph claim.",
			"inputSchema": objectSchema(map[string]any{
				"id":              ref,
				"claim_id":        ref,
				"run_id":          map[string]any{"type": "string", "maxLength": 200},
				"artifact_ref":    ref,
				"promotion_state": map[string]any{"type": "string", "enum": []string{projectevidence.PromotionStateCandidate, projectevidence.PromotionStateValidated, projectevidence.PromotionStatePromoted, projectevidence.PromotionStateRejected}},
				"source_ref":      ref,
				"verifier_ref":    map[string]any{"type": "string", "maxLength": 200},
				"decision_ref":    map[string]any{"type": "string", "maxLength": 200},
				"action_ref":      map[string]any{"type": "string", "maxLength": 200},
				"outcome_ref":     map[string]any{"type": "string", "maxLength": 200},
			}, []string{"id", "claim_id", "artifact_ref", "promotion_state", "source_ref"}),
		},
	}
}

func IsEvidenceGraphTool(name string) bool {
	for _, tool := range evidenceGraphTools {
		if name == tool || name == strings.ReplaceAll(tool, ".", "_") {
			return true
		}
	}
	return false
}

func CallTool(ctx context.Context, svc *projectevidence.Service, name string, arguments json.RawMessage) (map[string]any, error) {
	if svc == nil {
		return nil, store.ErrNotFound
	}
	switch name {
	case "projects.evidence_graph.claims.create", "projects_evidence_graph_claims_create":
		var input struct {
			ID       string          `json:"id"`
			RunID    string          `json:"run_id,omitempty"`
			TraceID  string          `json:"trace_id,omitempty"`
			ClaimRef string          `json:"claim_ref"`
			Summary  string          `json:"summary"`
			Status   string          `json:"status,omitempty"`
			Meta     json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid evidence graph arguments", projectevidence.ErrInvalidInput)
		}
		claim, err := svc.CreateClaim(ctx, projectevidence.CreateClaimInput{ProjectID: input.ID, RunID: input.RunID, TraceID: input.TraceID, ClaimRef: input.ClaimRef, Summary: input.Summary, Status: input.Status})
		return toolResult(claim), err
	case "projects.evidence_graph.claims.get", "projects_evidence_graph_claims_get":
		var input claimIDInput
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid evidence graph arguments", projectevidence.ErrInvalidInput)
		}
		record, err := svc.GetClaim(ctx, input.ID, input.ClaimID)
		return toolResult(record), err
	case "projects.evidence_graph.claims.list", "projects_evidence_graph_claims_list":
		var input struct {
			ID             string          `json:"id"`
			ArtifactRef    string          `json:"artifact_ref,omitempty"`
			PromotionState string          `json:"promotion_state,omitempty"`
			OutcomeStatus  string          `json:"outcome_status,omitempty"`
			RunID          string          `json:"run_id,omitempty"`
			TraceID        string          `json:"trace_id,omitempty"`
			PageSize       int             `json:"page_size,omitempty"`
			PageToken      string          `json:"page_token,omitempty"`
			Meta           json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid evidence graph arguments", projectevidence.ErrInvalidInput)
		}
		claims, err := svc.ListClaims(ctx, input.ID, projectevidence.ClaimFilter{ArtifactRef: input.ArtifactRef, PromotionState: input.PromotionState, OutcomeStatus: input.OutcomeStatus, RunID: input.RunID, TraceID: input.TraceID})
		if err != nil {
			return nil, err
		}
		list, err := paginateClaims(claims, input.PageSize, input.PageToken)
		return toolResult(list), err
	case "projects.evidence_graph.evidence.append", "projects_evidence_graph_evidence_append":
		var input struct {
			ID           string          `json:"id"`
			ClaimID      string          `json:"claim_id"`
			EvidenceRef  string          `json:"evidence_ref"`
			EvidenceKind string          `json:"evidence_kind"`
			SourceRef    string          `json:"source_ref,omitempty"`
			Summary      string          `json:"summary,omitempty"`
			Meta         json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid evidence graph arguments", projectevidence.ErrInvalidInput)
		}
		evidence, err := svc.AppendEvidence(ctx, projectevidence.AppendEvidenceInput{ProjectID: input.ID, ClaimID: input.ClaimID, EvidenceRef: input.EvidenceRef, EvidenceKind: input.EvidenceKind, SourceRef: input.SourceRef, Summary: input.Summary})
		return toolResult(evidence), err
	case "projects.evidence_graph.decisions.create", "projects_evidence_graph_decisions_create":
		var input struct {
			ID          string          `json:"id"`
			ClaimID     string          `json:"claim_id"`
			DecisionRef string          `json:"decision_ref"`
			State       string          `json:"state"`
			VerifierRef string          `json:"verifier_ref"`
			Rationale   string          `json:"rationale"`
			Meta        json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid evidence graph arguments", projectevidence.ErrInvalidInput)
		}
		decision, err := svc.CreateDecision(ctx, projectevidence.CreateDecisionInput{ProjectID: input.ID, ClaimID: input.ClaimID, DecisionRef: input.DecisionRef, State: input.State, VerifierRef: input.VerifierRef, Rationale: input.Rationale})
		return toolResult(decision), err
	case "projects.evidence_graph.actions.create", "projects_evidence_graph_actions_create":
		var input struct {
			ID           string          `json:"id"`
			ClaimID      string          `json:"claim_id"`
			DecisionID   string          `json:"decision_id"`
			ActionRef    string          `json:"action_ref"`
			ActionKind   string          `json:"action_kind"`
			Summary      string          `json:"summary,omitempty"`
			ChangedFiles []string        `json:"changed_files,omitempty"`
			RunID        string          `json:"run_id,omitempty"`
			Meta         json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid evidence graph arguments", projectevidence.ErrInvalidInput)
		}
		action, err := svc.CreateAction(ctx, projectevidence.CreateActionInput{ProjectID: input.ID, ClaimID: input.ClaimID, DecisionID: input.DecisionID, ActionRef: input.ActionRef, ActionKind: input.ActionKind, Summary: input.Summary, ChangedFiles: input.ChangedFiles, RunID: input.RunID})
		return toolResult(action), err
	case "projects.evidence_graph.outcomes.create", "projects_evidence_graph_outcomes_create":
		var input struct {
			ID          string          `json:"id"`
			ClaimID     string          `json:"claim_id"`
			ActionID    string          `json:"action_id"`
			OutcomeRef  string          `json:"outcome_ref"`
			OutcomeKind string          `json:"outcome_kind"`
			Status      string          `json:"status"`
			VerifierRef string          `json:"verifier_ref,omitempty"`
			Summary     string          `json:"summary,omitempty"`
			Meta        json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid evidence graph arguments", projectevidence.ErrInvalidInput)
		}
		outcome, err := svc.CreateOutcome(ctx, projectevidence.CreateOutcomeInput{ProjectID: input.ID, ClaimID: input.ClaimID, ActionID: input.ActionID, OutcomeRef: input.OutcomeRef, OutcomeKind: input.OutcomeKind, Status: input.Status, VerifierRef: input.VerifierRef, Summary: input.Summary})
		return toolResult(outcome), err
	case "projects.evidence_graph.artifacts.link", "projects_evidence_graph_artifacts_link":
		var input struct {
			ID           string          `json:"id"`
			ClaimID      string          `json:"claim_id"`
			ArtifactRef  string          `json:"artifact_ref"`
			ArtifactKind string          `json:"artifact_kind,omitempty"`
			RunID        string          `json:"run_id,omitempty"`
			Meta         json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid evidence graph arguments", projectevidence.ErrInvalidInput)
		}
		link, err := svc.LinkArtifact(ctx, projectevidence.LinkArtifactInput{ProjectID: input.ID, ClaimID: input.ClaimID, ArtifactRef: input.ArtifactRef, ArtifactKind: input.ArtifactKind, RunID: input.RunID})
		return toolResult(link), err
	case "projects.evidence_graph.promotions.link", "projects_evidence_graph_promotions_link":
		var input struct {
			ID             string          `json:"id"`
			ClaimID        string          `json:"claim_id"`
			RunID          string          `json:"run_id,omitempty"`
			ArtifactRef    string          `json:"artifact_ref"`
			PromotionState string          `json:"promotion_state"`
			SourceRef      string          `json:"source_ref"`
			VerifierRef    string          `json:"verifier_ref,omitempty"`
			DecisionRef    string          `json:"decision_ref,omitempty"`
			ActionRef      string          `json:"action_ref,omitempty"`
			OutcomeRef     string          `json:"outcome_ref,omitempty"`
			Meta           json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid evidence graph arguments", projectevidence.ErrInvalidInput)
		}
		link, err := svc.LinkPromotion(ctx, projectevidence.LinkPromotionInput{ProjectID: input.ID, ClaimID: input.ClaimID, RunID: input.RunID, ArtifactRef: input.ArtifactRef, PromotionState: input.PromotionState, SourceRef: input.SourceRef, VerifierRef: input.VerifierRef, DecisionRef: input.DecisionRef, ActionRef: input.ActionRef, OutcomeRef: input.OutcomeRef})
		return toolResult(link), err
	default:
		return nil, store.ErrNotFound
	}
}

type claimIDInput struct {
	ID      string          `json:"id"`
	ClaimID string          `json:"claim_id"`
	Meta    json.RawMessage `json:"_meta,omitempty"`
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

func toolResult(value any) map[string]any {
	encoded, _ := json.Marshal(value)
	return map[string]any{
		"content": []map[string]string{
			{"type": "text", "text": string(encoded)},
		},
		"structuredContent": value,
		"isError":           false,
	}
}

func paginateClaims(claims []projectevidence.Claim, pageSize int, pageToken string) (ClaimList, error) {
	if pageSize == 0 {
		pageSize = defaultPageSize
	}
	if pageSize < 1 || pageSize > maxPageSize {
		return ClaimList{}, fmt.Errorf("%w: page_size must be between 1 and %d", projectevidence.ErrInvalidInput, maxPageSize)
	}
	offset := 0
	pageToken = strings.TrimSpace(pageToken)
	if pageToken != "" {
		parsed, err := strconv.Atoi(pageToken)
		if err != nil || parsed < 0 {
			return ClaimList{}, fmt.Errorf("%w: page_token is invalid", projectevidence.ErrInvalidInput)
		}
		offset = parsed
	}
	if offset >= len(claims) {
		return ClaimList{Claims: []projectevidence.Claim{}}, nil
	}
	end := offset + pageSize
	if end > len(claims) {
		end = len(claims)
	}
	nextToken := ""
	if end < len(claims) {
		nextToken = strconv.Itoa(end)
	}
	return ClaimList{Claims: append([]projectevidence.Claim(nil), claims[offset:end]...), NextPageToken: nextToken}, nil
}

func decodeRaw(raw json.RawMessage, dst any) error {
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		raw = json.RawMessage(encoded)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
