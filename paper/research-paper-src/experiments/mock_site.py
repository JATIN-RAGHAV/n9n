"""Mock web site for the n9n technical-SEO and fault-injection experiments.
Routes:
  /ok            200 HTML page with title and meta description
  /notitle       200 HTML page without <title>
  /missing       404
  /moved         301 -> /ok (n9n does not follow redirects, so the node sees 301)
  /flaky/<key>   500 on the first two GETs for that key, then 200
  /slow          200 after 8 s (GET)   -- used for lease-loss recovery
  /post-slow     200 after 8 s (POST)  -- used for the uncertain-run case
  /robots.txt    200 text
  /sitemap.xml   200 xml
"""
import json, sys, time, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

flaky = {}
lock = threading.Lock()

class H(BaseHTTPRequestHandler):
    def log_message(self, *a):  # quiet
        pass
    def _send(self, code, body, ctype="text/html; charset=utf-8", headers=None):
        b = body.encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(b)))
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(b)
    def do_GET(self):
        p = self.path
        if p == "/ok":
            self._send(200, "<html><head><title>n9n test page</title><meta name='description' content='A page with a title and description.'></head><body><h1>Hello</h1></body></html>")
        elif p == "/notitle":
            self._send(200, "<html><head></head><body><h1>No title here</h1></body></html>")
        elif p == "/missing":
            self._send(404, "<html><title>404</title></html>")
        elif p == "/moved":
            self._send(301, "", headers={"Location": "/ok"})
        elif p.startswith("/flaky/"):
            with lock:
                n = flaky.get(p, 0) + 1
                flaky[p] = n
            if n <= 2:
                self._send(500, "transient failure %d" % n)
            else:
                self._send(200, "<html><head><title>Recovered page</title></head><body>served after %d requests</body></html>" % n)
        elif p == "/slow":
            time.sleep(8)
            self._send(200, json.dumps({"slow": True}), "application/json")
        elif p == "/robots.txt":
            self._send(200, "User-agent: *\nAllow: /\nSitemap: /sitemap.xml\n", "text/plain")
        elif p == "/sitemap.xml":
            self._send(200, "<?xml version='1.0'?><urlset><url><loc>/ok</loc></url></urlset>", "application/xml")
        else:
            self._send(404, "nope")
    def do_HEAD(self):
        self.do_GET()
    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        self.rfile.read(n)
        if self.path == "/post-slow":
            time.sleep(8)
            self._send(200, json.dumps({"posted": True}), "application/json")
        elif self.path == "/post-ok":
            self._send(200, json.dumps({"posted": True}), "application/json")
        else:
            self._send(404, "nope")

if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18090
    ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
