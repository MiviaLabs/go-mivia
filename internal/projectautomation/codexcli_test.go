package projectautomation

import (
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
