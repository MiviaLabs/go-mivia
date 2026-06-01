package projectingestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
)

type ExtractorName string

const (
	ExtractorGoStdlibAST       ExtractorName = "go-stdlib-ast"
	ExtractorMarkdownHeading   ExtractorName = "markdown-heading"
	ExtractorInfraLightweight  ExtractorName = "infra-lightweight"
	extractorVersionOne                      = "1"
	extractorVersionTwo                      = "2"
	extractorInitErrorCategory               = "extractor_initialization_failed"
)

type ExtractorResult struct {
	ExtractorName    string
	ExtractorVersion string
	Symbols          []Symbol
	Headings         []Heading
	References       []Reference
	Calls            []Call
	Implementations  []Implementation
}

type Extractor interface {
	Name() string
	Version() string
	Supports(relative string) bool
	Validate() error
	Parse(ctx context.Context, relative string, content []byte) (ExtractorResult, error)
}

type ExtractorRegistry struct {
	extractors []Extractor
}

func NewExtractorRegistry(extractors ...Extractor) *ExtractorRegistry {
	return &ExtractorRegistry{extractors: append([]Extractor(nil), extractors...)}
}

func NewDefaultExtractorRegistry() *ExtractorRegistry {
	return NewExtractorRegistry(
		staticExtractor{
			name:    string(ExtractorGoStdlibAST),
			version: extractorVersionTwo,
			supports: func(relative string) bool {
				return strings.EqualFold(path.Ext(relative), ".go")
			},
			parse: func(_ context.Context, relative string, content []byte) (ExtractorResult, error) {
				return ParseGoFileSemantic(relative, content)
			},
		},
		newTreeSitterJavaScriptExtractor(),
		newTreeSitterTypeScriptExtractor(),
		newTreeSitterTSXExtractor(),
		newTreeSitterCSharpExtractor(),
		newTreeSitterPythonExtractor(),
		newTreeSitterDartExtractor(),
		staticExtractor{
			name:    string(ExtractorMarkdownHeading),
			version: extractorVersionOne,
			supports: func(relative string) bool {
				extension := strings.ToLower(path.Ext(relative))
				return extension == ".md" || extension == ".markdown"
			},
			parse: func(_ context.Context, _ string, content []byte) (ExtractorResult, error) {
				headings, err := ParseMarkdownHeadings(content)
				return ExtractorResult{Headings: headings}, err
			},
		},
		staticExtractor{
			name:     string(ExtractorInfraLightweight),
			version:  extractorVersionOne,
			supports: supportsInfraLightweight,
			parse:    parseInfraLightweight,
		},
	)
}

func ValidateDefaultExtractorRegistry() error {
	return NewDefaultExtractorRegistry().Validate()
}

func (registry *ExtractorRegistry) Validate() error {
	if registry == nil {
		return fmt.Errorf("%s", extractorInitErrorCategory)
	}
	for _, extractor := range registry.extractors {
		if extractor == nil {
			return fmt.Errorf("%s", extractorInitErrorCategory)
		}
		if err := extractor.Validate(); err != nil {
			return fmt.Errorf("%s: %s", extractorInitErrorCategory, extractor.Name())
		}
	}
	return nil
}

func (registry *ExtractorRegistry) Extract(ctx context.Context, relative string, content []byte) (ExtractorResult, error) {
	extractor := registry.ExtractorFor(relative)
	if extractor == nil {
		return ExtractorResult{}, nil
	}
	result, err := extractor.Parse(ctx, relative, content)
	result.ExtractorName = extractor.Name()
	result.ExtractorVersion = extractor.Version()
	return result, err
}

func (registry *ExtractorRegistry) ExtractorFor(relative string) Extractor {
	if registry == nil {
		registry = NewDefaultExtractorRegistry()
	}
	for _, extractor := range registry.extractors {
		if !extractor.Supports(relative) {
			continue
		}
		return extractor
	}
	return nil
}

type staticExtractor struct {
	name     string
	version  string
	supports func(string) bool
	parse    func(context.Context, string, []byte) (ExtractorResult, error)
	validate func() error
}

func (extractor staticExtractor) Name() string {
	return extractor.name
}

func (extractor staticExtractor) Version() string {
	return extractor.version
}

func (extractor staticExtractor) Fingerprint() string {
	return extractorFingerprint(extractor.name, extractor.version, "")
}

func (extractor staticExtractor) Supports(relative string) bool {
	return extractor.supports != nil && extractor.supports(relative)
}

func (extractor staticExtractor) Validate() error {
	if extractor.name == "" || extractor.version == "" || extractor.supports == nil || extractor.parse == nil {
		return fmt.Errorf("invalid extractor")
	}
	if extractor.validate != nil {
		return extractor.validate()
	}
	return nil
}

func (extractor staticExtractor) Parse(ctx context.Context, relative string, content []byte) (ExtractorResult, error) {
	return extractor.parse(ctx, relative, content)
}

func extractorFingerprint(name string, version string, query string) string {
	sum := sha256.Sum256([]byte(name + "\x00" + version + "\x00" + query))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func supportsInfraLightweight(relative string) bool {
	extension := strings.ToLower(path.Ext(relative))
	base := strings.ToLower(path.Base(relative))
	if extension == ".asmdef" {
		return true
	}
	switch base {
	case "dockerfile", "containerfile", "makefile",
		"openapi.yaml", "openapi.yml", "openapi.json",
		"swagger.yaml", "swagger.yml", "swagger.json":
		return true
	}
	switch extension {
	case ".dockerfile", ".mk", ".sql", ".json", ".yaml", ".yml", ".toml":
		return true
	default:
		return false
	}
}

func parseInfraLightweight(_ context.Context, relative string, content []byte) (ExtractorResult, error) {
	extension := strings.ToLower(path.Ext(relative))
	base := strings.ToLower(path.Base(relative))
	if extension == ".asmdef" {
		symbols, err := ParseUnityAsmdefSymbols(content)
		return ExtractorResult{Symbols: symbols}, err
	}
	switch base {
	case "dockerfile", "containerfile":
		symbols, err := ParseDockerfileSymbols(content)
		return ExtractorResult{Symbols: symbols}, err
	case "makefile":
		symbols, err := ParseMakefileSymbols(content)
		return ExtractorResult{Symbols: symbols}, err
	case "openapi.yaml", "openapi.yml", "openapi.json", "swagger.yaml", "swagger.yml", "swagger.json":
		symbols, err := ParseOpenAPIPathSymbols(content)
		return ExtractorResult{Symbols: symbols}, err
	}
	switch extension {
	case ".dockerfile":
		symbols, err := ParseDockerfileSymbols(content)
		return ExtractorResult{Symbols: symbols}, err
	case ".mk":
		symbols, err := ParseMakefileSymbols(content)
		return ExtractorResult{Symbols: symbols}, err
	case ".sql":
		symbols, err := ParseSQLMigrationSymbols(relative, content)
		return ExtractorResult{Symbols: symbols}, err
	case ".json":
		symbols, err := ParseJSONTopLevelKeys(content)
		return ExtractorResult{Symbols: symbols}, err
	case ".yaml", ".yml", ".toml":
		symbols, err := ParseConfigTopLevelKeys(content)
		return ExtractorResult{Symbols: symbols}, err
	default:
		return ExtractorResult{}, nil
	}
}
