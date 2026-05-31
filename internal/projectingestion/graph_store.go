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
	"unicode/utf8"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

type graphBackend interface {
	PutNode(context.Context, ladybug.Node) error
	GetNode(context.Context, string, string) (ladybug.Node, error)
	ListNodes(context.Context, string, map[string]string) ([]ladybug.Node, error)
	DeleteNodes(context.Context, string, map[string]string) error
	PutRelationship(context.Context, ladybug.Relationship) error
	ListRelationships(context.Context, string, ladybug.RelationshipFilter) ([]ladybug.Relationship, error)
}

type GraphStore struct {
	graph graphBackend
}

func NewGraphStore(graph graphBackend) *GraphStore {
	return &GraphStore{graph: graph}
}

func (store *GraphStore) PutEligibleFile(ctx context.Context, project projectregistry.Project, run Run, state FileState, chunks []Chunk, symbols []Symbol, references []Reference, calls []Call, headings []Heading) error {
	return store.withBatch(ctx, func(store *GraphStore) error {
		return store.putEligibleFile(ctx, project, run, state, chunks, symbols, references, calls, headings)
	})
}

func (store *GraphStore) HasFileVersion(ctx context.Context, project projectregistry.Project, state FileState) (bool, error) {
	if store == nil || store.graph == nil || state.ContentSHA256 == "" {
		return false, nil
	}
	repoFileID := repoFileID(project.GraphNamespace, state.RelativePathHash)
	versionID := fileVersionID(repoFileID, state.ContentSHA256)
	node, err := store.graph.GetNode(ctx, "FileVersion", versionID)
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if node.Properties["project_id"] != project.ID || node.Properties["repo_file_id"] != repoFileID {
		return false, nil
	}
	if state.SizeBytes == 0 {
		return true, nil
	}
	chunks, err := store.graph.ListNodes(ctx, "ContentChunk", map[string]string{
		"project_id":      project.ID,
		"repo_file_id":    repoFileID,
		"file_version_id": versionID,
	})
	if err != nil {
		return false, err
	}
	return len(chunks) > 0, nil
}

func (store *GraphStore) PutUnchangedFile(ctx context.Context, project projectregistry.Project, run Run, state FileState) error {
	return store.withBatch(ctx, func(store *GraphStore) error {
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
		return store.graph.PutNode(ctx, ladybug.Node{
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
		})
	})
}

func (store *GraphStore) putEligibleFile(ctx context.Context, project projectregistry.Project, run Run, state FileState, chunks []Chunk, symbols []Symbol, references []Reference, calls []Call, headings []Heading) error {
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
				"start_byte":   strconv.Itoa(symbol.StartByte),
				"end_byte":     strconv.Itoa(symbol.EndByte),
				"start_column": strconv.Itoa(symbol.StartColumn),
				"end_column":   strconv.Itoa(symbol.EndColumn),
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

	symbolIDs := symbolIDIndex(repoFileID, symbols)
	if err := store.putReferences(ctx, project.ID, repoFileID, versionID, chunks, references, symbolIDs); err != nil {
		return err
	}
	if err := store.putCalls(ctx, project.ID, repoFileID, versionID, chunks, calls, symbolIDs); err != nil {
		return err
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
	if filter.NamePrefix != "" || filter.NameContains != "" || filter.Extension != "" || filter.Receiver != "" {
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
		name := node.Properties["name"]
		if filter.NamePrefix != "" && !strings.HasPrefix(name, filter.NamePrefix) {
			continue
		}
		if filter.NameContains != "" && !containsWithCaseOption(name, filter.NameContains, filter.CaseSensitive) {
			continue
		}
		if filter.Receiver != "" && node.Properties["receiver"] != filter.Receiver {
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

func (store *GraphStore) SearchText(ctx context.Context, project projectregistry.Project, options TextSearchOptions) (TextSearchResultList, error) {
	nodes, err := store.graph.ListNodes(ctx, "ContentChunk", map[string]string{"project_id": project.ID})
	if err != nil {
		return TextSearchResultList{}, err
	}
	fileCache := map[string]FileMetadata{}
	results := make([]TextSearchResult, 0)
	for _, node := range nodes {
		file, ok, err := store.searchFile(ctx, project, node.Properties["repo_file_id"], options.Extension, options.PathPrefix, fileCache)
		if err != nil {
			return TextSearchResultList{}, err
		}
		if !ok {
			continue
		}
		chunk, err := chunkMetadataWithoutText(node)
		if err != nil {
			return TextSearchResultList{}, err
		}
		text := node.Properties["text"]
		indexes := literalMatchIndexes(text, options.Query, options.CaseSensitive)
		for _, index := range indexes {
			end := index + len(options.Query)
			lineStart := chunk.StartLine + strings.Count(text[:index], "\n")
			lineEnd := lineStart + strings.Count(text[index:end], "\n")
			snippet, truncated := boundedSnippet(text, index, end, options.MaxSnippetBytes)
			results = append(results, TextSearchResult{
				File:             file,
				Chunk:            chunk,
				LineStart:        lineStart,
				LineEnd:          lineEnd,
				ByteStart:        chunk.ByteStart + index,
				ByteEnd:          chunk.ByteStart + end,
				Snippet:          snippet,
				SnippetTruncated: truncated,
			})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		left := results[i]
		right := results[j]
		if left.File.RelativePath != right.File.RelativePath {
			return left.File.RelativePath < right.File.RelativePath
		}
		if left.Chunk.Index != right.Chunk.Index {
			return left.Chunk.Index < right.Chunk.Index
		}
		if left.ByteStart != right.ByteStart {
			return left.ByteStart < right.ByteStart
		}
		return left.Chunk.ID < right.Chunk.ID
	})
	if options.MaxMatches > 0 && len(results) > options.MaxMatches {
		results = results[:options.MaxMatches]
	}
	window, nextToken, err := paginate(results, Pagination{PageSize: options.PageSize, PageToken: options.PageToken})
	if err != nil {
		return TextSearchResultList{}, err
	}
	return TextSearchResultList{Results: window, NextPageToken: nextToken, MaxSnippetBytes: options.MaxSnippetBytes}, nil
}

func (store *GraphStore) SearchReferences(ctx context.Context, project projectregistry.Project, options ReferenceSearchOptions) (SymbolReferenceList, error) {
	nodes, err := store.graph.ListNodes(ctx, "CodeReference", map[string]string{"project_id": project.ID})
	if err != nil {
		return SymbolReferenceList{}, err
	}
	fileCache := map[string]FileMetadata{}
	filtered := make([]ladybug.Node, 0, len(nodes))
	for _, node := range nodes {
		if !referenceNodeMatches(node, options) {
			continue
		}
		if _, ok, err := store.searchFile(ctx, project, node.Properties["repo_file_id"], options.Extension, options.PathPrefix, fileCache); err != nil {
			return SymbolReferenceList{}, err
		} else if !ok {
			continue
		}
		filtered = append(filtered, node)
	}
	sortReferenceSearchNodes(filtered, fileCache)
	window, nextToken, err := paginate(filtered, Pagination{PageSize: options.PageSize, PageToken: options.PageToken})
	if err != nil {
		return SymbolReferenceList{}, err
	}
	refs := make([]SymbolReferenceMetadata, 0, len(window))
	for _, node := range window {
		ref, err := referenceMetadataFromNode(node)
		if err != nil {
			return SymbolReferenceList{}, err
		}
		refs = append(refs, ref)
	}
	return SymbolReferenceList{References: refs, NextPageToken: nextToken}, nil
}

func (store *GraphStore) SearchCalls(ctx context.Context, project projectregistry.Project, options ReferenceSearchOptions) (SymbolCallEdgeList, error) {
	nodes, err := store.graph.ListNodes(ctx, "CodeCall", map[string]string{"project_id": project.ID})
	if err != nil {
		return SymbolCallEdgeList{}, err
	}
	fileCache := map[string]FileMetadata{}
	filtered := make([]ladybug.Node, 0, len(nodes))
	for _, node := range nodes {
		if !callNodeMatches(node, options) {
			continue
		}
		if _, ok, err := store.searchFile(ctx, project, node.Properties["repo_file_id"], options.Extension, options.PathPrefix, fileCache); err != nil {
			return SymbolCallEdgeList{}, err
		} else if !ok {
			continue
		}
		filtered = append(filtered, node)
	}
	sortCallSearchNodes(filtered, fileCache)
	window, nextToken, err := paginate(filtered, Pagination{PageSize: options.PageSize, PageToken: options.PageToken})
	if err != nil {
		return SymbolCallEdgeList{}, err
	}
	edges := make([]SymbolCallEdge, 0, len(window))
	for _, node := range window {
		edges = append(edges, callEdgeFromNode(node))
	}
	return SymbolCallEdgeList{Edges: edges, NextPageToken: nextToken}, nil
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
	chunks, err := store.listOutlineChunks(ctx, project, fileID, Pagination{PageSize: MaxPageSize}, options.IncludeChunkText, effectiveMaxChunkBytes(project, options.MaxChunkBytes))
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

func (store *GraphStore) listOutlineChunks(ctx context.Context, project projectregistry.Project, fileID string, pagination Pagination, includeText bool, maxChunkBytes int) ([]OutlineChunkMetadata, error) {
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
		chunk, err := outlineChunkMetadataFromNode(node, includeText, maxChunkBytes)
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

func (store *GraphStore) GetSymbolSource(ctx context.Context, project projectregistry.Project, symbolID string, maxSourceBytes int) (SymbolSource, error) {
	symbol, err := store.GetSymbol(ctx, project, symbolID)
	if err != nil {
		return SymbolSource{}, err
	}
	nodes, err := store.graph.ListNodes(ctx, "ContentChunk", map[string]string{
		"project_id":   project.ID,
		"repo_file_id": symbol.FileID,
	})
	if err != nil {
		return SymbolSource{}, err
	}
	sort.Slice(nodes, func(i, j int) bool {
		left, _ := strconv.Atoi(nodes[i].Properties["byte_start"])
		right, _ := strconv.Atoi(nodes[j].Properties["byte_start"])
		if left == right {
			return nodes[i].ID < nodes[j].ID
		}
		return left < right
	})
	var builder strings.Builder
	for _, node := range nodes {
		chunkStart, _ := strconv.Atoi(node.Properties["byte_start"])
		chunkEnd, _ := strconv.Atoi(node.Properties["byte_end"])
		if chunkEnd <= symbol.StartByte || chunkStart >= symbol.EndByte {
			continue
		}
		text := node.Properties["text"]
		start := maxInt(symbol.StartByte, chunkStart) - chunkStart
		end := minInt(symbol.EndByte, chunkEnd) - chunkStart
		if start < 0 || end < start || start > len(text) {
			continue
		}
		if end > len(text) {
			end = len(text)
		}
		builder.WriteString(text[start:end])
		if builder.Len() >= maxSourceBytes {
			text, _ := truncateUTF8Bytes(builder.String(), maxSourceBytes)
			return SymbolSource{Symbol: symbol, Text: text, TextTruncated: true, MaxBytes: maxSourceBytes}, nil
		}
	}
	text, truncated := truncateUTF8Bytes(builder.String(), maxSourceBytes)
	if text == "" && symbol.StartLine > 0 && symbol.EndLine >= symbol.StartLine {
		text, truncated = symbolSourceByLine(nodes, symbol.StartLine, symbol.EndLine, maxSourceBytes)
	}
	return SymbolSource{Symbol: symbol, Text: text, TextTruncated: truncated, MaxBytes: maxSourceBytes}, nil
}

func symbolSourceByLine(nodes []ladybug.Node, startLine int, endLine int, maxSourceBytes int) (string, bool) {
	var builder strings.Builder
	for _, node := range nodes {
		chunkStartLine, _ := strconv.Atoi(node.Properties["start_line"])
		chunkEndLine, _ := strconv.Atoi(node.Properties["end_line"])
		if chunkEndLine < startLine || chunkStartLine > endLine {
			continue
		}
		lines := strings.SplitAfter(node.Properties["text"], "\n")
		for index, line := range lines {
			lineNumber := chunkStartLine + index
			if lineNumber < startLine || lineNumber > endLine {
				continue
			}
			builder.WriteString(line)
			if builder.Len() >= maxSourceBytes {
				return truncateUTF8Bytes(builder.String(), maxSourceBytes)
			}
		}
	}
	return truncateUTF8Bytes(builder.String(), maxSourceBytes)
}

func (store *GraphStore) ListSymbolReferences(ctx context.Context, project projectregistry.Project, symbolID string, pagination Pagination) (SymbolReferenceList, error) {
	symbol, err := store.GetSymbol(ctx, project, symbolID)
	if err != nil {
		return SymbolReferenceList{}, err
	}
	nodes, err := store.graph.ListNodes(ctx, "CodeReference", map[string]string{
		"project_id":       project.ID,
		"target_symbol_id": symbol.ID,
	})
	if err != nil {
		return SymbolReferenceList{}, err
	}
	sortReferenceNodes(nodes)
	window, nextToken, err := paginate(nodes, pagination)
	if err != nil {
		return SymbolReferenceList{}, err
	}
	refs := make([]SymbolReferenceMetadata, 0, len(window))
	for _, node := range window {
		ref, err := referenceMetadataFromNode(node)
		if err != nil {
			return SymbolReferenceList{}, err
		}
		refs = append(refs, ref)
	}
	return SymbolReferenceList{Symbol: symbol, References: refs, NextPageToken: nextToken}, nil
}

func (store *GraphStore) ListSymbolCallers(ctx context.Context, project projectregistry.Project, symbolID string, pagination Pagination) (SymbolCallEdgeList, error) {
	symbol, err := store.GetSymbol(ctx, project, symbolID)
	if err != nil {
		return SymbolCallEdgeList{}, err
	}
	to := ladybug.NodeRef{Label: "CodeSymbol", ID: symbol.ID}
	return store.listSymbolCallEdges(ctx, project, symbol, ladybug.RelationshipFilter{To: &to, Properties: map[string]string{"project_id": project.ID}}, pagination)
}

func (store *GraphStore) ListSymbolCallees(ctx context.Context, project projectregistry.Project, symbolID string, pagination Pagination) (SymbolCallEdgeList, error) {
	symbol, err := store.GetSymbol(ctx, project, symbolID)
	if err != nil {
		return SymbolCallEdgeList{}, err
	}
	from := ladybug.NodeRef{Label: "CodeSymbol", ID: symbol.ID}
	return store.listSymbolCallEdges(ctx, project, symbol, ladybug.RelationshipFilter{From: &from, Properties: map[string]string{"project_id": project.ID}}, pagination)
}

func (store *GraphStore) listSymbolCallEdges(ctx context.Context, project projectregistry.Project, symbol SymbolMetadata, filter ladybug.RelationshipFilter, pagination Pagination) (SymbolCallEdgeList, error) {
	relationships, err := store.graph.ListRelationships(ctx, "SYMBOL_CALLS_SYMBOL", filter)
	if err != nil {
		return SymbolCallEdgeList{}, err
	}
	sortCallRelationships(relationships)
	window, nextToken, err := paginate(relationships, pagination)
	if err != nil {
		return SymbolCallEdgeList{}, err
	}
	edges := make([]SymbolCallEdge, 0, len(window))
	for _, relationship := range window {
		edges = append(edges, callEdgeFromRelationship(relationship))
	}
	return SymbolCallEdgeList{Symbol: symbol, Edges: edges, NextPageToken: nextToken}, nil
}

func (store *GraphStore) GetSymbolCallGraph(ctx context.Context, project projectregistry.Project, symbolID string, options CallGraphOptions) (SymbolCallGraph, error) {
	root, err := store.GetSymbol(ctx, project, symbolID)
	if err != nil {
		return SymbolCallGraph{}, err
	}
	nodes := map[string]SymbolMetadata{root.ID: root}
	visitedDepth := map[string]int{root.ID: 0}
	queue := []string{root.ID}
	edgesByID := map[string]SymbolCallEdge{}
	truncated := false
	for len(queue) > 0 {
		currentID := queue[0]
		queue = queue[1:]
		depth := visitedDepth[currentID]
		if depth >= options.MaxDepth {
			continue
		}
		for _, rel := range []struct {
			direction string
			filter    ladybug.RelationshipFilter
		}{
			{direction: "callees", filter: ladybug.RelationshipFilter{From: &ladybug.NodeRef{Label: "CodeSymbol", ID: currentID}, Properties: map[string]string{"project_id": project.ID}}},
			{direction: "callers", filter: ladybug.RelationshipFilter{To: &ladybug.NodeRef{Label: "CodeSymbol", ID: currentID}, Properties: map[string]string{"project_id": project.ID}}},
		} {
			if options.Direction != "both" && options.Direction != rel.direction {
				continue
			}
			relationships, err := store.graph.ListRelationships(ctx, "SYMBOL_CALLS_SYMBOL", rel.filter)
			if err != nil {
				return SymbolCallGraph{}, err
			}
			sortCallRelationships(relationships)
			for _, relationship := range relationships {
				edge := callEdgeFromRelationship(relationship)
				edgesByID[edge.ID] = edge
				nextID := relationship.To.ID
				if rel.direction == "callers" {
					nextID = relationship.From.ID
				}
				if _, ok := nodes[nextID]; !ok {
					if len(nodes) >= options.MaxNodes {
						truncated = true
						continue
					}
					node, err := store.graph.GetNode(ctx, "CodeSymbol", nextID)
					if err != nil {
						return SymbolCallGraph{}, err
					}
					metadata, err := symbolMetadataFromNode(node)
					if err != nil {
						return SymbolCallGraph{}, err
					}
					nodes[nextID] = metadata
				}
				if _, ok := visitedDepth[nextID]; !ok {
					visitedDepth[nextID] = depth + 1
					queue = append(queue, nextID)
				}
			}
		}
	}
	nodeList := make([]SymbolMetadata, 0, len(nodes))
	for _, node := range nodes {
		nodeList = append(nodeList, node)
	}
	sort.Slice(nodeList, func(i, j int) bool {
		if nodeList[i].Name == nodeList[j].Name {
			return nodeList[i].ID < nodeList[j].ID
		}
		return nodeList[i].Name < nodeList[j].Name
	})
	edgeList := make([]SymbolCallEdge, 0, len(edgesByID))
	for _, edge := range edgesByID {
		edgeList = append(edgeList, edge)
	}
	sort.Slice(edgeList, func(i, j int) bool { return edgeList[i].ID < edgeList[j].ID })
	return SymbolCallGraph{Symbol: root, Direction: options.Direction, MaxDepth: options.MaxDepth, MaxNodes: options.MaxNodes, Nodes: nodeList, Edges: edgeList, Truncated: truncated}, nil
}

func (store *GraphStore) putReferences(ctx context.Context, projectID string, repoFileID string, versionID string, chunks []Chunk, references []Reference, symbols symbolIndex) error {
	for index, ref := range references {
		refID := codeReferenceID(repoFileID, index, ref)
		enclosingID := symbols.byName[ref.EnclosingSymbolName]
		targetID := symbols.byName[ref.TargetName]
		status := ref.ResolutionStatus
		confidence := ref.Confidence
		if targetID != "" {
			status = "resolved"
			confidence = "direct"
		} else if status == "" {
			status = "unresolved"
		}
		if confidence == "" {
			confidence = "candidate"
		}
		if err := store.graph.PutNode(ctx, ladybug.Node{
			Label: "CodeReference",
			ID:    refID,
			Properties: map[string]string{
				"id":                    refID,
				"project_id":            projectID,
				"repo_file_id":          repoFileID,
				"file_version_id":       versionID,
				"kind":                  ref.Kind,
				"name":                  ref.Name,
				"target_name":           ref.TargetName,
				"target_symbol_id":      targetID,
				"package":               ref.PackageName,
				"receiver":              ref.Receiver,
				"import_path":           ref.ImportPath,
				"enclosing_symbol_id":   enclosingID,
				"enclosing_symbol_name": ref.EnclosingSymbolName,
				"start_line":            strconv.Itoa(ref.StartLine),
				"end_line":              strconv.Itoa(ref.EndLine),
				"start_byte":            strconv.Itoa(ref.StartByte),
				"end_byte":              strconv.Itoa(ref.EndByte),
				"start_column":          strconv.Itoa(ref.StartColumn),
				"end_column":            strconv.Itoa(ref.EndColumn),
				"resolution_status":     status,
				"confidence":            confidence,
			},
		}); err != nil {
			return err
		}
		if targetID != "" {
			if err := store.putRelationship(ctx, "SYMBOL_HAS_REFERENCE", "CodeSymbol", targetID, "CodeReference", refID, projectID); err != nil {
				return err
			}
			if enclosingID != "" {
				if err := store.putRelationship(ctx, "SYMBOL_REFERENCES_SYMBOL", "CodeSymbol", enclosingID, "CodeSymbol", targetID, projectID); err != nil {
					return err
				}
			}
		}
		if chunkID := containingChunkID(versionID, chunks, ref.StartLine); chunkID != "" {
			if err := store.putRelationship(ctx, "REFERENCE_IN_CHUNK", "CodeReference", refID, "ContentChunk", chunkID, projectID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (store *GraphStore) putCalls(ctx context.Context, projectID string, repoFileID string, versionID string, chunks []Chunk, calls []Call, symbols symbolIndex) error {
	for index, call := range calls {
		callID := codeCallID(repoFileID, index, call)
		callerID := symbols.byName[call.CallerName]
		calleeID := symbols.byName[call.CalleeName]
		status := call.ResolutionStatus
		confidence := call.Confidence
		if callerID != "" && calleeID != "" {
			status = "resolved"
			confidence = "direct"
		} else if status == "" {
			status = "unresolved"
		}
		if confidence == "" {
			confidence = "candidate"
		}
		if err := store.graph.PutNode(ctx, ladybug.Node{
			Label: "CodeCall",
			ID:    callID,
			Properties: map[string]string{
				"id":                callID,
				"project_id":        projectID,
				"repo_file_id":      repoFileID,
				"file_version_id":   versionID,
				"caller_symbol_id":  callerID,
				"callee_symbol_id":  calleeID,
				"caller_name":       call.CallerName,
				"callee_name":       call.CalleeName,
				"receiver":          call.Receiver,
				"import_path":       call.ImportPath,
				"start_line":        strconv.Itoa(call.StartLine),
				"end_line":          strconv.Itoa(call.EndLine),
				"start_byte":        strconv.Itoa(call.StartByte),
				"end_byte":          strconv.Itoa(call.EndByte),
				"start_column":      strconv.Itoa(call.StartColumn),
				"end_column":        strconv.Itoa(call.EndColumn),
				"resolution_status": status,
				"confidence":        confidence,
			},
		}); err != nil {
			return err
		}
		if callerID != "" && calleeID != "" {
			if err := store.putRelationshipWithProperties(ctx, "SYMBOL_CALLS_SYMBOL", "CodeSymbol", callerID, "CodeSymbol", calleeID, projectID, map[string]string{
				"call_id":           callID,
				"repo_file_id":      repoFileID,
				"caller_name":       call.CallerName,
				"callee_name":       call.CalleeName,
				"receiver":          call.Receiver,
				"import_path":       call.ImportPath,
				"start_line":        strconv.Itoa(call.StartLine),
				"end_line":          strconv.Itoa(call.EndLine),
				"start_byte":        strconv.Itoa(call.StartByte),
				"end_byte":          strconv.Itoa(call.EndByte),
				"start_column":      strconv.Itoa(call.StartColumn),
				"end_column":        strconv.Itoa(call.EndColumn),
				"resolution_status": status,
				"confidence":        confidence,
			}); err != nil {
				return err
			}
		}
		if chunkID := containingChunkID(versionID, chunks, call.StartLine); chunkID != "" {
			if err := store.putRelationship(ctx, "CALL_IN_CHUNK", "CodeCall", callID, "ContentChunk", chunkID, projectID); err != nil {
				return err
			}
		}
	}
	return nil
}

type symbolIndex struct {
	byName     map[string]string
	byNameKind map[string]SymbolKind
}

func symbolIDIndex(repoFileID string, symbols []Symbol) symbolIndex {
	index := symbolIndex{
		byName:     make(map[string]string, len(symbols)),
		byNameKind: make(map[string]SymbolKind, len(symbols)),
	}
	for _, symbol := range symbols {
		if symbol.Name == "" {
			continue
		}
		id := codeSymbolID(repoFileID, symbol)
		existingKind, exists := index.byNameKind[symbol.Name]
		if !exists || preferredResolutionKind(symbol.Kind, existingKind) {
			index.byName[symbol.Name] = id
			index.byNameKind[symbol.Name] = symbol.Kind
		}
	}
	return index
}

func preferredResolutionKind(candidate SymbolKind, existing SymbolKind) bool {
	if candidate == SymbolKindFunction || candidate == SymbolKindMethod || candidate == SymbolKindClass || candidate == SymbolKindType {
		return existing == SymbolKindPackage || existing == SymbolKindImport
	}
	return false
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
			"id":               run.ID,
			"project_id":       run.ProjectID,
			"trigger":          string(run.Trigger),
			"mode":             run.Mode,
			"status":           string(run.Status),
			"files_seen":       strconv.Itoa(run.FilesSeen),
			"files_ingested":   strconv.Itoa(run.FilesIngested),
			"files_skipped":    strconv.Itoa(run.FilesSkipped),
			"files_unchanged":  strconv.Itoa(run.FilesUnchanged),
			"chunks_stored":    strconv.Itoa(run.ChunksStored),
			"symbols_stored":   strconv.Itoa(run.SymbolsStored),
			"error_category":   run.ErrorCategory,
			"current_phase":    run.CurrentPhase,
			"started_at":       formatTime(run.StartedAt),
			"finished_at":      formatTime(run.FinishedAt),
			"heartbeat_at":     formatTime(run.HeartbeatAt),
			"last_progress_at": formatTime(run.LastProgressAt),
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
	for _, label := range []string{"CodeReference", "CodeCall", "CodeSymbol", "DocumentHeading", "ContentChunk", "FileVersion"} {
		if err := store.graph.DeleteNodes(ctx, label, filter); err != nil {
			return err
		}
	}
	return nil
}

func (store *GraphStore) putRelationship(ctx context.Context, relType string, fromLabel string, fromID string, toLabel string, toID string, projectID string) error {
	return store.putRelationshipWithProperties(ctx, relType, fromLabel, fromID, toLabel, toID, projectID, nil)
}

func (store *GraphStore) putRelationshipWithProperties(ctx context.Context, relType string, fromLabel string, fromID string, toLabel string, toID string, projectID string, properties map[string]string) error {
	props := map[string]string{"project_id": projectID}
	for key, value := range properties {
		props[key] = value
	}
	return store.graph.PutRelationship(ctx, ladybug.Relationship{
		Type:       relType,
		From:       ladybug.NodeRef{Label: fromLabel, ID: fromID},
		To:         ladybug.NodeRef{Label: toLabel, ID: toID},
		Properties: props,
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

func codeReferenceID(repoFileID string, index int, ref Reference) string {
	return repoFileID + ":reference:" + shortHash(strconv.Itoa(index)+"\x00"+ref.Kind+"\x00"+ref.TargetName+"\x00"+ref.EnclosingSymbolName+"\x00"+strconv.Itoa(ref.StartLine)+"\x00"+strconv.Itoa(ref.StartByte))
}

func codeCallID(repoFileID string, index int, call Call) string {
	return repoFileID + ":call:" + shortHash(strconv.Itoa(index)+"\x00"+call.CallerName+"\x00"+call.CalleeName+"\x00"+call.Receiver+"\x00"+strconv.Itoa(call.StartLine)+"\x00"+strconv.Itoa(call.StartByte))
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

func outlineChunkMetadataFromNode(node ladybug.Node, includeText bool, maxChunkBytes int) (OutlineChunkMetadata, error) {
	chunk, err := chunkMetadataFromNode(node, maxChunkBytes)
	if err != nil {
		return OutlineChunkMetadata{}, err
	}
	outline := OutlineChunkMetadata{
		ID:        chunk.ID,
		FileID:    chunk.FileID,
		ProjectID: chunk.ProjectID,
		Index:     chunk.Index,
		StartLine: chunk.StartLine,
		EndLine:   chunk.EndLine,
		ByteStart: chunk.ByteStart,
		ByteEnd:   chunk.ByteEnd,
	}
	if includeText {
		outline.Text = chunk.Text
		outline.TextTruncated = chunk.TextTruncated
	}
	return outline, nil
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
		StartByte:   atoiDefault(node.Properties["start_byte"]),
		EndByte:     atoiDefault(node.Properties["end_byte"]),
		StartColumn: atoiDefault(node.Properties["start_column"]),
		EndColumn:   atoiDefault(node.Properties["end_column"]),
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

func referenceMetadataFromNode(node ladybug.Node) (SymbolReferenceMetadata, error) {
	startLine, err := strconv.Atoi(node.Properties["start_line"])
	if err != nil {
		return SymbolReferenceMetadata{}, err
	}
	endLine, err := strconv.Atoi(node.Properties["end_line"])
	if err != nil {
		return SymbolReferenceMetadata{}, err
	}
	return SymbolReferenceMetadata{
		ID:                  node.ID,
		FileID:              node.Properties["repo_file_id"],
		ProjectID:           node.Properties["project_id"],
		Kind:                node.Properties["kind"],
		Name:                node.Properties["name"],
		TargetName:          node.Properties["target_name"],
		TargetSymbolID:      node.Properties["target_symbol_id"],
		PackageName:         node.Properties["package"],
		Receiver:            node.Properties["receiver"],
		ImportPath:          node.Properties["import_path"],
		EnclosingSymbolID:   node.Properties["enclosing_symbol_id"],
		EnclosingSymbolName: node.Properties["enclosing_symbol_name"],
		StartLine:           startLine,
		EndLine:             endLine,
		StartByte:           atoiDefault(node.Properties["start_byte"]),
		EndByte:             atoiDefault(node.Properties["end_byte"]),
		StartColumn:         atoiDefault(node.Properties["start_column"]),
		EndColumn:           atoiDefault(node.Properties["end_column"]),
		ResolutionStatus:    node.Properties["resolution_status"],
		Confidence:          node.Properties["confidence"],
	}, nil
}

func callEdgeFromRelationship(relationship ladybug.Relationship) SymbolCallEdge {
	props := relationship.Properties
	return SymbolCallEdge{
		ID:               relationshipKeyID(relationship),
		CallID:           props["call_id"],
		FileID:           props["repo_file_id"],
		ProjectID:        props["project_id"],
		CallerSymbolID:   relationship.From.ID,
		CalleeSymbolID:   relationship.To.ID,
		CallerName:       props["caller_name"],
		CalleeName:       props["callee_name"],
		Receiver:         props["receiver"],
		ImportPath:       props["import_path"],
		StartLine:        atoiDefault(props["start_line"]),
		EndLine:          atoiDefault(props["end_line"]),
		StartByte:        atoiDefault(props["start_byte"]),
		EndByte:          atoiDefault(props["end_byte"]),
		StartColumn:      atoiDefault(props["start_column"]),
		EndColumn:        atoiDefault(props["end_column"]),
		ResolutionStatus: props["resolution_status"],
		Confidence:       props["confidence"],
	}
}

func callEdgeFromNode(node ladybug.Node) SymbolCallEdge {
	props := node.Properties
	return SymbolCallEdge{
		ID:               node.ID,
		CallID:           node.ID,
		FileID:           props["repo_file_id"],
		ProjectID:        props["project_id"],
		CallerSymbolID:   props["caller_symbol_id"],
		CalleeSymbolID:   props["callee_symbol_id"],
		CallerName:       props["caller_name"],
		CalleeName:       props["callee_name"],
		Receiver:         props["receiver"],
		ImportPath:       props["import_path"],
		StartLine:        atoiDefault(props["start_line"]),
		EndLine:          atoiDefault(props["end_line"]),
		StartByte:        atoiDefault(props["start_byte"]),
		EndByte:          atoiDefault(props["end_byte"]),
		StartColumn:      atoiDefault(props["start_column"]),
		EndColumn:        atoiDefault(props["end_column"]),
		ResolutionStatus: props["resolution_status"],
		Confidence:       props["confidence"],
	}
}

func relationshipKeyID(relationship ladybug.Relationship) string {
	if callID := relationship.Properties["call_id"]; callID != "" {
		return callID
	}
	return shortHash(relationship.Type + "\x00" + relationship.From.ID + "\x00" + relationship.To.ID)
}

func sortReferenceNodes(nodes []ladybug.Node) {
	sort.Slice(nodes, func(i, j int) bool {
		left := atoiDefault(nodes[i].Properties["start_line"])
		right := atoiDefault(nodes[j].Properties["start_line"])
		if left == right {
			return nodes[i].ID < nodes[j].ID
		}
		return left < right
	})
}

func sortCallRelationships(relationships []ladybug.Relationship) {
	sort.Slice(relationships, func(i, j int) bool {
		left := atoiDefault(relationships[i].Properties["start_line"])
		right := atoiDefault(relationships[j].Properties["start_line"])
		if left == right {
			return relationshipKeyID(relationships[i]) < relationshipKeyID(relationships[j])
		}
		return left < right
	})
}

func atoiDefault(value string) int {
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func (store *GraphStore) searchFile(ctx context.Context, project projectregistry.Project, fileID string, extension string, pathPrefix string, cache map[string]FileMetadata) (FileMetadata, bool, error) {
	if fileID == "" {
		return FileMetadata{}, false, nil
	}
	file, ok := cache[fileID]
	if !ok {
		var err error
		file, err = store.GetFile(ctx, project, fileID)
		if errors.Is(err, ErrIngestionNotFound) {
			return FileMetadata{}, false, nil
		}
		if err != nil {
			return FileMetadata{}, false, err
		}
		cache[fileID] = file
	}
	if file.Status != string(FileStatusEligible) || !file.Present || !file.RelativePathOK {
		return FileMetadata{}, false, nil
	}
	if extension != "" && strings.ToLower(file.Extension) != extension {
		return FileMetadata{}, false, nil
	}
	if pathPrefix != "" && !strings.HasPrefix(file.RelativePath, pathPrefix) {
		return FileMetadata{}, false, nil
	}
	return file, true, nil
}

func chunkMetadataWithoutText(node ladybug.Node) (ChunkMetadata, error) {
	chunk, err := chunkMetadataFromNode(node, 1)
	if err != nil {
		return ChunkMetadata{}, err
	}
	chunk.Text = ""
	chunk.TextTruncated = false
	return chunk, nil
}

func literalMatchIndexes(text string, query string, caseSensitive bool) []int {
	if query == "" {
		return nil
	}
	haystack := text
	needle := query
	if !caseSensitive {
		haystack = strings.ToLower(text)
		needle = strings.ToLower(query)
	}
	indexes := []int{}
	offset := 0
	for {
		found := strings.Index(haystack[offset:], needle)
		if found < 0 {
			break
		}
		index := offset + found
		indexes = append(indexes, index)
		offset = index + len(needle)
	}
	return indexes
}

func boundedSnippet(text string, start int, end int, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		return "", len(text) > 0
	}
	if end < start {
		end = start
	}
	matchLen := end - start
	if matchLen >= maxBytes {
		snippet, _ := truncateUTF8Bytes(text[start:end], maxBytes)
		return snippet, true
	}
	context := (maxBytes - matchLen) / 2
	snippetStart := start - context
	if snippetStart < 0 {
		snippetStart = 0
	}
	snippetEnd := snippetStart + maxBytes
	if snippetEnd < end {
		snippetEnd = end
	}
	if snippetEnd > len(text) {
		snippetEnd = len(text)
		if snippetEnd-maxBytes > 0 {
			snippetStart = snippetEnd - maxBytes
		}
	}
	for snippetStart > 0 && !utf8.RuneStart(text[snippetStart]) {
		snippetStart--
	}
	for snippetEnd < len(text) && !utf8.RuneStart(text[snippetEnd]) {
		snippetEnd++
	}
	snippet, truncated := truncateUTF8Bytes(text[snippetStart:snippetEnd], maxBytes)
	return snippet, truncated || snippetStart > 0 || snippetEnd < len(text)
}

func referenceNodeMatches(node ladybug.Node, options ReferenceSearchOptions) bool {
	props := node.Properties
	if options.NameContains != "" && !containsWithCaseOption(props["name"], options.NameContains, options.CaseSensitive) {
		return false
	}
	if options.TargetNameContains != "" && !containsWithCaseOption(props["target_name"], options.TargetNameContains, options.CaseSensitive) {
		return false
	}
	if options.EnclosingContains != "" && !containsWithCaseOption(props["enclosing_symbol_name"], options.EnclosingContains, options.CaseSensitive) {
		return false
	}
	if options.ResolutionStatus != "" && props["resolution_status"] != options.ResolutionStatus {
		return false
	}
	if options.Confidence != "" && props["confidence"] != options.Confidence {
		return false
	}
	return true
}

func callNodeMatches(node ladybug.Node, options ReferenceSearchOptions) bool {
	props := node.Properties
	if options.NameContains != "" && !containsWithCaseOption(props["callee_name"], options.NameContains, options.CaseSensitive) && !containsWithCaseOption(props["caller_name"], options.NameContains, options.CaseSensitive) {
		return false
	}
	if options.CallerNameContains != "" && !containsWithCaseOption(props["caller_name"], options.CallerNameContains, options.CaseSensitive) {
		return false
	}
	if options.CalleeNameContains != "" && !containsWithCaseOption(props["callee_name"], options.CalleeNameContains, options.CaseSensitive) {
		return false
	}
	if options.ResolutionStatus != "" && props["resolution_status"] != options.ResolutionStatus {
		return false
	}
	if options.Confidence != "" && props["confidence"] != options.Confidence {
		return false
	}
	return true
}

func sortReferenceSearchNodes(nodes []ladybug.Node, files map[string]FileMetadata) {
	sort.Slice(nodes, func(i, j int) bool {
		leftFile := files[nodes[i].Properties["repo_file_id"]].RelativePath
		rightFile := files[nodes[j].Properties["repo_file_id"]].RelativePath
		if leftFile != rightFile {
			return leftFile < rightFile
		}
		leftLine := atoiDefault(nodes[i].Properties["start_line"])
		rightLine := atoiDefault(nodes[j].Properties["start_line"])
		if leftLine != rightLine {
			return leftLine < rightLine
		}
		return nodes[i].ID < nodes[j].ID
	})
}

func sortCallSearchNodes(nodes []ladybug.Node, files map[string]FileMetadata) {
	sort.Slice(nodes, func(i, j int) bool {
		leftFile := files[nodes[i].Properties["repo_file_id"]].RelativePath
		rightFile := files[nodes[j].Properties["repo_file_id"]].RelativePath
		if leftFile != rightFile {
			return leftFile < rightFile
		}
		leftLine := atoiDefault(nodes[i].Properties["start_line"])
		rightLine := atoiDefault(nodes[j].Properties["start_line"])
		if leftLine != rightLine {
			return leftLine < rightLine
		}
		return nodes[i].ID < nodes[j].ID
	})
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
