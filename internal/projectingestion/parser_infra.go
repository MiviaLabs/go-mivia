package projectingestion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	dockerStagePattern = regexp.MustCompile(`(?i)^\s*FROM\s+\S+(?:\s+AS\s+([A-Za-z0-9_.-]+))?`)
	makeTargetPattern  = regexp.MustCompile(`^([A-Za-z0-9_.%/-]+)\s*:(?:\s|$)`)
	yamlKeyPattern     = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*:\s*(?:#.*)?$`)
	tomlKeyPattern     = regexp.MustCompile(`^\s*(?:\[([A-Za-z0-9_.-]+)\]|([A-Za-z0-9_.-]+)\s*=)`)
	openAPIPathPattern = regexp.MustCompile(`^\s{2}(/[^\s:]+)\s*:\s*(?:#.*)?$`)
)

func ParseDockerfileSymbols(source []byte) ([]Symbol, error) {
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("invalid utf-8 content")
	}
	var symbols []Symbol
	for index, line := range strings.Split(string(source), "\n") {
		match := dockerStagePattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		name := match[1]
		if name == "" {
			name = "stage-" + fmt.Sprint(len(symbols))
		}
		symbols = append(symbols, Symbol{Kind: SymbolKindStage, Name: name, StartLine: index + 1, EndLine: index + 1})
	}
	return symbols, nil
}

func ParseMakefileSymbols(source []byte) ([]Symbol, error) {
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("invalid utf-8 content")
	}
	var symbols []Symbol
	for index, line := range strings.Split(string(source), "\n") {
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		match := makeTargetPattern.FindStringSubmatch(line)
		if match == nil || strings.Contains(match[1], "=") {
			continue
		}
		symbols = append(symbols, Symbol{Kind: SymbolKindTarget, Name: match[1], StartLine: index + 1, EndLine: index + 1})
	}
	return symbols, nil
}

func ParseOpenAPIPathSymbols(source []byte) ([]Symbol, error) {
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("invalid utf-8 content")
	}
	trimmed := bytes.TrimSpace(source)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return parseOpenAPIJSONPathSymbols(trimmed)
	}
	var symbols []Symbol
	inPaths := false
	for index, line := range strings.Split(string(source), "\n") {
		if strings.TrimSpace(line) == "paths:" {
			inPaths = true
			continue
		}
		if inPaths && line != "" && line[0] != ' ' {
			inPaths = false
		}
		if !inPaths {
			continue
		}
		match := openAPIPathPattern.FindStringSubmatch(line)
		if match != nil {
			symbols = append(symbols, Symbol{Kind: SymbolKindPath, Name: match[1], StartLine: index + 1, EndLine: index + 1})
		}
	}
	return symbols, nil
}

func parseOpenAPIJSONPathSymbols(source []byte) ([]Symbol, error) {
	var decoded struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(source, &decoded); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(decoded.Paths))
	for name := range decoded.Paths {
		names = append(names, name)
	}
	sort.Strings(names)
	symbols := make([]Symbol, 0, len(names))
	for _, name := range names {
		symbols = append(symbols, Symbol{Kind: SymbolKindPath, Name: name, StartLine: 1, EndLine: 1})
	}
	return symbols, nil
}

func ParseSQLMigrationSymbols(relative string, source []byte) ([]Symbol, error) {
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("invalid utf-8 content")
	}
	name := strings.TrimSuffix(path.Base(relative), path.Ext(relative))
	if name == "" {
		return nil, nil
	}
	return []Symbol{{Kind: SymbolKindMigration, Name: name, StartLine: 1, EndLine: lineCount(source)}}, nil
}

func ParseUnityAsmdefSymbols(source []byte) ([]Symbol, error) {
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("invalid utf-8 content")
	}
	var decoded struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(source, &decoded); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(decoded.Name)
	if name == "" {
		return nil, nil
	}
	return []Symbol{{Kind: SymbolKindAssembly, Name: name, StartLine: 1, EndLine: lineCount(source)}}, nil
}

func ParseJSONTopLevelKeys(source []byte) ([]Symbol, error) {
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("invalid utf-8 content")
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(source, &decoded); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(decoded))
	for key := range decoded {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	symbols := make([]Symbol, 0, len(keys))
	for _, key := range keys {
		symbols = append(symbols, Symbol{Kind: SymbolKindKey, Name: key, StartLine: 1, EndLine: 1})
	}
	return symbols, nil
}

func ParseConfigTopLevelKeys(source []byte) ([]Symbol, error) {
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("invalid utf-8 content")
	}
	seen := make(map[string]struct{})
	var symbols []Symbol
	for index, line := range strings.Split(string(source), "\n") {
		lineNumber := index + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		var name string
		if match := yamlKeyPattern.FindStringSubmatch(line); match != nil && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			name = match[1]
		}
		if match := tomlKeyPattern.FindStringSubmatch(line); name == "" && match != nil {
			if match[1] != "" {
				name = match[1]
			} else {
				name = match[2]
			}
		}
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		symbols = append(symbols, Symbol{Kind: SymbolKindKey, Name: name, StartLine: lineNumber, EndLine: lineNumber})
	}
	return symbols, nil
}

func lineCount(source []byte) int {
	if len(source) == 0 {
		return 0
	}
	return strings.Count(string(source), "\n") + 1
}
