package main

import (
	"encoding/json"
	"net/http"
)

var catalog = []map[string]any{
	{"type": "manual_trigger", "name": "Manual Trigger", "category": "Trigger", "description": "Start a workflow manually.", "config_schema": map[string]any{"type": "object", "properties": map[string]any{}}, "input_ports": []string{}, "output_ports": []string{"out"}},
	{"type": "webhook_trigger", "name": "Webhook Trigger", "category": "Trigger", "description": "Start on an authenticated HTTP request.", "config_schema": map[string]any{"type": "object", "required": []string{"secret"}, "properties": map[string]any{"secret": map[string]string{"type": "string"}}}, "input_ports": []string{}, "output_ports": []string{"out"}},
	{"type": "schedule_trigger", "name": "Schedule Trigger", "category": "Trigger", "description": "Run at a fixed interval in UTC.", "config_schema": map[string]any{"type": "object", "required": []string{"interval_seconds"}, "properties": map[string]any{"interval_seconds": map[string]any{"type": "integer", "minimum": 10}, "timezone": map[string]any{"type": "string", "enum": []string{"UTC"}}}}, "input_ports": []string{}, "output_ports": []string{"out"}},
	{"type": "email_trigger", "name": "Gmail Trigger", "category": "Trigger", "description": "Poll Gmail for new messages.", "config_schema": map[string]any{"type": "object", "properties": map[string]any{"poll_seconds": map[string]any{"type": "integer", "minimum": 30}}}, "input_ports": []string{}, "output_ports": []string{"out"}},
	{"type": "set_fields", "name": "Set Fields", "category": "Transform", "description": "Merge configured fields into input.", "config_schema": map[string]any{"type": "object", "required": []string{"fields"}, "properties": map[string]any{"fields": map[string]string{"type": "object"}}}, "input_ports": []string{"in"}, "output_ports": []string{"out"}},
	{"type": "http_request", "name": "HTTP Request", "category": "Action", "description": "Send an HTTP request.", "config_schema": map[string]any{"type": "object", "required": []string{"url", "method"}, "properties": map[string]any{"url": map[string]string{"type": "string"}, "method": map[string]any{"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"}}, "headers": map[string]string{"type": "object"}, "body": map[string]any{}}}, "input_ports": []string{"in"}, "output_ports": []string{"out"}},
	{"type": "condition", "name": "Condition", "category": "Logic", "description": "Route by a comparison.", "config_schema": map[string]any{"type": "object", "required": []string{"left", "operator"}, "properties": map[string]any{"left": map[string]any{}, "right": map[string]any{}, "operator": map[string]any{"type": "string", "enum": []string{"equals", "contains", "greater_than", "exists"}}}}, "input_ports": []string{"in"}, "output_ports": []string{"true", "false"}},
	{"type": "send_email", "name": "Send Email", "category": "Action", "description": "Send an email through Gmail.", "config_schema": map[string]any{"type": "object", "required": []string{"to", "subject", "body"}, "properties": map[string]any{"to": map[string]string{"type": "string"}, "subject": map[string]string{"type": "string"}, "body": map[string]string{"type": "string"}}}, "input_ports": []string{"in"}, "output_ports": []string{"out"}},
}

func (s *Server) hook(w http.ResponseWriter, r *http.Request, wid string) {
	var version int
	var graphStr string
	e := s.db.QueryRow(`SELECT w.published_version,v.graph FROM workflows w JOIN versions v ON v.workflow_id=w.id AND v.version=w.published_version WHERE w.id=? AND w.active=1`, wid).Scan(&version, &graphStr)
	if e != nil {
		fail(w, 404, "webhook not active")
		return
	}
	var g Graph
	_ = json.Unmarshal([]byte(graphStr), &g)
	secret := ""
	found := false
	for _, n := range g.Nodes {
		if n.Type == "webhook_trigger" {
			secret, _ = n.Config["secret"].(string)
			found = true
			break
		}
	}
	if !found || secret == "" || !equalSecret(r.Header.Get("X-Webhook-Secret"), secret) {
		fail(w, 403, "invalid webhook secret")
		return
	}
	var input map[string]any
	if e := decode(r, &input); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if input == nil {
		input = map[string]any{}
	}
	event := r.Header.Get("X-Event-ID")
	if event == "" {
		event = id()
	}
	if len(event) > 500 {
		fail(w, 400, "event ID too long")
		return
	}
	tx, e := s.db.Begin()
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	defer tx.Rollback()
	var currentVersion int
	if e = tx.QueryRow(`SELECT published_version FROM workflows WHERE id=? AND active=1`, wid).Scan(&currentVersion); e != nil || currentVersion != version {
		fail(w, 409, "webhook version is no longer active")
		return
	}
	var existing string
	e = tx.QueryRow(`SELECT run_id FROM trigger_events WHERE workflow_id=? AND version=? AND event_id=?`, wid, version, event).Scan(&existing)
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
	_, e = tx.Exec(`INSERT INTO runs(id,workflow_id,version,status,input,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, rid, wid, version, "queued", jsonText(input), t, t)
	if e == nil {
		_, e = tx.Exec(`INSERT INTO trigger_events VALUES(?,?,?,?)`, wid, version, event, rid)
	}
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, "database error")
		return
	}
	write(w, 200, map[string]any{"run": Run{ID: rid, WorkflowID: wid, Version: version, Status: "queued", Input: input, CreatedAt: t, UpdatedAt: t}, "duplicate": false})
}
