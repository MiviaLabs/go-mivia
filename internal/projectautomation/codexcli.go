package projectautomation

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type CodexCommand struct {
	Path      string
	Args      []string
	Env       []string
	StdinFile string
	Dir       string
	Timeout   time.Duration
}

type CodexCommandInput struct {
	BinaryPath string
	InputPath  string
	Timeout    time.Duration
	EnvAllow   map[string]string
}

type CodexRunResult struct {
	ExitCode            int
	Duration            time.Duration
	TimedOut            bool
	OutputTruncated     bool
	SafeFailureCategory string
	Output              string
}

func DetectCodex(binaryPath string) (string, bool) {
	binaryPath = strings.TrimSpace(binaryPath)
	if binaryPath != "" {
		if resolved, err := exec.LookPath(binaryPath); err == nil {
			return resolved, true
		}
		return "", false
	}
	resolved, err := exec.LookPath("codex")
	if err != nil {
		return "", false
	}
	return resolved, true
}

func BuildCodexCommand(input CodexCommandInput) (CodexCommand, error) {
	binaryPath := strings.TrimSpace(input.BinaryPath)
	if binaryPath == "" {
		return CodexCommand{}, fmt.Errorf("%w: codex binary path is required", ErrInvalidInput)
	}
	if strings.ContainsAny(binaryPath, "\x00\r\n") {
		return CodexCommand{}, fmt.Errorf("%w: codex binary path contains control characters", ErrInvalidInput)
	}
	inputPath := strings.TrimSpace(input.InputPath)
	if inputPath == "" {
		return CodexCommand{}, fmt.Errorf("%w: input path is required", ErrInvalidInput)
	}
	if strings.ContainsAny(inputPath, "\x00\r\n") {
		return CodexCommand{}, fmt.Errorf("%w: input path contains control characters", ErrInvalidInput)
	}
	if !filepath.IsAbs(inputPath) {
		return CodexCommand{}, fmt.Errorf("%w: input path must be absolute", ErrInvalidInput)
	}
	if input.Timeout <= 0 {
		return CodexCommand{}, fmt.Errorf("%w: timeout must be positive", ErrInvalidInput)
	}
	env := make([]string, 0, len(input.EnvAllow))
	for key, value := range input.EnvAllow {
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, "=\x00\r\n") {
			return CodexCommand{}, fmt.Errorf("%w: unsafe env key", ErrInvalidInput)
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			return CodexCommand{}, fmt.Errorf("%w: unsafe env value", ErrInvalidInput)
		}
		env = append(env, key+"="+value)
	}
	return CodexCommand{
		Path:      binaryPath,
		Args:      []string{"exec", "-"},
		Env:       env,
		StdinFile: inputPath,
		Timeout:   input.Timeout,
	}, nil
}

func RunCodexCommand(ctx context.Context, command CodexCommand, maxOutputBytes int64) (CodexRunResult, error) {
	if maxOutputBytes <= 0 {
		maxOutputBytes = 64 * 1024
	}
	runCtx, cancel := context.WithTimeout(ctx, command.Timeout)
	defer cancel()

	started := time.Now()
	cmd := exec.CommandContext(runCtx, command.Path, command.Args...)
	if strings.TrimSpace(command.Dir) != "" {
		cmd.Dir = command.Dir
	}
	cmd.Env = append(os.Environ(), command.Env...)
	if command.StdinFile != "" {
		stdin, err := os.Open(command.StdinFile)
		if err != nil {
			return CodexRunResult{Duration: time.Since(started)}, err
		}
		defer stdin.Close()
		cmd.Stdin = stdin
	}
	var output cappedBuffer
	output.limit = maxOutputBytes
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	safeFailureCategory := safeCodexFailureCategory(output.String())
	result := CodexRunResult{
		ExitCode:            0,
		Duration:            time.Since(started),
		TimedOut:            runCtx.Err() == context.DeadlineExceeded,
		OutputTruncated:     output.truncated,
		SafeFailureCategory: safeFailureCategory,
		Output:              output.String(),
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		if result.TimedOut {
			return result, fmt.Errorf("%w: codex_cli_timeout", ErrInvalidInput)
		}
		return result, err
	}
	return result, nil
}

func safeCodexFailureCategory(output string) string {
	normalized := strings.ToLower(output)
	switch {
	case (strings.Contains(normalized, "invalid schema") && strings.Contains(normalized, "response_format")) ||
		(strings.Contains(normalized, "output schema") && strings.Contains(normalized, "invalid")):
		return "codex_output_schema_invalid"
	case strings.Contains(normalized, "usage limit"):
		return "codex_usage_limit_reached"
	case strings.Contains(normalized, "failed to read config file") && strings.Contains(normalized, "permission denied"):
		return "codex_config_unreadable"
	case strings.Contains(normalized, "not logged in") || strings.Contains(normalized, "login") && strings.Contains(normalized, "codex"):
		return "codex_auth_unavailable"
	case strings.Contains(normalized, "api key") || strings.Contains(normalized, "authentication"):
		return "codex_auth_unavailable"
	default:
		return ""
	}
}

type cappedBuffer struct {
	buffer    bytes.Buffer
	limit     int64
	written   int64
	truncated bool
}

func (buffer *cappedBuffer) Write(p []byte) (int, error) {
	remaining := buffer.limit - buffer.written
	if remaining <= 0 {
		buffer.truncated = true
		return len(p), nil
	}
	writeBytes := p
	if int64(len(p)) > remaining {
		writeBytes = p[:remaining]
		buffer.truncated = true
	}
	n, err := buffer.buffer.Write(writeBytes)
	buffer.written += int64(n)
	if err != nil && err != io.ErrShortWrite {
		return n, err
	}
	return len(p), nil
}

func (buffer *cappedBuffer) String() string {
	return buffer.buffer.String()
}
