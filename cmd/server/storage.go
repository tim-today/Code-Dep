package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ── Directory layout ──────────────────────────────────────────────────────────
//
//	data/
//	├── .key                  (encryption key, 32 bytes, chmod 0600)
//	├── system/
//	│   ├── secrets.json
//	│   ├── nodes.json
//	│   ├── workers.json
//	│   ├── notifications.json
//	│   ├── users.json
//	│   └── meta.json          (NextID)
//	└── projects/
//	    └── {projectId}/
//	        ├── config.json
//	        └── records/
//	            ├── index.json   ([]Record without Log)
//	            └── {recordId}.log
//

// ── Encryption ────────────────────────────────────────────────────────────────

func loadOrCreateKey(dataDir string) ([]byte, error) {
	keyPath := filepath.Join(dataDir, ".key")
	if b, err := os.ReadFile(keyPath); err == nil && len(b) >= 32 {
		return b[:32], nil
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		return nil, err
	}
	return key, nil
}

func encryptField(key []byte, plaintext string) string {
	if plaintext == "" {
		return ""
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return plaintext
	}
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	io.ReadFull(rand.Reader, nonce)
	return "ENC:" + base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), nil))
}

func decryptField(key []byte, ciphertext string) string {
	if ciphertext == "" || !strings.HasPrefix(ciphertext, "ENC:") {
		return ciphertext
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext[4:])
	if err != nil {
		return ciphertext
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return ciphertext
	}
	gcm, _ := cipher.NewGCM(block)
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return ciphertext
	}
	plain, err := gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return ciphertext
	}
	return string(plain)
}

func encryptSecret(key []byte, s *Secret) {
	s.Password = encryptField(key, s.Password)
	s.Token = encryptField(key, s.Token)
	s.PrivateKey = encryptField(key, s.PrivateKey)
}

func decryptSecret(key []byte, s *Secret) {
	s.Password = decryptField(key, s.Password)
	s.Token = decryptField(key, s.Token)
	s.PrivateKey = decryptField(key, s.PrivateKey)
}

func encryptUser(key []byte, u *User) {
	// Only encrypt non-hash passwords (hashes start with $2)
	if u.Password != "" && !isPasswordHash(u.Password) {
		u.Password = encryptField(key, u.Password)
	}
}

func decryptUser(key []byte, u *User) {
	if u.Password != "" && strings.HasPrefix(u.Password, "ENC:") {
		u.Password = decryptField(key, u.Password)
	}
}

func encryptNotification(key []byte, n *Notification) {
	n.HookURL = encryptField(key, n.HookURL)
}

func decryptNotification(key []byte, n *Notification) {
	n.HookURL = decryptField(key, n.HookURL)
}

// ── File helpers ──────────────────────────────────────────────────────────────

func writeJSON(path string, v interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSONFile(path string, v interface{}) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(b, v)
}

// ── Per-type save/load ────────────────────────────────────────────────────────

func (s *Store) saveSecrets() error {
	secrets := make([]Secret, len(s.Secrets))
	copy(secrets, s.Secrets)
	for i := range secrets {
		encryptSecret(s.encKey, &secrets[i])
	}
	return writeJSON(filepath.Join(s.dataDir, "system", "secrets.json"), secrets)
}

func (s *Store) loadSecrets() error {
	path := filepath.Join(s.dataDir, "system", "secrets.json")
	if err := readJSONFile(path, &s.Secrets); err != nil {
		return err
	}
	if s.Secrets == nil {
		s.Secrets = []Secret{}
	}
	for i := range s.Secrets {
		decryptSecret(s.encKey, &s.Secrets[i])
	}
	return nil
}

func (s *Store) saveNodes() error {
	return writeJSON(filepath.Join(s.dataDir, "system", "nodes.json"), s.Nodes)
}

func (s *Store) loadNodes() error {
	if err := readJSONFile(filepath.Join(s.dataDir, "system", "nodes.json"), &s.Nodes); err != nil {
		return err
	}
	if s.Nodes == nil {
		s.Nodes = []Node{}
	}
	return nil
}

func (s *Store) saveWorkers() error {
	return writeJSON(filepath.Join(s.dataDir, "system", "workers.json"), s.Workers)
}

func (s *Store) loadWorkers() error {
	if err := readJSONFile(filepath.Join(s.dataDir, "system", "workers.json"), &s.Workers); err != nil {
		return err
	}
	if s.Workers == nil {
		s.Workers = []Worker{}
	}
	return nil
}

func (s *Store) saveNotifications() error {
	notifs := make([]Notification, len(s.Notifications))
	copy(notifs, s.Notifications)
	for i := range notifs {
		encryptNotification(s.encKey, &notifs[i])
	}
	return writeJSON(filepath.Join(s.dataDir, "system", "notifications.json"), notifs)
}

func (s *Store) loadNotifications() error {
	path := filepath.Join(s.dataDir, "system", "notifications.json")
	if err := readJSONFile(path, &s.Notifications); err != nil {
		return err
	}
	if s.Notifications == nil {
		s.Notifications = []Notification{}
	}
	for i := range s.Notifications {
		decryptNotification(s.encKey, &s.Notifications[i])
	}
	return nil
}

func (s *Store) saveUsers() error {
	users := make([]User, len(s.Users))
	copy(users, s.Users)
	for i := range users {
		encryptUser(s.encKey, &users[i])
	}
	return writeJSON(filepath.Join(s.dataDir, "system", "users.json"), users)
}

func (s *Store) loadUsers() error {
	path := filepath.Join(s.dataDir, "system", "users.json")
	if err := readJSONFile(path, &s.Users); err != nil {
		return err
	}
	if s.Users == nil {
		s.Users = []User{}
	}
	for i := range s.Users {
		decryptUser(s.encKey, &s.Users[i])
	}
	return nil
}

func (s *Store) saveMeta() error {
	return writeJSON(filepath.Join(s.dataDir, "system", "meta.json"), map[string]int64{"nextId": s.NextID})
}

func (s *Store) loadMeta() error {
	var m map[string]int64
	if err := readJSONFile(filepath.Join(s.dataDir, "system", "meta.json"), &m); err != nil {
		return err
	}
	if v, ok := m["nextId"]; ok && v > 0 {
		s.NextID = v
	}
	return nil
}

// ── Project storage ───────────────────────────────────────────────────────────

func projectDir(dataDir, projectID string) string {
	return filepath.Join(dataDir, "projects", projectID)
}

func (s *Store) saveProject(p Project) error {
	dir := projectDir(s.dataDir, p.ID)
	return writeJSON(filepath.Join(dir, "config.json"), p)
}

func (s *Store) loadProjects() error {
	projectsDir := filepath.Join(s.dataDir, "projects")
	s.Projects = []Project{}
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var p Project
		cfgPath := filepath.Join(projectsDir, e.Name(), "config.json")
		if err := readJSONFile(cfgPath, &p); err != nil {
			continue
		}
		if p.ID != "" {
			s.Projects = append(s.Projects, p)
		}
	}
	return nil
}

func (s *Store) removeProject(id string) error {
	return os.RemoveAll(projectDir(s.dataDir, id))
}

// ── Record storage ────────────────────────────────────────────────────────────

type recordMeta struct {
	ID          string     `json:"id"`
	ProjectID   string     `json:"projectId"`
	ProjectName string     `json:"projectName"`
	Env         string     `json:"env"`
	Ref         string     `json:"ref"`
	Version     string     `json:"version"`
	Status      string     `json:"status"`
	Mode        string     `json:"mode"`
	WorkerID    string     `json:"workerId"`
	WorkerName  string     `json:"workerName"`
	LogCount    int        `json:"logCount"`
	StartedAt   time.Time  `json:"startedAt"`
	EndedAt     *time.Time `json:"endedAt"`
}

func recordsDir(dataDir, projectID string) string {
	return filepath.Join(projectDir(dataDir, projectID), "records")
}

func (s *Store) saveRecordIndex(projectID string, records []Record) error {
	dir := recordsDir(s.dataDir, projectID)
	idx := make([]recordMeta, len(records))
	for i, r := range records {
		idx[i] = recordMeta{
			ID: r.ID, ProjectID: r.ProjectID, ProjectName: r.ProjectName,
			Env: r.Env, Ref: r.Ref, Version: r.Version, Status: r.Status,
			Mode: r.Mode, WorkerID: r.WorkerID, WorkerName: r.WorkerName, LogCount: len(r.Log), StartedAt: r.StartedAt, EndedAt: r.EndedAt,
		}
	}
	return writeJSON(filepath.Join(dir, "index.json"), idx)
}

func (s *Store) loadAllRecords() error {
	s.Records = []Record{}
	projectsDir := filepath.Join(s.dataDir, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		projectID := e.Name()
		idxPath := filepath.Join(projectsDir, projectID, "records", "index.json")
		var metas []recordMeta
		if err := readJSONFile(idxPath, &metas); err != nil || len(metas) == 0 {
			continue
		}
		for _, m := range metas {
			r := Record{
				ID: m.ID, ProjectID: m.ProjectID, ProjectName: m.ProjectName,
				Env: m.Env, Ref: m.Ref, Version: m.Version, Status: m.Status,
				Mode: m.Mode, WorkerID: m.WorkerID, WorkerName: m.WorkerName, StartedAt: m.StartedAt, EndedAt: m.EndedAt,
			}
			s.Records = append(s.Records, r)
		}
	}
	return nil
}

func (s *Store) appendRecordLog(id, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := indexRecord(s.Records, id)
	if idx < 0 {
		return
	}
	r := &s.Records[idx]
	r.Log = append(r.Log, line)
	logPath := filepath.Join(recordsDir(s.dataDir, r.ProjectID), id+".log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err == nil {
		fmt.Fprintln(f, line)
		f.Close()
	}
}

func (s *Store) loadRecordLog(projectID, recordID string) []string {
	logPath := filepath.Join(recordsDir(s.dataDir, projectID), recordID+".log")
	b, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func (s *Store) saveRecordMeta(projectID string) {
	records := s.recordsByProject(projectID)
	_ = s.saveRecordIndex(projectID, records)
}

func (s *Store) recordsByProject(projectID string) []Record {
	var out []Record
	for _, r := range s.Records {
		if r.ProjectID == projectID {
			out = append(out, r)
		}
	}
	return out
}

func (s *Store) removeRecordFile(projectID, recordID string) {
	os.Remove(filepath.Join(recordsDir(s.dataDir, projectID), recordID+".log"))
}

// ── Unified save (replaces old saveLocked) ────────────────────────────────────

func (s *Store) saveAll() error {
	if err := s.saveMeta(); err != nil {
		return err
	}
	if err := s.saveSecrets(); err != nil {
		return err
	}
	if err := s.saveNodes(); err != nil {
		return err
	}
	if err := s.saveWorkers(); err != nil {
		return err
	}
	if err := s.saveNotifications(); err != nil {
		return err
	}
	if err := s.saveUsers(); err != nil {
		return err
	}
	for _, p := range s.Projects {
		if err := s.saveProject(p); err != nil {
			return err
		}
		s.saveRecordMeta(p.ID)
	}
	return nil
}

// ── Migration from old store.json ─────────────────────────────────────────────

func migrateFromStoreJSON(store *Store, oldPath string) error {
	b, err := os.ReadFile(oldPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var old struct {
		NextID        int64          `json:"nextId"`
		Secrets       []Secret       `json:"secrets"`
		Nodes         []Node         `json:"nodes"`
		Workers       []Worker       `json:"workers"`
		Notifications []Notification `json:"notifications"`
		Users         []User         `json:"users"`
		Projects      []Project      `json:"projects"`
		Records       []Record       `json:"records"`
	}
	if err := json.Unmarshal(b, &old); err != nil {
		return err
	}
	store.NextID = old.NextID
	if store.NextID == 0 {
		store.NextID = 1
	}
	store.Secrets = old.Secrets
	store.Nodes = old.Nodes
	store.Workers = old.Workers
	store.Notifications = old.Notifications
	store.Users = old.Users
	store.Projects = old.Projects
	store.Records = old.Records
	log.Printf("migration loaded: secrets=%d nodes=%d projects=%d records=%d users=%d",
		len(store.Secrets), len(store.Nodes), len(store.Projects), len(store.Records), len(store.Users))
	if store.Secrets == nil {
		store.Secrets = []Secret{}
	}
	if store.Nodes == nil {
		store.Nodes = []Node{}
	}
	if store.Workers == nil {
		store.Workers = []Worker{}
	}
	if store.Notifications == nil {
		store.Notifications = []Notification{}
	}
	if store.Users == nil {
		store.Users = []User{}
	}
	if store.Projects == nil {
		store.Projects = []Project{}
	}
	if store.Records == nil {
		store.Records = []Record{}
	}
	// Split records by project
	for _, p := range store.Projects {
		recs := store.recordsByProject(p.ID)
		if len(recs) > 0 {
			// Write log files
			for _, r := range recs {
				if len(r.Log) > 0 {
					logPath := filepath.Join(recordsDir(store.dataDir, p.ID), r.ID+".log")
					os.MkdirAll(filepath.Dir(logPath), 0755)
					os.WriteFile(logPath, []byte(strings.Join(r.Log, "\n")+"\n"), 0600)
				}
			}
		}
	}
	// Save everything in new format
	if err := store.saveAll(); err != nil {
		return err
	}
	// Rename old file
	backup := oldPath + ".bak." + time.Now().Format("20060102150405")
	os.Rename(oldPath, backup)
	log.Printf("已迁移数据到新目录结构，旧文件备份为 %s", backup)
	return nil
}
