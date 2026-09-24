package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func registered(t *testing.T, s *Server, email string) *http.Cookie {
	t.Helper()
	status, value, cookie := request(t, s, nil, "POST", "/api/auth/register", map[string]any{"email": email, "password": "test-password-123"}, false)
	requireOK(t, status, value)
	return cookie
}

func TestSchemaMigrationAndRestart(t *testing.T) {
	path := testDatabaseURL(t)
	for i := 0; i < 2; i++ {
		s, err := NewServer(path, testKey, testToken)
		if err != nil {
			t.Fatal(err)
		}
		var version int
		if err := s.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil || version != 1 {
			t.Fatalf("migration version=%d err=%v", version, err)
		}
		if i == 0 {
			if _, err := s.db.Exec(`INSERT INTO users VALUES('legacy-user','legacy@example.com',$1,'2025-01-01')`, []byte("hash")); err != nil {
				t.Fatal(err)
			}
		}
		var email string
		if err := s.db.QueryRow(`SELECT email FROM users WHERE id='legacy-user'`).Scan(&email); err != nil || email != "legacy@example.com" {
			t.Fatalf("lost legacy data: %q %v", email, err)
		}
		s.db.Close()
	}
}

func TestRetentionPreservesEventTombstone(t *testing.T) {
	s := testServer(t)
	t.Setenv("RUN_RETENTION_DAYS", "1")
	c := registered(t, s, "retention@example.com")
	graph := Graph{Nodes: []Node{{ID: "hook", Type: "webhook_trigger", Config: map[string]any{"secret": "retention-secret"}}}, Edges: []Edge{}}
	status, value, _ := request(t, s, c, "POST", "/api/workflows", map[string]any{"name": "retention", "draft": graph}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	status, value, _ = request(t, s, c, "POST", "/api/workflows/"+wid+"/publish", map[string]any{}, false)
	requireOK(t, status, value)
	status, value, _ = request(t, s, c, "POST", "/api/workflows/"+wid+"/activate", map[string]any{"active": true}, false)
	requireOK(t, status, value)
	call := func() (int, map[string]any) {
		r := httptest.NewRequest("POST", "/api/hooks/"+wid, strings.NewReader(`{"message":"hello"}`))
		r.Header.Set("X-Webhook-Secret", "retention-secret")
		r.Header.Set("X-Event-ID", "same-event")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		var result map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return w.Code, result
	}
	status, value = call()
	requireOK(t, status, value)
	rid := value["run"].(map[string]any)["id"].(string)
	old := time.Now().UTC().AddDate(0, 0, -2).Format(timeLayout)
	if _, err := s.db.Exec(`UPDATE runs SET status='succeeded',updated_at=$1 WHERE id=$2`, old, rid); err != nil {
		t.Fatal(err)
	}
	s.cleanup()
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM runs WHERE id=$1`, rid).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old run retained: %d %v", count, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM trigger_events WHERE workflow_id=$1 AND event_id='same-event'`, wid).Scan(&count); err != nil || count != 1 {
		t.Fatalf("dedupe tombstone lost: %d %v", count, err)
	}
	status, value = call()
	requireOK(t, status, value)
	if value["duplicate"] != true || value["run"] != nil {
		t.Fatalf("requeued retained event: %v", value)
	}
}

func TestDepthAndAuthLimits(t *testing.T) {
	s := testServer(t)
	c := registered(t, s, "depth@example.com")
	deep := any("leaf")
	for i := 0; i < 33; i++ {
		deep = map[string]any{"next": deep}
	}
	if withinJSONDepth(deep) {
		t.Fatal("accepted deeply nested JSON")
	}
	graph := Graph{Nodes: []Node{{ID: "trigger", Type: "manual_trigger", Config: map[string]any{}}, {ID: "set", Type: "set_fields", Config: map[string]any{"fields": map[string]any{"nested": deep}}}}, Edges: []Edge{{ID: "edge", Source: "trigger", Target: "set", SourcePort: "out"}}}
	status, _, _ := request(t, s, c, "POST", "/api/workflows", map[string]any{"name": "deep", "draft": graph}, false)
	if status != 400 {
		t.Fatalf("nested draft returned %d", status)
	}
	graph.Nodes[1].Config = map[string]any{"fields": map[string]any{}}
	wid, _ := publishedRun(t, s, c, graph)
	status, _, _ = request(t, s, c, "POST", "/api/workflows/"+wid+"/run", map[string]any{"input": map[string]any{"deep": deep}}, false)
	if status != 400 {
		t.Fatalf("nested run input returned %d", status)
	}
	for i := 0; i < 21; i++ {
		status, _, _ = request(t, s, nil, "POST", "/api/auth/login", map[string]any{"email": "depth@example.com", "password": "bad-password"}, false)
	}
	if status != 429 {
		t.Fatalf("auth throttle returned %d", status)
	}
}

func TestVersionImmutabilityAndForeignCredential(t *testing.T) {
	s := testServer(t)
	a := registered(t, s, "version-a@example.com")
	b := registered(t, s, "version-b@example.com")
	graph := Graph{Nodes: []Node{{ID: "trigger", Type: "manual_trigger", Config: map[string]any{}}, {ID: "set", Type: "set_fields", Config: map[string]any{"fields": map[string]any{"tag": "one"}}}}, Edges: []Edge{{ID: "edge", Source: "trigger", Target: "set", SourcePort: "out"}}}
	wid, rid := publishedRun(t, s, a, graph)
	graph.Nodes[1].Config["fields"] = map[string]any{"tag": "two"}
	status, value, _ := request(t, s, a, "PUT", "/api/workflows/"+wid, map[string]any{"name": "updated", "draft": graph}, false)
	requireOK(t, status, value)
	status, value, _ = request(t, s, a, "POST", "/api/workflows/"+wid+"/publish", map[string]any{}, false)
	requireOK(t, status, value)
	var previous string
	if err := s.db.QueryRow(`SELECT graph FROM versions WHERE workflow_id=$1 AND version=1`, wid).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(previous, `"one"`) || strings.Contains(previous, `"two"`) {
		t.Fatalf("published version mutated: %s", previous)
	}
	status, value, _ = request(t, s, a, "GET", "/api/runs/"+rid, nil, false)
	requireOK(t, status, value)
	if value["run"].(map[string]any)["version"] != float64(1) {
		t.Fatalf("queued run version changed: %v", value)
	}
	status, value, _ = request(t, s, a, "POST", "/api/credentials", map[string]any{"name": "mailbox", "kind": "gmail_oauth", "data": map[string]any{"refresh_token": "secret"}}, false)
	requireOK(t, status, value)
	credentialID := value["credential"].(map[string]any)["id"].(string)
	foreignGraph := Graph{Nodes: []Node{{ID: "email", Type: "email_trigger", Config: map[string]any{"poll_seconds": 60}, CredentialID: credentialID}}, Edges: []Edge{}}
	status, value, _ = request(t, s, b, "POST", "/api/workflows", map[string]any{"name": "foreign", "draft": foreignGraph}, false)
	requireOK(t, status, value)
	foreignID := value["workflow"].(map[string]any)["id"].(string)
	status, _, _ = request(t, s, b, "POST", "/api/workflows/"+foreignID+"/publish", map[string]any{}, false)
	if status != 400 {
		t.Fatalf("accepted another user's credential: %d", status)
	}
}

func TestTriggerDedupeCheckpointAndStaleVersion(t *testing.T) {
	s := testServer(t)
	c := registered(t, s, "trigger@example.com")
	graph := Graph{Nodes: []Node{{ID: "timer", Type: "schedule_trigger", Config: map[string]any{"interval_seconds": 60, "timezone": "UTC"}}}, Edges: []Edge{}}
	status, value, _ := request(t, s, c, "POST", "/api/workflows", map[string]any{"name": "trigger", "draft": graph}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	status, value, _ = request(t, s, c, "POST", "/api/workflows/"+wid+"/publish", map[string]any{}, false)
	requireOK(t, status, value)
	status, value, _ = request(t, s, c, "POST", "/api/workflows/"+wid+"/activate", map[string]any{"active": true}, false)
	requireOK(t, status, value)
	path := "/internal/triggers/" + wid + "/events"
	body := map[string]any{"version": 1, "event_id": "tick-one", "input": map[string]any{"scheduled_at": "2026-01-01T00:00:00Z"}, "checkpoint": map[string]any{"last_occurrence": 123}}
	status, value, _ = request(t, s, nil, "POST", path, body, true)
	requireOK(t, status, value)
	rid := value["run"].(map[string]any)["id"].(string)
	status, value, _ = request(t, s, nil, "POST", path, body, true)
	requireOK(t, status, value)
	if value["duplicate"] != true || value["run"].(map[string]any)["id"] != rid {
		t.Fatalf("event not deduplicated: %v", value)
	}
	status, value, _ = request(t, s, nil, "GET", "/internal/triggers", nil, true)
	requireOK(t, status, value)
	triggers := value["triggers"].([]any)
	if len(triggers) != 1 || triggers[0].(map[string]any)["checkpoint"].(map[string]any)["last_occurrence"] != float64(123) {
		t.Fatalf("checkpoint not saved: %v", value)
	}
	status, value, _ = request(t, s, c, "POST", "/api/workflows/"+wid+"/publish", map[string]any{}, false)
	requireOK(t, status, value)
	status, _, _ = request(t, s, nil, "POST", path, body, true)
	if status != 409 {
		t.Fatalf("stale trigger version accepted: %d", status)
	}
	status, value, _ = request(t, s, c, "POST", "/api/workflows/"+wid+"/activate", map[string]any{"active": false}, false)
	requireOK(t, status, value)
	body["version"] = 2
	status, _, _ = request(t, s, nil, "POST", path, body, true)
	if status != 409 {
		t.Fatalf("inactive trigger accepted: %d", status)
	}
}

func TestCancellationFencesWorker(t *testing.T) {
	s := testServer(t)
	c := registered(t, s, "cancel@example.com")
	graph := Graph{Nodes: []Node{{ID: "trigger", Type: "manual_trigger", Config: map[string]any{}}}, Edges: []Edge{}}
	_, rid := publishedRun(t, s, c, graph)
	status, value, _ := request(t, s, nil, "POST", "/internal/jobs/claim", map[string]any{"runner_id": "worker"}, true)
	requireOK(t, status, value)
	lease := value["job"].(map[string]any)["lease_token"].(string)
	status, value, _ = request(t, s, c, "POST", "/api/runs/"+rid+"/cancel", map[string]any{}, false)
	requireOK(t, status, value)
	if value["run"].(map[string]any)["status"] != "cancelled" {
		t.Fatal(value)
	}
	status, _, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/complete", map[string]any{"lease_token": lease, "status": "succeeded"}, true)
	if status != 409 {
		t.Fatalf("cancelled run completed: %d", status)
	}
}

func publishedRun(t *testing.T, s *Server, cookie *http.Cookie, graph Graph) (string, string) {
	t.Helper()
	status, value, _ := request(t, s, cookie, "POST", "/api/workflows", map[string]any{"name": "reliability", "draft": graph}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	status, value, _ = request(t, s, cookie, "POST", "/api/workflows/"+wid+"/publish", map[string]any{}, false)
	requireOK(t, status, value)
	status, value, _ = request(t, s, cookie, "POST", "/api/workflows/"+wid+"/run", map[string]any{"input": map[string]any{"name": "Ada"}}, false)
	requireOK(t, status, value)
	return wid, value["run"].(map[string]any)["id"].(string)
}

func TestRootAndPublishMappingValidation(t *testing.T) {
	s := testServer(t)
	status, _, _ := request(t, s, nil, "GET", "/api/", nil, false)
	if status != 404 {
		t.Fatalf("/api/ returned %d", status)
	}
	good := Graph{Nodes: []Node{
		{ID: "source", Type: "manual_trigger", Config: map[string]any{}},
		{ID: "first", Type: "set_fields", Config: map[string]any{"fields": map[string]any{"answer": "{{input.name}}"}}},
		{ID: "second", Type: "set_fields", Config: map[string]any{"fields": map[string]any{"answer": "{{nodes.first.answer}}"}}},
	}, Edges: []Edge{{ID: "a", Source: "source", Target: "first", SourcePort: "out"}, {ID: "b", Source: "first", Target: "second", SourcePort: "out"}}}
	if err := validatePublish(good); err != nil {
		t.Fatal(err)
	}
	good.Nodes[1].Config["fields"] = map[string]any{"answer": "{{nodes.second.answer}}"}
	if validatePublish(good) == nil {
		t.Fatal("accepted downstream mapping")
	}
	good.Nodes[1].Config["fields"] = map[string]any{"answer": "{{nodes.invalid.id}}"}
	if validatePublish(good) == nil {
		t.Fatal("accepted unknown node mapping")
	}
	good.Nodes[1].Config["fields"] = map[string]any{"answer": "{{nodes.first."}
	if validatePublish(good) == nil {
		t.Fatal("accepted malformed mapping")
	}
	good.Nodes[1].ID = "contains.dot"
	if validateDraft(good) == nil {
		t.Fatal("accepted unmappable node ID")
	}
}

func TestLeaseRecoveryPreservesStepsAndFencesOldToken(t *testing.T) {
	s := testServer(t)
	c := registered(t, s, "lease@example.com")
	graph := Graph{Nodes: []Node{{ID: "trigger", Type: "manual_trigger", Config: map[string]any{}}, {ID: "set", Type: "set_fields", Config: map[string]any{"fields": map[string]any{"name": "Ada"}}}}, Edges: []Edge{{ID: "e", Source: "trigger", Target: "set", SourcePort: "out"}}}
	_, rid := publishedRun(t, s, c, graph)
	status, value, _ := request(t, s, nil, "POST", "/internal/jobs/claim", map[string]any{"runner_id": "one"}, true)
	requireOK(t, status, value)
	lease := value["job"].(map[string]any)["lease_token"].(string)
	status, value, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/steps", map[string]any{"lease_token": lease, "node_id": "trigger", "status": "succeeded", "input": map[string]any{"name": "Ada"}, "output": map[string]any{"name": "Ada"}, "attempt": 1}, true)
	requireOK(t, status, value)
	status, value, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/steps", map[string]any{"lease_token": lease, "node_id": "set", "status": "running", "input": map[string]any{"name": "Ada"}, "output": nil, "attempt": 1}, true)
	requireOK(t, status, value)
	if _, err := s.db.Exec(`UPDATE runs SET lease_until='2000-01-01T00:00:00.000000000Z' WHERE id=$1`, rid); err != nil {
		t.Fatal(err)
	}
	status, value, _ = request(t, s, nil, "POST", "/internal/jobs/claim", map[string]any{"runner_id": "two"}, true)
	requireOK(t, status, value)
	job := value["job"].(map[string]any)
	if job["id"] != rid || len(job["steps"].([]any)) != 2 {
		t.Fatalf("lost resumed job/steps: %v", job)
	}
	status, _, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/heartbeat", map[string]any{"lease_token": lease}, true)
	if status != 409 {
		t.Fatalf("stale lease heartbeat returned %d", status)
	}
	status, _, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/steps", map[string]any{"lease_token": lease, "node_id": "set", "status": "succeeded", "input": map[string]any{}, "output": map[string]any{}, "attempt": 1}, true)
	if status != 409 {
		t.Fatalf("stale lease wrote a step: %d", status)
	}
}

func TestExpiredSideEffectBecomesUncertain(t *testing.T) {
	s := testServer(t)
	c := registered(t, s, "uncertain@example.com")
	graph := Graph{Nodes: []Node{{ID: "trigger", Type: "manual_trigger", Config: map[string]any{}}, {ID: "post", Type: "http_request", Config: map[string]any{"url": "https://example.com", "method": "POST"}}}, Edges: []Edge{{ID: "e", Source: "trigger", Target: "post", SourcePort: "out"}}}
	_, rid := publishedRun(t, s, c, graph)
	status, value, _ := request(t, s, nil, "POST", "/internal/jobs/claim", map[string]any{"runner_id": "one"}, true)
	requireOK(t, status, value)
	lease := value["job"].(map[string]any)["lease_token"].(string)
	status, value, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/steps", map[string]any{"lease_token": lease, "node_id": "post", "status": "running", "input": map[string]any{}, "output": nil, "attempt": 1}, true)
	requireOK(t, status, value)
	if _, err := s.db.Exec(`UPDATE runs SET lease_until='2000-01-01T00:00:00.000000000Z' WHERE id=$1`, rid); err != nil {
		t.Fatal(err)
	}
	status, value, _ = request(t, s, nil, "POST", "/internal/jobs/claim", map[string]any{"runner_id": "two"}, true)
	requireOK(t, status, value)
	if value["job"] != nil {
		t.Fatalf("unsafe run requeued: %v", value)
	}
	status, value, _ = request(t, s, c, "GET", "/api/runs/"+rid, nil, false)
	requireOK(t, status, value)
	if value["run"].(map[string]any)["status"] != "uncertain" {
		t.Fatalf("wrong expired status: %v", value)
	}
}

func TestConcurrentClaimOnlyOneRunnerGetsEachJob(t *testing.T) {
	s := testServer(t)
	c := registered(t, s, "claims@example.com")
	graph := Graph{Nodes: []Node{{ID: "trigger", Type: "manual_trigger", Config: map[string]any{}}}, Edges: []Edge{}}
	_, rid := publishedRun(t, s, c, graph)
	var group sync.WaitGroup
	results := make(chan string, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			job, err := s.claim("runner")
			if err != nil {
				results <- "error"
				return
			}
			if job != nil {
				results <- job.(map[string]any)["id"].(string)
			}
		}()
	}
	group.Wait()
	close(results)
	count := 0
	for result := range results {
		if result != rid {
			t.Fatalf("unexpected claim: %s", result)
		}
		count++
	}
	if count != 1 {
		t.Fatalf("job claimed %d times", count)
	}
}

func TestOAuthStateBoundToSessionAndOneUse(t *testing.T) {
	s := testServer(t)
	t.Setenv("GOOGLE_CLIENT_ID", "client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
	t.Setenv("GOOGLE_REDIRECT_URI", "http://example.test/api/oauth/google/callback")
	t.Setenv("GOOGLE_AUTH_URL", "https://accounts.google.com/o/oauth2/v2/auth")
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"refresh_token":"test-refresh-token"}`))
	}))
	defer tokenServer.Close()
	t.Setenv("GOOGLE_TOKEN_URL", tokenServer.URL)
	c := registered(t, s, "oauth@example.com")
	status, value, _ := request(t, s, c, "GET", "/api/oauth/google/start", nil, false)
	requireOK(t, status, value)
	parsed, err := url.Parse(value["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatal("missing OAuth state")
	}
	callback := "/api/oauth/google/callback?state=" + url.QueryEscape(state) + "&code=mock-code"
	other := registered(t, s, "other-oauth@example.com")
	call := func(cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", callback, nil)
		req.AddCookie(cookie)
		out := httptest.NewRecorder()
		s.ServeHTTP(out, req)
		return out
	}
	if out := call(other); out.Code != 302 || !strings.Contains(out.Header().Get("Location"), "error=") {
		t.Fatalf("state accepted for other session: %d %s", out.Code, out.Header().Get("Location"))
	}
	if out := call(c); out.Code != 302 || !strings.Contains(out.Header().Get("Location"), "connected=") {
		t.Fatalf("valid state rejected: %d %s", out.Code, out.Header().Get("Location"))
	}
	if out := call(c); out.Code != 302 || !strings.Contains(out.Header().Get("Location"), "error=") {
		t.Fatalf("replayed state accepted: %d %s", out.Code, out.Header().Get("Location"))
	}
	status, value, _ = request(t, s, c, "GET", "/api/credentials", nil, false)
	requireOK(t, status, value)
	if len(value["credentials"].([]any)) != 1 {
		t.Fatal(value)
	}
	var data any
	if err := json.Unmarshal([]byte(jsonText(value)), &data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(jsonText(data), "test-refresh-token") {
		t.Fatal("OAuth secret leaked")
	}
}
