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
	return Run{ID: id, WorkflowID: wid, Version: version, Status: "queued", Input: input, CreatedAt: t, UpdatedAt: t}, nil
}
func getRun(q querier, id string) (Run, error) {
	var x Run
	var input string
	e := q.QueryRow(`SELECT id,workflow_id,version,status,input,error,created_at,updated_at FROM runs WHERE id=$1`, id).Scan(&x.ID, &x.WorkflowID, &x.Version, &x.Status, &input, &x.Error, &x.CreatedAt, &x.UpdatedAt)
	_ = json.Unmarshal([]byte(input), &x.Input)
	return x, e
}
func (s *Server) ownedRun(id, uid string) (Run, error) {
	var x Run
	var input string
	e := s.db.QueryRow(`SELECT r.id,r.workflow_id,r.version,r.status,r.input,r.error,r.created_at,r.updated_at FROM runs r JOIN workflows w ON w.id=r.workflow_id WHERE r.id=$1 AND w.user_id=$2`, id, uid).Scan(&x.ID, &x.WorkflowID, &x.Version, &x.Status, &input, &x.Error, &x.CreatedAt, &x.UpdatedAt)
	_ = json.Unmarshal([]byte(input), &x.Input)
	return x, e
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
		rows, e := s.db.Query(`SELECT node_id,status,input,output,error,branch,attempt,created_at FROM steps WHERE run_id=$1 ORDER BY id`, x.ID)
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		defer rows.Close()
		steps := []Step{}
		for rows.Next() {
			var t Step
			var in, out string
			if rows.Scan(&t.NodeID, &t.Status, &in, &out, &t.Error, &t.Branch, &t.Attempt, &t.CreatedAt) == nil {
				_ = json.Unmarshal([]byte(in), &t.Input)
				_ = json.Unmarshal([]byte(out), &t.Output)
				steps = append(steps, t)
			}
		}
		write(w, 200, map[string]any{"run": x, "steps": steps})
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
