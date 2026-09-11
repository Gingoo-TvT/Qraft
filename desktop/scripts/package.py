#!/usr/bin/env python3
"""Package an immutable Windows client and offline Linux/amd64 backend bundle.
Run on the release checkout after tests. Requires Go, Docker, PyYAML and NSIS.
Artifacts stay outside the repository; no registry upload or credentials.
"""
import argparse
import base64
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import zipfile
import yaml

ROOT = Path(__file__).resolve().parents[2]
VERSION = (ROOT / "VERSION").read_text().strip()
BACKEND_VERSION = re.search(r'const BackendVersion = "([^"]+)"', (ROOT / "desktop/internal/app/config.go").read_text()).group(1)
def run(args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)
def capture(args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()
def inspect(tag):
    return json.loads(capture(["docker", "image", "inspect", tag]))[0]
def sha(path):
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(4 << 20), b""):
            h.update(block)
    return h.hexdigest()

def build_runtime(out, revision, skip_build):
    images = {}
    targets = [("backend", "backend", "production"), ("migrations", "backend", "migrations"),
               ("frontend", "frontend", "production"), ("sandbox", "sandbox", None)]
    for name, context, target in targets:
        tag = f"qraft-desktop/{name}:{BACKEND_VERSION}"
        if not skip_build:
            cmd = ["docker", "build", "--platform", "linux/amd64", "-t", tag]
            cmd += ["--build-arg", ("SANDBOX_REVISION=" if name == "sandbox" else "SOURCE_REVISION=") + revision]
            if target:
                cmd += ["--target", target]
            run(cmd + [context], cwd=ROOT)
        meta = inspect(tag)
        if meta["Config"]["Labels"].get("org.opencontainers.image.revision") != revision:
            raise RuntimeError(f"{tag} does not match checkout {revision}")
        images[tag] = meta["Id"]
    compose = yaml.safe_load((ROOT / "docker-compose.yml").read_text())
    for name in ("postgresql", "temporal-db", "redis", "minio", "temporal", "caddy"):
        default = compose["services"][name]["image"]
        match = re.fullmatch(r"\$\{[^:]+:-(.+)\}", default)
        if not match:
            raise RuntimeError(f"Unpinned image: {name}")
        ref = match.group(1)
        try:
            meta = inspect(ref)
        except subprocess.CalledProcessError:
            run(["docker", "pull", "--platform", "linux/amd64", ref])
            meta = inspect(ref)
        if meta["Architecture"] != "amd64" or meta["Os"] != "linux":
            raise RuntimeError(f"Unsupported image architecture: {ref}")
        tag = f"qraft-desktop/{name}:{BACKEND_VERSION}"
        run(["docker", "tag", meta["Id"], tag])
        images[tag] = meta["Id"]
    sandbox = f"qraft-desktop/sandbox:{BACKEND_VERSION}"
    # A stopped, task-owned container is used only to read the packaged manifest.
    cid = capture(["docker", "create", "--network", "none", "--entrypoint", "/bin/true", sandbox])
    (out / "packaging-container.json").write_text(json.dumps({"id": cid, "purpose": "read image manifest", "cleanup": "docker rm exact ID"}))
    try:
        archive = subprocess.check_output(["docker", "cp", cid + ":/sandbox/app/toolchain-packages.txt", "-"])
        with tarfile.open(fileobj=io.BytesIO(archive)) as tar:
            data = tar.extractfile("toolchain-packages.txt").read()
        toolchain = "sha256:" + hashlib.sha256(data).hexdigest()
    finally:
        run(["docker", "rm", cid], stdout=subprocess.DEVNULL)
    (out / "packaging-container.json").unlink()
    source = (ROOT / "sandbox/engine.go").read_text()
    policy = re.search(r"const seccompPolicy = `([^`]+)`", source).group(1)
    seccomp = "sha256:" + hashlib.sha256(policy.encode()).hexdigest()
    bundle = out / f"Qraft-{BACKEND_VERSION}-backend-linux-x64.tar.gz"
    # Docker save preserves the locally pinned tags used by the generated Compose.
    with bundle.open("wb") as dest:
        proc = subprocess.Popen(["docker", "save", *images], stdout=subprocess.PIPE)
        (out / "packaging-process.json").write_text(json.dumps({"pid": proc.pid, "command": "docker save fixed release images", "cleanup": "wait; terminate exact child on packaging failure"}))
        try:
            with gzip.GzipFile(filename="", mode="wb", fileobj=dest, compresslevel=1, mtime=0) as gz:
                shutil.copyfileobj(proc.stdout, gz, 4 << 20)
            if proc.wait() != 0:
                raise RuntimeError("docker save failed")
        finally:
            proc.stdout.close()
            if proc.poll() is None:
                proc.terminate()
                proc.wait()
    (out / "packaging-process.json").unlink()
    if bundle.stat().st_size >= 2_000_000_000:
        raise RuntimeError("Backend bundle exceeds the release asset limit; split before publishing")
    manifest = {"version": BACKEND_VERSION, "source_revision": revision, "bundle_sha256": sha(bundle),
                "images": [{"tag": tag, "id": image_id} for tag, image_id in images.items()],
                "sandbox_image": images[sandbox], "toolchain_digest": toolchain, "seccomp_digest": seccomp}

    return bundle, manifest


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--out", type=Path, required=True)
    p.add_argument("--makensis", default="makensis")
    p.add_argument("--skip-build", action="store_true")
    p.add_argument("--reuse-runtime", type=Path, help="Directory containing a previously published pinned backend bundle and manifest")
    p.add_argument("--webview-bootstrapper", type=Path, required=True,
                   help="Microsoft-signed Evergreen bootstrapper, signature verified on Windows before packaging")
    a = p.parse_args()
    out = a.out.resolve()
    if out == ROOT or ROOT in out.parents:
        p.error("Artifacts must be outside the release checkout")
    out.mkdir(parents=True, exist_ok=True)
    revision = capture(["git", "rev-parse", "HEAD"])
    if capture(["git", "status", "--porcelain"]):
        p.error("Release checkout must be committed and clean")
    run(["python3", "desktop/scripts/prepare-runtime.py", "--check"], cwd=ROOT)
    if a.reuse_runtime:
        manifest = json.loads((a.reuse_runtime / "runtime-manifest.json").read_text())
        source_bundle = a.reuse_runtime / f"Qraft-{manifest['version']}-backend-linux-x64.tar.gz"
        if manifest["version"] != BACKEND_VERSION or sha(source_bundle) != manifest["bundle_sha256"]:
            raise RuntimeError("Reused backend identity does not match the pinned client runtime")
        bundle = out / source_bundle.name
        if bundle.exists():
            if sha(bundle) != manifest["bundle_sha256"]:
                raise RuntimeError("Existing output backend bundle has different bytes")
        else:
            try:
                os.link(source_bundle, bundle)
            except OSError:
                shutil.copy2(source_bundle, bundle)
    else:
        if BACKEND_VERSION != VERSION:
            p.error("This client update reuses a published backend; supply --reuse-runtime")
        bundle, manifest = build_runtime(out, revision, a.skip_build)
    (out / "runtime-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    # Bundle every business screen, code editor worker and math font in the EXE.
    run(["npm", "ci"], cwd=ROOT / "frontend")
    run(["npm", "run", "build:desktop"], cwd=ROOT / "frontend")
    encoded = base64.b64encode(json.dumps(manifest, separators=(",", ":")).encode()).decode()
    env = os.environ.copy()
    env.update(GOOS="windows", GOARCH="amd64", CGO_ENABLED="0")
    ldflags = "-s -w -H=windowsgui -X github.com/Gingoo-TvT/Qraft/desktop/internal/app.BuildManifestBase64=" + encoded
    exe = out / "Qraft.exe"
    run(["go", "build", "-mod=vendor", "-trimpath", "-ldflags", ldflags, "-o", str(exe), "./cmd/qraft"], cwd=ROOT / "desktop", env=env)
    bootstrap = out / "MicrosoftEdgeWebview2Setup.exe"
    shutil.copy2(a.webview_bootstrapper, bootstrap)
    (out / "portable.flag").write_text("Keep this file beside Qraft.exe to store client settings in ./data-qraft.\n")
    (out / "使用说明.md").write_text((ROOT / "docs/windows-desktop.md").read_text())
    shutil.copy2(ROOT / "desktop/THIRD_PARTY_NOTICES.md", out / "THIRD_PARTY_NOTICES.md")
    shutil.copy2(ROOT / "LICENSE", out / "LICENSE")
    npm_licenses = []
    lock = json.loads((ROOT / "frontend/package-lock.json").read_text())
    for relative, metadata in lock["packages"].items():
        if not relative or metadata.get("dev"):
            continue
        package_dir = ROOT / "frontend" / relative
        if not package_dir.is_dir():
            continue
        for notice in package_dir.iterdir():
            if notice.is_file() and notice.name.lower().startswith(("license", "copying", "notice")):
                dest = out / "licenses/npm" / Path(relative).relative_to("node_modules") / notice.name
                dest.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(notice, dest)
                npm_licenses.append(dest)
    portable = out / f"Qraft-{VERSION}-windows-x64-portable.zip"
    with zipfile.ZipFile(portable, "w", zipfile.ZIP_DEFLATED) as z:
        for name in ("Qraft.exe", "portable.flag", "使用说明.md", "LICENSE", "THIRD_PARTY_NOTICES.md", "MicrosoftEdgeWebview2Setup.exe", "runtime-manifest.json"):
            z.write(out / name, "Qraft/" + name)
        for notice in npm_licenses:
            z.write(notice, "Qraft/" + str(notice.relative_to(out)))
        for path in (ROOT / "desktop/vendor").rglob("*"):
            if path.is_file() and path.name.lower().startswith(("license", "copying", "notice")):
                z.write(path, "Qraft/licenses/" + str(path.relative_to(ROOT / "desktop/vendor")))
    for path in (ROOT / "desktop/vendor").rglob("*"):
        if path.is_file() and path.name.lower().startswith(("license", "copying", "notice")):
            dest = out / "licenses" / path.relative_to(ROOT / "desktop/vendor")
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, dest)
    go_license = ROOT / "desktop/assets/Go-LICENSE"
    shutil.copy2(go_license, out / "licenses/Go-LICENSE")
    with zipfile.ZipFile(portable, "a", zipfile.ZIP_DEFLATED) as z:
        z.write(go_license, "Qraft/licenses/Go-LICENSE")
    installer = out / f"Qraft-{VERSION}-windows-x64-setup.exe"
    run([a.makensis, "-DVERSION=" + VERSION, "-DPAYLOAD=" + str(out),
         "-DOUTPUT=" + str(installer), str(ROOT / "desktop/installer/qraft.nsi")], cwd=ROOT)
    # The standalone EXE is useful with an existing WebView2 Runtime.
    named_exe = out / f"Qraft-{VERSION}-windows-x64.exe"
    shutil.copy2(exe, named_exe)
    files = [bundle, portable, installer, named_exe, out / "runtime-manifest.json"]
    (out / "SHA256SUMS.txt").write_text("".join(sha(f) + "  " + f.name + "\n" for f in files))
    (out / "ARTIFACTS.json").write_text(json.dumps({"version": VERSION, "source_revision": revision,
        "backend_version": manifest["version"], "backend_source_revision": manifest["source_revision"],
        "backend_bundle_reused": bool(a.reuse_runtime),
        "backend_bundle_release": "https://github.com/Gingoo-TvT/Qraft/releases/tag/v" + manifest["version"],
        "files": [{"name": f.name, "bytes": f.stat().st_size, "sha256": sha(f)} for f in files]}, indent=2) + "\n")
    print("Packaged", VERSION, revision, flush=True)

if __name__ == "__main__":
    main()
