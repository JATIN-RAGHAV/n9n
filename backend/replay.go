package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

// descendants returns every node downstream of from, which a replay re-runs.
func descendants(g Graph, from string) map[string]bool {
	out := map[string][]string{}
	for _, e := range g.Edges {
		out[e.Source] = append(out[e.Source], e.Target)
	}
	seen := map[string]bool{}
	stack := []string{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, t := range out[n] {
			if !seen[t] {
				seen[t] = true
				stack = append(stack, t)
			}
		}
	}
	return seen
}

// feederOf returns the node whose output is the input of node, and the port
// that connects them. Published graphs allow one incoming edge per node.
func feederOf(g Graph, node string) (string, string) {
	for _, e := range g.Edges {
		if e.Target == node {
			return e.Source, e.SourcePort
		}
	}
	return "", ""
}

func queueReplayRun(tx *sql.Tx, parent Run, version int, graph Graph, fromNode string, input any) (Run, error) {
	id := id()
	t := now()
	_, err := tx.Exec(`INSERT INTO runs(id,workflow_id,version,status,input,graph_snapshot,kind,parent_run_id,resume_from_node,created_at,updated_at) VALUES($1,$2,$3,'queued',$4,$5,'replay',$6,$7,$8,$8)`, id, parent.WorkflowID, version, jsonText(input), jsonText(graph), parent.ID, fromNode, t)
	if err != nil {
		return Run{}, err
	}
	pid := parent.ID
	return Run{ID: id, WorkflowID: parent.WorkflowID, Version: version, Kind: "replay", ParentRunID: &pid, ResumeFromNode: fromNode, Status: "queued", Input: input, CreatedAt: t, UpdatedAt: t}, nil
}

// replay queues a new run that starts at from_node. Nodes that are not
// downstream of it keep the parent's recorded outputs, so the runner's resume
// path skips them; an input override replaces what from_node receives.
func (s *Server) replay(w http.ResponseWriter, r *http.Request, uid string, parent Run) {
	var a struct {
		FromNode      string         `json:"from_node"`
		InputOverride map[string]any `json:"input_override"`
		Graph         string         `json:"graph"`
	}
	if e := decode(r, &a); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if a.Graph == "" {
		a.Graph = "original"
	}
	if a.Graph != "original" && a.Graph != "draft" {
		fail(w, 400, "graph must be original or draft")
		return
	}
	if a.FromNode == "" {
		fail(w, 400, "from_node required")
		return
	}
	if a.InputOverride != nil && !withinJSONDepth(a.InputOverride) {
		fail(w, 400, "input_override exceeds nesting limit of 32")
		return
	}
	switch parent.Status {
	case "succeeded", "failed", "cancelled", "uncertain":
	default:
		fail(w, 409, "run is still in progress")
		return
	}
	tx, e := s.db.Begin()
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	defer tx.Rollback()
	var graph Graph
	version := parent.Version
	if a.Graph == "draft" {
		// Replay against the current draft: validated like Test draft and
		// snapshotted, so the workflow stays unpublished.
		var herr *httpError
		if graph, version, herr = validatedDraft(tx, parent.WorkflowID, uid); herr != nil {
			fail(w, herr.status, herr.msg)
			return
		}
	} else {
		var graphText string
		if e = tx.QueryRow(`SELECT COALESCE(r.graph_snapshot,v.graph) FROM runs r LEFT JOIN versions v ON v.workflow_id=r.workflow_id AND v.version=r.version WHERE r.id=$1`, parent.ID).Scan(&graphText); e != nil {
			fail(w, 500, "workflow snapshot unavailable")
			return
		}
		if e = json.Unmarshal([]byte(graphText), &graph); e != nil {
			fail(w, 500, "invalid workflow snapshot")
			return
		}
	}
	fromType := ""
	for _, n := range graph.Nodes {
		if n.ID == a.FromNode {
			fromType = n.Type
		}
	}
	if fromType == "" {
		fail(w, 400, "from_node is not in the workflow")
		return
	}
	if herr := checkCredentials(tx, graph, uid); herr != nil {
		fail(w, herr.status, herr.msg)
		return
	}
	input := parent.Input
	if a.InputOverride != nil && triggerTypes[fromType] {
		input = a.InputOverride
	}
	run, e := queueReplayRun(tx, parent, version, graph, a.FromNode, input)
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	rerun := descendants(graph, a.FromNode)
	rerun[a.FromNode] = true
	present := map[string]bool{}
	for _, n := range graph.Nodes {
		present[n.ID] = true
	}
	type copied struct{ input, output, branch string }
	latest := map[string]copied{}
	order := []string{}
	rows, e := tx.Query(`SELECT node_id,input,output,branch FROM steps WHERE run_id=$1 AND status='succeeded' ORDER BY id`, parent.ID)
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	for rows.Next() {
		var nodeID string
		var c copied
		if rows.Scan(&nodeID, &c.input, &c.output, &c.branch) != nil || rerun[nodeID] || !present[nodeID] {
			continue
		}
		if _, seen := latest[nodeID]; !seen {
			order = append(order, nodeID)
		}
		latest[nodeID] = c
	}
	rows.Close()
	if feeder, port := feederOf(graph, a.FromNode); a.InputOverride != nil && feeder != "" {
		c, seen := latest[feeder]
		if !seen {
			c = copied{input: "{}"}
			order = append(order, feeder)
		}
		c.output = jsonText(a.InputOverride)
		if port == "true" || port == "false" {
			c.branch = port
		}
		latest[feeder] = c
	}
	t := now()
	for _, nodeID := range order {
		c := latest[nodeID]
		if _, e = tx.Exec(`INSERT INTO steps(run_id,node_id,status,input,output,error,branch,attempt,finished_at,created_at) VALUES($1,$2,'succeeded',$3,$4,'',$5,1,$6,$6)`, run.ID, nodeID, c.input, c.output, c.branch, t); e != nil {
			fail(w, 500, "database error")
			return
		}
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, "database error")
		return
	}
	write(w, 200, map[string]any{"run": run})
}
