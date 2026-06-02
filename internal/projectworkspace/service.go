package projectworkspace

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/MiviaLabs/go-mivia/internal/agentactivity"
	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

type Options struct {
	Enabled bool
}

type GitRunner interface {
	Run(ctx context.Context, root string, maxBytes int, args ...string) ([]byte, bool, error)
}

type Service struct {
	registry       *projectregistry.Registry
	ingest         workspaceIngestion
	git            GitRunner
	enabled        bool
	secret         []byte
	locks          sync.Map
	policyRecorder *agentactivity.Recorder
}

type workspaceIngestion interface {
	GetFile(context.Context, string, string) (projectingestion.FileMetadata, error)
	IngestPath(context.Context, string, string, projectingestion.Trigger) (projectingestion.Run, error)
}

func NewService(registry *projectregistry.Registry, ingest workspaceIngestion, options Options) *Service {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		sum := sha256.Sum256([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
		secret = sum[:]
	}
	return &Service{
		registry: registry,
		ingest:   ingest,
		git:      execGitRunner{},
		enabled:  options.Enabled,
		secret:   secret,
	}
}

func (svc *Service) SetGitRunner(runner GitRunner) {
	if runner != nil {
		svc.git = runner
	}
}

func (svc *Service) SetPolicyRecorder(recorder *agentactivity.Recorder) {
	svc.policyRecorder = recorder
}

func (svc *Service) GitAvailable(ctx context.Context, projectID string) (bool, error) {
	project, err := svc.project(projectID, false)
	if err != nil {
		if errors.Is(err, ErrWorkspaceDisabled) || errors.Is(err, ErrProjectNotFound) {
			return false, nil
		}
		return false, err
	}
	out, _, err := svc.git.Run(ctx, project.CanonicalRootPath, 64, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		return false, nil
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

func (svc *Service) GitStatus(ctx context.Context, projectID string, options GitStatusOptions) (GitStatus, error) {
	project, err := svc.project(projectID, false)
	if err != nil {
		return GitStatus{}, err
	}
	prefix := strings.TrimSpace(options.PathPrefix)
	if prefix != "" {
		prefix, err = normalizeAllowedPath(project, prefix, nil)
		if err != nil {
			return GitStatus{}, err
		}
	}
	args := []string{"status", "--porcelain=v2", "--branch", "-z"}
	if !options.IncludeUntracked {
		args = append(args, "--untracked-files=no")
	}
	if prefix != "" {
		args = append(args, "--", prefix)
	}
	out, _, err := svc.git.Run(ctx, project.CanonicalRootPath, 256<<10, args...)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return GitStatus{}, err
		}
		return GitStatus{}, ErrGitUnavailable
	}
	status := parseGitStatus(project.ID, out, prefix)
	status.Entries = filterStatusEntries(project, status.Entries)
	pageSize := normalizePageSize(options.PageSize)
	offset, err := parsePageToken(options.PageToken)
	if err != nil {
		return GitStatus{}, err
	}
	status.Entries, status.NextPageToken, status.Truncated = paginate(status.Entries, offset, pageSize)
	return status, nil
}

func (svc *Service) GitDiff(ctx context.Context, projectID string, options GitDiffOptions) (GitDiff, error) {
	project, err := svc.project(projectID, false)
	if err != nil {
		return GitDiff{}, err
	}
	scope, err := normalizeDiffScope(options.Scope)
	if err != nil {
		return GitDiff{}, err
	}
	contextLines := options.ContextLines
	if contextLines < 0 || contextLines > 10 {
		return GitDiff{}, ErrInvalidInput
	}
	if contextLines == 0 && options.ContextLines == 0 {
		contextLines = 3
	}
	maxBytes := normalizeMax(options.MaxDiffBytes, DefaultMaxDiffBytes, MaxDiffBytes)
	pathspec, err := svc.diffPathspec(ctx, project, options)
	if err != nil {
		return GitDiff{}, err
	}
	changed, err := svc.changedFiles(ctx, project, scope, pathspec)
	if err != nil {
		return GitDiff{}, err
	}
	offset, err := parsePageToken(options.PageToken)
	if err != nil {
		return GitDiff{}, err
	}
	pageSize := normalizePageSize(0)
	result := GitDiff{ProjectID: project.ID, Scope: scope}
	remaining := maxBytes
	for i, file := range changed {
		if i < offset {
			continue
		}
		if len(result.Files) >= pageSize || remaining <= 0 {
			result.NextPageToken = strconv.Itoa(i)
			result.DiffTruncated = true
			break
		}
		if reason := svc.diffSkipReason(ctx, project, file.RelativePath, file.Status); reason != "" {
			result.Skipped = append(result.Skipped, DiffSkip{RelativePath: file.RelativePath, Reason: reason})
			continue
		}
		diffText, truncated, err := svc.fileDiff(ctx, project, scope, contextLines, remaining, file.RelativePath)
		if err != nil {
			return GitDiff{}, err
		}
		if !validTextDiff(diffText) {
			result.Skipped = append(result.Skipped, DiffSkip{RelativePath: file.RelativePath, Reason: string(projectingestion.SkipReasonBinaryContent)})
			continue
		}
		file.Diff = diffText
		file.Additions, file.Deletions = countDiffLines(diffText)
		result.Files = append(result.Files, file)
		if truncated {
			result.DiffTruncated = true
			break
		}
		remaining -= len(diffText)
	}
	return result, nil
}

func (svc *Service) ReadFile(ctx context.Context, projectID string, options ReadFileOptions) (WorkspaceFile, error) {
	project, err := svc.project(projectID, false)
	if err != nil {
		return WorkspaceFile{}, err
	}
	relativePath, fileID, err := svc.resolveSelector(ctx, project, options.FileID, options.RelativePath)
	if err != nil {
		return WorkspaceFile{}, err
	}
	maxBytes := normalizeMax(options.MaxBytes, DefaultMaxReadBytes, MaxReadBytes)
	content, info, err := svc.readEligibleFile(project, relativePath)
	if err != nil {
		return WorkspaceFile{}, err
	}
	return svc.workspaceFile(project, fileID, relativePath, content, info, maxBytes), nil
}

func (svc *Service) EditFile(ctx context.Context, projectID string, options EditFileOptions) (EditResult, error) {
	project, err := svc.project(projectID, true)
	if err != nil {
		return EditResult{}, err
	}
	if strings.TrimSpace(options.EditToken) == "" || len(options.Edits) == 0 {
		return EditResult{}, ErrInvalidInput
	}
	relativePath, fileID, err := svc.resolveSelector(ctx, project, options.FileID, options.RelativePath)
	if err != nil {
		return EditResult{}, err
	}
	lock := svc.lockFor(project.ID + "\x00" + relativePath)
	lock.Lock()
	defer lock.Unlock()
	content, info, err := svc.readEligibleFile(project, relativePath)
	if err != nil {
		return EditResult{}, err
	}
	if !hmac.Equal([]byte(options.EditToken), []byte(svc.editToken(project, relativePath, content, info))) {
		return EditResult{}, ErrEditTokenInvalid
	}
	next, err := applyExactEdits(content, options.Edits)
	if err != nil {
		svc.recordPolicyEvent(project.ID, "unsafe_edit", relativePath)
		return EditResult{}, err
	}
	if !safeText(project, relativePath, next, project.MaxFileBytes) {
		svc.recordPolicyEvent(project.ID, "unsafe_edit", relativePath)
		return EditResult{}, ErrUnsafeContent
	}
	preview := diffPreview(relativePath, content, next, DefaultMaxDiffBytes)
	if !safeText(project, relativePath, []byte(preview), DefaultMaxDiffBytes) {
		svc.recordPolicyEvent(project.ID, "unsafe_edit", relativePath)
		return EditResult{}, ErrUnsafeContent
	}
	result := EditResult{
		Applied:       !options.DryRun,
		DiffPreview:   preview,
		TextTruncated: len(preview) >= DefaultMaxDiffBytes,
	}
	if options.DryRun {
		result.File = svc.workspaceFile(project, fileID, relativePath, next, info, DefaultMaxReadBytes)
		result.NewEditToken = result.File.EditToken
		return result, nil
	}
	fullPath, err := resolveDiskPath(project, relativePath)
	if err != nil {
		return EditResult{}, err
	}
	if err := atomicWrite(fullPath, next, info); err != nil {
		return EditResult{}, ErrInvalidInput
	}
	writtenInfo, err := os.Stat(fullPath)
	if err != nil {
		return EditResult{}, ErrInvalidInput
	}
	written, err := os.ReadFile(fullPath)
	if err != nil {
		return EditResult{}, ErrInvalidInput
	}
	result.File = svc.workspaceFile(project, fileID, relativePath, written, writtenInfo, DefaultMaxReadBytes)
	result.NewEditToken = result.File.EditToken
	if svc.ingest != nil {
		run, err := svc.ingest.IngestPath(ctx, project.ID, relativePath, projectingestion.TriggerLive)
		if err != nil {
			return EditResult{}, ErrIngestionUnsupported
		}
		result.IngestionRunID = run.ID
	}
	return result, nil
}

func (svc *Service) CreateFile(ctx context.Context, projectID string, options CreateFileOptions) (CreateFileResult, error) {
	project, err := svc.project(projectID, true)
	if err != nil {
		return CreateFileResult{}, err
	}
	relativePath, err := normalizeAllowedPath(project, strings.TrimSpace(options.RelativePath), []byte(options.Text))
	if err != nil {
		return CreateFileResult{}, err
	}
	content := []byte(options.Text)
	if !safeText(project, relativePath, content, project.MaxFileBytes) {
		svc.recordPolicyEvent(project.ID, "unsafe_create", relativePath)
		return CreateFileResult{}, ErrUnsafeContent
	}
	lock := svc.lockFor(project.ID + "\x00" + relativePath)
	lock.Lock()
	defer lock.Unlock()
	fullPath, err := resolveCreateDiskPath(project, relativePath, options.CreateParentDirs, options.DryRun)
	if err != nil {
		return CreateFileResult{}, err
	}
	if options.DryRun {
		info := virtualFileInfo{name: filepath.Base(relativePath), size: int64(len(content)), mode: 0o600, modTime: time.Now().UTC()}
		file := svc.workspaceFile(project, "", relativePath, content, info, DefaultMaxReadBytes)
		return CreateFileResult{
			Applied:      false,
			File:         file,
			NewEditToken: file.EditToken,
		}, nil
	}
	if err := atomicCreate(fullPath, content, 0o600); err != nil {
		if errors.Is(err, os.ErrExist) {
			return CreateFileResult{}, ErrEditConflict
		}
		return CreateFileResult{}, ErrInvalidInput
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return CreateFileResult{}, ErrInvalidInput
	}
	written, err := os.ReadFile(fullPath)
	if err != nil {
		return CreateFileResult{}, ErrInvalidInput
	}
	result := CreateFileResult{
		Applied: true,
		File:    svc.workspaceFile(project, "", relativePath, written, info, DefaultMaxReadBytes),
	}
	result.NewEditToken = result.File.EditToken
	runID, err := svc.queuePathIngestion(ctx, project.ID, relativePath)
	if err != nil {
		return CreateFileResult{}, err
	}
	result.IngestionRunID = runID
	return result, nil
}

func (svc *Service) DeleteFile(ctx context.Context, projectID string, options DeleteFileOptions) (DeleteFileResult, error) {
	project, err := svc.project(projectID, true)
	if err != nil {
		return DeleteFileResult{}, err
	}
	if strings.TrimSpace(options.EditToken) == "" {
		return DeleteFileResult{}, ErrInvalidInput
	}
	relativePath, _, err := svc.resolveSelector(ctx, project, options.FileID, options.RelativePath)
	if err != nil {
		return DeleteFileResult{}, err
	}
	lock := svc.lockFor(project.ID + "\x00" + relativePath)
	lock.Lock()
	defer lock.Unlock()
	content, info, err := svc.readEligibleFile(project, relativePath)
	if err != nil {
		return DeleteFileResult{}, err
	}
	if !hmac.Equal([]byte(options.EditToken), []byte(svc.editToken(project, relativePath, content, info))) {
		return DeleteFileResult{}, ErrEditTokenInvalid
	}
	fullPath, err := resolveDiskPath(project, relativePath)
	if err != nil {
		return DeleteFileResult{}, err
	}
	lstat, err := os.Lstat(fullPath)
	if err != nil || lstat.IsDir() || lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() {
		return DeleteFileResult{}, ErrInvalidInput
	}
	if lstat.Size() != info.Size() || !lstat.ModTime().Equal(info.ModTime()) {
		return DeleteFileResult{}, ErrEditTokenInvalid
	}
	result := DeleteFileResult{
		Deleted:      !options.DryRun,
		ProjectID:    project.ID,
		RelativePath: relativePath,
	}
	if options.DryRun {
		return result, nil
	}
	if err := os.Remove(fullPath); err != nil {
		return DeleteFileResult{}, ErrInvalidInput
	}
	runID, err := svc.queuePathIngestion(ctx, project.ID, relativePath)
	if err != nil {
		return DeleteFileResult{}, err
	}
	result.IngestionRunID = runID
	return result, nil
}

func (svc *Service) project(projectID string, requireEdit bool) (projectregistry.Project, error) {
	if svc == nil || svc.registry == nil || !svc.enabled {
		return projectregistry.Project{}, ErrWorkspaceDisabled
	}
	project, ok := svc.registry.Get(strings.TrimSpace(projectID))
	if !ok {
		return projectregistry.Project{}, ErrProjectNotFound
	}
	if !project.Enabled {
		return projectregistry.Project{}, ErrWorkspaceDisabled
	}
	switch project.WorkspaceMode {
	case ModeReadOnly:
		if requireEdit {
			return projectregistry.Project{}, ErrWorkspaceReadOnly
		}
	case ModeEdit:
	case "", ModeDisabled:
		return projectregistry.Project{}, ErrWorkspaceDisabled
	default:
		return projectregistry.Project{}, ErrWorkspaceDisabled
	}
	if project.DigestMode != projectregistry.DigestModeContentGraph {
		return projectregistry.Project{}, ErrWorkspaceDisabled
	}
	if project.CanonicalRootPath == "" {
		return projectregistry.Project{}, ErrWorkspaceDisabled
	}
	return project, nil
}

func (svc *Service) recordPolicyEvent(projectID string, category string, relativePath string) {
	if svc == nil || svc.policyRecorder == nil {
		return
	}
	svc.policyRecorder.RecordPolicyEvent(agentactivity.PolicyEvent{
		ProjectID: projectID,
		Category:  category,
		Path:      relativePath,
	})
}

func (svc *Service) resolveSelector(ctx context.Context, project projectregistry.Project, fileID string, relativePath string) (string, string, error) {
	fileID = strings.TrimSpace(fileID)
	relativePath = strings.TrimSpace(relativePath)
	if (fileID == "") == (relativePath == "") {
		return "", "", ErrInvalidInput
	}
	if fileID != "" {
		if svc.ingest == nil {
			return "", "", ErrInvalidInput
		}
		file, err := svc.ingest.GetFile(ctx, project.ID, fileID)
		if err != nil {
			return "", "", ErrInvalidInput
		}
		if !file.Present || file.Status != string(projectingestion.FileStatusEligible) || !file.RelativePathOK || file.RelativePath == "" {
			return "", "", ErrUnsafeContent
		}
		relativePath = file.RelativePath
	}
	normalized, err := normalizeAllowedPath(project, relativePath, nil)
	if err != nil {
		return "", "", err
	}
	return normalized, fileID, nil
}

func normalizeAllowedPath(project projectregistry.Project, relativePath string, content []byte) (string, error) {
	if content == nil {
		content = []byte{}
	}
	safety := projectingestion.EvaluateSafety(relativePath, content, safetyOptions(project))
	if !safety.RelativePathSafe || safety.RelativePath == "" {
		return "", ErrInvalidInput
	}
	if !projectregistry.ProjectIncludesRelativePath(project, safety.RelativePath) {
		return "", ErrUnsafeContent
	}
	if !safety.Eligible && len(content) > 0 {
		return "", ErrUnsafeContent
	}
	return safety.RelativePath, nil
}

func (svc *Service) diffPathspec(ctx context.Context, project projectregistry.Project, options GitDiffOptions) (string, error) {
	if strings.TrimSpace(options.FileID) != "" || strings.TrimSpace(options.RelativePath) != "" {
		relativePath, _, err := svc.resolveSelector(ctx, project, options.FileID, options.RelativePath)
		return relativePath, err
	}
	if strings.TrimSpace(options.PathPrefix) == "" {
		return "", nil
	}
	return normalizeAllowedPath(project, options.PathPrefix, nil)
}

func (svc *Service) readEligibleFile(project projectregistry.Project, relativePath string) ([]byte, os.FileInfo, error) {
	fullPath, err := resolveDiskPath(project, relativePath)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(fullPath)
	if err != nil || info.IsDir() || !info.Mode().IsRegular() {
		return nil, nil, ErrInvalidInput
	}
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, nil, ErrInvalidInput
	}
	if !safeText(project, relativePath, content, project.MaxFileBytes) {
		return nil, nil, ErrUnsafeContent
	}
	return content, info, nil
}

func safeText(project projectregistry.Project, relativePath string, content []byte, maxBytes int64) bool {
	if maxBytes <= 0 {
		maxBytes = project.MaxFileBytes
	}
	result := projectingestion.EvaluateWorkspaceSafety(relativePath, content, workspaceSafetyOptions(project, maxBytes))
	return result.Eligible && projectregistry.ProjectIncludesRelativePath(project, result.RelativePath)
}

func safetyOptions(project projectregistry.Project) projectingestion.SafetyOptions {
	return projectingestion.SafetyOptions{
		MaxFileBytes:          project.MaxFileBytes,
		MaxChunkBytes:         project.MaxChunkBytes,
		SensitiveMarkerPolicy: project.SensitiveMarkerPolicy,
	}
}

func workspaceSafetyOptions(project projectregistry.Project, maxBytes int64) projectingestion.WorkspaceSafetyOptions {
	return projectingestion.WorkspaceSafetyOptions{
		MaxFileBytes:          maxBytes,
		SensitiveMarkerPolicy: project.SensitiveMarkerPolicy,
	}
}

func resolveDiskPath(project projectregistry.Project, relativePath string) (string, error) {
	normalized, err := normalizeAllowedPath(project, relativePath, nil)
	if err != nil {
		return "", err
	}
	root := filepath.Clean(project.CanonicalRootPath)
	fullPath := filepath.Join(root, filepath.FromSlash(normalized))
	cleanFull := filepath.Clean(fullPath)
	relative, err := filepath.Rel(root, cleanFull)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", ErrInvalidInput
	}
	current := root
	for _, part := range strings.Split(filepath.FromSlash(normalized), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", ErrInvalidInput
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", ErrUnsafeContent
		}
	}
	return cleanFull, nil
}

func resolveCreateDiskPath(project projectregistry.Project, relativePath string, createParentDirs bool, dryRun bool) (string, error) {
	normalized, err := normalizeAllowedPath(project, relativePath, nil)
	if err != nil {
		return "", err
	}
	root := filepath.Clean(project.CanonicalRootPath)
	fullPath := filepath.Join(root, filepath.FromSlash(normalized))
	cleanFull := filepath.Clean(fullPath)
	relative, err := filepath.Rel(root, cleanFull)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", ErrInvalidInput
	}
	parts := strings.Split(filepath.FromSlash(normalized), string(filepath.Separator))
	current := root
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return "", ErrUnsafeContent
			}
			if i == len(parts)-1 {
				return "", ErrEditConflict
			}
			if !info.IsDir() {
				return "", ErrInvalidInput
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", ErrInvalidInput
		}
		if !createParentDirs && i < len(parts)-1 {
			return "", ErrInvalidInput
		}
		if i < len(parts)-1 {
			if dryRun {
				break
			}
			if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return "", ErrInvalidInput
			}
			if info, err := os.Lstat(current); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", ErrUnsafeContent
			}
			continue
		}
		return cleanFull, nil
	}
	if dryRun {
		return cleanFull, nil
	}
	return "", ErrEditConflict
}

func (svc *Service) workspaceFile(project projectregistry.Project, fileID string, relativePath string, content []byte, info os.FileInfo, maxBytes int) WorkspaceFile {
	text := string(content)
	truncated := false
	if maxBytes > 0 && len(content) > maxBytes {
		text = string(content[:maxBytes])
		truncated = true
	}
	return WorkspaceFile{
		FileID:        fileID,
		ProjectID:     project.ID,
		RelativePath:  relativePath,
		Extension:     filepath.Ext(relativePath),
		SizeBytes:     int64(len(content)),
		ModifiedAt:    info.ModTime().UTC(),
		Text:          text,
		TextTruncated: truncated,
		LineCount:     lineCount(content),
		EditToken:     svc.editToken(project, relativePath, content, info),
	}
}

func (svc *Service) editToken(project projectregistry.Project, relativePath string, content []byte, info os.FileInfo) string {
	sum := sha256.Sum256(content)
	payload := strings.Join([]string{
		project.ID,
		relativePath,
		strconv.FormatInt(info.Size(), 10),
		strconv.FormatInt(info.ModTime().UTC().UnixNano(), 10),
		base64.RawURLEncoding.EncodeToString(sum[:]),
	}, "\x00")
	mac := hmac.New(sha256.New, svc.secret)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (svc *Service) changedFiles(ctx context.Context, project projectregistry.Project, scope string, pathspec string) ([]DiffFile, error) {
	args := diffBaseArgs(scope, 3)
	args = append([]string{"diff", "--name-status", "-z"}, args[1:]...)
	if pathspec != "" {
		args = append(args, "--", pathspec)
	}
	out, _, err := svc.git.Run(ctx, project.CanonicalRootPath, 256<<10, args...)
	if err != nil {
		return nil, ErrGitUnavailable
	}
	tokens := splitNUL(out)
	files := make([]DiffFile, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		status := tokens[i]
		if status == "" {
			continue
		}
		code := string(status[0])
		switch code {
		case "R", "C":
			if i+2 >= len(tokens) {
				return nil, ErrGitUnavailable
			}
			oldPath, newPath := tokens[i+1], tokens[i+2]
			i += 2
			normalized, err := normalizeAllowedPath(project, newPath, nil)
			if err != nil {
				files = append(files, DiffFile{RelativePath: "", Status: "skipped"})
				continue
			}
			if _, err := normalizeAllowedPath(project, oldPath, nil); err != nil {
				files = append(files, DiffFile{RelativePath: normalized, Status: "skipped"})
				continue
			}
			files = append(files, DiffFile{RelativePath: normalized, Status: code})
		default:
			if i+1 >= len(tokens) {
				return nil, ErrGitUnavailable
			}
			relativePath := tokens[i+1]
			i++
			normalized, err := normalizeAllowedPath(project, relativePath, nil)
			if err != nil {
				files = append(files, DiffFile{RelativePath: "", Status: "skipped"})
				continue
			}
			files = append(files, DiffFile{RelativePath: normalized, Status: code})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].RelativePath < files[j].RelativePath })
	return files, nil
}

func (svc *Service) diffSkipReason(ctx context.Context, project projectregistry.Project, relativePath string, status string) string {
	if relativePath == "" || status == "skipped" {
		return string(projectingestion.SkipReasonDeniedPath)
	}
	_ = ctx
	return ""
}

func (svc *Service) fileDiff(ctx context.Context, project projectregistry.Project, scope string, contextLines int, maxBytes int, relativePath string) (string, bool, error) {
	args := diffBaseArgs(scope, contextLines)
	args = append(args, "--", relativePath)
	out, truncated, err := svc.git.Run(ctx, project.CanonicalRootPath, maxBytes, args...)
	if err != nil {
		return "", false, ErrGitUnavailable
	}
	return string(out), truncated, nil
}

func diffBaseArgs(scope string, contextLines int) []string {
	args := []string{"diff", "--no-ext-diff", "--no-color", "--unified=" + strconv.Itoa(contextLines)}
	switch scope {
	case DiffScopeStaged:
		args = append(args, "--cached")
	case DiffScopeHead:
		args = append(args, "HEAD")
	}
	return args
}

type execGitRunner struct{}

func (execGitRunner) Run(ctx context.Context, root string, maxBytes int, args ...string) ([]byte, bool, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxDiffBytes
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, false, ErrGitUnavailable
	}
	commandArgs := append([]string{"--no-optional-locks", "-c", "safe.directory=" + root}, args...)
	cmd := exec.CommandContext(ctx, "git", commandArgs...)
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	var buf bytes.Buffer
	_, readErr := io.Copy(&buf, io.LimitReader(stdout, int64(maxBytes)+1))
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, false, readErr
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, false, ctxErr
	}
	if waitErr != nil {
		return nil, false, waitErr
	}
	out := buf.Bytes()
	truncated := len(out) > maxBytes
	if truncated {
		out = out[:maxBytes]
	}
	return out, truncated, nil
}

func parseGitStatus(projectID string, out []byte, prefix string) GitStatus {
	status := GitStatus{ProjectID: projectID}
	tokens := splitNUL(out)
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case strings.HasPrefix(token, "# branch.head "):
			branch := strings.TrimSpace(strings.TrimPrefix(token, "# branch.head "))
			if branch != "(detached)" {
				status.Branch = branch
			}
		case strings.HasPrefix(token, "# branch.oid "):
			oid := strings.TrimSpace(strings.TrimPrefix(token, "# branch.oid "))
			if len(oid) >= 12 {
				status.HeadOIDShort = oid[:12]
			}
		case strings.HasPrefix(token, "1 "):
			fields := strings.Fields(token)
			if len(fields) >= 9 {
				entry := statusEntry(fields[8], fields[1], "")
				if matchesPrefix(entry.RelativePath, prefix) {
					status.Entries = append(status.Entries, entry)
				}
			}
		case strings.HasPrefix(token, "? "):
			relativePath := strings.TrimSpace(strings.TrimPrefix(token, "? "))
			entry := StatusEntry{RelativePath: relativePath, Status: "untracked", WorktreeStatus: "?"}
			if matchesPrefix(entry.RelativePath, prefix) {
				status.Entries = append(status.Entries, entry)
			}
		case strings.HasPrefix(token, "2 "):
			fields := strings.Fields(token)
			if len(fields) >= 10 {
				renamedFrom := ""
				if i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "1 ") && !strings.HasPrefix(tokens[i+1], "2 ") && !strings.HasPrefix(tokens[i+1], "? ") {
					renamedFrom = tokens[i+1]
					i++
				}
				entry := statusEntry(fields[9], fields[1], renamedFrom)
				if matchesPrefix(entry.RelativePath, prefix) {
					status.Entries = append(status.Entries, entry)
				}
			}
		}
	}
	sort.Slice(status.Entries, func(i, j int) bool { return status.Entries[i].RelativePath < status.Entries[j].RelativePath })
	return status
}

func statusEntry(relativePath string, xy string, renamedFrom string) StatusEntry {
	staged, worktree := "", ""
	if len(xy) >= 2 {
		if xy[0] != '.' {
			staged = string(xy[0])
		}
		if xy[1] != '.' {
			worktree = string(xy[1])
		}
	}
	status := "modified"
	if staged == "A" || worktree == "A" {
		status = "added"
	} else if staged == "D" || worktree == "D" {
		status = "deleted"
	} else if staged == "R" || worktree == "R" {
		status = "renamed"
	}
	return StatusEntry{RelativePath: relativePath, Status: status, StagedStatus: staged, WorktreeStatus: worktree, RenamedFrom: renamedFrom}
}

func filterStatusEntries(project projectregistry.Project, entries []StatusEntry) []StatusEntry {
	filtered := make([]StatusEntry, 0, len(entries))
	for _, entry := range entries {
		relativePath, err := normalizeAllowedPath(project, entry.RelativePath, nil)
		if err != nil {
			continue
		}
		entry.RelativePath = relativePath
		if entry.RenamedFrom != "" {
			renamedFrom, err := normalizeAllowedPath(project, entry.RenamedFrom, nil)
			if err != nil {
				entry.RenamedFrom = ""
			} else {
				entry.RenamedFrom = renamedFrom
			}
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func applyExactEdits(content []byte, edits []ExactEdit) ([]byte, error) {
	next := make([]byte, 0, len(content))
	last := 0
	for _, edit := range edits {
		if edit.StartByte < last || edit.StartByte < 0 || edit.EndByte < edit.StartByte || edit.EndByte > len(content) {
			return nil, ErrInvalidInput
		}
		if !utf8.Valid(content[edit.StartByte:edit.EndByte]) || !utf8.ValidString(edit.NewText) {
			return nil, ErrInvalidInput
		}
		if string(content[edit.StartByte:edit.EndByte]) != edit.OldText {
			return nil, ErrEditConflict
		}
		next = append(next, content[last:edit.StartByte]...)
		next = append(next, edit.NewText...)
		last = edit.EndByte
	}
	next = append(next, content[last:]...)
	if !utf8.Valid(next) {
		return nil, ErrInvalidInput
	}
	return next, nil
}

func atomicWrite(fullPath string, content []byte, info os.FileInfo) error {
	dir := filepath.Dir(fullPath)
	temp, err := os.CreateTemp(dir, ".mivia-edit-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	_ = temp.Sync()
	if err := temp.Close(); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if uid, gid, ok := fileOwner(info); ok {
			if err := os.Chown(tempName, uid, gid); err != nil && !ownershipUnsupported(err) {
				return err
			}
		}
	}
	mode := info.Mode()
	if err := os.Chmod(tempName, mode); err != nil && !chmodUnsupported(err) {
		return err
	}
	if err := os.Rename(tempName, fullPath); err != nil {
		return err
	}
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return nil
}

func atomicCreate(fullPath string, content []byte, mode os.FileMode) error {
	file, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		_ = os.Remove(fullPath)
		return err
	}
	_ = file.Sync()
	if err := file.Close(); err != nil {
		_ = os.Remove(fullPath)
		return err
	}
	if err := os.Chmod(fullPath, mode); err != nil && !chmodUnsupported(err) {
		_ = os.Remove(fullPath)
		return err
	}
	if dirHandle, err := os.Open(filepath.Dir(fullPath)); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return nil
}

type virtualFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

func (info virtualFileInfo) Name() string       { return info.name }
func (info virtualFileInfo) Size() int64        { return info.size }
func (info virtualFileInfo) Mode() os.FileMode  { return info.mode }
func (info virtualFileInfo) ModTime() time.Time { return info.modTime }
func (info virtualFileInfo) IsDir() bool        { return false }
func (info virtualFileInfo) Sys() any           { return nil }

func (svc *Service) queuePathIngestion(ctx context.Context, projectID string, relativePath string) (string, error) {
	if svc.ingest == nil {
		return "", nil
	}
	run, err := svc.ingest.IngestPath(ctx, projectID, relativePath, projectingestion.TriggerLive)
	if err != nil {
		return "", ErrIngestionUnsupported
	}
	return run.ID, nil
}

func chmodUnsupported(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.EOPNOTSUPP)
}

func ownershipUnsupported(err error) bool {
	return chmodUnsupported(err)
}

func fileOwner(info os.FileInfo) (int, int, bool) {
	if info == nil {
		return 0, 0, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(stat.Uid), int(stat.Gid), true
}

func diffPreview(relativePath string, oldContent []byte, newContent []byte, maxBytes int) string {
	var builder strings.Builder
	builder.WriteString("--- a/")
	builder.WriteString(relativePath)
	builder.WriteString("\n+++ b/")
	builder.WriteString(relativePath)
	builder.WriteString("\n")
	oldLines := strings.SplitAfter(string(oldContent), "\n")
	newLines := strings.SplitAfter(string(newContent), "\n")
	builder.WriteString("@@ -1,")
	builder.WriteString(strconv.Itoa(len(oldLines)))
	builder.WriteString(" +1,")
	builder.WriteString(strconv.Itoa(len(newLines)))
	builder.WriteString(" @@\n")
	for _, line := range oldLines {
		if line == "" {
			continue
		}
		builder.WriteString("-")
		builder.WriteString(line)
		if builder.Len() >= maxBytes {
			return builder.String()[:maxBytes]
		}
	}
	for _, line := range newLines {
		if line == "" {
			continue
		}
		builder.WriteString("+")
		builder.WriteString(line)
		if builder.Len() >= maxBytes {
			return builder.String()[:maxBytes]
		}
	}
	return builder.String()
}

func normalizeDiffScope(scope string) (string, error) {
	switch strings.TrimSpace(scope) {
	case "", DiffScopeWorkingTree:
		return DiffScopeWorkingTree, nil
	case DiffScopeStaged:
		return DiffScopeStaged, nil
	case DiffScopeHead:
		return DiffScopeHead, nil
	default:
		return "", ErrInvalidInput
	}
}

func normalizeMax(value int, fallback int, max int) int {
	if value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}

func normalizePageSize(value int) int {
	if value <= 0 {
		return MaxPageSize
	}
	if value > MaxPageSize {
		return MaxPageSize
	}
	return value
}

func parsePageToken(token string) (int, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(token)
	if err != nil || offset < 0 {
		return 0, ErrInvalidInput
	}
	return offset, nil
}

func paginate(entries []StatusEntry, offset int, pageSize int) ([]StatusEntry, string, bool) {
	if offset >= len(entries) {
		return []StatusEntry{}, "", false
	}
	end := offset + pageSize
	if end >= len(entries) {
		return entries[offset:], "", false
	}
	return entries[offset:end], strconv.Itoa(end), true
}

func splitNUL(out []byte) []string {
	raw := bytes.Split(out, []byte{0})
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		if len(item) > 0 {
			values = append(values, string(item))
		}
	}
	return values
}

func matchesPrefix(relativePath string, prefix string) bool {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return true
	}
	return relativePath == prefix || strings.HasPrefix(relativePath, prefix+"/")
}

func countDiffLines(diffText string) (int, int) {
	additions, deletions := 0, 0
	for _, line := range strings.Split(diffText, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if strings.HasPrefix(line, "+") {
			additions++
		} else if strings.HasPrefix(line, "-") {
			deletions++
		}
	}
	return additions, deletions
}

func validTextDiff(diffText string) bool {
	return strings.IndexByte(diffText, 0) < 0 && utf8.ValidString(diffText)
}

func lineCount(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	count := bytes.Count(content, []byte{'\n'})
	if !bytes.HasSuffix(content, []byte{'\n'}) {
		count++
	}
	return count
}

func (svc *Service) lockFor(key string) *sync.Mutex {
	actual, _ := svc.locks.LoadOrStore(key, &sync.Mutex{})
	return actual.(*sync.Mutex)
}
