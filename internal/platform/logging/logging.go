package logging

import (
	"log/slog"
	"os"
)

func New(service string) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(handler).With(slog.String("service", service))
}
