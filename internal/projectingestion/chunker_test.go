package projectingestion

import "testing"

func TestBuildChunks_SplitsEligibleTextByByteCapAndLineRange(t *testing.T) {
	content := []byte("alpha\nbeta\ngamma\n")
	chunkSet, safety, err := BuildChunks("docs/synthetic.md", content, SafetyOptions{
		MaxFileBytes:          1024,
		MaxChunkBytes:         11,
		SensitiveMarkerPolicy: SensitiveMarkerPolicySkipFile,
	})
	if err != nil {
		t.Fatalf("build chunks: %v", err)
	}
	if !safety.Eligible {
		t.Fatalf("expected eligible safety result, got %#v", safety)
	}
	if chunkSet.ContentSHA256 == "" {
		t.Fatal("expected content hash for eligible stored content")
	}
	if len(chunkSet.Chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %#v", chunkSet.Chunks)
	}
	for _, chunk := range chunkSet.Chunks {
		if len([]byte(chunk.Text)) > 11 {
			t.Fatalf("chunk exceeds byte cap: %#v", chunk)
		}
		if chunk.StartLine <= 0 || chunk.EndLine < chunk.StartLine {
			t.Fatalf("invalid chunk line range: %#v", chunk)
		}
		if chunk.ContentSHA256 != chunkSet.ContentSHA256 {
			t.Fatalf("expected chunk content hash to match set hash")
		}
	}
}

func TestBuildChunks_RejectsSkippedContentBeforeHashing(t *testing.T) {
	chunkSet, safety, err := BuildChunks("docs/synthetic.md", []byte("password = synthetic_marker_value\n"), SafetyOptions{
		MaxFileBytes:          1024,
		MaxChunkBytes:         32,
		SensitiveMarkerPolicy: SensitiveMarkerPolicySkipFile,
	})
	if err != nil {
		t.Fatalf("build chunks: %v", err)
	}
	if safety.Reason != SkipReasonSensitiveContent {
		t.Fatalf("expected sensitive content skip, got %#v", safety)
	}
	if chunkSet.ContentSHA256 != "" || len(chunkSet.Chunks) != 0 {
		t.Fatalf("skipped content must not produce hash or chunks: %#v", chunkSet)
	}
}

func TestBuildChunks_DoesNotSplitUTF8Runes(t *testing.T) {
	chunkSet, safety, err := BuildChunks("docs/unicode.md", []byte("aaββcc\n"), SafetyOptions{
		MaxFileBytes:          1024,
		MaxChunkBytes:         5,
		SensitiveMarkerPolicy: SensitiveMarkerPolicySkipFile,
	})
	if err != nil {
		t.Fatalf("build chunks: %v", err)
	}
	if !safety.Eligible {
		t.Fatalf("expected eligible safety result, got %#v", safety)
	}
	for _, chunk := range chunkSet.Chunks {
		if string([]byte(chunk.Text)) != chunk.Text {
			t.Fatalf("chunk text should remain valid utf-8: %#v", chunk)
		}
		if len([]byte(chunk.Text)) > 5 {
			t.Fatalf("chunk exceeds byte cap: %#v", chunk)
		}
	}
}

func TestBuildChunks_RejectsRuneWiderThanByteCap(t *testing.T) {
	_, safety, err := BuildChunks("docs/unicode.md", []byte("β\n"), SafetyOptions{
		MaxFileBytes:          1024,
		MaxChunkBytes:         1,
		SensitiveMarkerPolicy: SensitiveMarkerPolicySkipFile,
	})
	if err == nil {
		t.Fatal("expected byte-cap error")
	}
	if !safety.Eligible {
		t.Fatalf("expected safety to pass before chunk byte-cap error, got %#v", safety)
	}
}
