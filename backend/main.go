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
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
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
type authWindow struct {
	Start time.Time
	Count int
}
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
	Test       bool   `json:"test,omitempty"`
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
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL must be set")
	}
	key := os.Getenv("ENCRYPTION_KEY")
	token := os.Getenv("RUNNER_TOKEN")
	if key == "" || token == "" {
		log.Fatal("ENCRYPTION_KEY and RUNNER_TOKEN must be set")
	}
	var s *Server
	var err error
	deadline := time.Now().Add(60 * time.Second)
	for {
		s, err = NewServer(dbURL, key, token)
		if err == nil || time.Now().After(deadline) {
			break
		}
		log.Printf("database not ready: %v", err)
		time.Sleep(time.Second)
	}
	if err != nil {
		log.Fatal(err)
	}
	defer s.db.Close()
	server := &http.Server{Addr: env("ADDR", ":8080"), Handler: s, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	maintenanceDone := make(chan struct{})
	go func() { defer close(maintenanceDone); s.maintain(ctx) }()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-shutdownDone
	<-maintenanceDone
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func NewServer(databaseURL, key, token string) (*Server, error) {
	if len(key) != 64 || len(token) < 32 {
		return nil, errors.New("ENCRYPTION_KEY must be 64 hex characters and RUNNER_TOKEN at least 32 characters")
	}
	keyBytes, e := hex.DecodeString(key)
	if e != nil {
		return nil, e
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(time.Hour)
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = db.PingContext(pingCtx)
	pingCancel()
	if err != nil {
		db.Close()
		return nil, err
	}
	s := &Server{db: db, runnerToken: token, secure: os.Getenv("COOKIE_SECURE") == "true", authWindows: map[string]authWindow{}, authSlots: make(chan struct{}, 4)}
	copy(s.key[:], keyBytes)
	if err = migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func migrate(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(917442621)`); err != nil {
		return err
	}
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	var version int
	if err = tx.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version > 4 {
		return errors.New("database schema is newer than this server")
	}
	if version == 0 {
		for _, q := range schema {
			if _, err = tx.Exec(q); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(1,$1)`, now()); err != nil {
			return err
		}
		version = 1
	}
	if version < 2 {
		for _, q := range migration2 {
			if _, err = tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(2,$1)`, now()); err != nil {
			return err
		}
		version = 2
	}
	if version < 3 {
		for _, q := range migration3 {
			if _, err = tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(3,$1)`, now()); err != nil {
			return err
		}
		version = 3
	}
	if version < 4 {
		for _, q := range migration4 {
			if _, err = tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(4,$1)`, now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Server) maintain(ctx context.Context) {
	s.cleanup()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanup()
		}
	}
}
func (s *Server) cleanup() {
	cutoff := now()
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at<$1`, cutoff); err != nil {
		log.Printf("session cleanup: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM oauth_states WHERE expires_at<$1`, cutoff); err != nil {
		log.Printf("OAuth state cleanup: %v", err)
	}
	days, err := strconv.Atoi(env("RUN_RETENTION_DAYS", "30"))
	if err != nil || days < 1 {
		days = 30
	}
	before := time.Now().UTC().AddDate(0, 0, -days).Format(timeLayout)
	if _, err := s.db.Exec(`DELETE FROM runs WHERE status IN ('succeeded','failed','cancelled','uncertain') AND updated_at<$1`, before); err != nil {
		log.Printf("run cleanup: %v", err)
	}
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,email TEXT UNIQUE NOT NULL,password_hash BYTEA NOT NULL,created_at TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,expires_at TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS workflows(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,name TEXT NOT NULL,draft JSONB NOT NULL,published_version INTEGER,active BOOLEAN NOT NULL DEFAULT FALSE,created_at TEXT NOT NULL,updated_at TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS versions(workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,version INTEGER NOT NULL,graph JSONB NOT NULL,PRIMARY KEY(workflow_id,version))`,
	`CREATE TABLE IF NOT EXISTS credentials(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,name TEXT NOT NULL,kind TEXT NOT NULL,ciphertext TEXT NOT NULL,created_at TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY,workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,version INTEGER NOT NULL,status TEXT NOT NULL,input JSONB NOT NULL,error TEXT NOT NULL DEFAULT '',lease_token TEXT,lease_until TEXT,runner_id TEXT,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,FOREIGN KEY(workflow_id,version) REFERENCES versions(workflow_id,version))`,
	`CREATE INDEX IF NOT EXISTS runs_queue ON runs(status,created_at)`,
	`CREATE TABLE IF NOT EXISTS steps(id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,node_id TEXT NOT NULL,status TEXT NOT NULL,input JSONB NOT NULL,output JSONB NOT NULL,error TEXT NOT NULL DEFAULT '',branch TEXT NOT NULL DEFAULT '',attempt INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS trigger_events(workflow_id TEXT NOT NULL,version INTEGER NOT NULL,event_id TEXT NOT NULL,run_id TEXT NOT NULL,PRIMARY KEY(workflow_id,version,event_id),FOREIGN KEY(workflow_id,version) REFERENCES versions(workflow_id,version) ON DELETE CASCADE)`,
	`CREATE TABLE IF NOT EXISTS checkpoints(workflow_id TEXT NOT NULL,version INTEGER NOT NULL,value JSONB NOT NULL,PRIMARY KEY(workflow_id,version),FOREIGN KEY(workflow_id,version) REFERENCES versions(workflow_id,version) ON DELETE CASCADE)`,
	`CREATE TABLE IF NOT EXISTS oauth_states(state_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,session_hash TEXT NOT NULL,expires_at TEXT NOT NULL)`,
}

var migration2 = []string{
	`ALTER TABLE runs DROP CONSTRAINT IF EXISTS runs_workflow_id_version_fkey`,
	`ALTER TABLE runs ADD COLUMN IF NOT EXISTS graph_snapshot JSONB`,
	`ALTER TABLE runs ADD COLUMN IF NOT EXISTS is_test BOOLEAN NOT NULL DEFAULT FALSE`,
}

var migration3 = []string{
	`ALTER TABLE runs ADD CONSTRAINT runs_snapshot_mode_check CHECK ((is_test AND graph_snapshot IS NOT NULL AND version >= 0) OR (NOT is_test AND graph_snapshot IS NULL AND version > 0))`,
}

var migration4 = []string{
	`CREATE OR REPLACE FUNCTION enforce_run_snapshot_version() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.is_test THEN IF NEW.graph_snapshot IS NULL OR NEW.version < 0 THEN RAISE EXCEPTION 'draft test run requires a nonnegative version and graph snapshot'; END IF; ELSE IF NEW.graph_snapshot IS NOT NULL OR NOT EXISTS (SELECT 1 FROM versions WHERE workflow_id=NEW.workflow_id AND version=NEW.version) THEN RAISE EXCEPTION 'published run must reference an existing graph version'; END IF; END IF; RETURN NEW; END; $$`,
	`CREATE TRIGGER runs_snapshot_version_integrity BEFORE INSERT OR UPDATE OF workflow_id,version,is_test,graph_snapshot ON runs FOR EACH ROW EXECUTE FUNCTION enforce_run_snapshot_version()`,
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
func withinJSONDepth(value any) bool {
	var check func(any, int) bool
	check = func(item any, depth int) bool {
		if depth > 32 {
			return false
		}
		switch node := item.(type) {
		case map[string]any:
			for _, child := range node {
				if !check(child, depth+1) {
					return false
				}
			}
		case []any:
			for _, child := range node {
				if !check(child, depth+1) {
					return false
				}
			}
		}
		return true
	}
	return check(value, 1)
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
		if len(parts) == 2 {
			s.auth(w, r, parts[1])
		} else if len(parts) == 3 && parts[1] == "mobile" {
			s.auth(w, r, "mobile/"+parts[2])
		} else {
			fail(w, 404, "not found")
		}
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
			write(w, 200, map[string]any{"version": 1, "nodes": catalog})
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
	token, _ := sessionToken(r)
	if token == "" {
		return ""
	}
	h := sha256.Sum256([]byte(token))
	var uid string
	_ = s.db.QueryRow(`SELECT user_id FROM sessions WHERE token_hash=$1 AND expires_at>$2`, hex.EncodeToString(h[:]), now()).Scan(&uid)
	return uid
}

func sessionToken(r *http.Request) (string, bool) {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			return "", true
		}
		token := strings.TrimSpace(auth[7:])
		if token == "" || strings.ContainsAny(token, " \t\r\n") {
			return "", true
		}
		return token, true
	}
	c, e := r.Cookie("n9n_session")
	if e != nil {
		return "", false
	}
	return c.Value, false
}

func (s *Server) newSession(uid string) (string, time.Time, error) {
	t := id() + id()
	h := sha256.Sum256([]byte(t))
	expiry := time.Now().UTC().Add(30 * 24 * time.Hour)
	_, err := s.db.Exec(`INSERT INTO sessions VALUES($1,$2,$3)`, hex.EncodeToString(h[:]), uid, expiry.Format(timeLayout))
	if err != nil {
		return "", time.Time{}, err
	}
	return t, expiry, nil
}
func (s *Server) session(w http.ResponseWriter, uid string) error {
	t, expiry, err := s.newSession(uid)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "n9n_session", Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secure, Expires: expiry})
	return nil
}
func (s *Server) auth(w http.ResponseWriter, r *http.Request, action string) {
	mobile := strings.HasPrefix(action, "mobile/")
	if mobile {
		action = strings.TrimPrefix(action, "mobile/")
	}
	if mobile && action != "login" && action != "register" {
		fail(w, 404, "not found")
		return
	}
	if action == "me" && r.Method == "GET" {
		uid := s.user(r)
		if uid == "" {
			fail(w, 401, "authentication required")
			return
		}
		var email string
		_ = s.db.QueryRow(`SELECT email FROM users WHERE id=$1`, uid).Scan(&email)
		write(w, 200, map[string]any{"user": map[string]string{"id": uid, "email": email}})
		return
	}
	if action == "logout" && r.Method == "POST" {
		token, bearer := sessionToken(r)
		if bearer && (token == "" || s.user(r) == "") {
			fail(w, 401, "authentication required")
			return
		}
		if token != "" {
			h := sha256.Sum256([]byte(token))
			if _, e := s.db.Exec(`DELETE FROM sessions WHERE token_hash=$1`, hex.EncodeToString(h[:])); e != nil {
				fail(w, 500, "session error")
				return
			}
		}
		if !bearer {
			http.SetCookie(w, &http.Cookie{Name: "n9n_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secure})
		}
		write(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Method != "POST" || (action != "register" && action != "login") {
		fail(w, 404, "not found")
		return
	}
	if !s.allowAuth(r) {
		fail(w, 429, "too many authentication attempts; try again shortly")
		return
	}
	select {
	case s.authSlots <- struct{}{}:
		defer func() { <-s.authSlots }()
	default:
		fail(w, 429, "authentication is busy; try again shortly")
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
		_, e = s.db.Exec(`INSERT INTO users VALUES($1,$2,$3,$4)`, uid, a.Email, hash, now())
		if e != nil {
			fail(w, 409, "email already registered")
			return
		}
	} else {
		var hash []byte
		e := s.db.QueryRow(`SELECT id,password_hash FROM users WHERE email=$1`, a.Email).Scan(&uid, &hash)
		if e != nil || bcrypt.CompareHashAndPassword(hash, []byte(a.Password)) != nil {
			fail(w, 401, "invalid credentials")
			return
		}
	}
	user := map[string]string{"id": uid, "email": a.Email}
	if mobile {
		token, expiry, e := s.newSession(uid)
		if e != nil {
			fail(w, 500, "session error")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, map[string]any{"user": user, "token": token, "expires_at": expiry.Format(timeLayout)})
		return
	}
	if e := s.session(w, uid); e != nil {
		fail(w, 500, "session error")
		return
	}
	write(w, 200, map[string]any{"user": user})
}
func (s *Server) allowAuth(r *http.Request) bool {
	ip := r.Header.Get("X-Real-IP")
	if ip == "" {
		ip, _, _ = net.SplitHostPort(r.RemoteAddr)
	}
	if net.ParseIP(ip) == nil {
		ip = "unknown"
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	current := time.Now()
	if len(s.authWindows) > 1024 {
		for key, window := range s.authWindows {
			if current.Sub(window.Start) >= time.Minute {
				delete(s.authWindows, key)
			}
		}
	}
	window := s.authWindows[ip]
	if current.Sub(window.Start) >= time.Minute {
		window = authWindow{Start: current}
	}
	if window.Count >= 20 {
		return false
	}
	window.Count++
	s.authWindows[ip] = window
	return true
}
