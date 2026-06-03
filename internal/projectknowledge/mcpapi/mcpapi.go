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

	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge/store"
)

var knowledgeTools = []string{
	"projects.knowledge.candidates.create",
	"projects.knowledge.validate",
	"projects.knowledge.promote_project",
	"projects.knowledge.submit_org_review",
	"projects.knowledge.promote_org",
	"projects.knowledge.reject",
	"projects.knowledge.supersede",
	"projects.knowledge.reuse_events.record",
	"projects.knowledge.get",
	"projects.knowledge.list",
	"orgs.knowledge.list",
}

const (
	defaultPageSize = 50
	maxPageSize     = 100
)

type KnowledgeList struct {
	Knowledge     []projectknowledge.KnowledgeRecord `json:"knowledge"`
	NextPageToken string                             `json:"next_page_token,omitempty"`
}

func ToolDefinitions() []map[string]any {
	ref := map[string]any{"type": "string", "minLength": 1, "maxLength": 200}
	text := map[string]any{"type": "string", "minLength": 1, "maxLength": 500}
	optionalText := map[string]any{"type": "string", "maxLength": 500}
	refList := map[string]any{"type": "array", "items": map[string]any{"type": "string", "maxLength": 200}, "maxItems": 100}
	filterProps := map[string]any{
		"scope":           map[string]any{"type": "string", "enum": []string{projectknowledge.ScopeProject, projectknowledge.ScopeOrg}},
		"state":           map[string]any{"type": "string", "enum": []string{projectknowledge.StateCandidate, projectknowledge.StateValidated, projectknowledge.StateProjectPromoted, projectknowledge.StateOrgReview, projectknowledge.StateOrgPromoted, projectknowledge.StateRejected, projectknowledge.StateSuperseded}},
		"claim_id":        ref,
		"knowledge_ref":   ref,
		"confidence_band": map[string]any{"type": "string", "enum": []string{projectconfidence.ScoreBandHigh, projectconfidence.ScoreBandMedium, projectconfidence.ScoreBandLow, projectconfidence.ScoreBandUnknown}},
		"min_confidence":  map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
		"max_confidence":  map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
		"page_size":       map[string]any{"type": "integer", "minimum": 1, "maximum": maxPageSize},
		"page_token":      map[string]any{"type": "string", "maxLength": 20},
	}
	return []map[string]any{
		{"name": "projects.knowledge.candidates.create", "title": "Create Knowledge Candidate", "description": "Create one project-scoped metadata-only knowledge candidate from safe Evidence Graph and confidence refs.", "inputSchema": objectSchema(map[string]any{
			"id":                       ref,
			"knowledge_ref":            ref,
			"claim_id":                 ref,
			"claim_ref":                ref,
			"confidence_assessment_id": map[string]any{"type": "string", "maxLength": 200},
			"confidence_score":         map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
			"confidence_band":          map[string]any{"type": "string", "enum": []string{projectconfidence.ScoreBandHigh, projectconfidence.ScoreBandMedium, projectconfidence.ScoreBandLow, projectconfidence.ScoreBandUnknown}},
			"summary":                  text,
			"reuse_guidance":           text,
			"evidence_refs":            refList,
			"verifier_refs":            refList,
			"outcome_refs":             refList,
			"promotion_refs":           refList,
			"supersedes_ref":           map[string]any{"type": "string", "maxLength": 200},
		}, []string{"id", "knowledge_ref", "claim_id", "claim_ref", "summary", "reuse_guidance"})},
		{"name": "projects.knowledge.validate", "title": "Validate Knowledge Candidate", "description": "Validate one project-scoped knowledge candidate using the configured Evidence Graph and Confidence Engine adapter.", "inputSchema": transitionSchema(ref, text)},
		{"name": "projects.knowledge.promote_project", "title": "Promote Project Knowledge", "description": "Promote validated knowledge at project scope using the configured Evidence Graph and Confidence Engine adapter.", "inputSchema": transitionSchema(ref, text)},
		{"name": "projects.knowledge.submit_org_review", "title": "Submit Knowledge For Org Review", "description": "Explicitly submit project-promoted knowledge for default org review; this does not promote org knowledge automatically.", "inputSchema": objectSchema(map[string]any{"id": ref, "knowledge_id": ref, "org_ref": map[string]any{"type": "string", "enum": []string{projectknowledge.DefaultOrgRef}}, "decision_ref": ref, "verifier_ref": ref, "rationale": text, "decided_by": ref}, []string{"id", "knowledge_id", "org_ref", "decision_ref", "verifier_ref", "rationale", "decided_by"})},
		{"name": "projects.knowledge.promote_org", "title": "Promote Org Knowledge", "description": "Explicitly promote org-reviewed knowledge to default org scope with stricter metadata gates derived from Evidence Graph and Confidence Engine metadata.", "inputSchema": objectSchema(map[string]any{"id": ref, "knowledge_id": ref, "scope": map[string]any{"type": "string", "enum": []string{projectknowledge.ScopeOrg}}, "org_ref": map[string]any{"type": "string", "enum": []string{projectknowledge.DefaultOrgRef}}, "decision_ref": ref, "verifier_ref": ref, "rationale": text, "decided_by": ref}, []string{"id", "knowledge_id", "scope", "org_ref", "decision_ref", "verifier_ref", "rationale", "decided_by"})},
		{"name": "projects.knowledge.reject", "title": "Reject Knowledge", "description": "Reject a knowledge record without deleting metadata.", "inputSchema": objectSchema(map[string]any{"id": ref, "knowledge_id": ref, "decision_ref": ref, "verifier_ref": ref, "rationale": text, "decided_by": map[string]any{"type": "string", "maxLength": 200}}, []string{"id", "knowledge_id", "decision_ref", "verifier_ref", "rationale"})},
		{"name": "projects.knowledge.supersede", "title": "Supersede Knowledge", "description": "Mark a knowledge record as superseded without deleting metadata.", "inputSchema": objectSchema(map[string]any{"id": ref, "knowledge_id": ref, "superseded_by_ref": ref, "decision_ref": ref, "verifier_ref": ref, "rationale": text, "decided_by": map[string]any{"type": "string", "maxLength": 200}}, []string{"id", "knowledge_id", "superseded_by_ref", "decision_ref", "verifier_ref", "rationale"})},
		{"name": "projects.knowledge.reuse_events.record", "title": "Record Knowledge Reuse Event", "description": "Record safe metadata about reuse of a knowledge record.", "inputSchema": objectSchema(map[string]any{"id": ref, "knowledge_id": ref, "agent_run_id": map[string]any{"type": "string", "maxLength": 200}, "trace_id": map[string]any{"type": "string", "maxLength": 200}, "reuse_ref": ref, "revalidated": map[string]any{"type": "boolean"}, "revalidation_ref": map[string]any{"type": "string", "maxLength": 200}, "outcome": map[string]any{"type": "string", "enum": []string{projectknowledge.ReuseOutcomeUsed, projectknowledge.ReuseOutcomeSkipped, projectknowledge.ReuseOutcomeStale, projectknowledge.ReuseOutcomeContradicted}}, "summary": optionalText}, []string{"id", "knowledge_id", "reuse_ref", "revalidated", "outcome"})},
		{"name": "projects.knowledge.get", "title": "Get Knowledge", "description": "Fetch one project-scoped knowledge record as metadata only.", "inputSchema": objectSchema(map[string]any{"id": ref, "knowledge_id": ref}, []string{"id", "knowledge_id"})},
		{"name": "projects.knowledge.list", "title": "List Project Knowledge", "description": "List project-scoped knowledge records with safe metadata filters only.", "inputSchema": objectSchema(withProperty(filterProps, "id", ref), []string{"id"})},
		{"name": "orgs.knowledge.list", "title": "List Org Knowledge", "description": "List default org-promoted knowledge only; org-wide non-promoted records are not exposed.", "inputSchema": objectSchema(withProperty(filterProps, "org_ref", map[string]any{"type": "string", "enum": []string{projectknowledge.DefaultOrgRef}}), []string{"org_ref"})},
	}
}

func IsKnowledgeTool(name string) bool {
	for _, tool := range knowledgeTools {
		if name == tool || name == strings.ReplaceAll(tool, ".", "_") {
			return true
		}
	}
	return false
}

func CallTool(ctx context.Context, svc *projectknowledge.Service, adapter *projectknowledge.PromotionInputAdapter, name string, arguments json.RawMessage) (map[string]any, error) {
	if svc == nil {
		return nil, store.ErrNotFound
	}
	switch name {
	case "projects.knowledge.candidates.create", "projects_knowledge_candidates_create":
		var input createCandidateInput
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid knowledge arguments", projectknowledge.ErrInvalidInput)
		}
		record, err := svc.CreateCandidate(ctx, projectknowledge.CreateCandidateInput{ProjectID: input.ID, KnowledgeRef: input.KnowledgeRef, ClaimID: input.ClaimID, ClaimRef: input.ClaimRef, ConfidenceAssessmentID: input.ConfidenceAssessmentID, ConfidenceScore: input.ConfidenceScore, ConfidenceBand: input.ConfidenceBand, Summary: input.Summary, ReuseGuidance: input.ReuseGuidance, EvidenceRefs: input.EvidenceRefs, VerifierRefs: input.VerifierRefs, OutcomeRefs: input.OutcomeRefs, PromotionRefs: input.PromotionRefs, SupersedesRef: input.SupersedesRef})
		return toolResult(record), err
	case "projects.knowledge.validate", "projects_knowledge_validate":
		var input transitionInput
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid knowledge arguments", projectknowledge.ErrInvalidInput)
		}
		if adapter == nil {
			return nil, fmt.Errorf("%w: promotion input adapter is required", projectknowledge.ErrInvalidInput)
		}
		record, err := svc.ValidateCandidateWithInputs(ctx, adapter, projectknowledge.ValidateCandidateWithInputsInput{ProjectID: input.ID, KnowledgeID: input.KnowledgeID, DecisionRef: input.DecisionRef, VerifierRef: input.VerifierRef, Rationale: input.Rationale})
		return toolResult(record), err
	case "projects.knowledge.promote_project", "projects_knowledge_promote_project":
		var input transitionInput
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid knowledge arguments", projectknowledge.ErrInvalidInput)
		}
		if adapter == nil {
			return nil, fmt.Errorf("%w: promotion input adapter is required", projectknowledge.ErrInvalidInput)
		}
		record, err := svc.PromoteProjectWithInputs(ctx, adapter, projectknowledge.PromoteProjectWithInputsInput{ProjectID: input.ID, KnowledgeID: input.KnowledgeID, DecisionRef: input.DecisionRef, VerifierRef: input.VerifierRef, Rationale: input.Rationale})
		return toolResult(record), err
	case "projects.knowledge.submit_org_review", "projects_knowledge_submit_org_review":
		var input orgReviewInput
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid knowledge arguments", projectknowledge.ErrInvalidInput)
		}
		record, err := svc.SubmitOrgReview(ctx, projectknowledge.SubmitOrgReviewInput{ProjectID: input.ID, KnowledgeID: input.KnowledgeID, OrgRef: input.OrgRef, DecisionRef: input.DecisionRef, VerifierRef: input.VerifierRef, Rationale: input.Rationale, DecidedBy: input.DecidedBy})
		return toolResult(record), err
	case "projects.knowledge.promote_org", "projects_knowledge_promote_org":
		var input promoteOrgInput
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid knowledge arguments", projectknowledge.ErrInvalidInput)
		}
		if adapter == nil {
			return nil, fmt.Errorf("%w: promotion input adapter is required", projectknowledge.ErrInvalidInput)
		}
		record, err := svc.PromoteOrgWithInputs(ctx, adapter, projectknowledge.PromoteOrgWithInputsInput{ProjectID: input.ID, KnowledgeID: input.KnowledgeID, Scope: input.Scope, OrgRef: input.OrgRef, DecisionRef: input.DecisionRef, VerifierRef: input.VerifierRef, Rationale: input.Rationale, DecidedBy: input.DecidedBy})
		return toolResult(record), err
	case "projects.knowledge.reject", "projects_knowledge_reject":
		var input rejectInput
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid knowledge arguments", projectknowledge.ErrInvalidInput)
		}
		record, err := svc.Reject(ctx, projectknowledge.RejectInput{ProjectID: input.ID, KnowledgeID: input.KnowledgeID, DecisionRef: input.DecisionRef, VerifierRef: input.VerifierRef, Rationale: input.Rationale, DecidedBy: input.DecidedBy})
		return toolResult(record), err
	case "projects.knowledge.supersede", "projects_knowledge_supersede":
		var input supersedeInput
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid knowledge arguments", projectknowledge.ErrInvalidInput)
		}
		record, err := svc.Supersede(ctx, projectknowledge.SupersedeInput{ProjectID: input.ID, KnowledgeID: input.KnowledgeID, SupersededByRef: input.SupersededByRef, DecisionRef: input.DecisionRef, VerifierRef: input.VerifierRef, Rationale: input.Rationale, DecidedBy: input.DecidedBy})
		return toolResult(record), err
	case "projects.knowledge.reuse_events.record", "projects_knowledge_reuse_events_record":
		var input reuseInput
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid knowledge arguments", projectknowledge.ErrInvalidInput)
		}
		event, err := svc.RecordReuseEvent(ctx, projectknowledge.RecordReuseEventInput{ProjectID: input.ID, KnowledgeID: input.KnowledgeID, AgentRunID: input.AgentRunID, TraceID: input.TraceID, ReuseRef: input.ReuseRef, Revalidated: input.Revalidated, RevalidationRef: input.RevalidationRef, Outcome: input.Outcome, Summary: input.Summary})
		return toolResult(event), err
	case "projects.knowledge.get", "projects_knowledge_get":
		var input knowledgeIDInput
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid knowledge arguments", projectknowledge.ErrInvalidInput)
		}
		record, err := svc.GetKnowledge(ctx, input.ID, input.KnowledgeID)
		return toolResult(record), err
	case "projects.knowledge.list", "projects_knowledge_list":
		var input listInput
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid knowledge arguments", projectknowledge.ErrInvalidInput)
		}
		records, err := svc.ListKnowledge(ctx, input.ID, input.toFilter())
		if err != nil {
			return nil, err
		}
		list, err := paginateKnowledge(records, input.PageSize, input.PageToken)
		return toolResult(list), err
	case "orgs.knowledge.list", "orgs_knowledge_list":
		var input orgListInput
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid knowledge arguments", projectknowledge.ErrInvalidInput)
		}
		records, err := svc.ListOrgKnowledge(ctx, input.OrgRef, input.toFilter())
		if err != nil {
			return nil, err
		}
		list, err := paginateKnowledge(records, input.PageSize, input.PageToken)
		return toolResult(list), err
	default:
		return nil, store.ErrNotFound
	}
}

type createCandidateInput struct {
	ID                     string   `json:"id"`
	KnowledgeRef           string   `json:"knowledge_ref"`
	ClaimID                string   `json:"claim_id"`
	ClaimRef               string   `json:"claim_ref"`
	ConfidenceAssessmentID string   `json:"confidence_assessment_id,omitempty"`
	ConfidenceScore        int      `json:"confidence_score,omitempty"`
	ConfidenceBand         string   `json:"confidence_band,omitempty"`
	Summary                string   `json:"summary"`
	ReuseGuidance          string   `json:"reuse_guidance"`
	EvidenceRefs           []string `json:"evidence_refs,omitempty"`
	VerifierRefs           []string `json:"verifier_refs,omitempty"`
	OutcomeRefs            []string `json:"outcome_refs,omitempty"`
	PromotionRefs          []string `json:"promotion_refs,omitempty"`
	SupersedesRef          string   `json:"supersedes_ref,omitempty"`
}

type transitionInput struct {
	ID          string `json:"id"`
	KnowledgeID string `json:"knowledge_id"`
	DecisionRef string `json:"decision_ref"`
	VerifierRef string `json:"verifier_ref"`
	Rationale   string `json:"rationale"`
}

type orgReviewInput struct {
	ID          string `json:"id"`
	KnowledgeID string `json:"knowledge_id"`
	OrgRef      string `json:"org_ref"`
	DecisionRef string `json:"decision_ref"`
	VerifierRef string `json:"verifier_ref"`
	Rationale   string `json:"rationale"`
	DecidedBy   string `json:"decided_by"`
}

type promoteOrgInput struct {
	ID          string `json:"id"`
	KnowledgeID string `json:"knowledge_id"`
	Scope       string `json:"scope"`
	OrgRef      string `json:"org_ref"`
	DecisionRef string `json:"decision_ref"`
	VerifierRef string `json:"verifier_ref"`
	Rationale   string `json:"rationale"`
	DecidedBy   string `json:"decided_by"`
}

type rejectInput struct {
	ID          string `json:"id"`
	KnowledgeID string `json:"knowledge_id"`
	DecisionRef string `json:"decision_ref"`
	VerifierRef string `json:"verifier_ref"`
	Rationale   string `json:"rationale"`
	DecidedBy   string `json:"decided_by,omitempty"`
}

type supersedeInput struct {
	ID              string `json:"id"`
	KnowledgeID     string `json:"knowledge_id"`
	SupersededByRef string `json:"superseded_by_ref"`
	DecisionRef     string `json:"decision_ref"`
	VerifierRef     string `json:"verifier_ref"`
	Rationale       string `json:"rationale"`
	DecidedBy       string `json:"decided_by,omitempty"`
}

type reuseInput struct {
	ID              string `json:"id"`
	KnowledgeID     string `json:"knowledge_id"`
	AgentRunID      string `json:"agent_run_id,omitempty"`
	TraceID         string `json:"trace_id,omitempty"`
	ReuseRef        string `json:"reuse_ref"`
	Revalidated     bool   `json:"revalidated"`
	RevalidationRef string `json:"revalidation_ref,omitempty"`
	Outcome         string `json:"outcome"`
	Summary         string `json:"summary,omitempty"`
}

type knowledgeIDInput struct {
	ID          string `json:"id"`
	KnowledgeID string `json:"knowledge_id"`
}

type listInput struct {
	ID             string `json:"id"`
	Scope          string `json:"scope,omitempty"`
	State          string `json:"state,omitempty"`
	ClaimID        string `json:"claim_id,omitempty"`
	KnowledgeRef   string `json:"knowledge_ref,omitempty"`
	ConfidenceBand string `json:"confidence_band,omitempty"`
	MinConfidence  *int   `json:"min_confidence,omitempty"`
	MaxConfidence  *int   `json:"max_confidence,omitempty"`
	PageSize       int    `json:"page_size,omitempty"`
	PageToken      string `json:"page_token,omitempty"`
}

func (input listInput) toFilter() projectknowledge.KnowledgeFilter {
	return projectknowledge.KnowledgeFilter{Scope: input.Scope, State: input.State, ClaimID: input.ClaimID, KnowledgeRef: input.KnowledgeRef, ConfidenceBand: input.ConfidenceBand, MinConfidence: input.MinConfidence, MaxConfidence: input.MaxConfidence, PageSize: input.PageSize, PageToken: input.PageToken}
}

type orgListInput struct {
	OrgRef         string `json:"org_ref"`
	State          string `json:"state,omitempty"`
	ClaimID        string `json:"claim_id,omitempty"`
	KnowledgeRef   string `json:"knowledge_ref,omitempty"`
	ConfidenceBand string `json:"confidence_band,omitempty"`
	MinConfidence  *int   `json:"min_confidence,omitempty"`
	MaxConfidence  *int   `json:"max_confidence,omitempty"`
	PageSize       int    `json:"page_size,omitempty"`
	PageToken      string `json:"page_token,omitempty"`
}

func (input orgListInput) toFilter() projectknowledge.KnowledgeFilter {
	return projectknowledge.KnowledgeFilter{State: input.State, ClaimID: input.ClaimID, KnowledgeRef: input.KnowledgeRef, ConfidenceBand: input.ConfidenceBand, MinConfidence: input.MinConfidence, MaxConfidence: input.MaxConfidence, PageSize: input.PageSize, PageToken: input.PageToken}
}

func transitionSchema(ref map[string]any, text map[string]any) map[string]any {
	return objectSchema(map[string]any{"id": ref, "knowledge_id": ref, "decision_ref": ref, "verifier_ref": ref, "rationale": text}, []string{"id", "knowledge_id", "decision_ref", "verifier_ref", "rationale"})
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
}

func withProperty(properties map[string]any, name string, value any) map[string]any {
	out := make(map[string]any, len(properties)+1)
	for key, existing := range properties {
		out[key] = existing
	}
	out[name] = value
	return out
}

func toolResult(value any) map[string]any {
	encoded, _ := json.Marshal(value)
	return map[string]any{"content": []map[string]string{{"type": "text", "text": string(encoded)}}, "structuredContent": value, "isError": false}
}

func paginateKnowledge(records []projectknowledge.KnowledgeRecord, pageSize int, pageToken string) (KnowledgeList, error) {
	if pageSize == 0 {
		pageSize = defaultPageSize
	}
	if pageSize < 1 || pageSize > maxPageSize {
		return KnowledgeList{}, fmt.Errorf("%w: page_size must be between 1 and %d", projectknowledge.ErrInvalidInput, maxPageSize)
	}
	offset := 0
	pageToken = strings.TrimSpace(pageToken)
	if pageToken != "" {
		parsed, err := strconv.Atoi(pageToken)
		if err != nil || parsed < 0 {
			return KnowledgeList{}, fmt.Errorf("%w: page_token is invalid", projectknowledge.ErrInvalidInput)
		}
		offset = parsed
	}
	if offset >= len(records) {
		return KnowledgeList{Knowledge: []projectknowledge.KnowledgeRecord{}}, nil
	}
	end := offset + pageSize
	if end > len(records) {
		end = len(records)
	}
	nextToken := ""
	if end < len(records) {
		nextToken = strconv.Itoa(end)
	}
	return KnowledgeList{Knowledge: append([]projectknowledge.KnowledgeRecord(nil), records[offset:end]...), NextPageToken: nextToken}, nil
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
