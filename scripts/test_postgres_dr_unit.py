#!/usr/bin/env python3
"""Docker-free unit tests for Step 11 DR safety primitives."""
from __future__ import annotations

import importlib.util
import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock

MODULE_PATH = Path(__file__).with_name("postgres_dr.py")
spec = importlib.util.spec_from_file_location("postgres_dr", MODULE_PATH)
assert spec and spec.loader
postgres_dr = importlib.util.module_from_spec(spec)
spec.loader.exec_module(postgres_dr)


class PostgresDRUnitTests(unittest.TestCase):
    def test_database_name_validation_rejects_sql_metacharacters(self) -> None:
        self.assertEqual(postgres_dr.validate_db_name("cpim_restore_01"), "cpim_restore_01")
        for value in ["cpim;DROP", "bad-name", "9leading", "x y", "x' OR 1=1 --"]:
            with self.subTest(value=value):
                with self.assertRaises(postgres_dr.DRError):
                    postgres_dr.validate_db_name(value)

    def test_checksum_sidecar_is_verified(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            archive = Path(td) / "backup.dump"
            archive.write_bytes(b"test-archive")
            digest = postgres_dr.sha256_file(archive)
            archive.with_suffix(".dump.sha256").write_text(f"{digest}  backup.dump\n", encoding="utf-8")
            postgres_dr.verify_sidecar_checksum(archive)
            archive.write_bytes(b"tampered")
            with self.assertRaisesRegex(postgres_dr.DRError, "checksum mismatch"):
                postgres_dr.verify_sidecar_checksum(archive)

    def test_restore_refuses_source_database_before_docker_access(self) -> None:
        with tempfile.TemporaryDirectory() as td, mock.patch.dict(os.environ, {"POSTGRES_DB": "cpim"}, clear=False):
            archive = Path(td) / "backup.dump"
            archive.write_bytes(b"non-empty")
            with mock.patch.object(postgres_dr, "ensure_compose_db_running") as docker_probe:
                with self.assertRaisesRegex(postgres_dr.DRError, "refusing in-place restore"):
                    postgres_dr.restore_backup(archive, "cpim")
                docker_probe.assert_not_called()

    def test_compose_command_supports_env_file_without_secret_logging(self) -> None:
        with mock.patch.dict(os.environ, {"CPIM_ENV_FILE": ".env.production", "CPIM_COMPOSE_FILES": "docker-compose.yml:docker-compose.prod.yml"}, clear=False):
            cmd = postgres_dr.compose_base()
        self.assertEqual(cmd[:4], ["docker", "compose", "--env-file", ".env.production"])
        self.assertEqual(cmd.count("-f"), 2)
        self.assertNotIn("DATABASE_URL", " ".join(cmd))


if __name__ == "__main__":
    unittest.main(verbosity=2)
