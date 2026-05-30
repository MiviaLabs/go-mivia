package projectingestion

import (
	"fmt"
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
