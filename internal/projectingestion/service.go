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

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

var (
	ErrProjectNotFound     = projectregistry.ErrProjectNotFound
	ErrProjectDisabled     = errors.New("ingestion project disabled")
	ErrUnsupportedIngest   = errors.New("ingestion unsupported")
	ErrPathEscapesRoot     = errors.New("path escapes project root")
	ErrPathNotProjectLocal = errors.New("path must be project-relative")
)

type stateStore interface {
	SaveRun(context.Context, Run) error
	GetRun(context.Context, string, string) (Run, error)
	SaveFileState(context.Context, FileState) error
	ListFileStates(context.Context, string, FileStateFilter) ([]FileState, error)
}

type Service struct {
	registry *projectregistry.Registry
	graph    *GraphStore
	state    stateStore
	now      func() time.Time
	newID    func(projectregistry.Project, time.Time) string
}

func NewService(registry *projectregistry.Registry, graph *GraphStore, state stateStore) *Service {
	return &Service{
		registry: registry,
		graph:    graph,
		state:    state,
		now:      func() time.Time { return time.Now().UTC() },
		newID:    defaultRunID,
	}
}

func (svc *Service) IngestProject(ctx context.Context, projectID string, trigger Trigger) (Run, error) {
	project, err := svc.projectForIngestion(projectID)
	if err != nil {
		return Run{}, err
	}
	run := svc.startRun(project, trigger)
	if err := svc.persistRun(ctx, project, run); err != nil {
		return run, err
	}

	seen := make(map[string]struct{})
	root := project.CanonicalRootPath
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			relative, _ := safeRelativePath(root, filePath)
			return fmt.Errorf("walk failed for relative path %q", relative)
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
			return svc.saveSkipped(ctx, project, run, state, entry.IsDir())
		}
		if entry.IsDir() {
			if projectregistry.ProjectExcludesRelativePath(project, relative) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat failed for relative path %q", relative)
		}
		if !info.Mode().IsRegular() {
			run.FilesSkipped++
			state := svc.skippedState(project, relative, SkipReasonUnsafePath, info.Size(), info.ModTime().UTC(), true, run.StartedAt)
			seen[state.RelativePathHash] = struct{}{}
			return svc.saveSkipped(ctx, project, run, state, false)
		}
		if !projectregistry.ProjectIncludesRelativePath(project, relative) {
			run.FilesSkipped++
			state := svc.skippedState(project, relative, SkipReasonDeniedPath, info.Size(), info.ModTime().UTC(), true, run.StartedAt)
			seen[state.RelativePathHash] = struct{}{}
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
	project, err := svc.projectForIngestion(projectID)
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
		state := svc.skippedState(project, relative, SkipReasonUnsafePath, info.Size(), info.ModTime().UTC(), true, run.StartedAt)
		if !projectregistry.ProjectIncludesRelativePath(project, relative) {
			state.SkippedReason = SkipReasonDeniedPath
		}
		run.FilesSkipped = 1
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
	}
	run.Status = RunStatusCompleted
	run.FinishedAt = svc.now().UTC()
	return run, svc.persistRun(ctx, project, run)
}

func (svc *Service) GetRun(ctx context.Context, projectID string, runID string) (Run, error) {
	return svc.state.GetRun(ctx, strings.TrimSpace(projectID), strings.TrimSpace(runID))
}

func (svc *Service) ListFileStates(ctx context.Context, projectID string, filter FileStateFilter) ([]FileState, error) {
	return svc.state.ListFileStates(ctx, strings.TrimSpace(projectID), filter)
}

func (svc *Service) projectForIngestion(projectID string) (projectregistry.Project, error) {
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
	if project.UpdatePolicy != projectregistry.UpdatePolicyManual {
		return projectregistry.Project{}, fmt.Errorf("%w: update_policy must be %q for manual ingestion", ErrUnsupportedIngest, projectregistry.UpdatePolicyManual)
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

func (svc *Service) startRun(project projectregistry.Project, trigger Trigger) Run {
	if trigger == "" {
		trigger = TriggerManual
	}
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
		return FileState{}, nil, nil, nil, fmt.Errorf("read failed for relative path %q", relative)
	}
	chunkSet, safety, err := BuildChunks(relative, content, options)
	if err != nil {
		return FileState{}, nil, nil, nil, err
	}
	if !safety.Eligible {
		state := fileStateFromSafety(project, relative, safety, "", info.ModTime().UTC(), run.StartedAt)
		return state, nil, nil, nil, svc.saveSkipped(ctx, project, run, state, false)
	}

	symbols, headings, err := parseEligible(relative, content)
	if err != nil {
		return FileState{}, nil, nil, nil, fmt.Errorf("parse failed for relative path %q", relative)
	}
	state := fileStateFromSafety(project, relative, safety, chunkSet.ContentSHA256, info.ModTime().UTC(), run.StartedAt)
	if err := svc.state.SaveFileState(ctx, state); err != nil {
		return FileState{}, nil, nil, nil, err
	}
	if err := svc.graph.PutEligibleFile(ctx, project, run, state, chunkSet.Chunks, symbols, headings); err != nil {
		return FileState{}, nil, nil, nil, err
	}
	return state, chunkSet.Chunks, symbols, headings, nil
}

func (svc *Service) saveSkipped(ctx context.Context, project projectregistry.Project, run Run, state FileState, skipDir bool) error {
	if err := svc.state.SaveFileState(ctx, state); err != nil {
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
		if !state.Present || state.Status != FileStatusEligible {
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
	switch strings.ToLower(path.Ext(relative)) {
	case ".go":
		symbols, err := ParseGoFile(relative, content)
		return symbols, nil, err
	case ".md", ".markdown":
		headings, err := ParseMarkdownHeadings(content)
		return nil, headings, err
	default:
		return nil, nil, nil
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
