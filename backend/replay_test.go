package main

import (
	"testing"
)

var chainNodes = []string{"trigger", "a", "b", "c", "d"}

// chainGraph is a five-node chain whose set_fields nodes stamp their name.
func chainGraph() map[string]any {
	nodes := []any{map[string]any{"id": "trigger", "type": "manual_trigger", "position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{}}}
	edges := []any{}
	for i, id := range chainNodes[1:] {
		nodes = append(nodes, map[string]any{"id": id, "type": "set_fields", "position": map[string]any{"x": 200 * (i + 1), "y": 0}, "config": map[string]any{"fields": map[string]any{id: id}}})
		edges = append(edges, map[string]any{"id": "e-" + id, "source": chainNodes[i], "target": id, "source_port": "out"})
	}
	return map[string]any{"nodes": nodes, "edges": edges}
}

// drive plays the runner for the next queued job: every node of the chain
// without a reused step is recorded as succeeded, except failAt, which fails
// and ends the run. It returns the claimed job.
func drive(t *testing.T, s *Server, failAt string) map[string]any {
	t.Helper()
	claimed, err := s.claim("replay-test")
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	job := claimed.(map[string]any)
	rid, lease := job["id"].(string), job["lease_token"].(string)
	done := map[string]bool{}
	for _, step := range job["steps"].([]Step) {
		if step.Status == "succeeded" {
			done[step.NodeID] = true
		}
	}
	for _, node := range chainNodes {
		if done[node] {
			continue
		}
		state := "succeeded"
		if node == failAt {
			state = "failed"
		}
		status, value, _ := request(t, s, nil, "POST", "/internal/jobs/"+rid+"/steps", map[string]any{"lease_token": lease, "node_id": node, "status": state, "input": map[string]any{}, "output": map[string]any{"from": node}, "error": "", "attempt": 1}, true)
		requireOK(t, status, value)
		if state == "failed" {
			status, value, _ = request(t, s, nil, "POST", "/internal/jobs/"+rid+"/complete", map[string]any{"lease_token": lease, "status": "failed", "error": "boom"}, true)
			requireOK(t, status, value)
			return job
		}
	}
	status, value, _ := request(t, s, nil, "POST", "/internal/jobs/"+rid+"/complete", map[string]any{"lease_token": lease, "status": "succeeded", "error": ""}, true)
	requireOK(t, status, value)
	return job
}

func jobNodes(job map[string]any) map[string]Step {
	out := map[string]Step{}
	for _, step := range job["steps"].([]Step) {
		out[step.NodeID] = step
	}
	return out
}

func TestReplayFromMiddleReusesUpstreamSteps(t *testing.T) {
	s := testServer(t)
	cookie := registered(t, s, "replay-"+id()+"@example.com")
	status, value, _ := request(t, s, cookie, "POST", "/api/workflows", map[string]any{"name": "chain", "draft": chainGraph()}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	status, value, _ = request(t, s, cookie, "POST", "/api/workflows/"+wid+"/publish", map[string]any{}, false)
	requireOK(t, status, value)
	status, value, _ = request(t, s, cookie, "POST", "/api/workflows/"+wid+"/run", map[string]any{"input": map[string]any{"x": 1}}, false)
	requireOK(t, status, value)
	parent := value["run"].(map[string]any)["id"].(string)
	drive(t, s, "")

	status, value, _ = request(t, s, cookie, "POST", "/api/runs/"+parent+"/replay", map[string]any{"from_node": "c"}, false)
	requireOK(t, status, value)
	run := value["run"].(map[string]any)
	if run["kind"] != "replay" || run["parent_run_id"] != parent || run["resume_from_node"] != "c" || run["status"] != "queued" || run["version"] != float64(1) || run["test"] != nil {
		t.Fatalf("replay run: %#v", run)
	}
	if run["input"].(map[string]any)["x"] != float64(1) {
		t.Fatalf("replay input: %#v", run["input"])
	}
	job := drive(t, s, "")
	if job["id"] != run["id"] || job["kind"] != "replay" {
		t.Fatalf("claimed job: %#v", job)
	}
	reused := jobNodes(job)
	if len(reused) != 3 || reused["trigger"].Status != "succeeded" || reused["a"].Status != "succeeded" || reused["b"].Status != "succeeded" {
		t.Fatalf("reused steps: %#v", reused)
	}
	if reused["b"].Output.(map[string]any)["from"] != "b" {
		t.Fatalf("copied output: %#v", reused["b"])
	}
	status, value, _ = request(t, s, cookie, "GET", "/api/runs/"+run["id"].(string), nil, false)
	requireOK(t, status, value)
	if value["run"].(map[string]any)["status"] != "succeeded" || len(value["steps"].([]any)) != 5 {
		t.Fatalf("replay detail: %#v", value)
	}
	status, value, _ = request(t, s, cookie, "GET", "/api/runs/"+parent, nil, false)
	requireOK(t, status, value)
	children := value["children"].([]any)
	if len(children) != 1 || children[0].(map[string]any)["id"] != run["id"] || children[0].(map[string]any)["kind"] != "replay" {
		t.Fatalf("children: %#v", children)
	}
}

func TestReplayFromTriggerRerunsEverythingWithNewInput(t *testing.T) {
	s := testServer(t)
	cookie := registered(t, s, "replay-"+id()+"@example.com")
	status, value, _ := request(t, s, cookie, "POST", "/api/workflows", map[string]any{"name": "chain", "draft": chainGraph()}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	// A draft test run can be replayed too; it keeps version 0 and a snapshot.
	status, value, _ = request(t, s, cookie, "POST", "/api/workflows/"+wid+"/test", map[string]any{"input": map[string]any{"x": 1}}, false)
	requireOK(t, status, value)
	parent := value["run"].(map[string]any)["id"].(string)
	drive(t, s, "")
	status, value, _ = request(t, s, cookie, "POST", "/api/runs/"+parent+"/replay", map[string]any{"from_node": "trigger", "input_override": map[string]any{"x": 2}}, false)
	requireOK(t, status, value)
	run := value["run"].(map[string]any)
	if run["version"] != float64(0) || run["kind"] != "replay" || run["input"].(map[string]any)["x"] != float64(2) {
		t.Fatalf("replay run: %#v", run)
	}
	job := drive(t, s, "")
	if len(job["steps"].([]Step)) != 0 || job["input"].(map[string]any)["x"] != float64(2) {
		t.Fatalf("job: %#v", job)
	}
}

func TestReplayRejectsForeignRunsBadNodesAndLiveRuns(t *testing.T) {
	s := testServer(t)
	cookie := registered(t, s, "replay-"+id()+"@example.com")
	other := registered(t, s, "other-"+id()+"@example.com")
	status, value, _ := request(t, s, cookie, "POST", "/api/workflows", map[string]any{"name": "chain", "draft": chainGraph()}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	status, value, _ = request(t, s, cookie, "POST", "/api/workflows/"+wid+"/publish", map[string]any{}, false)
	requireOK(t, status, value)
	status, value, _ = request(t, s, cookie, "POST", "/api/workflows/"+wid+"/run", map[string]any{"input": map[string]any{}}, false)
	requireOK(t, status, value)
	parent := value["run"].(map[string]any)["id"].(string)
	if status, _, _ = request(t, s, cookie, "POST", "/api/runs/"+parent+"/replay", map[string]any{"from_node": "c"}, false); status != 409 {
		t.Fatalf("replay of a queued run: %d", status)
	}
	drive(t, s, "")
	if status, _, _ = request(t, s, other, "POST", "/api/runs/"+parent+"/replay", map[string]any{"from_node": "c"}, false); status != 404 {
		t.Fatalf("foreign replay: %d", status)
	}
	for _, bad := range []map[string]any{
		{"from_node": "ghost"},
		{},
		{"from_node": "c", "graph": "nonsense"},
		{"from_node": "c", "surprise": true},
	} {
		if status, _, _ = request(t, s, cookie, "POST", "/api/runs/"+parent+"/replay", bad, false); status != 400 {
			t.Fatalf("bad replay accepted: %d %v", status, bad)
		}
	}
	status, value, _ = request(t, s, cookie, "GET", "/api/runs/"+parent, nil, false)
	requireOK(t, status, value)
	if len(value["children"].([]any)) != 0 {
		t.Fatalf("rejected replays created runs: %#v", value["children"])
	}
}

func TestReplayAgainstDraftUsesEditedConfigAndStaysUnpublished(t *testing.T) {
	s := testServer(t)
	cookie := registered(t, s, "replay-"+id()+"@example.com")
	status, value, _ := request(t, s, cookie, "POST", "/api/workflows", map[string]any{"name": "chain", "draft": chainGraph()}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	status, value, _ = request(t, s, cookie, "POST", "/api/workflows/"+wid+"/test", map[string]any{"input": map[string]any{}}, false)
	requireOK(t, status, value)
	parent := value["run"].(map[string]any)["id"].(string)
	drive(t, s, "")
	edited := chainGraph()
	edited["nodes"].([]any)[4].(map[string]any)["config"] = map[string]any{"fields": map[string]any{"d": "edited"}}
	status, value, _ = request(t, s, cookie, "PUT", "/api/workflows/"+wid, map[string]any{"name": "chain", "draft": edited}, false)
	requireOK(t, status, value)

	status, value, _ = request(t, s, cookie, "POST", "/api/runs/"+parent+"/replay", map[string]any{"from_node": "c", "graph": "draft"}, false)
	requireOK(t, status, value)
	run := value["run"].(map[string]any)
	if run["kind"] != "replay" || run["version"] != float64(0) || run["parent_run_id"] != parent {
		t.Fatalf("draft replay: %#v", run)
	}
	job := drive(t, s, "")
	var d Node
	for _, n := range job["graph"].(Graph).Nodes {
		if n.ID == "d" {
			d = n
		}
	}
	if d.Config["fields"].(map[string]any)["d"] != "edited" {
		t.Fatalf("draft replay graph: %#v", d)
	}
	if reused := jobNodes(job); len(reused) != 3 || reused["b"].Status != "succeeded" {
		t.Fatalf("reused steps: %#v", reused)
	}
	status, value, _ = request(t, s, cookie, "GET", "/api/workflows/"+wid, nil, false)
	requireOK(t, status, value)
	if value["workflow"].(map[string]any)["published_version"] != nil {
		t.Fatalf("draft replay published the workflow: %#v", value["workflow"])
	}
	// The original snapshot is untouched by draft edits.
	status, value, _ = request(t, s, cookie, "POST", "/api/runs/"+parent+"/replay", map[string]any{"from_node": "c", "graph": "original"}, false)
	requireOK(t, status, value)
	job = drive(t, s, "")
	for _, n := range job["graph"].(Graph).Nodes {
		if n.ID == "d" && n.Config["fields"].(map[string]any)["d"] != "d" {
			t.Fatalf("original replay graph: %#v", n)
		}
	}
	// A draft that fails publish validation, or no longer has from_node, is rejected.
	broken := map[string]any{"nodes": []any{
		map[string]any{"id": "trigger", "type": "manual_trigger", "position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{}},
		map[string]any{"id": "c", "type": "set_fields", "position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{"fields": map[string]any{}}},
	}, "edges": []any{}}
	status, value, _ = request(t, s, cookie, "PUT", "/api/workflows/"+wid, map[string]any{"name": "chain", "draft": broken}, false)
	requireOK(t, status, value)
	if status, value, _ = request(t, s, cookie, "POST", "/api/runs/"+parent+"/replay", map[string]any{"from_node": "c", "graph": "draft"}, false); status != 400 {
		t.Fatalf("invalid draft accepted: %d %v", status, value)
	}
	broken["edges"] = []any{map[string]any{"id": "e", "source": "trigger", "target": "c", "source_port": "out"}}
	status, value, _ = request(t, s, cookie, "PUT", "/api/workflows/"+wid, map[string]any{"name": "chain", "draft": broken}, false)
	requireOK(t, status, value)
	if status, value, _ = request(t, s, cookie, "POST", "/api/runs/"+parent+"/replay", map[string]any{"from_node": "d", "graph": "draft"}, false); status != 400 {
		t.Fatalf("missing from_node accepted: %d %v", status, value)
	}
}

func TestReplayOfFailedRunSucceedsAfterInputFix(t *testing.T) {
	s := testServer(t)
	cookie := registered(t, s, "replay-"+id()+"@example.com")
	status, value, _ := request(t, s, cookie, "POST", "/api/workflows", map[string]any{"name": "chain", "draft": chainGraph()}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	status, value, _ = request(t, s, cookie, "POST", "/api/workflows/"+wid+"/publish", map[string]any{}, false)
	requireOK(t, status, value)
	status, value, _ = request(t, s, cookie, "POST", "/api/workflows/"+wid+"/run", map[string]any{"input": map[string]any{}}, false)
	requireOK(t, status, value)
	parent := value["run"].(map[string]any)["id"].(string)
	drive(t, s, "c")
	status, value, _ = request(t, s, cookie, "GET", "/api/runs/"+parent, nil, false)
	requireOK(t, status, value)
	if value["run"].(map[string]any)["status"] != "failed" {
		t.Fatalf("parent: %#v", value["run"])
	}
	status, value, _ = request(t, s, cookie, "POST", "/api/runs/"+parent+"/replay", map[string]any{"from_node": "c", "input_override": map[string]any{"fixed": true}}, false)
	requireOK(t, status, value)
	run := value["run"].(map[string]any)
	job := drive(t, s, "")
	reused := jobNodes(job)
	if len(reused) != 3 || reused["b"].Output.(map[string]any)["fixed"] != true || reused["a"].Output.(map[string]any)["from"] != "a" {
		t.Fatalf("reused steps after fix: %#v", reused)
	}
	status, value, _ = request(t, s, cookie, "GET", "/api/runs/"+run["id"].(string), nil, false)
	requireOK(t, status, value)
	if value["run"].(map[string]any)["status"] != "succeeded" {
		t.Fatalf("replay after fix: %#v", value["run"])
	}
	steps := map[string]map[string]any{}
	for _, step := range value["steps"].([]any) {
		m := step.(map[string]any)
		steps[m["node_id"].(string)] = m
	}
	if steps["b"]["output"].(map[string]any)["fixed"] != true || steps["c"]["status"] != "succeeded" || steps["d"]["status"] != "succeeded" {
		t.Fatalf("replay steps: %#v", steps)
	}
}
