package projectingestion

const streamingSensitiveWindowBytes = 8192

type streamingSensitiveScanner struct {
	window []byte
}

func newStreamingSensitiveScanner() *streamingSensitiveScanner {
	return &streamingSensitiveScanner{}
}

func (scanner *streamingSensitiveScanner) Write(piece []byte) bool {
	if scanner == nil || len(piece) == 0 {
		return false
	}
	scanner.window = append(scanner.window, piece...)
	if len(scanner.window) > streamingSensitiveWindowBytes {
		scanner.window = scanner.window[len(scanner.window)-streamingSensitiveWindowBytes:]
	}
	return containsSensitiveContent(scanner.window)
}
