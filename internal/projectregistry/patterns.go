package projectregistry

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var windowsDrivePattern = regexp.MustCompile(`^[A-Za-z]:($|[\\/])`)

func validatePatterns(kind string, patterns []string) error {
	for i, pattern := range patterns {
		if err := validatePattern(pattern); err != nil {
			return fmt.Errorf("%s[%d]: %w", kind, i, err)
		}
	}
	return nil
}

func validatePattern(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("pattern must not be empty")
	}
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("pattern must not be blank")
	}
	if strings.ContainsRune(pattern, '\x00') {
		return fmt.Errorf("pattern must not contain NUL bytes")
	}
	if strings.Contains(pattern, "\\") {
		return fmt.Errorf("pattern must use slash separators")
	}
	if strings.HasPrefix(pattern, "/") || filepath.IsAbs(pattern) {
		return fmt.Errorf("pattern must be project-root-relative")
	}
	if strings.HasPrefix(pattern, "//") || strings.HasPrefix(pattern, `\\`) || windowsDrivePattern.MatchString(pattern) {
		return fmt.Errorf("pattern must not contain Windows or UNC roots")
	}
	for _, part := range strings.Split(pattern, "/") {
		if part == ".." {
			return fmt.Errorf("pattern must not contain path traversal")
		}
	}
	return nil
}

func defaultExcludePatterns(projectRoot string, storagePaths ...string) []string {
	excludes := []string{
		".git/**",
		".hg/**",
		".svn/**",
		".claude/worktrees/**",
		"data/**",
		"node_modules/**",
		"vendor/**",
		"dist/**",
		"build/**",
		"out/**",
		".next/**",
		"coverage/**",
		"target/**",
		"secrets/**",
		".env*",
		"lib-ladybug/**",
	}
	for _, storagePath := range storagePaths {
		if relative, ok := relativeStoragePath(projectRoot, storagePath); ok {
			excludes = append(excludes, relative)
		}
	}
	return dedupeStrings(excludes)
}

func relativeStoragePath(projectRoot string, storagePath string) (string, bool) {
	if storagePath == "" || storagePath == ":memory:" || projectRoot == "" {
		return "", false
	}
	absoluteStoragePath := storagePath
	var err error
	if !filepath.IsAbs(absoluteStoragePath) {
		absoluteStoragePath, err = filepath.Abs(absoluteStoragePath)
		if err != nil {
			return "", false
		}
	}
	relative, err := filepath.Rel(projectRoot, filepath.Clean(absoluteStoragePath))
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, "../") || filepath.IsAbs(relative) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func mergeExcludePatterns(projectRoot string, projectExcludes []string, storagePaths ...string) []string {
	merged := append([]string(nil), defaultExcludePatterns(projectRoot, storagePaths...)...)
	merged = append(merged, projectExcludes...)
	return dedupeStrings(merged)
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	deduped := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		deduped = append(deduped, value)
	}
	return deduped
}
