#!/usr/bin/env python3
"""Prepare immutable public CSF release sources; executed by bootstrap.sh's Docker image."""
from __future__ import annotations

import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import sys
import tarfile
import tempfile
import urllib.error
import urllib.parse
import urllib.request

PUBLIC_REPOSITORY = "candacelabs/csf"
RELEASES_URL = f"https://github.com/{PUBLIC_REPOSITORY}/releases"
RAW_URL = f"https://raw.githubusercontent.com/{PUBLIC_REPOSITORY}"
VERSION_PATTERN = re.compile(r"v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)")
REVISION_PATTERN = re.compile(r"[0-9a-f]{40}")
DIGEST_PATTERN = re.compile(r"[0-9a-f]{64}")
RECEIPT_NAME = ".csf-bootstrap.json"
BUILD_OUTPUT = "tools/csf-operator/target"
METADATA_LIMIT = 256 * 1024
BUFFER_SIZE = 1024 * 1024


class BootstrapError(Exception):
    """An install that must stop without replacing existing release sources."""


class HTTPSOnlyRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, new_url):
        if urllib.parse.urlsplit(new_url).scheme != "https":
            raise BootstrapError("Refusing an HTTPS downgrade while downloading CSF.")
        return super().redirect_request(request, response, code, message, headers, new_url)


def open_https(url):
    if urllib.parse.urlsplit(url).scheme != "https":
        raise BootstrapError("CSF release downloads require HTTPS.")
    opener = urllib.request.build_opener(HTTPSOnlyRedirect())
    return opener.open(urllib.request.Request(url, headers={"User-Agent": "csf-bootstrap"}), timeout=60)


def read_metadata(url, opener):
    with opener(url) as response:
        data = response.read(METADATA_LIMIT + 1)
    if len(data) > METADATA_LIMIT:
        raise BootstrapError("Release metadata exceeds the supported size.")
    return data


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise BootstrapError(f"Duplicate JSON field in release provenance: {key}")
        result[key] = value
    return result


def decode_object(data):
    try:
        result = json.loads(data, object_pairs_hook=unique_object)
    except (ValueError, UnicodeError) as error:
        raise BootstrapError("Invalid JSON release provenance.") from error
    if not isinstance(result, dict):
        raise BootstrapError("Release provenance must be a JSON object.")
    return result


def sha256_file(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(BUFFER_SIZE), b""):
            digest.update(chunk)
    return digest.hexdigest()


def download_archive(url, destination, expected, opener):
    digest = hashlib.sha256()
    with opener(url) as response, destination.open("xb") as output:
        while chunk := response.read(BUFFER_SIZE):
            output.write(chunk)
            digest.update(chunk)
    if digest.hexdigest() != expected:
        raise BootstrapError("Source archive checksum mismatch; nothing was installed.")


def archive_members(archive, prefix):
    """Validate all paths, types and links before writing a single member."""
    members = {}
    for member in archive.getmembers():
        name = member.name.rstrip("/")
        parts = name.split("/")
        if (not name or any(part in ("", ".", "..") for part in parts)
                or parts[0] != prefix or "\\" in name
                or any(ord(char) < 32 or ord(char) == 127 for char in name)):
            raise BootstrapError(f"Unsafe archive member path: {name!r}")
        if name in members:
            raise BootstrapError(f"Duplicate archive member: {name!r}")
        if not (member.isdir() or member.isreg() or member.issym()) or member.mode & 0o7000:
            raise BootstrapError(f"Unsupported archive member type or mode: {name!r}")
        members[name] = member
    if prefix not in members or not members[prefix].isdir():
        raise BootstrapError("Archive is missing its source-root directory.")
    for name, member in members.items():
        for parent in PurePosixPath(name).parents:
            if str(parent) == ".":
                break
            if str(parent) not in members or not members[str(parent)].isdir():
                raise BootstrapError("Archive member traverses a non-directory.")
        if member.issym():
            target = member.linkname
            if (not target or target.startswith("/") or "\\" in target
                    or any(ord(char) < 32 or ord(char) == 127 for char in target)):
                raise BootstrapError("Archive symlink must resolve directly inside the source root.")
            # Check each traversed component before processing a later '..'.
            # Normalizing first would miss an intermediate symlink escape.
            components = name.split("/")[:-1]
            target_parts = target.split("/")
            for index, part in enumerate(target_parts):
                if part in ("", "."):
                    continue
                if part == "..":
                    if len(components) <= 1:
                        raise BootstrapError("Archive symlink escapes its source root.")
                    components.pop()
                else:
                    components.append(part)
                traversed = "/".join(components)
                if index < len(target_parts) - 1 and (traversed not in members or not members[traversed].isdir()):
                    raise BootstrapError("Archive symlink target traverses a symlink.")
            resolved = "/".join(components)
            if resolved not in members or not (members[resolved].isreg() or members[resolved].isdir()):
                raise BootstrapError("Archive symlink must resolve directly inside the source root.")
    return members


def extract_archive(archive_path, destination, prefix):
    # Materialize directories/files before symlinks; never call extractall on
    # untrusted headers. Validated ancestors are directories, not symlinks.
    with tarfile.open(archive_path, "r:gz") as archive:
        members = archive_members(archive, prefix)
        for name, member in sorted(members.items(), key=lambda item: (len(item[0]), item[0])):
            path = destination.joinpath(*PurePosixPath(name).parts[1:])
            if member.isdir():
                path.mkdir(mode=0o755)
            elif member.isreg():
                with archive.extractfile(member) as source, path.open("xb") as output:
                    shutil.copyfileobj(source, output, BUFFER_SIZE)
                path.chmod(member.mode & 0o777)
        for name, member in members.items():
            if member.issym():
                destination.joinpath(*PurePosixPath(name).parts[1:]).symlink_to(member.linkname)


def source_inventory(root):
    inventory = {}
    for directory, directories, files in os.walk(root, followlinks=False):
        for name in list(directories) + files:
            path = Path(directory) / name
            relative = path.relative_to(root).as_posix()
            mode = path.lstat().st_mode
            if relative == BUILD_OUTPUT:
                if not stat.S_ISDIR(mode):
                    raise BootstrapError("Existing build output root is not a directory.")
                directories.remove(name)
                continue
            if path.parent == root and name.startswith("bazel-") and stat.S_ISLNK(mode):
                continue
            if stat.S_ISLNK(mode):
                value = ("symlink", os.readlink(path))
            elif stat.S_ISDIR(mode):
                value = ("directory",)
            elif stat.S_ISREG(mode):
                value = ("file", stat.S_IMODE(mode), sha256_file(path))
            else:
                raise BootstrapError("Existing release contains a special file.")
            inventory[relative] = value
    return inventory


@contextmanager
def installation_lock(releases):
    lock = releases / ".source-lock"
    try:
        lock.mkdir(mode=0o700)
    except FileExistsError as error:
        raise BootstrapError("Another installation is active, or .source-lock needs inspection after an interruption.") from error
    try:
        yield
    finally:
        lock.rmdir()


def prepare_install(releases, version=None, opener=None):
    opener = opener or open_https
    if version is None:
        try:
            with opener(f"{RELEASES_URL}/latest") as response:
                effective = response.geturl()
        except (OSError, urllib.error.URLError) as error:
            raise BootstrapError("No public CSF release could be resolved. No private or staging source will be used.") from error
        prefix = f"{RELEASES_URL}/tag/"
        if not effective.startswith(prefix):
            raise BootstrapError("Latest public release did not resolve to a CSF release tag.")
        version = effective[len(prefix):]
    if not VERSION_PATTERN.fullmatch(version):
        raise BootstrapError("Release version must be vMAJOR.MINOR.PATCH.")
    releases = Path(releases)
    if not releases.is_absolute():
        raise BootstrapError("Release directory must be absolute.")
    releases.mkdir(parents=True, exist_ok=True, mode=0o700)
    releases = releases.resolve()
    with installation_lock(releases), tempfile.TemporaryDirectory(prefix=".download-", dir=releases) as temporary:
        temporary = Path(temporary)
        try:
            marker = decode_object(read_metadata(f"{RAW_URL}/{version}/.candace-export.json", opener))
            revision = marker.get("source_revision")
            if not isinstance(revision, str) or not REVISION_PATTERN.fullmatch(revision):
                raise BootstrapError("Public export provenance must contain one full source_revision.")
            if marker.get("destination_repository") != PUBLIC_REPOSITORY or marker.get("source_path") != "candace":
                raise BootstrapError("Release provenance is not the public CSF export.")
            prefix = f"csf-{revision[:12]}"
            asset = f"{prefix}.tar.gz"
            url = f"{RELEASES_URL}/download/{version}/{asset}"
            checksum = read_metadata(url + ".sha256", opener).decode("ascii")
            expected, separator, filename = checksum.rstrip("\n").partition("  ")
            if not DIGEST_PATTERN.fullmatch(expected) or not separator or filename != asset or checksum != f"{expected}  {asset}\n":
                raise BootstrapError("Invalid release checksum sidecar.")
            archive_path = temporary / "source.tar.gz"
            download_archive(url, archive_path, expected, opener)
        except (OSError, urllib.error.URLError) as error:
            raise BootstrapError(f"Public release {version} is missing usable provenance or release assets: {error}") from error
        source = temporary / "source"
        extract_archive(archive_path, source, prefix)
        identity_path = source / ".candace-source.json"
        if not identity_path.is_file() or identity_path.is_symlink():
            raise BootstrapError("Archive source identity must be a regular file.")
        identity = decode_object(identity_path.read_bytes())
        tree = identity.get("source_tree")
        if (identity.get("format_version") != 1 or identity.get("artifact_kind") != "source_archive"
                or identity.get("source_revision") != revision or not isinstance(tree, str)
                or not REVISION_PATTERN.fullmatch(tree)):
            raise BootstrapError("Archive source identity does not match the release.")
        installer = source / "install.sh"
        if not installer.is_file() or installer.is_symlink() or not os.access(installer, os.X_OK):
            raise BootstrapError("Release has no executable install.sh.")
        receipt = {"version": version, "source_revision": revision, "sha256": expected}
        destination = releases / version
        if destination.is_symlink():
            raise BootstrapError("Refusing to replace a release symlink.")
        if destination.exists():
            record = destination / RECEIPT_NAME
            existing = destination / "source"
            if (not destination.is_dir() or not record.is_file() or record.is_symlink()
                    or decode_object(record.read_bytes()) != receipt):
                raise BootstrapError("Existing release is unrelated or has different release bytes; refusing to overwrite it.")
            if not existing.is_dir() or existing.is_symlink() or source_inventory(existing) != source_inventory(source):
                raise BootstrapError("Existing release source has different files, bytes, or modes; refusing to overwrite it.")
        else:
            # mkdir atomically claims the name and cannot replace a racing or
            # unrelated directory. Incomplete publication is never reused.
            destination.mkdir(mode=0o700)
            try:
                source.rename(destination / "source")
                with (destination / RECEIPT_NAME).open("x") as output:
                    json.dump(receipt, output, sort_keys=True)
                    output.write("\n")
            except BaseException:
                shutil.rmtree(destination)
                raise
        print(f"[PASS] Verified CSF {version}, source {revision}.", file=sys.stderr)
        return destination / "source"


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", help="immutable public release vMAJOR.MINOR.PATCH; default latest")
    parser.add_argument("--releases", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        print(prepare_install(args.releases, args.version))
    except (BootstrapError, OSError, tarfile.TarError, UnicodeError) as error:
        print(f"[FAIL] CSF installation: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
