"""Verify queued graph snapshots survive a backend restart.

Run after `make up`: python3 tests/restart_smoke.py
The script temporarily stops the runner and always starts it again.
"""

import argparse
import json
import secrets
import subprocess
import time
import urllib.error
from pathlib import Path

from e2e_smoke import Client, edge, node, until


ROOT = Path(__file__).resolve().parents[1]


def compose(*args):
    subprocess.run(["docker", "compose", *args], cwd=ROOT, check=True)


def wait_health(client, seconds=45):
    deadline = time.monotonic() + seconds
    last_error = None
    while time.monotonic() < deadline:
        try:
            if client.call("GET", "/api/health")["status"] == "ok":
                return
        except (urllib.error.URLError, AssertionError, json.JSONDecodeError) as error:
            last_error = error
        time.sleep(0.5)
    raise AssertionError(f"backend did not recover in {seconds}s: {last_error}")


def successful_step(detail, node_id):
    assert detail["run"]["status"] == "succeeded", detail
    steps = [step for step in detail["steps"] if step["node_id"] == node_id and step["status"] == "succeeded"]
    assert len(steps) == 1, detail
    return steps[0]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="http://localhost:8080")
    args = parser.parse_args()
    client = Client(args.base)
    runner_stopped = False
    try:
        compose("stop", "runner")
        runner_stopped = True
        wait_health(client)
        email = f"restart-{secrets.token_hex(5)}@example.test"
        client.call("POST", "/api/auth/register", {"email": email, "password": "restart-smoke-password"})

        graph = {
            "nodes": [
                node("trigger", "manual_trigger"),
                node("stamp", "set_fields", {"fields": {"version_tag": "one"}}),
            ],
            "edges": [edge("connection", "trigger", "stamp")],
        }
        workflow = client.call("POST", "/api/workflows", {"name": "restart snapshot smoke", "draft": graph})["workflow"]
        workflow_id = workflow["id"]
        client.call("POST", f"/api/workflows/{workflow_id}/publish")
        first = client.call("POST", f"/api/workflows/{workflow_id}/run", {"input": {}})["run"]
        assert first["version"] == 1 and first["status"] == "queued", first

        graph["nodes"][1]["config"]["fields"]["version_tag"] = "two"
        client.call("PUT", f"/api/workflows/{workflow_id}", {"name": "restart snapshot smoke", "draft": graph})
        published = client.call("POST", f"/api/workflows/{workflow_id}/publish")["workflow"]
        assert published["published_version"] == 2, published

        compose("restart", "backend")
        wait_health(client)
        queued = client.call("GET", f"/api/runs/{first['id']}")["run"]
        assert queued["status"] == "queued" and queued["version"] == 1, queued

        compose("start", "runner")
        runner_stopped = False
        first_detail = until(client, first["id"], timeout=90)
        assert successful_step(first_detail, "stamp")["output"]["version_tag"] == "one", first_detail

        second = client.call("POST", f"/api/workflows/{workflow_id}/run", {"input": {}})["run"]
        assert second["version"] == 2, second
        second_detail = until(client, second["id"], timeout=90)
        assert successful_step(second_detail, "stamp")["output"]["version_tag"] == "two", second_detail
        print("PASS: queued run persisted through backend restart and retained v1 snapshot; new run used v2")
    finally:
        if runner_stopped:
            compose("start", "runner")


if __name__ == "__main__":
    main()
