package projectintegrations

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
)

func TestPlanJiraQuery_HonorsAllowlistAndBoundsPageSize(t *testing.T) {
	plan, err := PlanJiraQuery(JiraPlanInput{
		ProjectID: "example-service",
		Config: config.JiraIntegration{
			Enabled:     true,
			MaxResults:  25,
			ProjectKeys: []string{" xyz ", "ABC", "abc"},
			Polling: config.IntegrationPolling{
				InitialPageSize: 50,
			},
			DefaultFields: []string{"summary", "status"},
		},
		Kind: SyncKindInitialFull,
	})
	if err != nil {
		t.Fatalf("plan jira query: %v", err)
	}
	if plan.Kind != SyncKindInitialFull || plan.Provider != ProviderJira {
		t.Fatalf("unexpected plan kind/provider: %#v", plan)
	}
	assertStringsEqual(t, plan.ProjectKeys, []string{"ABC", "XYZ"})
	if plan.JQL != "project in (ABC, XYZ) order by updated asc, key asc" {
		t.Fatalf("unexpected jql: %s", plan.JQL)
	}
	if plan.PageSize != 25 || plan.MaxResults != 25 {
		t.Fatalf("expected page size bounded by max results, got page=%d max=%d", plan.PageSize, plan.MaxResults)
	}
	assertStringsEqual(t, plan.Fields, []string{"summary", "status"})
}

func TestPlanJiraQuery_UsesConfigDefaultsAndAppliesOverlap(t *testing.T) {
	cfg := loadIntegrationProject(t).Integrations.Jira
	if cfg == nil {
		t.Fatal("expected jira config")
	}
	watermark := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	plan, err := PlanJiraQuery(JiraPlanInput{
		ProjectID: "example-service",
		Config:    *cfg,
		State: SyncState{
			LastIncrementalSyncAt: watermark,
		},
		Kind: SyncKindIncremental,
	})
	if err != nil {
		t.Fatalf("plan jira query: %v", err)
	}
	if plan.IncrementalInterval != time.Minute {
		t.Fatalf("expected config-layer 1m default, got %s", plan.IncrementalInterval)
	}
	expectedSince := watermark.Add(-3 * time.Minute)
	if !plan.Since.Equal(expectedSince) {
		t.Fatalf("expected since %s, got %s", expectedSince, plan.Since)
	}
	if !strings.Contains(plan.JQL, `updated >= "2026-05-31 11:57"`) {
		t.Fatalf("expected overlap-adjusted updated clause, got %s", plan.JQL)
	}
	if plan.PageSize != 40 || plan.MaxResults != 40 {
		t.Fatalf("expected page size capped by max results, got page=%d max=%d", plan.PageSize, plan.MaxResults)
	}
}

func TestPlanJiraQuery_RichFieldsStayDisabledByDefault(t *testing.T) {
	plan, err := PlanJiraQuery(JiraPlanInput{
		ProjectID: "example-service",
		Config: config.JiraIntegration{
			Enabled:       true,
			ProjectKeys:   []string{"ABC"},
			AllowedFields: []string{"description", "comment", "customfield_12345"},
			Polling: config.IntegrationPolling{
				IncrementalPageSize: 10,
			},
		},
		State: SyncState{
			LastSuccessAt: time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
		},
		Kind: SyncKindIncremental,
	})
	if err != nil {
		t.Fatalf("plan jira query: %v", err)
	}
	if plan.IncludeRichFields || plan.IncludeComments {
		t.Fatalf("rich flags should be false by default: %#v", plan)
	}
	assertStringMissing(t, strings.Join(plan.Fields, ","), "description", "comment", "customfield_12345")
}

func TestPlanJiraQuery_IncludesConfiguredSafeRichFieldsOnly(t *testing.T) {
	plan, err := PlanJiraQuery(JiraPlanInput{
		ProjectID: "example-service",
		Config: config.JiraIntegration{
			Enabled:           true,
			ProjectKeys:       []string{"ABC"},
			IncludeRichFields: true,
			IncludeComments:   true,
			AllowedFields:     []string{"description", "comment", "emailAddress", "api_token", "customfield_12345"},
		},
		Kind: SyncKindInitialFull,
	})
	if err != nil {
		t.Fatalf("plan jira query: %v", err)
	}
	fields := strings.Join(plan.Fields, ",")
	assertStringContains(t, fields, "description", "comment", "customfield_12345")
	assertStringMissing(t, fields, "emailAddress", "api_token")
}

func TestPlanConfluenceQuery_HonorsAllowlistAndConfiguredFlags(t *testing.T) {
	watermark := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	plan, err := PlanConfluenceQuery(ConfluencePlanInput{
		ProjectID: "example-service",
		Config: config.ConfluenceIntegration{
			Enabled:            true,
			MaxResults:         30,
			SpaceKeys:          []string{"ENG", "Ops"},
			BodyRepresentation: "atlas_doc_format",
			IncludeBody:        true,
			CQLExtraFilter:     "status=current",
			Polling: config.IntegrationPolling{
				IncrementalPageSize: 100,
				OverlapWindow:       2 * time.Minute,
			},
		},
		State: SyncState{
			LastSuccessAt: watermark,
		},
		Kind: SyncKindIncremental,
	})
	if err != nil {
		t.Fatalf("plan confluence query: %v", err)
	}
	assertStringsEqual(t, plan.SpaceKeys, []string{"ENG", "Ops"})
	if plan.PageSize != 30 || plan.MaxResults != 30 {
		t.Fatalf("expected page size bounded by max results, got page=%d max=%d", plan.PageSize, plan.MaxResults)
	}
	assertStringContains(t, plan.CQL, `space in ("ENG", "Ops")`, `type=page`, `lastmodified >= "2026-05-31 11:58"`, `(status=current)`)
	if plan.BodyRepresentation != "atlas_doc_format" || !plan.IncludeBody || plan.IncludeComments || plan.IncludeLabels || plan.IncludeProperties {
		t.Fatalf("unexpected content flags: %#v", plan)
	}
}

func TestPlanConfluenceQuery_RichContentFlagsStayDisabledByDefault(t *testing.T) {
	cfg := loadIntegrationProject(t).Integrations.Confluence
	if cfg == nil {
		t.Fatal("expected confluence config")
	}
	plan, err := PlanConfluenceQuery(ConfluencePlanInput{
		ProjectID: "example-service",
		Config:    *cfg,
		Kind:      SyncKindInitialFull,
	})
	if err != nil {
		t.Fatalf("plan confluence query: %v", err)
	}
	if plan.IncludeBody || plan.IncludeComments || plan.IncludeLabels || plan.IncludeProperties {
		t.Fatalf("rich confluence flags should be false by default: %#v", plan)
	}
	if plan.BodyRepresentation != "storage" {
		t.Fatalf("expected config body representation default, got %q", plan.BodyRepresentation)
	}
}

func TestPlannerOutputDoesNotExposeCredentialRefsOrProviderPayloads(t *testing.T) {
	project := loadIntegrationProject(t)
	jiraPlan, err := PlanJiraQuery(JiraPlanInput{
		ProjectID: project.ID,
		Config:    *project.Integrations.Jira,
		Kind:      SyncKindInitialFull,
	})
	if err != nil {
		t.Fatalf("plan jira query: %v", err)
	}
	confluencePlan, err := PlanConfluenceQuery(ConfluencePlanInput{
		ProjectID: project.ID,
		Config:    *project.Integrations.Confluence,
		Kind:      SyncKindInitialFull,
	})
	if err != nil {
		t.Fatalf("plan confluence query: %v", err)
	}
	rendered := fmt.Sprintf("%#v\n%#v", jiraPlan, confluencePlan)
	assertStringMissing(t, rendered,
		"https://example.atlassian.net",
		"MIVIA_ATLASSIAN_EMAIL_EXAMPLE_SERVICE",
		"MIVIA_ATLASSIAN_API_TOKEN_EXAMPLE_SERVICE",
		"secrets/atlassian-email",
		"secrets/atlassian-api-token",
		"provider_payload",
	)
}

func TestPlannerRejectsMissingAllowlists(t *testing.T) {
	if _, err := PlanJiraQuery(JiraPlanInput{
		ProjectID: "example-service",
		Config:    config.JiraIntegration{Enabled: true},
	}); err != ErrInvalidInput {
		t.Fatalf("expected invalid input for missing jira project keys, got %v", err)
	}
	if _, err := PlanConfluenceQuery(ConfluencePlanInput{
		ProjectID: "example-service",
		Config:    config.ConfluenceIntegration{Enabled: true},
	}); err != ErrInvalidInput {
		t.Fatalf("expected invalid input for missing confluence space keys, got %v", err)
	}
}

func loadIntegrationProject(t *testing.T) config.Project {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-server.local.toml")
	toml := `version = 1

[[projects]]
id = "example-service"
display_name = "Example Service"
description = "Planner test fixture"
root_path = "/tmp/example-service"
enabled = true

[projects.integrations.jira]
enabled = true
site_url = "https://example.atlassian.net"
auth_mode = "api_token_basic"
project_keys = ["ABC"]
email_env = "MIVIA_ATLASSIAN_EMAIL_EXAMPLE_SERVICE"
api_token_env = "MIVIA_ATLASSIAN_API_TOKEN_EXAMPLE_SERVICE"
overlap_window = "3m"
incremental_page_size = 100
max_results = 40

[projects.integrations.confluence]
enabled = true
site_url = "https://example.atlassian.net"
auth_mode = "api_token_basic"
space_keys = ["ENG"]
email_file = "secrets/atlassian-email"
api_token_file = "secrets/atlassian-api-token"
max_results = 40
`
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	t.Setenv("MIVIA_CONFIG_PATH", path)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config fixture: %v", err)
	}
	if len(cfg.Projects) != 1 {
		t.Fatalf("expected one project, got %d", len(cfg.Projects))
	}
	return cfg.Projects[0]
}

func assertStringsEqual(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

func assertStringContains(t *testing.T, value string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			t.Fatalf("expected %q to contain %q", value, needle)
		}
	}
}

func assertStringMissing(t *testing.T, value string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			t.Fatalf("expected %q to omit %q", value, needle)
		}
	}
}
