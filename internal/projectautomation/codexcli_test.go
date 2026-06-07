package projectautomation

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestBuildCodexCommandUsesFixedArgv(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "task.json")
	command, err := BuildCodexCommand(CodexCommandInput{BinaryPath: "/usr/local/bin/codex", InputPath: inputPath, Timeout: time.Minute, EnvAllow: map[string]string{"PATH": "/usr/bin"}})
	if err != nil {
		t.Fatalf("BuildCodexCommand returned error: %v", err)
	}
	if command.Path != "/usr/local/bin/codex" {
		t.Fatalf("unexpected path: %q", command.Path)
	}
	want := []string{"exec", "-"}
	if len(command.Args) != len(want) {
		t.Fatalf("unexpected args: %#v", command.Args)
	}
	for i := range want {
		if command.Args[i] != want[i] {
			t.Fatalf("arg %d = %q, want %q", i, command.Args[i], want[i])
		}
	}
	if command.StdinFile != inputPath {
		t.Fatalf("stdin file = %q, want %q", command.StdinFile, inputPath)
	}
}

func TestBuildCodexCommandRejectsRelativeInputPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relative/absolute path parsing differs on Windows host; WSL test covers this")
	}
	if _, err := BuildCodexCommand(CodexCommandInput{BinaryPath: "/usr/local/bin/codex", InputPath: "task.json", Timeout: time.Minute}); err == nil {
		t.Fatal("expected relative input path rejection")
	}
}

func TestBuildCodexCommandRejectsUnsafeEnv(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "task.json")
	if _, err := BuildCodexCommand(CodexCommandInput{BinaryPath: "/usr/local/bin/codex", InputPath: inputPath, Timeout: time.Minute, EnvAllow: map[string]string{"BAD=KEY": "value"}}); err == nil {
		t.Fatal("expected unsafe env key rejection")
	}
}

func TestRunCodexCommandClassifiesConfigPermissionFailure(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "task.txt")
	if err := os.WriteFile(inputPath, []byte("safe prompt"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	binary := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\nprintf '%s\\n' 'Error loading config.toml: Failed to read config file /home/example/.codex/config.toml: Permission denied (os error 13)' >&2\nexit 1\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	command, err := BuildCodexCommand(CodexCommandInput{BinaryPath: binary, InputPath: inputPath, Timeout: time.Minute})
	if err != nil {
		t.Fatalf("BuildCodexCommand returned error: %v", err)
	}

	result, err := RunCodexCommand(t.Context(), command, 1024)
	if err == nil {
		t.Fatal("expected fake codex failure")
	}
	if result.SafeFailureCategory != "codex_config_unreadable" {
		t.Fatalf("expected codex_config_unreadable, got %q", result.SafeFailureCategory)
	}
}

func TestRunCodexCommandClassifiesUsageLimitFailure(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "task.txt")
	if err := os.WriteFile(inputPath, []byte("safe prompt"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	binary := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\nprintf '%s\\n' \"ERROR: You've hit your usage limit. Visit https://chatgpt.com/codex/settings/usage to purchase more credits or try again later.\" >&2\nexit 1\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	command, err := BuildCodexCommand(CodexCommandInput{BinaryPath: binary, InputPath: inputPath, Timeout: time.Minute})
	if err != nil {
		t.Fatalf("BuildCodexCommand returned error: %v", err)
	}

	result, err := RunCodexCommand(t.Context(), command, 1024)
	if err == nil {
		t.Fatal("expected fake codex failure")
	}
	if result.SafeFailureCategory != "codex_usage_limit_reached" {
		t.Fatalf("expected codex_usage_limit_reached, got %q", result.SafeFailureCategory)
	}
}
