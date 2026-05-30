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
			"DocumentHeading",
			"IngestionRun",
			"IngestionFinding",
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
			{Type: "DOCUMENT_HAS_HEADING", From: "Document", To: "DocumentHeading"},
			{Type: "INGESTION_RUN_TOUCHED_FILE", From: "IngestionRun", To: "RepoFile"},
			{Type: "INGESTION_RUN_SKIPPED_FILE", From: "IngestionRun", To: "RepoFile"},
		},
	}
}
