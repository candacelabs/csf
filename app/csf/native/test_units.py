import re
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent
UNITS = ROOT / "systemd"
CONFIG = ROOT / "config"
LIBEXEC = ROOT / "libexec"


def unit(name):
    return (UNITS / name).read_text()


def executed_argv(script, environment=None):
    with tempfile.TemporaryDirectory() as temporary:
        root = Path(temporary)
        capture, binary, fixture = root / "argv", root / "csf", root / "helper"
        binary.write_text('#!/bin/sh\nprintf "%s\\n" "$@" > "$ARGV_CAPTURE"\n')
        binary.chmod(0o755)
        fixture.write_text(
            script.read_text().replace(
                "/opt/candace/csf/components/csf/bin/csf", str(binary),
            )
        )
        fixture.chmod(0o755)
        result = subprocess.run(
            ["sh", fixture],
            check=False,
            capture_output=True,
            text=True,
            env={**os.environ, "ARGV_CAPTURE": str(capture), **(environment or {})},
        )
        if result.returncode != 0:
            raise AssertionError(f"{script}: {result.stderr}")
        return capture.read_text().splitlines()


class NativeUnitTests(unittest.TestCase):
    def test_unit_files_have_sections_and_absolute_exec_paths(self):
        for path in sorted(UNITS.glob("*.service")):
            content = path.read_text()
            self.assertIn("[Unit]", content, path)
            self.assertIn("[Service]", content, path)
            for command in re.findall(r"^Exec(?:Start|StartPre|StartPost)=([^\n]+)", content, re.MULTILINE):
                executable = command.split()[0]
                self.assertTrue(executable.startswith("/"), f"{path}: {executable}")
                self.assertNotIn("|", command, f"{path}: {command}")

    def test_unit_entrypoints_match_packaged_components_or_helpers(self):
        expected = {
            "candace-csf.service": "/opt/candace/csf/libexec/csf-serve",
            "candace-csf-initialize.service": "/opt/candace/csf/libexec/csf-initialize",
            "candace-csf-postgresql-init.service": "/opt/candace/csf/libexec/postgresql-first-boot",
            "candace-csf-clickhouse.service": "/opt/candace/csf/components/clickhouse/bin/clickhouse",
            "candace-csf-opensearch.service": "/opt/candace/csf/components/opensearch/bin/opensearch",
            "candace-csf-minio.service": "/opt/candace/csf/components/minio/bin/minio",
        }
        for name, executable in expected.items():
            self.assertIn(f"ExecStart={executable}", unit(name))
            if "/libexec/" in executable:
                self.assertTrue((LIBEXEC / Path(executable).name).is_file(), executable)

    def test_minimal_csf_does_not_require_database_or_search(self):
        content = unit("candace-csf.service")
        self.assertNotIn("candace-csf-initialize.service", content)
        self.assertNotIn("candace-csf-opensearch.service", content)
        self.assertNotIn("--database-config", content)
        self.assertNotIn("--search-url", content)
        self.assertIn("csf-serve", content)
        self.assertIn("no Copilot Workbench UI or runtime", content)

    def test_database_initialization_is_explicit_and_optional(self):
        content = unit("candace-csf-initialize.service")
        self.assertIn("ConditionPathExists=/etc/candace/csf/database.json", content)
        self.assertNotIn("[Install]", content)
        self.assertIn("csf-initialize", content)
        script = (LIBEXEC / "csf-serve").read_text()
        self.assertIn("CSF_SEARCH_URL requires CSF_DATABASE_CONFIG", script)
        self.assertIn("--database-config=", script)
        self.assertIn("--search-url=", script)

    def test_csf_helpers_build_exact_argv_without_literal_plus_tokens(self):
        served = executed_argv(LIBEXEC / "csf-serve")
        self.assertEqual(served, [
            "serve",
            "--listen=127.0.0.1:14111",
            "--origin=http://127.0.0.1:14111",
            "--artifacts=/var/lib/candace-csf/artifacts",
            "--events=/var/lib/candace-csf/events.jsonl",
        ])
        configured = executed_argv(LIBEXEC / "csf-serve", {
            "CSF_DATABASE_CONFIG": "/run/credentials/database.json",
            "CSF_SEARCH_URL": "http://127.0.0.1:19200",
            "CSF_SEARCH_INDEX": "knowledge",
        })
        self.assertEqual(configured[-3:], [
            "--database-config=/run/credentials/database.json",
            "--search-url=http://127.0.0.1:19200",
            "--search-index=knowledge",
        ])
        initialized = executed_argv(LIBEXEC / "csf-initialize", {
            "CSF_DATABASE_CONFIG": "/run/credentials/database.json",
        })
        self.assertEqual(initialized, [
            "initialize",
            "--database-config=/run/credentials/database.json",
        ])

    def test_postgresql_has_a_stable_identity_and_first_boot_roles(self):
        users = (ROOT / "sysusers" / "candace-csf.conf").read_text()
        self.assertIn("u candace-csf-postgresql", users)
        for name in ("candace-csf-postgresql-init.service", "candace-csf-postgresql.service"):
            content = unit(name)
            self.assertIn("User=candace-csf-postgresql", content)
            self.assertIn("Group=candace-csf-postgresql", content)
        first_boot = (LIBEXEC / "postgresql-first-boot").read_text()
        self.assertIn("CREATE ROLE", first_boot)
        self.assertIn("CREATE DATABASE", first_boot)
        self.assertIn(".candace-csf-postgresql-provisioned", first_boot)
        self.assertIn('if [ ! -f', first_boot)
        self.assertIn(
            "ConditionPathExists=!/var/lib/candace-csf-postgresql/.candace-csf-postgresql-provisioned",
            unit("candace-csf-postgresql-init.service"),
        )
        self.assertIn("password_encryption=scram-sha-256", unit("candace-csf-postgresql.service"))
        config = (CONFIG / "postgresql.env.example").read_text()
        self.assertIn("CSF_POSTGRES_PASSWORD", config)
        self.assertIn("LANGFUSE_POSTGRES_PASSWORD", config)

    def test_postgresql_retries_provisioning_after_initdb_without_reinitializing(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            data, bin_directory = root / "data", root / "bin"
            bin_directory.mkdir()
            log, query = root / "initdb.log", root / "queries.sql"
            (bin_directory / "initdb").write_text(
                '#!/bin/sh\n'
                'printf "initdb\\n" >> "$FAKE_LOG"\n'
                'while [ "$#" -gt 0 ]; do\n'
                '  if [ "$1" = -D ]; then shift; mkdir -p "$1"; : > "$1/PG_VERSION"; break; fi\n'
                '  shift\n'
                'done\n'
            )
            (bin_directory / "postgres").write_text(
                '#!/bin/sh\ntrap "exit 0" HUP INT TERM\nwhile :; do sleep 1; done\n'
            )
            (bin_directory / "psql").write_text(
                '#!/bin/sh\n'
                'case "$*" in *--command=*) exit 0 ;; esac\n'
                'cat >> "$FAKE_QUERY"\n'
            )
            for executable in bin_directory.iterdir():
                executable.chmod(0o755)
            script = (LIBEXEC / "postgresql-first-boot").read_text()
            script = script.replace(
                "data=/var/lib/candace-csf-postgresql", f"data={data}",
            ).replace(
                "bin=/opt/candace/csf/components/postgresql/bin", f"bin={bin_directory}",
            )
            fixture = root / "postgresql-first-boot"
            fixture.write_text(script)
            fixture.chmod(0o755)
            environment = {
                "FAKE_LOG": str(log),
                "FAKE_QUERY": str(query),
                "CSF_POSTGRES_USER": "csf",
                "CSF_POSTGRES_DATABASE": "csf",
                "CSF_POSTGRES_PASSWORD": "secret",
                "LANGFUSE_POSTGRES_USER": "langfuse",
                "LANGFUSE_POSTGRES_DATABASE": "langfuse",
                "LANGFUSE_POSTGRES_PASSWORD": "secret",
            }
            for _ in range(2):
                completed = subprocess.run(
                    ["sh", fixture],
                    check=False,
                    capture_output=True,
                    text=True,
                    env={**os.environ, **environment},
                    timeout=5,
                )
                self.assertEqual(completed.returncode, 0, completed.stderr)
                marker = data / ".candace-csf-postgresql-provisioned"
                self.assertTrue(marker.is_file())
                marker.unlink()
            self.assertEqual(log.read_text(), "initdb\n")
            self.assertIn("CREATE ROLE", query.read_text())
            self.assertIn("CREATE DATABASE", query.read_text())

    def test_langfuse_uses_external_aws_s3(self):
        content = (CONFIG / "langfuse.env.example").read_text()
        self.assertIn("LANGFUSE_S3_EVENT_UPLOAD_BUCKET=CHANGE_ME", content)
        self.assertIn("LANGFUSE_S3_EVENT_UPLOAD_REGION=us-east-1", content)
        self.assertIn("LANGFUSE_S3_MEDIA_UPLOAD_BUCKET=CHANGE_ME", content)
        self.assertNotIn("_ENDPOINT=", content)

    def test_optional_minio_is_loopback_only_and_has_writable_mc_state(self):
        content = unit("candace-csf-minio.service")
        self.assertIn("--address 127.0.0.1:19000", content)
        self.assertIn("--console-address 127.0.0.1:19001", content)
        self.assertIn("RuntimeDirectory=candace-csf-minio", content)
        self.assertIn("MC_CONFIG_DIR=/run/candace-csf-minio/mc", content)
        self.assertIn("ExecStartPost=/opt/candace/csf/libexec/minio-create-bucket", content)

    def test_search_and_analytics_configs_bind_loopback(self):
        opensearch = (CONFIG / "opensearch.yml.example").read_text()
        self.assertIn("network.host: 127.0.0.1", opensearch)
        self.assertIn("http.port: 19200", opensearch)
        clickhouse = (CONFIG / "clickhouse.xml.example").read_text()
        self.assertIn("<listen_host>127.0.0.1</listen_host>", clickhouse)
        self.assertIn("<http_port>18123</http_port>", clickhouse)
        self.assertIn("<tcp_port>19090</tcp_port>", clickhouse)
        users = (CONFIG / "clickhouse-users.xml.example").read_text()
        self.assertIn("<profiles>", users)
        self.assertIn("<quotas>", users)

    def test_direct_config_files_have_declared_service_read_boundaries(self):
        permissions = (CONFIG / "permissions.md").read_text()
        expected = {
            "candace-csf.service": "root:candace-csf 0640",
            "candace-csf-clickhouse.service": "root:candace-csf-clickhouse 0640",
            "candace-csf-opensearch.service": "root:candace-csf-opensearch 0640",
        }
        for name, boundary in expected.items():
            self.assertIn(boundary, permissions)
            self.assertNotIn("DynamicUser=yes", unit(name))
        self.assertIn("EnvironmentFile is read by systemd PID 1", permissions)

    def test_langfuse_listeners_bind_loopback(self):
        for name in ("candace-csf-langfuse-web.service", "candace-csf-langfuse-worker.service"):
            self.assertIn("Environment=HOSTNAME=127.0.0.1", unit(name))

    def test_every_helper_passes_shell_syntax_without_activation(self):
        for script in sorted(LIBEXEC.iterdir()):
            if not script.is_file():
                continue
            content = script.read_text()
            if content.startswith("#!/usr/bin/env python3"):
                compile(content, str(script), "exec")
                continue
            completed = subprocess.run(["sh", "-n", script], check=False, capture_output=True, text=True)
            self.assertEqual(completed.returncode, 0, f"{script}: {completed.stderr}")


if __name__ == "__main__":
    unittest.main()
