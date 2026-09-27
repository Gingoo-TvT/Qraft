#!/usr/bin/env python3
"""Configure this checkout's local service. Never prints or replaces saved keys."""
import argparse
import base64
import hashlib
import ipaddress
import getpass
import http.cookiejar
import urllib.error
import io
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import tarfile
import tempfile
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]
ENV = ROOT / ".env"

def capture(args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()

def read_env(path):
    result = {}
    for line in path.read_text().splitlines():
        if line and not line.lstrip().startswith("#") and "=" in line:
            key, value = line.split("=", 1)
            result[key.strip()] = value.strip()
    return result

def update(values):
    text = ENV.read_text()
    for key, value in values.items():
        if any(c in str(value) for c in "\r\n\x00$'\"#"):
            raise ValueError("Unsafe environment value for " + key)
        pattern = r"^" + re.escape(key) + r"=.*$"
        line = key + "=" + str(value)
        text = re.sub(pattern, lambda _: line, text, flags=re.M) if re.search(pattern, text, re.M) else text.rstrip() + "\n" + line + "\n"
    fd, name = tempfile.mkstemp(prefix=".env-", dir=ROOT)
    try:
        os.chmod(name, 0o600)
        with os.fdopen(fd, "w") as stream:
            stream.write(text)
        os.replace(name, ENV)
    finally:
        if os.path.exists(name):
            os.unlink(name)

def source_revision():
    try:
        return capture(["git", "rev-parse", "HEAD"])
    except subprocess.CalledProcessError:
        return "qraft-development"

def refresh_revision():
    # Only an explicit build refreshes source identity. Normal setup and
    # applying saved provider metadata must preserve all existing settings.
    revision = source_revision()
    update({"SOURCE_REVISION": revision, "SANDBOX_REVISION": revision})
    print("Refreshed source and sandbox revisions for the next build.")

def initialize():
    if ENV.exists():
        print("Existing .env preserved.")
        return
    data = (ROOT / ".env.example").read_text()
    revision = source_revision()
    values = {
        "POSTGRES_PASSWORD": secrets.token_urlsafe(32),
        "TEMPORAL_DB_PASSWORD": secrets.token_urlsafe(32),
        "MINIO_SECRET_KEY": secrets.token_urlsafe(32),
        "JWT_SECRET": secrets.token_urlsafe(48),
        "QRAFT_AUTH_BOOTSTRAP_TOKEN": secrets.token_urlsafe(48),
        "ALGOFORGE_SETTINGS_ENCRYPTION_KEY": base64.b64encode(secrets.token_bytes(32)).decode(),
        "SOURCE_REVISION": revision,
        "SANDBOX_REVISION": revision,
    }
    for key, value in values.items():
        data = re.sub(r"^" + key + r"=.*$", lambda _: key + "=" + value, data, flags=re.M)
    fd = os.open(ENV, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as stream:
        stream.write(data)
    print("Created .env with new local credentials; model settings remain empty.")

def enable_shared_auth():
    env = read_env(ENV)
    values = {"APP_DEV_MODE": "false", "QRAFT_AUTH_SECURE_COOKIE": "true"}
    if not env.get("QRAFT_AUTH_BOOTSTRAP_TOKEN"):
        values["QRAFT_AUTH_BOOTSTRAP_TOKEN"] = secrets.token_urlsafe(48)
    update(values)
    print("Shared authentication configured. Use an HTTPS entrypoint; restart the API explicitly to apply.")

def cloud_host(value):
    # Accept one public IPv4 or a DNS hostname, never Caddy syntax or a URL.
    value = value.strip().lower()
    try:
        addr = ipaddress.ip_address(value)
    except ValueError:
        if len(value) > 253 or "." not in value or all(label.isdigit() for label in value.split(".")) or not all(
            re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label)
            for label in value.split(".")
        ) or value.endswith((".localhost", ".local", ".internal")):
            raise ValueError("Provide a public IPv4 address or DNS hostname without scheme, path, or port.")
    else:
        if addr.version != 4 or not addr.is_global:
            raise ValueError("Cloud IP deployment requires a public IPv4 address.")
    return value

def enable_cloud(host):
    host = cloud_host(host)
    enable_shared_auth()
    update({"QRAFT_PUBLIC_HOST": host, "DOMAIN": "https://" + host})
    print("Cloud entrypoint configured. Use docker-compose.cloud.yml; no service has been started.")

def authenticated_metadata(opener, base, endpoint):
    # The loopback local mode needs no account. Shared mode prompts only after
    # the server says authentication is required; credentials never enter .env.
    try:
        with opener.open(endpoint, timeout=15) as response:
            return json.loads(response.read(1 << 20))
    except urllib.error.HTTPError as error:
        if error.code != 401:
            raise
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme != "https" and parsed.hostname not in ("localhost", "127.0.0.1", "::1"):
        raise ValueError("Administrator login requires HTTPS outside loopback.")
    email = input("Administrator email: ").strip()
    password = getpass.getpass("Administrator password: ")
    request = urllib.request.Request(base.rstrip("/") + "/api/v1/auth/login",
        data=json.dumps({"email": email, "password": password}).encode(),
        headers={"Content-Type": "application/json", "X-Qraft-Client": "1"}, method="POST")
    with opener.open(request, timeout=30) as response:
        session = json.loads(response.read(1 << 20))["data"]
    try:
        with opener.open(endpoint, timeout=15) as response:
            return json.loads(response.read(1 << 20))
    finally:
        logout = urllib.request.Request(base.rstrip("/") + "/api/v1/auth/logout", data=b"{}",
            headers={"Content-Type": "application/json", "X-Qraft-Client": "1",
                     "X-CSRF-Token": session["csrf_token"]}, method="POST")
        try:
            with opener.open(logout, timeout=15):
                pass
        except (OSError, urllib.error.URLError):
            print("Could not revoke this temporary setup session; change your account password to revoke all active sessions.")

def bind_sandbox():
    env = read_env(ENV)
    metadata = json.loads(capture(["docker", "image", "inspect", env["SANDBOX_IMAGE"]]))[0]
    if metadata["Config"]["Labels"].get("org.opencontainers.image.revision") != env["SANDBOX_REVISION"]:
        raise ValueError("Sandbox image does not match .env SANDBOX_REVISION; build it first.")
    container = capture(["docker", "create", "--network", "none", "--label", "io.qraft.helper=configure", "--entrypoint", "/bin/true", metadata["Id"]])
    record_dir = ROOT / ".tmp"
    record_dir.mkdir(exist_ok=True)
    record = record_dir / ("sandbox-read-" + uuid.uuid4().hex + ".json")
    record.write_text(json.dumps({"container": container, "purpose": "read pinned toolchain manifest", "cleanup": "docker rm exact container ID"}))
    try:
        data = subprocess.check_output(["docker", "cp", container + ":/sandbox/app/toolchain-packages.txt", "-"], cwd=ROOT)
        with tarfile.open(fileobj=io.BytesIO(data)) as archive:
            manifest = archive.extractfile("toolchain-packages.txt").read()
    finally:
        subprocess.run(["docker", "rm", container], cwd=ROOT, check=True, stdout=subprocess.DEVNULL)
        record.unlink()
    policy = re.search(r"const seccompPolicy = `([^`]+)`", (ROOT / "sandbox/engine.go").read_text()).group(1)
    update({
        "SANDBOX_IMAGE_DIGEST": metadata["Id"],
        "ALGOFORGE_S3_SANDBOX_IMAGE_DIGEST": metadata["Id"],
        "ALGOFORGE_S3_SANDBOX_TOOLCHAIN_MANIFEST_DIGEST": "sha256:" + hashlib.sha256(manifest).hexdigest(),
        "ALGOFORGE_S3_SANDBOX_SECCOMP_POLICY_DIGEST": "sha256:" + hashlib.sha256(policy.encode()).hexdigest(),
    })
    print("Bound sandbox image, toolchain and policy identities. No container left running.")

def saved_embedding(base, model_version_id=""):
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.path.strip("/"):
        raise ValueError("Provide the saved service root URL without credentials or path.")
    # Read metadata only; API keys remain encrypted in the service database.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args):
            return None
    selected = str(uuid.UUID(model_version_id)) if model_version_id else ""
    endpoint = base.rstrip("/") + "/api/v1/embedding/saved-runtime-settings"
    if selected:
        endpoint += "?" + urllib.parse.urlencode({"model_version_id": selected})
    opener = urllib.request.build_opener(NoRedirect, urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    body = authenticated_metadata(opener, base, endpoint)
    data = body["data"]
    if not data.get("configured"):
        raise ValueError("First save and test an embedding provider in the service settings.")
    timeout = data.get("timeout_sec")
    model = data.get("model")
    provider = urllib.parse.urlsplit(data.get("base_url", ""))
    if (data.get("dimensions") != 1536 or not isinstance(model, str) or not model.strip()
            or not isinstance(timeout, int) or isinstance(timeout, bool) or timeout < 1
            or provider.scheme not in ("http", "https") or not provider.hostname
            or provider.username or provider.password or provider.query or provider.fragment):
        raise ValueError("Incomplete saved embedding configuration.")
    version = str(uuid.UUID(data["model_version_id"]))
    if selected and selected != version:
        raise ValueError("The saved configuration returned a different embedding model version.")
    update({
        "ALGOFORGE_EMBEDDING_ENABLED": "true",
        "ALGOFORGE_EMBEDDING_BASE_URL": data["base_url"],
        "ALGOFORGE_EMBEDDING_MODEL": data["model"],
        "ALGOFORGE_EMBEDDING_DIMENSIONS": data["dimensions"],
        "ALGOFORGE_EMBEDDING_TIMEOUT_SEC": data["timeout_sec"],
        "ALGOFORGE_EMBEDDING_EXPECTED_STATEMENT_MODEL_VERSION_ID": version,
    })
    print("Applied saved embedding identity; no model key was exported.")

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--bind-sandbox", action="store_true")
    group.add_argument("--cloud-host", metavar="IP_OR_DOMAIN", help="Configure the HTTPS cloud overlay for a public IPv4 or DNS hostname.")
    group.add_argument("--shared", action="store_true", help="Enable account authentication and HTTPS cookies for a shared service.")
    group.add_argument("--show-bootstrap-token", action="store_true", help="Print this instance's one-time administrator initialization token to your private terminal.")
    group.add_argument("--refresh-revision", action="store_true",
                       help="Refresh source identity immediately before building images.")
    group.add_argument("--saved-embedding", metavar="SERVICE_ROOT")
    parser.add_argument("--model-version-id", default="", metavar="UUID",
                        help="Select a registered embedding version when several versions match.")
    args = parser.parse_args()
    if args.model_version_id and not args.saved_embedding:
        parser.error("--model-version-id requires --saved-embedding")
    if args.cloud_host is not None:
        cloud_host(args.cloud_host)
    initialize()
    if args.cloud_host is not None:
        enable_cloud(args.cloud_host)
    if args.shared:
        enable_shared_auth()
    if args.show_bootstrap_token:
        token = read_env(ENV).get("QRAFT_AUTH_BOOTSTRAP_TOKEN", "")
        if not token:
            raise ValueError("No initialization token configured. Run --shared first.")
        print(token)
    if args.refresh_revision:
        refresh_revision()
    if args.bind_sandbox:
        bind_sandbox()
    if args.saved_embedding:
        saved_embedding(args.saved_embedding, args.model_version_id)

if __name__ == "__main__":
    main()
