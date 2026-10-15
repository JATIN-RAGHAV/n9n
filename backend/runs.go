package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

type querier interface{ QueryRow(string, ...any) *sql.Row }

func queueRun(db *sql.DB, wid string, version int, input any) (Run, error) {
	id := id()
	t := now()
	_, e := db.Exec(`INSERT INTO runs(id,workflow_id,version,status,input,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, wid, version, "queued", jsonText(input), t, t)
	if e != nil {
		return Run{}, e
	}
	return Run{ID: id, WorkflowID: wid, Version: version, Kind: "normal", Status: "queued", Input: input, CreatedAt: t, UpdatedAt: t}, nil
}
func queueTestRun(tx *sql.Tx, wid string, version int, graph Graph, input any) (Run, error) {
	id := id()
	t := now()
	_, err := tx.Exec(`INSERT INTO runs(id,workflow_id,version,status,input,graph_snapshot,is_test,kind,created_at,updated_at) VALUES($1,$2,$3,'queued',$4,$5,TRUE,'test',$6,$6)`, id, wid, version, jsonText(input), jsonText(graph), t)
	if err != nil {
		return Run{}, err
	}
	return Run{ID: id, WorkflowID: wid, Version: version, Kind: "test", Test: true, Status: "queued", Input: input, CreatedAt: t, UpdatedAt: t}, nil
}
func getRun(q querier, id string) (Run, error) {
	var x Run
	var input string
	var resume sql.NullString
	e := q.QueryRow(`SELECT id,workflow_id,version,kind,is_test,parent_run_id,resume_from_node,status,input,error,created_at,updated_at FROM runs WHERE id=$1`, id).Scan(&x.ID, &x.WorkflowID, &x.Version, &x.Kind, &x.Test, &x.ParentRunID, &resume, &x.Status, &input, &x.Error, &x.CreatedAt, &x.UpdatedAt)
	x.ResumeFromNode = resume.String
	_ = json.Unmarshal([]byte(input), &x.Input)
	return x, e
}
func (s *Server) ownedRun(id, uid string) (Run, error) {
	var x Run
	var input string
	var resume sql.NullString
	e := s.db.QueryRow(`SELECT r.id,r.workflow_id,r.version,r.kind,r.is_test,r.parent_run_id,r.resume_from_node,r.status,r.input,r.error,r.created_at,r.updated_at FROM runs r JOIN workflows w ON w.id=r.workflow_id WHERE r.id=$1 AND w.user_id=$2`, id, uid).Scan(&x.ID, &x.WorkflowID, &x.Version, &x.Kind, &x.Test, &x.ParentRunID, &resume, &x.Status, &input, &x.Error, &x.CreatedAt, &x.UpdatedAt)
	x.ResumeFromNode = resume.String
	_ = json.Unmarshal([]byte(input), &x.Input)
	return x, e
}
// stepAttempts returns a run's recorded HTTP attempts grouped by node.
func (s *Server) stepAttempts(runID string) (map[string][]StepAttempt, error) {
	rows, e := s.db.Query(`SELECT node_id,attempt,status,http_status,error,duration_ms,created_at FROM step_attempts WHERE run_id=$1 ORDER BY id`, runID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string][]StepAttempt{}
	for rows.Next() {
		var nodeID string
		var a StepAttempt
		var status sql.NullInt64
		if rows.Scan(&nodeID, &a.Attempt, &a.Status, &status, &a.Error, &a.DurationMs, &a.CreatedAt) == nil {
			if status.Valid {
				code := int(status.Int64)
				a.HTTPStatus = &code
			}
			out[nodeID] = append(out[nodeID], a)
		}
	}
	return out, rows.Err()
}
// childRuns lists the replay and shadow runs that came from a run.
func (s *Server) childRuns(runID, uid string) ([]Run, error) {
	rows, e := s.db.Query(`SELECT id FROM runs WHERE parent_run_id=$1 ORDER BY created_at`, runID)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	out := []Run{}
	for _, id := range ids {
		if x, e := s.ownedRun(id, uid); e == nil {
			out = append(out, x)
		}
	}
	return out, nil
}
func (s *Server) runs(w http.ResponseWriter, r *http.Request, uid string, p []string) {
	if len(p) == 0 && r.Method == "GET" {
		filter := r.URL.Query().Get("workflow_id")
		q := `SELECT r.id FROM runs r JOIN workflows w ON w.id=r.workflow_id WHERE w.user_id=$1`
		args := []any{uid}
		if filter != "" {
			q += ` AND w.id=$2`
			args = append(args, filter)
		}
		q += ` ORDER BY r.created_at DESC LIMIT 200`
		rows, e := s.db.Query(q, args...)
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		out := []Run{}
		for _, id := range ids {
			x, e := s.ownedRun(id, uid)
			if e == nil {
				out = append(out, x)
			}
		}
		write(w, 200, map[string]any{"runs": out})
		return
	}
	if len(p) == 0 {
		fail(w, 404, "not found")
		return
	}
	x, e := s.ownedRun(p[0], uid)
	if e != nil {
		fail(w, 404, "run not found")
		return
	}
	if len(p) == 1 && r.Method == "GET" {
		rows, e := s.db.Query(`SELECT node_id,status,input,output,error,branch,attempt,COALESCE(started_at,''),COALESCE(finished_at,''),duration_ms,created_at FROM steps WHERE run_id=$1 ORDER BY id`, x.ID)
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		defer rows.Close()
		steps := []Step{}
		for rows.Next() {
			var t Step
			var in, out string
			var duration sql.NullInt64
			if rows.Scan(&t.NodeID, &t.Status, &in, &out, &t.Error, &t.Branch, &t.Attempt, &t.StartedAt, &t.FinishedAt, &duration, &t.CreatedAt) == nil {
				if duration.Valid {
					ms := int(duration.Int64)
					t.DurationMs = &ms
				}
				_ = json.Unmarshal([]byte(in), &t.Input)
				_ = json.Unmarshal([]byte(out), &t.Output)
				steps = append(steps, t)
			}
		}
		rows.Close()
		attempts, e := s.stepAttempts(x.ID)
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		// Attempts belong to a node, so they hang off its latest step row.
		latest := map[string]int{}
		for i, t := range steps {
			latest[t.NodeID] = i
		}
		for nodeID, list := range attempts {
			if i, ok := latest[nodeID]; ok {
				steps[i].Attempts = list
			}
		}
		var graphText string
		e = s.db.QueryRow(`SELECT COALESCE(r.graph_snapshot,v.graph) FROM runs r LEFT JOIN versions v ON v.workflow_id=r.workflow_id AND v.version=r.version WHERE r.id=$1`, x.ID).Scan(&graphText)
		if e != nil {
			fail(w, 500, "workflow snapshot unavailable")
			return
		}
		var graph Graph
		if e = json.Unmarshal([]byte(graphText), &graph); e != nil {
			fail(w, 500, "invalid workflow snapshot")
			return
		}
		nodeTypes := map[string]string{}
		for _, node := range graph.Nodes {
			nodeTypes[node.ID] = node.Type
		}
		children, e := s.childRuns(x.ID, uid)
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		write(w, 200, map[string]any{"run": x, "steps": steps, "node_types": nodeTypes, "children": children})
		return
	}
	if len(p) == 2 && p[1] == "replay" && r.Method == "POST" {
		s.replay(w, r, uid, x)
		return
	}
	if len(p) == 2 && p[1] == "cancel" && r.Method == "POST" {
		if x.Status == "queued" || x.Status == "running" {
			_, e := s.db.Exec(`UPDATE runs SET status='cancelled',lease_token=NULL,lease_until=NULL,updated_at=$1 WHERE id=$2 AND status IN ('queued','running')`, now(), x.ID)
			if e != nil {
				fail(w, 500, "database error")
				return
			}
			x, _ = s.ownedRun(x.ID, uid)
		}
		write(w, 200, map[string]any{"run": x})
		return
	}
	fail(w, 404, "not found")
}
