package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

// recorded is a parent step's data a replay can reuse.
type recorded struct{ input, output, branch string }

// copiedStep is a parent step a replay reuses, with the branch its node takes.
type copiedStep struct{ node, branch string }

// replayPlan walks the graph the way the runner will, from the trigger until
// it reaches from, and returns the nodes whose steps the replay reuses.
// Conditions on the path to from are forced onto that path so a node the
// parent skipped can be replayed; other conditions keep their recorded
// branch, so sibling branches stay skipped. A reused node that never
// succeeded in the parent cannot be copied, except the feeder of from when an
// input override seeds its output.
func replayPlan(g Graph, from string, succeeded map[string]recorded, seeded bool) ([]copiedStep, *httpError) {
	nodes := map[string]Node{}
	root := ""
	for _, n := range g.Nodes {
		nodes[n.ID] = n
		if triggerTypes[n.Type] {
			root = n.ID
		}
	}
	if root == "" {
		return nil, &httpError{400, "workflow has no trigger"}
	}
	children := map[string][]Edge{}
	for _, e := range g.Edges {
		children[e.Source] = append(children[e.Source], e)
	}
	// The port each node on the path leaves through to reach from.
	pathPort := map[string]string{}
	feeder, port := feederOf(g, from)
	for n := feeder; n != ""; n, port = feederOf(g, n) {
		pathPort[n] = port
	}
	plan := []copiedStep{}
	visited := map[string]bool{}
	stack := []string{root}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == from || visited[id] {
			continue
		}
		visited[id] = true
		rec, done := succeeded[id]
		if !done && !(seeded && id == feeder) {
			return nil, &httpError{400, "node " + id + " did not complete in the parent run; replay from it or earlier"}
		}
		node := nodes[id]
		branch := rec.branch
		if forced, onPath := pathPort[id]; onPath && node.Type == "condition" {
			branch = forced
		}
		plan = append(plan, copiedStep{id, branch})
		for _, e := range children[id] {
			if node.Type == "condition" && e.SourcePort != branch {
				continue
			}
			stack = append(stack, e.Target)
		}
	}
	return plan, nil
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
	// The parent's last succeeded step per node is what a replay can reuse.
	succeeded := map[string]recorded{}
	rows, e := tx.Query(`SELECT node_id,input,output,branch FROM steps WHERE run_id=$1 AND status='succeeded' ORDER BY id`, parent.ID)
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	for rows.Next() {
		var nodeID string
		var rec recorded
		if rows.Scan(&nodeID, &rec.input, &rec.output, &rec.branch) == nil {
			succeeded[nodeID] = rec
		}
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		fail(w, 500, "database error")
		return
	}
	feeder, _ := feederOf(graph, a.FromNode)
	seeded := a.InputOverride != nil && feeder != ""
	plan, herr := replayPlan(graph, a.FromNode, succeeded, seeded)
	if herr != nil {
		fail(w, herr.status, herr.msg)
		return
	}
	run, e := queueReplayRun(tx, parent, version, graph, a.FromNode, input)
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	t := now()
	for _, c := range plan {
		rec := succeeded[c.node]
		if rec.input == "" {
			rec.input = "{}"
		}
		if seeded && c.node == feeder {
			rec.output = jsonText(a.InputOverride)
		}
		if _, e = tx.Exec(`INSERT INTO steps(run_id,node_id,status,input,output,error,branch,attempt,finished_at,created_at) VALUES($1,$2,'succeeded',$3,$4,'',$5,1,$6,$6)`, run.ID, c.node, rec.input, rec.output, c.branch, t); e != nil {
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
