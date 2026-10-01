#!/usr/bin/env python3
"""Assemble only declared, hash-checked regular files into a deterministic archive."""
import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import tarfile


def valid_path(value):
    path = PurePosixPath(value)
    if not value or path.is_absolute() or str(path) != value or ".." in path.parts or "\\" in value:
        raise ValueError(f"unsafe archive path: {value!r}")
    return path


def assemble(root, manifest, output, allow_declared_symlinks=False):
    receipt = json.loads(manifest.read_text())
    seen = set()
    with output.open("wb") as raw, gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w", format=tarfile.PAX_FORMAT) as archive:
            for record in receipt["files"]:
                name = record["path"]
                path = valid_path(name)
                if name in seen or name == "SOURCE_RECEIPT.json":
                    raise ValueError(f"duplicate/reserved path: {name}")
                seen.add(name)
                source = root / path
                if not allow_declared_symlinks and any(parent.is_symlink() for parent in [source, *source.parents]):
                    raise ValueError(f"symlink source: {name}")
                content = source.read_bytes()
                if hashlib.sha256(content).hexdigest() != record["sha256"]:
                    raise ValueError(f"hash mismatch: {name}")
                info = tarfile.TarInfo(name)
                info.size = len(content)
                info.mode = record["mode"]
                archive.addfile(info, io.BytesIO(content))
            content = json.dumps(receipt, indent=2, sort_keys=True).encode() + b"\n"
            info = tarfile.TarInfo("SOURCE_RECEIPT.json")
            info.size = len(content)
            info.mode = 0o644
            archive.addfile(info, io.BytesIO(content))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--allow-declared-symlinks", action="store_true", help="permit Bazel sandbox input links; content hashes are still required")
    args = parser.parse_args()
    assemble(args.root, args.manifest, args.output, args.allow_declared_symlinks)


if __name__ == "__main__":
    main()
