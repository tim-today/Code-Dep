package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	mrand "math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
)

const (
	staticDir        = "web/static"
	defaultPort      = "8080"
	commandTimeout   = 30 * time.Minute
	sshTimeout       = 20 * time.Second
	defaultNodeGroup = "默认分组"
)

var dataDir = "data"

type Store struct {
	mu            sync.RWMutex   `json:"-"`
	dataDir       string         `json:"-"`
	encKey        []byte         `json:"-"`
	NextID        int64          `json:"nextId"`
	Secrets       []Secret       `json:"secrets"`
	Nodes         []Node         `json:"nodes"`
	Workers       []Worker       `json:"workers"`
	Notifications []Notification `json:"notifications"`
	Users         []User         `json:"users"`
	Projects      []Project      `json:"projects"`
	Records       []Record       `json:"records"`
}

type Secret struct {
	ID            string    `json:"id"`
	Code          string    `json:"code"`
	Type          string    `json:"type"`
	Username      string    `json:"username"`
	Password      string    `json:"password"`
	Token         string    `json:"token"`
	PrivateKey    string    `json:"privateKey"`
	HasPassword   bool      `json:"hasPassword,omitempty"`
	HasToken      bool      `json:"hasToken,omitempty"`
	HasPrivateKey bool      `json:"hasPrivateKey,omitempty"`
	Remark        string    `json:"remark"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type Node struct {
	ID        string    `json:"id"`
	Code      string    `json:"code"`
	Group     string    `json:"group"`
	Type      string    `json:"type"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	User      string    `json:"user"`
	BaseDir   string    `json:"baseDir"`
	SecretID  string    `json:"secretId"`
	Remark    string    `json:"remark"`
	Status    string    `json:"status"`
	LastError string    `json:"lastError"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Worker struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	NodeID    string    `json:"nodeId"`
	WorkDir   string    `json:"workDir"`
	Weight    int       `json:"weight"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Notification struct {
	ID           string    `json:"id"`
	Code         string    `json:"code"`
	Type         string    `json:"type"`
	HookURL      string    `json:"hookUrl"`
	EmailEnabled bool      `json:"emailEnabled"`
	EmailTo      string    `json:"emailTo"`
	Remark       string    `json:"remark"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type User struct {
	ID           string        `json:"id"`
	Code         string        `json:"code"`
	Name         string        `json:"name"`
	Role         string        `json:"role"`
	Password     string        `json:"password"`
	ProjectPerms []ProjectPerm `json:"projectPerms"`
	NodeIDs      []string      `json:"nodeIds"`
	NodeGroups   []string      `json:"nodeGroups"`
	Remark       string        `json:"remark"`
	CreatedAt    time.Time     `json:"createdAt"`
	UpdatedAt    time.Time     `json:"updatedAt"`
}

type ProjectPerm struct {
	ProjectID string `json:"projectId"`
	CanRun    bool   `json:"canRun"`
	CanEdit   bool   `json:"canEdit"`
}

type Project struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Code            string          `json:"code"`
	Group           string          `json:"group"`
	LastStatus      string          `json:"lastStatus"`
	LastPublishedAt *time.Time      `json:"lastPublishedAt"`
	Git             GitConfig       `json:"git"`
	Build           BuildConfig     `json:"build"`
	Environments    []EnvConfig     `json:"environments"`
	Notify          NotifyConfig    `json:"notify"`
	Retention       RetentionConfig `json:"retention"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

type GitConfig struct {
	URL      string `json:"url"`
	Ref      string `json:"ref"`
	SecretID string `json:"secretId"`
}

type BuildConfig struct {
	NodeID            string   `json:"nodeId"`
	WorkDir           string   `json:"workDir"`
	ArtifactSource    string   `json:"artifactSource"`
	PreprocessEnabled bool     `json:"preprocessEnabled"`
	PreprocessCommand string   `json:"preprocessCommand"`
	WorkerIDs         []string `json:"workerIds"`
	PublishMode       string   `json:"publishMode"`
}

type EnvConfig struct {
	Name          string         `json:"name"`
	Goos          string         `json:"goos"`
	Goarch        string         `json:"goarch"`
	CompileDeploy bool           `json:"compileDeploy"`
	BuildCommand  string         `json:"buildCommand"`
	Artifacts     []ArtifactRule `json:"artifacts"`
	DeployCommand string         `json:"deployCommand"`
}

type ArtifactRule struct {
	Source     string   `json:"source"`
	TargetDir  string   `json:"targetDir"`
	NodeIDs    []string `json:"nodeIds"`
	NodeGroups []string `json:"nodeGroups"`
}

type NotifyConfig struct {
	NotificationID string `json:"notificationId"`
	WeComHook      string `json:"weComHook"`
	FeishuHook     string `json:"feishuHook"`
}

type RetentionConfig struct {
	KeepReleases int `json:"keepReleases"`
}

type Record struct {
	ID            string     `json:"id"`
	ProjectID     string     `json:"projectId"`
	ProjectName   string     `json:"projectName"`
	Env           string     `json:"env"`
	Ref           string     `json:"ref"`
	Version       string     `json:"version"`
	Status        string     `json:"status"`
	Mode          string     `json:"mode"`
	WorkerID      string     `json:"workerId"`
	WorkerName    string     `json:"workerName"`
	InitiatorID   string     `json:"initiatorId"`
	InitiatorCode string     `json:"initiatorCode"`
	InitiatorName string     `json:"initiatorName"`
	Log           []string   `json:"log"`
	StartedAt     time.Time  `json:"startedAt"`
	EndedAt       *time.Time `json:"endedAt"`
}

type Server struct {
	store    *Store
	jobs     *JobHub
	sessions map[string]Session
	smu      sync.RWMutex
	running  map[string]context.CancelFunc
	rmu      sync.Mutex
}

type Session struct {
	UserID    string
	ExpiresAt time.Time
}

type authRequest struct {
	Code     string `json:"code"`
	Password string `json:"password"`
}

type changePasswordRequest struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

type bootstrapPayload struct {
	CurrentUser   User           `json:"currentUser"`
	Secrets       []Secret       `json:"secrets"`
	Nodes         []Node         `json:"nodes"`
	Workers       []Worker       `json:"workers"`
	Notifications []Notification `json:"notifications"`
	Users         []User         `json:"users"`
	Projects      []Project      `json:"projects"`
	Records       []Record       `json:"records"`
}

type JobHub struct {
	mu   sync.RWMutex
	subs map[string][]chan string
}

type apiError struct {
	Error string `json:"error"`
}

type publishRequest struct {
	ProjectID string   `json:"projectId"`
	Env       string   `json:"env"`
	WorkerIDs []string `json:"workerIds"`
	Ref       string   `json:"ref"`
	Mode      string   `json:"mode"`
	RecordID  string   `json:"recordId"`
}

func main() {
	if envDir := os.Getenv("DATA_DIR"); envDir != "" {
		dataDir = envDir
	}
	store, err := loadStore(dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := ensureDefaultAdmin(store); err != nil {
		log.Fatal(err)
	}
	server := &Server{store: store, jobs: &JobHub{subs: map[string][]chan string{}}, sessions: map[string]Session{}, running: map[string]context.CancelFunc{}}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))
	mux.HandleFunc("/api/auth/login", server.login)
	mux.HandleFunc("/api/auth/logout", server.logout)
	mux.HandleFunc("/api/auth/me", server.me)
	mux.HandleFunc("/api/auth/change-password", server.changePassword)
	mux.HandleFunc("/api/bootstrap", server.bootstrap)
	mux.HandleFunc("/api/secrets", server.secrets)
	mux.HandleFunc("/api/secrets/", server.secretByID)
	mux.HandleFunc("/api/nodes", server.nodes)
	mux.HandleFunc("/api/nodes/", server.nodeByID)
	mux.HandleFunc("/api/workers", server.workers)
	mux.HandleFunc("/api/workers/", server.workerByID)
	mux.HandleFunc("/api/notifications", server.notifications)
	mux.HandleFunc("/api/notifications/", server.notificationByID)
	mux.HandleFunc("/api/users", server.users)
	mux.HandleFunc("/api/users/", server.userByID)
	mux.HandleFunc("/api/projects", server.projects)
	mux.HandleFunc("/api/projects/", server.projectByID)
	mux.HandleFunc("/api/records", server.records)
	mux.HandleFunc("/api/records/", server.recordByID)
	mux.HandleFunc("/api/publish", server.publish)
	mux.HandleFunc("/api/publish/", server.publishByID)
	mux.HandleFunc("/api/logs/", server.logs)
	mux.HandleFunc("/api/git/refs", server.gitRefs)

	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	absDataDir, _ := filepath.Abs(dataDir)
	banner := `
   ______          __     ____                 
  / ____/___  ____/ /__  / __ \___  ____  __  __
 / /   / __ \/ __  / _ \/ / / / _ \/ __ \/ / / /
/ /___/ /_/ / /_/ /  __/ /_/ /  __/ /_/ / /_/ / 
\____/\____/\__,_/\___/_____/\___/ .___/\__, /  
                                /_/    /____/   
`
	fmt.Printf("\x1b[36m%s\x1b[0m", banner)
	fmt.Printf("  \x1b[1;32m✔ Server started successfully!\x1b[0m\n")
	fmt.Printf("  \x1b[90m-------------------------------------------------------------\x1b[0m\n")
	fmt.Printf("  \x1b[1m* Web UI:\x1b[0m \x1b[34;4mhttp://localhost:%s\x1b[0m\n", port)
	fmt.Printf("  \x1b[1m* Data Dir:\x1b[0m \x1b[35m%s\x1b[0m\n", absDataDir)
	fmt.Printf("  \x1b[1m* Default Admin:\x1b[0m admin / 123456\n")
	fmt.Printf("  \x1b[90m-------------------------------------------------------------\x1b[0m\n")
	fmt.Printf("  \x1b[33m💡 [Note] After logging in for the first time, please be sure to go to the global settings to change the default password.。\x1b[0m\n\n")

	log.Fatal(http.ListenAndServe(":"+port, logging(server.requireAuth(mux))))
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return logging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/auth/login" {
			if _, ok := s.currentUser(r); !ok {
				fail(w, http.StatusUnauthorized, "请先登录")
				return
			}
		}
		next.ServeHTTP(w, r)
	}))
}

func loadStore(dataDir string) (*Store, error) {
	store := &Store{dataDir: dataDir, NextID: 1}
	key, err := loadOrCreateKey(dataDir)
	if err != nil {
		return nil, fmt.Errorf("load key failed: %w", err)
	}
	store.encKey = key
	oldPath := filepath.Join(dataDir, "store.json")
	if _, err := os.Stat(oldPath); err == nil {
		if err := migrateFromStoreJSON(store, oldPath); err != nil {
			log.Printf("migrate old data failed: %v", err)
		}
	}
	if err := store.loadMeta(); err != nil {
		return nil, err
	}
	if err := store.loadSecrets(); err != nil {
		return nil, err
	}
	if err := store.loadNodes(); err != nil {
		return nil, err
	}
	if err := store.loadWorkers(); err != nil {
		return nil, err
	}
	if err := store.loadNotifications(); err != nil {
		return nil, err
	}
	if err := store.loadUsers(); err != nil {
		return nil, err
	}
	if err := store.loadProjects(); err != nil {
		return nil, err
	}
	if err := store.loadAllRecords(); err != nil {
		return nil, err
	}
	if store.NextID == 0 {
		store.NextID = 1
	}
	migrateNodeGroups(store)
	migrateWorkspaceConfig(store)
	migrateWorkers(store)
	_ = store.saveAll()
	return store, os.MkdirAll(dataDir, 0755)
}

func migrateNodeGroups(store *Store) {
	for i := range store.Nodes {
		store.Nodes[i].Group = normalizeMultiNodeGroup(store.Nodes[i].Group)
	}
	for pi := range store.Projects {
		for ei := range store.Projects[pi].Environments {
			env := &store.Projects[pi].Environments[ei]
			for ai := range env.Artifacts {
				artifact := &env.Artifacts[ai]
				artifact.NodeGroups = normalizeNodeGroups(artifact.NodeGroups)
				if len(artifact.NodeGroups) == 0 && len(artifact.NodeIDs) > 0 {
					groups := []string{}
					for _, nodeID := range artifact.NodeIDs {
						if node, ok := findNode(store, nodeID); ok {
							groups = append(groups, node.Group)
						}
					}
					artifact.NodeGroups = normalizeNodeGroups(groups)
				}
			}
		}
	}
	for ui := range store.Users {
		user := &store.Users[ui]
		user.NodeGroups = normalizeNodeGroups(user.NodeGroups)
		if user.Role != "admin" && len(user.NodeGroups) == 0 && len(user.NodeIDs) > 0 {
			groups := []string{}
			for _, nodeID := range user.NodeIDs {
				if node, ok := findNode(store, nodeID); ok {
					groups = append(groups, node.Group)
				}
			}
			user.NodeGroups = normalizeNodeGroups(groups)
		}
	}
}

func migrateWorkspaceConfig(store *Store) {
	for pi := range store.Projects {
		if store.Projects[pi].Build.ArtifactSource == "" {
			store.Projects[pi].Build.ArtifactSource = firstArtifactSource(store.Projects[pi])
		}
		if store.Projects[pi].Build.PublishMode == "" {
			store.Projects[pi].Build.PublishMode = "overwrite"
		}
		for ei := range store.Projects[pi].Environments {
			env := &store.Projects[pi].Environments[ei]
			if strings.TrimSpace(env.BuildCommand) != "" {
				env.CompileDeploy = true
			}
		}
	}
}

func migrateWorkers(store *Store) {
	if len(store.Workers) > 0 {
		return
	}
	seen := map[string]string{}
	for pi := range store.Projects {
		p := &store.Projects[pi]
		if p.Build.NodeID == "" {
			continue
		}
		key := p.Build.NodeID + "|" + p.Build.WorkDir
		if id, ok := seen[key]; ok {
			p.Build.WorkerIDs = []string{id}
			continue
		}
		node, ok := findNode(store, p.Build.NodeID)
		if !ok {
			continue
		}
		id := store.newID("worker")
		seen[key] = id
		name := node.Code
		if p.Build.WorkDir != "" {
			name = node.Code + " · " + filepath.Base(filepath.Clean(p.Build.WorkDir))
		}
		store.Workers = append(store.Workers, Worker{
			ID: id, Name: name, NodeID: p.Build.NodeID, WorkDir: p.Build.WorkDir,
			Weight: 5, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
		p.Build.WorkerIDs = []string{id}
	}
	if len(store.Workers) == 0 {
		for _, node := range store.Nodes {
			if node.Status != "valid" {
				continue
			}
			id := store.newID("worker")
			store.Workers = append(store.Workers, Worker{
				ID: id, Name: node.Code, NodeID: node.ID, WorkDir: ".code-dep/workspaces",
				Weight: 5, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			})
		}
	}
}

func firstArtifactSource(project Project) string {
	for _, env := range project.Environments {
		for _, artifact := range env.Artifacts {
			if strings.TrimSpace(artifact.Source) != "" {
				return artifact.Source
			}
		}
	}
	return "."
}

func ensureDefaultAdmin(store *Store) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.Users) > 0 {
		changed := false
		for i := range store.Users {
			if store.Users[i].Password != "" && !isPasswordHash(store.Users[i].Password) {
				hash, err := hashPassword(store.Users[i].Password)
				if err != nil {
					return err
				}
				store.Users[i].Password = hash
				changed = true
			}
		}
		if changed {
			return store.saveLocked()
		}
		return nil
	}
	hash, err := hashPassword("123456")
	if err != nil {
		return err
	}
	now := time.Now()
	store.Users = append(store.Users, User{
		ID:        store.newID("usr"),
		Code:      "admin",
		Name:      "系统管理员",
		Role:      "admin",
		Password:  hash,
		Remark:    "默认管理员，请首次登录后修改密码",
		CreatedAt: now,
		UpdatedAt: now,
	})
	return store.saveLocked()
}

func (s *Store) saveLocked() error {
	return s.saveAll()
}

func (s *Store) newID(prefix string) string {
	s.NextID++
	return fmt.Sprintf("%s%05d", prefix, s.NextID-1)
}

func (s *Store) projectCodeLocked() string {
	return fmt.Sprintf("P%04d", len(s.Projects)+1)
}

func nowPtr() *time.Time {
	t := time.Now()
	return &t
}

func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	respond(w, apiError{Error: msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req authRequest
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	user, ok := s.findUserByCode(strings.TrimSpace(req.Code))
	if !ok || !checkPassword(user.Password, req.Password) {
		fail(w, http.StatusUnauthorized, "账号或密码错误")
		return
	}
	sessionID, err := randomToken()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	expires := time.Now().Add(24 * time.Hour)
	s.smu.Lock()
	s.sessions[sessionID] = Session{UserID: user.ID, ExpiresAt: expires}
	s.smu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     "qfb_session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
	})
	respond(w, map[string]any{"user": sanitizeUser(user)})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if cookie, err := r.Cookie("qfb_session"); err == nil {
		s.smu.Lock()
		delete(s.sessions, cookie.Value)
		s.smu.Unlock()
	}
	clearSessionCookie(w)
	respond(w, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, _ := s.currentUser(r)
	respond(w, map[string]any{"user": sanitizeUser(user)})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, ok := s.currentUser(r)
	if !ok {
		fail(w, http.StatusUnauthorized, "请先登录")
		return
	}
	var req changePasswordRequest
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if len(req.NewPassword) < 6 {
		fail(w, 400, "新密码至少 6 位")
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	idx := indexUser(s.store.Users, user.ID)
	if idx < 0 || !checkPassword(s.store.Users[idx].Password, req.OldPassword) {
		fail(w, 400, "原密码不正确")
		return
	}
	hash, err := hashPassword(req.NewPassword)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.store.Users[idx].Password = hash
	s.store.Users[idx].UpdatedAt = time.Now()
	if err := s.store.saveLocked(); err != nil {
		fail(w, 500, err.Error())
		return
	}
	respond(w, map[string]bool{"ok": true})
}

func (s *Server) currentUser(r *http.Request) (User, bool) {
	cookie, err := r.Cookie("qfb_session")
	if err != nil || cookie.Value == "" {
		return User{}, false
	}
	s.smu.RLock()
	session, ok := s.sessions[cookie.Value]
	s.smu.RUnlock()
	if !ok || time.Now().After(session.ExpiresAt) {
		if ok {
			s.smu.Lock()
			delete(s.sessions, cookie.Value)
			s.smu.Unlock()
		}
		return User{}, false
	}
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	idx := indexUser(s.store.Users, session.UserID)
	if idx < 0 {
		return User{}, false
	}
	return s.store.Users[idx], true
}

func (s *Server) findUserByCode(code string) (User, bool) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	for _, user := range s.store.Users {
		if user.Code == code {
			return user, true
		}
	}
	return User{}, false
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	user, ok := s.currentUser(r)
	if !ok {
		fail(w, http.StatusUnauthorized, "请先登录")
		return false
	}
	if user.Role != "admin" {
		fail(w, http.StatusForbidden, "需要管理员权限")
		return false
	}
	return true
}

func (s *Server) requireProjectPerm(w http.ResponseWriter, r *http.Request, projectID, action string) bool {
	user, ok := s.currentUser(r)
	if !ok {
		fail(w, http.StatusUnauthorized, "请先登录")
		return false
	}
	if !hasProjectAccess(user, projectID, action) {
		fail(w, http.StatusForbidden, "没有项目权限")
		return false
	}
	return true
}

func hasProjectAccess(user User, projectID, action string) bool {
	if len(user.ProjectPerms) == 0 {
		return true
	}
	for _, perm := range user.ProjectPerms {
		if perm.ProjectID != projectID {
			continue
		}
		if action == "view" {
			return perm.CanRun || perm.CanEdit
		}
		if action == "run" {
			return perm.CanRun
		}
		if action == "edit" {
			return perm.CanEdit
		}
	}
	return false
}

func canCreateProject(user User) bool {
	return user.Role == "admin" && len(user.ProjectPerms) == 0
}

func hasNodeAccess(user User, nodeID string) bool {
	if len(user.NodeIDs) == 0 && len(user.NodeGroups) == 0 {
		return true
	}
	for _, id := range user.NodeIDs {
		if id == nodeID {
			return true
		}
	}
	return false
}

func hasNodeGroupAccess(user User, group string) bool {
	if len(user.NodeGroups) == 0 {
		return true
	}
	subGroups := splitGroup(group)
	for _, sg := range subGroups {
		sg = normalizeNodeGroup(sg)
		for _, item := range user.NodeGroups {
			if normalizeNodeGroup(item) == sg {
				return true
			}
		}
	}
	return false
}

func (s *Server) bootstrapForUserLocked(user User) bootstrapPayload {
	payload := bootstrapPayload{
		CurrentUser:   sanitizeUser(user),
		Secrets:       sanitizeSecrets(s.store.Secrets),
		Nodes:         filterNodesForUser(s.store.Nodes, user),
		Workers:       filterWorkersForUser(s.store.Workers, s.store.Nodes, user),
		Notifications: append([]Notification(nil), s.store.Notifications...),
		Projects:      filterProjectsForUser(s.store.Projects, user),
		Records:       filterRecordsForUser(s.store.Records, user),
	}
	if user.Role == "admin" {
		payload.Users = sanitizeUsers(s.store.Users)
		return payload
	}
	return payload
}

func filterProjectsForUser(projects []Project, user User) []Project {
	if len(user.ProjectPerms) == 0 {
		return append([]Project(nil), projects...)
	}
	filtered := []Project{}
	for _, project := range projects {
		if hasProjectAccess(user, project.ID, "view") {
			filtered = append(filtered, project)
		}
	}
	return filtered
}

func filterRecordsForUser(records []Record, user User) []Record {
	if len(user.ProjectPerms) == 0 {
		return append([]Record(nil), records...)
	}
	filtered := []Record{}
	for _, record := range records {
		if hasProjectAccess(user, record.ProjectID, "view") {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func filterNodesForUser(nodes []Node, user User) []Node {
	if len(user.NodeGroups) == 0 {
		return append([]Node(nil), nodes...)
	}
	filtered := []Node{}
	for _, node := range nodes {
		if hasNodeGroupAccess(user, node.Group) {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

func filterWorkersForUser(workers []Worker, nodes []Node, user User) []Worker {
	return append([]Worker(nil), workers...)
}

func sanitizeUsers(users []User) []User {
	out := make([]User, len(users))
	for i, user := range users {
		out[i] = sanitizeUser(user)
	}
	return out
}

func sanitizeSecrets(secrets []Secret) []Secret {
	out := make([]Secret, len(secrets))
	for i, secret := range secrets {
		out[i] = sanitizeSecret(secret)
	}
	return out
}

func sanitizeSecret(secret Secret) Secret {
	secret.HasPassword = secret.Password != ""
	secret.HasToken = secret.Token != ""
	secret.HasPrivateKey = secret.PrivateKey != ""
	secret.Password = ""
	secret.Token = ""
	secret.PrivateKey = ""
	return secret
}

func sameSecret(a, b Secret) bool {
	return a.Code == b.Code &&
		a.Type == b.Type &&
		a.Username == b.Username &&
		a.Password == b.Password &&
		a.Token == b.Token &&
		a.PrivateKey == b.PrivateKey &&
		a.Remark == b.Remark
}

func sanitizeUser(user User) User {
	user.Password = ""
	return user
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "qfb_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

func hashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

func checkPassword(hash, password string) bool {
	if hash == "" {
		return false
	}
	if isPasswordHash(hash) {
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	}
	return hash == password
}

func isPasswordHash(value string) bool {
	return strings.HasPrefix(value, "$2a$") || strings.HasPrefix(value, "$2b$") || strings.HasPrefix(value, "$2y$")
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, _ := s.currentUser(r)
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	respond(w, s.bootstrapForUserLocked(user))
}

func (s *Server) secrets(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.store.mu.RLock()
		defer s.store.mu.RUnlock()
		respond(w, sanitizeSecrets(s.store.Secrets))
	case http.MethodPost:
		var item Secret
		if err := readJSON(r, &item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if strings.TrimSpace(item.Code) == "" {
			fail(w, 400, "秘钥 code 必填")
			return
		}
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		item.ID = s.store.newID("sec")
		item.CreatedAt = time.Now()
		item.UpdatedAt = item.CreatedAt
		s.store.Secrets = append(s.store.Secrets, item)
		if err := s.store.saveLocked(); err != nil {
			fail(w, 500, err.Error())
			return
		}
		respond(w, sanitizeSecret(item))
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) secretByID(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/secrets/")
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	idx := indexSecret(s.store.Secrets, id)
	if idx < 0 {
		fail(w, 404, "秘钥不存在")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var item Secret
		if err := readJSON(r, &item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		old := s.store.Secrets[idx]
		item.ID = id
		item.CreatedAt = old.CreatedAt
		item.UpdatedAt = old.UpdatedAt
		if item.Password == "" {
			item.Password = old.Password
		}
		if item.Token == "" {
			item.Token = old.Token
		}
		if item.PrivateKey == "" {
			item.PrivateKey = old.PrivateKey
		}
		if sameSecret(old, item) {
			respond(w, map[string]bool{"ok": true})
			return
		}
		item.UpdatedAt = time.Now()
		s.store.Secrets[idx] = item
	case http.MethodDelete:
		s.store.Secrets = append(s.store.Secrets[:idx], s.store.Secrets[idx+1:]...)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.store.saveLocked(); err != nil {
		fail(w, 500, err.Error())
		return
	}
	respond(w, map[string]bool{"ok": true})
}

func (s *Server) nodes(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	user, _ := s.currentUser(r)
	switch r.Method {
	case http.MethodGet:
		s.store.mu.RLock()
		defer s.store.mu.RUnlock()
		respond(w, filterNodesForUser(s.store.Nodes, user))
	case http.MethodPost:
		var item Node
		if err := readJSON(r, &item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if strings.TrimSpace(item.Code) == "" {
			fail(w, 400, "节点 code 必填")
			return
		}
		if item.Port == 0 {
			item.Port = 22
		}
		item.Group = normalizeMultiNodeGroup(item.Group)
		if !hasNodeGroupAccess(user, item.Group) {
			fail(w, http.StatusForbidden, "没有节点分组权限: "+item.Group)
			return
		}
		item.Status = "unknown"
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		item.ID = s.store.newID("node")
		item.CreatedAt = time.Now()
		item.UpdatedAt = item.CreatedAt
		s.store.Nodes = append(s.store.Nodes, item)
		if err := s.store.saveLocked(); err != nil {
			fail(w, 500, err.Error())
			return
		}
		respond(w, item)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) nodeByID(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	user, _ := s.currentUser(r)
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/nodes/"), "/")
	id := parts[0]
	if node, ok := findNode(s.store, id); ok && !hasNodeGroupAccess(user, node.Group) {
		fail(w, http.StatusForbidden, "没有节点分组权限: "+normalizeNodeGroup(node.Group))
		return
	}
	if len(parts) == 2 && parts[1] == "test" {
		s.testNode(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "console" {
		s.nodeConsole(w, r, id)
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	idx := indexNode(s.store.Nodes, id)
	if idx < 0 {
		fail(w, 404, "节点不存在")
		return
	}
	if !hasNodeGroupAccess(user, s.store.Nodes[idx].Group) {
		fail(w, http.StatusForbidden, "没有节点分组权限: "+normalizeNodeGroup(s.store.Nodes[idx].Group))
		return
	}
	switch r.Method {
	case http.MethodPut:
		var item Node
		if err := readJSON(r, &item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		item.ID = id
		item.CreatedAt = s.store.Nodes[idx].CreatedAt
		item.UpdatedAt = time.Now()
		if item.Port == 0 {
			item.Port = 22
		}
		item.Group = normalizeMultiNodeGroup(item.Group)
		if !hasNodeGroupAccess(user, item.Group) {
			fail(w, http.StatusForbidden, "没有节点分组权限: "+item.Group)
			return
		}
		item.Status = s.store.Nodes[idx].Status
		item.LastError = s.store.Nodes[idx].LastError
		s.store.Nodes[idx] = item
	case http.MethodDelete:
		s.store.Nodes = append(s.store.Nodes[:idx], s.store.Nodes[idx+1:]...)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.store.saveLocked(); err != nil {
		fail(w, 500, err.Error())
		return
	}
	respond(w, map[string]bool{"ok": true})
}

func (s *Server) workers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	user, _ := s.currentUser(r)
	switch r.Method {
	case http.MethodGet:
		s.store.mu.RLock()
		defer s.store.mu.RUnlock()
		respond(w, filterWorkersForUser(s.store.Workers, s.store.Nodes, user))
	case http.MethodPost:
		var item Worker
		if err := readJSON(r, &item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if err := validateWorker(item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		nodeIdx := indexNode(s.store.Nodes, item.NodeID)
		if nodeIdx < 0 {
			fail(w, 400, "worker 节点不存在")
			return
		}
		if !hasNodeGroupAccess(user, s.store.Nodes[nodeIdx].Group) {
			fail(w, http.StatusForbidden, "没有 worker 节点分组权限")
			return
		}
		item.ID = s.store.newID("worker")
		item.CreatedAt = time.Now()
		item.UpdatedAt = item.CreatedAt
		s.store.Workers = append(s.store.Workers, item)
		if err := s.store.saveLocked(); err != nil {
			fail(w, 500, err.Error())
			return
		}
		respond(w, item)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) workerByID(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	user, _ := s.currentUser(r)
	id := strings.TrimPrefix(r.URL.Path, "/api/workers/")
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	idx := indexWorker(s.store.Workers, id)
	if idx < 0 {
		fail(w, 404, "worker 不存在")
		return
	}
	if nodeIdx := indexNode(s.store.Nodes, s.store.Workers[idx].NodeID); nodeIdx >= 0 && !hasNodeGroupAccess(user, s.store.Nodes[nodeIdx].Group) {
		fail(w, http.StatusForbidden, "没有 worker 节点分组权限")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var item Worker
		if err := readJSON(r, &item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if err := validateWorker(item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		nodeIdx := indexNode(s.store.Nodes, item.NodeID)
		if nodeIdx < 0 {
			fail(w, 400, "worker 节点不存在")
			return
		}
		if !hasNodeGroupAccess(user, s.store.Nodes[nodeIdx].Group) {
			fail(w, http.StatusForbidden, "没有 worker 节点分组权限")
			return
		}
		item.ID = id
		item.CreatedAt = s.store.Workers[idx].CreatedAt
		item.UpdatedAt = time.Now()
		s.store.Workers[idx] = item
	case http.MethodDelete:
		s.store.Workers = append(s.store.Workers[:idx], s.store.Workers[idx+1:]...)
		for pi := range s.store.Projects {
			s.store.Projects[pi].Build.WorkerIDs = removeString(s.store.Projects[pi].Build.WorkerIDs, id)
		}
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.store.saveLocked(); err != nil {
		fail(w, 500, err.Error())
		return
	}
	respond(w, map[string]bool{"ok": true})
}

func validateWorker(item Worker) error {
	if strings.TrimSpace(item.Name) == "" {
		return errors.New("worker 名称必填")
	}
	if strings.TrimSpace(item.NodeID) == "" {
		return errors.New("worker 节点必选")
	}
	if item.Weight < 0 || item.Weight > 9 {
		return errors.New("worker 权重必须是 0-9")
	}
	return nil
}

func (s *Server) notifications(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.store.mu.RLock()
		defer s.store.mu.RUnlock()
		respond(w, s.store.Notifications)
	case http.MethodPost:
		var item Notification
		if err := readJSON(r, &item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if err := validateNotification(item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		item.ID = s.store.newID("ntf")
		item.CreatedAt = time.Now()
		item.UpdatedAt = item.CreatedAt
		s.store.Notifications = append(s.store.Notifications, item)
		if err := s.store.saveLocked(); err != nil {
			fail(w, 500, err.Error())
			return
		}
		respond(w, item)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) notificationByID(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/notifications/"), "/")
	id := parts[0]
	if len(parts) == 2 && parts[1] == "test" {
		s.testNotification(w, r, id)
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	idx := indexNotification(s.store.Notifications, id)
	if idx < 0 {
		fail(w, 404, "通知不存在")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var item Notification
		if err := readJSON(r, &item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if err := validateNotification(item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		item.ID = id
		item.CreatedAt = s.store.Notifications[idx].CreatedAt
		item.UpdatedAt = time.Now()
		s.store.Notifications[idx] = item
	case http.MethodDelete:
		s.store.Notifications = append(s.store.Notifications[:idx], s.store.Notifications[idx+1:]...)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.store.saveLocked(); err != nil {
		fail(w, 500, err.Error())
		return
	}
	respond(w, map[string]bool{"ok": true})
}

func validateNotification(item Notification) error {
	if strings.TrimSpace(item.Code) == "" {
		return errors.New("通知 code 必填")
	}
	if item.Type != "wecom" && item.Type != "feishu" {
		return errors.New("通知类型必须是企业微信或飞书")
	}
	if strings.TrimSpace(item.HookURL) == "" {
		return errors.New("hook-url 必填")
	}
	if item.EmailEnabled && strings.TrimSpace(item.EmailTo) == "" {
		return errors.New("启用邮件时收件地址必填")
	}
	return nil
}

func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.store.mu.RLock()
		defer s.store.mu.RUnlock()
		respond(w, s.store.Users)
	case http.MethodPost:
		var item User
		if err := readJSON(r, &item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		normalizeUser(&item)
		if err := validateUser(item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if strings.TrimSpace(item.Password) == "" {
			fail(w, 400, "新用户密码必填")
			return
		}
		hash, err := hashPassword(item.Password)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		item.Password = hash
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		item.ID = s.store.newID("usr")
		item.CreatedAt = time.Now()
		item.UpdatedAt = item.CreatedAt
		s.store.Users = append(s.store.Users, item)
		if err := s.store.saveLocked(); err != nil {
			fail(w, 500, err.Error())
			return
		}
		respond(w, item)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) userByID(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/users/")
	current, _ := s.currentUser(r)
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	idx := indexUser(s.store.Users, id)
	if idx < 0 {
		fail(w, 404, "用户不存在")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var item User
		if err := readJSON(r, &item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		normalizeUser(&item)
		if err := validateUser(item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if strings.TrimSpace(item.Password) == "" {
			item.Password = s.store.Users[idx].Password
		} else {
			hash, err := hashPassword(item.Password)
			if err != nil {
				fail(w, 500, err.Error())
				return
			}
			item.Password = hash
		}
		item.ID = id
		item.CreatedAt = s.store.Users[idx].CreatedAt
		item.UpdatedAt = time.Now()
		s.store.Users[idx] = item
	case http.MethodDelete:
		if current.ID == id {
			fail(w, 400, "不能删除当前登录用户")
			return
		}
		if s.store.Users[idx].Role == "admin" && adminCount(s.store.Users) <= 1 {
			fail(w, 400, "至少保留一个管理员")
			return
		}
		s.store.Users = append(s.store.Users[:idx], s.store.Users[idx+1:]...)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.store.saveLocked(); err != nil {
		fail(w, 500, err.Error())
		return
	}
	respond(w, map[string]bool{"ok": true})
}

func normalizeUser(item *User) {
	item.Code = strings.TrimSpace(item.Code)
	item.Name = strings.TrimSpace(item.Name)
	item.Role = strings.TrimSpace(item.Role)
	if item.Role == "" {
		item.Role = "user"
	}
	perms := item.ProjectPerms[:0]
	seen := map[string]bool{}
	for _, perm := range item.ProjectPerms {
		perm.ProjectID = strings.TrimSpace(perm.ProjectID)
		if perm.ProjectID == "" || seen[perm.ProjectID] {
			continue
		}
		seen[perm.ProjectID] = true
		perms = append(perms, perm)
	}
	item.ProjectPerms = perms
	nodeIDs := item.NodeIDs[:0]
	seenNodes := map[string]bool{}
	for _, id := range item.NodeIDs {
		id = strings.TrimSpace(id)
		if id == "" || seenNodes[id] {
			continue
		}
		seenNodes[id] = true
		nodeIDs = append(nodeIDs, id)
	}
	item.NodeIDs = nodeIDs
	item.NodeGroups = normalizeNodeGroups(item.NodeGroups)
}

func splitGroup(group string) []string {
	raw := strings.FieldsFunc(group, func(r rune) bool {
		return r == ',' || r == '，'
	})
	var result []string
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s != "" {
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		result = []string{defaultNodeGroup}
	}
	return result
}

func normalizeMultiNodeGroup(group string) string {
	parts := splitGroup(group)
	var clean []string
	seen := map[string]bool{}
	for _, p := range parts {
		p = normalizeNodeGroup(p)
		if !seen[p] {
			seen[p] = true
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		return defaultNodeGroup
	}
	return strings.Join(clean, ",")
}

func normalizeNodeGroup(group string) string {
	group = strings.TrimSpace(group)
	if group == "" {
		return defaultNodeGroup
	}
	return group
}

func normalizeNodeGroups(groups []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, group := range groups {
		group = normalizeNodeGroup(group)
		if seen[group] {
			continue
		}
		seen[group] = true
		out = append(out, group)
	}
	return out
}

func validateUser(item User) error {
	if item.Code == "" {
		return errors.New("用户 code 必填")
	}
	if item.Name == "" {
		return errors.New("用户名称必填")
	}
	if item.Role != "admin" && item.Role != "user" {
		return errors.New("用户角色必须是管理员或普通用户")
	}
	return nil
}

func (s *Server) projects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		user, _ := s.currentUser(r)
		s.store.mu.RLock()
		defer s.store.mu.RUnlock()
		respond(w, filterProjectsForUser(s.store.Projects, user))
	case http.MethodPost:
		if !s.requireAdmin(w, r) {
			return
		}
		user, _ := s.currentUser(r)
		if !canCreateProject(user) {
			fail(w, http.StatusForbidden, "没有创建项目权限")
			return
		}
		var item Project
		if err := readJSON(r, &item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if strings.TrimSpace(item.Name) == "" {
			fail(w, 400, "项目名称必填")
			return
		}
		normalizeProject(&item)
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		item.ID = s.store.newID("proj")
		if strings.TrimSpace(item.Code) == "" {
			item.Code = s.store.projectCodeLocked()
		}
		if item.Retention.KeepReleases == 0 {
			item.Retention.KeepReleases = 5
		}
		item.CreatedAt = time.Now()
		item.UpdatedAt = item.CreatedAt
		s.store.Projects = append(s.store.Projects, item)
		if err := s.store.saveLocked(); err != nil {
			fail(w, 500, err.Error())
			return
		}
		respond(w, item)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) projectByID(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/")
	id := parts[0]
	if len(parts) == 2 && parts[1] == "test-git" {
		if !s.requireProjectPerm(w, r, id, "edit") {
			return
		}
		s.testGit(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "refs" {
		user, ok := s.currentUser(r)
		if !ok {
			fail(w, http.StatusUnauthorized, "请先登录")
			return
		}
		if id == "_draft" {
			if !canCreateProject(user) {
				fail(w, http.StatusForbidden, "没有创建项目权限")
				return
			}
		} else if !hasProjectAccess(user, id, "run") && !hasProjectAccess(user, id, "edit") {
			fail(w, http.StatusForbidden, "没有项目权限")
			return
		}
		s.projectRefs(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "test-build-node" {
		if !s.requireProjectPerm(w, r, id, "edit") {
			return
		}
		s.testBuildNode(w, r, id)
		return
	}
	if r.Method == http.MethodGet {
		if !s.requireProjectPerm(w, r, id, "view") {
			return
		}
	} else if r.Method == http.MethodPut || r.Method == http.MethodDelete {
		if !s.requireProjectPerm(w, r, id, "edit") {
			return
		}
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	idx := indexProject(s.store.Projects, id)
	if idx < 0 {
		fail(w, 404, "项目不存在")
		return
	}
	switch r.Method {
	case http.MethodGet:
		respond(w, s.store.Projects[idx])
		return
	case http.MethodPut:
		var item Project
		if err := readJSON(r, &item); err != nil {
			fail(w, 400, err.Error())
			return
		}
		item.ID = id
		item.CreatedAt = s.store.Projects[idx].CreatedAt
		item.UpdatedAt = time.Now()
		if item.Retention.KeepReleases == 0 {
			item.Retention.KeepReleases = 5
		}
		normalizeProject(&item)
		s.store.Projects[idx] = item
	case http.MethodDelete:
		s.store.Projects = append(s.store.Projects[:idx], s.store.Projects[idx+1:]...)
		records := s.store.Records[:0]
		for _, record := range s.store.Records {
			if record.ProjectID != id {
				records = append(records, record)
			}
		}
		s.store.Records = records
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.store.saveLocked(); err != nil {
		fail(w, 500, err.Error())
		return
	}
	respond(w, map[string]bool{"ok": true})
}

func normalizeProject(item *Project) {
	if item.Build.ArtifactSource == "" {
		item.Build.ArtifactSource = firstArtifactSource(*item)
	}
	if item.Build.PublishMode != "clean" {
		item.Build.PublishMode = "overwrite"
	}
	for i := range item.Environments {
		env := &item.Environments[i]
		if !env.CompileDeploy {
			env.BuildCommand = ""
		}
		for ai := range env.Artifacts {
			env.Artifacts[ai].Source = item.Build.ArtifactSource
			env.Artifacts[ai].NodeGroups = normalizeNodeGroups(env.Artifacts[ai].NodeGroups)
		}
	}
	if !item.Build.PreprocessEnabled {
		item.Build.PreprocessCommand = ""
	}
}

func (s *Server) records(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	projectID := r.URL.Query().Get("projectId")
	user, _ := s.currentUser(r)
	if projectID != "" && !hasProjectAccess(user, projectID, "view") {
		fail(w, http.StatusForbidden, "没有项目访问权限")
		return
	}
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	records := append([]Record(nil), s.store.Records...)
	if projectID != "" {
		filtered := records[:0]
		for _, item := range records {
			if item.ProjectID == projectID {
				filtered = append(filtered, item)
			}
		}
		records = filtered
	}
	if user.Role != "admin" {
		records = filterRecordsForUser(records, user)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].StartedAt.After(records[j].StartedAt) })
	respond(w, records)
}

func (s *Server) recordByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/records/")
	user, _ := s.currentUser(r)
	if r.Method == http.MethodGet {
		record, ok := s.recordByIDValue(id)
		if !ok {
			fail(w, 404, "发布记录不存在")
			return
		}
		if !hasProjectAccess(user, record.ProjectID, "view") {
			fail(w, http.StatusForbidden, "没有项目访问权限")
			return
		}
		record.Log = s.recordLog(id)
		respond(w, record)
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	idx := indexRecord(s.store.Records, id)
	if idx < 0 {
		fail(w, 404, "发布记录不存在")
		return
	}
	record := s.store.Records[idx]
	if !hasProjectAccess(user, record.ProjectID, "edit") {
		fail(w, http.StatusForbidden, "没有项目修改权限")
		return
	}
	if record.Status == "running" {
		fail(w, 400, "运行中的发布记录不能删除")
		return
	}
	if pidx := indexProject(s.store.Projects, record.ProjectID); pidx >= 0 {
		_ = os.RemoveAll(filepath.Join(".code-dep", "releases", s.store.Projects[pidx].Code, record.Version))
	}
	s.store.removeRecordFile(record.ProjectID, record.ID)
	s.store.Records = append(s.store.Records[:idx], s.store.Records[idx+1:]...)
	s.store.saveRecordMeta(record.ProjectID)
	if err := s.store.saveMeta(); err != nil {
		fail(w, 500, err.Error())
		return
	}
	respond(w, map[string]bool{"ok": true})
}

func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req publishRequest
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !s.requireProjectPerm(w, r, req.ProjectID, "run") {
		return
	}
	initiator, _ := s.currentUser(r)
	project, env, baseRecord, err := s.preparePublish(req)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := s.requireEnvNodePerm(initiator, env); err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}
	var worker Worker
	if baseRecord == nil {
		var ok bool
		worker, ok = s.selectPublishWorker(project, req.WorkerIDs, initiator)
		if !ok {
			fail(w, 400, "未配置可用 worker")
			return
		}
	}
	record := Record{
		ID:          s.randomID("rec"),
		ProjectID:   project.ID,
		ProjectName: project.Name,
		Env:         env.Name,
		Ref:         req.Ref,
		Version:     versionName(project.Code),
		Status:      "running",
		Mode:        req.Mode,
		StartedAt:   time.Now(),
	}
	if initiator.ID != "" {
		record.InitiatorID = initiator.ID
		record.InitiatorCode = initiator.Code
		record.InitiatorName = initiator.Name
	}
	if worker.ID != "" {
		record.WorkerID = worker.ID
		record.WorkerName = worker.Name
	}
	if record.Ref == "" {
		record.Ref = project.Git.Ref
	}
	if record.Mode == "" {
		record.Mode = "build"
	}
	if baseRecord != nil {
		record.Ref = baseRecord.Ref
		record.Version = baseRecord.Version
	}
	s.store.mu.Lock()
	s.store.Records = append(s.store.Records, record)
	s.store.saveRecordMeta(record.ProjectID)
	_ = s.store.saveMeta()
	s.store.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	s.registerPublish(record.ID, cancel)
	go s.runPublish(ctx, project, env, record, baseRecord, worker)
	respond(w, record)
}

func (s *Server) publishByID(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/publish/"), "/")
	if len(parts) != 2 || parts[1] != "stop" {
		fail(w, 404, "发布任务不存在")
		return
	}
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	recordID := parts[0]
	record, ok := s.recordByIDValue(recordID)
	if !ok {
		fail(w, 404, "发布记录不存在")
		return
	}
	if !s.requireProjectPerm(w, r, record.ProjectID, "run") {
		return
	}
	if record.Status != "running" {
		respond(w, map[string]bool{"ok": true})
		return
	}
	s.cancelPublish(recordID)
	line := fmt.Sprintf("[%s] 用户终止发布任务", time.Now().Format("15:04:05"))
	s.store.appendRecordLog(recordID, line)
	s.jobs.publish(recordID, line)
	s.markRecordStopped(record)
	s.jobs.publish(recordID, "__DONE__:stopped")
	respond(w, map[string]bool{"ok": true})
}

func (s *Server) preparePublish(req publishRequest) (Project, EnvConfig, *Record, error) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	idx := indexProject(s.store.Projects, req.ProjectID)
	if idx < 0 {
		return Project{}, EnvConfig{}, nil, errors.New("项目不存在")
	}
	project := s.store.Projects[idx]
	var env EnvConfig
	for _, item := range project.Environments {
		if item.Name == req.Env {
			env = item
			break
		}
	}
	if env.Name == "" {
		return Project{}, EnvConfig{}, nil, errors.New("发布环境不存在")
	}
	if req.Mode == "redeploy" {
		idx := indexRecord(s.store.Records, req.RecordID)
		if idx < 0 || s.store.Records[idx].ProjectID != project.ID {
			return Project{}, EnvConfig{}, nil, errors.New("历史版本不存在")
		}
		base := s.store.Records[idx]
		return project, env, &base, nil
	}
	return project, env, nil, nil
}

func (s *Server) requireEnvNodePerm(user User, env EnvConfig) error {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	for _, artifact := range env.Artifacts {
		for _, group := range artifact.NodeGroups {
			group = normalizeNodeGroup(group)
			if hasNodeGroupAccess(user, group) {
				continue
			}
			return fmt.Errorf("没有目标节点分组发布权限: %s", group)
		}
		if len(artifact.NodeGroups) > 0 {
			continue
		}
		for _, nodeID := range artifact.NodeIDs {
			if hasNodeAccess(user, nodeID) {
				continue
			}
			label := nodeID
			if idx := indexNode(s.store.Nodes, nodeID); idx >= 0 {
				label = s.store.Nodes[idx].Code
				if hasNodeGroupAccess(user, s.store.Nodes[idx].Group) {
					continue
				}
			}
			return fmt.Errorf("没有目标节点发布权限: %s", label)
		}
	}
	return nil
}

func (s *Server) registerPublish(id string, cancel context.CancelFunc) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	s.running[id] = cancel
}

func (s *Server) cancelPublish(id string) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	if cancel, ok := s.running[id]; ok {
		cancel()
		delete(s.running, id)
	}
}

func (s *Server) clearPublish(id string) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	delete(s.running, id)
}

func (s *Server) selectPublishWorker(project Project, overrideIDs []string, user User) (Worker, bool) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	workerIDs := project.Build.WorkerIDs
	if len(overrideIDs) > 0 {
		workerIDs = overrideIDs
	}
	allowed := map[string]bool{}
	for _, id := range workerIDs {
		if strings.TrimSpace(id) != "" {
			allowed[id] = true
		}
	}
	candidates := []Worker{}
	for _, worker := range s.store.Workers {
		if len(allowed) > 0 && !allowed[worker.ID] {
			continue
		}
		nodeIdx := indexNode(s.store.Nodes, worker.NodeID)
		if nodeIdx < 0 {
			continue
		}
		candidates = append(candidates, worker)
	}
	if len(candidates) == 0 {
		return Worker{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Weight == candidates[j].Weight {
			return candidates[i].Name < candidates[j].Name
		}
		return candidates[i].Weight < candidates[j].Weight
	})
	start := 0
	if len(candidates) > 1 {
		minWeight := candidates[0].Weight
		sameWeight := 0
		for sameWeight < len(candidates) && candidates[sameWeight].Weight == minWeight {
			sameWeight++
		}
		start = mrand.Intn(sameWeight)
	}
	busySince := map[string]time.Time{}
	for _, r := range s.store.Records {
		if r.Status == "running" && r.WorkerID != "" {
			if t, ok := busySince[r.WorkerID]; !ok || r.StartedAt.Before(t) {
				busySince[r.WorkerID] = r.StartedAt
			}
		}
	}
	for i := 0; i < len(candidates); i++ {
		worker := candidates[(start+i)%len(candidates)]
		if _, busy := busySince[worker.ID]; !busy {
			return worker, true
		}
	}
	chosen := candidates[0]
	oldest := busySince[chosen.ID]
	for _, worker := range candidates[1:] {
		if t := busySince[worker.ID]; t.Before(oldest) {
			chosen = worker
			oldest = t
		}
	}
	return chosen, true
}

func (s *Server) markRecordStopped(record Record) {
	ended := time.Now()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if idx := indexRecord(s.store.Records, record.ID); idx >= 0 {
		s.store.Records[idx].Status = "stopped"
		s.store.Records[idx].EndedAt = &ended
	}
	if idx := indexProject(s.store.Projects, record.ProjectID); idx >= 0 {
		s.store.Projects[idx].LastStatus = "stopped"
		s.store.Projects[idx].LastPublishedAt = &ended
		s.store.Projects[idx].UpdatedAt = ended
		_ = s.store.saveProject(s.store.Projects[idx])
	}
	s.store.saveRecordMeta(record.ProjectID)
}

func (s *Server) runPublish(ctx context.Context, project Project, env EnvConfig, record Record, baseRecord *Record, worker Worker) {
	logLine := func(format string, args ...any) {
		line := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
		s.store.appendRecordLog(record.ID, line)
		s.jobs.publish(record.ID, line)
	}
	finish := func(status string) {
		s.clearPublish(record.ID)
		ended := time.Now()
		s.store.mu.Lock()
		if idx := indexRecord(s.store.Records, record.ID); idx >= 0 {
			if s.store.Records[idx].Status == "stopped" {
				s.store.mu.Unlock()
				return
			}
			s.store.Records[idx].Status = status
			s.store.Records[idx].EndedAt = &ended
		}
		if idx := indexProject(s.store.Projects, project.ID); idx >= 0 {
			s.store.Projects[idx].LastStatus = status
			s.store.Projects[idx].LastPublishedAt = &ended
			s.store.Projects[idx].UpdatedAt = ended
			_ = s.store.saveProject(s.store.Projects[idx])
		}
		s.store.saveRecordMeta(record.ProjectID)
		s.store.mu.Unlock()
		s.jobs.publish(record.ID, fmt.Sprintf("__DONE__:%s", status))
		go s.sendNotify(project, env, record, status, ended)
	}

	defer func() {
		s.clearPublish(record.ID)
		if r := recover(); r != nil {
			logLine("发布异常: %v", r)
			finish("failed")
		}
	}()

	logLine("开始发布 %s -> %s，模式: %s", project.Name, env.Name, record.Mode)
	releaseDir := filepath.Join(".code-dep", "releases", project.Code, record.Version)
	if baseRecord != nil {
		releaseDir = filepath.Join(".code-dep", "releases", project.Code, baseRecord.Version)
		logLine("使用历史版本 %s，跳过拉取和编译", baseRecord.Version)
	} else {
		logLine("当前工作 worker: %s（权重 %d）", worker.Name, worker.Weight)
		gitSecret, _ := findSecret(s.store, project.Git.SecretID)
		buildNode, ok := findNode(s.store, worker.NodeID)
		if !ok {
			logLine("worker 节点不存在: %s", worker.NodeID)
			finish("failed")
			return
		}
		if buildNode.Type == "ssh" {
			workDir := remoteWorkerProjectDir(project, worker, buildNode)
			secret, _ := findSecret(s.store, buildNode.SecretID)
			if err := remoteMkdir(ctx, buildNode, secret, pathDirRemote(workDir), logLine); err != nil {
				logLine("创建远程工作目录失败: %v", err)
				finish("failed")
				return
			}
			if strings.TrimSpace(project.Git.URL) != "" {
				logLine("清理远程项目目录: %s", workDir)
				if err := runShellRemote(ctx, buildNode, secret, "rm -rf "+shQuote(workDir), "", logLine); err != nil {
					logLine("清理远程项目目录失败: %v", err)
					finish("failed")
					return
				}
			} else if err := remoteMkdir(ctx, buildNode, secret, workDir, logLine); err != nil {
				logLine("创建远程项目目录失败: %v", err)
				finish("failed")
				return
			}
			if err := syncGitRemote(ctx, buildNode, secret, gitSecret, project, record.Ref, workDir, logLine); err != nil {
				logLine("远程源码准备失败: %v", err)
				finish("failed")
				return
			}
			if project.Build.PreprocessEnabled && strings.TrimSpace(project.Build.PreprocessCommand) != "" {
				logLine("执行预处理命令")
				if err := runShellRemote(ctx, buildNode, secret, project.Build.PreprocessCommand, workDir, logLine); err != nil {
					logLine("远程预处理失败: %v", err)
					finish("failed")
					return
				}
			} else {
				logLine("未配置预处理命令，跳过预处理")
			}
			if env.CompileDeploy && strings.TrimSpace(env.BuildCommand) != "" {
				logLine("执行目标编译命令: %s", env.Name)
				buildScript := wrapBuildEnv(env, env.BuildCommand)
				if err := runShellRemote(ctx, buildNode, secret, buildScript, workDir, logLine); err != nil {
					logLine("远程编译失败: %v", err)
					finish("failed")
					return
				}
			} else {
				logLine("未启用目标编译发布，跳过编译")
			}
			if err := packageRemoteRelease(ctx, buildNode, secret, workDir, releaseDir, logLine); err != nil {
				logLine("同步远程构建版本失败: %v", err)
				finish("failed")
				return
			}
		} else {
			workDir := localWorkerProjectDir(project, worker, buildNode)
			if err := os.MkdirAll(filepath.Dir(workDir), 0755); err != nil {
				logLine("创建工作目录失败: %v", err)
				finish("failed")
				return
			}
			if strings.TrimSpace(project.Git.URL) != "" {
				logLine("清理项目目录: %s", workDir)
				if err := os.RemoveAll(workDir); err != nil {
					logLine("清理项目目录失败: %v", err)
					finish("failed")
					return
				}
			} else if err := os.MkdirAll(workDir, 0755); err != nil {
				logLine("创建项目目录失败: %v", err)
				finish("failed")
				return
			}
			if err := syncGit(ctx, project, gitSecret, record.Ref, workDir, logLine); err != nil {
				logLine("源码准备失败: %v", err)
				finish("failed")
				return
			}
			if project.Build.PreprocessEnabled && strings.TrimSpace(project.Build.PreprocessCommand) != "" {
				logLine("执行预处理命令")
				if err := runShell(ctx, project.Build.PreprocessCommand, workDir, logLine); err != nil {
					logLine("预处理失败: %v", err)
					finish("failed")
					return
				}
			} else {
				logLine("未配置预处理命令，跳过预处理")
			}
			if env.CompileDeploy && strings.TrimSpace(env.BuildCommand) != "" {
				logLine("执行目标编译命令: %s", env.Name)
				buildScript := wrapBuildEnv(env, env.BuildCommand)
				if err := runShell(ctx, buildScript, workDir, logLine); err != nil {
					logLine("编译失败: %v", err)
					finish("failed")
					return
				}
			} else {
				logLine("未启用目标编译发布，跳过编译")
			}
			if err := packageRelease(workDir, releaseDir, logLine); err != nil {
				logLine("保存构建版本失败: %v", err)
				finish("failed")
				return
			}
		}
	}
	if err := deployArtifacts(ctx, s.store, project, env, releaseDir, logLine); err != nil {
		logLine("部署失败: %v", err)
		finish("failed")
		return
	}
	if strings.TrimSpace(env.DeployCommand) == "" {
		logLine("未配置发布命令，部署文件同步完成即视为部署成功")
	}
	cleanupReleases(project, logLine)
	if !hasNotification(project) {
		logLine("未配置消息通知，跳过消息推送")
	}
	logLine("发布完成")
	finish("success")
}

func syncGit(ctx context.Context, project Project, gitSecret Secret, ref, workDir string, logLine func(string, ...any)) error {
	if project.Git.URL == "" {
		logLine("未配置 git 地址，使用当前工作目录作为源码目录")
		return nil
	}
	gitURL := gitURLWithCredential(project.Git.URL, gitSecret)
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		logLine("克隆源码: %s", project.Git.URL)
		if err := runCommand(ctx, "", redactLog(logLine, gitURL), "git", "clone", gitURL, workDir); err != nil {
			return err
		}
	} else {
		logLine("更新源码")
		if gitURL != project.Git.URL {
			_ = runCommand(ctx, workDir, redactLog(logLine, gitURL), "git", "remote", "set-url", "origin", gitURL)
		}
		if err := runCommand(ctx, workDir, redactLog(logLine, gitURL), "git", "fetch", "--all", "--tags", "--prune"); err != nil {
			return err
		}
	}
	if ref == "" {
		ref = project.Git.Ref
	}
	if ref != "" {
		logLine("切换版本: %s", ref)
		return runCommand(ctx, workDir, logLine, "git", "checkout", ref)
	}
	return nil
}

func syncGitRemote(ctx context.Context, node Node, secret Secret, gitSecret Secret, project Project, ref, workDir string, logLine func(string, ...any)) error {
	if project.Git.URL == "" {
		logLine("未配置 git 地址，使用远程工作目录作为源码目录")
		return nil
	}
	if ref == "" {
		ref = project.Git.Ref
	}
	gitURL := gitURLWithCredential(project.Git.URL, gitSecret)
	safeLog := redactLog(logLine, gitURL)
	check := fmt.Sprintf("test -d %s/.git", shQuote(workDir))
	clone := fmt.Sprintf("git clone %s %s", shQuote(gitURL), shQuote(workDir))
	fetch := fmt.Sprintf("cd %s && git fetch --all --tags --prune", shQuote(workDir))
	if err := runShellRemote(ctx, node, secret, check+" || "+clone, "", safeLog); err != nil {
		return err
	}
	if err := runShellRemote(ctx, node, secret, fmt.Sprintf("git config --global --add safe.directory %s || true", shQuote(workDir)), "", logLine); err != nil {
		return err
	}
	if gitURL != project.Git.URL {
		_ = runShellRemote(ctx, node, secret, fmt.Sprintf("cd %s && git remote set-url origin %s", shQuote(workDir), shQuote(gitURL)), "", safeLog)
	}
	if err := runShellRemote(ctx, node, secret, fetch, "", safeLog); err != nil {
		return err
	}
	if ref != "" {
		logLine("远程切换版本: %s", ref)
		return runShellRemote(ctx, node, secret, fmt.Sprintf("cd %s && git checkout %s", shQuote(workDir), shQuote(ref)), "", logLine)
	}
	return nil
}

func packageRelease(workDir, releaseDir string, logLine func(string, ...any)) error {
	_ = os.RemoveAll(releaseDir)
	if err := os.MkdirAll(releaseDir, 0755); err != nil {
		return err
	}
	logLine("保存构建产物: %s", releaseDir)
	return copyPath(workDir, releaseDir, func(path string) bool {
		base := filepath.Base(path)
		return base == ".git" || base == "node_modules"
	})
}

func packageRemoteRelease(ctx context.Context, node Node, secret Secret, workDir, releaseDir string, logLine func(string, ...any)) error {
	_ = os.RemoveAll(releaseDir)
	if err := os.MkdirAll(releaseDir, 0755); err != nil {
		return err
	}
	logLine("同步远程构建产物: %s -> %s", node.Code, releaseDir)
	return downloadRemoteDir(ctx, node, secret, workDir, releaseDir, logLine)
}

func deployArtifacts(ctx context.Context, store *Store, project Project, env EnvConfig, releaseDir string, logLine func(string, ...any)) error {
	artifactSource := strings.TrimSpace(project.Build.ArtifactSource)
	if artifactSource == "" {
		artifactSource = "."
	}
	publishMode := project.Build.PublishMode
	if publishMode != "clean" {
		publishMode = "overwrite"
	}
	deployCommand := normalizeDeployScript(env.DeployCommand)
	commandTargets := map[string]bool{}

	// 收集并打印目标部署节点列表
	var allNodes []Node
	seenNodeIDs := map[string]bool{}
	for _, artifact := range env.Artifacts {
		for _, node := range artifactTargetNodes(store, artifact) {
			if !seenNodeIDs[node.ID] {
				seenNodeIDs[node.ID] = true
				allNodes = append(allNodes, node)
			}
		}
	}
	var nodeCodes []string
	for _, n := range allNodes {
		nodeCodes = append(nodeCodes, n.Code)
	}
	if len(nodeCodes) > 0 {
		logLine("目标部署节点列表: %s", strings.Join(nodeCodes, ","))
	}

	for _, artifact := range env.Artifacts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.TrimSpace(artifact.TargetDir) == "" {
			continue
		}
		source := releaseSourcePath(releaseDir, artifactSource)
		if _, err := os.Stat(source); err != nil {
			return fmt.Errorf("产物不存在: %s（已查找 %s）", artifactSource, source)
		}
		nodes := artifactTargetNodes(store, artifact)
		if len(nodes) == 0 {
			return fmt.Errorf("目标分组没有可发布节点: %s", strings.Join(artifact.NodeGroups, "、"))
		}
		for _, node := range nodes {
			if err := ctx.Err(); err != nil {
				return err
			}
			logLine("开始部署节点: %s", node.Code)

			err := func() error {
				if node.Type == "ssh" {
					secret, _ := findSecret(store, node.SecretID)
					logLine("发布到远程节点 %s -> %s", node.Code, artifact.TargetDir)
					if publishMode == "clean" {
						logLine("清理远程目标目录: %s", artifact.TargetDir)
						if err := runShellRemote(ctx, node, secret, "rm -rf "+shQuote(artifact.TargetDir)+" && mkdir -p "+shQuote(artifact.TargetDir), "", logLine); err != nil {
							return err
						}
					} else {
						if err := remoteMkdir(ctx, node, secret, artifact.TargetDir, logLine); err != nil {
							return err
						}
					}
					if err := uploadLocalPath(ctx, node, secret, source, artifact.TargetDir, logLine); err != nil {
						return err
					}
					key := node.ID + "|" + artifact.TargetDir
					if deployCommand != "" && !commandTargets[key] {
						commandTargets[key] = true
						logLine("在远程目标目录执行发布命令: %s -> %s", node.Code, artifact.TargetDir)
						if err := runShellRemote(ctx, node, secret, deployCommand, artifact.TargetDir, logLine); err != nil {
							return fmt.Errorf("远程发布命令失败: %w", err)
						}
					}
					return nil
				}
				targetDir := artifact.TargetDir
				if !filepath.IsAbs(targetDir) && node.BaseDir != "" {
					targetDir = filepath.Join(node.BaseDir, targetDir)
				}
				logLine("发布到本地节点 %s -> %s", node.Code, targetDir)
				if publishMode == "clean" {
					logLine("清理本地目标目录: %s", targetDir)
					if err := os.RemoveAll(targetDir); err != nil {
						return err
					}
				}
				if err := os.MkdirAll(targetDir, 0755); err != nil {
					return err
				}
				if err := copyArtifactContent(source, targetDir); err != nil {
					return err
				}
				key := node.ID + "|" + targetDir
				if deployCommand != "" && !commandTargets[key] {
					commandTargets[key] = true
					logLine("在本地目标目录执行发布命令: %s -> %s", node.Code, targetDir)
					if err := runShell(ctx, deployCommand, targetDir, logLine); err != nil {
						return fmt.Errorf("本地发布命令失败: %w", err)
					}
				}
				return nil
			}()

			if err != nil {
				logLine("节点 %s 部署失败: %v", node.Code, err)
				return err
			}
			logLine("节点 %s 部署成功", node.Code)
		}
	}
	logLine("部署文件同步完成")
	return nil
}

func artifactTargetNodes(store *Store, artifact ArtifactRule) []Node {
	nodes := []Node{}
	seen := map[string]bool{}
	groups := normalizeNodeGroups(artifact.NodeGroups)
	if len(groups) > 0 {
		groupSet := map[string]bool{}
		for _, group := range groups {
			groupSet[group] = true
		}
		store.mu.RLock()
		defer store.mu.RUnlock()
		for _, node := range store.Nodes {
			subGroups := splitGroup(node.Group)
			matched := false
			for _, sg := range subGroups {
				if groupSet[normalizeNodeGroup(sg)] {
					matched = true
					break
				}
			}
			if !matched || seen[node.ID] {
				continue
			}
			seen[node.ID] = true
			nodes = append(nodes, node)
		}
		return nodes
	}
	for _, nodeID := range artifact.NodeIDs {
		if nodeID == "" || seen[nodeID] {
			continue
		}
		if node, ok := findNode(store, nodeID); ok {
			seen[nodeID] = true
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func releaseSourcePath(releaseDir, source string) string {
	clean := filepath.Clean(source)
	if filepath.IsAbs(clean) {
		return filepath.Join(releaseDir, filepath.Base(clean))
	}
	return filepath.Join(releaseDir, clean)
}

func gitURLWithCredential(raw string, secret Secret) string {
	if raw == "" || secret.ID == "" {
		return raw
	}
	user := urlEscape(secret.Username)
	pass := secret.Password
	if pass == "" {
		pass = secret.Token
	}
	if user == "" && secret.Token != "" {
		user = "oauth2"
	}
	if user == "" && pass == "" {
		return raw
	}
	if strings.Contains(raw, "://") {
		parts := strings.SplitN(raw, "://", 2)
		cred := user
		if pass != "" {
			cred += ":" + urlEscape(pass)
		}
		return parts[0] + "://" + cred + "@" + parts[1]
	}
	return raw
}

func redactLog(logLine func(string, ...any), secrets ...string) func(string, ...any) {
	return func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		for _, secret := range secrets {
			if secret != "" {
				msg = strings.ReplaceAll(msg, secret, redactURL(secret))
			}
		}
		logLine("%s", msg)
	}
}

func redactURL(raw string) string {
	if !strings.Contains(raw, "@") || !strings.Contains(raw, "://") {
		return raw
	}
	parts := strings.SplitN(raw, "://", 2)
	host := parts[1]
	if idx := strings.Index(host, "@"); idx >= 0 {
		host = host[idx+1:]
	}
	return parts[0] + "://***@" + host
}

func urlEscape(s string) string {
	replacer := strings.NewReplacer("%", "%25", "@", "%40", ":", "%3A", "/", "%2F", "?", "%3F", "#", "%23", "&", "%26", "=", "%3D")
	return replacer.Replace(s)
}

func wrapBuildEnv(env EnvConfig, script string) string {
	var parts []string
	if env.Goos != "" {
		parts = append(parts, "GOOS="+shQuote(env.Goos))
	}
	if env.Goarch != "" {
		parts = append(parts, "GOARCH="+shQuote(env.Goarch))
	}
	if len(parts) == 0 {
		return script
	}
	return "export " + strings.Join(parts, " ") + "\n" + script
}

func runShell(ctx context.Context, script, dir string, logLine func(string, ...any)) error {
	script = strings.TrimSpace(script)
	if script == "" {
		return nil
	}
	logShellCommands("执行命令", script, logLine)
	return runCommand(ctx, dir, logLine, "sh", "-lc", "set -e\n"+script)
}

func runShellRemote(ctx context.Context, node Node, secret Secret, script, dir string, logLine func(string, ...any)) error {
	script = strings.TrimSpace(script)
	if script == "" {
		return nil
	}
	if dir != "" {
		script = "cd " + shQuote(dir) + "\n" + script
	}
	logShellCommands(fmt.Sprintf("远程执行 %s", node.Code), script, logLine)
	return runSSHCommand(ctx, node, secret, "set -e\n"+script, logLine)
}

func remoteMkdir(ctx context.Context, node Node, secret Secret, dir string, logLine func(string, ...any)) error {
	return runShellRemote(ctx, node, secret, "mkdir -p "+shQuote(dir), "", logLine)
}

func shellCommands(script string) []string {
	var commands []string
	for _, line := range strings.Split(strings.ReplaceAll(script, "\r\n", "\n"), "\n") {
		command := strings.TrimSpace(line)
		if command == "" || strings.HasPrefix(command, "#") {
			continue
		}
		commands = append(commands, command)
	}
	return commands
}

func logShellCommands(prefix, script string, logLine func(string, ...any)) {
	commands := shellCommands(script)
	for i, command := range commands {
		logLine("%s [%d/%d]: %s", prefix, i+1, len(commands), command)
	}
}

func normalizeDeployScript(script string) string {
	commands := shellCommands(script)
	for i, command := range commands {
		commands[i] = normalizeDeployCommand(command)
	}
	return strings.Join(commands, "\n")
}

func normalizeDeployCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return command
	}
	noWait := false
	if strings.HasPrefix(command, "exec none return ") {
		noWait = true
		command = strings.TrimSpace(strings.TrimPrefix(command, "exec none return "))
	}
	if command == "" {
		return command
	}
	background := strings.HasSuffix(command, "&")
	if background {
		command = strings.TrimSpace(strings.TrimSuffix(command, "&"))
	}
	if noWait || background || strings.HasPrefix(command, "nohup ") {
		return detachDeployCommand(command)
	}
	return command
}

func detachDeployCommand(command string) string {
	return `_code_dep_log=.code-dep-deploy-$(date +%Y%m%d%H%M%S).log; ` +
		`(` + command + `) </dev/null >> "$_code_dep_log" 2>&1 & ` +
		`sleep 1; head -n 20 "$_code_dep_log" 2>/dev/null || true; ` +
		`echo "后台命令已启动，日志: $_code_dep_log"`
}

func runCommand(ctx context.Context, dir string, logLine func(string, ...any), name string, args ...string) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, commandTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	pipe := func(r io.Reader) {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		var pending string
		for {
			n, err := r.Read(buf)
			if n > 0 {
				pending += string(buf[:n])
				lines := strings.Split(pending, "\n")
				pending = lines[len(lines)-1]
				for _, line := range lines[:len(lines)-1] {
					if strings.TrimSpace(line) != "" {
						logLine("%s", line)
					}
				}
			}
			if err != nil {
				if strings.TrimSpace(pending) != "" {
					logLine("%s", pending)
				}
				return
			}
		}
	}
	wg.Add(2)
	go pipe(stdout)
	go pipe(stderr)
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("命令超时: %s", name)
		}
		return err
	}
	return nil
}

func copyPath(src, dst string, skip func(string) bool) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if skip != nil && skip(src) {
		return nil
	}
	if !info.IsDir() {
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		return copyFile(src, dst, info.Mode())
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if skip != nil && skip(path) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyArtifactContent(src, targetDir string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyPath(src, filepath.Join(targetDir, filepath.Base(src)), nil)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := copyPath(filepath.Join(src, entry.Name()), filepath.Join(targetDir, entry.Name()), nil); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func runSSHCommand(ctx context.Context, node Node, secret Secret, script string, logLine func(string, ...any)) error {
	client, err := dialSSH(ctx, node, secret)
	if err != nil {
		return err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	stdout, _ := session.StdoutPipe()
	stderr, _ := session.StderrPipe()
	if err := session.Start("sh -lc " + shQuote(script)); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go scanLines(stdout, logLine, &wg)
		go scanLines(stderr, logLine, &wg)
		wg.Wait()
		done <- session.Wait()
	}()
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		return fmt.Errorf("远程命令超时或取消: %w", ctx.Err())
	case err := <-done:
		return err
	}
}

func scanLines(r io.Reader, logLine func(string, ...any), wg *sync.WaitGroup) {
	defer wg.Done()
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			logLine("%s", line)
		}
	}
}

func dialSSH(ctx context.Context, node Node, secret Secret) (*ssh.Client, error) {
	config, err := sshClientConfig(node, secret)
	if err != nil {
		return nil, err
	}
	port := node.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(node.Host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: sshTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

func sshClientConfig(node Node, secret Secret) (*ssh.ClientConfig, error) {
	user := node.User
	if user == "" {
		user = secret.Username
	}
	if user == "" {
		user = "root"
	}
	auths := []ssh.AuthMethod{}
	if signer, err := sshSigner(secret); err == nil && signer != nil {
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if secret.Password != "" {
		auths = append(auths, ssh.Password(secret.Password), ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = secret.Password
			}
			return answers, nil
		}))
	}
	if len(auths) == 0 {
		return nil, errors.New("SSH 节点未配置可用认证信息：请配置密码或私钥")
	}
	return &ssh.ClientConfig{
		User:            user,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         sshTimeout,
	}, nil
}

func sshSigner(secret Secret) (ssh.Signer, error) {
	key := strings.TrimSpace(secret.PrivateKey)
	if key == "" {
		return nil, nil
	}
	if strings.HasPrefix(key, "~") {
		home, _ := os.UserHomeDir()
		key = filepath.Join(home, strings.TrimPrefix(key, "~/"))
	}
	if strings.HasPrefix(key, "/") {
		b, err := os.ReadFile(key)
		if err != nil {
			return nil, err
		}
		key = string(b)
	}
	if !strings.Contains(key, "BEGIN") {
		return nil, nil
	}
	return ssh.ParsePrivateKey([]byte(key))
}

func downloadRemoteDir(ctx context.Context, node Node, secret Secret, remoteDir, localDir string, logLine func(string, ...any)) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, commandTimeout)
		defer cancel()
	}
	client, err := dialSSH(ctx, node, secret)
	if err != nil {
		return err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, _ := session.StderrPipe()
	logLine("远程打包传输: %s:%s -> %s", node.Code, remoteDir, localDir)
	script := fmt.Sprintf("cd %s && tar --exclude=.git --exclude=node_modules -czf - .", shQuote(remoteDir))
	if err := session.Start("sh -lc " + shQuote(script)); err != nil {
		return err
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go scanLines(stderr, logLine, &wg)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Signal(ssh.SIGKILL)
		case <-done:
		}
	}()
	counter := newTransferCounter("远程构建产物同步", logLine)
	err = extractTarGzip(ctx, stdout, localDir, counter)
	close(done)
	wg.Wait()
	waitErr := session.Wait()
	counter.done()
	if err != nil {
		return err
	}
	if waitErr != nil {
		return waitErr
	}
	return nil
}

func uploadLocalPath(ctx context.Context, node Node, secret Secret, localPath, remoteDir string, logLine func(string, ...any)) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, commandTimeout)
		defer cancel()
	}
	client, err := dialSSH(ctx, node, secret)
	if err != nil {
		return err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	stdout, _ := session.StdoutPipe()
	stderr, _ := session.StderrPipe()
	logLine("流式发布文件: %s -> %s:%s", localPath, node.Code, remoteDir)
	script := fmt.Sprintf("mkdir -p %s && cd %s && tar -xzf -", shQuote(remoteDir), shQuote(remoteDir))
	if err := session.Start("sh -lc " + shQuote(script)); err != nil {
		return err
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go scanLines(stdout, logLine, &wg)
	go scanLines(stderr, logLine, &wg)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Signal(ssh.SIGKILL)
		case <-done:
		}
	}()
	counter := newTransferCounter("发布文件同步", logLine)
	err = writeTarGzip(ctx, stdin, localPath, counter)
	_ = stdin.Close()
	close(done)
	wg.Wait()
	waitErr := session.Wait()
	counter.done()
	if err != nil {
		return err
	}
	if waitErr != nil {
		return waitErr
	}
	return nil
}

type transferCounter struct {
	label     string
	files     int
	bytes     int64
	lastFiles int
	lastBytes int64
	lastLog   time.Time
	logLine   func(string, ...any)
}

func newTransferCounter(label string, logLine func(string, ...any)) *transferCounter {
	return &transferCounter{label: label, lastLog: time.Now(), logLine: logLine}
}

func (p *transferCounter) addBytes(n int) {
	p.bytes += int64(n)
	p.maybeLog(false)
}

func (p *transferCounter) addFile() {
	p.files++
	p.maybeLog(false)
}

func (p *transferCounter) done() {
	p.logLine("%s完成: %d 个文件，%s", p.label, p.files, humanBytes(p.bytes))
}

func (p *transferCounter) maybeLog(force bool) {
	if !force && time.Since(p.lastLog) < 3*time.Second && p.files-p.lastFiles < 200 && p.bytes-p.lastBytes < 20*1024*1024 {
		return
	}
	p.logLine("%s进度: %d 个文件，%s", p.label, p.files, humanBytes(p.bytes))
	p.lastLog = time.Now()
	p.lastFiles = p.files
	p.lastBytes = p.bytes
}

func extractTarGzip(ctx context.Context, r io.Reader, targetDir string, progress *transferCounter) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, ok := safeTarTarget(targetDir, header.Name)
		if !ok {
			continue
		}
		mode := os.FileMode(header.Mode)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, mode); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if err := copyWithProgress(ctx, out, tr, progress); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
			progress.addFile()
		}
	}
}

func writeTarGzip(ctx context.Context, w io.Writer, source string, progress *transferCounter) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err := writeTarEntries(ctx, tw, source, progress)
	if closeErr := tw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := gz.Close(); err == nil {
		err = closeErr
	}
	return err
}

func writeTarEntries(ctx context.Context, tw *tar.Writer, source string, progress *transferCounter) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return addPathToTar(ctx, tw, source, filepath.Base(source), info, progress)
	}
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		return addPathToTar(ctx, tw, path, filepath.ToSlash(rel), info, progress)
	})
}

func addPathToTar(ctx context.Context, tw *tar.Writer, path, name string, info os.FileInfo, progress *transferCounter) error {
	if !info.Mode().IsRegular() && !info.IsDir() {
		return nil
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = name
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := copyWithProgress(ctx, tw, in, progress); err != nil {
		return err
	}
	progress.addFile()
	return nil
}

func copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader, progress *transferCounter) error {
	buf := make([]byte, 256*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			written, writeErr := dst.Write(buf[:n])
			if written > 0 {
				progress.addBytes(written)
			}
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func safeTarTarget(baseDir, name string) (string, bool) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", false
	}
	return filepath.Join(baseDir, clean), true
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	units := []string{"KB", "MB", "GB", "TB"}
	for _, suffix := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f PB", value/unit)
}

func pathJoinRemote(base, rel string) string {
	if rel == "." || rel == "" {
		return strings.TrimRight(base, "/")
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(rel, "/")
}

func pathDirRemote(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx <= 0 {
		return "."
	}
	return path[:idx]
}

func localWorkerRoot(worker Worker, node Node) string {
	dir := worker.WorkDir
	if dir == "" {
		dir = filepath.Join(".code-dep", "workspaces")
	}
	if !filepath.IsAbs(dir) && node.BaseDir != "" {
		return filepath.Join(node.BaseDir, dir)
	}
	return dir
}

func localWorkerProjectDir(project Project, worker Worker, node Node) string {
	return filepath.Join(localWorkerRoot(worker, node), project.Code)
}

func remoteWorkerRoot(worker Worker, node Node) string {
	dir := worker.WorkDir
	if dir == "" {
		dir = ".code-dep/workspaces"
	}
	if strings.HasPrefix(dir, "/") || node.BaseDir == "" {
		return dir
	}
	return strings.TrimRight(node.BaseDir, "/") + "/" + strings.TrimLeft(dir, "/")
}

func remoteWorkerProjectDir(project Project, worker Worker, node Node) string {
	return pathJoinRemote(remoteWorkerRoot(worker, node), project.Code)
}

func localWorkDir(project Project, node Node) string {
	dir := project.Build.WorkDir
	if dir == "" {
		dir = filepath.Join(".code-dep", "workspaces", project.Code)
	}
	if !filepath.IsAbs(dir) && node.BaseDir != "" {
		return filepath.Join(node.BaseDir, dir)
	}
	return dir
}

func remoteWorkDir(project Project, node Node) string {
	dir := project.Build.WorkDir
	if dir == "" {
		dir = ".code-dep/workspaces/" + project.Code
	}
	if strings.HasPrefix(dir, "/") || node.BaseDir == "" {
		return dir
	}
	return strings.TrimRight(node.BaseDir, "/") + "/" + strings.TrimLeft(dir, "/")
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func cleanupReleases(project Project, logLine func(string, ...any)) {
	keep := project.Retention.KeepReleases
	if keep <= 0 {
		keep = 5
	}
	root := filepath.Join(".code-dep", "releases", project.Code)
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) <= keep {
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		ai, _ := entries[i].Info()
		aj, _ := entries[j].Info()
		return ai.ModTime().After(aj.ModTime())
	})
	for _, entry := range entries[keep:] {
		_ = os.RemoveAll(filepath.Join(root, entry.Name()))
		logLine("清理旧版本: %s", entry.Name())
	}
}

func (s *Server) sendNotify(project Project, env EnvConfig, record Record, status string, ended time.Time) {
	text := buildNotificationText(project, env, record, status, ended)
	if project.Notify.NotificationID != "" {
		if item, ok := findNotification(s.store, project.Notify.NotificationID); ok {
			_ = postNotification(item, text)
			return
		}
	}
	legacy := []Notification{
		{Type: "wecom", HookURL: project.Notify.WeComHook},
		{Type: "feishu", HookURL: project.Notify.FeishuHook},
	}
	for _, item := range legacy {
		if strings.TrimSpace(item.HookURL) != "" {
			_ = postNotification(item, text)
		}
	}
}

func buildNotificationText(project Project, env EnvConfig, record Record, status string, ended time.Time) string {
	started := record.StartedAt
	if started.IsZero() {
		started = ended
	}
	projectCode := valueOr(project.Code, "-")
	ref := valueOr(record.Ref, "-")
	worker := valueOr(record.WorkerName, "-")
	if record.Mode == "redeploy" && record.WorkerName == "" {
		worker = "重新部署（无需编译）"
	}
	initiator := record.InitiatorName
	if initiator == "" {
		initiator = record.InitiatorCode
	} else if record.InitiatorCode != "" {
		initiator = fmt.Sprintf("%s（%s）", initiator, record.InitiatorCode)
	}
	initiator = valueOr(initiator, "-")
	mode := map[string]string{"build": "编译并发布", "redeploy": "重新部署"}[record.Mode]
	if mode == "" {
		mode = valueOr(record.Mode, "-")
	}
	titleIcon := map[string]string{"success": "✅", "failed": "❌", "stopped": "⏹"}[status]
	if titleIcon == "" {
		titleIcon = "ℹ️"
	}
	return strings.Join([]string{
		fmt.Sprintf("%s 轻发布通知：%s", titleIcon, notifyStatusLabel(status)),
		"",
		"【项目信息】",
		fmt.Sprintf("项目：%s（%s）", valueOr(project.Name, record.ProjectName), projectCode),
		fmt.Sprintf("环境：%s", valueOr(env.Name, record.Env)),
		fmt.Sprintf("版本：%s", valueOr(record.Version, "-")),
		fmt.Sprintf("模式：%s", mode),
		"",
		"【代码与执行】",
		fmt.Sprintf("分支/Tag：%s", ref),
		fmt.Sprintf("编译器：%s", worker),
		fmt.Sprintf("发起人：%s", initiator),
		"",
		"【时间】",
		fmt.Sprintf("开始时间：%s", formatNotifyTime(started)),
		fmt.Sprintf("完成时间：%s", formatNotifyTime(ended)),
		fmt.Sprintf("耗费时间：%s", formatNotifyDuration(ended.Sub(started))),
		fmt.Sprintf("时间戳：%s", ended.Format(time.RFC3339)),
	}, "\n")
}

func notifyStatusLabel(status string) string {
	switch status {
	case "success":
		return "发布成功"
	case "failed":
		return "发布失败"
	case "stopped":
		return "发布已终止"
	case "running":
		return "发布中"
	default:
		return valueOr(status, "未知状态")
	}
}

func formatNotifyTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04:05")
}

func formatNotifyDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int(d.Round(time.Second).Seconds())
	hours := seconds / 3600
	minutes := seconds % 3600 / 60
	secs := seconds % 60
	if hours > 0 {
		return fmt.Sprintf("%d小时%d分%d秒", hours, minutes, secs)
	}
	if minutes > 0 {
		return fmt.Sprintf("%d分%d秒", minutes, secs)
	}
	return fmt.Sprintf("%d秒", secs)
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func hasNotification(project Project) bool {
	return strings.TrimSpace(project.Notify.NotificationID) != "" ||
		strings.TrimSpace(project.Notify.WeComHook) != "" ||
		strings.TrimSpace(project.Notify.FeishuHook) != ""
}

func (s *Server) testNotification(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	item, ok := findNotification(s.store, id)
	if !ok {
		fail(w, 404, "通知不存在")
		return
	}
	if err := postNotification(item, "轻发布通知测试：连接成功"); err != nil {
		fail(w, 400, err.Error())
		return
	}
	respond(w, map[string]string{"message": "通知发送成功"})
}

func postNotification(item Notification, text string) error {
	var payload any
	if item.Type == "wecom" {
		payload = map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}
	} else {
		payload = map[string]any{"msg_type": "text", "content": map[string]string{"text": text}}
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, item.HookURL, strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 8 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("通知请求失败: HTTP %d", res.StatusCode)
	}
	return nil
}

func (s *Server) testNode(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	node, ok := findNode(s.store, id)
	if !ok {
		fail(w, 404, "节点不存在")
		return
	}
	setStatus := func(status, msg string) {
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		if idx := indexNode(s.store.Nodes, id); idx >= 0 {
			s.store.Nodes[idx].Status = status
			s.store.Nodes[idx].LastError = msg
			s.store.Nodes[idx].UpdatedAt = time.Now()
			_ = s.store.saveLocked()
		}
	}
	if node.Type == "ssh" {
		secret, _ := findSecret(s.store, node.SecretID)
		ctx, cancel := context.WithTimeout(context.Background(), sshTimeout)
		defer cancel()
		err := runSSHCommand(ctx, node, secret, "echo ok", func(string, ...any) {})
		if err != nil {
			setStatus("invalid", err.Error())
			fail(w, 400, err.Error())
			return
		}
		setStatus("valid", "")
		respond(w, map[string]string{"message": "SSH 连接成功"})
		return
	}
	dir := node.BaseDir
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		setStatus("invalid", err.Error())
		fail(w, 400, err.Error())
		return
	}
	setStatus("valid", "")
	respond(w, map[string]string{"message": "本地目录可用"})
}

func startPTY(cmd *exec.Cmd) (*os.File, error) {
	return pty.Start(cmd)
}

func setPTYSize(f *os.File, rows, cols int) {
	pty.Setsize(f, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) nodeConsole(w http.ResponseWriter, r *http.Request, id string) {
	node, ok := findNode(s.store, id)
	if !ok {
		fail(w, 404, "节点不存在")
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer ws.Close()

	if node.Type == "ssh" {
		s.sshConsole(ws, node)
	} else {
		s.localConsole(ws, node)
	}
}

func (s *Server) sshConsole(ws *websocket.Conn, node Node) {
	secret, _ := findSecret(s.store, node.SecretID)
	config, err := sshClientConfig(node, secret)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\x1b[31m错误: "+err.Error()+"\x1b[0m\r\n"))
		return
	}
	port := node.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(node.Host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\x1b[31m连接失败: "+err.Error()+"\x1b[0m\r\n"))
		return
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\x1b[31mSSH握手失败: "+err.Error()+"\x1b[0m\r\n"))
		return
	}
	client := ssh.NewClient(c, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\x1b[31m创建会话失败: "+err.Error()+"\x1b[0m\r\n"))
		return
	}
	defer session.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", 40, 120, modes); err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\x1b[31mPTY请求失败: "+err.Error()+"\x1b[0m\r\n"))
		return
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\x1b[31m管道创建失败\x1b[0m\r\n"))
		return
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\x1b[31m管道创建失败\x1b[0m\r\n"))
		return
	}

	if err := session.Shell(); err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\x1b[31m启动Shell失败: "+err.Error()+"\x1b[0m\r\n"))
		return
	}

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 32*1024)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				ws.WriteMessage(websocket.BinaryMessage, buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if len(msg) < 1 {
				continue
			}
			switch msg[0] {
			case 0: // input
				stdin.Write(msg[1:])
			case 1: // resize
				if len(msg) >= 5 {
					cols := int(msg[1])<<8 | int(msg[2])
					rows := int(msg[3])<<8 | int(msg[4])
					if cols > 0 && rows > 0 {
						session.WindowChange(rows, cols)
					}
				}
			}
		}
	}()

	<-done
}

func (s *Server) localConsole(ws *websocket.Conn, node Node) {
	dir := node.BaseDir
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell, "-i")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := startPTY(cmd)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\x1b[31m启动终端失败: "+err.Error()+"\x1b[0m\r\n"))
		return
	}
	defer ptmx.Close()

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				ws.WriteMessage(websocket.BinaryMessage, buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if len(msg) < 1 {
				continue
			}
			switch msg[0] {
			case 0:
				ptmx.Write(msg[1:])
			case 1:
				if len(msg) >= 5 {
					cols := int(msg[1])<<8 | int(msg[2])
					rows := int(msg[3])<<8 | int(msg[4])
					if cols > 0 && rows > 0 {
						setPTYSize(ptmx, rows, cols)
					}
				}
			}
		}
	}()

	<-done
}

func (s *Server) testGit(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	project, ok := s.projectFromRequest(w, r, id)
	if !ok {
		return
	}
	if project.Git.URL == "" {
		fail(w, 400, "未配置 git 地址")
		return
	}
	gitSecret, _ := findSecret(s.store, project.Git.SecretID)
	gitURL := gitURLWithCredential(project.Git.URL, gitSecret)
	_, err := gitLsRemote(context.Background(), gitURL)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	respond(w, map[string]string{"message": "Git 连接成功"})
}

func (s *Server) projectRefs(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	project, ok := s.projectFromRequest(w, r, id)
	if !ok {
		return
	}
	if project.Git.URL == "" {
		fail(w, 400, "未配置 git 地址")
		return
	}
	gitSecret, _ := findSecret(s.store, project.Git.SecretID)
	gitURL := gitURLWithCredential(project.Git.URL, gitSecret)
	out, err := gitLsRemote(context.Background(), gitURL)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	respond(w, parseGitRefs(out))
}

func (s *Server) projectFromRequest(w http.ResponseWriter, r *http.Request, id string) (Project, bool) {
	if id == "_draft" {
		var project Project
		if err := readJSON(r, &project); err != nil {
			fail(w, 400, err.Error())
			return Project{}, false
		}
		return project, true
	}
	s.store.mu.RLock()
	idx := indexProject(s.store.Projects, id)
	if idx < 0 {
		s.store.mu.RUnlock()
		fail(w, 404, "项目不存在")
		return Project{}, false
	}
	project := s.store.Projects[idx]
	s.store.mu.RUnlock()
	if r.Body != nil && r.ContentLength != 0 {
		var override Project
		if err := readJSON(r, &override); err == nil && override.Git.URL != "" {
			project.Git = override.Git
		}
	}
	return project, true
}

func (s *Server) testBuildNode(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.store.mu.RLock()
	idx := indexProject(s.store.Projects, id)
	if idx < 0 {
		s.store.mu.RUnlock()
		fail(w, 404, "项目不存在")
		return
	}
	project := s.store.Projects[idx]
	s.store.mu.RUnlock()
	dir := project.Build.WorkDir
	if dir == "" {
		dir = filepath.Join(".code-dep", "workspaces", project.Code)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		fail(w, 400, err.Error())
		return
	}
	respond(w, map[string]string{"message": "编译工作目录可用: " + dir})
}

func (s *Server) gitRefs(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	url := r.URL.Query().Get("url")
	if url == "" {
		fail(w, 400, "url 必填")
		return
	}
	out, err := gitLsRemote(context.Background(), url)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	respond(w, parseGitRefs(out))
}

type GitRefResult struct {
	Branches []string `json:"branches"`
	Tags     []string `json:"tags"`
	Refs     []string `json:"refs"`
}

func gitLsRemote(ctx context.Context, gitURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--heads", "--tags", gitURL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(redactURLInText(string(out))))
	}
	return string(out), nil
}

func parseGitRefs(out string) GitRefResult {
	result := GitRefResult{}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.HasSuffix(fields[1], "^{}") {
			continue
		}
		var ref string
		if strings.HasPrefix(fields[1], "refs/heads/") {
			ref = strings.TrimPrefix(fields[1], "refs/heads/")
			result.Branches = append(result.Branches, ref)
		} else if strings.HasPrefix(fields[1], "refs/tags/") {
			ref = strings.TrimPrefix(fields[1], "refs/tags/")
			result.Tags = append(result.Tags, ref)
		}
		if ref != "" && !seen[ref] {
			seen[ref] = true
			result.Refs = append(result.Refs, ref)
		}
	}
	sort.Strings(result.Branches)
	sort.Strings(result.Tags)
	sort.Strings(result.Refs)
	return result
}

func redactURLInText(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		for _, field := range strings.Fields(line) {
			if strings.Contains(field, "://") && strings.Contains(field, "@") {
				line = strings.ReplaceAll(line, field, redactURL(field))
			}
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/logs/")
	user, _ := s.currentUser(r)
	record, ok := s.recordByIDValue(id)
	if !ok {
		fail(w, 404, "发布记录不存在")
		return
	}
	if !hasProjectAccess(user, record.ProjectID, "view") {
		fail(w, http.StatusForbidden, "没有项目访问权限")
		return
	}
	tail := 1000
	if raw := r.URL.Query().Get("tail"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 && n <= 5000 {
			tail = n
		}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "stream not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	lines := s.recordLog(id)
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	for _, line := range lines {
		fmt.Fprintf(w, "data: %s\n\n", escapeSSE(line))
	}
	ch := s.jobs.subscribe(id)
	defer s.jobs.unsubscribe(id, ch)
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case line := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", escapeSSE(line))
			flusher.Flush()
			if strings.HasPrefix(line, "__DONE__:") {
				return
			}
		}
	}
}

// appendRecordLog - see storage.go

func (s *Server) recordLog(id string) []string {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	if idx := indexRecord(s.store.Records, id); idx >= 0 {
		r := s.store.Records[idx]
		log := s.store.loadRecordLog(r.ProjectID, id)
		if log != nil {
			return log
		}
		return append([]string(nil), r.Log...)
	}
	return nil
}

func (s *Server) recordByIDValue(id string) (Record, bool) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	if idx := indexRecord(s.store.Records, id); idx >= 0 {
		return s.store.Records[idx], true
	}
	return Record{}, false
}

func (h *JobHub) publish(id, line string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, ch := range h.subs[id] {
		select {
		case ch <- line:
		default:
		}
	}
}

func (h *JobHub) subscribe(id string) chan string {
	ch := make(chan string, 128)
	h.mu.Lock()
	h.subs[id] = append(h.subs[id], ch)
	h.mu.Unlock()
	return ch
}

func (h *JobHub) unsubscribe(id string, ch chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	subs := h.subs[id]
	for i, item := range subs {
		if item == ch {
			h.subs[id] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
}

func findNode(store *Store, id string) (Node, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, node := range store.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return Node{}, false
}

func findSecret(store *Store, id string) (Secret, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, secret := range store.Secrets {
		if secret.ID == id {
			return secret, true
		}
	}
	return Secret{}, false
}

func findNotification(store *Store, id string) (Notification, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, item := range store.Notifications {
		if item.ID == id {
			return item, true
		}
	}
	return Notification{}, false
}

func indexSecret(items []Secret, id string) int {
	for i, item := range items {
		if item.ID == id {
			return i
		}
	}
	return -1
}

func indexNode(items []Node, id string) int {
	for i, item := range items {
		if item.ID == id {
			return i
		}
	}
	return -1
}

func indexWorker(items []Worker, id string) int {
	for i, item := range items {
		if item.ID == id {
			return i
		}
	}
	return -1
}

func removeString(items []string, value string) []string {
	out := items[:0]
	for _, item := range items {
		if item != value {
			out = append(out, item)
		}
	}
	return out
}

func indexNotification(items []Notification, id string) int {
	for i, item := range items {
		if item.ID == id {
			return i
		}
	}
	return -1
}

func indexUser(items []User, id string) int {
	for i, item := range items {
		if item.ID == id {
			return i
		}
	}
	return -1
}

func adminCount(items []User) int {
	count := 0
	for _, item := range items {
		if item.Role == "admin" {
			count++
		}
	}
	return count
}

func indexProject(items []Project, id string) int {
	for i, item := range items {
		if item.ID == id {
			return i
		}
	}
	return -1
}

func indexRecord(items []Record, id string) int {
	for i, item := range items {
		if item.ID == id {
			return i
		}
	}
	return -1
}

func sshTarget(node Node) string {
	target := node.Host
	if node.User != "" {
		target = node.User + "@" + target
	}
	return target
}

func sshBaseArgs(node Node, secret Secret) []string {
	args := []string{}
	if node.Port != 0 && node.Port != 22 {
		args = append(args, "-p", strconv.Itoa(node.Port))
	}
	if keyPath := materializeSSHKey(secret); keyPath != "" {
		args = append(args, "-i", keyPath, "-o", "IdentitiesOnly=yes")
	}
	return args
}

func scpBaseArgs(node Node, secret Secret) []string {
	args := []string{}
	if node.Port != 0 && node.Port != 22 {
		args = append(args, "-P", strconv.Itoa(node.Port))
	}
	if keyPath := materializeSSHKey(secret); keyPath != "" {
		args = append(args, "-i", keyPath, "-o", "IdentitiesOnly=yes")
	}
	return args
}

func materializeSSHKey(secret Secret) string {
	key := strings.TrimSpace(secret.PrivateKey)
	if key == "" {
		return ""
	}
	if strings.HasPrefix(key, "/") || strings.HasPrefix(key, "~") {
		if strings.HasPrefix(key, "~") {
			home, _ := os.UserHomeDir()
			key = filepath.Join(home, strings.TrimPrefix(key, "~/"))
		}
		return key
	}
	if !strings.Contains(key, "BEGIN") {
		return ""
	}
	dir := filepath.Join(".code-dep", "keys")
	_ = os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, secret.ID+".pem")
	_ = os.WriteFile(path, []byte(key+"\n"), 0600)
	return path
}

func versionName(code string) string {
	return code + "-" + time.Now().Format("20060102150405")
}

func (s *Server) randomID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func escapeSSE(s string) string {
	return strings.ReplaceAll(s, "\n", "\\n")
}

func createArchive(src, dst string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name, _ = filepath.Rel(src, path)
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	})
}
