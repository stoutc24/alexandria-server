#!/usr/bin/env python3
"""A small Audiobookshelf for the provisioning page test. TEST-ONLY.

Answers the calls internal/provision makes (POST /login, GET /api/users,
GET /api/libraries, POST /api/users, PATCH /api/users/<id>) for the admin
"admin" / the password in ABS_ADMIN_PASSWORD, and GET /__state with what it
holds, for run.sh to check. Listens on 127.0.0.1:$PORT. Nothing leaves this
machine.
"""
import json
import os
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ADMIN_PASSWORD = os.environ["ABS_ADMIN_PASSWORD"]
lock = threading.Lock()
# A test account an earlier "Test accounts" run left behind: the next run
# deletes it first.
users = [{"id": "root-1", "username": "root", "type": "root"},
         {"id": "left-1", "username": "alexandria-selftest-old111", "type": "user"}]
passwords = {"alexandria-selftest-old111": "left-over-pass"}
# Ids are never reused (deletes shorten the list).
next_id = [10]
# One name carries markup, which the page must show as text.
libraries = [{"id": "lib-books", "name": "Audiobooks", "mediaType": "book"},
             {"id": "lib-pods", "name": "Podcasts <b>", "mediaType": "podcast"}]


class Handler(BaseHTTPRequestHandler):
    def _send(self, status, obj):
        body = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return json.loads(self.rfile.read(n) or b"{}") if n else {}

    def _authed(self):
        return self.headers.get("Authorization") == "Bearer stub-jwt"

    def do_GET(self):
        with lock:
            if self.path == "/__state":
                return self._send(200, {"users": users, "passwords": passwords})
            if self.path == "/api/users" and self._authed():
                return self._send(200, {"users": users})
            if self.path == "/api/libraries" and self._authed():
                return self._send(200, {"libraries": libraries})
        self._send(401 if self.path.startswith("/api/") else 404, {"error": "no"})

    def do_POST(self):
        body = self._body()
        with lock:
            if self.path == "/login":
                if body.get("username") == "admin" and body.get("password") == ADMIN_PASSWORD:
                    return self._send(200, {"user": {"accessToken": "stub-jwt"}})
                # An account made here signs in with its own password.
                if body.get("username") in passwords and passwords[body["username"]] == body.get("password"):
                    return self._send(200, {"user": {"accessToken": "user-jwt"}})
                return self._send(401, {"error": "Unauthorized"})
            if self.path == "/api/users" and self._authed():
                if any(u["username"].lower() == body["username"].lower() for u in users):
                    return self._send(400, {"error": "Username already taken"})
                uid = "u-%d" % next_id[0]
                next_id[0] += 1
                perms = body.get("permissions") or {}
                users.append({"id": uid, "username": body["username"], "type": body.get("type"),
                              "isActive": body.get("isActive"), "permissions": perms,
                              "librariesAccessible": body.get("librariesAccessible")})
                passwords[body["username"]] = body["password"]
                return self._send(200, {"user": {"id": uid}})
        self._send(404, {"error": "no"})

    def do_PATCH(self):
        body = self._body()
        with lock:
            if self.path.startswith("/api/users/") and self._authed():
                uid = self.path.rsplit("/", 1)[1]
                for u in users:
                    if u["id"] == uid:
                        passwords[u["username"]] = body["password"]
                        return self._send(200, {"success": True})
        self._send(404, {"error": "no"})

    def do_DELETE(self):
        with lock:
            if self.path.startswith("/api/users/") and self._authed():
                uid = self.path.rsplit("/", 1)[1]
                for u in list(users):
                    if u["id"] == uid:
                        users.remove(u)
                        passwords.pop(u["username"], None)
                        return self._send(200, {"success": True})
        self._send(404, {"error": "no"})

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", int(os.environ["PORT"])), Handler).serve_forever()
