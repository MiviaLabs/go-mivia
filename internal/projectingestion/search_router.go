package projectingestion

import (
	"context"
	"fmt"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

type SearchStoreBackend struct {
	ProjectID  string
	Store      any
	StorageKey string
}

type SearchStorageDiagnostic struct {
	ProjectID  string                 `json:"project_id"`
	Backend    string                 `json:"backend"`
	StorageKey string                 `json:"storage_key,omitempty"`
	Write      *SearchWriteDiagnostic `json:"write,omitempty"`
}

type SearchWriteDiagnostic struct {
	TransactionCount  int64                 `json:"transaction_count"`
	MaxWriteWeight    int                   `json:"max_write_weight,omitempty"`
	RowsInserted      map[string]int64      `json:"rows_inserted,omitempty"`
	DeleteStatements  map[string]int64      `json:"delete_statements,omitempty"`
	FTSRewriteSkipped int64                 `json:"fts_rewrite_skipped,omitempty"`
	Query             SearchQueryDiagnostic `json:"query,omitempty"`
}

type SearchQueryDiagnostic struct {
	FTSQueries              int64 `json:"fts_queries,omitempty"`
	ScopedFallbackQueries   int64 `json:"scoped_fallback_queries,omitempty"`
	RejectedFallbackQueries int64 `json:"rejected_fallback_queries,omitempty"`
	RowsScanned             int64 `json:"rows_scanned,omitempty"`
}

type SearchStoreRouter struct {
	registry            *projectregistry.Registry
	fallback            any
	storeByProject      map[string]any
	storageKeyByProject map[string]string
}

func NewProjectScopedSearchStoreRouter(registry *projectregistry.Registry, fallback any, backends []SearchStoreBackend) *SearchStoreRouter {
	router := &SearchStoreRouter{
		registry:            registry,
		fallback:            fallback,
		storeByProject:      make(map[string]any, len(backends)),
		storageKeyByProject: make(map[string]string, len(backends)),
	}
	for _, backend := range backends {
		projectID := strings.TrimSpace(backend.ProjectID)
		if projectID == "" || backend.Store == nil {
			continue
		}
		router.storeByProject[projectID] = backend.Store
		router.storageKeyByProject[projectID] = strings.TrimSpace(backend.StorageKey)
	}
	return router
}

func (router *SearchStoreRouter) UpsertSearchFile(ctx context.Context, project projectregistry.Project, state FileState, chunks []Chunk, symbols []Symbol, references []Reference, calls []Call) error {
	store, err := router.mutationStore(project.ID)
	if err != nil {
		return err
	}
	return store.UpsertSearchFile(ctx, project, state, chunks, symbols, references, calls)
}

func (router *SearchStoreRouter) UpsertSearchFilesBatch(ctx context.Context, project projectregistry.Project, files []PreparedSearchFile) error {
	store, err := router.mutationStore(project.ID)
	if err != nil {
		return err
	}
	return store.UpsertSearchFilesBatch(ctx, project, files)
}

func (router *SearchStoreRouter) ApplySearchFileBatch(ctx context.Context, project projectregistry.Project, results []fullScanFileResult) error {
	store, err := router.batchMutationStore(project.ID)
	if err != nil {
		return err
	}
	return store.ApplySearchFileBatch(ctx, project, results)
}

func (router *SearchStoreRouter) DeleteSearchFile(ctx context.Context, projectID string, fileID string) error {
	store, err := router.mutationStore(projectID)
	if err != nil {
		return err
	}
	return store.DeleteSearchFile(ctx, projectID, fileID)
}

func (router *SearchStoreRouter) DeleteSearchProject(ctx context.Context, projectID string) error {
	store, err := router.mutationStore(projectID)
	if err != nil {
		return err
	}
	return store.DeleteSearchProject(ctx, projectID)
}

func (router *SearchStoreRouter) MarkSearchIndexDegraded(ctx context.Context, projectID string, reason string) error {
	store, err := router.mutationStore(projectID)
	if err != nil {
		return err
	}
	return store.MarkSearchIndexDegraded(ctx, projectID, reason)
}

func (router *SearchStoreRouter) ClearSearchIndexDegraded(ctx context.Context, projectID string) error {
	store, err := router.mutationStore(projectID)
	if err != nil {
		return err
	}
	return store.ClearSearchIndexDegraded(ctx, projectID)
}

func (router *SearchStoreRouter) HasSearchFileVersion(ctx context.Context, project projectregistry.Project, state FileState) (bool, error) {
	store, err := router.mutationStore(project.ID)
	if err != nil {
		return false, err
	}
	return store.HasSearchFileVersion(ctx, project, state)
}

func (router *SearchStoreRouter) UpdateSearchFileMetadata(ctx context.Context, project projectregistry.Project, state FileState) error {
	store, err := router.mutationStore(project.ID)
	if err != nil {
		return err
	}
	return store.UpdateSearchFileMetadata(ctx, project, state)
}

func (router *SearchStoreRouter) UpdateSearchFileMetadataBatch(ctx context.Context, project projectregistry.Project, states []FileState) error {
	store, err := router.mutationStore(project.ID)
	if err != nil {
		return err
	}
	return store.UpdateSearchFileMetadataBatch(ctx, project, states)
}

func (router *SearchStoreRouter) ReconcileSearchIndex(ctx context.Context, project projectregistry.Project) ([]FileState, error) {
	store, err := router.repairStore(project.ID)
	if err != nil {
		return nil, err
	}
	return store.ReconcileSearchIndex(ctx, project)
}

func (router *SearchStoreRouter) SearchText(ctx context.Context, project projectregistry.Project, options TextSearchOptions) (TextSearchResultList, error) {
	store, err := router.queryStore(project.ID)
	if err != nil {
		return TextSearchResultList{}, err
	}
	return store.SearchText(ctx, project, options)
}

func (router *SearchStoreRouter) SearchFiles(ctx context.Context, project projectregistry.Project, options FileSearchOptions) (FileList, error) {
	store, err := router.queryStore(project.ID)
	if err != nil {
		return FileList{}, err
	}
	return store.SearchFiles(ctx, project, options)
}

func (router *SearchStoreRouter) SearchSymbols(ctx context.Context, project projectregistry.Project, filter SymbolFilter, pagination Pagination) (SymbolList, error) {
	store, err := router.queryStore(project.ID)
	if err != nil {
		return SymbolList{}, err
	}
	return store.SearchSymbols(ctx, project, filter, pagination)
}

func (router *SearchStoreRouter) SearchReferences(ctx context.Context, project projectregistry.Project, options ReferenceSearchOptions) (SymbolReferenceList, error) {
	store, err := router.queryStore(project.ID)
	if err != nil {
		return SymbolReferenceList{}, err
	}
	return store.SearchReferences(ctx, project, options)
}

func (router *SearchStoreRouter) SearchCalls(ctx context.Context, project projectregistry.Project, options ReferenceSearchOptions) (SymbolCallEdgeList, error) {
	store, err := router.queryStore(project.ID)
	if err != nil {
		return SymbolCallEdgeList{}, err
	}
	return store.SearchCalls(ctx, project, options)
}

func (router *SearchStoreRouter) SearchIndexHealth(ctx context.Context, project projectregistry.Project) (SearchIndexHealth, error) {
	store, err := router.queryStore(project.ID)
	if err != nil {
		return SearchIndexHealth{}, err
	}
	return store.SearchIndexHealth(ctx, project)
}

func (router *SearchStoreRouter) CountSearchSymbols(ctx context.Context, project projectregistry.Project) (int, error) {
	store, err := router.counterStore(project.ID)
	if err != nil {
		return 0, err
	}
	return store.CountSearchSymbols(ctx, project)
}

func (router *SearchStoreRouter) CountSearchChunks(ctx context.Context, project projectregistry.Project) (int, error) {
	store, err := router.counterStore(project.ID)
	if err != nil {
		return 0, err
	}
	return store.CountSearchChunks(ctx, project)
}

func (router *SearchStoreRouter) ContextSearchIndexHealth(ctx context.Context, project projectregistry.Project) (SearchIndexHealth, error) {
	store, err := router.counterStore(project.ID)
	if err != nil {
		return SearchIndexHealth{}, err
	}
	return store.ContextSearchIndexHealth(ctx, project)
}

func (router *SearchStoreRouter) SearchStorageDiagnostics() []SearchStorageDiagnostic {
	if router == nil || router.registry == nil {
		return nil
	}
	diagnostics := make([]SearchStorageDiagnostic, 0, len(router.registry.List()))
	for _, project := range router.registry.List() {
		diagnostic := SearchStorageDiagnostic{ProjectID: project.ID, Backend: "shared"}
		if storageKey := strings.TrimSpace(router.storageKeyByProject[project.ID]); storageKey != "" {
			diagnostic.Backend = "persistent_project"
			diagnostic.StorageKey = storageKey
		}
		if provider, ok := router.storeForProjectOrFallback(project.ID).(interface {
			SearchWriteDiagnostics(string) SearchWriteDiagnostic
		}); ok {
			write := provider.SearchWriteDiagnostics(project.ID)
			if write.TransactionCount > 0 {
				diagnostic.Write = &write
			}
		}
		diagnostics = append(diagnostics, diagnostic)
	}
	return diagnostics
}

func (router *SearchStoreRouter) storeForProjectOrFallback(projectID string) any {
	if router == nil {
		return nil
	}
	if store := router.storeByProject[projectID]; store != nil {
		return store
	}
	return router.fallback
}

func (router *SearchStoreRouter) mutationStore(projectID string) (searchMutationStore, error) {
	store, err := router.storeForProject(projectID)
	if err != nil {
		return nil, err
	}
	typed, ok := store.(searchMutationStore)
	if !ok {
		return nil, fmt.Errorf("%w: search mutation store is required", ErrUnsupportedIngest)
	}
	return typed, nil
}

func (router *SearchStoreRouter) batchMutationStore(projectID string) (searchBatchMutationStore, error) {
	store, err := router.storeForProject(projectID)
	if err != nil {
		return nil, err
	}
	typed, ok := store.(searchBatchMutationStore)
	if !ok {
		return nil, fmt.Errorf("%w: search batch mutation store is required", ErrUnsupportedIngest)
	}
	return typed, nil
}

func (router *SearchStoreRouter) repairStore(projectID string) (searchRepairStore, error) {
	store, err := router.storeForProject(projectID)
	if err != nil {
		return nil, err
	}
	typed, ok := store.(searchRepairStore)
	if !ok {
		return nil, fmt.Errorf("%w: search repair store is required", ErrUnsupportedIngest)
	}
	return typed, nil
}

func (router *SearchStoreRouter) queryStore(projectID string) (searchQueryStore, error) {
	store, err := router.storeForProject(projectID)
	if err != nil {
		return nil, err
	}
	typed, ok := store.(searchQueryStore)
	if !ok {
		return nil, fmt.Errorf("%w: search query store is required", ErrUnsupportedIngest)
	}
	return typed, nil
}

func (router *SearchStoreRouter) counterStore(projectID string) (searchCounter, error) {
	store, err := router.storeForProject(projectID)
	if err != nil {
		return nil, err
	}
	typed, ok := store.(searchCounter)
	if !ok {
		return nil, fmt.Errorf("%w: search counter store is required", ErrUnsupportedIngest)
	}
	return typed, nil
}

func (router *SearchStoreRouter) storeForProject(projectID string) (any, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		if _, ok := router.registry.Get(projectID); !ok {
			return nil, ErrProjectNotFound
		}
		if store := router.storeByProject[projectID]; store != nil {
			return store, nil
		}
	}
	if router.fallback == nil {
		return nil, fmt.Errorf("%w: search store is required", ErrUnsupportedIngest)
	}
	return router.fallback, nil
}
