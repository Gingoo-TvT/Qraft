import base64
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("qraft_configure", Path(__file__).with_name("configure.py"))
configure = importlib.util.module_from_spec(spec)
spec.loader.exec_module(configure)
SOURCE_ROOT = configure.ROOT
VERSION = "10000000-0000-4000-8000-000000000001"


class ConfigurationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / ".env.example").write_text((SOURCE_ROOT / ".env.example").read_text())
        self.patches = [
            mock.patch.object(configure, "ROOT", self.root),
            mock.patch.object(configure, "ENV", self.root / ".env"),
            mock.patch.object(configure, "capture", return_value="old-test-revision"),
        ]
        for patch in self.patches:
            patch.start()
            self.addCleanup(patch.stop)
        self.output = io.StringIO()
        patch = contextlib.redirect_stdout(self.output)
        patch.__enter__()
        self.addCleanup(patch.__exit__, None, None, None)
        configure.initialize()

    def saved(self, data=None, selected=""):
        data = data if data is not None else {
            "configured": True,
            "base_url": "https://embedding.example/v1",
            "model": "synthetic-model",
            "dimensions": 1536,
            "timeout_sec": 30,
            "model_version_id": VERSION,
            "api_key": "must-not-leave-response",
        }
        response = io.BytesIO(json.dumps({"success": True, "data": data}).encode())
        with mock.patch.object(configure.urllib.request, "build_opener") as factory:
            factory.return_value.open.return_value = response
            configure.saved_embedding("http://localhost:18180", selected)
            return factory.return_value.open.call_args

    def test_fresh_instance_is_unconfigured_and_repeat_preserves_keys(self):
        before = configure.ENV.read_bytes()
        configure.initialize()
        self.assertEqual(before, configure.ENV.read_bytes())
        env = configure.read_env(configure.ENV)
        self.assertEqual(len(base64.b64decode(env["ALGOFORGE_SETTINGS_ENCRYPTION_KEY"])), 32)
        self.assertNotEqual(env["POSTGRES_PASSWORD"], env["TEMPORAL_DB_PASSWORD"])
        self.assertTrue(env["JWT_SECRET"])
        self.assertEqual(env["ALGOFORGE_EMBEDDING_ENABLED"], "false")
        self.assertEqual(env["ALGOFORGE_EMBEDDING_UI_ALLOW_PUBLIC"], "true")
        self.assertEqual(env["ALGOFORGE_EMBEDDING_BASE_URL"], "")
        self.assertEqual(env["ALGOFORGE_EMBEDDING_MODEL"], "")
        self.assertEqual(env["ALGOFORGE_LLM_API_KEY"], "")
        with self.assertRaises(ValueError):
            configure.update({"ALGOFORGE_EMBEDDING_MODEL": "model\nJWT_SECRET=changed"})
        self.assertEqual(before, configure.ENV.read_bytes())

    def test_saved_identity_enables_runtime_without_exporting_or_replacing_keys(self):
        configure.update({"ALGOFORGE_LLM_API_KEY": "user-llm-key", "ALGOFORGE_EMBEDDING_API_KEY": "user-env-key"})
        before = configure.read_env(configure.ENV)
        request = self.saved()
        after = configure.read_env(configure.ENV)
        self.assertEqual(request.args[0], "http://localhost:18180/api/v1/embedding/saved-runtime-settings")
        self.assertEqual(after["ALGOFORGE_EMBEDDING_ENABLED"], "true")
        self.assertEqual(after["ALGOFORGE_EMBEDDING_EXPECTED_STATEMENT_MODEL_VERSION_ID"], VERSION)
        self.assertEqual(after["ALGOFORGE_EMBEDDING_MODEL"], "synthetic-model")
        for key in ("POSTGRES_PASSWORD", "JWT_SECRET", "MINIO_SECRET_KEY",
                    "ALGOFORGE_SETTINGS_ENCRYPTION_KEY", "ALGOFORGE_LLM_API_KEY",
                    "ALGOFORGE_EMBEDDING_API_KEY", "SOURCE_REVISION", "SANDBOX_REVISION"):
            self.assertEqual(after[key], before[key])
        self.assertNotIn("must-not-leave-response", configure.ENV.read_text())
        self.assertNotIn("user-llm-key", self.output.getvalue())

    def test_explicit_version_is_sent_for_ambiguous_model_identity(self):
        request = self.saved(selected=VERSION)
        self.assertTrue(request.args[0].endswith("?model_version_id=" + VERSION))
        before = configure.ENV.read_bytes()
        with self.assertRaises(ValueError):
            self.saved(selected="not-a-uuid")
        self.assertEqual(before, configure.ENV.read_bytes())

    def test_incomplete_or_mismatched_identity_does_not_enable_runtime(self):
        good = {
            "configured": True, "base_url": "https://embedding.example/v1",
            "model": "synthetic-model", "dimensions": 1536,
            "timeout_sec": 30, "model_version_id": VERSION,
        }
        for change in ({"configured": False}, {"model": ""}, {"model": None},
                       {"timeout_sec": 0}, {"dimensions": 768},
                       {"base_url": ""}, {"model_version_id": "not-a-uuid"}):
            with self.subTest(change=change):
                before = configure.ENV.read_bytes()
                with self.assertRaises(ValueError):
                    self.saved({**good, **change})
                self.assertEqual(before, configure.ENV.read_bytes())
        before = configure.ENV.read_bytes()
        with self.assertRaises(ValueError):
            self.saved(good, selected="20000000-0000-4000-8000-000000000002")
        self.assertEqual(before, configure.ENV.read_bytes())

    def test_only_explicit_build_refresh_changes_revisions(self):
        configure.update({"ALGOFORGE_LLM_API_KEY": "user-owned-key", "HTTP_PORT": "19180"})
        before = configure.ENV.read_bytes()
        configure.capture.return_value = "new-test-revision"
        configure.initialize()
        self.assertEqual(before, configure.ENV.read_bytes())
        old = configure.read_env(configure.ENV)
        configure.refresh_revision()
        new = configure.read_env(configure.ENV)
        changed = {key for key in old if old[key] != new[key]}
        self.assertEqual(changed, {"SOURCE_REVISION", "SANDBOX_REVISION"})
        self.assertEqual(new["SOURCE_REVISION"], "new-test-revision")
        self.assertEqual(new["SANDBOX_REVISION"], "new-test-revision")

    @unittest.skipUnless(shutil.which("docker"), "Docker CLI is unavailable")
    def test_compose_resolves_both_configuration_and_generation_stages(self):
        # Docker Compose only renders configuration; it does not contact the
        # daemon, start containers, or modify this checkout's real .env.
        command = ["docker", "compose", "--project-directory", str(SOURCE_ROOT),
                   "--env-file", str(configure.ENV), "-f",
                   str(SOURCE_ROOT / "docker-compose.yml"), "config", "--format", "json"]
        clean_env = {"PATH": os.environ["PATH"]}
        for key in ("HOME", "USERPROFILE", "SYSTEMROOT"):
            if key in os.environ:
                clean_env[key] = os.environ[key]
        def rendered():
            result = subprocess.run(command, text=True, capture_output=True, env=clean_env)
            self.assertEqual(result.returncode, 0, result.stderr)
            return json.loads(result.stdout)
        initial = rendered()
        self.assertEqual(initial["services"]["api"]["environment"]["ALGOFORGE_EMBEDDING_ENABLED"], "false")
        self.assertEqual(initial["services"]["api"]["environment"]["ALGOFORGE_EMBEDDING_BASE_URL"], "")
        self.assertEqual(initial["services"]["api"]["environment"]["ALGOFORGE_EMBEDDING_UI_ALLOW_PUBLIC"], "true")
        self.saved(selected=VERSION)
        configure.capture.return_value = "current-build-revision"
        configure.refresh_revision()
        ready = rendered()
        for service in ("api", "worker"):
            env = ready["services"][service]["environment"]
            self.assertEqual(env["ALGOFORGE_EMBEDDING_ENABLED"], "true")
            self.assertEqual(env["ALGOFORGE_EMBEDDING_BASE_URL"], "https://embedding.example/v1")
            self.assertEqual(env["ALGOFORGE_EMBEDDING_EXPECTED_STATEMENT_MODEL_VERSION_ID"], VERSION)
            self.assertEqual(ready["services"][service]["build"]["args"]["SOURCE_REVISION"], "current-build-revision")
        self.assertEqual(ready["services"]["sandbox"]["build"]["args"]["SANDBOX_REVISION"], "current-build-revision")


if __name__ == "__main__":
    unittest.main()
