package projectingestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strconv"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

type graphWriter interface {
	PutNode(context.Context, ladybug.Node) error
	PutRelationship(context.Context, ladybug.Relationship) error
}

type GraphStore struct {
	graph graphWriter
}

func NewGraphStore(graph graphWriter) *GraphStore {
	return &GraphStore{graph: graph}
}

func (store *GraphStore) PutEligibleFile(ctx context.Context, project projectregistry.Project, run Run, state FileState, chunks []Chunk, symbols []Symbol, headings []Heading) error {
	if err := store.putProject(ctx, project); err != nil {
		return err
	}
	if err := store.putRun(ctx, run); err != nil {
		return err
	}
	repoFileID := repoFileID(project.GraphNamespace, state.RelativePathHash)
	if err := store.putRepoFile(ctx, project, repoFileID, state, true); err != nil {
		return err
	}
	if err := store.putRelationship(ctx, "PROJECT_HAS_REPO_FILE", "Project", project.ID, "RepoFile", repoFileID, project.ID); err != nil {
		return err
	}
	if err := store.putRelationship(ctx, "INGESTION_RUN_TOUCHED_FILE", "IngestionRun", run.ID, "RepoFile", repoFileID, project.ID); err != nil {
		return err
	}

	versionID := fileVersionID(repoFileID, state.ContentSHA256)
	if err := store.graph.PutNode(ctx, ladybug.Node{
		Label: "FileVersion",
		ID:    versionID,
		Properties: map[string]string{
			"id":             versionID,
			"project_id":     project.ID,
			"repo_file_id":   repoFileID,
			"content_sha256": state.ContentSHA256,
			"size_bytes":     strconv.FormatInt(state.SizeBytes, 10),
			"modified_at":    formatTime(state.ModifiedAt),
			"present":        strconv.FormatBool(state.Present),
		},
	}); err != nil {
		return err
	}
	if err := store.putRelationship(ctx, "REPO_FILE_HAS_VERSION", "RepoFile", repoFileID, "FileVersion", versionID, project.ID); err != nil {
		return err
	}

	for _, chunk := range chunks {
		chunkID := contentChunkID(versionID, chunk.Index)
		if err := store.graph.PutNode(ctx, ladybug.Node{
			Label: "ContentChunk",
			ID:    chunkID,
			Properties: map[string]string{
				"id":              chunkID,
				"project_id":      project.ID,
				"repo_file_id":    repoFileID,
				"file_version_id": versionID,
				"chunk_index":     strconv.Itoa(chunk.Index),
				"start_line":      strconv.Itoa(chunk.StartLine),
				"end_line":        strconv.Itoa(chunk.EndLine),
				"byte_start":      strconv.Itoa(chunk.ByteStart),
				"byte_end":        strconv.Itoa(chunk.ByteEnd),
				"text":            chunk.Text,
				"content_sha256":  chunk.ContentSHA256,
			},
		}); err != nil {
			return err
		}
		if err := store.putRelationship(ctx, "VERSION_HAS_CHUNK", "FileVersion", versionID, "ContentChunk", chunkID, project.ID); err != nil {
			return err
		}
	}

	for _, symbol := range symbols {
		symbolID := codeSymbolID(repoFileID, symbol)
		if err := store.graph.PutNode(ctx, ladybug.Node{
			Label: "CodeSymbol",
			ID:    symbolID,
			Properties: map[string]string{
				"id":           symbolID,
				"project_id":   project.ID,
				"repo_file_id": repoFileID,
				"kind":         string(symbol.Kind),
				"name":         symbol.Name,
				"package":      symbol.PackageName,
				"import_path":  symbol.ImportPath,
				"receiver":     symbol.Receiver,
				"start_line":   strconv.Itoa(symbol.StartLine),
				"end_line":     strconv.Itoa(symbol.EndLine),
			},
		}); err != nil {
			return err
		}
		if err := store.putRelationship(ctx, "REPO_FILE_DECLARES_SYMBOL", "RepoFile", repoFileID, "CodeSymbol", symbolID, project.ID); err != nil {
			return err
		}
		if chunkID := containingChunkID(versionID, chunks, symbol.StartLine); chunkID != "" {
			if err := store.putRelationship(ctx, "SYMBOL_IN_CHUNK", "CodeSymbol", symbolID, "ContentChunk", chunkID, project.ID); err != nil {
				return err
			}
		}
	}

	for index, heading := range headings {
		headingID := documentHeadingID(repoFileID, index, heading)
		if err := store.graph.PutNode(ctx, ladybug.Node{
			Label: "DocumentHeading",
			ID:    headingID,
			Properties: map[string]string{
				"id":           headingID,
				"project_id":   project.ID,
				"repo_file_id": repoFileID,
				"level":        strconv.Itoa(heading.Level),
				"text":         heading.Text,
				"parent_index": strconv.Itoa(heading.ParentIndex),
				"start_line":   strconv.Itoa(heading.StartLine),
				"end_line":     strconv.Itoa(heading.EndLine),
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (store *GraphStore) PutSkippedFile(ctx context.Context, project projectregistry.Project, run Run, state FileState) error {
	if err := store.putProject(ctx, project); err != nil {
		return err
	}
	if err := store.putRun(ctx, run); err != nil {
		return err
	}
	repoFileID := repoFileID(project.GraphNamespace, state.RelativePathHash)
	if err := store.putRepoFile(ctx, project, repoFileID, state, state.RelativePathSafe); err != nil {
		return err
	}
	return store.putRelationship(ctx, "INGESTION_RUN_SKIPPED_FILE", "IngestionRun", run.ID, "RepoFile", repoFileID, project.ID)
}

func (store *GraphStore) PutRun(ctx context.Context, project projectregistry.Project, run Run) error {
	if err := store.putProject(ctx, project); err != nil {
		return err
	}
	if err := store.putRun(ctx, run); err != nil {
		return err
	}
	return store.putRelationship(ctx, "PROJECT_HAS_INGESTION_RUN", "Project", project.ID, "IngestionRun", run.ID, project.ID)
}

func (store *GraphStore) putProject(ctx context.Context, project projectregistry.Project) error {
	return store.graph.PutNode(ctx, ladybug.Node{
		Label: "Project",
		ID:    project.ID,
		Properties: map[string]string{
			"id":              project.ID,
			"graph_namespace": project.GraphNamespace,
			"classification":  project.Classification,
			"digest_mode":     project.DigestMode,
			"update_policy":   project.UpdatePolicy,
			"enabled":         strconv.FormatBool(project.Enabled),
		},
	})
}

func (store *GraphStore) putRun(ctx context.Context, run Run) error {
	return store.graph.PutNode(ctx, ladybug.Node{
		Label: "IngestionRun",
		ID:    run.ID,
		Properties: map[string]string{
			"id":             run.ID,
			"project_id":     run.ProjectID,
			"trigger":        string(run.Trigger),
			"mode":           run.Mode,
			"status":         string(run.Status),
			"files_seen":     strconv.Itoa(run.FilesSeen),
			"files_ingested": strconv.Itoa(run.FilesIngested),
			"files_skipped":  strconv.Itoa(run.FilesSkipped),
			"chunks_stored":  strconv.Itoa(run.ChunksStored),
			"symbols_stored": strconv.Itoa(run.SymbolsStored),
			"error_category": run.ErrorCategory,
			"started_at":     formatTime(run.StartedAt),
			"finished_at":    formatTime(run.FinishedAt),
		},
	})
}

func (store *GraphStore) putRepoFile(ctx context.Context, project projectregistry.Project, repoFileID string, state FileState, includeRelativePath bool) error {
	props := map[string]string{
		"id":                 repoFileID,
		"project_id":         project.ID,
		"graph_namespace":    project.GraphNamespace,
		"relative_path_hash": state.RelativePathHash,
		"relative_path_safe": strconv.FormatBool(state.RelativePathSafe),
		"status":             string(state.Status),
		"present":            strconv.FormatBool(state.Present),
		"size_bytes":         strconv.FormatInt(state.SizeBytes, 10),
		"modified_at":        formatTime(state.ModifiedAt),
		"skipped_reason":     string(state.SkippedReason),
	}
	if includeRelativePath && state.RelativePathSafe {
		props["relative_path"] = state.RelativePath
		props["extension"] = path.Ext(state.RelativePath)
	}
	return store.graph.PutNode(ctx, ladybug.Node{Label: "RepoFile", ID: repoFileID, Properties: props})
}

func (store *GraphStore) putRelationship(ctx context.Context, relType string, fromLabel string, fromID string, toLabel string, toID string, projectID string) error {
	return store.graph.PutRelationship(ctx, ladybug.Relationship{
		Type: relType,
		From: ladybug.NodeRef{Label: fromLabel, ID: fromID},
		To:   ladybug.NodeRef{Label: toLabel, ID: toID},
		Properties: map[string]string{
			"project_id": projectID,
		},
	})
}

func repoFileID(graphNamespace string, relativePathHash string) string {
	return graphNamespace + ":" + relativePathHash
}

func fileVersionID(repoFileID string, contentSHA256 string) string {
	return repoFileID + ":version:" + shortHash(contentSHA256)
}

func contentChunkID(versionID string, index int) string {
	return versionID + ":chunk:" + strconv.Itoa(index)
}

func codeSymbolID(repoFileID string, symbol Symbol) string {
	return repoFileID + ":symbol:" + shortHash(string(symbol.Kind)+"\x00"+symbol.Name+"\x00"+symbol.Receiver+"\x00"+symbol.ImportPath+"\x00"+strconv.Itoa(symbol.StartLine))
}

func documentHeadingID(repoFileID string, index int, heading Heading) string {
	return repoFileID + ":heading:" + shortHash(strconv.Itoa(index)+"\x00"+heading.Text+"\x00"+strconv.Itoa(heading.StartLine))
}

func containingChunkID(versionID string, chunks []Chunk, line int) string {
	for _, chunk := range chunks {
		if line >= chunk.StartLine && line <= chunk.EndLine {
			return contentChunkID(versionID, chunk.Index)
		}
	}
	return ""
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
