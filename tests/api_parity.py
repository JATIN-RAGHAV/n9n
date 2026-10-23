"""Checks that the Go API and the TypeScript Worker API behave the same.

Both servers must point at the same database. Run with both up:
  python3 tests/api_parity.py --go http://127.0.0.1:18080 --ts http://localhost:8787
Uses only Python's standard library.
"""

import argparse
import http.cookiejar
import json
import secrets
import sys
import urllib.error
import urllib.request

VOLATILE = {"id", "created_at", "updated_at", "workflow_id"}


class Client:
    def __init__(self, base):
        self.base = base.rstrip("/")
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def raw(self, method, path, body=None):
        data = body if isinstance(body, (bytes, type(None))) else json.dumps(body).encode()
        req = urllib.request.Request(self.base + path, data=data, method=method, headers={"Content-Type": "application/json"})
        try:
            with self.opener.open(req, timeout=30) as resp:
                return resp.status, json.loads(resp.read() or b"null")
        except urllib.error.HTTPError as err:
            return err.code, json.loads(err.read() or b"null")

    def ok(self, method, path, body=None):
        status, payload = self.raw(method, path, body)
        assert status == 200, f"{method} {self.base}{path} -> {status} {payload}"
        return payload


def scrub(value):
    if isinstance(value, dict):
        return {k: scrub(v) for k, v in value.items() if k not in VOLATILE}
    if isinstance(value, list):
        return [scrub(v) for v in value]
    return value


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--go", required=True)
    p.add_argument("--ts", required=True)
    args = p.parse_args()
    go, ts = Client(args.go), Client(args.ts)

    # Accounts and sessions cross over: password hashes and sessions share one format.
    a, b = f"parity-go-{secrets.token_hex(4)}@example.test", f"parity-ts-{secrets.token_hex(4)}@example.test"
    go.ok("POST", "/api/auth/register", {"email": a, "password": "parity-password-1"})
    ts.ok("POST", "/api/auth/register", {"email": b, "password": "parity-password-1"})
    go_b, ts_a = Client(args.go), Client(args.ts)
    go_b.ok("POST", "/api/auth/login", {"email": b, "password": "parity-password-1"})
    ts_a.ok("POST", "/api/auth/login", {"email": a, "password": "parity-password-1"})
    mobile = Client(args.ts).ok("POST", "/api/auth/mobile/login", {"email": a, "password": "parity-password-1"})
    req = urllib.request.Request(args.go.rstrip("/") + "/api/auth/me", headers={"Authorization": "Bearer " + mobile["token"]})
    with urllib.request.urlopen(req, timeout=30) as resp:
        assert json.loads(resp.read())["user"]["email"] == a, "Worker bearer token rejected by Go"
    print("PASS: accounts, cookies and bearer tokens work across both servers")

    # Read-only responses are identical.
    for path in ("/api/nodes", "/api/features"):
        x, y = go.ok("GET", path), ts_a.ok("GET", path)
        assert x == y, f"{path} differs:\nGo: {json.dumps(x)[:400]}\nTS: {json.dumps(y)[:400]}"
    print("PASS: node catalog and feature flags match")

    # A workflow written by one server reads back identically from the other.
    graph = {
        "nodes": [
            {"id": "m", "type": "manual_trigger", "position": {"x": 1, "y": 2}, "config": {}},
            {"id": "s", "type": "set_fields", "position": {"x": 3, "y": 4}, "config": {"fields": {"who": "{{input.name}}"}}},
        ],
        "edges": [{"id": "e", "source": "m", "target": "s", "source_port": "out"}],
    }
    wf = go.ok("POST", "/api/workflows", {"name": "parity", "draft": graph})["workflow"]
    go.ok("POST", f"/api/workflows/{wf['id']}/publish")
    x, y = go.ok("GET", f"/api/workflows/{wf['id']}"), ts_a.ok("GET", f"/api/workflows/{wf['id']}")
    assert x == y, f"workflow differs:\nGo: {x}\nTS: {y}"
    run = ts_a.ok("POST", f"/api/workflows/{wf['id']}/run", {"input": {"name": "Ada"}})["run"]
    x, y = go.ok("GET", f"/api/runs/{run['id']}")["run"], ts_a.ok("GET", f"/api/runs/{run['id']}")["run"]
    assert x == y, f"run differs:\nGo: {x}\nTS: {y}"
    print("PASS: workflows and runs read back identically")

    # The same bad requests fail the same way.
    cases = [
        ("POST", "/api/workflows", {"name": "x", "bogus": 1}),
        ("POST", "/api/workflows", {"name": "x", "draft": {"nodes": [{"id": "a b", "type": "manual_trigger", "config": {}}], "edges": []}}),
        ("POST", "/api/workflows", {"name": "x", "draft": {"nodes": [{"id": "a", "type": "nope", "config": {}}], "edges": []}}),
        ("POST", "/api/workflows", {"name": "y" * 201}),
        ("POST", "/api/workflows", b"{}{}"),
        ("POST", "/api/workflows", b""),
        ("PUT", f"/api/workflows/{wf['id']}", {"name": "", "draft": graph}),
        ("POST", f"/api/workflows/{wf['id']}/activate", {"active": "yes"}),
        ("GET", "/api/workflows/does-not-exist", None),
        ("GET", "/api/runs/does-not-exist", None),
        ("POST", "/api/credentials", {"name": "", "kind": "k", "data": {}}),
        ("DELETE", "/api/credentials/does-not-exist", None),
        ("POST", f"/api/runs/{run['id']}/replay", {"from_node": "nope"}),
        ("GET", "/api/nope", None),
    ]
    bad = []
    for method, path, body in cases:
        gs, gb = go.raw(method, path, body)
        tsv, tb = ts_a.raw(method, path, body)
        if gs != tsv or (gs != 500 and scrub(gb) != scrub(tb)):
            bad.append(f"{method} {path}: Go {gs} {gb} / TS {tsv} {tb}")
    assert not bad, "error responses differ:\n" + "\n".join(bad)
    print(f"PASS: {len(cases)} error cases answer with the same status and message")
    print("API parity checks passed.")


if __name__ == "__main__":
    try:
        main()
    except AssertionError as err:
        print(f"FAIL: {err}")
        sys.exit(1)
