package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const testToken = "0123456789abcdef0123456789abcdef"

func testServer(t *testing.T) *Server {
	t.Helper()
	s, e := NewServer(filepath.Join(t.TempDir(), "test.db"), testKey, testToken)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.db.Close() })
	return s
}
func request(t *testing.T, s *Server, cookie *http.Cookie, method, path string, body any, internal bool) (int, map[string]any, *http.Cookie) {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if internal {
		r.Header.Set("Authorization", "Bearer "+testToken)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var v map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	var c *http.Cookie
	for _, x := range w.Result().Cookies() {
		if x.Name == "n9n_session" {
			c = x
		}
	}
	return w.Code, v, c
}
func requireOK(t *testing.T, status int, v map[string]any) {
	t.Helper()
	if status != 200 {
		t.Fatalf("status %d: %v", status, v)
	}
}
func TestWorkflowRunAndIsolation(t *testing.T) {
	s := testServer(t)
	status, v, c := request(t, s, nil, "POST", "/api/auth/register", map[string]any{"email": "a@example.com", "password": "password123"}, false)
	requireOK(t, status, v)
	status, v, c2 := request(t, s, nil, "POST", "/api/auth/register", map[string]any{"email": "b@example.com", "password": "password123"}, false)
	requireOK(t, status, v)
	graph := map[string]any{"nodes": []any{map[string]any{"id": "start", "type": "manual_trigger", "position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{}}, map[string]any{"id": "set", "type": "set_fields", "position": map[string]any{"x": 200, "y": 0}, "config": map[string]any{"fields": map[string]any{"message": "hello"}}}}, "edges": []any{map[string]any{"id": "e1", "source": "start", "target": "set", "source_port": "out"}}}
	status, v, _ = request(t, s, c, "POST", "/api/workflows", map[string]any{"name": "Test", "draft": graph}, false)
	requireOK(t, status, v)
	wid := v["workflow"].(map[string]any)["id"].(string)
	status, v, _ = request(t, s, c, "GET", "/api/workflows", nil, false)
	requireOK(t, status, v)
	if len(v["workflows"].([]any)) != 1 {
		t.Fatal(v)
	}
	status, _, _ = request(t, s, c2, "GET", "/api/workflows/"+wid, nil, false)
	if status != 404 {
		t.Fatal("cross-user workflow read")
	}
	status, v, _ = request(t, s, c, "POST", "/api/workflows/"+wid+"/publish", map[string]any{}, false)
	requireOK(t, status, v)
	status, v, _ = request(t, s, c, "POST", "/api/workflows/"+wid+"/run", map[string]any{"input": map[string]any{"foo": 1}}, false)
	requireOK(t, status, v)
	rid := v["run"].(map[string]any)["id"].(string)
	status, v, _ = request(t, s, c, "GET", "/api/runs", nil, false)
	requireOK(t, status, v)
	if len(v["runs"].([]any)) != 1 {
		t.Fatal(v)
	}
	status, v, _ = request(t, s, nil, "POST", "/internal/jobs/claim", map[string]any{"runner_id": "test"}, true)
	requireOK(t, status, v)
	job := v["job"].(map[string]any)
	if job["id"] != rid {
		t.Fatal(job)
	}
	lease := job["lease_token"].(string)
	status, v, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/steps", map[string]any{"lease_token": lease, "node_id": "set", "status": "succeeded", "input": map[string]any{}, "output": map[string]any{"message": "hello"}, "attempt": 1}, true)
	requireOK(t, status, v)
	status, v, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/complete", map[string]any{"lease_token": lease, "status": "succeeded"}, true)
	requireOK(t, status, v)
	status, v, _ = request(t, s, c, "GET", "/api/runs/"+rid, nil, false)
	requireOK(t, status, v)
	if v["run"].(map[string]any)["status"] != "succeeded" || len(v["steps"].([]any)) != 1 {
		t.Fatal(v)
	}
}
func TestPublishRejectsCycleAndCredentialsNeverEcho(t *testing.T) {
	s := testServer(t)
	status, v, c := request(t, s, nil, "POST", "/api/auth/register", map[string]any{"email": "a@example.com", "password": "password123"}, false)
	requireOK(t, status, v)
	status, v, _ = request(t, s, c, "POST", "/api/credentials", map[string]any{"name": "Gmail", "kind": "gmail_oauth", "data": map[string]any{"refresh_token": "secret-value"}}, false)
	requireOK(t, status, v)
	if bytes.Contains([]byte(jsonText(v)), []byte("secret-value")) {
		t.Fatal("secret echoed")
	}
	status, v, _ = request(t, s, c, "GET", "/api/credentials", nil, false)
	requireOK(t, status, v)
	if bytes.Contains([]byte(jsonText(v)), []byte("secret-value")) {
		t.Fatal("secret listed")
	}
	g := Graph{Nodes: []Node{{ID: "a", Type: "manual_trigger", Config: map[string]any{}}, {ID: "b", Type: "set_fields", Config: map[string]any{"fields": map[string]any{}}}}, Edges: []Edge{{ID: "1", Source: "a", Target: "b", SourcePort: "out"}, {ID: "2", Source: "b", Target: "a", SourcePort: "out"}}}
	if validatePublish(g) == nil {
		t.Fatal("accepted cycle")
	}
}
