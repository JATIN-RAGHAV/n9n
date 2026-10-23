"""Smoke test for time-travel replay through the public n9n API.

Runs a branching workflow, replays it from the condition with a changed
input so the other branch executes, and checks the parent link, the copied
upstream steps and a replay against an edited draft. Standard library only.

Run after `make up`: python3 tests/replay_smoke.py
"""

import argparse
import json
import secrets

from e2e_smoke import Client, edge, node, until


def steps_by_node(detail):
    return {step["node_id"]: step for step in detail["steps"]}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="http://localhost:8080")
    args = parser.parse_args()
    c = Client(args.base)
    assert c.call("GET", "/api/health")["status"] == "ok"
    c.call("POST", "/api/auth/register", {
        "email": f"replay-{secrets.token_hex(5)}@example.test", "password": "smoke-password-123",
    })

    graph = {
        "nodes": [
            node("trigger", "manual_trigger"),
            node("stamp", "set_fields", {"fields": {"stamped": "{{input.customer}}"}}),
            node("branch", "condition", {"left": "{{input.route}}", "operator": "equals", "right": "yes"}),
            node("yes", "set_fields", {"fields": {"choice": "yes"}}),
            node("no", "set_fields", {"fields": {"choice": "no", "stamped_again": "{{nodes.stamp.stamped}}"}}),
        ],
        "edges": [
            edge("a", "trigger", "stamp"), edge("b", "stamp", "branch"),
            edge("c", "branch", "yes", "true"), edge("d", "branch", "no", "false"),
        ],
    }
    wid = c.call("POST", "/api/workflows", {"name": "smoke replay", "draft": graph})["workflow"]["id"]
    c.call("POST", f"/api/workflows/{wid}/publish")
    parent = c.call("POST", f"/api/workflows/{wid}/run", {"input": {"route": "yes", "customer": "Ada"}})["run"]
    assert parent["kind"] == "normal" and parent.get("parent_run_id") is None
    parent_detail = until(c, parent["id"])
    assert parent_detail["run"]["status"] == "succeeded", parent_detail
    parent_steps = steps_by_node(parent_detail)
    assert parent_steps["yes"]["status"] == "succeeded" and parent_steps["no"]["status"] == "skipped"
    assert parent_steps["branch"]["duration_ms"] is not None and parent_detail["graph"]["nodes"]
    print("PASS: branching workflow ran the true branch with step timing")

    # Replay from the condition with the route flipped: the stamp output is
    # copied (with the override applied), the condition re-evaluates and the
    # other branch runs.
    replay = c.call("POST", f"/api/runs/{parent['id']}/replay", {
        "from_node": "branch", "input_override": {"route": "no", "customer": "Ada", "stamped": "Ada"},
    })["run"]
    assert replay["kind"] == "replay" and replay["parent_run_id"] == parent["id"]
    assert replay["resume_from_node"] == "branch" and replay["version"] == 1
    replay_detail = until(c, replay["id"])
    assert replay_detail["run"]["status"] == "succeeded", replay_detail
    replay_steps = steps_by_node(replay_detail)
    assert replay_steps["trigger"]["status"] == "succeeded"
    assert replay_steps["stamp"]["output"] == {"route": "no", "customer": "Ada", "stamped": "Ada"}, replay_steps["stamp"]
    assert replay_steps["branch"]["branch"] == "false", replay_steps["branch"]
    assert replay_steps["no"]["status"] == "succeeded" and replay_steps["no"]["output"]["choice"] == "no"
    assert replay_steps["no"]["output"]["stamped_again"] == "Ada"
    assert replay_steps["yes"]["status"] == "skipped"
    # Copied steps were not executed again, so they carry no timing.
    assert replay_steps["stamp"].get("duration_ms") is None and replay_steps["no"].get("duration_ms") is not None
    children = c.call("GET", f"/api/runs/{parent['id']}")["children"]
    assert [child["id"] for child in children] == [replay["id"]], children
    print("PASS: replay from the condition reused upstream steps and took the other branch")

    # Replay against the edited draft uses the new config and leaves the
    # workflow unpublished at version 1.
    edited = json.loads(json.dumps(graph))
    edited["nodes"][4]["config"]["fields"]["choice"] = "no (draft)"
    c.call("PUT", f"/api/workflows/{wid}", {"name": "smoke replay", "draft": edited})
    draft_replay = c.call("POST", f"/api/runs/{replay['id']}/replay", {"from_node": "no", "graph": "draft"})["run"]
    assert draft_replay["parent_run_id"] == replay["id"]
    draft_detail = until(c, draft_replay["id"])
    assert draft_detail["run"]["status"] == "succeeded", draft_detail
    draft_steps = steps_by_node(draft_detail)
    assert draft_steps["no"]["output"]["choice"] == "no (draft)", draft_steps["no"]
    assert draft_steps["branch"]["status"] == "succeeded" and draft_steps["branch"].get("duration_ms") is None
    assert c.call("GET", f"/api/workflows/{wid}")["workflow"]["published_version"] == 1
    print("PASS: replay against the current draft used the edited node without publishing")

    c.call("POST", f"/api/runs/{parent['id']}/replay", {"from_node": "ghost"}, expected=400)
    other = Client(args.base)
    other.call("POST", "/api/auth/register", {
        "email": f"other-{secrets.token_hex(5)}@example.test", "password": "smoke-password-123",
    })
    other.call("POST", f"/api/runs/{parent['id']}/replay", {"from_node": "branch"}, expected=404)
    print("PASS: unknown nodes are rejected and other users cannot replay the run")
    print("All replay smoke checks passed.")


if __name__ == "__main__":
    main()
