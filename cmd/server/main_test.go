package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestWrapBuildEnvAppliesToScriptContext(t *testing.T) {
	env := EnvConfig{Goos: "linux", Goarch: "amd64"}
	got := wrapBuildEnv(env, "go build ./cmd/server\nfile server")
	want := "export GOOS='linux' GOARCH='amd64'\ngo build ./cmd/server\nfile server"
	if got != want {
		t.Fatalf("wrapped script = %q, want %q", got, want)
	}
}

func TestRunShellPreservesContextAcrossLines(t *testing.T) {
	dir := t.TempDir()
	if err := runShell(context.Background(), "mkdir app\ncd app\npwd > ../pwd.txt", dir, t.Logf); err != nil {
		t.Fatalf("runShell returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "pwd.txt"))
	if err != nil {
		t.Fatalf("read pwd output: %v", err)
	}
	gotDir, err := filepath.EvalSymlinks(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("resolve pwd output: %v", err)
	}
	wantDir, err := filepath.EvalSymlinks(filepath.Join(dir, "app"))
	if err != nil {
		t.Fatalf("resolve app dir: %v", err)
	}
	if gotDir != wantDir {
		t.Fatalf("shell context dir = %q, want %q", gotDir, wantDir)
	}
}

func TestBuildNotificationTextIncludesPublishDetails(t *testing.T) {
	started := time.Date(2026, 6, 1, 10, 0, 0, 0, time.Local)
	ended := started.Add(2*time.Minute + 5*time.Second)
	text := buildNotificationText(
		Project{Name: "订单服务", Code: "P0001"},
		EnvConfig{Name: "prod"},
		Record{
			ProjectName:   "订单服务",
			Env:           "prod",
			Ref:           "release/v1",
			Version:       "P0001-20260601100000",
			Mode:          "build",
			WorkerName:    "w17",
			InitiatorName: "张三",
			InitiatorCode: "zhangsan",
			StartedAt:     started,
		},
		"success",
		ended,
	)
	for _, want := range []string{
		"✅ 轻发布通知：发布成功",
		"【项目信息】",
		"项目：订单服务（P0001）",
		"分支/Tag：release/v1",
		"编译器：w17",
		"发起人：张三（zhangsan）",
		"耗费时间：2分5秒",
		"时间戳：2026-06-01T10:02:05",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("notification text missing %q:\n%s", want, text)
		}
	}
}

func TestDeployArtifactsRunsDeployCommandInTargetDir(t *testing.T) {
	root := t.TempDir()
	releaseDir := filepath.Join(root, "release")
	targetDir := filepath.Join(root, "target")
	if err := os.MkdirAll(releaseDir, 0755); err != nil {
		t.Fatalf("create release dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseDir, "app.txt"), []byte("artifact"), 0644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	store := &Store{
		Nodes: []Node{{ID: "node1", Code: "local1", Type: "local"}},
	}
	project := Project{Build: BuildConfig{ArtifactSource: "."}}
	env := EnvConfig{
		DeployCommand: "pwd > deploy_pwd.txt\nprintf deployed > deploy_marker.txt",
		Artifacts: []ArtifactRule{{
			Source:    ".",
			TargetDir: targetDir,
			NodeIDs:   []string{"node1"},
		}},
	}
	if err := deployArtifacts(context.Background(), store, project, env, releaseDir, t.Logf); err != nil {
		t.Fatalf("deployArtifacts returned error: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(targetDir, "deploy_marker.txt"))
	if err != nil {
		t.Fatalf("read deploy marker: %v", err)
	}
	if string(marker) != "deployed" {
		t.Fatalf("deploy marker = %q, want deployed", marker)
	}
	pwd, err := os.ReadFile(filepath.Join(targetDir, "deploy_pwd.txt"))
	if err != nil {
		t.Fatalf("read deploy pwd: %v", err)
	}
	gotDir, err := filepath.EvalSymlinks(strings.TrimSpace(string(pwd)))
	if err != nil {
		t.Fatalf("resolve deploy pwd: %v", err)
	}
	wantDir, err := filepath.EvalSymlinks(targetDir)
	if err != nil {
		t.Fatalf("resolve target dir: %v", err)
	}
	if gotDir != wantDir {
		t.Fatalf("deploy command ran in %q, want %q", gotDir, wantDir)
	}
}

func TestNormalizeDeployCommandDetachesNohup(t *testing.T) {
	got := normalizeDeployCommand("nohup java -jar app.jar")
	for _, want := range []string{
		`(nohup java -jar app.jar) </dev/null >> "$_code_dep_log" 2>&1 &`,
		`head -n 20 "$_code_dep_log"`,
		`后台命令已启动，日志: $_code_dep_log`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized command missing %q: %q", want, got)
		}
	}
}

func TestNormalizeDeployCommandKeepsExistingRedirects(t *testing.T) {
	got := normalizeDeployCommand("nohup java -jar app.jar > app.log 2>&1")
	want := `(nohup java -jar app.jar > app.log 2>&1) </dev/null >> "$_code_dep_log" 2>&1 &`
	if !strings.Contains(got, want) {
		t.Fatalf("normalized command missing %q: %q", want, got)
	}
}

func TestNormalizeDeployCommandLeavesNonNohupCommand(t *testing.T) {
	got := normalizeDeployCommand("systemctl restart app")
	want := "systemctl restart app"
	if got != want {
		t.Fatalf("normalized command = %q, want %q", got, want)
	}
}

func TestNormalizeDeployCommandSupportsNoWaitDirective(t *testing.T) {
	got := normalizeDeployCommand("exec none return java -jar app.jar")
	want := `(java -jar app.jar) </dev/null >> "$_code_dep_log" 2>&1 &`
	if !strings.Contains(got, want) {
		t.Fatalf("normalized command missing %q: %q", want, got)
	}
}
