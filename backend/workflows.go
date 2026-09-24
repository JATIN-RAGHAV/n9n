package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

var emptyGraph = Graph{Nodes: []Node{}, Edges: []Edge{}}
var nodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var mappingPattern = regexp.MustCompile(`\{\{\s*(input|nodes)\.([A-Za-z0-9_-]+)(?:\.([A-Za-z0-9_.-]+))?\s*\}\}`)

func loadWorkflow(db *sql.DB, id, uid string) (Workflow, error) {
	var w Workflow
	var draft string
	var version sql.NullInt64
	var active int
	err := db.QueryRow(`SELECT id,name,draft,published_version,active,created_at,updated_at FROM workflows WHERE id=? AND user_id=?`, id, uid).Scan(&w.ID, &w.Name, &draft, &version, &active, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return w, err
	}
	_ = json.Unmarshal([]byte(draft), &w.Draft)
	if w.Draft.Nodes == nil {
		w.Draft.Nodes = []Node{}
	}
	if w.Draft.Edges == nil {
		w.Draft.Edges = []Edge{}
	}
	if version.Valid {
		v := int(version.Int64)
		w.PublishedVersion = &v
	}
	w.Active = active != 0
	return w, nil
}
func (s *Server) workflows(w http.ResponseWriter, r *http.Request, uid string, p []string) {
	if len(p) == 0 {
		switch r.Method {
		case "GET":
			rows, e := s.db.Query(`SELECT id FROM workflows WHERE user_id=? ORDER BY updated_at DESC`, uid)
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
			out := []Workflow{}
			for _, id := range ids {
				x, e := loadWorkflow(s.db, id, uid)
				if e == nil {
					out = append(out, x)
				}
			}
			write(w, 200, map[string]any{"workflows": out})
			return
		case "POST":
			var a struct {
				Name  string `json:"name"`
				Draft *Graph `json:"draft"`
			}
			if e := decode(r, &a); e != nil {
				fail(w, 400, e.Error())
				return
			}
			a.Name = strings.TrimSpace(a.Name)
			if a.Name == "" {
				a.Name = "Untitled workflow"
			}
			if len(a.Name) > 200 {
				fail(w, 400, "name too long")
				return
			}
			g := emptyGraph
			if a.Draft != nil {
				g = *a.Draft
			}
			if e := validateDraft(g); e != nil {
				fail(w, 400, e.Error())
				return
			}
			id := id()
			t := now()
			_, e := s.db.Exec(`INSERT INTO workflows(id,user_id,name,draft,created_at,updated_at) VALUES(?,?,?,?,?,?)`, id, uid, a.Name, jsonText(g), t, t)
			if e != nil {
				fail(w, 500, "database error")
				return
			}
			x, _ := loadWorkflow(s.db, id, uid)
			write(w, 200, map[string]any{"workflow": x})
			return
		}
	}
	if len(p) == 0 {
		fail(w, 404, "not found")
		return
	}
	x, e := loadWorkflow(s.db, p[0], uid)
	if e != nil {
		fail(w, 404, "workflow not found")
		return
	}
	if len(p) == 1 {
		switch r.Method {
		case "GET":
			write(w, 200, map[string]any{"workflow": x})
			return
		case "PUT":
			var a struct {
				Name  string `json:"name"`
				Draft Graph  `json:"draft"`
			}
			if e := decode(r, &a); e != nil {
				fail(w, 400, e.Error())
				return
			}
			a.Name = strings.TrimSpace(a.Name)
			if a.Name == "" || len(a.Name) > 200 {
				fail(w, 400, "invalid name")
				return
			}
			if e := validateDraft(a.Draft); e != nil {
				fail(w, 400, e.Error())
				return
			}
			_, e := s.db.Exec(`UPDATE workflows SET name=?,draft=?,updated_at=? WHERE id=?`, a.Name, jsonText(a.Draft), now(), x.ID)
			if e != nil {
				fail(w, 500, "database error")
				return
			}
			x, _ = loadWorkflow(s.db, x.ID, uid)
			write(w, 200, map[string]any{"workflow": x})
			return
		case "DELETE":
			_, e := s.db.Exec(`DELETE FROM workflows WHERE id=?`, x.ID)
			if e != nil {
				fail(w, 500, "database error")
				return
			}
			write(w, 200, map[string]bool{"ok": true})
			return
		}
	}
	if len(p) == 2 && r.Method == "POST" {
		switch p[1] {
		case "publish":
			if e := validatePublish(x.Draft); e != nil {
				fail(w, 400, e.Error())
				return
			}
			for _, n := range x.Draft.Nodes {
				if n.CredentialID != "" {
					var count int
					_ = s.db.QueryRow(`SELECT count(*) FROM credentials WHERE id=? AND user_id=?`, n.CredentialID, uid).Scan(&count)
					if count == 0 {
						fail(w, 400, "credential not found: "+n.CredentialID)
						return
					}
				}
			}
			tx, e := s.db.Begin()
			if e != nil {
				fail(w, 500, "database error")
				return
			}
			defer tx.Rollback()
			v := 1
			if x.PublishedVersion != nil {
				v = *x.PublishedVersion + 1
			}
			if _, e = tx.Exec(`INSERT INTO versions VALUES(?,?,?)`, x.ID, v, jsonText(x.Draft)); e == nil {
				_, e = tx.Exec(`UPDATE workflows SET published_version=?,updated_at=? WHERE id=?`, v, now(), x.ID)
			}
			if e != nil {
				fail(w, 500, "database error")
				return
			}
			if e = tx.Commit(); e != nil {
				fail(w, 500, "database error")
				return
			}
			x, _ = loadWorkflow(s.db, x.ID, uid)
			write(w, 200, map[string]any{"workflow": x})
			return
		case "activate":
			var a struct {
				Active bool `json:"active"`
			}
			if e := decode(r, &a); e != nil {
				fail(w, 400, e.Error())
				return
			}
			if a.Active && x.PublishedVersion == nil {
				fail(w, 400, "publish before activation")
				return
			}
			_, e := s.db.Exec(`UPDATE workflows SET active=?,updated_at=? WHERE id=?`, a.Active, now(), x.ID)
			if e != nil {
				fail(w, 500, "database error")
				return
			}
			x, _ = loadWorkflow(s.db, x.ID, uid)
			write(w, 200, map[string]any{"workflow": x})
			return
		case "run":
			if x.PublishedVersion == nil {
				fail(w, 400, "publish before running")
				return
			}
			var a struct {
				Input map[string]any `json:"input"`
			}
			if e := decode(r, &a); e != nil {
				fail(w, 400, e.Error())
				return
			}
			if a.Input == nil {
				a.Input = map[string]any{}
			}
			run, e := queueRun(s.db, x.ID, *x.PublishedVersion, a.Input)
			if e != nil {
				fail(w, 500, "database error")
				return
			}
			write(w, 200, map[string]any{"run": run})
			return
		}
	}
	fail(w, 404, "not found")
}
func validateDraft(g Graph) error {
	if len(g.Nodes) > 100 || len(g.Edges) > 300 {
		return errors.New("graph too large")
	}
	ids := map[string]bool{}
	for _, n := range g.Nodes {
		if n.ID == "" || len(n.ID) > 100 || !nodeIDPattern.MatchString(n.ID) || ids[n.ID] {
			return errors.New("invalid or duplicate node ID")
		}
		ids[n.ID] = true
		if !nodeTypes[n.Type] {
			return fmt.Errorf("unknown node type: %s", n.Type)
		}
		if n.Config == nil {
			return fmt.Errorf("node %s requires config", n.ID)
		}
	}
	edges := map[string]bool{}
	for _, e := range g.Edges {
		if e.ID == "" || edges[e.ID] || !ids[e.Source] || !ids[e.Target] {
			return errors.New("invalid edge")
		}
		edges[e.ID] = true
		if e.SourcePort != "out" && e.SourcePort != "true" && e.SourcePort != "false" {
			return errors.New("invalid source port")
		}
	}
	return nil
}
func validatePublish(g Graph) error {
	if e := validateDraft(g); e != nil {
		return e
	}
	nodes := map[string]Node{}
	indeg := map[string]int{}
	out := map[string][]string{}
	triggers := 0
	root := ""
	for _, n := range g.Nodes {
		nodes[n.ID] = n
		indeg[n.ID] = 0
		if triggerTypes[n.Type] {
			triggers++
			root = n.ID
		}
		switch n.Type {
		case "schedule_trigger":
			if val, ok := number(n.Config["interval_seconds"]); !ok || val < 10 || val != float64(int(val)) {
				return errors.New("schedule interval_seconds must be integer >= 10")
			}
			if tz, ok := n.Config["timezone"].(string); ok && tz != "UTC" {
				return errors.New("only UTC schedules supported")
			}
		case "webhook_trigger":
			if str, _ := n.Config["secret"].(string); str == "" {
				return errors.New("webhook secret required")
			}
		case "email_trigger":
			if n.CredentialID == "" {
				return errors.New("email node requires credential")
			}
			if val, exists := n.Config["poll_seconds"]; exists {
				p, ok := number(val)
				if !ok || p < 30 || p != float64(int(p)) {
					return errors.New("poll_seconds must be integer >= 30")
				}
			}
		case "send_email":
			if n.CredentialID == "" {
				return errors.New("email node requires credential")
			}
			for _, key := range []string{"to", "subject", "body"} {
				if _, ok := n.Config[key].(string); !ok {
					return fmt.Errorf("send_email requires %s", key)
				}
			}
		case "set_fields":
			if _, ok := n.Config["fields"].(map[string]any); !ok {
				return errors.New("set_fields requires fields object")
			}
		case "http_request":
			u, ok := n.Config["url"].(string)
			if !ok || u == "" {
				return errors.New("http_request requires url")
			}
			m, _ := n.Config["method"].(string)
			if m == "" {
				return errors.New("http_request requires method")
			}
			switch m {
			case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
			default:
				return errors.New("unsupported HTTP method")
			}
			if value, present := n.Config["idempotency_key"]; present {
				key, ok := value.(string)
				if !ok || strings.TrimSpace(key) == "" {
					return errors.New("idempotency_key must be a nonempty string")
				}
			}
		case "condition":
			op, _ := n.Config["operator"].(string)
			if _, ok := n.Config["left"]; !ok {
				return errors.New("condition requires left")
			}
			if op != "exists" {
				if _, ok := n.Config["right"]; !ok {
					return errors.New("condition requires right")
				}
			}
			switch op {
			case "equals", "contains", "greater_than", "exists":
			default:
				return errors.New("invalid condition operator")
			}
		}
	}
	if triggers != 1 {
		return errors.New("workflow must have exactly one trigger")
	}
	for _, e := range g.Edges {
		if triggerTypes[nodes[e.Target].Type] {
			return errors.New("trigger cannot have incoming edge")
		}
		if nodes[e.Source].Type == "condition" {
			if e.SourcePort != "true" && e.SourcePort != "false" {
				return errors.New("condition requires true or false port")
			}
		} else if e.SourcePort != "out" {
			return errors.New("non-condition requires out port")
		}
		indeg[e.Target]++
		if indeg[e.Target] > 1 {
			return errors.New("nodes may have at most one incoming edge")
		}
		out[e.Source] = append(out[e.Source], e.Target)
	}
	seen := map[string]bool{}
	var visit func(string) error
	visit = func(n string) error {
		if seen[n] {
			return errors.New("cycle detected")
		}
		seen[n] = true
		for _, t := range out[n] {
			if e := visit(t); e != nil {
				return e
			}
		}
		return nil
	}
	if e := visit(root); e != nil {
		return e
	}
	if len(seen) != len(nodes) {
		return errors.New("all nodes must be reachable from trigger")
	}
	for _, n := range g.Nodes {
		ancestors := map[string]bool{}
		var collect func(string)
		collect = func(target string) {
			for _, e := range g.Edges {
				if e.Target == target && !ancestors[e.Source] {
					ancestors[e.Source] = true
					collect(e.Source)
				}
			}
		}
		collect(n.ID)
		if e := validateMappings(n.Config, ancestors); e != nil {
			return fmt.Errorf("node %s: %w", n.ID, e)
		}
	}
	return nil
}
func validateMappings(value any, ancestors map[string]bool) error {
	switch v := value.(type) {
	case string:
		matches := mappingPattern.FindAllStringSubmatchIndex(v, -1)
		clean := mappingPattern.ReplaceAllString(v, "")
		if strings.Contains(clean, "{{") || strings.Contains(clean, "}}") {
			return errors.New("invalid mapping expression")
		}
		for _, match := range matches {
			if v[match[2]:match[3]] == "nodes" && !ancestors[v[match[4]:match[5]]] {
				return fmt.Errorf("mapping references a node that is not upstream: %s", v[match[4]:match[5]])
			}
		}
	case map[string]any:
		for _, item := range v {
			if err := validateMappings(item, ancestors); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := validateMappings(item, ancestors); err != nil {
				return err
			}
		}
	}
	return nil
}
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	default:
		return 0, false
	}
}
