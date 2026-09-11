#!/usr/bin/env python3
"""Minimal Qraft Hydro integration client.

This example intentionally uses only Python standard-library modules so an
external team can read and reimplement the service contract without importing
Qraft internals.
"""

from __future__ import annotations

import argparse
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


def auth_headers(token: str = "") -> dict[str, str]:
    headers: dict[str, str] = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    return headers


def json_request(
    base_url: str,
    method: str,
    path: str,
    token: str = "",
    payload: Any | None = None,
    timeout: float = 120.0,
) -> dict[str, Any]:
    body = None
    headers = auth_headers(token)
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
        with request.urlopen(req, timeout=timeout) as resp:
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
) -> Path:
    headers = auth_headers(token)
    req = request.Request(api_url(base_url, path), method="GET", headers=headers)
    output.parent.mkdir(parents=True, exist_ok=True)
    try:
        with request.urlopen(req, timeout=timeout) as resp:
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
) -> dict[str, Any]:
    boundary = "----algoforge-" + uuid.uuid4().hex
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
    headers = auth_headers(token)
    headers["Content-Type"] = f"multipart/form-data; boundary={boundary}"
    headers["Content-Length"] = str(len(body))

    req = request.Request(
        api_url(base_url, path),
        data=body,
        method="POST",
        headers=headers,
    )
    try:
        with request.urlopen(req, timeout=timeout) as resp:
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
) -> dict[str, Any]:
    terminal = {"Completed", "Failed", "Canceled", "Terminated", "TimedOut"}
    last: dict[str, Any] = {}
    for _ in range(max_polls):
        last = json_request(base_url, "GET", f"/workflows/{parse.quote(workflow_id)}", token)
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
    result = json_request(args.base_url, "POST", "/problems/generate", args.token, payload)
    print(json.dumps(result, ensure_ascii=False, indent=2))


def cmd_validate_problem(args: argparse.Namespace) -> None:
    result = json_request(args.base_url, "POST", f"/problems/{args.problem_id}/validate", args.token)
    print(json.dumps(result, ensure_ascii=False, indent=2))


def cmd_download_hydro(args: argparse.Namespace) -> None:
    output = download_file(
        args.base_url,
        f"/problems/{args.problem_id}/hydro.zip",
        Path(args.output),
        args.token,
    )
    print(str(output))


def cmd_preflight_hydro(args: argparse.Namespace) -> None:
    result = multipart_upload(
        args.base_url,
        "/problems/hydro/validate",
        "file",
        Path(args.zip),
        args.token,
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))


def cmd_chain(args: argparse.Namespace) -> None:
    out_dir = Path(args.output_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    problem_id = args.problem_id

    if args.generate_payload:
        payload = json.loads(Path(args.generate_payload).read_text(encoding="utf-8"))
        generated = json_request(args.base_url, "POST", "/problems/generate", args.token, payload)
        workflow_id = (generated.get("data") or {}).get("workflow_id")
        if not workflow_id:
            raise RuntimeError("generation response did not contain data.workflow_id")
        generation_state = wait_workflow(
            args.base_url,
            workflow_id,
            args.token,
            args.poll_seconds,
            args.max_polls,
        )
        problem_id = extract_problem_id(generation_state)

    if not problem_id:
        raise RuntimeError("chain requires --problem-id or --generate-payload")

    validation = json_request(args.base_url, "POST", f"/problems/{problem_id}/validate", args.token)
    validation_workflow = (validation.get("data") or {}).get("workflow_id")
    if validation_workflow:
        wait_workflow(
            args.base_url,
            validation_workflow,
            args.token,
            args.poll_seconds,
            args.max_polls,
        )

    zip_path = out_dir / f"{problem_id}-hydro.zip"
    download_file(args.base_url, f"/problems/{problem_id}/hydro.zip", zip_path, args.token)
    report = multipart_upload(args.base_url, "/problems/hydro/validate", "file", zip_path, args.token)
    print(json.dumps({"problem_id": problem_id, "zip": str(zip_path), "preflight": report}, ensure_ascii=False, indent=2))


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default=os.environ.get("ALGOFORGE_API_BASE_URL", "http://127.0.0.1:8081/api/v1"))
    parser.add_argument("--token", default=os.environ.get("ALGOFORGE_TOKEN", ""))
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
    try:
        args.func(args)
    except Exception as exc:  # noqa: BLE001 - example CLI should print concise errors.
        print(f"error: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
