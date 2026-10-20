"""驱动真实发布脚本，模拟Docker出口，检查失败时序和应用回滚。"""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]

FAKE_DOCKER = r'''#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "$TEST_ROOT/commands"
if [[ "$1" == inspect ]]; then
  if [[ "$*" == *container-backend* && "$TEST_SCENARIO" == backend_fail ]] &&
    grep -q newbackend "$TEST_ROOT/shared/.release.env"; then
    echo unhealthy
  elif [[ "$*" == *container-frontend* ]]; then echo running
  else echo healthy; fi
  exit 0
fi
[[ "$1" == compose ]] || exit 0
shift
if [[ "$1" == version ]]; then exit 0; fi
while [[ "$1" == --* ]]; do shift 2; done
command="$1"; shift
case "$command" in
  ps) [[ "${1:-}" != -q ]] || echo "container-$2" ;;
  exec)
    if [[ "$*" == *pg_dump* ]]; then
      [[ "$TEST_SCENARIO" != backup_fail ]] || exit 1
      printf 'archive-data'
    elif [[ "$*" == *pg_restore* ]]; then
      cat >/dev/null
      [[ "$TEST_SCENARIO" != corrupt_backup ]] || exit 1
    elif [[ "$*" == *psql* ]]; then
      cat > "$TEST_ROOT/migration-batch.sql"
      [[ "$TEST_SCENARIO" != migration_fail ]] || exit 1
    else exit 2; fi ;;
esac
'''


class DeploymentTests(unittest.TestCase):
    def execute(self, scenario="success", previous=True, missing_migration=False):
        # 子目录只包含虚构配置、归档与Docker命令，不含任何实际数据库凭据。
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            release = root / "release"
            release.mkdir()
            (root / "shared").mkdir()
            fake_bin = root / "bin"
            fake_bin.mkdir()
            for name in ("deploy.sh", "database.sh", "migrations.list", "compose.prod.yaml"):
                shutil.copyfile(ROOT / "deploy" / name, release / name)
            (release / "migrations").mkdir()
            for name in (release / "migrations.list").read_text().splitlines():
                shutil.copyfile(ROOT / "scripts/migrations" / name, release / "migrations" / name)
            if missing_migration:
                (release / "migrations/20261017_message_embedding_jobs.sql").unlink()
            for name in (".env", "config.yaml"):
                (root / "shared" / name).write_text("APP_PORT=18888\n")
            old = "BACKEND_IMAGE=ghcr.io/test/oldbackend:abc1234\nFRONTEND_IMAGE=ghcr.io/test/oldfrontend:abc1234\n"
            if previous:
                (root / "shared/.release.env").write_text(old)
            fake = fake_bin / "docker"
            fake.write_text(FAKE_DOCKER, encoding="utf-8", newline="\n")
            fake.chmod(0o755)
            env = dict(os.environ, SOLVIFY_ROOT=str(root), TEST_ROOT=str(root), TEST_SCENARIO=scenario)
            env["PATH"] = str(fake_bin) + os.pathsep + env["PATH"]
            result = subprocess.run(["bash", str(release / "deploy.sh"),
                "ghcr.io/test/newbackend:def5678", "ghcr.io/test/newfrontend:def5678", "def5678"],
                capture_output=True, text=True, env=env, timeout=20)
            commands = (root / "commands").read_text() if (root / "commands").exists() else ""
            state_file = root / "shared/.release.env"
            state = state_file.read_text() if state_file.exists() else None
            backups = list((root / "shared/backups").glob("*.dump"))
            batch = (root / "migration-batch.sql").read_text() if (root / "migration-batch.sql").exists() else None
            return result, commands, state, old, len(backups), batch

    def test_backup_precedes_migration_and_application_update(self):
        result, commands, state, _, backups, batch = self.execute()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(backups, 1)
        self.assertIn("newbackend", state)
        self.assertLess(commands.index("pg_dump"), commands.index("pg_restore --list"))
        self.assertLess(commands.index("pg_restore --file=/dev/null"), commands.index("psql -X"))
        self.assertLess(commands.index("psql -X"), commands.index("up -d --no-deps backend"))
        self.assertIn("pg_advisory_lock", batch)
        self.assertIn("20261017_message_embedding_jobs", batch)

    def test_dump_failure_preserves_running_app_and_release(self):
        result, commands, state, old, backups, batch = self.execute("backup_fail")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(state, old)
        self.assertEqual(backups, 0)
        self.assertIsNone(batch)
        self.assertNotIn("up -d --no-deps", commands)
        self.assertNotIn("stop frontend backend", commands)

    def test_corrupt_backup_stops_before_sql_or_update(self):
        result, commands, state, old, backups, batch = self.execute("corrupt_backup")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(state, old)
        self.assertEqual(backups, 0)
        self.assertIsNone(batch)
        self.assertNotIn("up -d --no-deps", commands)

    def test_migration_failure_keeps_backup_and_application(self):
        result, commands, state, old, backups, _ = self.execute("migration_fail")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(state, old)
        self.assertEqual(backups, 1)
        self.assertNotIn("up -d --no-deps", commands)

    def test_first_release_failure_does_not_leave_candidate_state(self):
        result, commands, state, _, _, _ = self.execute("backup_fail", previous=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIsNone(state)
        self.assertNotIn("up -d --no-deps", commands)

    def test_application_health_failure_rolls_back_images(self):
        result, commands, state, old, _, _ = self.execute("backend_fail")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(state, old)
        self.assertEqual(commands.count("up -d --no-deps backend"), 2)
        self.assertIn("up -d --no-deps frontend", commands)
        self.assertIn("上一版本恢复完成", result.stdout)

    def test_incomplete_package_fails_before_containers_change(self):
        result, commands, _, _, _, _ = self.execute(missing_migration=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("up -d", commands)
        self.assertNotIn("pg_dump", commands)


if __name__ == "__main__":
    unittest.main()
