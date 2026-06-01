package projectingestion

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
	tree_sitter_dart "github.com/UserNobody14/tree-sitter-dart/bindings/go"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_c_sharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

const astSearchMatchLimit = 1000

func searchAST(ctx context.Context, svc *Service, project projectregistry.Project, options ASTSearchOptions) (ASTSearchResultList, error) {
	entry, ok := astSearchCatalogEntry(options.Language, options.Query)
	if !ok {
		return ASTSearchResultList{}, ErrInvalidInput
	}
	if options.Extension != "" && !astSearchExtensionAllowed(entry.Extensions, options.Extension) {
		return ASTSearchResultList{}, ErrInvalidInput
	}
	language, err := astSearchLanguage(entry.Language)
	if err != nil {
		return ASTSearchResultList{}, err
	}
	query, queryErr := tree_sitter.NewQuery(language, entry.Query)
	if queryErr != nil || query == nil {
		return ASTSearchResultList{}, fmt.Errorf("%w: ast query is unavailable", ErrInvalidInput)
	}
	defer query.Close()
	pageSize, offset, err := paginationWindow(Pagination{PageSize: options.PageSize, PageToken: options.PageToken})
	if err != nil {
		return ASTSearchResultList{}, err
	}
	resultLimit := options.MaxMatches
	if resultLimit > pageSize {
		resultLimit = pageSize
	}
	targetCount := offset + resultLimit + 1
	captureAllowlist := astCaptureAllowlist(options.Captures)
	states, err := astSearchFileStates(ctx, svc, project, entry, options)
	if err != nil {
		return ASTSearchResultList{}, err
	}
	results := make([]ASTSearchResult, 0, resultLimit)
	truncated := false
	for _, state := range states {
		if err := ctx.Err(); err != nil {
			return ASTSearchResultList{}, err
		}
		fileID := repoFileID(project.GraphNamespace, state.RelativePathHash)
		chunks, err := astSearchAllChunks(ctx, svc, project, fileID)
		if err != nil {
			return ASTSearchResultList{}, err
		}
		file := MetadataForFileState(project, state)
		fileResults, fileTruncated, err := astSearchFile(ctx, language, query, entry, captureAllowlist, file, chunks, options.MaxSnippetBytes)
		if err != nil {
			return ASTSearchResultList{}, err
		}
		truncated = truncated || fileTruncated
		for _, result := range fileResults {
			results = append(results, result)
			if len(results) >= targetCount || len(results) >= options.MaxMatches {
				truncated = true
				break
			}
		}
		if truncated && (len(results) >= targetCount || len(results) >= options.MaxMatches) {
			break
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].File.RelativePath != results[j].File.RelativePath {
			return results[i].File.RelativePath < results[j].File.RelativePath
		}
		if results[i].ByteStart != results[j].ByteStart {
			return results[i].ByteStart < results[j].ByteStart
		}
		return results[i].CaptureName < results[j].CaptureName
	})
	nextToken := ""
	end := offset + resultLimit
	if offset > len(results) {
		offset = len(results)
	}
	if end > len(results) {
		end = len(results)
	}
	if end < len(results) {
		nextToken = fmt.Sprint(end)
		truncated = true
	}
	return ASTSearchResultList{
		Results:         results[offset:end],
		NextPageToken:   nextToken,
		QueryLanguage:   entry.Language,
		QueryVersion:    entry.Version,
		ResultTruncated: truncated,
		MaxSnippetBytes: options.MaxSnippetBytes,
	}, nil
}

func astSearchLanguage(language string) (*tree_sitter.Language, error) {
	switch normalizeASTLanguage(language) {
	case "go":
		return tree_sitter.NewLanguage(tree_sitter_go.Language()), nil
	case "python":
		return tree_sitter.NewLanguage(tree_sitter_python.Language()), nil
	case "javascript":
		return tree_sitter.NewLanguage(tree_sitter_javascript.Language()), nil
	case "jsx", "tsx":
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()), nil
	case "typescript":
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()), nil
	case "csharp":
		return tree_sitter.NewLanguage(tree_sitter_c_sharp.Language()), nil
	case "dart":
		return tree_sitter.NewLanguage(tree_sitter_dart.Language()), nil
	default:
		return nil, ErrInvalidInput
	}
}

func astSearchFileStates(ctx context.Context, svc *Service, project projectregistry.Project, entry astSearchQuery, options ASTSearchOptions) ([]FileState, error) {
	extensions := entry.Extensions
	if options.Extension != "" {
		extensions = []string{options.Extension}
	}
	seen := make(map[string]struct{})
	var out []FileState
	present := true
	for _, extension := range extensions {
		states, err := svc.state.ListFileStates(ctx, project.ID, FileStateFilter{
			Status:     FileStatusEligible,
			Extension:  extension,
			PathPrefix: options.PathPrefix,
			Present:    &present,
		})
		if err != nil {
			return nil, err
		}
		for _, state := range states {
			if !state.RelativePathSafe || state.Status != FileStatusEligible || !state.Present || state.SkippedReason == SkipReasonSemanticTooLarge {
				continue
			}
			if _, ok := seen[state.RelativePathHash]; ok {
				continue
			}
			seen[state.RelativePathHash] = struct{}{}
			out = append(out, state)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].RelativePath < out[j].RelativePath
	})
	return out, nil
}

func astSearchCoverage(ctx context.Context, svc *Service, project projectregistry.Project, entry astSearchQuery, options ASTSearchOptions) (ASTCoverageMetadata, error) {
	extensions := entry.Extensions
	if options.Extension != "" {
		extensions = []string{options.Extension}
	}
	coverage := ASTCoverageMetadata{
		Language:       entry.Language,
		Extensions:     append([]string(nil), extensions...),
		CoverageScope:  string(SkipReasonFileTooLarge),
		CoverageStatus: "complete",
	}
	present := true
	for _, extension := range extensions {
		eligible, err := svc.state.ListFileStates(ctx, project.ID, FileStateFilter{
			Status:     FileStatusEligible,
			Extension:  extension,
			PathPrefix: options.PathPrefix,
			Present:    &present,
		})
		if err != nil {
			return ASTCoverageMetadata{}, err
		}
		coverage.EligibleFiles += len(eligible)
		oversized, err := svc.state.ListFileStates(ctx, project.ID, FileStateFilter{
			Status:        FileStatusEligible,
			Extension:     extension,
			PathPrefix:    options.PathPrefix,
			SkippedReason: SkipReasonSemanticTooLarge,
			Present:       &present,
		})
		if err != nil {
			return ASTCoverageMetadata{}, err
		}
		coverage.SkippedFileTooLarge += len(oversized)
	}
	if coverage.SkippedFileTooLarge > 0 {
		coverage.CoverageStatus = "partial"
		coverage.CoveragePartialCause = string(SkipReasonFileTooLarge)
	}
	return coverage, nil
}

func astSearchCatalogCoverage(ctx context.Context, svc *Service, project projectregistry.Project) ([]ASTCoverageMetadata, error) {
	out := make([]ASTCoverageMetadata, 0, len(astSearchLanguageExtensions))
	for language, extensions := range astSearchLanguageExtensions {
		coverage, err := astSearchCoverage(ctx, svc, project, astSearchQuery{Language: language, Extensions: extensions}, ASTSearchOptions{})
		if err != nil {
			return nil, err
		}
		out = append(out, coverage)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Language < out[j].Language
	})
	return out, nil
}

func astSearchAllChunks(ctx context.Context, svc *Service, project projectregistry.Project, fileID string) ([]ChunkMetadata, error) {
	var chunks []ChunkMetadata
	pageToken := ""
	for {
		list, err := svc.graph.ListChunks(ctx, project, fileID, Pagination{PageSize: MaxPageSize, PageToken: pageToken}, project.MaxChunkBytes)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, list.Chunks...)
		if list.NextPageToken == "" {
			return chunks, nil
		}
		pageToken = list.NextPageToken
	}
}

func astSearchFile(ctx context.Context, language *tree_sitter.Language, query *tree_sitter.Query, entry astSearchQuery, captureAllowlist map[string]struct{}, file FileMetadata, chunks []ChunkMetadata, maxSnippetBytes int) ([]ASTSearchResult, bool, error) {
	content, ok := astJoinChunks(chunks)
	if !ok || content == "" {
		return nil, false, nil
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(language); err != nil {
		return nil, false, fmt.Errorf("%w: ast parser unavailable", ErrInvalidInput)
	}
	contentBytes := []byte(content)
	tree := parser.ParseCtx(ctx, contentBytes, nil)
	if tree == nil {
		return nil, false, fmt.Errorf("%w: ast parse failed", ErrInvalidInput)
	}
	defer tree.Close()
	root := tree.RootNode()
	if root == nil {
		return nil, false, nil
	}
	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.SetMatchLimit(astSearchMatchLimit)
	captures := cursor.Captures(query, root, contentBytes)
	captureNames := query.CaptureNames()
	var results []ASTSearchResult
	for {
		match, captureIndex := captures.Next()
		if match == nil {
			break
		}
		if int(captureIndex) >= len(match.Captures) {
			continue
		}
		capture := match.Captures[captureIndex]
		if int(capture.Index) >= len(captureNames) {
			continue
		}
		captureName := captureNames[capture.Index]
		if len(captureAllowlist) > 0 {
			if _, ok := captureAllowlist[captureName]; !ok {
				continue
			}
		}
		if !astCatalogCaptureAllowed(entry, captureName) {
			continue
		}
		node := capture.Node
		chunk, ok := astChunkForByte(chunks, int(node.StartByte()))
		if !ok {
			continue
		}
		captureText, captureTextTruncated := truncateUTF8Bytes(strings.TrimSpace(node.Utf8Text(contentBytes)), maxSnippetBytes)
		snippet, snippetTruncated := astSnippet(content, int(node.StartByte()), int(node.EndByte()), maxSnippetBytes)
		results = append(results, ASTSearchResult{
			File:                 file,
			Chunk:                astChunkMetadataWithoutText(chunk),
			CaptureName:          captureName,
			CaptureText:          captureText,
			CaptureTextTruncated: captureTextTruncated,
			LineStart:            int(node.StartPosition().Row) + 1,
			LineEnd:              int(node.EndPosition().Row) + 1,
			ByteStart:            int(node.StartByte()),
			ByteEnd:              int(node.EndByte()),
			StartColumn:          int(node.StartPosition().Column) + 1,
			EndColumn:            int(node.EndPosition().Column) + 1,
			Snippet:              snippet,
			SnippetTruncated:     snippetTruncated,
		})
	}
	return results, cursor.DidExceedMatchLimit(), nil
}

func astJoinChunks(chunks []ChunkMetadata) (string, bool) {
	if len(chunks) == 0 {
		return "", true
	}
	chunks = append([]ChunkMetadata(nil), chunks...)
	sort.SliceStable(chunks, func(i, j int) bool {
		if chunks[i].ByteStart != chunks[j].ByteStart {
			return chunks[i].ByteStart < chunks[j].ByteStart
		}
		return chunks[i].Index < chunks[j].Index
	})
	var builder strings.Builder
	for _, chunk := range chunks {
		if chunk.Text == "" || !utf8.ValidString(chunk.Text) || chunk.ByteStart != builder.Len() {
			return "", false
		}
		builder.WriteString(chunk.Text)
	}
	return builder.String(), true
}

func astChunkForByte(chunks []ChunkMetadata, offset int) (ChunkMetadata, bool) {
	for _, chunk := range chunks {
		if offset >= chunk.ByteStart && offset < chunk.ByteEnd {
			return chunk, true
		}
	}
	if len(chunks) > 0 && offset == chunks[len(chunks)-1].ByteEnd {
		return chunks[len(chunks)-1], true
	}
	return ChunkMetadata{}, false
}

func astCaptureAllowlist(captures []string) map[string]struct{} {
	if len(captures) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(captures))
	for _, capture := range captures {
		out[capture] = struct{}{}
	}
	return out
}

func astCatalogCaptureAllowed(entry astSearchQuery, name string) bool {
	for _, capture := range entry.Captures {
		if capture == name {
			return true
		}
	}
	return false
}

func astSearchExtensionAllowed(extensions []string, extension string) bool {
	for _, candidate := range extensions {
		if candidate == extension {
			return true
		}
	}
	return false
}

func astChunkMetadataWithoutText(chunk ChunkMetadata) ChunkMetadata {
	chunk.Text = ""
	chunk.TextTruncated = false
	return chunk
}

func astSnippet(content string, start int, end int, maxBytes int) (string, bool) {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end > len(content) {
		end = len(content)
	}
	windowStart := start - maxBytes/2
	if windowStart < 0 {
		windowStart = 0
	}
	windowEnd := end + maxBytes/2
	if windowEnd > len(content) {
		windowEnd = len(content)
	}
	for windowStart > 0 && !utf8.RuneStart(content[windowStart]) {
		windowStart--
	}
	for windowEnd < len(content) && !utf8.RuneStart(content[windowEnd]) {
		windowEnd++
	}
	snippet, truncated := truncateUTF8Bytes(content[windowStart:windowEnd], maxBytes)
	return snippet, truncated || windowStart > 0 || windowEnd < len(content)
}
