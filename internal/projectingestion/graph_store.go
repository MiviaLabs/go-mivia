package projectingestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

type graphBackend interface {
	PutNode(context.Context, ladybug.Node) error
	GetNode(context.Context, string, string) (ladybug.Node, error)
	ListNodes(context.Context, string, map[string]string) ([]ladybug.Node, error)
	DeleteNodes(context.Context, string, map[string]string) error
	PutRelationship(context.Context, ladybug.Relationship) error
}

type GraphStore struct {
	graph graphBackend
}

func NewGraphStore(graph graphBackend) *GraphStore {
	return &GraphStore{graph: graph}
}

func (store *GraphStore) PutEligibleFile(ctx context.Context, project projectregistry.Project, run Run, state FileState, chunks []Chunk, symbols []Symbol, headings []Heading) error {
	return store.withBatch(ctx, func(store *GraphStore) error {
		return store.putEligibleFile(ctx, project, run, state, chunks, symbols, headings)
	})
}

func (store *GraphStore) putEligibleFile(ctx context.Context, project projectregistry.Project, run Run, state FileState, chunks []Chunk, symbols []Symbol, headings []Heading) error {
	if err := store.putProject(ctx, project); err != nil {
		return err
	}
	if err := store.putRun(ctx, run); err != nil {
		return err
	}
	repoFileID := repoFileID(project.GraphNamespace, state.RelativePathHash)
	if err := store.deleteDerivedFileNodes(ctx, project.ID, repoFileID); err != nil {
		return err
	}
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
	return store.withBatch(ctx, func(store *GraphStore) error {
		return store.putSkippedFile(ctx, project, run, state)
	})
}

func (store *GraphStore) putSkippedFile(ctx context.Context, project projectregistry.Project, run Run, state FileState) error {
	if err := store.putProject(ctx, project); err != nil {
		return err
	}
	if err := store.putRun(ctx, run); err != nil {
		return err
	}
	repoFileID := repoFileID(project.GraphNamespace, state.RelativePathHash)
	if err := store.deleteDerivedFileNodes(ctx, project.ID, repoFileID); err != nil {
		return err
	}
	if err := store.putRepoFile(ctx, project, repoFileID, state, state.RelativePathSafe); err != nil {
		return err
	}
	return store.putRelationship(ctx, "INGESTION_RUN_SKIPPED_FILE", "IngestionRun", run.ID, "RepoFile", repoFileID, project.ID)
}

func (store *GraphStore) PutFileState(ctx context.Context, project projectregistry.Project, run Run, state FileState) error {
	return store.withBatch(ctx, func(store *GraphStore) error {
		return store.putFileState(ctx, project, run, state)
	})
}

func (store *GraphStore) putFileState(ctx context.Context, project projectregistry.Project, run Run, state FileState) error {
	if err := store.putProject(ctx, project); err != nil {
		return err
	}
	if err := store.putRun(ctx, run); err != nil {
		return err
	}
	repoFileID := repoFileID(project.GraphNamespace, state.RelativePathHash)
	if err := store.deleteDerivedFileNodes(ctx, project.ID, repoFileID); err != nil {
		return err
	}
	if err := store.putRepoFile(ctx, project, repoFileID, state, state.RelativePathSafe); err != nil {
		return err
	}
	if err := store.putRelationship(ctx, "PROJECT_HAS_REPO_FILE", "Project", project.ID, "RepoFile", repoFileID, project.ID); err != nil {
		return err
	}
	return store.putRelationship(ctx, "INGESTION_RUN_TOUCHED_FILE", "IngestionRun", run.ID, "RepoFile", repoFileID, project.ID)
}

func (store *GraphStore) PutRun(ctx context.Context, project projectregistry.Project, run Run) error {
	return store.withBatch(ctx, func(store *GraphStore) error {
		return store.putRunWithProject(ctx, project, run)
	})
}

func (store *GraphStore) putRunWithProject(ctx context.Context, project projectregistry.Project, run Run) error {
	if err := store.putProject(ctx, project); err != nil {
		return err
	}
	if err := store.putRun(ctx, run); err != nil {
		return err
	}
	return store.putRelationship(ctx, "PROJECT_HAS_INGESTION_RUN", "Project", project.ID, "IngestionRun", run.ID, project.ID)
}

func (store *GraphStore) WithBatch(ctx context.Context, fn func(*GraphStore) error) error {
	return store.withBatch(ctx, fn)
}

func (store *GraphStore) withBatch(ctx context.Context, fn func(*GraphStore) error) error {
	if fn == nil {
		return nil
	}
	if store == nil || store.graph == nil {
		return fn(store)
	}
	batcher, ok := store.graph.(ladybug.BatchGraph)
	if !ok {
		return fn(store)
	}
	return batcher.Batch(ctx, func(graph ladybug.Graph) error {
		return fn(&GraphStore{graph: graph})
	})
}

func (store *GraphStore) GetFile(ctx context.Context, project projectregistry.Project, fileID string) (FileMetadata, error) {
	if !validOpaqueID(fileID) {
		return FileMetadata{}, ErrInvalidInput
	}
	node, err := store.graph.GetNode(ctx, "RepoFile", fileID)
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return FileMetadata{}, ErrIngestionNotFound
	}
	if err != nil {
		return FileMetadata{}, err
	}
	if node.Properties["project_id"] != project.ID {
		return FileMetadata{}, ErrIngestionNotFound
	}
	return fileMetadataFromNode(node)
}

func (store *GraphStore) ListChunks(ctx context.Context, project projectregistry.Project, fileID string, pagination Pagination, maxChunkBytes int) (ChunkList, error) {
	if _, err := store.GetFile(ctx, project, fileID); err != nil {
		return ChunkList{}, err
	}
	nodes, err := store.graph.ListNodes(ctx, "ContentChunk", map[string]string{
		"project_id":   project.ID,
		"repo_file_id": fileID,
	})
	if err != nil {
		return ChunkList{}, err
	}
	sort.Slice(nodes, func(i, j int) bool {
		left, _ := strconv.Atoi(nodes[i].Properties["chunk_index"])
		right, _ := strconv.Atoi(nodes[j].Properties["chunk_index"])
		if left == right {
			return nodes[i].ID < nodes[j].ID
		}
		return left < right
	})
	window, nextToken, err := paginate(nodes, pagination)
	if err != nil {
		return ChunkList{}, err
	}
	chunks := make([]ChunkMetadata, 0, len(window))
	for _, node := range window {
		chunk, err := chunkMetadataFromNode(node, maxChunkBytes)
		if err != nil {
			return ChunkList{}, err
		}
		chunks = append(chunks, chunk)
	}
	return ChunkList{Chunks: chunks, NextPageToken: nextToken}, nil
}

func (store *GraphStore) GetChunk(ctx context.Context, project projectregistry.Project, fileID string, chunkID string, maxChunkBytes int) (ChunkMetadata, error) {
	if !validOpaqueID(fileID) || !validOpaqueID(chunkID) {
		return ChunkMetadata{}, ErrInvalidInput
	}
	node, err := store.graph.GetNode(ctx, "ContentChunk", chunkID)
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return ChunkMetadata{}, ErrIngestionNotFound
	}
	if err != nil {
		return ChunkMetadata{}, err
	}
	if node.Properties["project_id"] != project.ID || node.Properties["repo_file_id"] != fileID {
		return ChunkMetadata{}, ErrIngestionNotFound
	}
	return chunkMetadataFromNode(node, maxChunkBytes)
}

func (store *GraphStore) ListSymbols(ctx context.Context, project projectregistry.Project, filter SymbolFilter, pagination Pagination) (SymbolList, error) {
	nodeFilter := map[string]string{"project_id": project.ID}
	if filter.Kind != "" {
		nodeFilter["kind"] = string(filter.Kind)
	}
	if filter.FileID != "" {
		if !validOpaqueID(filter.FileID) {
			return SymbolList{}, ErrInvalidInput
		}
		nodeFilter["repo_file_id"] = filter.FileID
	}
	if filter.Package != "" {
		nodeFilter["package"] = filter.Package
	}
	nodes, err := store.graph.ListNodes(ctx, "CodeSymbol", nodeFilter)
	if err != nil {
		return SymbolList{}, err
	}
	if filter.NamePrefix != "" || filter.Extension != "" {
		nodes, err = store.filterSymbolNodes(ctx, project, nodes, filter)
		if err != nil {
			return SymbolList{}, err
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Properties["name"] == nodes[j].Properties["name"] {
			return nodes[i].ID < nodes[j].ID
		}
		return nodes[i].Properties["name"] < nodes[j].Properties["name"]
	})
	window, nextToken, err := paginate(nodes, pagination)
	if err != nil {
		return SymbolList{}, err
	}
	symbols := make([]SymbolMetadata, 0, len(window))
	for _, node := range window {
		symbol, err := symbolMetadataFromNode(node)
		if err != nil {
			return SymbolList{}, err
		}
		symbols = append(symbols, symbol)
	}
	return SymbolList{Symbols: symbols, NextPageToken: nextToken}, nil
}

func (store *GraphStore) filterSymbolNodes(ctx context.Context, project projectregistry.Project, nodes []ladybug.Node, filter SymbolFilter) ([]ladybug.Node, error) {
	out := make([]ladybug.Node, 0, len(nodes))
	fileExtension := make(map[string]string)
	for _, node := range nodes {
		if filter.NamePrefix != "" && !strings.HasPrefix(node.Properties["name"], filter.NamePrefix) {
			continue
		}
		if filter.Extension != "" {
			fileID := node.Properties["repo_file_id"]
			extension, ok := fileExtension[fileID]
			if !ok {
				file, err := store.GetFile(ctx, project, fileID)
				if errors.Is(err, ErrIngestionNotFound) {
					continue
				}
				if err != nil {
					return nil, err
				}
				extension = strings.ToLower(file.Extension)
				fileExtension[fileID] = extension
			}
			if extension != filter.Extension {
				continue
			}
		}
		out = append(out, node)
	}
	return out, nil
}

func (store *GraphStore) ListHeadings(ctx context.Context, project projectregistry.Project, fileID string, pagination Pagination) (HeadingList, error) {
	filter := map[string]string{"project_id": project.ID}
	if strings.TrimSpace(fileID) != "" {
		if !validOpaqueID(fileID) {
			return HeadingList{}, ErrInvalidInput
		}
		filter["repo_file_id"] = fileID
	}
	nodes, err := store.graph.ListNodes(ctx, "DocumentHeading", filter)
	if err != nil {
		return HeadingList{}, err
	}
	sort.Slice(nodes, func(i, j int) bool {
		leftFile := nodes[i].Properties["repo_file_id"]
		rightFile := nodes[j].Properties["repo_file_id"]
		if leftFile != rightFile {
			return leftFile < rightFile
		}
		leftLine, _ := strconv.Atoi(nodes[i].Properties["start_line"])
		rightLine, _ := strconv.Atoi(nodes[j].Properties["start_line"])
		if leftLine == rightLine {
			return nodes[i].ID < nodes[j].ID
		}
		return leftLine < rightLine
	})
	window, nextToken, err := paginate(nodes, pagination)
	if err != nil {
		return HeadingList{}, err
	}
	headings := make([]HeadingMetadata, 0, len(window))
	for _, node := range window {
		heading, err := headingMetadataFromNode(node)
		if err != nil {
			return HeadingList{}, err
		}
		headings = append(headings, heading)
	}
	return HeadingList{Headings: headings, NextPageToken: nextToken}, nil
}

func (store *GraphStore) GetFileOutline(ctx context.Context, project projectregistry.Project, fileID string, options FileOutlineOptions) (FileOutline, error) {
	file, err := store.GetFile(ctx, project, fileID)
	if err != nil {
		return FileOutline{}, err
	}
	headings, err := store.ListHeadings(ctx, project, fileID, Pagination{PageSize: MaxPageSize})
	if err != nil {
		return FileOutline{}, err
	}
	symbolFilter := options.SymbolFilter
	symbolFilter.FileID = fileID
	symbolPagination := options.SymbolPagination
	if symbolPagination.PageSize == 0 && strings.TrimSpace(symbolPagination.PageToken) == "" {
		symbolPagination.PageSize = MaxPageSize
	}
	symbols, err := store.ListSymbols(ctx, project, symbolFilter, symbolPagination)
	if err != nil {
		return FileOutline{}, err
	}
	chunks, err := store.listOutlineChunks(ctx, project, fileID, Pagination{PageSize: MaxPageSize})
	if err != nil {
		return FileOutline{}, err
	}
	return FileOutline{
		File:                 file,
		Headings:             headings.Headings,
		Symbols:              symbols.Symbols,
		SymbolsNextPageToken: symbols.NextPageToken,
		Chunks:               chunks,
	}, nil
}

func (store *GraphStore) listOutlineChunks(ctx context.Context, project projectregistry.Project, fileID string, pagination Pagination) ([]OutlineChunkMetadata, error) {
	nodes, err := store.graph.ListNodes(ctx, "ContentChunk", map[string]string{
		"project_id":   project.ID,
		"repo_file_id": fileID,
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(nodes, func(i, j int) bool {
		left, _ := strconv.Atoi(nodes[i].Properties["chunk_index"])
		right, _ := strconv.Atoi(nodes[j].Properties["chunk_index"])
		if left == right {
			return nodes[i].ID < nodes[j].ID
		}
		return left < right
	})
	window, _, err := paginate(nodes, pagination)
	if err != nil {
		return nil, err
	}
	chunks := make([]OutlineChunkMetadata, 0, len(window))
	for _, node := range window {
		chunk, err := outlineChunkMetadataFromNode(node)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

func (store *GraphStore) GetSymbol(ctx context.Context, project projectregistry.Project, symbolID string) (SymbolMetadata, error) {
	if !validOpaqueID(symbolID) {
		return SymbolMetadata{}, ErrInvalidInput
	}
	node, err := store.graph.GetNode(ctx, "CodeSymbol", symbolID)
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return SymbolMetadata{}, ErrIngestionNotFound
	}
	if err != nil {
		return SymbolMetadata{}, err
	}
	if node.Properties["project_id"] != project.ID {
		return SymbolMetadata{}, ErrIngestionNotFound
	}
	return symbolMetadataFromNode(node)
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
		props["extension"] = strings.ToLower(path.Ext(state.RelativePath))
	}
	return store.graph.PutNode(ctx, ladybug.Node{Label: "RepoFile", ID: repoFileID, Properties: props})
}

func (store *GraphStore) deleteDerivedFileNodes(ctx context.Context, projectID string, repoFileID string) error {
	filter := map[string]string{"project_id": projectID, "repo_file_id": repoFileID}
	for _, label := range []string{"CodeSymbol", "DocumentHeading", "ContentChunk", "FileVersion"} {
		if err := store.graph.DeleteNodes(ctx, label, filter); err != nil {
			return err
		}
	}
	return nil
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

func fileMetadataFromNode(node ladybug.Node) (FileMetadata, error) {
	size, err := strconv.ParseInt(node.Properties["size_bytes"], 10, 64)
	if err != nil && node.Properties["size_bytes"] != "" {
		return FileMetadata{}, err
	}
	modifiedAt, err := parseOptionalTime(node.Properties["modified_at"])
	if err != nil {
		return FileMetadata{}, err
	}
	present, _ := strconv.ParseBool(node.Properties["present"])
	relativePathSafe, _ := strconv.ParseBool(node.Properties["relative_path_safe"])
	metadata := FileMetadata{
		ID:             node.ID,
		ProjectID:      node.Properties["project_id"],
		Status:         node.Properties["status"],
		Present:        present,
		SizeBytes:      size,
		ModifiedAt:     modifiedAt,
		SkippedReason:  node.Properties["skipped_reason"],
		RelativePathOK: relativePathSafe,
	}
	if relativePathSafe {
		metadata.RelativePath = node.Properties["relative_path"]
		metadata.Extension = node.Properties["extension"]
	}
	return metadata, nil
}

func chunkMetadataFromNode(node ladybug.Node, maxChunkBytes int) (ChunkMetadata, error) {
	index, err := strconv.Atoi(node.Properties["chunk_index"])
	if err != nil {
		return ChunkMetadata{}, err
	}
	startLine, err := strconv.Atoi(node.Properties["start_line"])
	if err != nil {
		return ChunkMetadata{}, err
	}
	endLine, err := strconv.Atoi(node.Properties["end_line"])
	if err != nil {
		return ChunkMetadata{}, err
	}
	byteStart, err := strconv.Atoi(node.Properties["byte_start"])
	if err != nil {
		return ChunkMetadata{}, err
	}
	byteEnd, err := strconv.Atoi(node.Properties["byte_end"])
	if err != nil {
		return ChunkMetadata{}, err
	}
	text, truncated := truncateUTF8Bytes(node.Properties["text"], maxChunkBytes)
	return ChunkMetadata{
		ID:            node.ID,
		FileID:        node.Properties["repo_file_id"],
		ProjectID:     node.Properties["project_id"],
		Index:         index,
		StartLine:     startLine,
		EndLine:       endLine,
		ByteStart:     byteStart,
		ByteEnd:       byteEnd,
		Text:          text,
		TextTruncated: truncated,
	}, nil
}

func outlineChunkMetadataFromNode(node ladybug.Node) (OutlineChunkMetadata, error) {
	chunk, err := chunkMetadataFromNode(node, 0)
	if err != nil {
		return OutlineChunkMetadata{}, err
	}
	return OutlineChunkMetadata{
		ID:        chunk.ID,
		FileID:    chunk.FileID,
		ProjectID: chunk.ProjectID,
		Index:     chunk.Index,
		StartLine: chunk.StartLine,
		EndLine:   chunk.EndLine,
		ByteStart: chunk.ByteStart,
		ByteEnd:   chunk.ByteEnd,
	}, nil
}

func symbolMetadataFromNode(node ladybug.Node) (SymbolMetadata, error) {
	startLine, err := strconv.Atoi(node.Properties["start_line"])
	if err != nil {
		return SymbolMetadata{}, err
	}
	endLine, err := strconv.Atoi(node.Properties["end_line"])
	if err != nil {
		return SymbolMetadata{}, err
	}
	return SymbolMetadata{
		ID:          node.ID,
		FileID:      node.Properties["repo_file_id"],
		ProjectID:   node.Properties["project_id"],
		Kind:        node.Properties["kind"],
		Name:        node.Properties["name"],
		PackageName: node.Properties["package"],
		ImportPath:  node.Properties["import_path"],
		Receiver:    node.Properties["receiver"],
		StartLine:   startLine,
		EndLine:     endLine,
	}, nil
}

func headingMetadataFromNode(node ladybug.Node) (HeadingMetadata, error) {
	level, err := strconv.Atoi(node.Properties["level"])
	if err != nil {
		return HeadingMetadata{}, err
	}
	parentIndex, err := strconv.Atoi(node.Properties["parent_index"])
	if err != nil {
		return HeadingMetadata{}, err
	}
	startLine, err := strconv.Atoi(node.Properties["start_line"])
	if err != nil {
		return HeadingMetadata{}, err
	}
	endLine, err := strconv.Atoi(node.Properties["end_line"])
	if err != nil {
		return HeadingMetadata{}, err
	}
	return HeadingMetadata{
		ID:          node.ID,
		FileID:      node.Properties["repo_file_id"],
		ProjectID:   node.Properties["project_id"],
		Level:       level,
		Text:        node.Properties["text"],
		ParentIndex: parentIndex,
		StartLine:   startLine,
		EndLine:     endLine,
	}, nil
}
