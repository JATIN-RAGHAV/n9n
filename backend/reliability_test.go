package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func registered(t *testing.T, s *Server, email string) *http.Cookie {
	t.Helper()
	status, value, cookie := request(t, s, nil, "POST", "/api/auth/register", map[string]any{"email": email, "password": "test-password-123"}, false)
	requireOK(t, status, value)
	return cookie
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
	if _, err := s.db.Exec(`UPDATE runs SET lease_until='2000-01-01T00:00:00.000000000Z' WHERE id=?`, rid); err != nil {
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
	if _, err := s.db.Exec(`UPDATE runs SET lease_until='2000-01-01T00:00:00.000000000Z' WHERE id=?`, rid); err != nil {
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
