"""Serves a goreleaser dist/ directory the way GitHub serves a release,
so CI can run the install scripts against it:

  /releases/latest              302 to /releases/tag/v0.0.0
  /releases/tag/v0.0.0          200
  /releases/download/v0.0.0/F   dist/F

Usage: python release-server.py DIST PORT
"""

import http.server
import os
import sys

DIST, PORT = sys.argv[1], int(sys.argv[2])
TAG = "v0.0.0"


class Release(http.server.BaseHTTPRequestHandler):
    def do_HEAD(self):
        self.respond(body=False)

    def do_GET(self):
        self.respond(body=True)

    def respond(self, body):
        prefix = f"/releases/download/{TAG}/"
        if self.path == "/releases/latest":
            self.send_response(302)
            self.send_header("Location", f"/releases/tag/{TAG}")
            self.send_header("Content-Length", "0")
            self.end_headers()
        elif self.path == f"/releases/tag/{TAG}":
            self.send_response(200)
            self.send_header("Content-Length", "0")
            self.end_headers()
        elif self.path.startswith(prefix) and "/" not in self.path[len(prefix):]:
            path = os.path.join(DIST, self.path[len(prefix):])
            if not os.path.isfile(path):
                self.send_error(404)
                return
            with open(path, "rb") as f:
                data = f.read()
            self.send_response(200)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            if body:
                self.wfile.write(data)
        else:
            self.send_error(404)


http.server.ThreadingHTTPServer(("127.0.0.1", PORT), Release).serve_forever()
