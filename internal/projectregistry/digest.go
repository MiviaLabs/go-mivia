package projectregistry

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
	"strconv"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
)

const (
	DigestStatusCompleted = "completed"
	DigestStatusFailed    = "failed"
)

var (
	ErrDigestProjectNotFound = ErrProjectNotFound
	ErrDigestProjectDisabled = errors.New("digest project disabled")
	ErrDigestUnsupported     = errors.New("digest unsupported")
)

type DigestRun struct {
	ID             string
	ProjectID      string
	GraphNamespace string
	Status         string
	FilesScanned   int
	FilesStored    int
	FilesSkipped   int
	StartedAt      time.Time
	CompletedAt    time.Time
}

type DigestRunMetadata struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	GraphNamespace string    `json:"graph_namespace"`
	Status         string    `json:"status"`
	FilesScanned   int       `json:"files_scanned"`
	FilesStored    int       `json:"files_stored"`
	FilesSkipped   int       `json:"files_skipped"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at"`
}

type DigestService struct {
	registry *Registry
	graph    digestGraph
	now      func() time.Time
	newID    func(Project, time.Time) string
}

type digestGraph interface {
	PutNode(context.Context, ladybug.Node) error
	GetNode(context.Context, string, string) (ladybug.Node, error)
	PutRelationship(context.Context, ladybug.Relationship) error
}

type digestCounts struct {
	scanned int
	stored  int
	skipped int
}

type fileDigest struct {
	nodeID           string
	relativePath     string
	relativePathHash string
	extension        string
	languageHint     string
	sizeBytes        int64
	modifiedAt       time.Time
	metadataSHA256   string
}

func NewDigestService(registry *Registry, graph digestGraph) *DigestService {
	return &DigestService{
		registry: registry,
		graph:    graph,
		now:      func() time.Time { return time.Now().UTC() },
		newID:    defaultDigestRunID,
	}
}

func (svc *DigestService) DigestProject(ctx context.Context, projectID string) (DigestRun, error) {
	if svc == nil || svc.registry == nil {
		return DigestRun{}, ErrProjectNotFound
	}
	project, ok := svc.registry.Get(strings.TrimSpace(projectID))
	if !ok {
		return DigestRun{}, ErrProjectNotFound
	}
	return svc.Digest(ctx, project)
}

func (svc *DigestService) GetDigestRun(ctx context.Context, projectID string, runID string) (DigestRun, error) {
	if svc == nil || svc.graph == nil {
		return DigestRun{}, fmt.Errorf("%w: graph reader is required", ErrDigestUnsupported)
	}
	node, err := svc.graph.GetNode(ctx, "DigestRun", strings.TrimSpace(runID))
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return DigestRun{}, ErrProjectNotFound
	}
	if err != nil {
		return DigestRun{}, err
	}
	run, err := digestRunFromNode(node)
	if err != nil {
		return DigestRun{}, err
	}
	if run.ProjectID != strings.TrimSpace(projectID) {
		return DigestRun{}, ErrProjectNotFound
	}
	return run, nil
}

func (svc *DigestService) Digest(ctx context.Context, project Project) (DigestRun, error) {
	if svc == nil || svc.graph == nil {
		return DigestRun{}, fmt.Errorf("%w: graph writer is required", ErrDigestUnsupported)
	}
	startedAt := svc.now().UTC()
	run := DigestRun{
		ID:             svc.newID(project, startedAt),
		ProjectID:      project.ID,
		GraphNamespace: project.GraphNamespace,
		Status:         DigestStatusCompleted,
		StartedAt:      startedAt,
	}
	normalizedProject, err := normalizeDigestProject(project)
	if err != nil {
		return run, err
	}
	project = normalizedProject
	if err := svc.putProjectNode(ctx, project); err != nil {
		return run, err
	}

	files, counts, err := collectProjectFiles(project)
	run.FilesScanned = counts.scanned
	run.FilesStored = counts.stored
	run.FilesSkipped = counts.skipped
	if err != nil {
		run.Status = DigestStatusFailed
		run.CompletedAt = svc.now().UTC()
		_ = svc.putDigestRunNode(ctx, run)
		_ = svc.putProjectDigestRunRelationship(ctx, run)
		return run, err
	}

	for _, file := range files {
		if err := svc.putRepoFileNode(ctx, project, file); err != nil {
			run.Status = DigestStatusFailed
			run.CompletedAt = svc.now().UTC()
			_ = svc.putDigestRunNode(ctx, run)
			_ = svc.putProjectDigestRunRelationship(ctx, run)
			return run, err
		}
		if err := svc.putProjectRepoFileRelationship(ctx, project, file); err != nil {
			run.Status = DigestStatusFailed
			run.CompletedAt = svc.now().UTC()
			_ = svc.putDigestRunNode(ctx, run)
			_ = svc.putProjectDigestRunRelationship(ctx, run)
			return run, err
		}
	}

	run.CompletedAt = svc.now().UTC()
	if err := svc.putDigestRunNode(ctx, run); err != nil {
		return run, err
	}
	if err := svc.putProjectDigestRunRelationship(ctx, run); err != nil {
		return run, err
	}
	return run, nil
}

func normalizeDigestProject(project Project) (Project, error) {
	if !project.Enabled {
		return Project{}, ErrDigestProjectDisabled
	}
	if project.DigestMode != DigestModeMetadataOnly {
		return Project{}, fmt.Errorf("%w: digest_mode must be %q", ErrDigestUnsupported, DigestModeMetadataOnly)
	}
	if project.UpdatePolicy != UpdatePolicyManual {
		return Project{}, fmt.Errorf("%w: update_policy must be %q", ErrDigestUnsupported, UpdatePolicyManual)
	}
	if project.FollowSymlinks {
		return Project{}, fmt.Errorf("%w: follow_symlinks must be false", ErrDigestUnsupported)
	}
	if project.RootPath == "" {
		return Project{}, fmt.Errorf("%w: root path is required", ErrDigestUnsupported)
	}
	cleanRootPath, canonicalRootPath, err := validateRootPath(project.RootPath, true)
	if err != nil {
		return Project{}, fmt.Errorf("%w: invalid root path", ErrDigestUnsupported)
	}
	if project.CanonicalRootPath != "" && project.CanonicalRootPath != canonicalRootPath {
		return Project{}, fmt.Errorf("%w: canonical root path mismatch", ErrDigestUnsupported)
	}
	project.RootPath = cleanRootPath
	project.CanonicalRootPath = canonicalRootPath
	return project, nil
}

func collectProjectFiles(project Project) ([]fileDigest, digestCounts, error) {
	root := project.CanonicalRootPath
	if root == "" {
		root = project.RootPath
	}
	root = filepath.Clean(root)
	files := make([]fileDigest, 0)
	var counts digestCounts

	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			relative, _ := relativeProjectPath(root, filePath)
			return fmt.Errorf("walk failed for relative path %q", relative)
		}
		if filePath == root {
			return nil
		}
		relative, ok := relativeProjectPath(root, filePath)
		if !ok {
			return fmt.Errorf("walk escaped project root")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			counts.skipped++
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if matchesAnyPattern(project.Exclude, relative) {
				counts.skipped++
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat failed for relative path %q", relative)
		}
		if !info.Mode().IsRegular() {
			counts.skipped++
			return nil
		}

		counts.scanned++
		if !includedByPatterns(project.Include, relative) || matchesAnyPattern(project.Exclude, relative) {
			counts.skipped++
			return nil
		}

		extension := strings.ToLower(path.Ext(relative))
		language := languageHint(relative, extension)
		files = append(files, fileDigest{
			nodeID:           repoFileNodeID(project.GraphNamespace, relative),
			relativePath:     relative,
			relativePathHash: hashString(relative),
			extension:        extension,
			languageHint:     language,
			sizeBytes:        info.Size(),
			modifiedAt:       info.ModTime().UTC(),
			metadataSHA256:   fileMetadataSHA256(relative, extension, language, info.Size(), info.ModTime().UTC()),
		})
		counts.stored++
		return nil
	})
	if err != nil {
		return nil, counts, err
	}
	return files, counts, nil
}

func (svc *DigestService) putProjectNode(ctx context.Context, project Project) error {
	return svc.graph.PutNode(ctx, ladybug.Node{
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

func (svc *DigestService) putRepoFileNode(ctx context.Context, project Project, file fileDigest) error {
	return svc.graph.PutNode(ctx, ladybug.Node{
		Label: "RepoFile",
		ID:    file.nodeID,
		Properties: map[string]string{
			"id":                 file.nodeID,
			"project_id":         project.ID,
			"graph_namespace":    project.GraphNamespace,
			"relative_path":      file.relativePath,
			"relative_path_hash": file.relativePathHash,
			"extension":          file.extension,
			"language_hint":      file.languageHint,
			"size_bytes":         strconv.FormatInt(file.sizeBytes, 10),
			"modified_at":        file.modifiedAt.Format(time.RFC3339Nano),
			"metadata_sha256":    file.metadataSHA256,
		},
	})
}

func (svc *DigestService) putDigestRunNode(ctx context.Context, run DigestRun) error {
	return svc.graph.PutNode(ctx, ladybug.Node{
		Label: "DigestRun",
		ID:    run.ID,
		Properties: map[string]string{
			"id":              run.ID,
			"project_id":      run.ProjectID,
			"graph_namespace": run.GraphNamespace,
			"status":          run.Status,
			"files_scanned":   strconv.Itoa(run.FilesScanned),
			"files_stored":    strconv.Itoa(run.FilesStored),
			"files_skipped":   strconv.Itoa(run.FilesSkipped),
			"started_at":      run.StartedAt.Format(time.RFC3339Nano),
			"completed_at":    run.CompletedAt.Format(time.RFC3339Nano),
		},
	})
}

func (svc *DigestService) putProjectRepoFileRelationship(ctx context.Context, project Project, file fileDigest) error {
	return svc.graph.PutRelationship(ctx, ladybug.Relationship{
		Type: "PROJECT_HAS_REPO_FILE",
		From: ladybug.NodeRef{Label: "Project", ID: project.ID},
		To:   ladybug.NodeRef{Label: "RepoFile", ID: file.nodeID},
		Properties: map[string]string{
			"project_id":      project.ID,
			"graph_namespace": project.GraphNamespace,
		},
	})
}

func (svc *DigestService) putProjectDigestRunRelationship(ctx context.Context, run DigestRun) error {
	return svc.graph.PutRelationship(ctx, ladybug.Relationship{
		Type: "PROJECT_HAS_DIGEST_RUN",
		From: ladybug.NodeRef{Label: "Project", ID: run.ProjectID},
		To:   ladybug.NodeRef{Label: "DigestRun", ID: run.ID},
		Properties: map[string]string{
			"project_id":      run.ProjectID,
			"graph_namespace": run.GraphNamespace,
		},
	})
}

func digestRunFromNode(node ladybug.Node) (DigestRun, error) {
	filesScanned, err := strconv.Atoi(node.Properties["files_scanned"])
	if err != nil {
		return DigestRun{}, err
	}
	filesStored, err := strconv.Atoi(node.Properties["files_stored"])
	if err != nil {
		return DigestRun{}, err
	}
	filesSkipped, err := strconv.Atoi(node.Properties["files_skipped"])
	if err != nil {
		return DigestRun{}, err
	}
	startedAt, err := time.Parse(time.RFC3339Nano, node.Properties["started_at"])
	if err != nil {
		return DigestRun{}, err
	}
	completedAt, err := time.Parse(time.RFC3339Nano, node.Properties["completed_at"])
	if err != nil {
		return DigestRun{}, err
	}
	return DigestRun{
		ID:             node.Properties["id"],
		ProjectID:      node.Properties["project_id"],
		GraphNamespace: node.Properties["graph_namespace"],
		Status:         node.Properties["status"],
		FilesScanned:   filesScanned,
		FilesStored:    filesStored,
		FilesSkipped:   filesSkipped,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
	}, nil
}

func MetadataForDigestRun(run DigestRun) DigestRunMetadata {
	return DigestRunMetadata{
		ID:             run.ID,
		ProjectID:      run.ProjectID,
		GraphNamespace: run.GraphNamespace,
		Status:         run.Status,
		FilesScanned:   run.FilesScanned,
		FilesStored:    run.FilesStored,
		FilesSkipped:   run.FilesSkipped,
		StartedAt:      run.StartedAt,
		CompletedAt:    run.CompletedAt,
	}
}

func includedByPatterns(patterns []string, relative string) bool {
	if len(patterns) == 0 {
		return true
	}
	return matchesAnyPattern(patterns, relative)
}

func mayIncludeByPatterns(patterns []string, relative string) bool {
	if len(patterns) == 0 || strings.Trim(relative, "/") == "" {
		return true
	}
	relative = strings.Trim(relative, "/")
	for _, pattern := range patterns {
		if mayMatchDescendant(pattern, relative) {
			return true
		}
	}
	return false
}

func mayMatchDescendant(pattern string, relative string) bool {
	pattern = strings.Trim(pattern, "/")
	if pattern == relative || strings.HasPrefix(pattern, relative+"/") {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return prefix == relative || strings.HasPrefix(prefix, relative+"/") || strings.HasPrefix(relative, prefix+"/")
	}
	patternParts := strings.Split(pattern, "/")
	relativeParts := strings.Split(relative, "/")
	if len(patternParts) > 0 && patternParts[0] == "**" {
		return true
	}
	if len(patternParts) < len(relativeParts) {
		return false
	}
	for i, part := range relativeParts {
		if patternParts[i] == "**" {
			return true
		}
		matched, err := path.Match(patternParts[i], part)
		if err != nil || !matched {
			return false
		}
	}
	return true
}

func matchesAnyPattern(patterns []string, relative string) bool {
	for _, pattern := range patterns {
		if matchSlashPattern(pattern, relative) {
			return true
		}
	}
	return false
}

func matchSlashPattern(pattern string, relative string) bool {
	pattern = strings.Trim(pattern, "/")
	relative = strings.Trim(relative, "/")
	if pattern == relative {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		if relative == prefix || strings.HasPrefix(relative, prefix+"/") {
			return true
		}
	}
	return matchPatternParts(strings.Split(pattern, "/"), strings.Split(relative, "/"))
}

func matchPatternParts(patternParts []string, relativeParts []string) bool {
	if len(patternParts) == 0 {
		return len(relativeParts) == 0
	}
	if patternParts[0] == "**" {
		if matchPatternParts(patternParts[1:], relativeParts) {
			return true
		}
		for len(relativeParts) > 0 {
			relativeParts = relativeParts[1:]
			if matchPatternParts(patternParts[1:], relativeParts) {
				return true
			}
		}
		return false
	}
	if len(relativeParts) == 0 {
		return false
	}
	matched, err := path.Match(patternParts[0], relativeParts[0])
	if err != nil || !matched {
		return false
	}
	return matchPatternParts(patternParts[1:], relativeParts[1:])
}

func relativeProjectPath(root string, filePath string) (string, bool) {
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

func repoFileNodeID(graphNamespace string, relativePath string) string {
	return graphNamespace + ":" + hashString(relativePath)
}

func defaultDigestRunID(project Project, startedAt time.Time) string {
	sum := sha256.Sum256([]byte(project.ID + "\x00" + project.GraphNamespace + "\x00" + startedAt.Format(time.RFC3339Nano)))
	return "digest_" + hex.EncodeToString(sum[:8])
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func languageHint(relativePath string, extension string) string {
	switch extension {
	case ".go":
		return "go"
	case ".md", ".markdown":
		return "markdown"
	case ".toml":
		return "toml"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".sql":
		return "sql"
	case ".sh":
		return "shell"
	}
	switch path.Base(relativePath) {
	case "Dockerfile":
		return "dockerfile"
	case "Makefile":
		return "make"
	default:
		return strings.TrimPrefix(extension, ".")
	}
}

func fileMetadataSHA256(relativePath string, extension string, language string, sizeBytes int64, modifiedAt time.Time) string {
	value := relativePath + "\x00" +
		extension + "\x00" +
		language + "\x00" +
		strconv.FormatInt(sizeBytes, 10) + "\x00" +
		modifiedAt.Format(time.RFC3339Nano)
	return hashString(value)
}
