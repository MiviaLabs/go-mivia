package projectingestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

var (
	ErrProjectNotFound     = projectregistry.ErrProjectNotFound
	ErrInvalidInput        = projectregistry.ErrInvalidInput
	ErrProjectDisabled     = errors.New("ingestion project disabled")
	ErrUnsupportedIngest   = errors.New("ingestion unsupported")
	ErrIngestionNotFound   = errors.New("ingestion resource not found")
	ErrPathEscapesRoot     = errors.New("path escapes project root")
	ErrPathNotProjectLocal = errors.New("path must be project-relative")
)

type stateStore interface {
	SaveRun(context.Context, Run) error
	GetRun(context.Context, string, string) (Run, error)
	ListLatestRuns(context.Context, string, int) ([]Run, error)
	SaveFileState(context.Context, FileState) error
	ListFileStates(context.Context, string, FileStateFilter) ([]FileState, error)
	ListFileStatesPage(context.Context, string, FileStateFilter, Pagination) ([]FileState, string, error)
	GetFileStateByHash(context.Context, string, string) (FileState, error)
	GetExtractorCache(context.Context, string, string, string, string, string) (ExtractorCacheEntry, error)
	SaveExtractorCache(context.Context, ExtractorCacheEntry) error
	DeleteExtractorCacheForFile(context.Context, string, string) error
}

type API interface {
	IngestProject(context.Context, string, Trigger) (Run, error)
	IngestPath(context.Context, string, string, Trigger) (Run, error)
	SubmitIngestProject(context.Context, string, Trigger) (Run, error)
	RunMetadata(context.Context, string, string) (RunMetadata, error)
	LatestRunMetadata(context.Context, string) (RunMetadata, error)
	ListFiles(context.Context, string, FileStateFilter, Pagination) (FileList, error)
	GetFile(context.Context, string, string) (FileMetadata, error)
	ListChunks(context.Context, string, string, Pagination, int) (ChunkList, error)
	GetChunk(context.Context, string, string, string, int) (ChunkMetadata, error)
	ListSymbols(context.Context, string, SymbolFilter, Pagination) (SymbolList, error)
	GetSymbol(context.Context, string, string) (SymbolMetadata, error)
	ListHeadings(context.Context, string, string, Pagination) (HeadingList, error)
	GetFileOutline(context.Context, string, string, FileOutlineOptions) (FileOutline, error)
}

type Service struct {
	registry              *projectregistry.Registry
	graph                 *GraphStore
	state                 stateStore
	extractors            *ExtractorRegistry
	extractorCacheEnabled bool
	fullScanBatchSize     int
	now                   func() time.Time
	newID                 func(projectregistry.Project, time.Time) string
}

func NewService(registry *projectregistry.Registry, graph *GraphStore, state stateStore) *Service {
	return &Service{
		registry:              registry,
		graph:                 graph,
		state:                 state,
		extractors:            NewDefaultExtractorRegistry(),
		extractorCacheEnabled: true,
		fullScanBatchSize:     500,
		now:                   func() time.Time { return time.Now().UTC() },
		newID:                 defaultRunID,
	}
}

func (svc *Service) withGraphBatch(ctx context.Context, fn func(*Service) (Run, error)) (Run, error) {
	if svc == nil || svc.graph == nil {
		return fn(svc)
	}
	var run Run
	err := svc.graph.WithBatch(ctx, func(graph *GraphStore) error {
		batched := *svc
		batched.graph = graph
		var innerErr error
		run, innerErr = fn(&batched)
		return innerErr
	})
	return run, err
}

func (svc *Service) IngestProject(ctx context.Context, projectID string, trigger Trigger) (Run, error) {
	project, err := svc.projectForIngestion(projectID, normalizeTrigger(trigger))
	if err != nil {
		return Run{}, err
	}
	run := svc.startRun(project, trigger)
	run.Status = RunStatusRunning
	if err := svc.persistRun(ctx, project, run); err != nil {
		return run, err
	}
	return svc.executeProjectRun(ctx, project, run)
}

func (svc *Service) SubmitIngestProject(ctx context.Context, projectID string, trigger Trigger) (Run, error) {
	return Run{}, fmt.Errorf("%w: manual ingestion submission requires scheduler", ErrUnsupportedIngest)
}

func (svc *Service) SetFullScanBatchSize(size int) {
	if size > 0 {
		svc.fullScanBatchSize = size
	}
}

func (svc *Service) SetExtractorCacheEnabled(enabled bool) {
	svc.extractorCacheEnabled = enabled
}

func (svc *Service) PrepareProjectRun(ctx context.Context, projectID string, trigger Trigger) (Run, error) {
	trigger = normalizeTrigger(trigger)
	project, err := svc.projectForIngestion(projectID, trigger)
	if err != nil {
		return Run{}, err
	}
	run := svc.startRun(project, trigger)
	run.Status = RunStatusPending
	if err := svc.persistRun(ctx, project, run); err != nil {
		return run, err
	}
	return run, nil
}

func (svc *Service) ExecutePreparedProjectRun(ctx context.Context, run Run) (Run, error) {
	if strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.ProjectID) == "" {
		return Run{}, ErrInvalidInput
	}
	project, err := svc.projectForIngestion(run.ProjectID, normalizeTrigger(run.Trigger))
	if err != nil {
		return run, err
	}
	run.ProjectID = project.ID
	run.Trigger = normalizeTrigger(run.Trigger)
	run.Mode = project.DigestMode
	run.Status = RunStatusRunning
	if run.StartedAt.IsZero() {
		run.StartedAt = svc.now().UTC()
	}
	if err := svc.persistRun(ctx, project, run); err != nil {
		return run, err
	}
	return svc.executeProjectRun(ctx, project, run)
}

func (svc *Service) FailPreparedProjectRun(ctx context.Context, run Run, errorCategory string) (Run, error) {
	project, err := svc.projectForIngestion(run.ProjectID, normalizeTrigger(run.Trigger))
	if err != nil {
		return run, err
	}
	run.Status = RunStatusFailed
	run.ErrorCategory = strings.TrimSpace(errorCategory)
	if run.ErrorCategory == "" {
		run.ErrorCategory = "ingest_failed"
	}
	run.FinishedAt = svc.now().UTC()
	return run, svc.persistRun(ctx, project, run)
}

func (svc *Service) executeProjectRun(ctx context.Context, project projectregistry.Project, run Run) (Run, error) {
	seen := make(map[string]struct{})
	root := project.CanonicalRootPath
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if filePath == root {
				return fmt.Errorf("walk failed for project root")
			}
			relative, ok := safeRelativePath(root, filePath)
			if !ok {
				return ErrPathEscapesRoot
			}
			run.FilesSkipped++
			state := svc.skippedState(project, relative, SkipReasonStatError, 0, time.Time{}, true, run.StartedAt)
			seen[state.RelativePathHash] = struct{}{}
			recordRunReason(&run, state.SkippedReason)
			return svc.saveSkipped(ctx, project, run, state, false)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if filePath == root {
			return nil
		}
		relative, ok := safeRelativePath(root, filePath)
		if !ok {
			return ErrPathEscapesRoot
		}
		if entry.Type()&os.ModeSymlink != 0 {
			run.FilesSkipped++
			state := svc.skippedState(project, relative, SkipReasonUnsafePath, 0, time.Time{}, true, run.StartedAt)
			seen[state.RelativePathHash] = struct{}{}
			recordRunReason(&run, state.SkippedReason)
			return svc.saveSkipped(ctx, project, run, state, entry.IsDir())
		}
		if entry.IsDir() {
			if projectregistry.ProjectExcludesRelativePath(project, relative) {
				return filepath.SkipDir
			}
			if !projectregistry.ProjectMayIncludeRelativePath(project, relative) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			run.FilesSkipped++
			state := svc.skippedState(project, relative, SkipReasonStatError, 0, time.Time{}, true, run.StartedAt)
			seen[state.RelativePathHash] = struct{}{}
			recordRunReason(&run, state.SkippedReason)
			return svc.saveSkipped(ctx, project, run, state, false)
		}
		if !info.Mode().IsRegular() {
			run.FilesSkipped++
			state := svc.skippedState(project, relative, SkipReasonUnsafePath, info.Size(), info.ModTime().UTC(), true, run.StartedAt)
			seen[state.RelativePathHash] = struct{}{}
			recordRunReason(&run, state.SkippedReason)
			return svc.saveSkipped(ctx, project, run, state, false)
		}
		if !projectregistry.ProjectIncludesRelativePath(project, relative) {
			run.FilesSkipped++
			state := svc.skippedState(project, relative, SkipReasonDeniedPath, info.Size(), info.ModTime().UTC(), true, run.StartedAt)
			seen[state.RelativePathHash] = struct{}{}
			recordRunReason(&run, state.SkippedReason)
			return svc.saveSkipped(ctx, project, run, state, false)
		}
		state, chunks, symbols, _, err := svc.ingestExistingFile(ctx, project, relative, filePath, info, run)
		seen[state.RelativePathHash] = struct{}{}
		run.FilesSeen++
		if state.Status == FileStatusEligible {
			run.FilesIngested++
			run.ChunksStored += len(chunks)
			run.SymbolsStored += len(symbols)
		} else {
			run.FilesSkipped++
		}
		recordRunReason(&run, state.SkippedReason)
		return err
	})
	if err != nil {
		run.Status = RunStatusFailed
		run.ErrorCategory = "walk_failed"
		run.FinishedAt = svc.now().UTC()
		_ = svc.persistRun(ctx, project, run)
		return run, err
	}
	if err := svc.tombstoneMissingFiles(ctx, project, run, seen); err != nil {
		run.Status = RunStatusFailed
		run.ErrorCategory = "tombstone_failed"
		run.FinishedAt = svc.now().UTC()
		_ = svc.persistRun(ctx, project, run)
		return run, err
	}
	run.Status = RunStatusCompleted
	run.FinishedAt = svc.now().UTC()
	if err := svc.persistRun(ctx, project, run); err != nil {
		return run, err
	}
	return run, nil
}

func (svc *Service) IngestPath(ctx context.Context, projectID string, relativePath string, trigger Trigger) (Run, error) {
	return svc.withGraphBatch(ctx, func(svc *Service) (Run, error) {
		return svc.ingestPath(ctx, projectID, relativePath, trigger)
	})
}

func (svc *Service) ingestPath(ctx context.Context, projectID string, relativePath string, trigger Trigger) (Run, error) {
	trigger = normalizeTrigger(trigger)
	project, err := svc.projectForIngestion(projectID, trigger)
	if err != nil {
		return Run{}, err
	}
	relative, ok := normalizeProjectRelativePath(relativePath)
	if !ok {
		return Run{}, ErrPathNotProjectLocal
	}
	fullPath := filepath.Join(project.CanonicalRootPath, filepath.FromSlash(relative))
	checkedRelative, ok := safeRelativePath(project.CanonicalRootPath, fullPath)
	if !ok || checkedRelative != relative {
		return Run{}, ErrPathEscapesRoot
	}

	run := svc.startRun(project, trigger)
	if err := svc.persistRun(ctx, project, run); err != nil {
		return run, err
	}
	info, err := os.Lstat(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		state := FileState{
			ProjectID:        project.ID,
			RelativePathHash: hashValue(relative),
			RelativePath:     relative,
			RelativePathSafe: true,
			Status:           FileStatusAbsent,
			Present:          false,
			LastEventAt:      run.StartedAt,
			LastIngestedAt:   run.StartedAt,
		}
		if err := svc.state.SaveFileState(ctx, state); err != nil {
			return run, err
		}
		if err := svc.state.DeleteExtractorCacheForFile(ctx, project.ID, state.RelativePathHash); err != nil {
			return run, err
		}
		if err := svc.graph.PutFileState(ctx, project, run, state); err != nil {
			return run, err
		}
		run.Status = RunStatusCompleted
		run.FinishedAt = svc.now().UTC()
		return run, svc.persistRun(ctx, project, run)
	}
	if err != nil {
		run.Status = RunStatusFailed
		run.ErrorCategory = "stat_failed"
		run.FinishedAt = svc.now().UTC()
		_ = svc.persistRun(ctx, project, run)
		return run, fmt.Errorf("stat failed for relative path %q", relative)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() || !info.Mode().IsRegular() || !projectregistry.ProjectIncludesRelativePath(project, relative) {
		reason := SkipReasonUnsafePath
		if !projectregistry.ProjectIncludesRelativePath(project, relative) {
			reason = SkipReasonDeniedPath
		}
		state := svc.skippedState(project, relative, reason, info.Size(), info.ModTime().UTC(), true, run.StartedAt)
		run.FilesSkipped = 1
		recordRunReason(&run, state.SkippedReason)
		if err := svc.saveSkipped(ctx, project, run, state, false); err != nil {
			return run, err
		}
		run.Status = RunStatusCompleted
		run.FinishedAt = svc.now().UTC()
		return run, svc.persistRun(ctx, project, run)
	}
	state, chunks, symbols, _, err := svc.ingestExistingFile(ctx, project, relative, fullPath, info, run)
	if err != nil {
		run.Status = RunStatusFailed
		run.ErrorCategory = "ingest_failed"
		run.FinishedAt = svc.now().UTC()
		_ = svc.persistRun(ctx, project, run)
		return run, err
	}
	if state.Status == FileStatusEligible {
		run.FilesSeen = 1
		run.FilesIngested = 1
		run.ChunksStored = len(chunks)
		run.SymbolsStored = len(symbols)
	} else {
		run.FilesSeen = 1
		run.FilesSkipped = 1
		recordRunReason(&run, state.SkippedReason)
	}
	run.Status = RunStatusCompleted
	run.FinishedAt = svc.now().UTC()
	return run, svc.persistRun(ctx, project, run)
}

func (svc *Service) GetRun(ctx context.Context, projectID string, runID string) (Run, error) {
	return svc.state.GetRun(ctx, strings.TrimSpace(projectID), strings.TrimSpace(runID))
}

func (svc *Service) RunMetadata(ctx context.Context, projectID string, runID string) (RunMetadata, error) {
	run, err := svc.GetRun(ctx, projectID, runID)
	if err != nil {
		return RunMetadata{}, err
	}
	return MetadataForRun(run), nil
}

func (svc *Service) LatestRunMetadata(ctx context.Context, projectID string) (RunMetadata, error) {
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return RunMetadata{}, err
	}
	runs, err := svc.state.ListLatestRuns(ctx, project.ID, 1)
	if err != nil {
		return RunMetadata{}, err
	}
	if len(runs) == 0 {
		return RunMetadata{}, ErrRunNotFound
	}
	return MetadataForRun(runs[0]), nil
}

func (svc *Service) ListFileStates(ctx context.Context, projectID string, filter FileStateFilter) ([]FileState, error) {
	return svc.state.ListFileStates(ctx, strings.TrimSpace(projectID), filter)
}

func (svc *Service) ListFiles(ctx context.Context, projectID string, filter FileStateFilter, pagination Pagination) (FileList, error) {
	if filter.Extension != "" {
		normalized, err := NormalizeFileExtension(filter.Extension)
		if err != nil {
			return FileList{}, err
		}
		filter.Extension = normalized
	}
	if filter.PathPrefix != "" {
		normalized, err := NormalizePathPrefix(filter.PathPrefix)
		if err != nil {
			return FileList{}, err
		}
		filter.PathPrefix = normalized
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return FileList{}, err
	}
	states, nextToken, err := svc.state.ListFileStatesPage(ctx, project.ID, filter, pagination)
	if err != nil {
		return FileList{}, err
	}
	files := make([]FileMetadata, 0, len(states))
	for _, state := range states {
		files = append(files, MetadataForFileState(project, state))
	}
	return FileList{Files: files, NextPageToken: nextToken}, nil
}

func NormalizePathPrefix(raw string) (string, error) {
	prefix := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if prefix == "" {
		return "", nil
	}
	if strings.HasPrefix(prefix, "/") || strings.Contains(prefix, "\x00") {
		return "", ErrInvalidInput
	}
	cleaned := path.Clean(prefix)
	if cleaned == "." {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrInvalidInput
	}
	if strings.HasSuffix(prefix, "/") && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned, nil
}

func NormalizeFileExtension(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.ContainsAny(raw, `/\`) {
		return "", ErrInvalidInput
	}
	if strings.ContainsFunc(raw, unicode.IsSpace) {
		return "", ErrInvalidInput
	}
	extension := strings.ToLower(raw)
	if !strings.HasPrefix(extension, ".") {
		extension = "." + extension
	}
	if extension == "." {
		return "", ErrInvalidInput
	}
	for _, value := range extension[1:] {
		if !isExtensionChar(value) {
			return "", ErrInvalidInput
		}
	}
	return extension, nil
}

func isExtensionChar(value rune) bool {
	return utf8.RuneLen(value) == 1 && (value >= 'a' && value <= 'z' || value >= '0' && value <= '9')
}

func (svc *Service) GetFile(ctx context.Context, projectID string, fileID string) (FileMetadata, error) {
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return FileMetadata{}, err
	}
	if !validOpaqueID(fileID) {
		return FileMetadata{}, ErrInvalidInput
	}
	prefix := project.GraphNamespace + ":"
	if !strings.HasPrefix(fileID, prefix) {
		return FileMetadata{}, ErrIngestionNotFound
	}
	state, err := svc.state.GetFileStateByHash(ctx, project.ID, strings.TrimPrefix(fileID, prefix))
	if err != nil {
		return FileMetadata{}, err
	}
	return MetadataForFileState(project, state), nil
}

func (svc *Service) ListChunks(ctx context.Context, projectID string, fileID string, pagination Pagination, maxChunkBytes int) (ChunkList, error) {
	if maxChunkBytes < 0 {
		return ChunkList{}, ErrInvalidInput
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return ChunkList{}, err
	}
	return svc.graph.ListChunks(ctx, project, fileID, pagination, effectiveMaxChunkBytes(project, maxChunkBytes))
}

func (svc *Service) GetChunk(ctx context.Context, projectID string, fileID string, chunkID string, maxChunkBytes int) (ChunkMetadata, error) {
	if maxChunkBytes < 0 {
		return ChunkMetadata{}, ErrInvalidInput
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return ChunkMetadata{}, err
	}
	return svc.graph.GetChunk(ctx, project, fileID, chunkID, effectiveMaxChunkBytes(project, maxChunkBytes))
}

func (svc *Service) ListSymbols(ctx context.Context, projectID string, filter SymbolFilter, pagination Pagination) (SymbolList, error) {
	normalized, err := NormalizeSymbolFilter(filter)
	if err != nil {
		return SymbolList{}, err
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return SymbolList{}, err
	}
	return svc.graph.ListSymbols(ctx, project, normalized, pagination)
}

func (svc *Service) ListHeadings(ctx context.Context, projectID string, fileID string, pagination Pagination) (HeadingList, error) {
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return HeadingList{}, err
	}
	return svc.graph.ListHeadings(ctx, project, strings.TrimSpace(fileID), pagination)
}

func (svc *Service) GetFileOutline(ctx context.Context, projectID string, fileID string, options FileOutlineOptions) (FileOutline, error) {
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return FileOutline{}, err
	}
	if !validOpaqueID(fileID) {
		return FileOutline{}, ErrInvalidInput
	}
	normalized, err := NormalizeSymbolFilter(options.SymbolFilter)
	if err != nil {
		return FileOutline{}, err
	}
	options.SymbolFilter = normalized
	prefix := project.GraphNamespace + ":"
	if !strings.HasPrefix(fileID, prefix) {
		return FileOutline{}, ErrIngestionNotFound
	}
	return svc.graph.GetFileOutline(ctx, project, strings.TrimSpace(fileID), options)
}

func NormalizeSymbolFilter(filter SymbolFilter) (SymbolFilter, error) {
	filter.NamePrefix = strings.TrimSpace(filter.NamePrefix)
	filter.FileID = strings.TrimSpace(filter.FileID)
	filter.Package = strings.TrimSpace(filter.Package)
	if filter.Extension != "" {
		extension, err := NormalizeFileExtension(filter.Extension)
		if err != nil {
			return SymbolFilter{}, err
		}
		filter.Extension = extension
	}
	if filter.Kind != "" && !validSymbolKind(filter.Kind) {
		return SymbolFilter{}, ErrInvalidInput
	}
	if filter.NamePrefix != "" && strings.Contains(filter.NamePrefix, "\x00") {
		return SymbolFilter{}, ErrInvalidInput
	}
	if filter.FileID != "" && !validOpaqueID(filter.FileID) {
		return SymbolFilter{}, ErrInvalidInput
	}
	if filter.Package != "" && strings.ContainsAny(filter.Package, "\x00/\\") {
		return SymbolFilter{}, ErrInvalidInput
	}
	return filter, nil
}

func validSymbolKind(kind SymbolKind) bool {
	switch kind {
	case SymbolKindPackage, SymbolKindImport, SymbolKindFunction, SymbolKindMethod, SymbolKindType,
		SymbolKindClass, SymbolKindExport, SymbolKindStage, SymbolKindTarget, SymbolKindPath,
		SymbolKindKey, SymbolKindMigration:
		return true
	default:
		return false
	}
}

func (svc *Service) GetSymbol(ctx context.Context, projectID string, symbolID string) (SymbolMetadata, error) {
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return SymbolMetadata{}, err
	}
	return svc.graph.GetSymbol(ctx, project, symbolID)
}

func (svc *Service) projectForIngestion(projectID string, trigger Trigger) (projectregistry.Project, error) {
	if svc == nil || svc.registry == nil || svc.graph == nil || svc.state == nil {
		return projectregistry.Project{}, fmt.Errorf("%w: service dependencies are required", ErrUnsupportedIngest)
	}
	project, ok := svc.registry.Get(strings.TrimSpace(projectID))
	if !ok {
		return projectregistry.Project{}, ErrProjectNotFound
	}
	if !project.Enabled {
		return projectregistry.Project{}, ErrProjectDisabled
	}
	if project.DigestMode != projectregistry.DigestModeContentGraph {
		return projectregistry.Project{}, fmt.Errorf("%w: digest_mode must be %q", ErrUnsupportedIngest, projectregistry.DigestModeContentGraph)
	}
	switch trigger {
	case TriggerManual:
	case TriggerLive:
		if project.UpdatePolicy != projectregistry.UpdatePolicyLive {
			return projectregistry.Project{}, fmt.Errorf("%w: update_policy must be %q for live ingestion", ErrUnsupportedIngest, projectregistry.UpdatePolicyLive)
		}
	default:
		return projectregistry.Project{}, ErrInvalidInput
	}
	root := project.CanonicalRootPath
	if root == "" {
		root = project.RootPath
	}
	cleanRoot, canonicalRoot, err := validateCanonicalRoot(root)
	if err != nil {
		return projectregistry.Project{}, fmt.Errorf("%w: invalid root path", ErrUnsupportedIngest)
	}
	if project.CanonicalRootPath != "" && project.CanonicalRootPath != canonicalRoot {
		return projectregistry.Project{}, fmt.Errorf("%w: canonical root path mismatch", ErrUnsupportedIngest)
	}
	project.RootPath = cleanRoot
	project.CanonicalRootPath = canonicalRoot
	return project, nil
}

func (svc *Service) projectForQuery(projectID string) (projectregistry.Project, error) {
	if svc == nil || svc.registry == nil || svc.graph == nil || svc.state == nil {
		return projectregistry.Project{}, fmt.Errorf("%w: service dependencies are required", ErrUnsupportedIngest)
	}
	project, ok := svc.registry.Get(strings.TrimSpace(projectID))
	if !ok {
		return projectregistry.Project{}, ErrProjectNotFound
	}
	if !project.Enabled {
		return projectregistry.Project{}, ErrProjectDisabled
	}
	if project.DigestMode != projectregistry.DigestModeContentGraph {
		return projectregistry.Project{}, fmt.Errorf("%w: digest_mode must be %q", ErrUnsupportedIngest, projectregistry.DigestModeContentGraph)
	}
	return project, nil
}

func (svc *Service) startRun(project projectregistry.Project, trigger Trigger) Run {
	trigger = normalizeTrigger(trigger)
	startedAt := svc.now().UTC()
	return Run{
		ID:        svc.newID(project, startedAt),
		ProjectID: project.ID,
		Trigger:   trigger,
		Mode:      project.DigestMode,
		Status:    RunStatusRunning,
		StartedAt: startedAt,
	}
}

func normalizeTrigger(trigger Trigger) Trigger {
	if trigger == "" {
		return TriggerManual
	}
	return trigger
}

func (svc *Service) ingestExistingFile(ctx context.Context, project projectregistry.Project, relative string, fullPath string, info fs.FileInfo, run Run) (FileState, []Chunk, []Symbol, []Heading, error) {
	options := SafetyOptions{
		MaxFileBytes:          project.MaxFileBytes,
		MaxChunkBytes:         project.MaxChunkBytes,
		SensitiveMarkerPolicy: project.SensitiveMarkerPolicy,
	}
	if options.MaxFileBytes <= 0 {
		options.MaxFileBytes = DefaultSafetyOptions().MaxFileBytes
	}
	if info.Size() > options.MaxFileBytes {
		state := svc.skippedState(project, relative, SkipReasonFileTooLarge, info.Size(), info.ModTime().UTC(), true, run.StartedAt)
		return state, nil, nil, nil, svc.saveSkipped(ctx, project, run, state, false)
	}
	content, err := os.ReadFile(fullPath)
	if err != nil {
		state := svc.skippedState(project, relative, SkipReasonReadError, info.Size(), info.ModTime().UTC(), true, run.StartedAt)
		return state, nil, nil, nil, svc.saveSkipped(ctx, project, run, state, false)
	}
	chunkSet, safety, err := BuildChunks(relative, content, options)
	if err != nil {
		state := svc.skippedState(project, relative, SkipReasonChunkError, int64(len(content)), info.ModTime().UTC(), true, run.StartedAt)
		return state, nil, nil, nil, svc.saveSkipped(ctx, project, run, state, false)
	}
	if !safety.Eligible {
		state := fileStateFromSafety(project, relative, safety, "", info.ModTime().UTC(), run.StartedAt)
		return state, nil, nil, nil, svc.saveSkipped(ctx, project, run, state, false)
	}

	extractor := svc.extractors.ExtractorFor(relative)
	result, err := svc.extractEligible(ctx, project, relative, hashValue(relative), chunkSet.ContentSHA256, extractor, content, run.StartedAt)
	if err != nil {
		state := svc.skippedState(project, relative, SkipReasonParseError, int64(len(content)), info.ModTime().UTC(), true, run.StartedAt)
		return state, nil, nil, nil, svc.saveSkipped(ctx, project, run, state, false)
	}
	state := fileStateFromSafety(project, relative, safety, chunkSet.ContentSHA256, info.ModTime().UTC(), run.StartedAt)
	if err := svc.state.SaveFileState(ctx, state); err != nil {
		return FileState{}, nil, nil, nil, err
	}
	if err := svc.graph.PutEligibleFile(ctx, project, run, state, chunkSet.Chunks, result.Symbols, result.Headings); err != nil {
		return FileState{}, nil, nil, nil, err
	}
	return state, chunkSet.Chunks, result.Symbols, result.Headings, nil
}

func (svc *Service) extractEligible(ctx context.Context, project projectregistry.Project, relative string, relativePathHash string, contentSHA256 string, extractor Extractor, content []byte, eventAt time.Time) (ExtractorResult, error) {
	if extractor == nil {
		return ExtractorResult{}, nil
	}
	if svc.extractorCacheEnabled && contentSHA256 != "" {
		entry, err := svc.state.GetExtractorCache(ctx, project.ID, relativePathHash, contentSHA256, extractor.Name(), extractor.Version())
		if err == nil {
			return ExtractorResult{
				ExtractorName:    entry.ExtractorName,
				ExtractorVersion: entry.ExtractorVersion,
				Symbols:          entry.Symbols,
				Headings:         entry.Headings,
			}, nil
		}
		if err != nil && !errors.Is(err, ErrExtractorCacheMiss) {
			return ExtractorResult{}, err
		}
	}
	result, err := extractor.Parse(ctx, relative, content)
	if err != nil {
		return ExtractorResult{}, err
	}
	result.ExtractorName = extractor.Name()
	result.ExtractorVersion = extractor.Version()
	if svc.extractorCacheEnabled && contentSHA256 != "" {
		if err := svc.state.SaveExtractorCache(ctx, ExtractorCacheEntry{
			ProjectID:        project.ID,
			RelativePathHash: relativePathHash,
			ContentSHA256:    contentSHA256,
			ExtractorName:    result.ExtractorName,
			ExtractorVersion: result.ExtractorVersion,
			Symbols:          result.Symbols,
			Headings:         result.Headings,
			CreatedAt:        eventAt,
			UpdatedAt:        eventAt,
		}); err != nil {
			return ExtractorResult{}, err
		}
	}
	return result, nil
}

func (svc *Service) saveSkipped(ctx context.Context, project projectregistry.Project, run Run, state FileState, skipDir bool) error {
	if err := svc.state.SaveFileState(ctx, state); err != nil {
		return err
	}
	if err := svc.state.DeleteExtractorCacheForFile(ctx, project.ID, state.RelativePathHash); err != nil {
		return err
	}
	if err := svc.graph.PutSkippedFile(ctx, project, run, state); err != nil {
		return err
	}
	if skipDir {
		return filepath.SkipDir
	}
	return nil
}

func (svc *Service) tombstoneMissingFiles(ctx context.Context, project projectregistry.Project, run Run, seen map[string]struct{}) error {
	states, err := svc.state.ListFileStates(ctx, project.ID, FileStateFilter{})
	if err != nil {
		return err
	}
	for _, state := range states {
		if !state.Present {
			continue
		}
		if _, ok := seen[state.RelativePathHash]; ok {
			continue
		}
		state.Status = FileStatusAbsent
		state.Present = false
		state.ContentSHA256 = ""
		state.LastEventAt = run.StartedAt
		state.LastIngestedAt = run.StartedAt
		if err := svc.state.SaveFileState(ctx, state); err != nil {
			return err
		}
		if err := svc.state.DeleteExtractorCacheForFile(ctx, project.ID, state.RelativePathHash); err != nil {
			return err
		}
		if err := svc.graph.putFileState(ctx, project, run, state); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) persistRun(ctx context.Context, project projectregistry.Project, run Run) error {
	if err := svc.state.SaveRun(ctx, run); err != nil {
		return err
	}
	return svc.graph.PutRun(ctx, project, run)
}

func (svc *Service) skippedState(project projectregistry.Project, relative string, reason SkipReason, size int64, modifiedAt time.Time, present bool, eventAt time.Time) FileState {
	state := FileState{
		ProjectID:        project.ID,
		RelativePathHash: hashValue(relative),
		RelativePath:     relative,
		RelativePathSafe: true,
		Status:           FileStatusSkipped,
		Present:          present,
		SizeBytes:        size,
		ModifiedAt:       modifiedAt,
		LastEventAt:      eventAt,
		LastIngestedAt:   eventAt,
		SkippedReason:    reason,
	}
	if reason == SkipReasonDeniedPath || reason == SkipReasonSensitiveContent {
		state.RelativePath = ""
		state.RelativePathSafe = false
	}
	return state
}

func fileStateFromSafety(project projectregistry.Project, originalRelative string, safety SafetyResult, contentSHA256 string, modifiedAt time.Time, eventAt time.Time) FileState {
	relative := safety.RelativePath
	if relative == "" {
		relative = originalRelative
	}
	status := FileStatusSkipped
	if safety.Eligible {
		status = FileStatusEligible
	}
	state := FileState{
		ProjectID:        project.ID,
		RelativePathHash: hashValue(relative),
		RelativePath:     relative,
		RelativePathSafe: safety.RelativePathSafe,
		Status:           status,
		Present:          true,
		ContentSHA256:    contentSHA256,
		SizeBytes:        safety.SizeBytes,
		ModifiedAt:       modifiedAt,
		LastEventAt:      eventAt,
		LastIngestedAt:   eventAt,
		SkippedReason:    safety.Reason,
	}
	if safety.Reason == SkipReasonDeniedPath || safety.Reason == SkipReasonSensitiveContent {
		state.RelativePath = ""
		state.RelativePathSafe = false
	}
	if status != FileStatusEligible {
		state.ContentSHA256 = ""
	}
	return state
}

func parseEligible(relative string, content []byte) ([]Symbol, []Heading, error) {
	result, err := NewDefaultExtractorRegistry().Extract(context.Background(), relative, content)
	return result.Symbols, result.Headings, err
}

func recordRunReason(run *Run, reason SkipReason) {
	if run == nil || reason == SkipReasonNone {
		return
	}
	if run.ReasonCounts == nil {
		run.ReasonCounts = make(map[string]int)
	}
	run.ReasonCounts[string(reason)]++
	if isFileErrorReason(reason) {
		run.ErrorCategory = "file_errors"
	}
}

func isFileErrorReason(reason SkipReason) bool {
	switch reason {
	case SkipReasonStatError, SkipReasonReadError, SkipReasonChunkError, SkipReasonParseError:
		return true
	default:
		return false
	}
}

func validateCanonicalRoot(root string) (string, string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", "", fmt.Errorf("root path must be absolute")
	}
	cleanRoot := filepath.Clean(root)
	info, err := os.Stat(cleanRoot)
	if err != nil {
		return "", "", err
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("root path must be a directory")
	}
	canonicalRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return "", "", err
	}
	canonicalRoot = filepath.Clean(canonicalRoot)
	if canonicalRoot != cleanRoot {
		return "", "", fmt.Errorf("root path must not resolve through a symlink")
	}
	return cleanRoot, canonicalRoot, nil
}

func safeRelativePath(root string, filePath string) (string, bool) {
	relative, err := filepath.Rel(root, filePath)
	if err != nil {
		return "", false
	}
	if relative == "." {
		return "", true
	}
	if relative == ".." || strings.HasPrefix(relative, "../") || filepath.IsAbs(relative) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func normalizeProjectRelativePath(relative string) (string, bool) {
	relative = strings.TrimSpace(relative)
	if relative == "" || strings.ContainsRune(relative, '\x00') || strings.Contains(relative, "\\") {
		return "", false
	}
	if strings.HasPrefix(relative, "/") || strings.HasPrefix(relative, "//") || filepath.IsAbs(relative) {
		return "", false
	}
	cleaned := path.Clean(relative)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

func defaultRunID(project projectregistry.Project, startedAt time.Time) string {
	return "ingest_" + shortHash(project.ID+"\x00"+project.GraphNamespace+"\x00"+startedAt.UTC().Format(time.RFC3339Nano))
}

func hashValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
