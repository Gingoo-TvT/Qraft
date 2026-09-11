#!/usr/bin/env python3
"""Small checks for accidentally committed local data, broken docs and version drift."""
import json
from pathlib import Path
import re
import subprocess
import sys
import urllib.parse

ROOT = Path(__file__).resolve().parents[1]
paths = [p for p in subprocess.check_output(["git", "ls-files", "-z"], cwd=ROOT).decode().split("\0") if p]
if not paths:
    sys.exit("No tracked files to check; stage the intended source files first.")
errors = []
secret = re.compile(r"gh[pousr]_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----")
for name in paths:
    p = Path(name)
    if any(part in {".next", "node_modules", ".cache", "coverage", "outputs", "artifacts", "data-qraft", "user-data", "backups"} for part in p.parts):
        errors.append("Generated or instance data: " + name)
    if (p.name.startswith(".env") and p.name != ".env.example") or p.name in {"settings.json", "ui-preferences.json"} or p.suffix.lower() in {".exe", ".db", ".sqlite", ".sqlite3", ".dump", ".bak", ".xlsx"}:
        errors.append("Local data or binary/template: " + name)
    if "quiz_template_real" in name or "quiz_sample_rows" in name or name.startswith("docs/integration/"):
        errors.append("Private integration artifact: " + name)
    try:
        content = (ROOT / name).read_text(encoding="utf-8")
    except (UnicodeError, OSError):
        continue
    if secret.search(content):
        errors.append("Credential-like text (value not printed): " + name)
    if "Gingoo-TvT/" + "algoforge-oj-integration" in content and name != "scripts/check-repository.py":
        errors.append("Old private repository link: " + name)
    if p.suffix == ".md" and (name.startswith("docs/") or p.parent == Path(".")):
        # Inline relative links and local images; network resources are not fetched.
        for target in re.findall(r"!?\[[^\]]*\]\(([^)\s]+)(?:\s+\"[^\"]*\")?\)", content):
            target = target.strip("<>")
            if target.startswith(("http:", "https:", "mailto:", "#")):
                continue
            target = urllib.parse.unquote(target.split("#", 1)[0])
            if target and not (ROOT / p.parent / target).exists():
                errors.append("Broken local link in " + name + ": " + target)

version = (ROOT / "VERSION").read_text().strip()
for name in ["frontend/package.json", "frontend/package-lock.json"]:
    data = json.loads((ROOT / name).read_text())
    if data["version"] != version:
        errors.append("Version mismatch: " + name)
    if name.endswith("package-lock.json") and data["packages"][""]["version"] != version:
        errors.append("Lock root version mismatch")
config = (ROOT / "desktop/internal/app/config.go").read_text()
for key in ["Version", "BackendVersion"]:
    if not re.search(r'const ' + key + r' = "' + re.escape(version) + '"', config):
        errors.append("Desktop version mismatch: " + key)
if errors:
    print("\n".join(sorted(set(errors))), file=sys.stderr)
    sys.exit(1)
print(f"Repository checks passed: {len(paths)} tracked files, version {version}.")
