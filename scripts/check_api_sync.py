"""Fails when the Go API and the TypeScript Worker API disagree on the schema.

The two servers share one PostgreSQL database, so their migration lists must be
identical statement for statement: backend/main.go (Go raw strings) and
cloudflare/src/api/db.ts (template literals). Run: python3 scripts/check_api_sync.py
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent


def go_migrations():
    text = (ROOT / "backend/main.go").read_text()
    blocks = dict(re.findall(r"var (schema|migration\d+) = \[\]string\{(.*?)\n\}", text, re.S))
    order = re.search(r"var migrations = \[\]\[\]string\{([^}]*)\}", text).group(1)
    return [re.findall(r"`([^`]*)`", blocks[name.strip()]) for name in order.split(",")]


def ts_migrations():
    text = (ROOT / "cloudflare/src/api/db.ts").read_text()
    blocks = dict(re.findall(r"const (schema|migration\d+) = \[(.*?)\n\];", text, re.S))
    order = re.search(r"export const migrations: string\[\]\[\] = \[([^\]]*)\]", text).group(1)
    return [re.findall(r"`([^`]*)`", blocks[name.strip()]) for name in order.split(",")]


def main():
    go, ts = go_migrations(), ts_migrations()
    problems = []
    if len(go) != len(ts):
        problems.append(f"Go has {len(go)} migrations, TypeScript has {len(ts)}")
    for i, (a, b) in enumerate(zip(go, ts), start=1):
        if a != b:
            for j in range(max(len(a), len(b))):
                x = a[j] if j < len(a) else "<missing>"
                y = b[j] if j < len(b) else "<missing>"
                if x != y:
                    problems.append(f"migration {i} statement {j + 1} differs:\n  Go: {x}\n  TS: {y}")
                    break
    if problems:
        print("API schema out of sync:\n" + "\n".join(problems))
        return 1
    print(f"API schema in sync: {len(go)} migrations, {sum(map(len, go))} statements")
    return 0


if __name__ == "__main__":
    sys.exit(main())
