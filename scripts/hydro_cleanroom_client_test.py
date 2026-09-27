"""Exercise the public example as a real CLI against a synthetic local service."""

from __future__ import annotations

import contextlib
import http.server
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest

ROOT = Path(__file__).resolve().parents[1]
CLIENT = ROOT / "examples" / "hydro_cleanroom_client.py"
PASSWORD = "synthetic-only-password"
COOKIE = "qraft_session=synthetic-session"
CSRF = "synthetic-csrf"
ZIP = b"PK\x03\x04synthetic-zip"


@contextlib.contextmanager
def service(*, account=True, fail="", redirect="", destination=""):
    requests = []
    violations = []

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def reply(self, status, value, headers=None, binary=False):
            body = value if binary else json.dumps(value).encode()
            self.send_response(status)
            for name, value in (headers or {}).items():
                self.send_header(name, value)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def handle_request(self):
            body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            requests.append((self.command, self.path, dict(self.headers), body))
            if self.path == redirect:
                self.reply(302, {}, {"Location": destination})
                return
            if self.headers.get("X-Qraft-Client") != "1":
                violations.append("missing required client header")
                self.reply(403, {})
                return
            if self.path == "/api/v1/auth/login":
                if json.loads(body) != {"email": "member@example.test", "password": PASSWORD}:
                    violations.append("incorrect login payload")
                if self.path == fail:
                    self.reply(401, {"error": "synthetic login failure"})
                else:
                    self.reply(200, {"data": {"csrf_token": CSRF}}, {
                        "Set-Cookie": COOKIE + "; Path=/api/v1; HttpOnly; SameSite=Strict",
                    })
                return
            if account:
                if self.headers.get("Cookie") != COOKIE:
                    violations.append("missing session cookie")
                if self.command == "POST" and self.headers.get("X-CSRF-Token") != CSRF:
                    violations.append("missing CSRF token")
            if self.path == fail:
                self.reply(500, {"error": "synthetic business failure"})
            elif self.path == "/api/v1/auth/logout":
                self.reply(200, {"data": {"authenticated": False}}, {
                    "Set-Cookie": "qraft_session=; Path=/api/v1; Max-Age=0",
                })
            elif self.path == "/api/v1/problems/generate":
                self.reply(200, {"data": {"workflow_id": "generation-test"}})
            elif self.path == "/api/v1/problems/test-problem/validate":
                self.reply(200, {"data": {"workflow_id": "validation-test"}})
            elif self.path.startswith("/api/v1/workflows/"):
                self.reply(200, {"data": {"execution_status": "Completed", "state": {"problem_id": "test-problem"}}})
            elif self.path == "/api/v1/problems/test-problem/hydro.zip":
                self.reply(200, ZIP, binary=True)
            elif self.path == "/api/v1/problems/hydro/validate":
                if ZIP not in body or b'name="file"' not in body:
                    violations.append("incorrect multipart archive")
                self.reply(200, {"data": {"valid": True}})
            else:
                self.reply(404, {"error": "unexpected synthetic route"})

        do_GET = handle_request
        do_POST = handle_request

    server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01})
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}/api/v1", requests, violations
    finally:
        server.shutdown()
        thread.join(timeout=5)
        server.server_close()
        if thread.is_alive():
            raise RuntimeError("synthetic HTTP server did not stop")


class ClientTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.output = Path(self.temp.name)

    def run_cli(self, base_url, *command, account=True, extra=()):
        env = os.environ.copy()
        for name in ("QRAFT_EMAIL", "QRAFT_PASSWORD", "ALGOFORGE_TOKEN",
                     "QRAFT_API_BASE_URL", "ALGOFORGE_API_BASE_URL",
                     "http_proxy", "https_proxy", "all_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"):
            env.pop(name, None)
        env["NO_PROXY"] = "*"
        if account:
            env["QRAFT_PASSWORD"] = PASSWORD
        args = [sys.executable, str(CLIENT), "--base-url", base_url]
        if account:
            args.extend(["--email", "member@example.test"])
        result = subprocess.run(args + list(extra) + list(command), capture_output=True,
                                text=True, timeout=15, env=env, cwd=self.output)
        self.assertNotIn(PASSWORD, result.stdout + result.stderr)
        return result

    def test_full_chain_authenticates_generates_polls_downloads_uploads_and_logs_out(self):
        payload = self.output / "request.json"
        payload.write_text('{"topic":"synthetic-only"}')
        with service() as (url, calls, violations):
            result = self.run_cli(url, "chain", "--generate-payload", str(payload),
                                  "--output-dir", str(self.output), "--poll-seconds", "0")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(violations, [])
        self.assertEqual((self.output / "test-problem-hydro.zip").read_bytes(), ZIP)
        self.assertEqual([path for _, path, _, _ in calls], [
            "/api/v1/auth/login", "/api/v1/problems/generate",
            "/api/v1/workflows/generation-test", "/api/v1/problems/test-problem/validate",
            "/api/v1/workflows/validation-test", "/api/v1/problems/test-problem/hydro.zip",
            "/api/v1/problems/hydro/validate", "/api/v1/auth/logout",
        ])
        for method, _, headers, _ in calls:
            if method == "GET":
                self.assertNotIn("X-Csrf-Token", headers)
        self.assertEqual(sorted(p.name for p in self.output.iterdir()), ["request.json", "test-problem-hydro.zip"])

    def test_business_error_still_logs_out(self):
        with service(fail="/api/v1/problems/test-problem/validate") as (url, calls, violations):
            result = self.run_cli(url, "validate-problem", "--problem-id", "test-problem")
        self.assertEqual(result.returncode, 1)
        self.assertIn("HTTP 500", result.stderr)
        self.assertEqual(calls[-1][1], "/api/v1/auth/logout")
        self.assertEqual(violations, [])

    def test_login_error_does_not_run_business_request(self):
        with service(fail="/api/v1/auth/login") as (url, calls, violations):
            result = self.run_cli(url, "validate-problem", "--problem-id", "test-problem")
        self.assertEqual(result.returncode, 1)
        self.assertEqual([c[1] for c in calls], ["/api/v1/auth/login"])
        self.assertEqual(violations, [])

    def test_logout_failure_is_reported(self):
        with service(fail="/api/v1/auth/logout") as (url, calls, violations):
            result = self.run_cli(url, "download-hydro", "--problem-id", "test-problem",
                                  "--output", str(self.output / "problem.zip"))
        self.assertEqual(result.returncode, 1)
        self.assertIn("session logout failed", result.stderr)
        self.assertEqual((self.output / "problem.zip").read_bytes(), ZIP)
        self.assertEqual(violations, [])

    def test_remote_http_and_credential_urls_are_rejected_before_any_request(self):
        for url in ("http://192.0.2.1/api/v1", "http://localhost.example.invalid/api/v1",
                    "http://user:secret@127.0.0.1/api/v1", "file:///tmp/api"):
            with self.subTest(url=url):
                result = self.run_cli(url, "validate-problem", "--problem-id", "test-problem")
                self.assertEqual(result.returncode, 1)
                self.assertIn("account ", result.stderr)
                self.assertNotIn("Connection", result.stderr)

    def test_login_redirect_cannot_forward_password_or_headers(self):
        with service(account=False) as (target, target_calls, _):
            with service(redirect="/api/v1/auth/login", destination=target + "/capture") as (url, calls, _):
                result = self.run_cli(url, "validate-problem", "--problem-id", "test-problem")
        self.assertEqual(result.returncode, 1)
        self.assertIn("do not follow redirects", result.stderr)
        self.assertEqual(target_calls, [])
        self.assertEqual(len(calls), 1)

    def test_business_redirect_cannot_forward_cookie_or_csrf_and_still_logs_out(self):
        with service(account=False) as (target, target_calls, _):
            with service(redirect="/api/v1/problems/test-problem/validate",
                         destination=target + "/capture") as (url, calls, violations):
                result = self.run_cli(url, "validate-problem", "--problem-id", "test-problem")
        self.assertEqual(result.returncode, 1)
        self.assertEqual(target_calls, [])
        self.assertEqual(calls[-1][1], "/api/v1/auth/logout")
        self.assertEqual(violations, [])

    def test_legacy_token_and_local_no_auth_download_remain_available(self):
        for extra in ((), ("--token", "synthetic-gateway-token")):
            with self.subTest(extra=extra), service(account=False) as (url, calls, violations):
                result = self.run_cli(url, "download-hydro", "--problem-id", "test-problem",
                                      "--output", str(self.output / "legacy.zip"), account=False, extra=extra)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(violations, [])
            self.assertEqual(len(calls), 1)
            self.assertNotIn("Cookie", calls[0][2])
            if extra:
                self.assertEqual(calls[0][2].get("Authorization"), "Bearer synthetic-gateway-token")


if __name__ == "__main__":
    unittest.main()
