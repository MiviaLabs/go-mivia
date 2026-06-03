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
	confidencestore "github.com/MiviaLabs/go-mivia/internal/projectconfidence/store"
)

var confidenceTools = []string{
	"projects.confidence.claims.score",
	"projects.confidence.claims.get",
	"projects.confidence.claims.list",
}

const (
	defaultPageSize = 50
	maxPageSize     = 100
)

type ClaimScoreList struct {
	Assessments   []projectconfidence.ConfidenceAssessment `json:"assessments"`
	NextPageToken string                                   `json:"next_page_token,omitempty"`
}

func ToolDefinitions() []map[string]any {
	ref := map[string]any{"type": "string", "minLength": 1, "maxLength": 200}
	return []map[string]any{
		{
			"name":        "projects.confidence.claims.score",
			"title":       "Score Claim Confidence",
			"description": "Score one project-scoped Evidence Graph claim using deterministic metadata-only confidence inputs.",
			"inputSchema": objectSchema(map[string]any{
				"id":                ref,
				"claim_id":          ref,
				"changed_paths":     map[string]any{"type": "array", "items": map[string]any{"type": "string", "maxLength": 300}, "maxItems": 100},
				"claim_check_paths": map[string]any{"type": "array", "items": map[string]any{"type": "string", "maxLength": 300}, "maxItems": 20},
				"include_verified":  map[string]any{"type": "boolean"},
			}, []string{"id", "claim_id"}),
		},
		{
			"name":        "projects.confidence.claims.get",
			"title":       "Get Claim Confidence Score",
			"description": "Fetch the stored metadata-only confidence assessment for one project-scoped claim.",
			"inputSchema": objectSchema(map[string]any{
				"id":       ref,
				"claim_id": ref,
			}, []string{"id", "claim_id"}),
		},
		{
			"name":        "projects.confidence.claims.list",
			"title":       "List Claim Confidence Scores",
			"description": "List project-scoped metadata-only confidence assessments with safe filters.",
			"inputSchema": objectSchema(map[string]any{
				"id":             ref,
				"band":           map[string]any{"type": "string", "enum": []string{projectconfidence.ScoreBandHigh, projectconfidence.ScoreBandMedium, projectconfidence.ScoreBandLow, projectconfidence.ScoreBandUnknown}},
				"min_score":      map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
				"max_score":      map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
				"recommendation": map[string]any{"type": "string", "enum": []string{projectconfidence.RecommendationPromote, projectconfidence.RecommendationVerify, projectconfidence.RecommendationReview, projectconfidence.RecommendationReject, projectconfidence.RecommendationInsufficientEvidence}},
				"run_id":         map[string]any{"type": "string", "maxLength": 200},
				"trace_id":       map[string]any{"type": "string", "maxLength": 200},
				"page_size":      map[string]any{"type": "integer", "minimum": 1, "maximum": maxPageSize},
				"page_token":     map[string]any{"type": "string", "maxLength": 20},
			}, []string{"id"}),
		},
	}
}

func IsConfidenceTool(name string) bool {
	for _, tool := range confidenceTools {
		if name == tool || name == strings.ReplaceAll(tool, ".", "_") {
			return true
		}
	}
	return false
}

func CallTool(ctx context.Context, svc *projectconfidence.Service, adapter *projectconfidence.ReliabilityInputAdapter, name string, arguments json.RawMessage) (map[string]any, error) {
	if svc == nil {
		return nil, confidencestore.ErrNotFound
	}
	switch name {
	case "projects.confidence.claims.score", "projects_confidence_claims_score":
		if adapter == nil {
			return nil, fmt.Errorf("%w: confidence input adapter is required", projectconfidence.ErrInvalidInput)
		}
		var input struct {
			ID              string   `json:"id"`
			ClaimID         string   `json:"claim_id"`
			ChangedPaths    []string `json:"changed_paths,omitempty"`
			ClaimCheckPaths []string `json:"claim_check_paths,omitempty"`
			IncludeVerified bool     `json:"include_verified,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid confidence arguments", projectconfidence.ErrInvalidInput)
		}
		assessment, err := svc.ScoreClaimWithInputs(ctx, adapter, projectconfidence.ReliabilityInputOptions{
			ProjectID:       input.ID,
			ClaimID:         input.ClaimID,
			ChangedPaths:    input.ChangedPaths,
			ClaimCheckPaths: input.ClaimCheckPaths,
			IncludeVerified: input.IncludeVerified,
		})
		return toolResult(projectconfidence.ScoreClaimResponse{Assessment: assessment}), err
	case "projects.confidence.claims.get", "projects_confidence_claims_get":
		var input claimIDInput
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid confidence arguments", projectconfidence.ErrInvalidInput)
		}
		assessment, err := svc.GetAssessment(ctx, input.ID, input.ClaimID)
		return toolResult(assessment), err
	case "projects.confidence.claims.list", "projects_confidence_claims_list":
		var input struct {
			ID             string `json:"id"`
			Band           string `json:"band,omitempty"`
			MinScore       *int   `json:"min_score,omitempty"`
			MaxScore       *int   `json:"max_score,omitempty"`
			Recommendation string `json:"recommendation,omitempty"`
			RunID          string `json:"run_id,omitempty"`
			TraceID        string `json:"trace_id,omitempty"`
			PageSize       int    `json:"page_size,omitempty"`
			PageToken      string `json:"page_token,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid confidence arguments", projectconfidence.ErrInvalidInput)
		}
		assessments, err := svc.ListAssessments(ctx, input.ID, projectconfidence.AssessmentFilter{
			Band:           input.Band,
			MinScore:       input.MinScore,
			MaxScore:       input.MaxScore,
			Recommendation: input.Recommendation,
			RunID:          input.RunID,
			TraceID:        input.TraceID,
		})
		if err != nil {
			return nil, err
		}
		list, err := paginateAssessments(assessments, input.PageSize, input.PageToken)
		return toolResult(list), err
	default:
		return nil, confidencestore.ErrNotFound
	}
}

type claimIDInput struct {
	ID      string `json:"id"`
	ClaimID string `json:"claim_id"`
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

func paginateAssessments(assessments []projectconfidence.ConfidenceAssessment, pageSize int, pageToken string) (ClaimScoreList, error) {
	if pageSize == 0 {
		pageSize = defaultPageSize
	}
	if pageSize < 1 || pageSize > maxPageSize {
		return ClaimScoreList{}, fmt.Errorf("%w: page_size must be between 1 and %d", projectconfidence.ErrInvalidInput, maxPageSize)
	}
	offset := 0
	pageToken = strings.TrimSpace(pageToken)
	if pageToken != "" {
		parsed, err := strconv.Atoi(pageToken)
		if err != nil || parsed < 0 {
			return ClaimScoreList{}, fmt.Errorf("%w: page_token is invalid", projectconfidence.ErrInvalidInput)
		}
		offset = parsed
	}
	if offset >= len(assessments) {
		return ClaimScoreList{Assessments: []projectconfidence.ConfidenceAssessment{}}, nil
	}
	end := offset + pageSize
	if end > len(assessments) {
		end = len(assessments)
	}
	nextToken := ""
	if end < len(assessments) {
		nextToken = strconv.Itoa(end)
	}
	return ClaimScoreList{Assessments: append([]projectconfidence.ConfidenceAssessment(nil), assessments[offset:end]...), NextPageToken: nextToken}, nil
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
