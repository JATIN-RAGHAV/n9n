package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func testServerPair(t *testing.T) (*Server, *Server) {
	t.Helper()
	dsn := testDatabaseURL(t)
	a, err := NewServer(dsn, testKey, testToken)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewServer(dsn, testKey, testToken)
	if err != nil {
		a.db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.db.Close(); _ = b.db.Close() })
	return a, b
}

func TestConcurrentPublishAndClaim(t *testing.T) {
	a, b := testServerPair(t)
	c := registered(t, a, "concurrent@example.com")
	graph := Graph{Nodes: []Node{{ID: "start", Type: "manual_trigger", Config: map[string]any{}}}, Edges: []Edge{}}
	status, value, _ := request(t, a, c, "POST", "/api/workflows", map[string]any{"name": "concurrent", "draft": graph}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	servers := []*Server{a, b}
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for _, server := range servers {
		wg.Add(1)
		go func(server *Server) {
			defer wg.Done()
			code, body, _ := request(t, server, c, "POST", "/api/workflows/"+wid+"/publish", map[string]any{}, false)
			_ = body
			statuses <- code
		}(server)
	}
	wg.Wait()
	close(statuses)
	for code := range statuses {
		if code != 200 {
			t.Fatalf("concurrent publish status %d", code)
		}
	}
	var count, maximum int
	if err := a.db.QueryRow(`SELECT count(*),max(version) FROM versions WHERE workflow_id=$1`, wid).Scan(&count, &maximum); err != nil || count != 2 || maximum != 2 {
		t.Fatalf("version rows=%d max=%d err=%v", count, maximum, err)
	}
	status, value, _ = request(t, a, c, "POST", "/api/workflows/"+wid+"/run", map[string]any{"input": map[string]any{}}, false)
	requireOK(t, status, value)
	type claimResult struct { job any; err error }
	jobs := make(chan claimResult, 2)
	for _, server := range servers {
		wg.Add(1)
		go func(server *Server) { defer wg.Done(); job, err := server.claim("test-runner"); jobs <- claimResult{job, err} }(server)
	}
	wg.Wait()
	close(jobs)
	claimed := 0
	for result := range jobs {
		if result.err != nil { t.Fatalf("claim failed: %v", result.err) }
		if result.job != nil {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("same queued job claimed %d times", claimed)
	}
}

func TestConcurrentWebhookDedupe(t *testing.T) {
	a, b := testServerPair(t)
	c := registered(t, a, "dedupe@example.com")
	graph := Graph{Nodes: []Node{{ID: "hook", Type: "webhook_trigger", Config: map[string]any{"secret": "secret"}}}, Edges: []Edge{}}
	status, value, _ := request(t, a, c, "POST", "/api/workflows", map[string]any{"name": "hook", "draft": graph}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	for _, action := range []string{"publish", "activate"} {
		body := map[string]any{}
		if action == "activate" {
			body["active"] = true
		}
		status, value, _ = request(t, a, c, "POST", "/api/workflows/"+wid+"/"+action, body, false)
		requireOK(t, status, value)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, server := range []*Server{a, b} {
		wg.Add(1)
		go func(server *Server) {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodPost, "/api/hooks/"+wid, strings.NewReader(`{"message":"once"}`))
			r.Header.Set("X-Webhook-Secret", "secret")
			r.Header.Set("X-Event-ID", "duplicate-event")
			w := httptest.NewRecorder()
			server.ServeHTTP(w, r)
			codes <- w.Code
		}(server)
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatalf("concurrent webhook status %d", code)
		}
	}
	var count int
	if err := a.db.QueryRow(`SELECT count(*) FROM trigger_events WHERE workflow_id=$1`, wid).Scan(&count); err != nil || count != 1 {
		t.Fatalf("event rows=%d err=%v", count, err)
	}
	if err := a.db.QueryRow(`SELECT count(*) FROM runs WHERE workflow_id=$1`, wid).Scan(&count); err != nil || count != 1 {
		t.Fatalf("run rows=%d err=%v", count, err)
	}
}
