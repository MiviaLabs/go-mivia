package projectintegrations

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
)

const (
	defaultPlannerIncrementalInterval = time.Minute
	defaultPlannerPageSize            = 100
	defaultPlannerMaxResults          = 100
)

var defaultJiraFields = []string{"summary", "status", "updated", "issuetype", "project"}

type JiraPlanInput struct {
	ProjectID string
	Config    config.JiraIntegration
	State     SyncState
	Kind      SyncKind
}

type ConfluencePlanInput struct {
	ProjectID string
	Config    config.ConfluenceIntegration
	State     SyncState
	Kind      SyncKind
}

type JiraQueryPlan struct {
	ProjectID           string
	Provider            Provider
	Kind                SyncKind
	ProjectKeys         []string
	JQL                 string
	Fields              []string
	PageSize            int
	MaxResults          int
	IncrementalInterval time.Duration
	OverlapWindow       time.Duration
	Since               time.Time
	IncludeRichFields   bool
	IncludeComments     bool
}

type ConfluenceQueryPlan struct {
	ProjectID           string
	Provider            Provider
	Kind                SyncKind
	SpaceKeys           []string
	CQL                 string
	PageSize            int
	MaxResults          int
	IncrementalInterval time.Duration
	OverlapWindow       time.Duration
	Since               time.Time
	BodyRepresentation  string
	IncludeBody         bool
	IncludeComments     bool
	IncludeLabels       bool
	IncludeProperties   bool
}

func PlanJiraQuery(input JiraPlanInput) (JiraQueryPlan, error) {
	cfg := input.Config
	if !cfg.Enabled || strings.TrimSpace(input.ProjectID) == "" || len(cfg.ProjectKeys) == 0 {
		return JiraQueryPlan{}, ErrInvalidInput
	}
	kind, err := planKind(input.Kind, input.State)
	if err != nil {
		return JiraQueryPlan{}, err
	}
	keys := normalizeList(cfg.ProjectKeys, true)
	if len(keys) == 0 {
		return JiraQueryPlan{}, ErrInvalidInput
	}
	fields := jiraFields(cfg)
	since := sinceForKind(kind, input.State, cfg.Polling.OverlapWindow)
	jql := buildJiraJQL(keys, since, cfg.JQLExtraFilter)
	maxResults := positiveOrDefault(cfg.MaxResults, defaultPlannerMaxResults)
	pageSize := boundedPageSize(pageSizeForKind(kind, cfg.Polling), maxResults)
	return JiraQueryPlan{
		ProjectID:           strings.TrimSpace(input.ProjectID),
		Provider:            ProviderJira,
		Kind:                kind,
		ProjectKeys:         keys,
		JQL:                 jql,
		Fields:              fields,
		PageSize:            pageSize,
		MaxResults:          maxResults,
		IncrementalInterval: durationOrDefault(cfg.Polling.IncrementalInterval, defaultPlannerIncrementalInterval),
		OverlapWindow:       cfg.Polling.OverlapWindow,
		Since:               since,
		IncludeRichFields:   cfg.IncludeRichFields,
		IncludeComments:     cfg.IncludeComments,
	}, nil
}

func PlanConfluenceQuery(input ConfluencePlanInput) (ConfluenceQueryPlan, error) {
	cfg := input.Config
	if !cfg.Enabled || strings.TrimSpace(input.ProjectID) == "" || len(cfg.SpaceKeys) == 0 {
		return ConfluenceQueryPlan{}, ErrInvalidInput
	}
	kind, err := planKind(input.Kind, input.State)
	if err != nil {
		return ConfluenceQueryPlan{}, err
	}
	keys := normalizeList(cfg.SpaceKeys, false)
	if len(keys) == 0 {
		return ConfluenceQueryPlan{}, ErrInvalidInput
	}
	since := sinceForKind(kind, input.State, cfg.Polling.OverlapWindow)
	cql := buildConfluenceCQL(keys, since, cfg.CQLExtraFilter)
	maxResults := positiveOrDefault(cfg.MaxResults, defaultPlannerMaxResults)
	pageSize := boundedPageSize(pageSizeForKind(kind, cfg.Polling), maxResults)
	return ConfluenceQueryPlan{
		ProjectID:           strings.TrimSpace(input.ProjectID),
		Provider:            ProviderConfluence,
		Kind:                kind,
		SpaceKeys:           keys,
		CQL:                 cql,
		PageSize:            pageSize,
		MaxResults:          maxResults,
		IncrementalInterval: durationOrDefault(cfg.Polling.IncrementalInterval, defaultPlannerIncrementalInterval),
		OverlapWindow:       cfg.Polling.OverlapWindow,
		Since:               since,
		BodyRepresentation:  strings.TrimSpace(cfg.BodyRepresentation),
		IncludeBody:         cfg.IncludeBody,
		IncludeComments:     cfg.IncludeComments,
		IncludeLabels:       cfg.IncludeLabels,
		IncludeProperties:   cfg.IncludeProperties,
	}, nil
}

func planKind(kind SyncKind, state SyncState) (SyncKind, error) {
	switch kind {
	case "":
		if state.LastFullSyncAt.IsZero() && state.LastSuccessAt.IsZero() {
			return SyncKindInitialFull, nil
		}
		return SyncKindIncremental, nil
	case SyncKindInitialFull, SyncKindIncremental:
		return kind, nil
	default:
		return "", ErrInvalidInput
	}
}

func sinceForKind(kind SyncKind, state SyncState, overlap time.Duration) time.Time {
	if kind != SyncKindIncremental {
		return time.Time{}
	}
	watermark := state.LastIncrementalSyncAt
	if watermark.IsZero() {
		watermark = state.LastSuccessAt
	}
	if watermark.IsZero() {
		return time.Time{}
	}
	if overlap > 0 {
		watermark = watermark.Add(-overlap)
	}
	return watermark.UTC()
}

func buildJiraJQL(projectKeys []string, since time.Time, extraFilter string) string {
	parts := []string{fmt.Sprintf("project in (%s)", strings.Join(projectKeys, ", "))}
	if !since.IsZero() {
		parts = append(parts, fmt.Sprintf("updated >= %q", formatProviderTime(since)))
	}
	if extra := strings.TrimSpace(extraFilter); extra != "" {
		parts = append(parts, "("+extra+")")
	}
	return strings.Join(parts, " and ") + " order by updated asc, key asc"
}

func buildConfluenceCQL(spaceKeys []string, since time.Time, extraFilter string) string {
	quoted := make([]string, 0, len(spaceKeys))
	for _, key := range spaceKeys {
		quoted = append(quoted, quoteCQL(key))
	}
	parts := []string{fmt.Sprintf("space in (%s)", strings.Join(quoted, ", ")), "type=page"}
	if !since.IsZero() {
		parts = append(parts, fmt.Sprintf("lastmodified >= %q", formatProviderTime(since)))
	}
	if extra := strings.TrimSpace(extraFilter); extra != "" {
		parts = append(parts, "("+extra+")")
	}
	return strings.Join(parts, " and ") + " order by lastmodified asc"
}

func jiraFields(cfg config.JiraIntegration) []string {
	fields := normalizeFieldList(cfg.DefaultFields)
	if len(fields) == 0 {
		fields = append(fields, defaultJiraFields...)
	}
	if cfg.IncludeRichFields {
		fields = append(fields, normalizeFieldList(cfg.AllowedFields)...)
	}
	if cfg.IncludeComments && containsField(cfg.AllowedFields, "comment") {
		fields = append(fields, "comment")
	}
	return dedupeStrings(fields)
}

func pageSizeForKind(kind SyncKind, polling config.IntegrationPolling) int {
	if kind == SyncKindInitialFull {
		return polling.InitialPageSize
	}
	return polling.IncrementalPageSize
}

func boundedPageSize(pageSize int, maxResults int) int {
	pageSize = positiveOrDefault(pageSize, defaultPlannerPageSize)
	if maxResults > 0 && pageSize > maxResults {
		return maxResults
	}
	return pageSize
}

func positiveOrDefault(value int, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func durationOrDefault(value time.Duration, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func normalizeList(values []string, uppercase bool) []string {
	seen := make(map[string]bool)
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if uppercase {
			value = strings.ToUpper(value)
		}
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}

func normalizeFieldList(fields []string) []string {
	normalized := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || isSensitiveFieldName(field) {
			continue
		}
		normalized = append(normalized, field)
	}
	return dedupeStrings(normalized)
}

func containsField(fields []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, field := range fields {
		if strings.ToLower(strings.TrimSpace(field)) == target {
			return true
		}
	}
	return false
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]bool)
	deduped := make([]string, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, value)
	}
	return deduped
}

func isSensitiveFieldName(field string) bool {
	field = strings.ToLower(field)
	for _, marker := range []string{"credential", "password", "secret", "token", "api_key", "api-token", "email", "payload", "file_ref"} {
		if strings.Contains(field, marker) {
			return true
		}
	}
	return false
}

func formatProviderTime(value time.Time) string {
	return value.UTC().Format("2006-01-02 15:04")
}

func quoteCQL(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
