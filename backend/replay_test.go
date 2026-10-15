package main

import (
	"net/http"
	"strings"
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

// outcome is what the simulated runner records for one node.
type outcome struct {
	status string // succeeded, failed or uncertain
	branch string
	output map[string]any
}

// simulate plays the runner for the next queued job the way execute_inner
// does: it walks the graph from the trigger, reuses succeeded steps from the
// job, skips the branches a condition does not take and asks decide for
// every node it executes. A failed or uncertain outcome ends the run with
// that status. It returns the claimed job.
func simulate(t *testing.T, s *Server, decide func(node Node, input map[string]any) outcome) map[string]any {
	t.Helper()
	claimed, err := s.claim("replay-test")
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	job := claimed.(map[string]any)
	rid, lease := job["id"].(string), job["lease_token"].(string)
	graph := job["graph"].(Graph)
	reused := map[string]Step{}
	for _, step := range job["steps"].([]Step) {
		if step.Status == "succeeded" {
			reused[step.NodeID] = step
		}
	}
	nodes := map[string]Node{}
	root := ""
	for _, n := range graph.Nodes {
		nodes[n.ID] = n
		if triggerTypes[n.Type] {
			root = n.ID
		}
	}
	post := func(node, state string, input, output map[string]any, branch string) {
		t.Helper()
		status, value, _ := request(t, s, nil, "POST", "/internal/jobs/"+rid+"/steps", map[string]any{"lease_token": lease, "node_id": node, "status": state, "input": input, "output": output, "error": "", "branch": branch, "attempt": 1}, true)
		requireOK(t, status, value)
	}
	type frame struct {
		node  string
		input map[string]any
		skip  bool
	}
	stack := []frame{{root, job["input"].(map[string]any), false}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		var output map[string]any
		branch := ""
		if f.skip {
			post(f.node, "skipped", f.input, nil, "")
		} else if step, ok := reused[f.node]; ok {
			output, _ = step.Output.(map[string]any)
			branch = step.Branch
		} else {
			o := decide(nodes[f.node], f.input)
			if o.status != "succeeded" {
				post(f.node, "failed", f.input, nil, "")
				status, value, _ := request(t, s, nil, "POST", "/internal/jobs/"+rid+"/complete", map[string]any{"lease_token": lease, "status": o.status, "error": "boom"}, true)
				requireOK(t, status, value)
				return job
			}
			output, branch = o.output, o.branch
			post(f.node, "succeeded", f.input, output, branch)
		}
		for _, e := range graph.Edges {
			if e.Source == f.node {
				stack = append(stack, frame{e.Target, output, f.skip || (nodes[f.node].Type == "condition" && e.SourcePort != branch)})
			}
		}
	}
	status, value, _ := request(t, s, nil, "POST", "/internal/jobs/"+rid+"/complete", map[string]any{"lease_token": lease, "status": "succeeded", "error": ""}, true)
	requireOK(t, status, value)
	return job
}

// plain is the simulated runner's default behaviour: triggers pass their
// input through, conditions take the true branch and set_fields nodes add a
// "from" stamp.
func plain(node Node, input map[string]any) outcome {
	output := map[string]any{}
	for k, v := range input {
		output[k] = v
	}
	if node.Type == "condition" {
		return outcome{"succeeded", "true", output}
	}
	if !triggerTypes[node.Type] {
		output["from"] = node.ID
	}
	return outcome{"succeeded", "", output}
}

// drive runs the next job with plain behaviour, except that failAt fails
// unless the input it receives carries fixed:true, so a replay only succeeds
// when its override really reached the node.
func drive(t *testing.T, s *Server, failAt string) map[string]any {
	t.Helper()
	return simulate(t, s, func(node Node, input map[string]any) outcome {
		if node.ID == failAt && input["fixed"] != true {
			return outcome{status: "failed"}
		}
		return plain(node, input)
	})
}

// branchGraph is trigger -> gate (condition) -> yes | no -> after_no.
func branchGraph() map[string]any {
	n := func(id, kind string, config map[string]any) map[string]any {
		return map[string]any{"id": id, "type": kind, "position": map[string]any{"x": 0, "y": 0}, "config": config}
	}
	return map[string]any{"nodes": []any{
		n("trigger", "manual_trigger", map[string]any{}),
		n("gate", "condition", map[string]any{"left": "{{input.route}}", "operator": "equals", "right": "yes"}),
		n("yes", "set_fields", map[string]any{"fields": map[string]any{"choice": "yes"}}),
		n("no", "set_fields", map[string]any{"fields": map[string]any{"choice": "no"}}),
		n("after_no", "set_fields", map[string]any{"fields": map[string]any{"done": true}}),
	}, "edges": []any{
		map[string]any{"id": "a", "source": "trigger", "target": "gate", "source_port": "out"},
		map[string]any{"id": "b", "source": "gate", "target": "yes", "source_port": "true"},
		map[string]any{"id": "c", "source": "gate", "target": "no", "source_port": "false"},
		map[string]any{"id": "d", "source": "no", "target": "after_no", "source_port": "out"},
	}}
}

// finishedRun creates, publishes and runs a workflow, drives it with decide
// and returns the workflow and run IDs.
func finishedRun(t *testing.T, s *Server, c *http.Cookie, graph map[string]any, decide func(Node, map[string]any) outcome) (string, string) {
	t.Helper()
	status, value, _ := request(t, s, c, "POST", "/api/workflows", map[string]any{"name": "replay", "draft": graph}, false)
	requireOK(t, status, value)
	wid := value["workflow"].(map[string]any)["id"].(string)
	status, value, _ = request(t, s, c, "POST", "/api/workflows/"+wid+"/publish", map[string]any{}, false)
	requireOK(t, status, value)
	status, value, _ = request(t, s, c, "POST", "/api/workflows/"+wid+"/run", map[string]any{"input": map[string]any{"route": "yes"}}, false)
	requireOK(t, status, value)
	rid := value["run"].(map[string]any)["id"].(string)
	simulate(t, s, decide)
	return wid, rid
}

func detailSteps(t *testing.T, s *Server, cookie *http.Cookie, rid string) (map[string]any, map[string]map[string]any) {
	t.Helper()
	status, value, _ := request(t, s, cookie, "GET", "/api/runs/"+rid, nil, false)
	requireOK(t, status, value)
	steps := map[string]map[string]any{}
	for _, step := range value["steps"].([]any) {
		m := step.(map[string]any)
		steps[m["node_id"].(string)] = m
	}
	return value["run"].(map[string]any), steps
}

func TestReplayFromSkippedNodeForcesTheConditionOntoItsBranch(t *testing.T) {
	s := testServer(t)
	cookie := registered(t, s, "replay-"+id()+"@example.com")
	_, parent := finishedRun(t, s, cookie, branchGraph(), plain)
	_, steps := detailSteps(t, s, cookie, parent)
	if steps["yes"]["status"] != "succeeded" || steps["no"]["status"] != "skipped" {
		t.Fatalf("parent steps: %#v", steps)
	}
	// No override: the skipped node runs with the condition's recorded output.
	status, value, _ := request(t, s, cookie, "POST", "/api/runs/"+parent+"/replay", map[string]any{"from_node": "no"}, false)
	requireOK(t, status, value)
	job := simulate(t, s, plain)
	reused := jobNodes(job)
	if len(reused) != 2 || reused["gate"].Branch != "false" {
		t.Fatalf("reused steps: %#v", reused)
	}
	if _, copied := reused["yes"]; copied {
		t.Fatalf("sibling branch copied: %#v", reused)
	}
	_, steps = detailSteps(t, s, cookie, value["run"].(map[string]any)["id"].(string))
	if steps["no"]["status"] != "succeeded" || steps["after_no"]["status"] != "succeeded" || steps["yes"]["status"] != "skipped" {
		t.Fatalf("replay steps: %#v", steps)
	}
	if steps["no"]["input"].(map[string]any)["route"] != "yes" {
		t.Fatalf("replayed node input: %#v", steps["no"])
	}
}

func TestReplayFromGrandchildOnUntakenBranchNeedsAnOverride(t *testing.T) {
	s := testServer(t)
	cookie := registered(t, s, "replay-"+id()+"@example.com")
	_, parent := finishedRun(t, s, cookie, branchGraph(), plain)
	// Without an override the skipped feeder has no output to give.
	status, value, _ := request(t, s, cookie, "POST", "/api/runs/"+parent+"/replay", map[string]any{"from_node": "after_no"}, false)
	if status != 400 || !strings.Contains(value["error"].(string), "node no did not complete") {
		t.Fatalf("replay below a skipped node: %d %v", status, value)
	}
	status, value, _ = request(t, s, cookie, "POST", "/api/runs/"+parent+"/replay", map[string]any{"from_node": "after_no", "input_override": map[string]any{"seeded": true}}, false)
	requireOK(t, status, value)
	job := simulate(t, s, plain)
	reused := jobNodes(job)
	if len(reused) != 3 || reused["gate"].Branch != "false" || reused["no"].Output.(map[string]any)["seeded"] != true {
		t.Fatalf("reused steps: %#v", reused)
	}
	_, steps := detailSteps(t, s, cookie, value["run"].(map[string]any)["id"].(string))
	if steps["after_no"]["status"] != "succeeded" || steps["after_no"]["input"].(map[string]any)["seeded"] != true || steps["yes"]["status"] != "skipped" {
		t.Fatalf("replay steps: %#v", steps)
	}
}

func TestReplayRejectsReuseOfNodesThatNeverCompleted(t *testing.T) {
	s := testServer(t)
	cookie := registered(t, s, "replay-"+id()+"@example.com")
	n := func(id, kind string, config map[string]any) map[string]any {
		return map[string]any{"id": id, "type": kind, "position": map[string]any{"x": 0, "y": 0}, "config": config}
	}
	graph := map[string]any{"nodes": []any{
		n("trigger", "manual_trigger", map[string]any{}),
		n("post", "http_request", map[string]any{"url": "https://example.com/orders", "method": "POST"}),
		n("after", "set_fields", map[string]any{"fields": map[string]any{"done": true}}),
	}, "edges": []any{
		map[string]any{"id": "a", "source": "trigger", "target": "post", "source_port": "out"},
		map[string]any{"id": "b", "source": "post", "target": "after", "source_port": "out"},
	}}
	for _, final := range []string{"failed", "uncertain"} {
		_, parent := finishedRun(t, s, cookie, graph, func(node Node, input map[string]any) outcome {
			if node.ID == "post" {
				return outcome{status: final}
			}
			return plain(node, input)
		})
		run, _ := detailSteps(t, s, cookie, parent)
		if run["status"] != final {
			t.Fatalf("parent: %#v", run)
		}
		// Reusing the POST that never completed would silently re-send it.
		status, value, _ := request(t, s, cookie, "POST", "/api/runs/"+parent+"/replay", map[string]any{"from_node": "after"}, false)
		if status != 400 || !strings.Contains(value["error"].(string), "node post did not complete in the parent run") {
			t.Fatalf("%s parent: replay below incomplete node: %d %v", final, status, value)
		}
		// Replaying from the POST itself is the user's explicit choice.
		status, value, _ = request(t, s, cookie, "POST", "/api/runs/"+parent+"/replay", map[string]any{"from_node": "post"}, false)
		requireOK(t, status, value)
		if reused := jobNodes(simulate(t, s, plain)); len(reused) != 1 || reused["trigger"].Status != "succeeded" {
			t.Fatalf("reused steps: %#v", reused)
		}
	}
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
