package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/bcrypt"
)

type Server struct {
	db          *sql.DB
	runnerToken string
	key         [32]byte
	secure      bool
	authMu      sync.Mutex
	authWindows map[string]authWindow
	authSlots   chan struct{}
}
type authWindow struct { Start time.Time; Count int }
type Node struct {
	ID           string             `json:"id"`
	Type         string             `json:"type"`
	Position     map[string]float64 `json:"position"`
	Config       map[string]any     `json:"config"`
	CredentialID string             `json:"credential_id,omitempty"`
}
type Edge struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	SourcePort string `json:"source_port"`
}
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}
type Workflow struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Draft            Graph  `json:"draft"`
	PublishedVersion *int   `json:"published_version"`
	Active           bool   `json:"active"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}
type Run struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflow_id"`
	Version    int    `json:"version"`
	Status     string `json:"status"`
	Input      any    `json:"input"`
	Error      string `json:"error,omitempty"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}
type Step struct {
	NodeID    string `json:"node_id"`
	Status    string `json:"status"`
	Input     any    `json:"input"`
	Output    any    `json:"output"`
	Error     string `json:"error,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Attempt   int    `json:"attempt"`
	CreatedAt string `json:"created_at"`
}

var nodeTypes = map[string]bool{"manual_trigger": true, "webhook_trigger": true, "schedule_trigger": true, "email_trigger": true, "set_fields": true, "http_request": true, "condition": true, "send_email": true}
var triggerTypes = map[string]bool{"manual_trigger": true, "webhook_trigger": true, "schedule_trigger": true, "email_trigger": true}

func main() {
	dbPath := env("DATABASE_PATH", "/data/n9n.db")
	if err := os.MkdirAll(dir(dbPath), 0700); err != nil {
		log.Fatal(err)
	}
	key := os.Getenv("ENCRYPTION_KEY")
	token := os.Getenv("RUNNER_TOKEN")
	if key == "" || token == "" {
		log.Fatal("ENCRYPTION_KEY and RUNNER_TOKEN must be set")
	}
	s, err := NewServer(dbPath, key, token)
	if err != nil {
		log.Fatal(err)
	}
	defer s.db.Close()
	server := &http.Server{Addr: env("ADDR", ":8080"), Handler: s, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go s.maintain(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil { log.Printf("shutdown: %v", err) }
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) { log.Fatal(err) }
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func dir(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "."
	}
	if i == 0 {
		return "/"
	}
	return p[:i]
}
func NewServer(path, key, token string) (*Server, error) {
	if len(key) != 64 || len(token) < 32 {
		return nil, errors.New("ENCRYPTION_KEY must be 64 hex characters and RUNNER_TOKEN at least 32 characters")
	}
	keyBytes, e := hex.DecodeString(key)
	if e != nil {
		return nil, e
	}
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Server{db: db, runnerToken: token, secure: os.Getenv("COOKIE_SECURE") == "true", authWindows: map[string]authWindow{}, authSlots: make(chan struct{}, 4)}
	copy(s.key[:], keyBytes)
	if err = migrate(db); err != nil { db.Close(); return nil, err }
	return s, nil
}

func migrate(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil { return err }
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil { return err }
	if version > 1 { return errors.New("database schema is newer than this server") }
	if version == 0 {
		for _, q := range schema { if _, err = tx.Exec(q); err != nil { return err } }
		if _, err = tx.Exec(`PRAGMA user_version = 1`); err != nil { return err }
	}
	return tx.Commit()
}

func (s *Server) maintain(ctx context.Context) {
	run := func() {
		cutoff := now()
		if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at<?`, cutoff); err != nil { log.Printf("session cleanup: %v", err) }
		if _, err := s.db.Exec(`DELETE FROM oauth_states WHERE expires_at<?`, cutoff); err != nil { log.Printf("OAuth state cleanup: %v", err) }
		days, err := strconv.Atoi(env("RUN_RETENTION_DAYS", "30"))
		if err != nil || days < 1 { days = 30 }
		before := time.Now().UTC().AddDate(0, 0, -days).Format(timeLayout)
		if _, err := s.db.Exec(`DELETE FROM runs WHERE status IN ('succeeded','failed','cancelled','uncertain') AND updated_at<?`, before); err != nil { log.Printf("run cleanup: %v", err) }
	}
	run()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for { select { case <-ctx.Done(): return; case <-ticker.C: run() } }
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,email TEXT UNIQUE NOT NULL,password_hash BLOB NOT NULL,created_at TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,expires_at TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS workflows(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,name TEXT NOT NULL,draft TEXT NOT NULL,published_version INTEGER,active INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,updated_at TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS versions(workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,version INTEGER NOT NULL,graph TEXT NOT NULL,PRIMARY KEY(workflow_id,version))`,
	`CREATE TABLE IF NOT EXISTS credentials(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,name TEXT NOT NULL,kind TEXT NOT NULL,ciphertext TEXT NOT NULL,created_at TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY,workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,version INTEGER NOT NULL,status TEXT NOT NULL,input TEXT NOT NULL,error TEXT NOT NULL DEFAULT '',lease_token TEXT,lease_until TEXT,runner_id TEXT,created_at TEXT NOT NULL,updated_at TEXT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS runs_queue ON runs(status,created_at)`,
	`CREATE TABLE IF NOT EXISTS steps(id INTEGER PRIMARY KEY AUTOINCREMENT,run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,node_id TEXT NOT NULL,status TEXT NOT NULL,input TEXT NOT NULL,output TEXT NOT NULL,error TEXT NOT NULL DEFAULT '',branch TEXT NOT NULL DEFAULT '',attempt INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS trigger_events(workflow_id TEXT NOT NULL,version INTEGER NOT NULL,event_id TEXT NOT NULL,run_id TEXT NOT NULL,PRIMARY KEY(workflow_id,version,event_id))`,
	`CREATE TABLE IF NOT EXISTS checkpoints(workflow_id TEXT NOT NULL,version INTEGER NOT NULL,value TEXT NOT NULL,PRIMARY KEY(workflow_id,version))`,
	`CREATE TABLE IF NOT EXISTS oauth_states(state_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,session_hash TEXT NOT NULL,expires_at TEXT NOT NULL)`,
}

func id() string { b := make([]byte, 16); _, _ = rand.Read(b); return hex.EncodeToString(b) }

const timeLayout = "2006-01-02T15:04:05.000000000Z"

func now() string           { return time.Now().UTC().Format(timeLayout) }
func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	write(w, status, map[string]any{"error": msg})
}
func decode(r *http.Request, v any) error {
	return decodeLimit(r, v, 1<<20)
}
func decodeLimit(r *http.Request, v any, bytes int64) error {
	r.Body = http.MaxBytesReader(nil, r.Body, bytes)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		if o := r.Header.Get("Origin"); o != "" {
			u, e := url.Parse(o)
			host := r.Host
			if h := r.Header.Get("X-Forwarded-Host"); h != "" {
				host = h
			}
			allow := os.Getenv("ALLOWED_ORIGIN")
			if e != nil || (!strings.EqualFold(u.Host, host) && o != allow) {
				fail(w, 403, "origin mismatch")
				return
			}
		}
	}
	if strings.HasPrefix(r.URL.Path, "/internal/") {
		s.internal(w, r, strings.Split(strings.TrimPrefix(r.URL.Path, "/internal/"), "/"))
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		fail(w, 404, "not found")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")[1:]
	if len(parts) == 0 {
		fail(w, 404, "not found")
		return
	}
	if len(parts) == 1 && parts[0] == "health" && r.Method == "GET" {
		write(w, 200, map[string]string{"status": "ok"})
		return
	}
	if len(parts) == 3 && parts[0] == "oauth" && parts[1] == "google" {
		s.oauth(w, r, parts[2])
		return
	}
	if len(parts) >= 2 && parts[0] == "auth" {
		s.auth(w, r, parts[1])
		return
	}
	if len(parts) == 2 && parts[0] == "hooks" && r.Method == "POST" {
		s.hook(w, r, parts[1])
		return
	}
	uid := s.user(r)
	if uid == "" {
		fail(w, 401, "authentication required")
		return
	}
	switch parts[0] {
	case "nodes":
		if len(parts) == 1 && r.Method == "GET" {
			write(w, 200, map[string]any{"nodes": catalog})
			return
		}
	case "workflows":
		s.workflows(w, r, uid, parts[1:])
		return
	case "runs":
		s.runs(w, r, uid, parts[1:])
		return
	case "credentials":
		s.credentials(w, r, uid, parts[1:])
		return
	}
	fail(w, 404, "not found")
}
func (s *Server) user(r *http.Request) string {
	c, e := r.Cookie("n9n_session")
	if e != nil {
		return ""
	}
	h := sha256.Sum256([]byte(c.Value))
	var uid string
	_ = s.db.QueryRow(`SELECT user_id FROM sessions WHERE token_hash=? AND expires_at>?`, hex.EncodeToString(h[:]), now()).Scan(&uid)
	return uid
}
func (s *Server) session(w http.ResponseWriter, uid string) error {
	t := id() + id()
	h := sha256.Sum256([]byte(t))
	expiry := time.Now().UTC().Add(30 * 24 * time.Hour)
	_, err := s.db.Exec(`INSERT INTO sessions VALUES(?,?,?)`, hex.EncodeToString(h[:]), uid, expiry.Format(timeLayout))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "n9n_session", Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secure, Expires: expiry})
	return nil
}
func (s *Server) auth(w http.ResponseWriter, r *http.Request, action string) {
	if action == "me" && r.Method == "GET" {
		uid := s.user(r)
		if uid == "" {
			fail(w, 401, "authentication required")
			return
		}
		var email string
		_ = s.db.QueryRow(`SELECT email FROM users WHERE id=?`, uid).Scan(&email)
		write(w, 200, map[string]any{"user": map[string]string{"id": uid, "email": email}})
		return
	}
	if action == "logout" && r.Method == "POST" {
		if c, e := r.Cookie("n9n_session"); e == nil {
			h := sha256.Sum256([]byte(c.Value))
			_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, hex.EncodeToString(h[:]))
		}
		http.SetCookie(w, &http.Cookie{Name: "n9n_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secure})
		write(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Method != "POST" || (action != "register" && action != "login") {
		fail(w, 404, "not found")
		return
	}
	var a struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if e := decode(r, &a); e != nil {
		fail(w, 400, e.Error())
		return
	}
	a.Email = strings.ToLower(strings.TrimSpace(a.Email))
	if len(a.Email) > 254 || !strings.Contains(a.Email, "@") || len(a.Password) < 8 || len(a.Password) > 72 {
		fail(w, 400, "invalid email or password")
		return
	}
	var uid string
	if action == "register" {
		hash, e := bcrypt.GenerateFromPassword([]byte(a.Password), bcrypt.DefaultCost)
		if e != nil {
			fail(w, 500, "password error")
			return
		}
		uid = id()
		_, e = s.db.Exec(`INSERT INTO users VALUES(?,?,?,?)`, uid, a.Email, hash, now())
		if e != nil {
			fail(w, 409, "email already registered")
			return
		}
	} else {
		var hash []byte
		e := s.db.QueryRow(`SELECT id,password_hash FROM users WHERE email=?`, a.Email).Scan(&uid, &hash)
		if e != nil || bcrypt.CompareHashAndPassword(hash, []byte(a.Password)) != nil {
			fail(w, 401, "invalid credentials")
			return
		}
	}
	if e := s.session(w, uid); e != nil {
		fail(w, 500, "session error")
		return
	}
	write(w, 200, map[string]any{"user": map[string]string{"id": uid, "email": a.Email}})
}
