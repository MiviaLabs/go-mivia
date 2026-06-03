package schema_test

import (
	"context"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
)

func TestBootstrapSchema_Idempotent(t *testing.T) {
	graph := ladybug.NewMemoryGraph()
	bootstrap := schema.BootstrapSchema()

	if err := graph.Bootstrap(context.Background(), bootstrap); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	if err := graph.Bootstrap(context.Background(), bootstrap); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}

	snapshot := graph.SchemaSnapshot()
	if len(snapshot.NodeLabels) != len(bootstrap.NodeLabels) {
		t.Fatalf("expected %d labels, got %d", len(bootstrap.NodeLabels), len(snapshot.NodeLabels))
	}
	if len(snapshot.Relationships) != len(bootstrap.Relationships) {
		t.Fatalf("expected %d relationships, got %d", len(bootstrap.Relationships), len(snapshot.Relationships))
	}
}

func TestBootstrapSchema_ProjectDigestMetadataLabels(t *testing.T) {
	bootstrap := schema.BootstrapSchema()

	assertLabel(t, bootstrap, "Project")
	assertLabel(t, bootstrap, "DigestRun")
	assertLabel(t, bootstrap, "RepoFile")
	assertRelationship(t, bootstrap, "PROJECT_HAS_REPO_FILE", "Project", "RepoFile")
	assertRelationship(t, bootstrap, "PROJECT_HAS_DIGEST_RUN", "Project", "DigestRun")
}

func TestBootstrapSchema_IngestionLabelsAndRelationships(t *testing.T) {
	bootstrap := schema.BootstrapSchema()

	for _, label := range []string{
		"FileVersion",
		"ContentChunk",
		"CodeSymbol",
		"DocumentHeading",
		"IngestionRun",
		"IngestionFinding",
		"IntegrationArtifact",
		"IntegrationContentChunk",
	} {
		assertLabel(t, bootstrap, label)
	}

	assertRelationship(t, bootstrap, "PROJECT_HAS_INGESTION_RUN", "Project", "IngestionRun")
	assertRelationship(t, bootstrap, "REPO_FILE_HAS_VERSION", "RepoFile", "FileVersion")
	assertRelationship(t, bootstrap, "VERSION_HAS_CHUNK", "FileVersion", "ContentChunk")
	assertRelationship(t, bootstrap, "REPO_FILE_DECLARES_SYMBOL", "RepoFile", "CodeSymbol")
	assertRelationship(t, bootstrap, "SYMBOL_IN_CHUNK", "CodeSymbol", "ContentChunk")
	assertRelationship(t, bootstrap, "DOCUMENT_HAS_HEADING", "Document", "DocumentHeading")
	assertRelationship(t, bootstrap, "INGESTION_RUN_TOUCHED_FILE", "IngestionRun", "RepoFile")
	assertRelationship(t, bootstrap, "INGESTION_RUN_SKIPPED_FILE", "IngestionRun", "RepoFile")
	assertRelationship(t, bootstrap, "PROJECT_HAS_INTEGRATION_ARTIFACT", "Project", "IntegrationArtifact")
	assertRelationship(t, bootstrap, "INTEGRATION_ARTIFACT_HAS_CHUNK", "IntegrationArtifact", "IntegrationContentChunk")
}

func TestBootstrapSchema_EvidenceLabelsAndRelationships(t *testing.T) {
	bootstrap := schema.BootstrapSchema()

	for _, label := range []string{
		"Claim",
		"Evidence",
		"Decision",
		"Action",
		"Outcome",
		"Artifact",
		"Promotion",
	} {
		assertLabel(t, bootstrap, label)
	}

	for _, forbidden := range []string{
		"EvidenceClaim",
		"EvidenceDecision",
		"EvidenceAction",
		"EvidenceOutcome",
		"EvidenceArtifact",
		"EvidencePromotion",
	} {
		assertNoLabel(t, bootstrap, forbidden)
	}

	assertRelationship(t, bootstrap, "PROJECT_HAS_CLAIM", "Project", "Claim")
	assertRelationship(t, bootstrap, "AGENT_RUN_MADE_CLAIM", "AgentRun", "Claim")
	assertRelationship(t, bootstrap, "CLAIM_HAS_EVIDENCE", "Claim", "Evidence")
	assertRelationship(t, bootstrap, "EVIDENCE_SUPPORTS_DECISION", "Evidence", "Decision")
	assertRelationship(t, bootstrap, "CLAIM_HAS_DECISION", "Claim", "Decision")
	assertRelationship(t, bootstrap, "DECISION_PRODUCED_ACTION", "Decision", "Action")
	assertRelationship(t, bootstrap, "ACTION_PRODUCED_OUTCOME", "Action", "Outcome")
	assertRelationship(t, bootstrap, "ACTION_PRODUCED_ARTIFACT", "Action", "Artifact")
	assertRelationship(t, bootstrap, "ARTIFACT_HAS_PROMOTION", "Artifact", "Promotion")
	assertRelationship(t, bootstrap, "PROMOTION_DECIDES_CLAIM", "Promotion", "Claim")
	assertRelationship(t, bootstrap, "OUTCOME_SUPPORTS_PROMOTION", "Outcome", "Promotion")
}

func TestBootstrapSchema_RelationshipEndpointsAreDeclaredLabels(t *testing.T) {
	bootstrap := schema.BootstrapSchema()
	labels := make(map[string]struct{}, len(bootstrap.NodeLabels))
	for _, label := range bootstrap.NodeLabels {
		labels[label] = struct{}{}
	}
	for _, rel := range bootstrap.Relationships {
		if _, ok := labels[rel.From]; !ok {
			t.Fatalf("relationship %q has undeclared from label %q", rel.Type, rel.From)
		}
		if _, ok := labels[rel.To]; !ok {
			t.Fatalf("relationship %q has undeclared to label %q", rel.Type, rel.To)
		}
	}
}

func assertLabel(t *testing.T, bootstrap schema.GraphSchema, expected string) {
	t.Helper()
	for _, label := range bootstrap.NodeLabels {
		if label == expected {
			return
		}
	}
	t.Fatalf("expected label %q in %#v", expected, bootstrap.NodeLabels)
}

func assertNoLabel(t *testing.T, bootstrap schema.GraphSchema, forbidden string) {
	t.Helper()
	for _, label := range bootstrap.NodeLabels {
		if label == forbidden {
			t.Fatalf("did not expect label %q in %#v", forbidden, bootstrap.NodeLabels)
		}
	}
}

func assertRelationship(t *testing.T, bootstrap schema.GraphSchema, expectedType string, expectedFrom string, expectedTo string) {
	t.Helper()
	for _, rel := range bootstrap.Relationships {
		if rel.Type == expectedType && rel.From == expectedFrom && rel.To == expectedTo {
			return
		}
	}
	t.Fatalf("expected relationship %q from %q to %q in %#v", expectedType, expectedFrom, expectedTo, bootstrap.Relationships)
}
