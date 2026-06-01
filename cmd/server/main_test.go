package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestShellCommands(t *testing.T) {
	got := shellCommands("  echo one  \r\n\n# skip me\n echo two\n")
	want := []string{"echo one", "echo two"}
	if len(got) != len(want) {
		t.Fatalf("got %d commands, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("command %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRunShellRunsLinesSequentially(t *testing.T) {
	dir := t.TempDir()
	var logs []string
	logLine := func(format string, args ...any) {
		logs = append(logs, format)
	}
	err := runShell(context.Background(), "printf one > out.txt\nprintf two >> out.txt", dir, logLine)
	if err != nil {
		t.Fatalf("runShell returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "onetwo" {
		t.Fatalf("output = %q, want %q", string(data), "onetwo")
	}
	if len(logs) < 2 {
		t.Fatalf("expected command logs, got %#v", logs)
	}
}

func TestWrapBuildEnvAppliesToEachLine(t *testing.T) {
	env := EnvConfig{Goos: "linux", Goarch: "amd64"}
	got := wrapBuildEnv(env, "go build ./cmd/server\nfile server")
	want := "GOOS='linux' GOARCH='amd64' go build ./cmd/server\nGOOS='linux' GOARCH='amd64' file server"
	if got != want {
		t.Fatalf("wrapped script = %q, want %q", got, want)
	}
}
