package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestRecordByIDGetReturnsFullPersistedLog(t *testing.T) {
	dir := t.TempDir()
	projectID := "project-1"
	recordID := "record-1"
	if err := os.MkdirAll(recordsDir(dir, projectID), 0755); err != nil {
		t.Fatalf("create records dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(recordsDir(dir, projectID), recordID+".log"), []byte("line 1\nline 2\nline 3\n"), 0600); err != nil {
		t.Fatalf("write log file: %v", err)
	}
	server := &Server{
		store: &Store{
			dataDir: dir,
			Users:   []User{{ID: "u1", Role: "admin"}},
			Records: []Record{{
				ID:          recordID,
				ProjectID:   projectID,
				ProjectName: "demo",
				Log:         []string{"memory tail"},
				StartedAt:   time.Now(),
			}},
		},
		sessions: map[string]Session{"sid": {UserID: "u1", ExpiresAt: time.Now().Add(time.Hour)}},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/records/"+recordID, nil)
	req.AddCookie(&http.Cookie{Name: "qfb_session", Value: "sid"})
	rr := httptest.NewRecorder()

	server.recordByID(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var got Record
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := []string{"line 1", "line 2", "line 3"}
	if strings.Join(got.Log, "\n") != strings.Join(want, "\n") {
		t.Fatalf("log = %#v, want %#v", got.Log, want)
	}
}

func TestSecretResponsesAreSanitized(t *testing.T) {
	got := sanitizeSecret(Secret{Password: "pass", Token: "tok", PrivateKey: "key"})
	if got.Password != "" || got.Token != "" || got.PrivateKey != "" {
		t.Fatalf("secret response leaked sensitive values: %#v", got)
	}
	if !got.HasPassword || !got.HasToken || !got.HasPrivateKey {
		t.Fatalf("secret response missing value markers: %#v", got)
	}
}

func TestSecretUpdatePreservesSensitiveFieldsWhenOmitted(t *testing.T) {
	updatedAt := time.Date(2026, 6, 2, 10, 0, 0, 0, time.Local)
	server := &Server{
		store: &Store{
			Users: []User{{ID: "u1", Role: "admin"}},
			Secrets: []Secret{{
				ID:         "sec1",
				Code:       "git-prod",
				Type:       "git",
				Username:   "deploy",
				Password:   "password",
				Token:      "token",
				PrivateKey: "private-key",
				Remark:     "remark",
				CreatedAt:  updatedAt,
				UpdatedAt:  updatedAt,
			}},
		},
		sessions: map[string]Session{"sid": {UserID: "u1", ExpiresAt: time.Now().Add(time.Hour)}},
	}
	req := httptest.NewRequest(http.MethodPut, "/api/secrets/sec1", strings.NewReader(`{"code":"git-prod","type":"git","username":"deploy","remark":"remark"}`))
	req.AddCookie(&http.Cookie{Name: "qfb_session", Value: "sid"})
	rr := httptest.NewRecorder()

	server.secretByID(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	got := server.store.Secrets[0]
	if got.Password != "password" || got.Token != "token" || got.PrivateKey != "private-key" {
		t.Fatalf("sensitive fields were overwritten: %#v", got)
	}
	if !got.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("unchanged secret updatedAt = %s, want %s", got.UpdatedAt, updatedAt)
	}
}

func TestRequireEnvNodePermForRegularUser(t *testing.T) {
	server := &Server{store: &Store{Nodes: []Node{{ID: "node1", Code: "prod-a", Group: "prod"}, {ID: "node2", Code: "prod-b", Group: "gray"}}}}
	user := User{ID: "u1", Role: "user", NodeGroups: []string{"prod"}}
	env := EnvConfig{Artifacts: []ArtifactRule{{NodeGroups: []string{"prod", "gray"}}}}

	err := server.requireEnvNodePerm(user, env)

	if err == nil || !strings.Contains(err.Error(), "gray") {
		t.Fatalf("expected unauthorized group error for gray, got %v", err)
	}
	if err := server.requireEnvNodePerm(User{Role: "admin"}, env); err != nil {
		t.Fatalf("empty admin node permissions should access all nodes: %v", err)
	}
	restrictedAdmin := User{Role: "admin", NodeGroups: []string{"prod"}}
	if err := server.requireEnvNodePerm(restrictedAdmin, env); err == nil || !strings.Contains(err.Error(), "gray") {
		t.Fatalf("restricted admin should be denied gray group, got %v", err)
	}
}

func TestProjectPermissionsDefaultOpenAndRestrictAdmins(t *testing.T) {
	admin := User{Role: "admin"}
	if !hasProjectAccess(admin, "project-1", "edit") || !canCreateProject(admin) {
		t.Fatalf("empty admin project permissions should be fully open")
	}
	admin.ProjectPerms = []ProjectPerm{{ProjectID: "project-1", CanRun: true, CanEdit: false}}
	if !hasProjectAccess(admin, "project-1", "run") {
		t.Fatalf("configured admin should run authorized project")
	}
	if hasProjectAccess(admin, "project-1", "edit") {
		t.Fatalf("configured admin should not edit without edit permission")
	}
	if hasProjectAccess(admin, "project-2", "view") {
		t.Fatalf("configured admin should not view unauthorized project")
	}
	if canCreateProject(admin) {
		t.Fatalf("configured admin should not create unscoped projects")
	}
}

func TestDeployArtifactsExpandsNodeGroups(t *testing.T) {
	root := t.TempDir()
	releaseDir := filepath.Join(root, "release")
	targetA := filepath.Join(root, "target-a")
	targetB := filepath.Join(root, "target-b")
	targetC := filepath.Join(root, "target-c")
	if err := os.MkdirAll(releaseDir, 0755); err != nil {
		t.Fatalf("create release dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseDir, "app.txt"), []byte("artifact"), 0644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	store := &Store{
		Nodes: []Node{
			{ID: "node1", Code: "local1", Type: "local", Group: "prod", BaseDir: targetA},
			{ID: "node2", Code: "local2", Type: "local", Group: "prod", BaseDir: targetB},
			{ID: "node3", Code: "local3", Type: "local", Group: "gray", BaseDir: targetC},
		},
	}
	project := Project{Build: BuildConfig{ArtifactSource: "."}}
	env := EnvConfig{Artifacts: []ArtifactRule{{
		Source:     ".",
		TargetDir:  "app",
		NodeGroups: []string{"prod"},
	}}}
	if err := deployArtifacts(context.Background(), store, project, env, releaseDir, t.Logf); err != nil {
		t.Fatalf("deployArtifacts returned error: %v", err)
	}
	for _, target := range []string{targetA, targetB} {
		got, err := os.ReadFile(filepath.Join(target, "app", "app.txt"))
		if err != nil {
			t.Fatalf("read deployed artifact from %s: %v", target, err)
		}
		if string(got) != "artifact" {
			t.Fatalf("artifact in %s = %q, want artifact", target, got)
		}
	}
	if _, err := os.Stat(filepath.Join(targetC, "app", "app.txt")); !os.IsNotExist(err) {
		t.Fatalf("unexpected deploy to non-selected group: %v", err)
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

func TestCleanupReleases(t *testing.T) {
	dataDir := t.TempDir()

	projectCode := "test-project-cleanup-unique-id"
	projectID := "proj-cleanup-1"

	defer os.RemoveAll(filepath.Join(".code-dep", "releases", projectCode))

	v1Dir := filepath.Join(".code-dep", "releases", projectCode, "v1")
	v2Dir := filepath.Join(".code-dep", "releases", projectCode, "v2")
	v3Dir := filepath.Join(".code-dep", "releases", projectCode, "v3")

	os.MkdirAll(v1Dir, 0755)
	os.MkdirAll(v2Dir, 0755)
	os.MkdirAll(v3Dir, 0755)

	logDir := filepath.Join(dataDir, "projects", projectID, "records")
	os.MkdirAll(logDir, 0755)
	os.WriteFile(filepath.Join(logDir, "rec1.log"), []byte("log 1"), 0600)
	os.WriteFile(filepath.Join(logDir, "rec2.log"), []byte("log 2"), 0600)
	os.WriteFile(filepath.Join(logDir, "rec3.log"), []byte("log 3"), 0600)

	store := &Store{
		dataDir: dataDir,
		Records: []Record{
			{
				ID:        "rec1",
				ProjectID: projectID,
				Version:   "v1",
				StartedAt: time.Now().Add(-30 * time.Minute),
				Status:    "success",
			},
			{
				ID:        "rec2",
				ProjectID: projectID,
				Version:   "v2",
				StartedAt: time.Now().Add(-20 * time.Minute),
				Status:    "success",
			},
			{
				ID:        "rec3",
				ProjectID: projectID,
				Version:   "v3",
				StartedAt: time.Now().Add(-10 * time.Minute),
				Status:    "success",
			},
		},
	}

	server := &Server{store: store}
	project := Project{
		ID:   projectID,
		Code: projectCode,
		Retention: RetentionConfig{
			KeepReleases: 2,
		},
	}

	logs := []string{}
	logLine := func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	server.cleanupReleases(project, logLine)

	server.store.mu.Lock()
	records := server.store.Records
	server.store.mu.Unlock()

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	if _, err := os.Stat(v1Dir); !os.IsNotExist(err) {
		t.Fatalf("expected v1 directory to be deleted")
	}
	if _, err := os.Stat(v2Dir); err != nil {
		t.Fatalf("expected v2 directory to exist")
	}
	if _, err := os.Stat(v3Dir); err != nil {
		t.Fatalf("expected v3 directory to exist")
	}

	if _, err := os.Stat(filepath.Join(logDir, "rec1.log")); !os.IsNotExist(err) {
		t.Fatalf("expected rec1.log to be deleted")
	}
	if _, err := os.Stat(filepath.Join(logDir, "rec2.log")); err != nil {
		t.Fatalf("expected rec2.log to exist")
	}
	if _, err := os.Stat(filepath.Join(logDir, "rec3.log")); err != nil {
		t.Fatalf("expected rec3.log to exist")
	}

	idxBytes, err := os.ReadFile(filepath.Join(logDir, "index.json"))
	if err != nil {
		t.Fatalf("failed to read index.json: %v", err)
	}
	var idxMetas []recordMeta
	if err := json.Unmarshal(idxBytes, &idxMetas); err != nil {
		t.Fatalf("failed to unmarshal index.json: %v", err)
	}
	if len(idxMetas) != 2 {
		t.Fatalf("expected index.json to have 2 metas, got %d", len(idxMetas))
	}
}
