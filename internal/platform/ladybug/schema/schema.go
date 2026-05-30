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
			"Source",
			"Document",
			"Chunk",
			"RepoFile",
		},
		Relationships: []Relationship{
			{Type: "AGENT_RAN_TASK", From: "Agent", To: "Task"},
			{Type: "TASK_CREATED_RESEARCH_RUN", From: "Task", To: "ResearchRun"},
			{Type: "TASK_USED_SOURCE", From: "Task", To: "Source"},
			{Type: "DOCUMENT_HAS_CHUNK", From: "Document", To: "Chunk"},
			{Type: "DOCUMENT_LINKS_TO_DOCUMENT", From: "Document", To: "Document"},
			{Type: "TASK_TOUCHED_REPO_FILE", From: "Task", To: "RepoFile"},
		},
	}
}
