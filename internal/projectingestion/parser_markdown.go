package projectingestion

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func ParseMarkdownHeadings(source []byte) ([]Heading, error) {
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("invalid utf-8 content")
	}
	lines := strings.Split(string(source), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}

	var headings []Heading
	inFence := false
	for i, line := range lines {
		lineNumber := i + 1
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		level, text, ok := parseATXHeading(line)
		if !ok {
			continue
		}
		headings = append(headings, Heading{
			Level:       level,
			Text:        text,
			ParentIndex: parentHeadingIndex(headings, level),
			StartLine:   lineNumber,
			EndLine:     len(lines),
		})
	}

	for i := range headings {
		for j := i + 1; j < len(headings); j++ {
			if headings[j].Level <= headings[i].Level {
				headings[i].EndLine = headings[j].StartLine - 1
				break
			}
		}
	}
	return headings, nil
}

func parseATXHeading(line string) (int, string, bool) {
	trimmedLeft := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmedLeft, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(trimmedLeft) && trimmedLeft[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, "", false
	}
	if level < len(trimmedLeft) && trimmedLeft[level] != ' ' && trimmedLeft[level] != '\t' {
		return 0, "", false
	}
	text := strings.TrimSpace(trimmedLeft[level:])
	text = strings.TrimSpace(strings.TrimRight(text, "#"))
	if text == "" {
		return 0, "", false
	}
	return level, text, true
}

func parentHeadingIndex(headings []Heading, level int) int {
	for i := len(headings) - 1; i >= 0; i-- {
		if headings[i].Level < level {
			return i
		}
	}
	return -1
}
