package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

type Options struct {
	FileEnabled bool
	FilePath    string
}

type closeFunc func() error

func (fn closeFunc) Close() error {
	return fn()
}

func New(service string) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(handler).With(slog.String("service", service))
}

func NewWithOptions(service string, options Options) (*slog.Logger, io.Closer, error) {
	writer := io.Writer(os.Stdout)
	var file *os.File
	if options.FileEnabled {
		if err := os.MkdirAll(filepath.Dir(options.FilePath), 0o700); err != nil {
			return nil, nil, err
		}
		opened, err := os.OpenFile(options.FilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, nil, err
		}
		file = opened
		writer = io.MultiWriter(os.Stdout, file)
	}
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo})
	closer := closeFunc(func() error {
		if file == nil {
			return nil
		}
		return file.Close()
	})
	return slog.New(handler).With(slog.String("service", service)), closer, nil
}
