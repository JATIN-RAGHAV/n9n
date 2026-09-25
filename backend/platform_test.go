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
