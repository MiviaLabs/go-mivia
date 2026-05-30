package projectingestion

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research/redaction"
)

const SensitiveMarkerPolicySkipFile = "skip_file"

var windowsRootPattern = regexp.MustCompile(`^[A-Za-z]:($|[\\/])`)

var deniedPathPatterns = []string{
	".git/**",
	"data/**",
	"secrets/**",
	".env*",
	"lib-ladybug/**",
	"node_modules/**",
	"vendor/**",
	".venv/**",
	"dist/**",
	"build/**",
	"coverage/**",
	"*.pem",
	"*.key",
	"*.p12",
}

var deniedPathSegments = map[string]struct{}{
	".git":         {},
	"data":         {},
	"secrets":      {},
	"lib-ladybug":  {},
	"node_modules": {},
	"vendor":       {},
	".venv":        {},
	"dist":         {},
	"build":        {},
	"coverage":     {},
}

var contentMarkerPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|auth[_-]?token|secret|password)\s*(=|:\s*)\s*["']?[^=,\s]+`),
	regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
}

type SafetyOptions struct {
	MaxFileBytes          int64
	MaxChunkBytes         int
	SensitiveMarkerPolicy string
}

type SafetyResult struct {
	Eligible         bool
	RelativePath     string
	RelativePathSafe bool
	Reason           SkipReason
	SizeBytes        int64
}

func DefaultSafetyOptions() SafetyOptions {
	return SafetyOptions{
		MaxFileBytes:          1 << 20,
		MaxChunkBytes:         16 << 10,
		SensitiveMarkerPolicy: SensitiveMarkerPolicySkipFile,
	}
}

func EvaluateSafety(relativePath string, content []byte, options SafetyOptions) SafetyResult {
	options = normalizeSafetyOptions(options)
	normalizedPath, ok := normalizeRelativePath(relativePath)
	if !ok {
		return skipped(SkipReasonUnsafePath, "", false, len(content))
	}
	if matchesDeniedPath(normalizedPath) || redaction.ContainsSensitive(normalizedPath) {
		return skipped(SkipReasonDeniedPath, "", false, len(content))
	}
	if options.SensitiveMarkerPolicy != SensitiveMarkerPolicySkipFile {
		return skipped(SkipReasonUnsupportedPolicy, normalizedPath, true, len(content))
	}
	if int64(len(content)) > options.MaxFileBytes {
		return skipped(SkipReasonFileTooLarge, normalizedPath, true, len(content))
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return skipped(SkipReasonNULByte, normalizedPath, true, len(content))
	}
	if looksBinary(content) {
		return skipped(SkipReasonBinaryContent, normalizedPath, true, len(content))
	}
	if !utf8.Valid(content) {
		return skipped(SkipReasonInvalidUTF8, normalizedPath, true, len(content))
	}
	if containsSensitiveContent(content) {
		return skipped(SkipReasonSensitiveContent, normalizedPath, true, len(content))
	}
	return SafetyResult{
		Eligible:         true,
		RelativePath:     normalizedPath,
		RelativePathSafe: true,
		Reason:           SkipReasonNone,
		SizeBytes:        int64(len(content)),
	}
}

func EligibleContentSHA256(result SafetyResult, content []byte) (string, error) {
	if !result.Eligible {
		return "", fmt.Errorf("content hash is available only for eligible content: %s", result.Reason)
	}
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func normalizeSafetyOptions(options SafetyOptions) SafetyOptions {
	defaults := DefaultSafetyOptions()
	if options.MaxFileBytes <= 0 {
		options.MaxFileBytes = defaults.MaxFileBytes
	}
	if options.MaxChunkBytes <= 0 {
		options.MaxChunkBytes = defaults.MaxChunkBytes
	}
	if options.SensitiveMarkerPolicy == "" {
		options.SensitiveMarkerPolicy = defaults.SensitiveMarkerPolicy
	}
	return options
}

func skipped(reason SkipReason, relativePath string, relativePathSafe bool, size int) SafetyResult {
	return SafetyResult{
		Eligible:         false,
		RelativePath:     relativePath,
		RelativePathSafe: relativePathSafe,
		Reason:           reason,
		SizeBytes:        int64(size),
	}
}

func normalizeRelativePath(relativePath string) (string, bool) {
	if relativePath == "" || strings.TrimSpace(relativePath) == "" {
		return "", false
	}
	if strings.ContainsRune(relativePath, '\x00') || strings.Contains(relativePath, "\\") {
		return "", false
	}
	if strings.HasPrefix(relativePath, "/") || strings.HasPrefix(relativePath, "//") || windowsRootPattern.MatchString(relativePath) {
		return "", false
	}
	for _, part := range strings.Split(relativePath, "/") {
		if part == ".." {
			return "", false
		}
	}
	cleaned := path.Clean(relativePath)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	return cleaned, true
}

func matchesDeniedPath(relativePath string) bool {
	base := path.Base(relativePath)
	if strings.HasPrefix(base, ".env") {
		return true
	}
	for _, part := range strings.Split(relativePath, "/") {
		if _, ok := deniedPathSegments[part]; ok {
			return true
		}
		if strings.HasPrefix(part, ".env") {
			return true
		}
	}
	for _, pattern := range deniedPathPatterns {
		if matchesPathPattern(pattern, relativePath) {
			return true
		}
	}
	return false
}

func matchesPathPattern(pattern string, relativePath string) bool {
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return relativePath == prefix || strings.HasPrefix(relativePath, prefix+"/")
	}
	if ok, _ := path.Match(pattern, relativePath); ok {
		return true
	}
	if ok, _ := path.Match(pattern, path.Base(relativePath)); ok {
		return true
	}
	return false
}

func looksBinary(content []byte) bool {
	if len(content) == 0 {
		return false
	}
	control := 0
	for _, b := range content {
		if b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		if b < 0x20 || b == 0x7f {
			control++
		}
	}
	return control > 0 && control*100/len(content) > 30
}

func containsSensitiveContent(content []byte) bool {
	value := string(content)
	if redaction.Redact(value) != value {
		return true
	}
	for _, pattern := range contentMarkerPatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}
