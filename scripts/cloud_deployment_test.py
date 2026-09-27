"""Cloud setup contracts, using temporary synthetic configuration only."""
import base64
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


SOURCE_ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location(
    "qraft_cloud_configure", SOURCE_ROOT / "scripts" / "configure.py"
)
configure = importlib.util.module_from_spec(spec)
spec.loader.exec_module(configure)

SECRET_KEYS = (
    "POSTGRES_PASSWORD",
    "TEMPORAL_DB_PASSWORD",
    "MINIO_SECRET_KEY",
    "JWT_SECRET",
    "QRAFT_AUTH_BOOTSTRAP_TOKEN",
    "ALGOFORGE_SETTINGS_ENCRYPTION_KEY",
)
INVALID_HOSTS = (
    "",
    "   ",
    "https://qraft.example.com",
    "qraft.example.com/path",
    "qraft.example.com:443",
    "qraft.example.com,other.example.com",
    "qraft.example.com\nAPP_DEV_MODE=true",
    "qraft.example.com {",
    "localhost",
    "qraft.localhost",
    "qraft.local",
    "qraft.internal",
    "127.0.0.1",
    "10.0.0.1",
    "192.168.1.2",
    "0.0.0.0",
    "::1",
    "2606:4700:4700::1111",
    "999.1.2.3",
    "1.2.3.999",
    "001.002.003.004",
    "qraft..example.com",
    "-qraft.example.com",
    "qraft_.example.com",
    "qraft." + "a" * 64 + ".com",
)


class CloudConfigurationTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        (self.root / ".env.example").write_text(
            (SOURCE_ROOT / ".env.example").read_text()
        )
        for patch in (
            mock.patch.object(configure, "ROOT", self.root),
            mock.patch.object(configure, "ENV", self.root / ".env"),
            mock.patch.object(configure, "capture", return_value="synthetic-cloud-revision"),
        ):
            patch.start()
            self.addCleanup(patch.stop)

    def run_cloud(self, host):
        output = io.StringIO()
        with mock.patch.object(sys, "argv", ["configure.py", "--cloud-host=" + host]):
            with contextlib.redirect_stdout(output):
                configure.main()
        return output.getvalue()

    def files(self):
        return {
            path.name: path.read_bytes()
            for path in self.root.iterdir()
            if path.is_file()
        }

    def test_cloud_host_initializes_fresh_shared_instance(self):
        output = self.run_cloud("Qraft.Example.COM")
        env = configure.read_env(configure.ENV)
        self.assertEqual(env["QRAFT_PUBLIC_HOST"], "qraft.example.com")
        self.assertEqual(env["DOMAIN"], "https://qraft.example.com")
        self.assertEqual(env["APP_DEV_MODE"], "false")
        self.assertEqual(env["QRAFT_AUTH_SECURE_COOKIE"], "true")
        self.assertEqual(env["SOURCE_REVISION"], "synthetic-cloud-revision")
        self.assertEqual(env["SANDBOX_REVISION"], "synthetic-cloud-revision")
        self.assertEqual(len(base64.b64decode(env["ALGOFORGE_SETTINGS_ENCRYPTION_KEY"])), 32)
        self.assertGreaterEqual(len(env["QRAFT_AUTH_BOOTSTRAP_TOKEN"]), 32)
        for key in SECRET_KEYS:
            self.assertTrue(env[key], key)
            self.assertNotIn(env[key], output, key + " was printed")
        self.assertNotEqual(env["POSTGRES_PASSWORD"], env["TEMPORAL_DB_PASSWORD"])
        self.assertEqual(env["ALGOFORGE_LLM_API_KEY"], "")
        self.assertEqual(env["ALGOFORGE_EMBEDDING_API_KEY"], "")
        if os.name != "nt":
            self.assertEqual(configure.ENV.stat().st_mode & 0o777, 0o600)

    def test_repeated_cloud_setup_preserves_credentials_and_existing_settings(self):
        self.run_cloud("qraft.example.com")
        configure.update({
            "ALGOFORGE_LLM_API_KEY": "synthetic-existing-model-key",
            "OUTBOX_ENDPOINT": "https://events.example.com/qraft",
        })
        before = configure.read_env(configure.ENV)
        same_host = configure.ENV.read_bytes()
        self.run_cloud("qraft.example.com")
        self.assertEqual(configure.ENV.read_bytes(), same_host)
        configure.capture.return_value = "a-new-revision-must-not-replace-the-saved-one"
        self.run_cloud("another.example.com")
        after = configure.read_env(configure.ENV)
        changed = {key for key in before if before[key] != after[key]}
        self.assertEqual(changed, {"QRAFT_PUBLIC_HOST", "DOMAIN"})
        for key in SECRET_KEYS:
            self.assertEqual(after[key], before[key], key)

    def test_public_ipv4_is_accepted_without_contacting_it(self):
        # This address is only parsed into the temporary config; no network call.
        self.run_cloud("8.8.8.8")
        env = configure.read_env(configure.ENV)
        self.assertEqual(env["QRAFT_PUBLIC_HOST"], "8.8.8.8")
        self.assertEqual(env["DOMAIN"], "https://8.8.8.8")

    def test_invalid_host_never_creates_configuration(self):
        before = self.files()
        for host in INVALID_HOSTS:
            with self.subTest(host=repr(host)):
                with self.assertRaises(ValueError):
                    self.run_cloud(host)
                self.assertEqual(self.files(), before)
                self.assertFalse(configure.ENV.exists())

    def test_invalid_host_never_changes_existing_configuration(self):
        self.run_cloud("qraft.example.com")
        before = self.files()
        for host in INVALID_HOSTS:
            with self.subTest(host=repr(host)):
                with self.assertRaises(ValueError):
                    self.run_cloud(host)
                self.assertEqual(self.files(), before)


class CloudComposeTests(unittest.TestCase):
    def setUp(self):
        if not shutil.which("docker"):
            self.skipTest("Docker CLI unavailable; cloud Compose rendering not exercised")
        self.clean_env = {"PATH": os.environ.get("PATH", ""), "COMPOSE_DISABLE_ENV_FILE": "1"}
        for key in ("HOME", "USERPROFILE", "SYSTEMROOT", "SystemRoot"):
            if key in os.environ:
                self.clean_env[key] = os.environ[key]
        version = subprocess.run(
            ["docker", "compose", "version", "--short"],
            capture_output=True, text=True, env=self.clean_env, timeout=30
        )
        if version.returncode:
            self.skipTest("Docker Compose plugin unavailable; cloud rendering not exercised")
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.env_file = self.root / "synthetic.env"
        # Deliberately unsafe base preferences must be overridden by cloud mode.
        # Neither the checkout's .env nor the caller's deployment env is loaded.
        self.env_file.write_text(
            "COMPOSE_PROJECT_NAME=qraft-cloud-test\n"
            "POSTGRES_PASSWORD=synthetic-postgres-password\n"
            "TEMPORAL_DB_PASSWORD=synthetic-temporal-password\n"
            "MINIO_SECRET_KEY=synthetic-minio-password\n"
            "JWT_SECRET=synthetic-jwt-secret\n"
            "ALGOFORGE_SETTINGS_ENCRYPTION_KEY=" + base64.b64encode(b"x" * 32).decode() + "\n"
            "QRAFT_AUTH_BOOTSTRAP_TOKEN=synthetic-bootstrap-token-for-tests-only\n"
            "QRAFT_PUBLIC_HOST=qraft.example.com\n"
            "APP_DEV_MODE=true\n"
            "QRAFT_AUTH_SECURE_COOKIE=false\n"
            "QRAFT_AUTH_TRUST_PROXY=false\n"
            "QRAFT_ALLOWED_ORIGINS=https://unwanted.example.com\n"
            "NEXT_PUBLIC_API_URL=https://unwanted.example.com/api/v1\n"
            "NEXT_PUBLIC_WS_URL=https://unwanted.example.com\n"
            "HTTP_PORT=19980\n"
            "HTTPS_PORT=19943\n"
        )

    def render(self, profiles=()):
        command = [
            "docker", "compose", "--project-directory", str(SOURCE_ROOT),
            "--env-file", str(self.env_file),
            "-f", str(SOURCE_ROOT / "docker-compose.yml"),
            "-f", str(SOURCE_ROOT / "docker-compose.cloud.yml"),
        ]
        for profile in profiles:
            command.extend(["--profile", profile])
        command.extend(["config", "--format", "json"])
        result = subprocess.run(
            command, text=True, capture_output=True, env=self.clean_env,
            cwd=self.root, timeout=30
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def test_cloud_overlay_exposes_only_https_gateway_and_keeps_private_services_private(self):
        default = self.render()
        rendered = self.render(profiles=("*",))
        services = rendered["services"]
        publishers = {
            name: service["ports"] for name, service in services.items() if service.get("ports")
        }
        self.assertEqual(set(publishers), {"caddy"})
        self.assertEqual(
            {(int(p["published"]), p["target"], p.get("protocol", "tcp")) for p in publishers["caddy"]},
            {(80, 80, "tcp"), (443, 443, "tcp")},
        )
        for port in publishers["caddy"]:
            self.assertIn(port.get("host_ip", ""), ("", "0.0.0.0", "::"))
        for name, service in services.items():
            self.assertNotEqual(service.get("network_mode"), "host", name)

        for name in ("api", "worker"):
            env = services[name]["environment"]
            self.assertEqual(env["APP_DEV_MODE"], "false", name)
            self.assertEqual(env["QRAFT_AUTH_SECURE_COOKIE"], "true", name)
        api = services["api"]["environment"]
        self.assertEqual(api["QRAFT_AUTH_TRUST_PROXY"], "true")
        self.assertEqual(api["QRAFT_ALLOWED_ORIGINS"], "")
        for scope in (services["frontend"]["environment"], services["frontend"]["build"]["args"]):
            self.assertEqual(scope["NEXT_PUBLIC_API_URL"], "")
            self.assertEqual(scope["NEXT_PUBLIC_WS_URL"], "")

        mounts = {mount["target"]: mount for mount in services["caddy"]["volumes"]}
        self.assertEqual(
            Path(mounts["/etc/caddy/Caddyfile"]["source"]).resolve(),
            (SOURCE_ROOT / "deploy/caddy/Caddyfile.cloud").resolve(),
        )
        self.assertTrue(mounts["/etc/caddy/Caddyfile"]["read_only"])
        for target, volume in (("/data", "caddy_data"), ("/config", "caddy_config")):
            self.assertEqual(mounts[target]["type"], "volume")
            self.assertEqual(mounts[target]["source"], volume)
            self.assertIn(volume, rendered["volumes"])
        self.assertEqual(services["caddy"]["environment"]["QRAFT_PUBLIC_HOST"], "qraft.example.com")

        self.assertEqual(services["temporal-ui"]["profiles"], ["admin-tools"])
        self.assertNotIn("temporal-ui", default["services"])
        self.assertEqual(set(services["sandbox"]["networks"]), {"sandbox_internal"})
        self.assertTrue(rendered["networks"]["sandbox_internal"]["internal"])
        self.assertIn("sandbox_internal", services["worker"]["networks"])
        self.assertEqual(services["worker"]["depends_on"]["sandbox"]["condition"], "service_healthy")


if __name__ == "__main__":
    unittest.main()
