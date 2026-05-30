package projectingestion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

type searchMutationStore interface {
	UpsertSearchFile(context.Context, projectregistry.Project, FileState, []Chunk, []Symbol, []Reference, []Call) error
	DeleteSearchFile(context.Context, string, string) error
	DeleteSearchProject(context.Context, string) error
	MarkSearchIndexDegraded(context.Context, string, string) error
	ClearSearchIndexDegraded(context.Context, string) error
}

type searchRepairStore interface {
	searchMutationStore
	ReconcileSearchIndex(context.Context, projectregistry.Project) ([]FileState, error)
}

type searchQueryStore interface {
	SearchText(context.Context, projectregistry.Project, TextSearchOptions) (TextSearchResultList, error)
	SearchFiles(context.Context, projectregistry.Project, FileSearchOptions) (FileList, error)
	SearchSymbols(context.Context, projectregistry.Project, SymbolFilter, Pagination) (SymbolList, error)
	SearchReferences(context.Context, projectregistry.Project, ReferenceSearchOptions) (SymbolReferenceList, error)
	SearchCalls(context.Context, projectregistry.Project, ReferenceSearchOptions) (SymbolCallEdgeList, error)
	SearchIndexHealth(context.Context, projectregistry.Project) (SearchIndexHealth, error)
}

type SearchIndexHealth struct {
	Degraded bool
	Reason   string
}

type graphSearchAdapter struct {
	graph *GraphStore
}

func (adapter graphSearchAdapter) SearchText(ctx context.Context, project projectregistry.Project, options TextSearchOptions) (TextSearchResultList, error) {
	return adapter.graph.SearchText(ctx, project, options)
}

func (adapter graphSearchAdapter) SearchFiles(context.Context, projectregistry.Project, FileSearchOptions) (FileList, error) {
	return FileList{}, ErrUnsupportedIngest
}

func (adapter graphSearchAdapter) SearchSymbols(ctx context.Context, project projectregistry.Project, filter SymbolFilter, pagination Pagination) (SymbolList, error) {
	return adapter.graph.ListSymbols(ctx, project, filter, pagination)
}

func (adapter graphSearchAdapter) SearchReferences(ctx context.Context, project projectregistry.Project, options ReferenceSearchOptions) (SymbolReferenceList, error) {
	return adapter.graph.SearchReferences(ctx, project, options)
}

func (adapter graphSearchAdapter) SearchCalls(ctx context.Context, project projectregistry.Project, options ReferenceSearchOptions) (SymbolCallEdgeList, error) {
	return adapter.graph.SearchCalls(ctx, project, options)
}

func (adapter graphSearchAdapter) SearchIndexHealth(context.Context, projectregistry.Project) (SearchIndexHealth, error) {
	return SearchIndexHealth{}, nil
}

func (store *SQLiteStore) UpsertSearchFile(ctx context.Context, project projectregistry.Project, state FileState, chunks []Chunk, symbols []Symbol, references []Reference, calls []Call) error {
	if state.Status != FileStatusEligible || !state.Present || !state.RelativePathSafe || state.ContentSHA256 == "" {
		return store.DeleteSearchFile(ctx, project.ID, repoFileID(project.GraphNamespace, state.RelativePathHash))
	}
	fileID := repoFileID(project.GraphNamespace, state.RelativePathHash)
	versionID := fileVersionID(fileID, state.ContentSHA256)
	extension := strings.ToLower(path.Ext(state.RelativePath))
	symbolIDs := symbolIDIndex(fileID, symbols)

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteSearchFileTx(ctx, tx, project.ID, fileID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_search_files_fts (
		project_id, file_id, relative_path, extension, size_bytes, modified_at
	) VALUES (?, ?, ?, ?, ?, ?)`,
		project.ID, fileID, state.RelativePath, extension, strconv.FormatInt(state.SizeBytes, 10), formatTime(state.ModifiedAt)); err != nil {
		return err
	}
	for _, chunk := range chunks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_search_chunks_fts (
			project_id, file_id, chunk_id, relative_path, extension, size_bytes, modified_at,
			chunk_index, start_line, end_line, byte_start, byte_end, text
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			project.ID, fileID, contentChunkID(versionID, chunk.Index), state.RelativePath, extension,
			strconv.FormatInt(state.SizeBytes, 10), formatTime(state.ModifiedAt),
			strconv.Itoa(chunk.Index), strconv.Itoa(chunk.StartLine), strconv.Itoa(chunk.EndLine),
			strconv.Itoa(chunk.ByteStart), strconv.Itoa(chunk.ByteEnd), chunk.Text); err != nil {
			return err
		}
	}
	for _, symbol := range symbols {
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_search_symbols_fts (
			project_id, file_id, symbol_id, relative_path, extension, kind, name, package, import_path, receiver,
			start_line, end_line, start_byte, end_byte, start_column, end_column
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			project.ID, fileID, codeSymbolID(fileID, symbol), state.RelativePath, extension, string(symbol.Kind), symbol.Name,
			symbol.PackageName, symbol.ImportPath, symbol.Receiver, strconv.Itoa(symbol.StartLine), strconv.Itoa(symbol.EndLine),
			strconv.Itoa(symbol.StartByte), strconv.Itoa(symbol.EndByte), strconv.Itoa(symbol.StartColumn), strconv.Itoa(symbol.EndColumn)); err != nil {
			return err
		}
	}
	for index, ref := range references {
		targetID := symbolIDs.byName[ref.TargetName]
		enclosingID := symbolIDs.byName[ref.EnclosingSymbolName]
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_search_references_fts (
			project_id, file_id, reference_id, relative_path, extension, kind, name, target_name, target_symbol_id,
			package, receiver, import_path, enclosing_symbol_id, enclosing_symbol_name, start_line, end_line,
			start_byte, end_byte, start_column, end_column, resolution_status, confidence
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			project.ID, fileID, codeReferenceID(fileID, index, ref), state.RelativePath, extension, ref.Kind, ref.Name,
			ref.TargetName, targetID, ref.PackageName, ref.Receiver, ref.ImportPath, enclosingID, ref.EnclosingSymbolName,
			strconv.Itoa(ref.StartLine), strconv.Itoa(ref.EndLine), strconv.Itoa(ref.StartByte), strconv.Itoa(ref.EndByte),
			strconv.Itoa(ref.StartColumn), strconv.Itoa(ref.EndColumn), status, confidence); err != nil {
			return err
		}
	}
	for index, call := range calls {
		callerID := symbolIDs.byName[call.CallerName]
		calleeID := symbolIDs.byName[call.CalleeName]
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_search_calls_fts (
			project_id, file_id, call_id, relative_path, extension, caller_symbol_id, callee_symbol_id, caller_name,
			callee_name, receiver, import_path, start_line, end_line, start_byte, end_byte, start_column, end_column,
			resolution_status, confidence
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			project.ID, fileID, codeCallID(fileID, index, call), state.RelativePath, extension, callerID, calleeID,
			call.CallerName, call.CalleeName, call.Receiver, call.ImportPath, strconv.Itoa(call.StartLine), strconv.Itoa(call.EndLine),
			strconv.Itoa(call.StartByte), strconv.Itoa(call.EndByte), strconv.Itoa(call.StartColumn), strconv.Itoa(call.EndColumn),
			status, confidence); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (store *SQLiteStore) DeleteSearchFile(ctx context.Context, projectID string, fileID string) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteSearchFileTx(ctx, tx, projectID, fileID); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *SQLiteStore) DeleteSearchProject(ctx context.Context, projectID string) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range searchFTSTables() {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE project_id = ?", projectID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (store *SQLiteStore) ReconcileSearchIndex(ctx context.Context, project projectregistry.Project) ([]FileState, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	states, err := searchRepairEligibleStates(ctx, tx, project)
	if err != nil {
		return nil, err
	}
	eligibleByFileID := make(map[string]FileState, len(states))
	for _, state := range states {
		eligibleByFileID[repoFileID(project.GraphNamespace, state.RelativePathHash)] = state
	}

	searchFileCounts, err := searchFileCountsByID(ctx, tx, project.ID)
	if err != nil {
		return nil, err
	}
	searchFileIDs, err := searchFileIDsByAnyTable(ctx, tx, project.ID)
	if err != nil {
		return nil, err
	}
	for fileID := range searchFileIDs {
		if _, ok := eligibleByFileID[fileID]; ok {
			continue
		}
		if err := deleteSearchFileTx(ctx, tx, project.ID, fileID); err != nil {
			return nil, err
		}
	}

	var repair []FileState
	for _, state := range states {
		fileID := repoFileID(project.GraphNamespace, state.RelativePathHash)
		count := searchFileCounts[fileID]
		if count != 1 {
			repair = append(repair, state)
			continue
		}
		needsRepair, err := searchFileNeedsRepair(ctx, tx, project.ID, fileID, state)
		if err != nil {
			return nil, err
		}
		if needsRepair {
			repair = append(repair, state)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return repair, nil
}

func (store *SQLiteStore) MarkSearchIndexDegraded(ctx context.Context, projectID string, reason string) error {
	reason = safeSearchIndexReason(reason)
	_, err := store.db.ExecContext(ctx, `INSERT INTO project_search_index_state (
		project_id, status, degraded_reason, updated_at
	) VALUES (?, 'degraded', ?, CURRENT_TIMESTAMP)
	ON CONFLICT(project_id) DO UPDATE SET
		status = excluded.status,
		degraded_reason = excluded.degraded_reason,
		updated_at = excluded.updated_at`, projectID, reason)
	return err
}

func (store *SQLiteStore) ClearSearchIndexDegraded(ctx context.Context, projectID string) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO project_search_index_state (
		project_id, status, degraded_reason, updated_at
	) VALUES (?, 'ready', '', CURRENT_TIMESTAMP)
	ON CONFLICT(project_id) DO UPDATE SET
		status = excluded.status,
		degraded_reason = excluded.degraded_reason,
		updated_at = excluded.updated_at`, projectID)
	return err
}

func (store *SQLiteStore) SearchIndexHealth(ctx context.Context, project projectregistry.Project) (SearchIndexHealth, error) {
	var status, reason string
	err := store.db.QueryRowContext(ctx, `SELECT status, degraded_reason
		FROM project_search_index_state
		WHERE project_id = ?`, project.ID).Scan(&status, &reason)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SearchIndexHealth{}, sanitizeSearchError(err)
	}
	if status == "degraded" {
		return SearchIndexHealth{Degraded: true, Reason: safeSearchIndexReason(reason)}, nil
	}
	drift, err := store.searchIndexHasDrift(ctx, project)
	if err != nil {
		return SearchIndexHealth{}, err
	}
	if drift {
		return SearchIndexHealth{Degraded: true, Reason: "search_index_drift"}, nil
	}
	return SearchIndexHealth{}, nil
}

func (store *SQLiteStore) searchIndexHasDrift(ctx context.Context, project projectregistry.Project) (bool, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, sanitizeSearchError(err)
	}
	defer tx.Rollback()
	states, err := searchRepairEligibleStates(ctx, tx, project)
	if err != nil {
		return false, sanitizeSearchError(err)
	}
	counts, err := searchFileCountsByID(ctx, tx, project.ID)
	if err != nil {
		return false, sanitizeSearchError(err)
	}
	searchFileIDs, err := searchFileIDsByAnyTable(ctx, tx, project.ID)
	if err != nil {
		return false, sanitizeSearchError(err)
	}
	eligibleByFileID := make(map[string]struct{}, len(states))
	for _, state := range states {
		eligibleByFileID[repoFileID(project.GraphNamespace, state.RelativePathHash)] = struct{}{}
	}
	for fileID := range searchFileIDs {
		if _, ok := eligibleByFileID[fileID]; !ok {
			return true, nil
		}
	}
	for _, state := range states {
		fileID := repoFileID(project.GraphNamespace, state.RelativePathHash)
		if counts[fileID] != 1 {
			return true, nil
		}
		needsRepair, err := searchFileNeedsRepair(ctx, tx, project.ID, fileID, state)
		if err != nil {
			return false, sanitizeSearchError(err)
		}
		if needsRepair {
			return true, nil
		}
	}
	return false, nil
}

func deleteSearchFileTx(ctx context.Context, tx *sql.Tx, projectID string, fileID string) error {
	for _, table := range searchFTSTables() {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE project_id = ? AND file_id = ?", projectID, fileID); err != nil {
			return err
		}
	}
	return nil
}

func searchRepairEligibleStates(ctx context.Context, tx *sql.Tx, project projectregistry.Project) ([]FileState, error) {
	rows, err := tx.QueryContext(ctx, `SELECT
		relative_path_hash, relative_path, content_sha256, size_bytes, modified_at, last_event_at, last_ingested_at
		FROM project_file_ingestion_state
		WHERE project_id = ?
			AND status = ?
			AND present = 1
			AND relative_path_safe = 1
			AND content_sha256 != ''
		ORDER BY relative_path_hash`, project.ID, string(FileStatusEligible))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var states []FileState
	for rows.Next() {
		var state FileState
		var modifiedAt, lastEventAt, lastIngestedAt string
		if err := rows.Scan(&state.RelativePathHash, &state.RelativePath, &state.ContentSHA256, &state.SizeBytes, &modifiedAt, &lastEventAt, &lastIngestedAt); err != nil {
			return nil, err
		}
		state.ProjectID = project.ID
		state.Status = FileStatusEligible
		state.Present = true
		state.RelativePathSafe = true
		if parsed, err := parseOptionalTime(modifiedAt); err == nil {
			state.ModifiedAt = parsed
		}
		if parsed, err := parseOptionalTime(lastEventAt); err == nil {
			state.LastEventAt = parsed
		}
		if parsed, err := parseOptionalTime(lastIngestedAt); err == nil {
			state.LastIngestedAt = parsed
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

func searchFileCountsByID(ctx context.Context, tx *sql.Tx, projectID string) (map[string]int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT file_id, COUNT(*)
		FROM project_search_files_fts
		WHERE project_id = ?
		GROUP BY file_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var fileID string
		var count int
		if err := rows.Scan(&fileID, &count); err != nil {
			return nil, err
		}
		counts[fileID] = count
	}
	return counts, rows.Err()
}

func searchFileIDsByAnyTable(ctx context.Context, tx *sql.Tx, projectID string) (map[string]struct{}, error) {
	ids := make(map[string]struct{})
	for _, table := range searchFTSTables() {
		rows, err := tx.QueryContext(ctx, "SELECT file_id FROM "+table+" WHERE project_id = ? GROUP BY file_id", projectID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var fileID string
			if err := rows.Scan(&fileID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			ids[fileID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func hasSearchChunkVersion(ctx context.Context, tx *sql.Tx, projectID string, fileID string, versionID string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM project_search_chunks_fts
		WHERE project_id = ?
			AND file_id = ?
			AND chunk_id LIKE ? ESCAPE '\'`,
		projectID, fileID, escapeLike(versionID)+":chunk:%").Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func searchFileNeedsRepair(ctx context.Context, tx *sql.Tx, projectID string, fileID string, state FileState) (bool, error) {
	versionID := fileVersionID(fileID, state.ContentSHA256)
	indexed, err := hasSearchChunkVersion(ctx, tx, projectID, fileID, versionID)
	if err != nil {
		return false, err
	}
	if indexed {
		return false, nil
	}
	chunkCount, err := searchChunkCount(ctx, tx, projectID, fileID)
	if err != nil {
		return false, err
	}
	if state.SizeBytes == 0 && chunkCount == 0 {
		return false, nil
	}
	return true, nil
}

func searchChunkCount(ctx context.Context, tx *sql.Tx, projectID string, fileID string) (int, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM project_search_chunks_fts
		WHERE project_id = ?
			AND file_id = ?`, projectID, fileID).Scan(&count)
	return count, err
}

func searchFTSTables() []string {
	return []string{
		"project_search_chunks_fts",
		"project_search_files_fts",
		"project_search_symbols_fts",
		"project_search_references_fts",
		"project_search_calls_fts",
	}
}

func (store *SQLiteStore) SearchText(ctx context.Context, project projectregistry.Project, options TextSearchOptions) (TextSearchResultList, error) {
	where := []string{"project_id = ?"}
	args := []any{project.ID}
	if options.Extension != "" {
		where = append(where, "extension = ?")
		args = append(args, options.Extension)
	}
	if options.PathPrefix != "" {
		where = append(where, "relative_path LIKE ? ESCAPE '\\'")
		args = append(args, likePrefix(options.PathPrefix))
	}
	if match, ok := ftsLiteralQuery(options.Query); ok {
		where = append(where, "project_search_chunks_fts MATCH ?")
		args = append(args, match)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT
		file_id, chunk_id, relative_path, extension, size_bytes, modified_at, chunk_index, start_line, end_line, byte_start, byte_end, text
		FROM project_search_chunks_fts
		WHERE `+strings.Join(where, " AND "), args...)
	if err != nil {
		return TextSearchResultList{}, sanitizeSearchError(err)
	}
	defer rows.Close()

	results := make([]TextSearchResult, 0)
	for rows.Next() {
		var row searchChunkRow
		if err := rows.Scan(&row.FileID, &row.ChunkID, &row.RelativePath, &row.Extension, &row.SizeBytes, &row.ModifiedAt, &row.ChunkIndex, &row.StartLine, &row.EndLine, &row.ByteStart, &row.ByteEnd, &row.Text); err != nil {
			return TextSearchResultList{}, sanitizeSearchError(err)
		}
		if options.PathPrefix != "" && !strings.HasPrefix(row.RelativePath, options.PathPrefix) {
			continue
		}
		indexes := literalMatchIndexes(row.Text, options.Query, options.CaseSensitive)
		for _, index := range indexes {
			end := index + len(options.Query)
			lineStart := atoiDefault(row.StartLine) + strings.Count(row.Text[:index], "\n")
			lineEnd := lineStart + strings.Count(row.Text[index:end], "\n")
			snippet, truncated := boundedSnippet(row.Text, index, end, options.MaxSnippetBytes)
			results = append(results, TextSearchResult{
				File:             row.fileMetadata(project.ID),
				Chunk:            row.chunkMetadata(project.ID),
				LineStart:        lineStart,
				LineEnd:          lineEnd,
				ByteStart:        atoiDefault(row.ByteStart) + index,
				ByteEnd:          atoiDefault(row.ByteStart) + end,
				Snippet:          snippet,
				SnippetTruncated: truncated,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return TextSearchResultList{}, sanitizeSearchError(err)
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

func (store *SQLiteStore) SearchFiles(ctx context.Context, project projectregistry.Project, options FileSearchOptions) (FileList, error) {
	where := []string{"project_id = ?"}
	args := []any{project.ID}
	if options.Extension != "" {
		where = append(where, "extension = ?")
		args = append(args, options.Extension)
	}
	if options.PathPrefix != "" {
		where = append(where, "relative_path LIKE ? ESCAPE '\\'")
		args = append(args, likePrefix(options.PathPrefix))
	}
	if match, ok := ftsLiteralQuery(options.PathContains); ok {
		where = append(where, "project_search_files_fts MATCH ?")
		args = append(args, match)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT file_id, relative_path, extension, size_bytes, modified_at
		FROM project_search_files_fts
		WHERE `+strings.Join(where, " AND "), args...)
	if err != nil {
		return FileList{}, sanitizeSearchError(err)
	}
	defer rows.Close()
	files := make([]FileMetadata, 0)
	for rows.Next() {
		var fileID, relativePath, extension, sizeRaw, modifiedRaw string
		if err := rows.Scan(&fileID, &relativePath, &extension, &sizeRaw, &modifiedRaw); err != nil {
			return FileList{}, sanitizeSearchError(err)
		}
		if options.PathContains != "" && !containsWithCaseOption(relativePath, options.PathContains, options.CaseSensitive) {
			continue
		}
		size, _ := strconv.ParseInt(sizeRaw, 10, 64)
		modifiedAt, err := parseOptionalTime(modifiedRaw)
		if err != nil {
			return FileList{}, sanitizeSearchError(err)
		}
		files = append(files, FileMetadata{
			ID:             fileID,
			ProjectID:      project.ID,
			RelativePath:   relativePath,
			Extension:      extension,
			Status:         string(FileStatusEligible),
			Present:        true,
			SizeBytes:      size,
			ModifiedAt:     modifiedAt,
			RelativePathOK: true,
		})
	}
	if err := rows.Err(); err != nil {
		return FileList{}, sanitizeSearchError(err)
	}
	sortFileMetadata(files)
	window, nextToken, err := paginate(files, Pagination{PageSize: options.PageSize, PageToken: options.PageToken})
	if err != nil {
		return FileList{}, err
	}
	return FileList{Files: window, NextPageToken: nextToken}, nil
}

func (store *SQLiteStore) SearchSymbols(ctx context.Context, project projectregistry.Project, filter SymbolFilter, pagination Pagination) (SymbolList, error) {
	where := []string{"project_id = ?"}
	args := []any{project.ID}
	if filter.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, string(filter.Kind))
	}
	if filter.FileID != "" {
		where = append(where, "file_id = ?")
		args = append(args, filter.FileID)
	}
	if filter.Extension != "" {
		where = append(where, "extension = ?")
		args = append(args, filter.Extension)
	}
	if filter.Package != "" {
		where = append(where, "package = ?")
		args = append(args, filter.Package)
	}
	if match, ok := ftsLiteralQuery(firstNonEmpty(filter.NameContains, filter.NamePrefix, filter.Receiver)); ok {
		where = append(where, "project_search_symbols_fts MATCH ?")
		args = append(args, match)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT
		symbol_id, file_id, kind, name, package, import_path, receiver, start_line, end_line, start_byte, end_byte, start_column, end_column
		FROM project_search_symbols_fts
		WHERE `+strings.Join(where, " AND "), args...)
	if err != nil {
		return SymbolList{}, sanitizeSearchError(err)
	}
	defer rows.Close()
	symbols := make([]SymbolMetadata, 0)
	for rows.Next() {
		var symbol SymbolMetadata
		var startLine, endLine, startByte, endByte, startColumn, endColumn string
		if err := rows.Scan(&symbol.ID, &symbol.FileID, &symbol.Kind, &symbol.Name, &symbol.PackageName, &symbol.ImportPath, &symbol.Receiver, &startLine, &endLine, &startByte, &endByte, &startColumn, &endColumn); err != nil {
			return SymbolList{}, sanitizeSearchError(err)
		}
		if filter.NamePrefix != "" && !strings.HasPrefix(symbol.Name, filter.NamePrefix) {
			continue
		}
		if filter.NameContains != "" && !containsWithCaseOption(symbol.Name, filter.NameContains, filter.CaseSensitive) {
			continue
		}
		if filter.Receiver != "" && symbol.Receiver != filter.Receiver {
			continue
		}
		symbol.ProjectID = project.ID
		symbol.StartLine = atoiDefault(startLine)
		symbol.EndLine = atoiDefault(endLine)
		symbol.StartByte = atoiDefault(startByte)
		symbol.EndByte = atoiDefault(endByte)
		symbol.StartColumn = atoiDefault(startColumn)
		symbol.EndColumn = atoiDefault(endColumn)
		symbols = append(symbols, symbol)
	}
	if err := rows.Err(); err != nil {
		return SymbolList{}, sanitizeSearchError(err)
	}
	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].Name == symbols[j].Name {
			return symbols[i].ID < symbols[j].ID
		}
		return symbols[i].Name < symbols[j].Name
	})
	window, nextToken, err := paginate(symbols, pagination)
	if err != nil {
		return SymbolList{}, err
	}
	return SymbolList{Symbols: window, NextPageToken: nextToken}, nil
}

func (store *SQLiteStore) SearchReferences(ctx context.Context, project projectregistry.Project, options ReferenceSearchOptions) (SymbolReferenceList, error) {
	where := []string{"project_id = ?"}
	args := []any{project.ID}
	addReferenceFilters(&where, &args, options)
	if match, ok := ftsLiteralQuery(firstNonEmpty(options.NameContains, options.TargetNameContains, options.EnclosingContains)); ok {
		where = append(where, "project_search_references_fts MATCH ?")
		args = append(args, match)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT
		reference_id, file_id, relative_path, kind, name, target_name, target_symbol_id, package, receiver, import_path,
		enclosing_symbol_id, enclosing_symbol_name, start_line, end_line, start_byte, end_byte, start_column, end_column,
		resolution_status, confidence
		FROM project_search_references_fts
		WHERE `+strings.Join(where, " AND "), args...)
	if err != nil {
		return SymbolReferenceList{}, sanitizeSearchError(err)
	}
	defer rows.Close()
	refs := make([]referenceSearchRow, 0)
	for rows.Next() {
		var ref referenceSearchRow
		if err := rows.Scan(&ref.ID, &ref.FileID, &ref.RelativePath, &ref.Kind, &ref.Name, &ref.TargetName, &ref.TargetSymbolID, &ref.PackageName,
			&ref.Receiver, &ref.ImportPath, &ref.EnclosingSymbolID, &ref.EnclosingSymbolName, &ref.StartLine, &ref.EndLine,
			&ref.StartByte, &ref.EndByte, &ref.StartColumn, &ref.EndColumn, &ref.ResolutionStatus, &ref.Confidence); err != nil {
			return SymbolReferenceList{}, sanitizeSearchError(err)
		}
		if !referenceRowMatches(ref, options) {
			continue
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return SymbolReferenceList{}, sanitizeSearchError(err)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].less(refs[j]) })
	window, nextToken, err := paginate(refs, Pagination{PageSize: options.PageSize, PageToken: options.PageToken})
	if err != nil {
		return SymbolReferenceList{}, err
	}
	out := make([]SymbolReferenceMetadata, 0, len(window))
	for _, row := range window {
		out = append(out, row.metadata(project.ID))
	}
	return SymbolReferenceList{References: out, NextPageToken: nextToken}, nil
}

func (store *SQLiteStore) SearchCalls(ctx context.Context, project projectregistry.Project, options ReferenceSearchOptions) (SymbolCallEdgeList, error) {
	where := []string{"project_id = ?"}
	args := []any{project.ID}
	addCallFilters(&where, &args, options)
	if match, ok := ftsLiteralQuery(firstNonEmpty(options.NameContains, options.CallerNameContains, options.CalleeNameContains)); ok {
		where = append(where, "project_search_calls_fts MATCH ?")
		args = append(args, match)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT
		call_id, file_id, relative_path, caller_symbol_id, callee_symbol_id, caller_name, callee_name, receiver, import_path,
		start_line, end_line, start_byte, end_byte, start_column, end_column, resolution_status, confidence
		FROM project_search_calls_fts
		WHERE `+strings.Join(where, " AND "), args...)
	if err != nil {
		return SymbolCallEdgeList{}, sanitizeSearchError(err)
	}
	defer rows.Close()
	calls := make([]callSearchRow, 0)
	for rows.Next() {
		var call callSearchRow
		if err := rows.Scan(&call.ID, &call.FileID, &call.RelativePath, &call.CallerSymbolID, &call.CalleeSymbolID, &call.CallerName,
			&call.CalleeName, &call.Receiver, &call.ImportPath, &call.StartLine, &call.EndLine, &call.StartByte, &call.EndByte,
			&call.StartColumn, &call.EndColumn, &call.ResolutionStatus, &call.Confidence); err != nil {
			return SymbolCallEdgeList{}, sanitizeSearchError(err)
		}
		if !callRowMatches(call, options) {
			continue
		}
		calls = append(calls, call)
	}
	if err := rows.Err(); err != nil {
		return SymbolCallEdgeList{}, sanitizeSearchError(err)
	}
	sort.Slice(calls, func(i, j int) bool { return calls[i].less(calls[j]) })
	window, nextToken, err := paginate(calls, Pagination{PageSize: options.PageSize, PageToken: options.PageToken})
	if err != nil {
		return SymbolCallEdgeList{}, err
	}
	out := make([]SymbolCallEdge, 0, len(window))
	for _, row := range window {
		out = append(out, row.metadata(project.ID))
	}
	return SymbolCallEdgeList{Edges: out, NextPageToken: nextToken}, nil
}

type searchChunkRow struct {
	FileID       string
	ChunkID      string
	RelativePath string
	Extension    string
	SizeBytes    string
	ModifiedAt   string
	ChunkIndex   string
	StartLine    string
	EndLine      string
	ByteStart    string
	ByteEnd      string
	Text         string
}

func (row searchChunkRow) fileMetadata(projectID string) FileMetadata {
	size, _ := strconv.ParseInt(row.SizeBytes, 10, 64)
	modifiedAt, _ := parseOptionalTime(row.ModifiedAt)
	return FileMetadata{
		ID:             row.FileID,
		ProjectID:      projectID,
		RelativePath:   row.RelativePath,
		Extension:      row.Extension,
		Status:         string(FileStatusEligible),
		Present:        true,
		SizeBytes:      size,
		ModifiedAt:     modifiedAt,
		RelativePathOK: true,
	}
}

func (row searchChunkRow) chunkMetadata(projectID string) ChunkMetadata {
	return ChunkMetadata{
		ID:        row.ChunkID,
		FileID:    row.FileID,
		ProjectID: projectID,
		Index:     atoiDefault(row.ChunkIndex),
		StartLine: atoiDefault(row.StartLine),
		EndLine:   atoiDefault(row.EndLine),
		ByteStart: atoiDefault(row.ByteStart),
		ByteEnd:   atoiDefault(row.ByteEnd),
	}
}

type referenceSearchRow struct {
	ID                  string
	FileID              string
	RelativePath        string
	Kind                string
	Name                string
	TargetName          string
	TargetSymbolID      string
	PackageName         string
	Receiver            string
	ImportPath          string
	EnclosingSymbolID   string
	EnclosingSymbolName string
	StartLine           string
	EndLine             string
	StartByte           string
	EndByte             string
	StartColumn         string
	EndColumn           string
	ResolutionStatus    string
	Confidence          string
}

func (row referenceSearchRow) less(other referenceSearchRow) bool {
	if row.RelativePath != other.RelativePath {
		return row.RelativePath < other.RelativePath
	}
	if atoiDefault(row.StartLine) != atoiDefault(other.StartLine) {
		return atoiDefault(row.StartLine) < atoiDefault(other.StartLine)
	}
	return row.ID < other.ID
}

func (row referenceSearchRow) metadata(projectID string) SymbolReferenceMetadata {
	return SymbolReferenceMetadata{
		ID:                  row.ID,
		FileID:              row.FileID,
		ProjectID:           projectID,
		Kind:                row.Kind,
		Name:                row.Name,
		TargetName:          row.TargetName,
		TargetSymbolID:      row.TargetSymbolID,
		PackageName:         row.PackageName,
		Receiver:            row.Receiver,
		ImportPath:          row.ImportPath,
		EnclosingSymbolID:   row.EnclosingSymbolID,
		EnclosingSymbolName: row.EnclosingSymbolName,
		StartLine:           atoiDefault(row.StartLine),
		EndLine:             atoiDefault(row.EndLine),
		StartByte:           atoiDefault(row.StartByte),
		EndByte:             atoiDefault(row.EndByte),
		StartColumn:         atoiDefault(row.StartColumn),
		EndColumn:           atoiDefault(row.EndColumn),
		ResolutionStatus:    row.ResolutionStatus,
		Confidence:          row.Confidence,
	}
}

type callSearchRow struct {
	ID               string
	FileID           string
	RelativePath     string
	CallerSymbolID   string
	CalleeSymbolID   string
	CallerName       string
	CalleeName       string
	Receiver         string
	ImportPath       string
	StartLine        string
	EndLine          string
	StartByte        string
	EndByte          string
	StartColumn      string
	EndColumn        string
	ResolutionStatus string
	Confidence       string
}

func (row callSearchRow) less(other callSearchRow) bool {
	if row.RelativePath != other.RelativePath {
		return row.RelativePath < other.RelativePath
	}
	if atoiDefault(row.StartLine) != atoiDefault(other.StartLine) {
		return atoiDefault(row.StartLine) < atoiDefault(other.StartLine)
	}
	return row.ID < other.ID
}

func (row callSearchRow) metadata(projectID string) SymbolCallEdge {
	return SymbolCallEdge{
		ID:               row.ID,
		CallID:           row.ID,
		FileID:           row.FileID,
		ProjectID:        projectID,
		CallerSymbolID:   row.CallerSymbolID,
		CalleeSymbolID:   row.CalleeSymbolID,
		CallerName:       row.CallerName,
		CalleeName:       row.CalleeName,
		Receiver:         row.Receiver,
		ImportPath:       row.ImportPath,
		StartLine:        atoiDefault(row.StartLine),
		EndLine:          atoiDefault(row.EndLine),
		StartByte:        atoiDefault(row.StartByte),
		EndByte:          atoiDefault(row.EndByte),
		StartColumn:      atoiDefault(row.StartColumn),
		EndColumn:        atoiDefault(row.EndColumn),
		ResolutionStatus: row.ResolutionStatus,
		Confidence:       row.Confidence,
	}
}

func addReferenceFilters(where *[]string, args *[]any, options ReferenceSearchOptions) {
	if options.Extension != "" {
		*where = append(*where, "extension = ?")
		*args = append(*args, options.Extension)
	}
	if options.PathPrefix != "" {
		*where = append(*where, "relative_path LIKE ? ESCAPE '\\'")
		*args = append(*args, likePrefix(options.PathPrefix))
	}
	if options.ResolutionStatus != "" {
		*where = append(*where, "resolution_status = ?")
		*args = append(*args, options.ResolutionStatus)
	}
	if options.Confidence != "" {
		*where = append(*where, "confidence = ?")
		*args = append(*args, options.Confidence)
	}
}

func addCallFilters(where *[]string, args *[]any, options ReferenceSearchOptions) {
	addReferenceFilters(where, args, options)
}

func referenceRowMatches(row referenceSearchRow, options ReferenceSearchOptions) bool {
	if options.NameContains != "" && !containsWithCaseOption(row.Name, options.NameContains, options.CaseSensitive) {
		return false
	}
	if options.TargetNameContains != "" && !containsWithCaseOption(row.TargetName, options.TargetNameContains, options.CaseSensitive) {
		return false
	}
	if options.EnclosingContains != "" && !containsWithCaseOption(row.EnclosingSymbolName, options.EnclosingContains, options.CaseSensitive) {
		return false
	}
	if options.PathPrefix != "" && !strings.HasPrefix(row.RelativePath, options.PathPrefix) {
		return false
	}
	return true
}

func callRowMatches(row callSearchRow, options ReferenceSearchOptions) bool {
	if options.NameContains != "" && !containsWithCaseOption(row.CalleeName, options.NameContains, options.CaseSensitive) && !containsWithCaseOption(row.CallerName, options.NameContains, options.CaseSensitive) {
		return false
	}
	if options.CallerNameContains != "" && !containsWithCaseOption(row.CallerName, options.CallerNameContains, options.CaseSensitive) {
		return false
	}
	if options.CalleeNameContains != "" && !containsWithCaseOption(row.CalleeName, options.CalleeNameContains, options.CaseSensitive) {
		return false
	}
	if options.PathPrefix != "" && !strings.HasPrefix(row.RelativePath, options.PathPrefix) {
		return false
	}
	return true
}

func ftsLiteralQuery(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 3 {
		return "", false
	}
	for _, r := range value {
		if r == ' ' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return "", false
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`, true
}

func likePrefix(value string) string {
	return escapeLike(value) + "%"
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func sanitizeSearchError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "fts") || strings.Contains(message, "match") || strings.Contains(message, "syntax") {
		return fmt.Errorf("%w: invalid search query", ErrInvalidInput)
	}
	return fmt.Errorf("%w: search index unavailable", ErrUnsupportedIngest)
}

func safeSearchIndexReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case "search_index_write_failed":
		return "search_index_write_failed"
	case "search_index_delete_failed":
		return "search_index_delete_failed"
	case "search_index_rebuild_failed":
		return "search_index_rebuild_failed"
	case "search_index_drift":
		return "search_index_drift"
	default:
		return "search_index_degraded"
	}
}
