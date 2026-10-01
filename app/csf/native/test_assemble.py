import hashlib
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

import assemble


class NativePackageTests(unittest.TestCase):
    def payload(self, root, name, version, files):
        directory = root / name
        records = []
        for path, content in files.items():
            target = directory / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(content)
            target.chmod(0o755)
            records.append({"path": path, "sha256": hashlib.sha256(content).hexdigest(), "mode": 0o755})
        (directory / assemble.RECEIPT_NAME).write_text(json.dumps({
            "component": name,
            "version": version,
            "target": "debian-12-linux-amd64",
            "source": f"fixture://{name}",
            "source_sha256": hashlib.sha256(name.encode()).hexdigest(),
            "files": records,
        }))

    def test_fixture_package_is_deterministic_and_receipted(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            manifest = json.loads((assemble.ROOT / "manifest.json").read_text())
            for name in ("langfuse", "object-storage", "postgresql", "clickhouse", "redis"):
                component = manifest["components"][name]
                required = component["required_files"] + component["executables"]
                self.payload(root, name, component["version"], {path: f"fixture {name} {path}".encode() for path in required})
            first = root / "candace-csf-native-fixture-a.tar.gz"
            second = root / "candace-csf-native-fixture-b.tar.gz"
            receipt = assemble.assemble(assemble.ROOT / "manifest.json", root, first, ["langfuse"], "bundled")
            assemble.assemble(assemble.ROOT / "manifest.json", root, second, ["langfuse"], "bundled")
            self.assertEqual(first.read_bytes(), second.read_bytes())
            self.assertEqual(receipt["components"], ["clickhouse", "langfuse", "object-storage", "postgresql", "redis"])
            with tarfile.open(first) as archive:
                names = archive.getnames()
                self.assertIn("opt/candace/csf/components/langfuse/web/server.js", names)
                self.assertIn("opt/candace/csf/components/object-storage/bin/object-storage", names)
                self.assertIn("usr/lib/systemd/system/candace-csf-langfuse-web.service", names)
                self.assertIn("opt/candace/csf/share/native/NATIVE_RECEIPT.json", names)

    def test_external_langfuse_does_not_bundle_stores(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            component = json.loads((assemble.ROOT / "manifest.json").read_text())["components"]["langfuse"]
            required = component["required_files"] + component["executables"]
            self.payload(root, "langfuse", component["version"], {path: b"fixture" for path in required})
            object_storage = json.loads((assemble.ROOT / "manifest.json").read_text())["components"]["object-storage"]
            self.payload(
                root,
                "object-storage",
                object_storage["version"],
                {path: b"fixture" for path in object_storage["executables"]},
            )
            output = root / "candace-csf-native-fixture-external.tar.gz"
            receipt = assemble.assemble(assemble.ROOT / "manifest.json", root, output, ["langfuse"], "external")
            self.assertEqual(receipt["components"], ["langfuse", "object-storage"])

    def test_hash_mismatch_and_missing_entrypoint_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.payload(root, "csf", "source", {"bin/csf": b"fixture"})
            (root / "csf" / "bin/csf").write_bytes(b"changed")
            with self.assertRaisesRegex(ValueError, "hash mismatch"):
                assemble.assemble(assemble.ROOT / "manifest.json", root, root / "bad.tar.gz", ["csf"], "external")
            receipt = json.loads((root / "csf" / assemble.RECEIPT_NAME).read_text())
            receipt["files"] = []
            (root / "csf" / assemble.RECEIPT_NAME).write_text(json.dumps(receipt))
            (root / "csf" / "bin/csf").unlink()
            with self.assertRaisesRegex(ValueError, "missing required files"):
                assemble.assemble(assemble.ROOT / "manifest.json", root, root / "missing.tar.gz", ["csf"], "external")

    def test_internal_symlink_is_preserved_and_escape_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.payload(root, "redis", "7.4.2", {"bin/redis-server": b"fixture"})
            link = root / "redis/bin/redis-sentinel"
            link.symlink_to("redis-server")
            receipt_path = root / "redis" / assemble.RECEIPT_NAME
            receipt = json.loads(receipt_path.read_text())
            receipt["files"].append({"path": "bin/redis-sentinel", "type": "symlink",
                                     "target": "redis-server", "mode": 0o777,
                                     "sha256": hashlib.sha256(b"redis-server").hexdigest()})
            receipt_path.write_text(json.dumps(receipt))
            output = root / "linked.tar.gz"
            assemble.assemble(assemble.ROOT / "manifest.json", root, output, ["redis"], "external")
            with tarfile.open(output) as archive:
                member = archive.getmember("opt/candace/csf/components/redis/bin/redis-sentinel")
                self.assertTrue(member.issym())
                self.assertEqual(member.linkname, "redis-server")
            link.unlink()
            (root / "outside").write_bytes(b"outside")
            link.symlink_to("../../outside")
            receipt = json.loads(receipt_path.read_text())
            receipt["files"][-1].update(target="../../outside",
                                         sha256=hashlib.sha256(b"../../outside").hexdigest())
            receipt_path.write_text(json.dumps(receipt))
            with self.assertRaisesRegex(ValueError, "escaping|dangling"):
                assemble.assemble(assemble.ROOT / "manifest.json", root, root / "escape.tar.gz", ["redis"], "external")


if __name__ == "__main__":
    unittest.main()
