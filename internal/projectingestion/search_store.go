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

	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

type searchMutationStore interface {
	UpsertSearchFile(context.Context, projectregistry.Project, FileState, []Chunk, []Symbol, []Reference, []Call) error
	UpsertSearchFilesBatch(context.Context, projectregistry.Project, []PreparedSearchFile) error
	DeleteSearchFile(context.Context, string, string) error
	DeleteSearchProject(context.Context, string) error
	MarkSearchIndexDegraded(context.Context, string, string) error
	ClearSearchIndexDegraded(context.Context, string) error
	HasSearchFileVersion(context.Context, projectregistry.Project, FileState) (bool, error)
	UpdateSearchFileMetadata(context.Context, projectregistry.Project, FileState) error
	UpdateSearchFileMetadataBatch(context.Context, projectregistry.Project, []FileState) error
}

type searchBatchMutationStore interface {
	ApplySearchFileBatch(context.Context, projectregistry.Project, []fullScanFileResult) error
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

type PreparedSearchFile struct {
	State      FileState
	Chunks     []Chunk
	Symbols    []Symbol
	References []Reference
	Calls      []Call
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
	return store.UpsertSearchFilesBatch(ctx, project, []PreparedSearchFile{{
		State:      state,
		Chunks:     chunks,
		Symbols:    symbols,
		References: references,
		Calls:      calls,
	}})
}

func (store *SQLiteStore) UpsertSearchFilesBatch(ctx context.Context, project projectregistry.Project, files []PreparedSearchFile) error {
	if len(files) == 0 {
		return nil
	}
	weight, insertedRows, deleteStatements := preparedSearchFilesWritePlan(project, files)
	tx, unlock, err := store.beginWriteTx(ctx)
	if err != nil {
		return sanitizeSearchError(err)
	}
	var rewritesSkipped int64
	for _, file := range files {
		skippedRewrite, err := upsertSearchFileTx(ctx, tx, project, file.State, file.Chunks, file.Symbols, file.References, file.Calls)
		if err != nil {
			_ = tx.Rollback()
			unlock()
			_ = store.MarkSearchIndexDegraded(ctx, project.ID, "search_index_write_failed")
			return sanitizeSearchError(err)
		}
		if skippedRewrite {
			rewritesSkipped++
		}
	}
	err = tx.Commit()
	unlock()
	if err == nil {
		store.recordSearchWrite(project.ID, weight, insertedRows, deleteStatements)
		store.recordSearchRewriteSkipped(project.ID, rewritesSkipped)
	}
	return sanitizeSearchError(err)
}

func (store *SQLiteStore) ApplySearchFileBatch(ctx context.Context, project projectregistry.Project, results []fullScanFileResult) error {
	if len(results) == 0 {
		return nil
	}
	return forEachFullScanResultBatchByWeight(results, fullScanPreparedBatchMaxWriteWeight, func(batch []fullScanFileResult) error {
		return store.applySearchFileBatchTx(ctx, project, batch)
	})
}

func forEachFullScanResultBatchByWeight(results []fullScanFileResult, maxWeight int, fn func([]fullScanFileResult) error) error {
	if len(results) == 0 {
		return nil
	}
	if maxWeight <= 0 {
		maxWeight = fullScanPreparedBatchMaxWriteWeight
	}
	batchStart := 0
	batchWeight := 0
	for index, result := range results {
		weight := result.fullScanWriteWeight()
		if index > batchStart && batchWeight+weight > maxWeight {
			if err := fn(results[batchStart:index]); err != nil {
				return err
			}
			batchStart = index
			batchWeight = 0
		}
		batchWeight += weight
	}
	return fn(results[batchStart:])
}

func preparedSearchFilesWritePlan(project projectregistry.Project, files []PreparedSearchFile) (int, map[string]int64, map[string]int64) {
	weight := 0
	insertedRows := make(map[string]int64)
	deleteStatements := make(map[string]int64)
	for _, file := range files {
		weight += preparedSearchFileWriteWeight(file)
		addPreparedSearchFileWritePlan(file, insertedRows, deleteStatements)
	}
	return weight, emptyNilInt64Map(insertedRows), emptyNilInt64Map(deleteStatements)
}

func preparedSearchFileWriteWeight(file PreparedSearchFile) int {
	weight := 1
	if file.State.Status != FileStatusEligible || !file.State.Present || !file.State.RelativePathSafe || file.State.ContentSHA256 == "" {
		return weight
	}
	weight += len(file.Chunks)
	weight += len(file.Symbols)
	weight += len(file.References)
	weight += len(file.Calls)
	return weight
}

func fullScanResultsWritePlan(project projectregistry.Project, results []fullScanFileResult) (int, map[string]int64, map[string]int64) {
	weight := 0
	insertedRows := make(map[string]int64)
	deleteStatements := make(map[string]int64)
	for _, result := range results {
		if result.unchanged {
			weight++
			continue
		}
		file := PreparedSearchFile{
			State:      result.state,
			Chunks:     result.chunks,
			Symbols:    result.symbols,
			References: result.references,
			Calls:      result.calls,
		}
		weight += preparedSearchFileWriteWeight(file)
		addPreparedSearchFileWritePlan(file, insertedRows, deleteStatements)
	}
	return weight, emptyNilInt64Map(insertedRows), emptyNilInt64Map(deleteStatements)
}

func addPreparedSearchFileWritePlan(file PreparedSearchFile, insertedRows map[string]int64, deleteStatements map[string]int64) {
	if file.State.Status != FileStatusEligible || !file.State.Present || !file.State.RelativePathSafe || file.State.ContentSHA256 == "" {
		addSearchFTSDeletePlan(deleteStatements)
		return
	}
	addSearchFTSDeletePlan(deleteStatements)
	insertedRows["project_search_files_fts"]++
	insertedRows["project_search_chunks_fts"] += int64(len(file.Chunks))
	insertedRows["project_search_symbols_fts"] += int64(len(file.Symbols))
	insertedRows["project_search_references_fts"] += int64(len(file.References))
	insertedRows["project_search_calls_fts"] += int64(len(file.Calls))
}

func searchFTSDeletePlan() map[string]int64 {
	deleteStatements := make(map[string]int64)
	addSearchFTSDeletePlan(deleteStatements)
	return deleteStatements
}

func addSearchFTSDeletePlan(deleteStatements map[string]int64) {
	for _, table := range searchFTSTables() {
		deleteStatements[table]++
	}
}

func emptyNilInt64Map(values map[string]int64) map[string]int64 {
	for _, value := range values {
		if value != 0 {
			return values
		}
	}
	return nil
}

func (store *SQLiteStore) applySearchFileBatchTx(ctx context.Context, project projectregistry.Project, results []fullScanFileResult) error {
	if len(results) == 0 {
		return nil
	}
	weight, insertedRows, deleteStatements := fullScanResultsWritePlan(project, results)
	tx, unlock, err := store.beginWriteTx(ctx)
	if err != nil {
		return sanitizeSearchError(err)
	}
	defer unlock()
	defer tx.Rollback()
	var rewritesSkipped int64
	for _, result := range results {
		switch {
		case result.unchanged:
			if err := upsertSearchFileVersionTx(ctx, tx, project.ID, repoFileID(project.GraphNamespace, result.state.RelativePathHash), result.state); err != nil {
				return sanitizeSearchError(err)
			}
		case result.state.Status == FileStatusEligible:
			skippedRewrite, err := upsertSearchFileTx(ctx, tx, project, result.state, result.chunks, result.symbols, result.references, result.calls)
			if err != nil {
				return sanitizeSearchError(err)
			}
			if skippedRewrite {
				rewritesSkipped++
			}
		default:
			if err := deleteSearchFileTx(ctx, tx, project.ID, repoFileID(project.GraphNamespace, result.state.RelativePathHash)); err != nil {
				return sanitizeSearchError(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return sanitizeSearchError(err)
	}
	store.recordSearchWrite(project.ID, weight, insertedRows, deleteStatements)
	store.recordSearchRewriteSkipped(project.ID, rewritesSkipped)
	return nil
}

func upsertSearchFileTx(ctx context.Context, tx *sql.Tx, project projectregistry.Project, state FileState, chunks []Chunk, symbols []Symbol, references []Reference, calls []Call) (bool, error) {
	if state.Status != FileStatusEligible || !state.Present || !state.RelativePathSafe || state.ContentSHA256 == "" {
		return false, deleteSearchFileTx(ctx, tx, project.ID, repoFileID(project.GraphNamespace, state.RelativePathHash))
	}
	fileID := repoFileID(project.GraphNamespace, state.RelativePathHash)
	versionID := fileVersionID(fileID, state.ContentSHA256)
	extension := strings.ToLower(path.Ext(state.RelativePath))
	symbolIDs := symbolIDIndex(fileID, symbols)
	current, err := searchFileVersionCurrentTx(ctx, tx, project.ID, fileID, state)
	if err != nil {
		return false, err
	}
	if current && len(symbols) == 0 && len(references) == 0 && len(calls) == 0 {
		return true, updateSearchFileMetadataTx(ctx, tx, project, state)
	}
	if err := deleteSearchFileTx(ctx, tx, project.ID, fileID); err != nil {
		return false, err
	}
	if err := upsertSearchFileVersionTx(ctx, tx, project.ID, fileID, state); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_search_files_fts (
		project_id, file_id, relative_path, extension, size_bytes, modified_at
	) VALUES (?, ?, ?, ?, ?, ?)`,
		project.ID, fileID, state.RelativePath, extension, strconv.FormatInt(state.SizeBytes, 10), formatTime(state.ModifiedAt)); err != nil {
		return false, err
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
			return false, err
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
			return false, err
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
			return false, err
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
			return false, err
		}
	}
	return false, nil
}

func (store *SQLiteStore) HasSearchFileVersion(ctx context.Context, project projectregistry.Project, state FileState) (bool, error) {
	if state.Status != FileStatusEligible || !state.Present || !state.RelativePathSafe || state.ContentSHA256 == "" {
		return false, nil
	}
	fileID := repoFileID(project.GraphNamespace, state.RelativePathHash)
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, sanitizeSearchError(err)
	}
	defer tx.Rollback()
	current, err := searchFileVersionCurrentTx(ctx, tx, project.ID, fileID, state)
	if err != nil {
		return false, sanitizeSearchError(err)
	}
	return current, nil
}

func searchFileVersionCurrentTx(ctx context.Context, tx *sql.Tx, projectID string, fileID string, state FileState) (bool, error) {
	var contentSHA256, status string
	var present int
	err := tx.QueryRowContext(ctx, `SELECT content_sha256, status, present
		FROM project_search_file_versions
		WHERE project_id = ? AND file_id = ?`, projectID, fileID).Scan(&contentSHA256, &status, &present)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if contentSHA256 != state.ContentSHA256 || status != string(FileStatusEligible) || present != 1 {
		return false, nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_search_files_fts WHERE project_id = ? AND file_id = ?`, projectID, fileID).Scan(&count); err != nil {
		return false, err
	}
	if count != 1 {
		return false, nil
	}
	needsRepair, err := searchFileNeedsRepair(ctx, tx, projectID, fileID, state)
	if err != nil {
		return false, err
	}
	return !needsRepair, nil
}

func (store *SQLiteStore) UpdateSearchFileMetadata(ctx context.Context, project projectregistry.Project, state FileState) error {
	return store.UpdateSearchFileMetadataBatch(ctx, project, []FileState{state})
}

func (store *SQLiteStore) UpdateSearchFileMetadataBatch(ctx context.Context, project projectregistry.Project, states []FileState) error {
	if len(states) == 0 {
		return nil
	}
	weight, deleteStatements := searchMetadataUpdateWritePlan(states)
	tx, unlock, err := store.beginWriteTx(ctx)
	if err != nil {
		return sanitizeSearchError(err)
	}
	for _, state := range states {
		if err := updateSearchFileMetadataTx(ctx, tx, project, state); err != nil {
			_ = tx.Rollback()
			unlock()
			_ = store.MarkSearchIndexDegraded(ctx, project.ID, "search_index_write_failed")
			return sanitizeSearchError(err)
		}
	}
	err = tx.Commit()
	unlock()
	if err == nil {
		store.recordSearchWrite(project.ID, weight, nil, deleteStatements)
	}
	return sanitizeSearchError(err)
}

func searchMetadataUpdateWritePlan(states []FileState) (int, map[string]int64) {
	deleteStatements := make(map[string]int64)
	for _, state := range states {
		if state.Status != FileStatusEligible || !state.Present || !state.RelativePathSafe || state.ContentSHA256 == "" {
			addSearchFTSDeletePlan(deleteStatements)
		}
	}
	return len(states), emptyNilInt64Map(deleteStatements)
}

func updateSearchFileMetadataTx(ctx context.Context, tx *sql.Tx, project projectregistry.Project, state FileState) error {
	fileID := repoFileID(project.GraphNamespace, state.RelativePathHash)
	extension := strings.ToLower(path.Ext(state.RelativePath))
	modifiedAt := formatTime(state.ModifiedAt)
	if state.Status != FileStatusEligible || !state.Present || !state.RelativePathSafe || state.ContentSHA256 == "" {
		return deleteSearchFileTx(ctx, tx, project.ID, fileID)
	}
	if err := upsertSearchFileVersionTx(ctx, tx, project.ID, fileID, state); err != nil {
		return err
	}
	for _, table := range []string{"project_search_files_fts", "project_search_chunks_fts"} {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s
			SET relative_path = ?, extension = ?, size_bytes = ?, modified_at = ?
			WHERE project_id = ? AND file_id = ?`, table),
			state.RelativePath,
			extension,
			strconv.FormatInt(state.SizeBytes, 10),
			modifiedAt,
			project.ID,
			fileID,
		); err != nil {
			return sanitizeSearchError(err)
		}
	}
	for _, table := range []string{"project_search_symbols_fts", "project_search_references_fts", "project_search_calls_fts"} {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s
			SET relative_path = ?, extension = ?
			WHERE project_id = ? AND file_id = ?`, table),
			state.RelativePath,
			extension,
			project.ID,
			fileID,
		); err != nil {
			return sanitizeSearchError(err)
		}
	}
	return nil
}

func (store *SQLiteStore) DeleteSearchFile(ctx context.Context, projectID string, fileID string) error {
	tx, unlock, err := store.beginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	defer tx.Rollback()
	if err := deleteSearchFileTx(ctx, tx, projectID, fileID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	store.recordSearchWrite(projectID, 1, nil, searchFTSDeletePlan())
	return nil
}

func (store *SQLiteStore) DeleteSearchProject(ctx context.Context, projectID string) error {
	tx, unlock, err := store.beginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	defer tx.Rollback()
	for _, table := range searchFTSTables() {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE project_id = ?", projectID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_search_file_versions WHERE project_id = ?`, projectID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	store.recordSearchWrite(projectID, 1, nil, searchFTSDeletePlan())
	return nil
}

func (store *SQLiteStore) ReconcileSearchIndex(ctx context.Context, project projectregistry.Project) ([]FileState, error) {
	var states []FileState
	if store.searchState != nil {
		var err error
		states, err = store.searchRepairEligibleStates(ctx, nil, project)
		if err != nil {
			return nil, err
		}
	}
	tx, unlock, err := store.beginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	defer tx.Rollback()

	if store.searchState == nil {
		states, err = store.searchRepairEligibleStates(ctx, tx, project)
		if err != nil {
			return nil, err
		}
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
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
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
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
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
	health, err := store.ContextSearchIndexHealth(ctx, project)
	if err != nil || health.Degraded {
		return health, err
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

func (store *SQLiteStore) ContextSearchIndexHealth(ctx context.Context, project projectregistry.Project) (SearchIndexHealth, error) {
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
	return SearchIndexHealth{}, nil
}

func (store *SQLiteStore) CountSearchSymbols(ctx context.Context, project projectregistry.Project) (int, error) {
	var count int
	err := store.db.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM project_search_symbols_fts
		WHERE project_id = ?`, project.ID).Scan(&count)
	return count, sanitizeSearchError(err)
}

func (store *SQLiteStore) CountSearchChunks(ctx context.Context, project projectregistry.Project) (int, error) {
	var count int
	err := store.db.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM project_search_chunks_fts
		WHERE project_id = ?`, project.ID).Scan(&count)
	return count, sanitizeSearchError(err)
}

func (store *SQLiteStore) searchIndexHasDrift(ctx context.Context, project projectregistry.Project) (bool, error) {
	var states []FileState
	if store.searchState != nil {
		var err error
		states, err = store.searchRepairEligibleStates(ctx, nil, project)
		if err != nil {
			return false, sanitizeSearchError(err)
		}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, sanitizeSearchError(err)
	}
	defer tx.Rollback()
	if store.searchState == nil {
		states, err = store.searchRepairEligibleStates(ctx, tx, project)
		if err != nil {
			return false, sanitizeSearchError(err)
		}
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_search_file_versions WHERE project_id = ? AND file_id = ?`, projectID, fileID); err != nil {
		return err
	}
	return nil
}

func upsertSearchFileVersionTx(ctx context.Context, tx *sql.Tx, projectID string, fileID string, state FileState) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO project_search_file_versions (
		project_id, file_id, content_sha256, relative_path, extension, status, present, size_bytes, modified_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(project_id, file_id) DO UPDATE SET
		content_sha256 = excluded.content_sha256,
		relative_path = excluded.relative_path,
		extension = excluded.extension,
		status = excluded.status,
		present = excluded.present,
		size_bytes = excluded.size_bytes,
		modified_at = excluded.modified_at,
		updated_at = excluded.updated_at`,
		projectID,
		fileID,
		state.ContentSHA256,
		state.RelativePath,
		strings.ToLower(path.Ext(state.RelativePath)),
		string(state.Status),
		boolToInt(state.Present),
		state.SizeBytes,
		formatTime(state.ModifiedAt),
	)
	return err
}

func markSearchIndexDegradedTx(ctx context.Context, tx *sql.Tx, projectID string, reason string) error {
	reason = safeSearchIndexReason(reason)
	_, err := tx.ExecContext(ctx, `INSERT INTO project_search_index_state (
		project_id, status, degraded_reason, updated_at
	) VALUES (?, 'degraded', ?, CURRENT_TIMESTAMP)
	ON CONFLICT(project_id) DO UPDATE SET
		status = excluded.status,
		degraded_reason = excluded.degraded_reason,
		updated_at = excluded.updated_at`, projectID, reason)
	return err
}

func (store *SQLiteStore) searchRepairEligibleStates(ctx context.Context, tx *sql.Tx, project projectregistry.Project) ([]FileState, error) {
	if store.searchState != nil {
		present := true
		states, err := store.searchState.ListFileStates(ctx, project.ID, FileStateFilter{
			Status:  FileStatusEligible,
			Present: &present,
		})
		if err != nil {
			return nil, err
		}
		eligible := make([]FileState, 0, len(states))
		for _, state := range states {
			if state.RelativePathSafe && state.ContentSHA256 != "" {
				eligible = append(eligible, state)
			}
		}
		return eligible, nil
	}
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
	pageSize, offset, err := paginationWindow(Pagination{PageSize: options.PageSize, PageToken: options.PageToken})
	if err != nil {
		return TextSearchResultList{}, err
	}
	resultLimit := offset + pageSize + 1
	if options.MaxMatches > 0 && options.MaxMatches < resultLimit {
		resultLimit = options.MaxMatches
	}
	if resultLimit <= offset {
		return TextSearchResultList{Results: []TextSearchResult{}, MaxSnippetBytes: options.MaxSnippetBytes}, nil
	}
	match, usesFTS := ftsLiteralQuery(options.Query)
	if !usesFTS && options.Extension == "" && options.PathPrefix == "" {
		store.recordSearchQuery(project.ID, SearchQueryDiagnostic{RejectedFallbackQueries: 1})
		return TextSearchResultList{}, fmt.Errorf("%w: text search query requires indexed literal syntax or a scoped extension/path prefix", ErrInvalidInput)
	}
	if usesFTS {
		store.recordSearchQuery(project.ID, SearchQueryDiagnostic{FTSQueries: 1})
	} else {
		store.recordSearchQuery(project.ID, SearchQueryDiagnostic{ScopedFallbackQueries: 1})
	}

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
	if usesFTS {
		where = append(where, "project_search_chunks_fts MATCH ?")
		args = append(args, match)
	} else {
		where = append(where, "text LIKE ? ESCAPE '\\'")
		args = append(args, likeContains(options.Query))
	}
	args = append(args, resultLimit)
	rows, err := store.db.QueryContext(ctx, `SELECT
		file_id, chunk_id, relative_path, extension, size_bytes, modified_at, chunk_index, start_line, end_line, byte_start, byte_end, text
		FROM project_search_chunks_fts
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY relative_path ASC, CAST(chunk_index AS INTEGER) ASC, chunk_id ASC
		LIMIT ?`, args...)
	if err != nil {
		return TextSearchResultList{}, sanitizeSearchError(err)
	}
	defer rows.Close()

	results := make([]TextSearchResult, 0)
	var rowsScanned int64
	for rows.Next() {
		rowsScanned++
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
			if len(results) >= resultLimit {
				break
			}
		}
		if len(results) >= resultLimit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return TextSearchResultList{}, sanitizeSearchError(err)
	}
	store.recordSearchQuery(project.ID, SearchQueryDiagnostic{RowsScanned: rowsScanned})
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
	nextToken := ""
	if len(results) > offset+pageSize {
		nextToken = strconv.Itoa(offset + pageSize)
		results = results[:offset+pageSize]
	}
	if offset >= len(results) {
		results = []TextSearchResult{}
	} else {
		results = results[offset:]
	}
	return TextSearchResultList{Results: results, NextPageToken: nextToken, MaxSnippetBytes: options.MaxSnippetBytes}, nil
}

func (store *SQLiteStore) SearchFiles(ctx context.Context, project projectregistry.Project, options FileSearchOptions) (FileList, error) {
	pageSize, offset, err := paginationWindow(Pagination{PageSize: options.PageSize, PageToken: options.PageToken})
	if err != nil {
		return FileList{}, err
	}
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
		store.recordSearchQuery(project.ID, SearchQueryDiagnostic{FTSQueries: 1})
	} else if options.PathContains != "" {
		where = append(where, "relative_path LIKE ? ESCAPE '\\'")
		args = append(args, likeContains(options.PathContains))
		store.recordSearchQuery(project.ID, SearchQueryDiagnostic{ScopedFallbackQueries: 1})
	}
	args = append(args, pageSize+1, offset)
	rows, err := store.db.QueryContext(ctx, `SELECT file_id, relative_path, extension, size_bytes, modified_at
		FROM project_search_files_fts
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY relative_path ASC
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return FileList{}, sanitizeSearchError(err)
	}
	defer rows.Close()
	files := make([]FileMetadata, 0, pageSize+1)
	var rowsScanned int64
	for rows.Next() {
		rowsScanned++
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
	store.recordSearchQuery(project.ID, SearchQueryDiagnostic{RowsScanned: rowsScanned})
	nextToken := ""
	if len(files) > pageSize {
		nextToken = strconv.Itoa(offset + pageSize)
		files = files[:pageSize]
	}
	return FileList{Files: files, NextPageToken: nextToken}, nil
}

func (store *SQLiteStore) SearchSymbols(ctx context.Context, project projectregistry.Project, filter SymbolFilter, pagination Pagination) (SymbolList, error) {
	pageSize, offset, err := paginationWindow(pagination)
	if err != nil {
		return SymbolList{}, err
	}
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
	usesFTS := false
	if match, ok := ftsLiteralQuery(firstNonEmpty(filter.NameContains, filter.NamePrefix, filter.Receiver)); ok {
		where = append(where, "project_search_symbols_fts MATCH ?")
		args = append(args, match)
		usesFTS = true
		store.recordSearchQuery(project.ID, SearchQueryDiagnostic{FTSQueries: 1})
	}
	if canPageSymbolsInSQL(filter, usesFTS) {
		args = append(args, pageSize+1, offset)
		rows, err := store.db.QueryContext(ctx, `SELECT
			symbol_id, file_id, relative_path, extension, kind, name, package, import_path, receiver, start_line, end_line, start_byte, end_byte, start_column, end_column
			FROM project_search_symbols_fts
			WHERE `+strings.Join(where, " AND ")+`
			ORDER BY name ASC, symbol_id ASC
			LIMIT ? OFFSET ?`, args...)
		if err != nil {
			return SymbolList{}, sanitizeSearchError(err)
		}
		defer rows.Close()
		symbols, err := scanSymbolRows(rows, project.ID)
		if err != nil {
			return SymbolList{}, err
		}
		store.recordSearchQuery(project.ID, SearchQueryDiagnostic{RowsScanned: int64(len(symbols))})
		nextToken := ""
		if len(symbols) > pageSize {
			nextToken = strconv.Itoa(offset + pageSize)
			symbols = symbols[:pageSize]
		}
		return SymbolList{Symbols: symbols, NextPageToken: nextToken}, nil
	}
	rows, err := store.db.QueryContext(ctx, `SELECT
		symbol_id, file_id, relative_path, extension, kind, name, package, import_path, receiver, start_line, end_line, start_byte, end_byte, start_column, end_column
		FROM project_search_symbols_fts
		WHERE `+strings.Join(where, " AND "), args...)
	if err != nil {
		return SymbolList{}, sanitizeSearchError(err)
	}
	defer rows.Close()
	var rowsScanned int64
	symbols := make([]SymbolMetadata, 0)
	for rows.Next() {
		rowsScanned++
		var symbol SymbolMetadata
		var startLine, endLine, startByte, endByte, startColumn, endColumn string
		if err := rows.Scan(&symbol.ID, &symbol.FileID, &symbol.RelativePath, &symbol.Extension, &symbol.Kind, &symbol.Name, &symbol.PackageName, &symbol.ImportPath, &symbol.Receiver, &startLine, &endLine, &startByte, &endByte, &startColumn, &endColumn); err != nil {
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
	store.recordSearchQuery(project.ID, SearchQueryDiagnostic{RowsScanned: rowsScanned})
	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].Name == symbols[j].Name {
			return symbols[i].ID < symbols[j].ID
		}
		return symbols[i].Name < symbols[j].Name
	})
	window, nextToken, err := paginate(symbols, Pagination{PageSize: pageSize, PageToken: pagination.PageToken})
	if err != nil {
		return SymbolList{}, err
	}
	return SymbolList{Symbols: window, NextPageToken: nextToken}, nil
}

func (store *SQLiteStore) SearchReferences(ctx context.Context, project projectregistry.Project, options ReferenceSearchOptions) (SymbolReferenceList, error) {
	pageSize, offset, err := paginationWindow(Pagination{PageSize: options.PageSize, PageToken: options.PageToken})
	if err != nil {
		return SymbolReferenceList{}, err
	}
	where := []string{"project_id = ?"}
	args := []any{project.ID}
	addReferenceFilters(&where, &args, options)
	usesContainsFilter := firstNonEmpty(options.NameContains, options.TargetNameContains, options.EnclosingContains) != ""
	if match, ok := ftsLiteralQuery(firstNonEmpty(options.NameContains, options.TargetNameContains, options.EnclosingContains)); ok {
		where = append(where, "project_search_references_fts MATCH ?")
		args = append(args, match)
		store.recordSearchQuery(project.ID, SearchQueryDiagnostic{FTSQueries: 1})
	}
	if !usesContainsFilter {
		args = append(args, pageSize+1, offset)
		rows, err := store.db.QueryContext(ctx, `SELECT
			reference_id, file_id, relative_path, kind, name, target_name, target_symbol_id, package, receiver, import_path,
			enclosing_symbol_id, enclosing_symbol_name, start_line, end_line, start_byte, end_byte, start_column, end_column,
			resolution_status, confidence
			FROM project_search_references_fts
			WHERE `+strings.Join(where, " AND ")+`
			ORDER BY relative_path ASC, CAST(start_line AS INTEGER) ASC, reference_id ASC
			LIMIT ? OFFSET ?`, args...)
		if err != nil {
			return SymbolReferenceList{}, sanitizeSearchError(err)
		}
		defer rows.Close()
		refs, err := scanReferenceRows(rows)
		if err != nil {
			return SymbolReferenceList{}, err
		}
		store.recordSearchQuery(project.ID, SearchQueryDiagnostic{RowsScanned: int64(len(refs))})
		nextToken := ""
		if len(refs) > pageSize {
			nextToken = strconv.Itoa(offset + pageSize)
			refs = refs[:pageSize]
		}
		out := make([]SymbolReferenceMetadata, 0, len(refs))
		for _, row := range refs {
			out = append(out, row.metadata(project.ID))
		}
		return SymbolReferenceList{References: out, NextPageToken: nextToken}, nil
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
	var rowsScanned int64
	refs := make([]referenceSearchRow, 0)
	for rows.Next() {
		rowsScanned++
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
	store.recordSearchQuery(project.ID, SearchQueryDiagnostic{RowsScanned: rowsScanned})
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
	pageSize, offset, err := paginationWindow(Pagination{PageSize: options.PageSize, PageToken: options.PageToken})
	if err != nil {
		return SymbolCallEdgeList{}, err
	}
	where := []string{"project_id = ?"}
	args := []any{project.ID}
	addCallFilters(&where, &args, options)
	usesContainsFilter := firstNonEmpty(options.NameContains, options.CallerNameContains, options.CalleeNameContains) != ""
	if match, ok := ftsLiteralQuery(firstNonEmpty(options.NameContains, options.CallerNameContains, options.CalleeNameContains)); ok {
		where = append(where, "project_search_calls_fts MATCH ?")
		args = append(args, match)
		store.recordSearchQuery(project.ID, SearchQueryDiagnostic{FTSQueries: 1})
	}
	if !usesContainsFilter {
		args = append(args, pageSize+1, offset)
		rows, err := store.db.QueryContext(ctx, `SELECT
			call_id, file_id, relative_path, caller_symbol_id, callee_symbol_id, caller_name, callee_name, receiver, import_path,
			start_line, end_line, start_byte, end_byte, start_column, end_column, resolution_status, confidence
			FROM project_search_calls_fts
			WHERE `+strings.Join(where, " AND ")+`
			ORDER BY relative_path ASC, CAST(start_line AS INTEGER) ASC, call_id ASC
			LIMIT ? OFFSET ?`, args...)
		if err != nil {
			return SymbolCallEdgeList{}, sanitizeSearchError(err)
		}
		defer rows.Close()
		calls, err := scanCallRows(rows)
		if err != nil {
			return SymbolCallEdgeList{}, err
		}
		store.recordSearchQuery(project.ID, SearchQueryDiagnostic{RowsScanned: int64(len(calls))})
		nextToken := ""
		if len(calls) > pageSize {
			nextToken = strconv.Itoa(offset + pageSize)
			calls = calls[:pageSize]
		}
		out := make([]SymbolCallEdge, 0, len(calls))
		for _, row := range calls {
			out = append(out, row.metadata(project.ID))
		}
		return SymbolCallEdgeList{Edges: out, NextPageToken: nextToken}, nil
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
	var rowsScanned int64
	calls := make([]callSearchRow, 0)
	for rows.Next() {
		rowsScanned++
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
	store.recordSearchQuery(project.ID, SearchQueryDiagnostic{RowsScanned: rowsScanned})
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

func canPageSymbolsInSQL(filter SymbolFilter, usesFTS bool) bool {
	if usesFTS {
		return false
	}
	return filter.NamePrefix == "" && filter.NameContains == "" && filter.Receiver == ""
}

func scanSymbolRows(rows *sql.Rows, projectID string) ([]SymbolMetadata, error) {
	symbols := make([]SymbolMetadata, 0)
	for rows.Next() {
		var symbol SymbolMetadata
		var startLine, endLine, startByte, endByte, startColumn, endColumn string
		if err := rows.Scan(&symbol.ID, &symbol.FileID, &symbol.RelativePath, &symbol.Extension, &symbol.Kind, &symbol.Name, &symbol.PackageName, &symbol.ImportPath, &symbol.Receiver, &startLine, &endLine, &startByte, &endByte, &startColumn, &endColumn); err != nil {
			return nil, sanitizeSearchError(err)
		}
		symbol.ProjectID = projectID
		symbol.StartLine = atoiDefault(startLine)
		symbol.EndLine = atoiDefault(endLine)
		symbol.StartByte = atoiDefault(startByte)
		symbol.EndByte = atoiDefault(endByte)
		symbol.StartColumn = atoiDefault(startColumn)
		symbol.EndColumn = atoiDefault(endColumn)
		symbols = append(symbols, symbol)
	}
	if err := rows.Err(); err != nil {
		return nil, sanitizeSearchError(err)
	}
	return symbols, nil
}

func scanReferenceRows(rows *sql.Rows) ([]referenceSearchRow, error) {
	refs := make([]referenceSearchRow, 0)
	for rows.Next() {
		var ref referenceSearchRow
		if err := rows.Scan(&ref.ID, &ref.FileID, &ref.RelativePath, &ref.Kind, &ref.Name, &ref.TargetName, &ref.TargetSymbolID, &ref.PackageName,
			&ref.Receiver, &ref.ImportPath, &ref.EnclosingSymbolID, &ref.EnclosingSymbolName, &ref.StartLine, &ref.EndLine,
			&ref.StartByte, &ref.EndByte, &ref.StartColumn, &ref.EndColumn, &ref.ResolutionStatus, &ref.Confidence); err != nil {
			return nil, sanitizeSearchError(err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, sanitizeSearchError(err)
	}
	return refs, nil
}

func scanCallRows(rows *sql.Rows) ([]callSearchRow, error) {
	calls := make([]callSearchRow, 0)
	for rows.Next() {
		var call callSearchRow
		if err := rows.Scan(&call.ID, &call.FileID, &call.RelativePath, &call.CallerSymbolID, &call.CalleeSymbolID, &call.CallerName,
			&call.CalleeName, &call.Receiver, &call.ImportPath, &call.StartLine, &call.EndLine, &call.StartByte, &call.EndByte,
			&call.StartColumn, &call.EndColumn, &call.ResolutionStatus, &call.Confidence); err != nil {
			return nil, sanitizeSearchError(err)
		}
		calls = append(calls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, sanitizeSearchError(err)
	}
	return calls, nil
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
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`, true
}

func likePrefix(value string) string {
	return escapeLike(value) + "%"
}

func likeContains(value string) string {
	return "%" + escapeLike(value) + "%"
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
