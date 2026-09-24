"""Black-box smoke for the public n9n API. Uses only Python's standard library.

Run after `make up`: python3 tests/e2e_smoke.py
"""

import argparse
import http.cookiejar
import json
import secrets
import time
import urllib.error
import urllib.request


class Client:
    def __init__(self, base):
        self.base = base.rstrip("/")
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar())
        )

    def call(self, method, path, body=None, headers=None, expected=200):
        data = None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(
            self.base + path,
            data=data,
            method=method,
            headers={"Content-Type": "application/json", **(headers or {})},
        )
        try:
            with self.opener.open(request, timeout=20) as response:
                status = response.status
                payload = json.load(response)
        except urllib.error.HTTPError as error:
            status = error.code
            payload = json.load(error)
        assert status == expected, f"{method} {path}: {status}, wanted {expected}: {payload}"
        return payload


def node(id_, kind, config=None, credential=None):
    value = {
        "id": id_,
        "type": kind,
        "position": {"x": 40, "y": 40},
        "config": config or {},
    }
    if credential:
        value["credential_id"] = credential
    return value


def edge(id_, source, target, port="out"):
    return {"id": id_, "source": source, "target": target, "source_port": port}


def until(client, run_id, timeout=35):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        detail = client.call("GET", f"/api/runs/{run_id}")
        if detail["run"]["status"] in {"succeeded", "failed", "cancelled", "uncertain"}:
            return detail
        time.sleep(0.4)
    raise AssertionError(f"run {run_id} did not finish in {timeout}s")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="http://localhost:8080")
    args = parser.parse_args()
    c = Client(args.base)
    assert c.call("GET", "/api/health")["status"] == "ok"
    email = f"smoke-{secrets.token_hex(5)}@example.test"
    c.call("POST", "/api/auth/register", {"email": email, "password": "smoke-password-123"})
    assert c.call("GET", "/api/auth/me")["user"]["email"] == email

    graph = {
        "nodes": [
            node("trigger", "manual_trigger"),
            node("stamp", "set_fields", {"fields": {"version_tag": "one", "copied": "{{input.customer}}"}}),
            node("branch", "condition", {"left": "{{input.route}}", "operator": "equals", "right": "yes"}),
            node("yes", "set_fields", {"fields": {"choice": "yes"}}),
            node("no", "set_fields", {"fields": {"choice": "no"}}),
        ],
        "edges": [
            edge("a", "trigger", "stamp"), edge("b", "stamp", "branch"),
            edge("c", "branch", "yes", "true"), edge("d", "branch", "no", "false"),
        ],
    }
    workflow = c.call("POST", "/api/workflows", {"name": "smoke branching", "draft": graph})["workflow"]
    wid = workflow["id"]
    c.call("POST", f"/api/workflows/{wid}/publish")
    first = c.call("POST", f"/api/workflows/{wid}/run", {"input": {"route": "yes", "customer": "Ada"}})["run"]
    detail = until(c, first["id"])
    assert detail["run"]["status"] == "succeeded", detail
    successful = {step["node_id"]: step for step in detail["steps"] if step["status"] == "succeeded"}
    assert successful["stamp"]["output"]["copied"] == "Ada"
    assert successful["yes"]["output"]["choice"] == "yes"
    assert "no" not in successful
    assert first["version"] == 1
    print("PASS: registration, mappings, true branch, run steps")

    graph["nodes"][1]["config"]["fields"]["version_tag"] = "two"
    c.call("PUT", f"/api/workflows/{wid}", {"name": "smoke branching", "draft": graph})
    c.call("POST", f"/api/workflows/{wid}/publish")
    second = c.call("POST", f"/api/workflows/{wid}/run", {"input": {"route": "no", "customer": "Lin"}})["run"]
    second_detail = until(c, second["id"])
    second_steps = {step["node_id"]: step for step in second_detail["steps"] if step["status"] == "succeeded"}
    assert second["version"] == 2 and first["version"] == 1
    assert second_steps["stamp"]["output"]["version_tag"] == "two"
    assert second_steps["no"]["output"]["choice"] == "no"
    assert "yes" not in second_steps
    print("PASS: publish versions and false branch")

    other = Client(args.base)
    other.call("POST", "/api/auth/register", {"email": f"other-{secrets.token_hex(5)}@example.test", "password": "smoke-password-123"})
    other.call("GET", f"/api/workflows/{wid}", expected=404)
    other.call("GET", f"/api/runs/{first['id']}", expected=404)
    print("PASS: cross-user access denied")

    hook_graph = {"nodes": [node("hook", "webhook_trigger", {"secret": "test-hook-secret"}), node("save", "set_fields", {"fields": {"received": "{{input.message}}"}})], "edges": [edge("h", "hook", "save")]}
    hook_id = c.call("POST", "/api/workflows", {"name": "smoke webhook", "draft": hook_graph})["workflow"]["id"]
    c.call("POST", f"/api/workflows/{hook_id}/publish")
    c.call("POST", f"/api/workflows/{hook_id}/activate", {"active": True})
    c.call("POST", f"/api/hooks/{hook_id}", {"message": "hello"}, expected=403)
    event = secrets.token_hex(6)
    headers = {"X-Webhook-Secret": "test-hook-secret", "X-Event-ID": event}
    hook_run = c.call("POST", f"/api/hooks/{hook_id}", {"message": "hello"}, headers)["run"]
    duplicate = c.call("POST", f"/api/hooks/{hook_id}", {"message": "hello"}, headers)
    assert duplicate["duplicate"] is True and duplicate["run"]["id"] == hook_run["id"]
    assert until(c, hook_run["id"])["run"]["status"] == "succeeded"
    print("PASS: webhook secret and event deduplication")

    private_graph = {"nodes": [node("m", "manual_trigger"), node("request", "http_request", {"url": "http://127.0.0.1:8080/api/health", "method": "GET"})], "edges": [edge("p", "m", "request")]}
    private_id = c.call("POST", "/api/workflows", {"name": "smoke private HTTP", "draft": private_graph})["workflow"]["id"]
    c.call("POST", f"/api/workflows/{private_id}/publish")
    private_run = c.call("POST", f"/api/workflows/{private_id}/run", {"input": {}})["run"]
    private_detail = until(c, private_run["id"])
    assert private_detail["run"]["status"] == "failed"
    assert "private" in str(private_detail).lower()
    print("PASS: private HTTP address rejected")

    schedule_graph = {"nodes": [node("timer", "schedule_trigger", {"interval_seconds": 10, "timezone": "UTC"}), node("record", "set_fields", {"fields": {"source": "schedule"}})], "edges": [edge("s", "timer", "record")]}
    schedule_id = c.call("POST", "/api/workflows", {"name": "smoke schedule", "draft": schedule_graph})["workflow"]["id"]
    c.call("POST", f"/api/workflows/{schedule_id}/publish")
    c.call("POST", f"/api/workflows/{schedule_id}/activate", {"active": True})
    deadline = time.monotonic() + 35
    scheduled = []
    while time.monotonic() < deadline:
        scheduled = c.call("GET", f"/api/runs?workflow_id={schedule_id}")["runs"]
        if scheduled:
            break
        time.sleep(1)
    assert scheduled, "schedule did not fire"
    c.call("POST", f"/api/workflows/{schedule_id}/activate", {"active": False})
    assert until(c, scheduled[0]["id"])["run"]["status"] == "succeeded"
    print("PASS: schedule fired and deactivated")

    queued = c.call("POST", f"/api/workflows/{wid}/run", {"input": {"route": "yes"}})["run"]
    cancelled = c.call("POST", f"/api/runs/{queued['id']}/cancel")["run"]
    assert cancelled["status"] in {"cancelled", "succeeded"}, cancelled
    print("PASS: cancel endpoint")
    print("All public API smoke checks passed.")


if __name__ == "__main__":
    main()
