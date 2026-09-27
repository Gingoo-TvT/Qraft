#!/usr/bin/env python3
"""Minimal Qraft Hydro integration client.

This example intentionally uses only Python standard-library modules so an
external team can read and reimplement the service contract without importing
Qraft internals.
"""

from __future__ import annotations

import argparse
import getpass
import http.cookiejar
import ipaddress
import json
import mimetypes
import os
import sys
import time
import uuid
from pathlib import Path
from typing import Any
from urllib import error, parse, request


def api_url(base_url: str, path: str) -> str:
    return base_url.rstrip("/") + "/" + path.lstrip("/")


def auth_headers(token: str = "", csrf_token: str = "") -> dict[str, str]:
    headers: dict[str, str] = {"Accept": "application/json", "X-Qraft-Client": "1"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if csrf_token:
        headers["X-CSRF-Token"] = csrf_token
    return headers


def validate_account_url(base_url: str) -> None:
    url = parse.urlsplit(base_url)
    if not url.hostname or url.username is not None or url.password is not None or url.query or url.fragment:
        raise ValueError("account base URL must be an HTTP(S) service URL without credentials, query or fragment")
    # Do not resolve arbitrary names: DNS must not turn a public HTTP URL into
    # an acceptable credential destination.
    loopback = url.hostname.lower() == "localhost"
    try:
        loopback = loopback or ipaddress.ip_address(url.hostname).is_loopback
    except ValueError:
        pass
    if url.scheme != "https" and not (url.scheme == "http" and loopback):
        raise ValueError("account login requires HTTPS, except for loopback HTTP")
    _ = url.port  # Validate a supplied port before prompting for the password.


class AccountRedirectHandler(request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # API URLs should be canonical. Refuse even same-host redirects so
        # passwords, cookies and CSRF headers never follow an unexpected hop.
        raise RuntimeError("account requests do not follow redirects; use the final HTTPS service URL")


def open_request(req: request.Request, timeout: float, opener: request.OpenerDirector | None = None):
    return (opener.open(req, timeout=timeout) if opener else request.urlopen(req, timeout=timeout))


def json_request(
    base_url: str,
    method: str,
    path: str,
    token: str = "",
    payload: Any | None = None,
    timeout: float = 120.0,
    *,
    opener: request.OpenerDirector | None = None,
    csrf_token: str = "",
) -> dict[str, Any]:
    body = None
    headers = auth_headers(token, csrf_token if method.upper() not in {"GET", "HEAD", "OPTIONS"} else "")
    if payload is not None:
        body = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"

    req = request.Request(
        api_url(base_url, path),
        data=body,
        method=method,
        headers=headers,
    )
    try:
        with open_request(req, timeout, opener) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} {path} failed: HTTP {exc.code}: {detail}") from exc


def download_file(
    base_url: str,
    path: str,
    output: Path,
    token: str = "",
    timeout: float = 120.0,
    *,
    opener: request.OpenerDirector | None = None,
) -> Path:
    headers = auth_headers(token)
    req = request.Request(api_url(base_url, path), method="GET", headers=headers)
    output.parent.mkdir(parents=True, exist_ok=True)
    try:
        with open_request(req, timeout, opener) as resp:
            output.write_bytes(resp.read())
    except error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"GET {path} failed: HTTP {exc.code}: {detail}") from exc
    return output


def multipart_upload(
    base_url: str,
    path: str,
    field_name: str,
    file_path: Path,
    token: str = "",
    timeout: float = 120.0,
    *,
    opener: request.OpenerDirector | None = None,
    csrf_token: str = "",
) -> dict[str, Any]:
    boundary = "----qraft-" + uuid.uuid4().hex
    filename = file_path.name
    content_type = mimetypes.guess_type(filename)[0] or "application/octet-stream"
    file_data = file_path.read_bytes()
    parts = [
        f"--{boundary}\r\n".encode("ascii"),
        (
            f'Content-Disposition: form-data; name="{field_name}"; '
            f'filename="{filename}"\r\n'
        ).encode("utf-8"),
        f"Content-Type: {content_type}\r\n\r\n".encode("ascii"),
        file_data,
        b"\r\n",
        f"--{boundary}--\r\n".encode("ascii"),
    ]
    body = b"".join(parts)
    headers = auth_headers(token, csrf_token)
    headers["Content-Type"] = f"multipart/form-data; boundary={boundary}"
    headers["Content-Length"] = str(len(body))

    req = request.Request(
        api_url(base_url, path),
        data=body,
        method="POST",
        headers=headers,
    )
    try:
        with open_request(req, timeout, opener) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"POST {path} failed: HTTP {exc.code}: {detail}") from exc


def wait_workflow(
    base_url: str,
    workflow_id: str,
    token: str,
    poll_seconds: float,
    max_polls: int,
    *,
    opener: request.OpenerDirector | None = None,
    csrf_token: str = "",
) -> dict[str, Any]:
    terminal = {"Completed", "Failed", "Canceled", "Terminated", "TimedOut"}
    last: dict[str, Any] = {}
    for _ in range(max_polls):
        last = json_request(base_url, "GET", f"/workflows/{parse.quote(workflow_id)}", token, opener=opener, csrf_token=csrf_token)
        data = last.get("data") or {}
        status = str(data.get("execution_status") or data.get("status") or "")
        if status in terminal:
            return last
        time.sleep(poll_seconds)
    raise TimeoutError(f"workflow {workflow_id} did not finish after {max_polls} polls")


def extract_problem_id(workflow_response: dict[str, Any]) -> str:
    data = workflow_response.get("data") or {}
    state = data.get("state") or {}
    problem_id = state.get("problem_id")
    if not problem_id:
        raise RuntimeError("workflow response does not contain data.state.problem_id")
    return str(problem_id)


def cmd_generate(args: argparse.Namespace) -> None:
    payload = json.loads(Path(args.payload).read_text(encoding="utf-8"))
    result = json_request(args.base_url, "POST", "/problems/generate", args.token, payload, opener=args.opener, csrf_token=args.csrf_token)
    print(json.dumps(result, ensure_ascii=False, indent=2))


def cmd_validate_problem(args: argparse.Namespace) -> None:
    result = json_request(args.base_url, "POST", f"/problems/{args.problem_id}/validate", args.token, opener=args.opener, csrf_token=args.csrf_token)
    print(json.dumps(result, ensure_ascii=False, indent=2))


def cmd_download_hydro(args: argparse.Namespace) -> None:
    output = download_file(
        args.base_url,
        f"/problems/{args.problem_id}/hydro.zip",
        Path(args.output),
        args.token,
        opener=args.opener,
    )
    print(str(output))


def cmd_preflight_hydro(args: argparse.Namespace) -> None:
    result = multipart_upload(
        args.base_url,
        "/problems/hydro/validate",
        "file",
        Path(args.zip),
        args.token,
        opener=args.opener,
        csrf_token=args.csrf_token,
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))


def cmd_chain(args: argparse.Namespace) -> None:
    out_dir = Path(args.output_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    problem_id = args.problem_id

    if args.generate_payload:
        payload = json.loads(Path(args.generate_payload).read_text(encoding="utf-8"))
        generated = json_request(args.base_url, "POST", "/problems/generate", args.token, payload, opener=args.opener, csrf_token=args.csrf_token)
        workflow_id = (generated.get("data") or {}).get("workflow_id")
        if not workflow_id:
            raise RuntimeError("generation response did not contain data.workflow_id")
        generation_state = wait_workflow(
            args.base_url,
            workflow_id,
            args.token,
            args.poll_seconds,
            args.max_polls,
            opener=args.opener,
            csrf_token=args.csrf_token,
        )
        problem_id = extract_problem_id(generation_state)

    if not problem_id:
        raise RuntimeError("chain requires --problem-id or --generate-payload")

    validation = json_request(args.base_url, "POST", f"/problems/{problem_id}/validate", args.token, opener=args.opener, csrf_token=args.csrf_token)
    validation_workflow = (validation.get("data") or {}).get("workflow_id")
    if validation_workflow:
        wait_workflow(
            args.base_url,
            validation_workflow,
            args.token,
            args.poll_seconds,
            args.max_polls,
            opener=args.opener,
            csrf_token=args.csrf_token,
        )

    zip_path = out_dir / f"{problem_id}-hydro.zip"
    download_file(args.base_url, f"/problems/{problem_id}/hydro.zip", zip_path, args.token, opener=args.opener)
    report = multipart_upload(args.base_url, "/problems/hydro/validate", "file", zip_path, args.token, opener=args.opener, csrf_token=args.csrf_token)
    print(json.dumps({"problem_id": problem_id, "zip": str(zip_path), "preflight": report}, ensure_ascii=False, indent=2))


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default=os.environ.get("QRAFT_API_BASE_URL", os.environ.get("ALGOFORGE_API_BASE_URL", "http://127.0.0.1:8081/api/v1")))
    parser.add_argument("--token", default=os.environ.get("ALGOFORGE_TOKEN", ""), help="Legacy bearer token for an external gateway.")
    parser.add_argument("--email", default=os.environ.get("QRAFT_EMAIL", ""), help="Qraft account email; prompts for a password unless QRAFT_PASSWORD is set.")
    parser.add_argument("--password", default=os.environ.get("QRAFT_PASSWORD", ""), help="Legacy password argument; prefer the hidden prompt or QRAFT_PASSWORD in automation.")
    sub = parser.add_subparsers(dest="command", required=True)

    generate = sub.add_parser("generate")
    generate.add_argument("--payload", required=True)
    generate.set_defaults(func=cmd_generate)

    validate = sub.add_parser("validate-problem")
    validate.add_argument("--problem-id", required=True)
    validate.set_defaults(func=cmd_validate_problem)

    download = sub.add_parser("download-hydro")
    download.add_argument("--problem-id", required=True)
    download.add_argument("--output", required=True)
    download.set_defaults(func=cmd_download_hydro)

    preflight = sub.add_parser("preflight-hydro")
    preflight.add_argument("--zip", required=True)
    preflight.set_defaults(func=cmd_preflight_hydro)

    chain = sub.add_parser("chain")
    chain.add_argument("--problem-id", default="")
    chain.add_argument("--generate-payload", default="")
    chain.add_argument("--output-dir", default=".tmp/hydro-chain")
    chain.add_argument("--poll-seconds", type=float, default=5.0)
    chain.add_argument("--max-polls", type=int, default=120)
    chain.set_defaults(func=cmd_chain)

    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    if args.email and args.token:
        parser.error("--email and --token are mutually exclusive")
    args.opener = None
    args.csrf_token = ""
    result = 0
    try:
        if args.email:
            validate_account_url(args.base_url)
            args.opener = request.build_opener(
                request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
                AccountRedirectHandler(),
            )
            password = args.password or getpass.getpass("Qraft password: ")
            login = json_request(
                args.base_url,
                "POST",
                "/auth/login",
                payload={"email": args.email, "password": password},
                opener=args.opener,
            )
            password = args.password = ""
            login_data = login.get("data") or login
            args.csrf_token = str(login_data.get("csrf_token") or "")
            if not args.csrf_token:
                raise RuntimeError("login succeeded without a CSRF token")
        args.func(args)
    except Exception as exc:  # noqa: BLE001 - example CLI should print concise errors.
        print(f"error: {exc}", file=sys.stderr)
        result = 1
    finally:
        if args.csrf_token:
            try:
                json_request(args.base_url, "POST", "/auth/logout", opener=args.opener, csrf_token=args.csrf_token)
            except Exception as exc:  # noqa: BLE001 - preserve any original command error.
                print(f"error: session logout failed: {exc}", file=sys.stderr)
                result = 1
    return result


if __name__ == "__main__":
    raise SystemExit(main())
