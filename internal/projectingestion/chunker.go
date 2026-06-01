package projectingestion

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"unicode/utf8"
)

type ChunkSet struct {
	RelativePath  string
	ContentSHA256 string
	Chunks        []Chunk
}

func BuildChunks(relativePath string, content []byte, options SafetyOptions) (ChunkSet, SafetyResult, error) {
	options = normalizeSafetyOptions(options)
	safety := EvaluateSafety(relativePath, content, options)
	if !safety.Eligible {
		return ChunkSet{}, safety, nil
	}
	contentSHA256, err := EligibleContentSHA256(safety, content)
	if err != nil {
		return ChunkSet{}, safety, err
	}
	chunks, err := chunkEligibleText(safety.RelativePath, string(content), contentSHA256, options.MaxChunkBytes)
	if err != nil {
		return ChunkSet{}, safety, err
	}
	return ChunkSet{
		RelativePath:  safety.RelativePath,
		ContentSHA256: contentSHA256,
		Chunks:        chunks,
	}, safety, nil
}

func BuildChunksFromReader(relativePath string, reader io.Reader, sizeBytes int64, options SafetyOptions) (ChunkSet, SafetyResult, error) {
	options = normalizeSafetyOptions(options)
	normalizedPath, ok := normalizeRelativePath(relativePath)
	if !ok {
		return ChunkSet{}, skipped(SkipReasonUnsafePath, "", false, int(sizeBytes)), nil
	}
	if matchesDeniedPath(normalizedPath) {
		return ChunkSet{}, skipped(SkipReasonDeniedPath, "", false, int(sizeBytes)), nil
	}
	if options.SensitiveMarkerPolicy != SensitiveMarkerPolicySkipFile {
		return ChunkSet{}, skipped(SkipReasonUnsupportedPolicy, normalizedPath, true, int(sizeBytes)), nil
	}
	builder := streamingChunkBuilder{
		relativePath:     normalizedPath,
		maxChunkBytes:    options.MaxChunkBytes,
		currentStartLine: 1,
		currentEndLine:   1,
		lineNumber:       1,
	}
	hasher := sha256.New()
	scanner := bufio.NewReaderSize(reader, options.MaxChunkBytes)
	var sample []byte
	sampleChecked := false
	var tail []byte
	var total int64
	for {
		r, width, err := scanner.ReadRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ChunkSet{}, skipped(SkipReasonReadError, normalizedPath, true, int(total)), err
		}
		if r == utf8.RuneError && width == 1 {
			return ChunkSet{}, skipped(SkipReasonInvalidUTF8, normalizedPath, true, int(total)), nil
		}
		var encoded [utf8.UTFMax]byte
		n := utf8.EncodeRune(encoded[:], r)
		if n != width {
			return ChunkSet{}, skipped(SkipReasonInvalidUTF8, normalizedPath, true, int(total)), nil
		}
		piece := encoded[:n]
		total += int64(n)
		if r == 0 {
			return ChunkSet{}, skipped(SkipReasonNULByte, normalizedPath, true, int(total)), nil
		}
		if len(sample) < 4096 {
			sample = append(sample, piece...)
			if len(sample) >= 4096 {
				sampleChecked = true
				if looksBinary(sample) {
					return ChunkSet{}, skipped(SkipReasonBinaryContent, normalizedPath, true, int(total)), nil
				}
			}
		}
		hasher.Write(piece)
		tail = append(tail, piece...)
		if len(tail) > 4096 {
			tail = tail[len(tail)-4096:]
		}
		if containsSensitiveContent(tail) {
			return ChunkSet{}, skipped(SkipReasonSensitiveContent, normalizedPath, true, int(total)), nil
		}
		if err := builder.appendRune(r, piece); err != nil {
			return ChunkSet{}, skipped(SkipReasonChunkError, normalizedPath, true, int(total)), err
		}
	}
	if !sampleChecked && looksBinary(sample) {
		return ChunkSet{}, skipped(SkipReasonBinaryContent, normalizedPath, true, int(total)), nil
	}
	if sizeBytes >= 0 && total != sizeBytes {
		sizeBytes = total
	}
	contentSHA256 := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	builder.contentSHA256 = contentSHA256
	chunks := builder.finish()
	return ChunkSet{RelativePath: normalizedPath, ContentSHA256: contentSHA256, Chunks: chunks}, SafetyResult{
		Eligible: true, RelativePath: normalizedPath, RelativePathSafe: true, Reason: SkipReasonNone, SizeBytes: sizeBytes,
	}, nil
}

type streamingChunkBuilder struct {
	relativePath     string
	contentSHA256    string
	maxChunkBytes    int
	chunks           []Chunk
	current          []byte
	currentStartLine int
	currentEndLine   int
	currentByteStart int
	byteOffset       int
	lineNumber       int
}

func (builder *streamingChunkBuilder) appendRune(r rune, piece []byte) error {
	if len(piece) > builder.maxChunkBytes {
		return fmt.Errorf("rune at byte offset %d exceeds max chunk bytes", builder.byteOffset)
	}
	if len(builder.current)+len(piece) > builder.maxChunkBytes {
		builder.flush(builder.byteOffset)
	}
	if len(builder.current) == 0 {
		builder.currentStartLine = builder.lineNumber
		builder.currentByteStart = builder.byteOffset
	}
	builder.current = append(builder.current, piece...)
	builder.currentEndLine = builder.lineNumber
	builder.byteOffset += len(piece)
	if r == '\n' {
		builder.lineNumber++
	}
	return nil
}

func (builder *streamingChunkBuilder) flush(byteEnd int) {
	if len(builder.current) == 0 {
		return
	}
	index := len(builder.chunks)
	builder.chunks = append(builder.chunks, Chunk{
		ID: fmt.Sprintf("chunk-%d", index), Index: index, RelativePath: builder.relativePath,
		StartLine: builder.currentStartLine, EndLine: builder.currentEndLine,
		ByteStart: builder.currentByteStart, ByteEnd: byteEnd, Text: string(builder.current),
		ContentSHA256: builder.contentSHA256,
	})
	builder.current = nil
	builder.currentStartLine = builder.lineNumber
	builder.currentEndLine = builder.lineNumber
	builder.currentByteStart = byteEnd
}

func (builder *streamingChunkBuilder) finish() []Chunk {
	builder.flush(builder.byteOffset)
	for i := range builder.chunks {
		builder.chunks[i].ContentSHA256 = builder.contentSHA256
	}
	return builder.chunks
}

func chunkEligibleText(relativePath string, text string, contentSHA256 string, maxChunkBytes int) ([]Chunk, error) {
	if maxChunkBytes <= 0 {
		return nil, fmt.Errorf("max chunk bytes must be positive")
	}
	if text == "" {
		return nil, nil
	}

	var chunks []Chunk
	var current []byte
	currentStartLine := 1
	currentEndLine := 1
	currentByteStart := 0
	byteOffset := 0
	lineNumber := 1

	flush := func(byteEnd int) {
		if len(current) == 0 {
			return
		}
		index := len(chunks)
		chunks = append(chunks, Chunk{
			ID:            fmt.Sprintf("chunk-%d", index),
			Index:         index,
			RelativePath:  relativePath,
			StartLine:     currentStartLine,
			EndLine:       currentEndLine,
			ByteStart:     currentByteStart,
			ByteEnd:       byteEnd,
			Text:          string(current),
			ContentSHA256: contentSHA256,
		})
		current = nil
		currentStartLine = lineNumber
		currentEndLine = lineNumber
		currentByteStart = byteEnd
	}

	for len(text) > 0 {
		r, width := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError && width == 1 {
			return nil, fmt.Errorf("invalid utf-8 content")
		}
		if width > maxChunkBytes {
			return nil, fmt.Errorf("rune at byte offset %d exceeds max chunk bytes", byteOffset)
		}
		if len(current)+width > maxChunkBytes {
			flush(byteOffset)
		}
		if len(current) == 0 {
			currentStartLine = lineNumber
			currentByteStart = byteOffset
		}
		current = append(current, text[:width]...)
		currentEndLine = lineNumber
		byteOffset += width
		text = text[width:]
		if r == '\n' {
			lineNumber++
		}
	}
	flush(byteOffset)
	return chunks, nil
}
