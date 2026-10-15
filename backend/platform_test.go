package main

import (
	"database/sql"
	"strings"
	"testing"
)

// openAtVersion prepares a database at the given schema version, as an older
// server would have left it.
func openAtVersion(t *testing.T, version int) (string, *sql.DB) {
	t.Helper()
	path := testDatabaseURL(t)
	db, err := sql.Open("pgx", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < version; i++ {
		for _, q := range migrations[i] {
			if _, err = db.Exec(q); err != nil {
				t.Fatalf("migration %d: %v", i+1, err)
			}
		}
		if _, err = db.Exec(`INSERT INTO schema_migrations VALUES($1,'2025-01-01')`, i+1); err != nil {
			t.Fatal(err)
		}
	}
	return path, db
}

func TestMigration5BackfillsRunKindsAndKeepsOldWritersWorking(t *testing.T) {
	path, db := openAtVersion(t, 4)
	graph := `{"nodes":[{"id":"t","type":"manual_trigger","config":{}}],"edges":[]}`
	for _, q := range []string{
		`INSERT INTO users VALUES('u1','u1@example.com','h','2025-01-01')`,
		`INSERT INTO workflows(id,user_id,name,draft,published_version,created_at,updated_at) VALUES('w1','u1','w','` + graph + `',1,'2025-01-01','2025-01-01')`,
		`INSERT INTO versions VALUES('w1',1,'` + graph + `')`,
		`INSERT INTO runs(id,workflow_id,version,status,input,created_at,updated_at) VALUES('normal','w1',1,'succeeded','{}','2025-01-01','2025-01-01')`,
		`INSERT INTO runs(id,workflow_id,version,status,input,graph_snapshot,is_test,created_at,updated_at) VALUES('test','w1',0,'succeeded','{}','` + graph + `',TRUE,'2025-01-01','2025-01-01')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewServer(path, testKey, testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	for id, want := range map[string]string{"normal": "normal", "test": "test"} {
		var kind string
		if err := s.db.QueryRow(`SELECT kind FROM runs WHERE id=$1`, id).Scan(&kind); err != nil || kind != want {
			t.Fatalf("run %s kind=%q err=%v", id, kind, err)
		}
	}
	// A server that only knows is_test still produces a consistent test run.
	if _, err := s.db.Exec(`INSERT INTO runs(id,workflow_id,version,status,input,graph_snapshot,is_test,created_at,updated_at) VALUES('legacy','w1',1,'queued','{}','` + graph + `',TRUE,'2025-01-02','2025-01-02')`); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := s.db.QueryRow(`SELECT kind FROM runs WHERE id='legacy'`).Scan(&kind); err != nil || kind != "test" {
		t.Fatalf("legacy test run kind=%q err=%v", kind, err)
	}
	// Replay and shadow runs must carry a snapshot; normal runs must not.
	_, err = s.db.Exec(`INSERT INTO runs(id,workflow_id,version,status,input,kind,created_at,updated_at) VALUES('bad','w1',1,'queued','{}','replay','2025-01-02','2025-01-02')`)
	if err == nil || !strings.Contains(err.Error(), "replay run requires") {
		t.Fatalf("replay without snapshot accepted: %v", err)
	}
	if _, err = s.db.Exec(`INSERT INTO runs(id,workflow_id,version,status,input,graph_snapshot,kind,parent_run_id,resume_from_node,created_at,updated_at) VALUES('replay','w1',1,'queued','{}','` + graph + `','replay','normal','t','2025-01-02','2025-01-02')`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO runs(id,workflow_id,version,status,input,graph_snapshot,created_at,updated_at) VALUES('bad2','w1',1,'queued','{}','` + graph + `','2025-01-02','2025-01-02')`); err == nil {
		t.Fatal("normal run with snapshot accepted")
	}
	run, err := getRun(s.db, "replay")
	if err != nil || run.Kind != "replay" || run.ParentRunID == nil || *run.ParentRunID != "normal" || run.ResumeFromNode != "t" {
		t.Fatalf("replay run fields: %#v %v", run, err)
	}
}

func TestTestRunAndClaimedJobReportKind(t *testing.T) {
	s := testServer(t)
	cookie := registered(t, s, "kind-"+id()+"@example.com")
	graph := map[string]any{"nodes": []any{map[string]any{"id": "start", "type": "manual_trigger", "position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{}}}, "edges": []any{}}
	status, value, _ := request(t, s, cookie, "POST", "/api/workflows", map[string]any{"name": "kind", "draft": graph}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	status, value, _ = request(t, s, cookie, "POST", "/api/workflows/"+wid+"/test", map[string]any{"input": map[string]any{}}, false)
	requireOK(t, status, value)
	run := value["run"].(map[string]any)
	if run["kind"] != "test" || run["test"] != true {
		t.Fatalf("test run: %#v", run)
	}
	job, err := s.claim("timing")
	if err != nil {
		t.Fatal(err)
	}
	if job.(map[string]any)["kind"] != "test" {
		t.Fatalf("job kind: %#v", job)
	}
}

func TestFeatureDiscoveryAndCatalogVersion(t *testing.T) {
	s := testServer(t)
	if status, _, _ := request(t, s, nil, "GET", "/api/features", nil, false); status != 401 {
		t.Fatalf("features without session: %d", status)
	}
	cookie := registered(t, s, "features-"+id()+"@example.com")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ENABLE_CODE_NODE", "")
	status, value, _ := request(t, s, cookie, "GET", "/api/features", nil, false)
	requireOK(t, status, value)
	features := value["features"].(map[string]any)
	if features["ai"] != false || features["code_node"] != false {
		t.Fatalf("features off by default: %#v", features)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("ENABLE_CODE_NODE", "true")
	status, value, _ = request(t, s, cookie, "GET", "/api/features", nil, false)
	requireOK(t, status, value)
	features = value["features"].(map[string]any)
	if features["ai"] != true || features["code_node"] != true {
		t.Fatalf("features on: %#v", features)
	}
	status, value, _ = request(t, s, cookie, "GET", "/api/nodes", nil, false)
	requireOK(t, status, value)
	if value["version"] != float64(2) || len(value["nodes"].([]any)) != len(catalog) {
		t.Fatalf("catalog: version=%v nodes=%d", value["version"], len(value["nodes"].([]any)))
	}
}

func TestRunDetailReturnsTheExecutedGraph(t *testing.T) {
	s := testServer(t)
	cookie := registered(t, s, "graph-"+id()+"@example.com")
	graph := map[string]any{"nodes": []any{
		map[string]any{"id": "start", "type": "manual_trigger", "position": map[string]any{"x": 10, "y": 20}, "config": map[string]any{}},
		map[string]any{"id": "set", "type": "set_fields", "position": map[string]any{"x": 300, "y": 20}, "config": map[string]any{"fields": map[string]any{"a": 1}}},
	}, "edges": []any{map[string]any{"id": "e1", "source": "start", "target": "set", "source_port": "out"}}}
	status, value, _ := request(t, s, cookie, "POST", "/api/workflows", map[string]any{"name": "graph", "draft": graph}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	status, value, _ = request(t, s, cookie, "POST", "/api/workflows/"+wid+"/test", map[string]any{"input": map[string]any{}}, false)
	requireOK(t, status, value)
	rid := value["run"].(map[string]any)["id"].(string)
	// Editing the draft afterwards must not change what the run reports.
	graph["nodes"].([]any)[1].(map[string]any)["config"] = map[string]any{"fields": map[string]any{"a": 2}}
	status, value, _ = request(t, s, cookie, "PUT", "/api/workflows/"+wid, map[string]any{"name": "graph", "draft": graph}, false)
	requireOK(t, status, value)
	status, value, _ = request(t, s, cookie, "GET", "/api/runs/"+rid, nil, false)
	requireOK(t, status, value)
	executed := value["graph"].(map[string]any)
	nodes := executed["nodes"].([]any)
	if len(nodes) != 2 || len(executed["edges"].([]any)) != 1 {
		t.Fatalf("graph: %#v", executed)
	}
	set := nodes[1].(map[string]any)
	if set["id"] != "set" || set["position"].(map[string]any)["x"] != float64(300) || set["config"].(map[string]any)["fields"].(map[string]any)["a"] != float64(1) {
		t.Fatalf("snapshot node: %#v", set)
	}
}

func TestHTTPAttemptsAreStoredAndNestedUnderSteps(t *testing.T) {
	s := testServer(t)
	cookie := registered(t, s, "attempts-"+id()+"@example.com")
	graph := map[string]any{"nodes": []any{
		map[string]any{"id": "start", "type": "manual_trigger", "position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{}},
		map[string]any{"id": "call", "type": "http_request", "position": map[string]any{"x": 200, "y": 0}, "config": map[string]any{"url": "https://example.com/", "method": "GET"}},
	}, "edges": []any{map[string]any{"id": "e1", "source": "start", "target": "call", "source_port": "out"}}}
	status, value, _ := request(t, s, cookie, "POST", "/api/workflows", map[string]any{"name": "attempts", "draft": graph}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	status, value, _ = request(t, s, cookie, "POST", "/api/workflows/"+wid+"/test", map[string]any{"input": map[string]any{}}, false)
	requireOK(t, status, value)
	rid := value["run"].(map[string]any)["id"].(string)
	claimed, err := s.claim("attempts")
	if err != nil {
		t.Fatal(err)
	}
	lease := claimed.(map[string]any)["lease_token"].(string)
	step := func(node, state string) {
		t.Helper()
		status, value, _ := request(t, s, nil, "POST", "/internal/jobs/"+rid+"/steps", map[string]any{"lease_token": lease, "node_id": node, "status": state, "input": map[string]any{}, "output": map[string]any{}, "attempt": 1}, true)
		requireOK(t, status, value)
	}
	step("start", "succeeded")
	step("call", "running")
	for i, code := range []int{500, 503, 200} {
		state := "failed"
		if code == 200 {
			state = "succeeded"
		}
		status, value, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/attempts", map[string]any{"lease_token": lease, "node_id": "call", "attempt": i + 1, "status": state, "http_status": code, "error": "", "duration_ms": 12 * (i + 1)}, true)
		requireOK(t, status, value)
	}
	step("call", "succeeded")
	for _, bad := range []map[string]any{
		{"lease_token": lease, "node_id": "call", "attempt": 0, "status": "failed", "error": "", "duration_ms": 1},
		{"lease_token": lease, "node_id": "call", "attempt": 1, "status": "retrying", "error": "", "duration_ms": 1},
		{"lease_token": lease, "node_id": "call", "attempt": 1, "status": "failed", "http_status": 42, "error": "", "duration_ms": 1},
		{"lease_token": lease, "node_id": "ghost", "attempt": 1, "status": "failed", "error": "", "duration_ms": 1},
	} {
		if status, _, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/attempts", bad, true); status != 400 {
			t.Fatalf("invalid attempt accepted: %d %v", status, bad)
		}
	}
	if status, _, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/attempts", map[string]any{"lease_token": "wrong", "node_id": "call", "attempt": 1, "status": "failed", "error": "", "duration_ms": 1}, true); status != 409 {
		t.Fatalf("attempt without lease accepted: %d", status)
	}
	status, value, _ = request(t, s, cookie, "GET", "/api/runs/"+rid, nil, false)
	requireOK(t, status, value)
	steps := value["steps"].([]any)
	if len(steps) != 3 {
		t.Fatalf("steps: %#v", steps)
	}
	if _, present := steps[1].(map[string]any)["attempts"]; present {
		t.Fatalf("running step carries attempts: %#v", steps[1])
	}
	attempts := steps[2].(map[string]any)["attempts"].([]any)
	if len(attempts) != 3 {
		t.Fatalf("attempts: %#v", attempts)
	}
	first, last := attempts[0].(map[string]any), attempts[2].(map[string]any)
	if first["attempt"] != float64(1) || first["status"] != "failed" || first["http_status"] != float64(500) || first["duration_ms"] != float64(12) {
		t.Fatalf("first attempt: %#v", first)
	}
	if last["attempt"] != float64(3) || last["status"] != "succeeded" || last["http_status"] != float64(200) {
		t.Fatalf("last attempt: %#v", last)
	}
	if _, present := steps[0].(map[string]any)["attempts"]; present {
		t.Fatalf("trigger step carries attempts: %#v", steps[0])
	}
}

func TestStepTimingIsStoredAndReported(t *testing.T) {
	s := testServer(t)
	cookie := registered(t, s, "timing-"+id()+"@example.com")
	graph := map[string]any{"nodes": []any{map[string]any{"id": "start", "type": "manual_trigger", "position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{}}}, "edges": []any{}}
	status, value, _ := request(t, s, cookie, "POST", "/api/workflows", map[string]any{"name": "timing", "draft": graph}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	status, value, _ = request(t, s, cookie, "POST", "/api/workflows/"+wid+"/test", map[string]any{"input": map[string]any{}}, false)
	requireOK(t, status, value)
	rid := value["run"].(map[string]any)["id"].(string)
	claimed, err := s.claim("timing")
	if err != nil {
		t.Fatal(err)
	}
	lease := claimed.(map[string]any)["lease_token"].(string)
	started := "2026-01-02T03:04:05.123456789Z"
	for _, body := range []map[string]any{
		{"lease_token": lease, "node_id": "start", "status": "running", "input": map[string]any{}, "output": nil, "attempt": 1, "started_at": started},
		{"lease_token": lease, "node_id": "start", "status": "succeeded", "input": map[string]any{}, "output": map[string]any{}, "attempt": 1, "started_at": started, "duration_ms": 42},
	} {
		status, value, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/steps", body, true)
		requireOK(t, status, value)
	}
	for _, bad := range []map[string]any{
		{"lease_token": lease, "node_id": "start", "status": "succeeded", "input": map[string]any{}, "output": map[string]any{}, "attempt": 1, "started_at": "yesterday"},
		{"lease_token": lease, "node_id": "start", "status": "succeeded", "input": map[string]any{}, "output": map[string]any{}, "attempt": 1, "duration_ms": -1},
	} {
		if status, _, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/steps", bad, true); status != 400 {
			t.Fatalf("invalid timing accepted: %d %v", status, bad)
		}
	}
	// A runner that does not send timing still works.
	status, value, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/steps", map[string]any{"lease_token": lease, "node_id": "start", "status": "skipped", "input": map[string]any{}, "output": nil, "attempt": 1}, true)
	requireOK(t, status, value)
	status, value, _ = request(t, s, cookie, "GET", "/api/runs/"+rid, nil, false)
	requireOK(t, status, value)
	steps := value["steps"].([]any)
	running, done, legacy := steps[0].(map[string]any), steps[1].(map[string]any), steps[2].(map[string]any)
	if running["started_at"] != started || running["finished_at"] != nil || running["duration_ms"] != nil {
		t.Fatalf("running step: %#v", running)
	}
	if done["started_at"] != started || done["finished_at"] == nil || done["duration_ms"] != float64(42) {
		t.Fatalf("finished step: %#v", done)
	}
	if legacy["started_at"] != nil || legacy["duration_ms"] != nil || legacy["finished_at"] == nil {
		t.Fatalf("legacy step: %#v", legacy)
	}
}
