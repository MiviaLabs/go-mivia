package projectingestion

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	jsFunctionPattern = regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][A-Za-z0-9_$]*)\b`)
	jsClassPattern    = regexp.MustCompile(`^\s*(?:export\s+)?class\s+([A-Za-z_$][A-Za-z0-9_$]*)\b`)
	jsExportPattern   = regexp.MustCompile(`^\s*export\s+(?:const|let|var|type|interface|enum)\s+([A-Za-z_$][A-Za-z0-9_$]*)\b`)
	jsArrowPattern    = regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][A-Za-z0-9_$]*)\s*=>`)
)

func ParseJavaScriptLikeSymbols(source []byte) ([]Symbol, error) {
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("invalid utf-8 content")
	}
	lines := strings.Split(string(source), "\n")
	symbols := make([]Symbol, 0)
	for index, line := range lines {
		lineNumber := index + 1
		switch {
		case jsFunctionPattern.MatchString(line):
			match := jsFunctionPattern.FindStringSubmatch(line)
			symbols = append(symbols, Symbol{Kind: SymbolKindFunction, Name: match[1], StartLine: lineNumber, EndLine: lineNumber})
		case jsArrowPattern.MatchString(line):
			match := jsArrowPattern.FindStringSubmatch(line)
			symbols = append(symbols, Symbol{Kind: SymbolKindFunction, Name: match[1], StartLine: lineNumber, EndLine: lineNumber})
		case jsClassPattern.MatchString(line):
			match := jsClassPattern.FindStringSubmatch(line)
			symbols = append(symbols, Symbol{Kind: SymbolKindClass, Name: match[1], StartLine: lineNumber, EndLine: lineNumber})
		case jsExportPattern.MatchString(line):
			match := jsExportPattern.FindStringSubmatch(line)
			symbols = append(symbols, Symbol{Kind: SymbolKindExport, Name: match[1], StartLine: lineNumber, EndLine: lineNumber})
		}
	}
	return symbols, nil
}
