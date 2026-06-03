package schema

type GraphSchema struct {
	NodeLabels    []string
	Relationships []Relationship
}

type Relationship struct {
	Type  string
	From  string
	To    string
	Notes string
}

func BootstrapSchema() GraphSchema {
	return GraphSchema{
		NodeLabels: []string{
			"Agent",
			"AgentRun",
			"Task",
			"ResearchRun",
			"Project",
			"DigestRun",
			"Source",
			"Document",
			"Chunk",
			"RepoFile",
			"FileVersion",
			"ContentChunk",
			"CodeSymbol",
			"CodeReference",
			"CodeCall",
			"CodeImplementation",
			"DocumentHeading",
			"IngestionRun",
			"IngestionFinding",
			"IntegrationArtifact",
			"IntegrationContentChunk",
			"Claim",
			"Evidence",
			"Decision",
			"Action",
			"Outcome",
			"Artifact",
			"Promotion",
			"ConfidenceAssessment",
			"ConfidenceFactor",
		},
		Relationships: []Relationship{
			{Type: "AGENT_RAN_TASK", From: "Agent", To: "Task"},
			{Type: "TASK_CREATED_RESEARCH_RUN", From: "Task", To: "ResearchRun"},
			{Type: "TASK_USED_SOURCE", From: "Task", To: "Source"},
			{Type: "DOCUMENT_HAS_CHUNK", From: "Document", To: "Chunk"},
			{Type: "DOCUMENT_LINKS_TO_DOCUMENT", From: "Document", To: "Document"},
			{Type: "TASK_TOUCHED_REPO_FILE", From: "Task", To: "RepoFile"},
			{Type: "PROJECT_HAS_REPO_FILE", From: "Project", To: "RepoFile"},
			{Type: "PROJECT_HAS_DIGEST_RUN", From: "Project", To: "DigestRun"},
			{Type: "PROJECT_HAS_INGESTION_RUN", From: "Project", To: "IngestionRun"},
			{Type: "REPO_FILE_HAS_VERSION", From: "RepoFile", To: "FileVersion"},
			{Type: "VERSION_HAS_CHUNK", From: "FileVersion", To: "ContentChunk"},
			{Type: "REPO_FILE_DECLARES_SYMBOL", From: "RepoFile", To: "CodeSymbol"},
			{Type: "SYMBOL_IN_CHUNK", From: "CodeSymbol", To: "ContentChunk"},
			{Type: "SYMBOL_HAS_REFERENCE", From: "CodeSymbol", To: "CodeReference"},
			{Type: "REFERENCE_IN_CHUNK", From: "CodeReference", To: "ContentChunk"},
			{Type: "SYMBOL_REFERENCES_SYMBOL", From: "CodeSymbol", To: "CodeSymbol"},
			{Type: "SYMBOL_CALLS_SYMBOL", From: "CodeSymbol", To: "CodeSymbol"},
			{Type: "CALL_IN_CHUNK", From: "CodeCall", To: "ContentChunk"},
			{Type: "SYMBOL_IMPLEMENTS_SYMBOL", From: "CodeSymbol", To: "CodeSymbol"},
			{Type: "IMPLEMENTATION_IN_CHUNK", From: "CodeImplementation", To: "ContentChunk"},
			{Type: "DOCUMENT_HAS_HEADING", From: "Document", To: "DocumentHeading"},
			{Type: "INGESTION_RUN_TOUCHED_FILE", From: "IngestionRun", To: "RepoFile"},
			{Type: "INGESTION_RUN_SKIPPED_FILE", From: "IngestionRun", To: "RepoFile"},
			{Type: "PROJECT_HAS_INTEGRATION_ARTIFACT", From: "Project", To: "IntegrationArtifact"},
			{Type: "INTEGRATION_ARTIFACT_HAS_CHUNK", From: "IntegrationArtifact", To: "IntegrationContentChunk"},
			{Type: "PROJECT_HAS_CLAIM", From: "Project", To: "Claim"},
			{Type: "AGENT_RUN_MADE_CLAIM", From: "AgentRun", To: "Claim"},
			{Type: "CLAIM_HAS_EVIDENCE", From: "Claim", To: "Evidence"},
			{Type: "EVIDENCE_SUPPORTS_DECISION", From: "Evidence", To: "Decision"},
			{Type: "CLAIM_HAS_DECISION", From: "Claim", To: "Decision"},
			{Type: "DECISION_PRODUCED_ACTION", From: "Decision", To: "Action"},
			{Type: "ACTION_PRODUCED_OUTCOME", From: "Action", To: "Outcome"},
			{Type: "ACTION_PRODUCED_ARTIFACT", From: "Action", To: "Artifact"},
			{Type: "ARTIFACT_HAS_PROMOTION", From: "Artifact", To: "Promotion"},
			{Type: "PROMOTION_DECIDES_CLAIM", From: "Promotion", To: "Claim"},
			{Type: "OUTCOME_SUPPORTS_PROMOTION", From: "Outcome", To: "Promotion"},
			{Type: "CLAIM_HAS_CONFIDENCE", From: "Claim", To: "ConfidenceAssessment"},
			{Type: "CONFIDENCE_HAS_FACTOR", From: "ConfidenceAssessment", To: "ConfidenceFactor"},
			{Type: "CONFIDENCE_USED_EVIDENCE", From: "ConfidenceAssessment", To: "Evidence"},
			{Type: "CONFIDENCE_USED_DECISION", From: "ConfidenceAssessment", To: "Decision"},
			{Type: "CONFIDENCE_USED_OUTCOME", From: "ConfidenceAssessment", To: "Outcome"},
		},
	}
}
