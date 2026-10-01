#!/usr/bin/env python3
"""Assemble hash-receipted native component payloads into a fixed-layout tar."""

import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import stat
import tarfile


ROOT = Path(__file__).resolve().parent
RECEIPT_NAME = "PAYLOAD_RECEIPT.json"


def safe_path(value):
    path = PurePosixPath(value)
    if path.is_absolute() or not path.parts or any(part in {"", ".", ".."} for part in path.parts):
        raise ValueError(f"unsafe path: {value}")
    return path


def digest(content):
    return hashlib.sha256(content).hexdigest()


def file_digest(path):
    result = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            result.update(chunk)
    return result.hexdigest()


def payload_paths(directory):
    paths = set()
    for current, directories, filenames in os.walk(directory, followlinks=False):
        current_path = Path(current)
        for name in [*directories, *filenames]:
            path = current_path / name
            if path.name != RECEIPT_NAME and (path.is_file() or path.is_symlink()):
                paths.add(str(path.relative_to(directory)))
    return paths


def validate_symlink(directory, source, expected_target):
    target = os.readlink(source)
    if target != expected_target or PurePosixPath(target).is_absolute():
        raise ValueError(f"invalid payload symlink: {source.relative_to(directory)}")
    try:
        resolved = source.resolve(strict=True)
    except (OSError, RuntimeError) as error:
        raise ValueError(f"dangling or cyclic payload symlink: {source.relative_to(directory)}") from error
    if (not resolved.is_relative_to(directory.resolve())
            or (not resolved.is_file() and not resolved.is_dir())
            or (resolved.is_dir() and source.parent.resolve().is_relative_to(resolved))):
        raise ValueError(f"escaping payload symlink: {source.relative_to(directory)}")
    return target


def select_components(manifest, requested, langfuse_dependencies):
    selected = set()
    pending = list(requested)
    while pending:
        name = pending.pop()
        component = manifest["components"].get(name)
        if component is None:
            raise ValueError(f"unknown component: {name}")
        if name in selected:
            continue
        selected.add(name)
        pending.extend(component.get("requires", []))
        if name == "langfuse" and langfuse_dependencies == "bundled":
            pending.extend(component["bundled_requires"])
    return sorted(selected)


def read_payload(payload_root, name, component, target):
    directory = payload_root / name
    receipt_path = directory / RECEIPT_NAME
    if not receipt_path.is_file():
        raise ValueError(f"missing {name}/{RECEIPT_NAME}")
    receipt = json.loads(receipt_path.read_text())
    if (receipt.get("component") != name or receipt.get("version") != component["version"]
            or receipt.get("target") != target):
        raise ValueError(f"{name} receipt identity does not match the package manifest")
    declared = {}
    for item in receipt.get("files", []):
        path = safe_path(item["path"])
        if str(path) in declared:
            raise ValueError(f"duplicate {name} payload path: {path}")
        source = directory.joinpath(*path.parts)
        mode = int(item.get("mode", 0o644))
        kind = item.get("type", "file")
        if kind == "symlink":
            if not source.is_symlink():
                raise ValueError(f"missing {name} payload symlink: {path}")
            link_target = validate_symlink(directory, source, item["target"])
            sha256 = digest(link_target.encode())
            size = 0
            actual_mode = stat.S_IMODE(source.lstat().st_mode)
        elif kind == "file":
            if not source.is_file() or source.is_symlink():
                raise ValueError(f"missing regular {name} payload file: {path}")
            link_target = None
            sha256 = file_digest(source)
            size = source.stat().st_size
            actual_mode = stat.S_IMODE(source.stat().st_mode)
        else:
            raise ValueError(f"unknown {name} payload entry type: {kind}")
        if actual_mode != mode:
            raise ValueError(f"{name} payload mode mismatch: {path}")
        if sha256 != item["sha256"]:
            raise ValueError(f"{name} payload hash mismatch: {path}")
        declared[str(path)] = (source, size, sha256, mode, kind, link_target)
    actual = payload_paths(directory)
    if actual != set(declared):
        raise ValueError(f"{name} payload receipt does not exactly cover its files")
    required = set(component.get("required_files", [])) | set(component["executables"])
    missing = sorted(required - set(declared))
    if missing:
        raise ValueError(f"{name} payload is missing required files: {', '.join(missing)}")
    not_executable = sorted(path for path in component["executables"] if not declared[path][3] & 0o111)
    if not_executable:
        raise ValueError(f"{name} entrypoints are not executable: {', '.join(not_executable)}")
    return receipt, declared


def tar_entry(archive, path, source, size, mode, kind="file", link_target=None):
    info = tarfile.TarInfo(path)
    info.size = size
    info.mode = mode
    info.uid = info.gid = 0
    info.uname = info.gname = "root"
    info.mtime = 0
    if kind == "symlink":
        info.type = tarfile.SYMTYPE
        info.linkname = link_target
        info.size = 0
        archive.addfile(info)
    elif isinstance(source, Path):
        with source.open("rb") as content:
            archive.addfile(info, content)
    else:
        archive.addfile(info, io.BytesIO(source))


def assemble(manifest_path, payload_root, output, requested, langfuse_dependencies):
    manifest = json.loads(manifest_path.read_text())
    if manifest.get("format_version") != 1:
        raise ValueError("unsupported native package manifest")
    selected = select_components(manifest, requested, langfuse_dependencies)
    files = {}
    sources = {}
    for name in selected:
        component = manifest["components"][name]
        receipt, payload = read_payload(payload_root, name, component, manifest["target"])
        sources[name] = {
            "version": receipt["version"],
            "source": receipt["source"],
            "source_sha256": receipt["source_sha256"],
        }
        for path, entry in payload.items():
            files[f"opt/candace/csf/components/{name}/{path}"] = entry
        for unit in component["units"]:
            source = ROOT / "systemd" / unit
            files[f"usr/lib/systemd/system/{unit}"] = (source, source.stat().st_size, file_digest(source), 0o644, "file", None)
    for directory, destination in (("config", "opt/candace/csf/share/native/config"),
                                   ("libexec", "opt/candace/csf/libexec"),
                                   ("sysusers", "usr/lib/sysusers.d")):
        for source in sorted((ROOT / directory).glob("*")):
            if source.is_file():
                mode = 0o755 if directory == "libexec" else 0o644
                files[f"{destination}/{source.name}"] = (source, source.stat().st_size, file_digest(source), mode, "file", None)
    package_receipt = {
        "format_version": 1,
        "target": manifest["target"],
        "install_prefix": manifest["install_prefix"],
        "components": selected,
        "langfuse_dependencies": langfuse_dependencies if "langfuse" in selected else None,
        "sources": sources,
        "files": [
            {"path": path, "sha256": sha256, "mode": oct(mode), "type": kind,
             **({"target": link_target} if kind == "symlink" else {})}
            for path, (_, _, sha256, mode, kind, link_target) in sorted(files.items())
        ],
    }
    receipt_content = (json.dumps(package_receipt, indent=2, sort_keys=True) + "\n").encode()
    files["opt/candace/csf/share/native/NATIVE_RECEIPT.json"] = (
        receipt_content, len(receipt_content), digest(receipt_content), 0o644, "file", None)
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.PAX_FORMAT) as archive:
                for path, (source, size, _, mode, kind, link_target) in sorted(files.items()):
                    tar_entry(archive, path, source, size, mode, kind, link_target)
    return package_receipt


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", type=Path, default=ROOT / "manifest.json")
    parser.add_argument("--payload-root", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--components", default="csf")
    parser.add_argument("--langfuse-dependencies", choices=("bundled", "external"), default="external")
    args = parser.parse_args()
    requested = [name for name in args.components.split(",") if name]
    receipt = assemble(args.manifest, args.payload_root, args.output, requested, args.langfuse_dependencies)
    print(json.dumps(receipt, sort_keys=True))


if __name__ == "__main__":
    main()
