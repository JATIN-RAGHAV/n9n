package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) internal(w http.ResponseWriter, r *http.Request, p []string) {
	auth := r.Header.Get("Authorization")
	if !equalSecret(auth, "Bearer "+s.runnerToken) {
		fail(w, 401, "runner authentication required")
		return
	}
	if len(p) == 0 {
		fail(w, 404, "not found")
		return
	}
	switch p[0] {
	case "jobs":
		s.internalJobs(w, r, p[1:])
	case "triggers":
		s.internalTriggers(w, r, p[1:])
	default:
		fail(w, 404, "not found")
	}
}
func (s *Server) internalJobs(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 1 && p[0] == "claim" && r.Method == "POST" {
		var a struct {
			RunnerID string `json:"runner_id"`
		}
		if e := decode(r, &a); e != nil || a.RunnerID == "" {
			fail(w, 400, "runner_id required")
			return
		}
		job, e := s.claim(a.RunnerID)
		if e != nil {
			fail(w, 500, "claim error")
			return
		}
		write(w, 200, map[string]any{"job": job})
		return
	}
	if len(p) < 2 {
		fail(w, 404, "not found")
		return
	}
	runID := p[0]
	if len(p) == 3 && p[1] == "credentials" && r.Method == "GET" {
		s.credentialForJob(w, runID, p[2], r.Header.Get("X-Lease-Token"))
		return
	}
	if r.Method != "POST" || len(p) != 2 {
		fail(w, 404, "not found")
		return
	}
	switch p[1] {
	case "heartbeat":
		var a struct {
			LeaseToken string `json:"lease_token"`
		}
		if e := decode(r, &a); e != nil {
			fail(w, 400, e.Error())
			return
		}
		until := time.Now().UTC().Add(60 * time.Second).Format(timeLayout)
		res, e := s.db.Exec(`UPDATE runs SET lease_until=?,updated_at=? WHERE id=? AND status='running' AND lease_token=? AND lease_until>?`, until, now(), runID, a.LeaseToken, now())
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fail(w, 409, "lease expired")
			return
		}
		write(w, 200, map[string]bool{"ok": true})
		return
	case "steps":
		var a struct {
			LeaseToken string `json:"lease_token"`
			NodeID     string `json:"node_id"`
			Status     string `json:"status"`
			Input      any    `json:"input"`
			Output     any    `json:"output"`
			Error      string `json:"error"`
			Branch     string `json:"branch"`
			Attempt    int    `json:"attempt"`
		}
		if e := decodeLimit(r, &a, 4<<20); e != nil {
			fail(w, 400, e.Error())
			return
		}
		if a.NodeID == "" || (a.Status != "running" && a.Status != "succeeded" && a.Status != "failed" && a.Status != "skipped") || a.Attempt < 0 || a.Attempt > 100 {
			fail(w, 400, "invalid step")
			return
		}
		if a.Attempt == 0 {
			a.Attempt = 1
		}
		if a.Branch != "" && a.Branch != "true" && a.Branch != "false" {
			fail(w, 400, "invalid branch")
			return
		}
		tx, e := s.db.Begin()
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		defer tx.Rollback()
		var graphStr string
		e = tx.QueryRow(`SELECT v.graph FROM runs r JOIN versions v ON v.workflow_id=r.workflow_id AND v.version=r.version WHERE r.id=? AND r.status='running' AND r.lease_token=? AND r.lease_until>?`, runID, a.LeaseToken, now()).Scan(&graphStr)
		if e != nil {
			fail(w, 409, "lease expired")
			return
		}
		var graph Graph
		_ = json.Unmarshal([]byte(graphStr), &graph)
		found := false
		for _, n := range graph.Nodes {
			if n.ID == a.NodeID {
				found = true
				break
			}
		}
		if !found {
			fail(w, 400, "node not in workflow")
			return
		}
		_, e = tx.Exec(`INSERT INTO steps(run_id,node_id,status,input,output,error,branch,attempt,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, runID, a.NodeID, a.Status, jsonText(a.Input), jsonText(a.Output), a.Error, a.Branch, a.Attempt, now())
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		if e = tx.Commit(); e != nil {
			fail(w, 500, "database error")
			return
		}
		write(w, 200, map[string]bool{"ok": true})
		return
	case "complete":
		var a struct {
			LeaseToken string `json:"lease_token"`
			Status     string `json:"status"`
			Error      string `json:"error"`
		}
		if e := decode(r, &a); e != nil {
			fail(w, 400, e.Error())
			return
		}
		if a.Status != "succeeded" && a.Status != "failed" && a.Status != "uncertain" {
			fail(w, 400, "invalid status")
			return
		}
		res, e := s.db.Exec(`UPDATE runs SET status=?,error=?,lease_token=NULL,lease_until=NULL,updated_at=? WHERE id=? AND status='running' AND lease_token=? AND lease_until>?`, a.Status, a.Error, now(), runID, a.LeaseToken, now())
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fail(w, 409, "lease expired")
			return
		}
		write(w, 200, map[string]bool{"ok": true})
		return
	}
	fail(w, 404, "not found")
}
func (s *Server) claim(runner string) (any, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	rows, e := tx.Query(`SELECT id FROM runs WHERE status='running' AND lease_until<?`, now())
	if e != nil {
		return nil, e
	}
	expired := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			expired = append(expired, id)
		}
	}
	rows.Close()
	for _, rid := range expired {
		var graphStr string
		e = tx.QueryRow(`SELECT v.graph FROM runs r JOIN versions v ON v.workflow_id=r.workflow_id AND v.version=r.version WHERE r.id=?`, rid).Scan(&graphStr)
		if e != nil {
			return nil, e
		}
		var graph Graph
		_ = json.Unmarshal([]byte(graphStr), &graph)
		latest := map[string]string{}
		stepRows, e := tx.Query(`SELECT node_id,status FROM steps WHERE run_id=? ORDER BY id`, rid)
		if e != nil {
			return nil, e
		}
		for stepRows.Next() {
			var nodeID, status string
			if stepRows.Scan(&nodeID, &status) == nil {
				latest[nodeID] = status
			}
		}
		stepRows.Close()
		unsafe := false
		for _, n := range graph.Nodes {
			if latest[n.ID] != "running" && latest[n.ID] != "failed" {
				continue
			}
			if n.Type == "send_email" {
				unsafe = true
			}
			if n.Type == "http_request" {
				m, _ := n.Config["method"].(string)
				if m == "" {
					m = "GET"
				}
				key, _ := n.Config["idempotency_key"].(string)
				idempotent := key != ""
				if !(strings.EqualFold(m, "GET") || strings.EqualFold(m, "HEAD") || idempotent) {
					unsafe = true
				}
			}
		}
		status := "queued"
		reason := ""
		if unsafe {
			status = "uncertain"
			reason = "lease expired during non-idempotent action"
		}
		if _, e = tx.Exec(`UPDATE runs SET status=?,error=?,lease_token=NULL,lease_until=NULL,updated_at=? WHERE id=?`, status, reason, now(), rid); e != nil {
			return nil, e
		}
	}
	var rid, wid, graphStr, inputStr string
	var version int
	e = tx.QueryRow(`SELECT r.id,r.workflow_id,r.version,v.graph,r.input FROM runs r JOIN versions v ON v.workflow_id=r.workflow_id AND v.version=r.version WHERE r.status='queued' ORDER BY r.created_at LIMIT 1`).Scan(&rid, &wid, &version, &graphStr, &inputStr)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if e != nil {
		return nil, e
	}
	lease := id() + id()
	until := time.Now().UTC().Add(60 * time.Second).Format(timeLayout)
	_, e = tx.Exec(`UPDATE runs SET status='running',lease_token=?,lease_until=?,runner_id=?,updated_at=? WHERE id=?`, lease, until, runner, now(), rid)
	if e != nil {
		return nil, e
	}
	rows, e = tx.Query(`SELECT node_id,status,input,output,error,branch,attempt,created_at FROM steps WHERE run_id=? ORDER BY id`, rid)
	if e != nil {
		return nil, e
	}
	steps := []Step{}
	for rows.Next() {
		var x Step
		var in, out string
		if rows.Scan(&x.NodeID, &x.Status, &in, &out, &x.Error, &x.Branch, &x.Attempt, &x.CreatedAt) == nil {
			_ = json.Unmarshal([]byte(in), &x.Input)
			_ = json.Unmarshal([]byte(out), &x.Output)
			steps = append(steps, x)
		}
	}
	rows.Close()
	var graph Graph
	var input any
	_ = json.Unmarshal([]byte(graphStr), &graph)
	_ = json.Unmarshal([]byte(inputStr), &input)
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return map[string]any{"id": rid, "lease_token": lease, "workflow_id": wid, "version": version, "graph": graph, "input": input, "steps": steps}, nil
}
func (s *Server) internalTriggers(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 0 && r.Method == "GET" {
		rows, e := s.db.Query(`SELECT w.id,w.published_version,v.graph FROM workflows w JOIN versions v ON v.workflow_id=w.id AND v.version=w.published_version WHERE w.active=1`)
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		type tr struct {
			ID      string
			Version int
			Graph   string
		}
		items := []tr{}
		for rows.Next() {
			var t tr
			if rows.Scan(&t.ID, &t.Version, &t.Graph) == nil {
				items = append(items, t)
			}
		}
		rows.Close()
		out := []any{}
		for _, t := range items {
			var g Graph
			_ = json.Unmarshal([]byte(t.Graph), &g)
			var cp string
			_ = s.db.QueryRow(`SELECT value FROM checkpoints WHERE workflow_id=? AND version=?`, t.ID, t.Version).Scan(&cp)
			var checkpoint any
			if cp != "" {
				_ = json.Unmarshal([]byte(cp), &checkpoint)
			}
			for _, n := range g.Nodes {
				if triggerTypes[n.Type] {
					out = append(out, map[string]any{"workflow_id": t.ID, "version": t.Version, "node": n, "checkpoint": checkpoint})
					break
				}
			}
		}
		write(w, 200, map[string]any{"triggers": out})
		return
	}
	if len(p) == 3 && p[1] == "credentials" && r.Method == "GET" {
		v, e := strconv.Atoi(r.URL.Query().Get("version"))
		if e != nil {
			fail(w, 400, "version required")
			return
		}
		s.credentialForTrigger(w, p[0], p[2], v)
		return
	}
	if len(p) != 2 || r.Method != "POST" {
		fail(w, 404, "not found")
		return
	}
	wid := p[0]
	var a struct {
		Version    int            `json:"version"`
		EventID    string         `json:"event_id"`
		Input      map[string]any `json:"input"`
		Checkpoint any            `json:"checkpoint"`
	}
	if e := decode(r, &a); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if a.Version <= 0 {
		fail(w, 400, "version required")
		return
	}
	var current int
	e := s.db.QueryRow(`SELECT published_version FROM workflows WHERE id=? AND active=1`, wid).Scan(&current)
	if e != nil || current != a.Version {
		fail(w, 409, "inactive or stale trigger")
		return
	}
	if p[1] == "checkpoint" {
		_, e := s.db.Exec(`INSERT INTO checkpoints VALUES(?,?,?) ON CONFLICT(workflow_id,version) DO UPDATE SET value=excluded.value`, wid, a.Version, jsonText(a.Checkpoint))
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		write(w, 200, map[string]bool{"ok": true})
		return
	}
	if p[1] != "events" {
		fail(w, 404, "not found")
		return
	}
	if a.EventID == "" || len(a.EventID) > 500 {
		fail(w, 400, "event_id required")
		return
	}
	if a.Input == nil {
		a.Input = map[string]any{}
	}
	tx, e := s.db.Begin()
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	defer tx.Rollback()
	e = tx.QueryRow(`SELECT published_version FROM workflows WHERE id=? AND active=1`, wid).Scan(&current)
	if e != nil || current != a.Version {
		fail(w, 409, "inactive or stale trigger")
		return
	}
	var existing string
	e = tx.QueryRow(`SELECT run_id FROM trigger_events WHERE workflow_id=? AND version=? AND event_id=?`, wid, a.Version, a.EventID).Scan(&existing)
	if e == nil {
		x, runErr := getRun(tx, existing)
		if runErr != nil {
			write(w, 200, map[string]any{"run": nil, "duplicate": true})
		} else {
			write(w, 200, map[string]any{"run": x, "duplicate": true})
		}
		return
	}
	rid := id()
	t := now()
	_, e = tx.Exec(`INSERT INTO runs(id,workflow_id,version,status,input,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, rid, wid, a.Version, "queued", jsonText(a.Input), t, t)
	if e == nil {
		_, e = tx.Exec(`INSERT INTO trigger_events VALUES(?,?,?,?)`, wid, a.Version, a.EventID, rid)
	}
	if e == nil && a.Checkpoint != nil {
		_, e = tx.Exec(`INSERT INTO checkpoints VALUES(?,?,?) ON CONFLICT(workflow_id,version) DO UPDATE SET value=excluded.value`, wid, a.Version, jsonText(a.Checkpoint))
	}
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, "database error")
		return
	}
	write(w, 200, map[string]any{"run": Run{ID: rid, WorkflowID: wid, Version: a.Version, Status: "queued", Input: a.Input, CreatedAt: t, UpdatedAt: t}, "duplicate": false})
}
