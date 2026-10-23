"""Turn results.json into LaTeX macros and table bodies for the paper."""
import json, sys, os
res = json.load(open(sys.argv[1])); out = sys.argv[2]
def f(x, d=1): return f"{x:,.{d}f}"
def esc(s): return s.replace("_", "\\_").replace("%", "\\%").replace("&", "\\&").replace("#", "\\#")
L = res["latency"]; R = res["replay"]; T = res["throughput"]; F = res["fault"]; S = res["seo"]
m = []
m.append(r"\newcommand{\LATn}{%d}" % L["runs"])
m.append(r"\newcommand{\LATmedian}{%s}" % f(L["wall_ms"]["median"], 0))
m.append(r"\newcommand{\LATpNN}{%s}" % f(L["wall_ms"]["p95"], 0))
m.append(r"\newcommand{\LATstepsum}{%s}" % f(L["step_sum_ms"]["median"], 0))
levels = sorted(T, key=int)
for name, lv in zip(("one", "four", "eight"), levels):
    m.append(r"\newcommand{\THR%s}{%s}" % (name, f(T[lv]["runs_per_s"], 1)))
m.append(r"\newcommand{\THRn}{%d}" % T[levels[0]]["runs"])
m.append(r"\newcommand{\THRenq}{%s}" % f(T[levels[0]]["enqueue_s"], 1))
m.append(r"\newcommand{\THRbest}{%s}" % f(max(T[l]["runs_per_s"] for l in levels), 1))
m.append(r"\newcommand{\REPreps}{%d}" % R["reps"])
m.append(r"\newcommand{\REPcopy}{%d}" % round(R["steps_copied"]["median"]))
m.append(r"\newcommand{\REPexec}{%d}" % round(R["steps_executed"]["median"]))
rec = F["POST_no_key"]["seconds_kill_to_terminal"] - 60
m.append(r"\newcommand{\FAULTrecover}{%s}" % f(max(rec, 0), 1))
open(os.path.join(out, "numbers.tex"), "w").write("\n".join(m) + "\n")

rows = []
w = L["wall_ms"]; s = L["step_sum_ms"]
rows.append(f"End-to-end wall time (ms) & {f(w['median'])} & {f(w['mean'])} & {f(w['p95'])} & {f(w['max'])} \\\\")
rows.append(f"Recorded step time, sum per run (ms) & {f(s['median'])} & {f(s['mean'])} & {f(s['p95'])} & {f(s['max'])} \\\\")
order = ["trigger", "normalize", "threshold", "known_customer", "approved", "invalid_customer", "review"]
for n in order:
    if n in L["per_node_ms"]:
        v = L["per_node_ms"][n]
        rows.append(f"\\quad step: {esc(n)} (ms, n={v['n']}) & {f(v['median'],2)} & {f(v['mean'],2)} & {f(v['p95'],2)} & {f(v['max'],2)} \\\\")
open(os.path.join(out, "latency_table.tex"), "w").write("\n".join(rows) + "\n\\bottomrule\n")

rows = [f"{lv} & {f(T[lv]['makespan_s'],2)} & {f(T[lv]['runs_per_s'],1)} & {T[lv]['succeeded']}/{T[lv]['runs']} \\\\" for lv in levels]
open(os.path.join(out, "throughput_table.tex"), "w").write("\n".join(rows) + "\n\\bottomrule\n")

rows = [f"Steps copied from parent (median) & 0 & {f(R['steps_copied']['median'],0)} \\\\",
        f"Steps executed (median) & {R['chain_nodes']} & {f(R['steps_executed']['median'],0)} \\\\",
        f"Wall time, median (ms) & {f(R['full_wall_ms']['median'])} & {f(R['replay_wall_ms']['median'])} \\\\",
        f"Wall time, p95 (ms) & {f(R['full_wall_ms']['p95'])} & {f(R['replay_wall_ms']['p95'])} \\\\"]
open(os.path.join(out, "replay_table.tex"), "w").write("\n".join(rows) + "\n\\bottomrule\n")

rows = []
for r in S:
    url = r["url"].replace("http://127.0.0.1:18090", "mock").replace("https://", "").replace("www.", "").rstrip("/")
    if r["kind"] == "mock" and "/flaky/" in url: url = "mock/flaky (500,500,200)"
    st = r["http_status"] or (r["attempts"][-1][0] if r["attempts"] else "--")
    ms = r["fetch_ms"] if r["fetch_ms"] is not None else "--"
    rows.append(f"{esc(url)} & {st} & {len(r['attempts'])} & {ms} & {esc(str(r['verdict']))} \\\\")
open(os.path.join(out, "seo_table.tex"), "w").write("\n".join(rows) + "\n\\bottomrule\n")

labels = {"GET_slow": "GET (idempotent by method)", "POST_no_key": "POST, no idempotency key", "POST_idempotency_key": "POST with idempotency key"}
rows = []
for k, v in F.items():
    call = [s for s in v["steps"] if s["node"] == "call"]
    att = max((s["attempt"] for s in call), default=0)
    last = call[-1]["status"] if call else "--"
    rows.append(f"{labels[k]} & \\emph{{{v['final_status']}}} & {f(v['seconds_kill_to_terminal'])} & call: {last}, attempt {att}{'; ' + esc(v['error']) if v['error'] else ''} \\\\")
open(os.path.join(out, "fault_table.tex"), "w").write("\n".join(rows) + "\n\\bottomrule\n")
print("tables written")
