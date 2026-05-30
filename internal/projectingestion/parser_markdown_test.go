package projectingestion

import "testing"

func TestParseMarkdownHeadings_ExtractsHierarchyAndLineRanges(t *testing.T) {
	source := []byte(`# Root
intro
## Child
text
` + "```" + `
# Ignored
` + "```" + `
### Grandchild
## Sibling ##
`)

	headings, err := ParseMarkdownHeadings(source)
	if err != nil {
		t.Fatalf("parse markdown headings: %v", err)
	}
	if len(headings) != 4 {
		t.Fatalf("expected four headings, got %#v", headings)
	}

	assertHeading(t, headings[0], 1, "Root", -1, 1, 10)
	assertHeading(t, headings[1], 2, "Child", 0, 3, 8)
	assertHeading(t, headings[2], 3, "Grandchild", 1, 8, 8)
	assertHeading(t, headings[3], 2, "Sibling", 0, 9, 10)
}

func TestParseMarkdownHeadings_RejectsInvalidUTF8(t *testing.T) {
	if _, err := ParseMarkdownHeadings([]byte{0xff, 0xfe}); err == nil {
		t.Fatal("expected invalid utf-8 error")
	}
}

func assertHeading(t *testing.T, heading Heading, level int, text string, parentIndex int, startLine int, endLine int) {
	t.Helper()
	if heading.Level != level || heading.Text != text || heading.ParentIndex != parentIndex || heading.StartLine != startLine || heading.EndLine != endLine {
		t.Fatalf("unexpected heading: %#v", heading)
	}
}
