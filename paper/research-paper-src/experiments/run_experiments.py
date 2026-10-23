"""Measurement harness for the n9n research paper. Standard library only.

Usage: python3 -I run_experiments.py --project <repo> --backend-bin <path> --runner-bin <path> --out <dir>
Starts the Go backend on :18080 against the n9n_paper database, the mock site on :18090,
and manages the Rust runner process itself (restarting it with different concurrency).
"""
import argparse, http.cookiejar, json, os, secrets, signal, statistics, subprocess, sys, time, urllib.error, urllib.request

BASE = "http://127.0.0.1:18080"
SITE = "http://127.0.0.1:18090"

def read_env(path):
    env = {}
    for line in open(path):
        line = line.strip()
        if line and not line.startswith("#") and "=" in line:
            k, v = line.split("=", 1)
            env[k] = v.strip().strip('"')
    return env

class Client:
    def __init__(self, base):
        self.base = base
        self.creds = None
        self.relogins = 0
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    def call(self, method, path, body=None, headers=None, expected=200, _retry=True):
        status, payload = self._do(method, path, body, headers)
        if status == 401 and expected != 401 and self.creds and _retry:
            self.relogins += 1
            self._do("POST", "/api/auth/login", self.creds, None)
            status, payload = self._do(method, path, body, headers)
        assert status == expected, f"{method} {path}: {status} wanted {expected}: {payload}"
        return payload
    def _do(self, method, path, body, headers):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.base + path, data=data, method=method,
                                     headers={"Content-Type": "application/json", **(headers or {})})
        try:
            with self.opener.open(req, timeout=30) as r:
                return r.status, json.load(r)
        except urllib.error.HTTPError as e:
            return e.code, json.load(e)

def node(id_, kind, config=None):
    return {"id": id_, "type": kind, "position": {"x": 40, "y": 40}, "config": config or {}}
def edge(id_, s, t, port="out"):
    return {"id": id_, "source": s, "target": t, "source_port": port}

TERMINAL = {"succeeded", "failed", "cancelled", "uncertain"}

def wait_run(c, rid, timeout=120, poll=0.02):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        d = c.call("GET", f"/api/runs/{rid}")
        if d["run"]["status"] in TERMINAL:
            return d
        time.sleep(poll)
    raise AssertionError(f"run {rid} did not finish")

def wait_all(c, rids, timeout=600, poll=0.25):
    deadline = time.monotonic() + timeout
    pending = set(rids)
    while pending and time.monotonic() < deadline:
        for rid in list(pending):
            if c.call("GET", f"/api/runs/{rid}")["run"]["status"] in TERMINAL:
                pending.discard(rid)
        if pending:
            time.sleep(poll)
    assert not pending, f"{len(pending)} runs unfinished"

def parse_ts(t):
    # backend layout 2006-01-02T15:04:05.000000000Z (nanoseconds)
    import datetime
    base, frac = t[:19], t[20:-1]
    dt = datetime.datetime.strptime(base, "%Y-%m-%dT%H:%M:%S").replace(tzinfo=datetime.timezone.utc)
    return dt.timestamp() + int(frac.ljust(9, "0")[:9]) / 1e9

def pct(xs, p):
    xs = sorted(xs)
    k = (len(xs) - 1) * p
    f, c = int(k), min(int(k) + 1, len(xs) - 1)
    return xs[f] + (xs[c] - xs[f]) * (k - f)

def summary(xs):
    return {"n": len(xs), "mean": statistics.mean(xs), "median": statistics.median(xs),
            "p95": pct(xs, 0.95), "min": min(xs), "max": max(xs), "stdev": statistics.pstdev(xs) if len(xs) > 1 else 0.0}

class Procs:
    def __init__(self, args, env):
        self.args, self.env, self.runner = args, env, None
        self.log = open(os.path.join(args.out, "processes.log"), "a")
    def start_backend(self):
        e = dict(os.environ, DATABASE_URL="postgres://n9n:n9n_local_dev@127.0.0.1:55432/n9n_paper?sslmode=disable",
                 ENCRYPTION_KEY=self.env["ENCRYPTION_KEY"], RUNNER_TOKEN=self.env["RUNNER_TOKEN"],
                 ADDR=":18080", ALLOWED_ORIGIN="http://127.0.0.1:18080", COOKIE_SECURE="false")
        self.backend = subprocess.Popen([self.args.backend_bin], env=e, stdout=self.log, stderr=subprocess.STDOUT)
        for _ in range(100):
            try:
                urllib.request.urlopen(BASE + "/api/health", timeout=2); return
            except Exception:
                time.sleep(0.2)
        raise SystemExit("backend did not start")
    def start_site(self):
        self.site = subprocess.Popen([sys.executable, "-I", os.path.join(os.path.dirname(__file__), "mock_site.py"), "18090"], stdout=self.log, stderr=subprocess.STDOUT)
        for _ in range(50):
            try:
                urllib.request.urlopen(SITE + "/ok", timeout=2); return
            except Exception:
                time.sleep(0.1)
        raise SystemExit("site did not start")
    def start_runner(self, concurrency=4, poll_ms=250):
        self.stop_runner()
        e = dict(os.environ, BACKEND_URL=BASE, RUNNER_TOKEN=self.env["RUNNER_TOKEN"], RUNNER_ID=f"runner-c{concurrency}",
                 RUNNER_POLL_MS=str(poll_ms), RUNNER_CONCURRENCY=str(concurrency), ALLOW_PRIVATE_HTTP="true")
        self.runner = subprocess.Popen([self.args.runner_bin], env=e, stdout=self.log, stderr=subprocess.STDOUT)
        time.sleep(1.0)
    def stop_runner(self, kill=False):
        if self.runner and self.runner.poll() is None:
            self.runner.send_signal(signal.SIGKILL if kill else signal.SIGTERM)
            try:
                self.runner.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.runner.kill(); self.runner.wait()
        self.runner = None
    def stop_all(self):
        self.stop_runner()
        for p in (getattr(self, "backend", None), getattr(self, "site", None)):
            if p and p.poll() is None:
                p.terminate()
                try: p.wait(timeout=10)
                except subprocess.TimeoutExpired: p.kill()

def branching_graph():
    return {"nodes": [
        node("trigger", "manual_trigger"),
        node("normalize", "set_fields", {"fields": {"customer": "{{input.customer}}", "score": "{{input.score}}"}}),
        node("threshold", "condition", {"left": "{{nodes.normalize.score}}", "operator": "greater_than", "right": 5}),
        node("known_customer", "condition", {"left": "{{nodes.normalize.customer}}", "operator": "contains", "right": "@"}),
        node("approved", "set_fields", {"fields": {"decision": "approved", "recipient": "{{nodes.normalize.customer}}"}}),
        node("invalid_customer", "set_fields", {"fields": {"decision": "invalid"}}),
        node("review", "set_fields", {"fields": {"decision": "review"}}),
    ], "edges": [
        edge("a", "trigger", "normalize"), edge("b", "normalize", "threshold"),
        edge("c", "threshold", "known_customer", "true"), edge("d", "threshold", "review", "false"),
        edge("e", "known_customer", "approved", "true"), edge("f", "known_customer", "invalid_customer", "false"),
    ]}

def chain_graph(n):
    nodes = [node("trigger", "manual_trigger")]
    edges = []
    prev = "trigger"
    for i in range(1, n + 1):
        nid = f"s{i}"
        nodes.append(node(nid, "set_fields", {"fields": {f"f{i}": f"value-{i}", "last": nid}}))
        edges.append(edge(f"e{i}", prev, nid)); prev = nid
    return {"nodes": nodes, "edges": edges}

def seo_graph():
    ua = {"User-Agent": "n9n-seo-audit/0.1 (research prototype)"}
    return {"nodes": [
        node("trigger", "webhook_trigger", {"secret": "seo-secret-0123456789"}),
        node("fetch", "http_request", {"url": "{{input.url}}", "method": "GET", "headers": ua}),
        node("has_title", "condition", {"left": "{{nodes.fetch.body}}", "operator": "contains", "right": "<title>"}),
        node("pass", "set_fields", {"fields": {"verdict": "pass", "url": "{{nodes.trigger.url}}", "status": "{{nodes.fetch.status}}"}}),
        node("no_title", "set_fields", {"fields": {"verdict": "missing_title", "url": "{{nodes.trigger.url}}", "status": "{{nodes.fetch.status}}"}}),
    ], "edges": [
        edge("a", "trigger", "fetch"), edge("b", "fetch", "has_title"),
        edge("e", "has_title", "pass", "true"), edge("f", "has_title", "no_title", "false"),
    ]}

def http_graph(url, method="GET", idem=None):
    cfg = {"url": url, "method": method}
    if method == "POST":
        cfg["body"] = {"hello": "world"}
    if idem:
        cfg["idempotency_key"] = idem
    return {"nodes": [node("trigger", "manual_trigger"), node("call", "http_request", cfg),
                      node("after", "set_fields", {"fields": {"status": "{{nodes.call.status}}"}})],
            "edges": [edge("a", "trigger", "call"), edge("b", "call", "after")]}

def new_workflow(c, name, graph, publish=True):
    wid = c.call("POST", "/api/workflows", {"name": name, "draft": graph})["workflow"]["id"]
    if publish:
        c.call("POST", f"/api/workflows/{wid}/publish")
    return wid

def exp_latency(c, n=50):
    wid = new_workflow(c, "paper latency", branching_graph())
    walls, sums, counts, per_node = [], [], [], {}
    for i in range(n):
        inp = {"customer": "ada@example.com" if i % 3 else "ada", "score": 9 if i % 2 else 2}
        t0 = time.monotonic()
        rid = c.call("POST", f"/api/workflows/{wid}/run", {"input": inp})["run"]["id"]
        d = wait_run(c, rid)
        walls.append((time.monotonic() - t0) * 1000)
        assert d["run"]["status"] == "succeeded", d
        executed = [s for s in d["steps"] if s["status"] == "succeeded"]
        sums.append(sum(s.get("duration_ms") or 0 for s in executed))
        counts.append(len(d["steps"]))
        for s in executed:
            per_node.setdefault(s["node_id"], []).append(s.get("duration_ms") or 0)
    return {"runs": n, "wall_ms": summary(walls), "step_sum_ms": summary(sums), "steps_recorded": summary(counts),
            "per_node_ms": {k: summary(v) for k, v in per_node.items()}, "raw_wall_ms": walls}

def exp_throughput(c, procs, n=100, levels=(1, 4, 8)):
    wid = new_workflow(c, "paper throughput", branching_graph())
    out = {}
    for conc in levels:
        procs.stop_runner()   # queue the burst with no runner so the burst is observed as a whole
        rids = []
        t_enq0 = time.monotonic()
        for i in range(n):
            rids.append(c.call("POST", f"/api/workflows/{wid}/run", {"input": {"customer": "a@b", "score": 9}})["run"]["id"])
        enqueue_s = time.monotonic() - t_enq0
        t0 = time.monotonic()
        procs.start_runner(concurrency=conc)
        wait_all(c, rids)
        client_span = time.monotonic() - t0
        statuses, starts, ends, claimed_by = [], [], [], set()
        for r in rids:
            d = c.call("GET", f"/api/runs/{r}")
            statuses.append(d["run"]["status"]); claimed_by.add(d["run"].get("runner_id"))
            for st in d["steps"]:
                if st.get("started_at"): starts.append(parse_ts(st["started_at"]))
                if st.get("finished_at"): ends.append(parse_ts(st["finished_at"]))
        makespan = max(ends) - min(starts)   # first step started to last step finished, from the database records
        out[str(conc)] = {"runs": n, "enqueue_s": enqueue_s, "makespan_s": makespan, "runs_per_s": n / makespan,
                          "client_span_s": client_span, "succeeded": statuses.count("succeeded"), "runner_ids": sorted(x for x in claimed_by if x)}
    procs.start_runner(concurrency=4)
    return out

def exp_replay(c, chain_len=8, from_idx=5, reps=10):
    wid = new_workflow(c, "paper replay", chain_graph(chain_len))
    fulls, replays, copied, executed = [], [], [], []
    for _ in range(reps):
        t0 = time.monotonic()
        rid = c.call("POST", f"/api/workflows/{wid}/run", {"input": {"seed": 1}})["run"]["id"]
        d = wait_run(c, rid); fulls.append((time.monotonic() - t0) * 1000)
        assert d["run"]["status"] == "succeeded"
        t1 = time.monotonic()
        rep = c.call("POST", f"/api/runs/{rid}/replay", {"from_node": f"s{from_idx}", "input_override": {"seed": 2, "edited": True}})["run"]
        rd = wait_run(c, rep["id"]); replays.append((time.monotonic() - t1) * 1000)
        assert rd["run"]["status"] == "succeeded", rd
        assert rd["run"]["kind"] == "replay" and rd["run"]["parent_run_id"] == rid
        cp = [s for s in rd["steps"] if s["status"] == "succeeded" and not s.get("started_at")]
        ex = [s for s in rd["steps"] if s["status"] == "succeeded" and s.get("started_at")]
        copied.append(len(cp)); executed.append(len(ex))
        assert rd["steps"] and all(s["node_id"] != f"s{from_idx}" for s in cp)
    return {"chain_nodes": chain_len + 1, "replay_from": f"s{from_idx}", "reps": reps,
            "full_wall_ms": summary(fulls), "replay_wall_ms": summary(replays),
            "steps_copied": summary(copied), "steps_executed": summary(executed)}

def exp_seo(c, public_urls):
    wid = new_workflow(c, "paper seo audit", seo_graph())
    c.call("POST", f"/api/workflows/{wid}/activate", {"active": True})
    hdr = {"X-Webhook-Secret": "seo-secret-0123456789"}
    targets = [("mock", SITE + "/ok"), ("mock", SITE + "/notitle"), ("mock", SITE + "/missing"),
               ("mock", SITE + "/moved"), ("mock", SITE + "/flaky/" + secrets.token_hex(3)),
               ("mock", SITE + "/robots.txt"), ("mock", SITE + "/sitemap.xml")] + [("public", u) for u in public_urls]
    rows = []
    for kind, url in targets:
        rid = c.call("POST", f"/api/hooks/{wid}", {"url": url, "event": secrets.token_hex(4)}, hdr)["run"]["id"]
        d = wait_run(c, rid, timeout=90)
        steps = {s["node_id"]: s for s in d["steps"]}
        fetch = steps.get("fetch", {})
        verdict = next((steps[k]["output"].get("verdict") for k in ("pass", "no_title") if steps.get(k, {}).get("status") == "succeeded"), None)
        if verdict is None and fetch.get("status") == "failed":
            verdict = "fetch_failed"
        rows.append({"kind": kind, "url": url, "run_status": d["run"]["status"], "fetch_status": fetch.get("status"),
                     "http_status": (fetch.get("output") or {}).get("status") if isinstance(fetch.get("output"), dict) else None,
                     "fetch_ms": fetch.get("duration_ms"), "attempts": [(a.get("http_status"), a.get("status"), a.get("duration_ms")) for a in (fetch.get("attempts") or [])],
                     "verdict": verdict, "error": (fetch.get("error") or "")[:120],
                     "skipped": sorted(k for k, s in steps.items() if s["status"] == "skipped")})
    return rows

def exp_fault(c, procs):
    """Kill the runner mid-step; watch what the backend does after the 60 s lease expires."""
    cases = [("GET_slow", http_graph(SITE + "/slow", "GET", None)),
             ("POST_no_key", http_graph(SITE + "/post-slow", "POST", None)),
             ("POST_idempotency_key", http_graph(SITE + "/post-slow", "POST", "{{input.event}}"))]
    out = {}
    for name, g in cases:
        wid = new_workflow(c, f"paper fault {name}", g)
        procs.start_runner(concurrency=1)
        t0 = time.monotonic()
        rid = c.call("POST", f"/api/workflows/{wid}/run", {"input": {"event": secrets.token_hex(4)}})["run"]["id"]
        # wait until the call step is recorded as running, then kill the runner hard
        for _ in range(200):
            d = c.call("GET", f"/api/runs/{rid}")
            if any(s["node_id"] == "call" and s["status"] == "running" for s in d["steps"]):
                break
            time.sleep(0.05)
        t_kill = time.monotonic()
        procs.stop_runner(kill=True)
        time.sleep(0.5)
        procs.start_runner(concurrency=1)   # a fresh runner; it can only claim after the lease expires
        d = wait_run(c, rid, timeout=200, poll=0.25)
        t_end = time.monotonic()
        steps = [{"node": s["node_id"], "status": s["status"], "attempt": s["attempt"]} for s in d["steps"]]
        out[name] = {"final_status": d["run"]["status"], "error": d["run"].get("error", ""),
                     "seconds_kill_to_terminal": t_end - t_kill, "seconds_total": t_end - t0, "steps": steps,
                     "runner_ids_seen": sorted({s.get("runner_id") for s in [d["run"]] if s.get("runner_id")})}
    procs.start_runner(concurrency=4)
    return out

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--project", required=True); ap.add_argument("--backend-bin", required=True)
    ap.add_argument("--runner-bin", required=True); ap.add_argument("--out", required=True)
    ap.add_argument("--only", default="")
    ap.add_argument("--public", default="https://example.com/,https://www.iana.org/,https://www.python.org/,https://en.wikipedia.org/wiki/Main_Page,https://www.rust-lang.org/")
    args = ap.parse_args()
    os.makedirs(args.out, exist_ok=True)
    env = read_env(os.path.join(args.project, ".env"))
    procs = Procs(args, env)
    rpath = os.path.join(args.out, "results.json")
    results = json.load(open(rpath)) if (args.only and os.path.exists(rpath)) else {}
    results.update({"started_at_utc": results.get("started_at_utc") or time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                    "machine": os.uname().machine, "os": os.uname().sysname + " " + os.uname().release})
    try:
        procs.start_backend(); procs.start_site(); procs.start_runner(concurrency=4)
        c = Client(BASE)
        c.creds = {"email": f"paper-{secrets.token_hex(4)}@example.test", "password": "paper-password-123"}
        c.call("POST", "/api/auth/register", c.creds)
        results["features"] = c.call("GET", "/api/features")
        results["catalog_version"] = c.call("GET", "/api/nodes").get("version")
        results["node_types"] = [n["type"] for n in c.call("GET", "/api/nodes")["nodes"]]
        only = set(args.only.split(",")) if args.only else None
        def want(k): return only is None or k in only
        def save():
            results["relogins"] = c.relogins
            with open(os.path.join(args.out, "results.json"), "w") as f:
                json.dump(results, f, indent=2)
        if want("latency"):
            print("latency...", flush=True); results["latency"] = exp_latency(c); save()
        if want("replay"):
            print("replay...", flush=True); results["replay"] = exp_replay(c); save()
        if want("seo"):
            print("seo...", flush=True); results["seo"] = exp_seo(c, [u for u in args.public.split(",") if u]); save()
        if want("throughput"):
            print("throughput...", flush=True); results["throughput"] = exp_throughput(c, procs); save()
        if want("fault"):
            print("fault...", flush=True); results["fault"] = exp_fault(c, procs); save()
        results["finished_at_utc"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    finally:
        procs.stop_all()
    with open(os.path.join(args.out, "results.json"), "w") as f:
        json.dump(results, f, indent=2)
    print(json.dumps({k: v for k, v in results.items() if k not in ("latency",)}, indent=1)[:4000])
    print("DONE")

if __name__ == "__main__":
    main()
