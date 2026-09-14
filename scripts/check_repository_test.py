"""Exercise client/backend version boundaries using a tiny disposable checkout."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

CHECKER = Path(__file__).with_name("check-repository.py")


class RepositoryVersionTests(unittest.TestCase):
    def check_versions(self, client="2.2.1", backend="2.2.0", frontend="2.2.1"):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            files = {
                "VERSION": "2.2.1\n",
                "frontend/package.json": json.dumps({"version": frontend}),
                "frontend/package-lock.json": json.dumps({
                    "version": frontend, "packages": {"": {"version": frontend}}
                }),
                "desktop/internal/app/config.go": (
                    'package app\nconst Version = "' + client +
                    '"\nconst BackendVersion = "' + backend + '"\n'
                ),
                "scripts/check-repository.py": CHECKER.read_text(),
            }
            for name, content in files.items():
                target = root / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(content)
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            subprocess.run(["git", "-C", str(root), "add", "--", *files], check=True)
            return subprocess.run(
                [sys.executable, str(root / "scripts/check-repository.py")],
                capture_output=True, text=True,
            )

    def test_client_patch_can_keep_a_published_backend(self):
        for backend in ["2.2.0", "2.2.1"]:
            with self.subTest(backend=backend):
                result = self.check_versions(backend=backend)
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_client_and_frontend_versions_still_follow_root_version(self):
        for changed in [{"client": "2.2.0"}, {"frontend": "2.2.0"}]:
            with self.subTest(changed=changed):
                self.assertNotEqual(self.check_versions(**changed).returncode, 0)

    def test_backend_requires_an_explicit_release(self):
        for backend in ["", "latest", "2.2", "2.2.0-dev"]:
            with self.subTest(backend=backend):
                self.assertNotEqual(self.check_versions(backend=backend).returncode, 0)


if __name__ == "__main__":
    unittest.main()
