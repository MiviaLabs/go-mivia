package projectingestion

const streamingSensitiveWindowBytes = 8192

type streamingSensitiveScanner struct {
	relativePath string
	window       []byte
}

func newStreamingSensitiveScanner(relativePath string) *streamingSensitiveScanner {
	return &streamingSensitiveScanner{relativePath: relativePath}
}

func (scanner *streamingSensitiveScanner) Write(piece []byte) bool {
	if scanner == nil || len(piece) == 0 {
		return false
	}
	scanner.append(piece)
	return containsSensitiveContentForPath(scanner.relativePath, scanner.window)
}

func (scanner *streamingSensitiveScanner) WriteHardMarkers(piece []byte) bool {
	if scanner == nil || len(piece) == 0 {
		return false
	}
	scanner.append(piece)
	value := string(scanner.window)
	return containsPIIMarker(value) || containsContentMarkerPattern(value, contentMarkerPatterns)
}

func (scanner *streamingSensitiveScanner) append(piece []byte) {
	scanner.window = append(scanner.window, piece...)
	if len(scanner.window) > streamingSensitiveWindowBytes {
		scanner.window = scanner.window[len(scanner.window)-streamingSensitiveWindowBytes:]
	}
}
