"""Public bootstrap acceptance with fixture HTTP and isolated install roots."""
from contextlib import contextmanager, redirect_stderr
import importlib.util
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import threading
import urllib.request

import pytest

ROOT = Path(__file__).resolve().parents[3]
BOOTSTRAP = ROOT / "bootstrap.sh"
SPEC = importlib.util.spec_from_file_location("csf_bootstrap", ROOT / "tools/csf-operator/bootstrap.py")
bootstrap = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(bootstrap)
REVISION = "0123456789abcdef0123456789abcdef01234567"
PREFIX = f"csf-{REVISION[:12]}"
VERSION = "v1.2.3"


class FixtureHTTPServer(ThreadingHTTPServer):
    def __init__(self, routes):
        self.routes = routes
        self.requests = []
        super().__init__(("127.0.0.1", 0), FixtureHandler)


class FixtureHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.server.requests.append(self.path)
        response = self.server.routes.get(self.path)
        if response is None:
            self.send_error(404)
            return
        if isinstance(response, tuple):
            self.send_response(302)
            self.send_header("Location", response[1])
            self.end_headers()
            return
        self.send_response(200)
        self.end_headers()
        self.wfile.write(response)

    def log_message(self, *_arguments):
        pass


def archive(extra=None, installer=None):
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz", format=tarfile.USTAR_FORMAT) as output:
        def member(name, content=b"", kind=tarfile.REGTYPE, target="", mode=0o644):
            entry = tarfile.TarInfo(f"{PREFIX}/{name}" if name else PREFIX)
            entry.type = kind
            entry.mode = mode
            entry.linkname = target
            entry.size = len(content) if kind == tarfile.REGTYPE else 0
            output.addfile(entry, io.BytesIO(content) if entry.size else None)
        for directory in ("", "tools", "tools/csf-operator", "queries", "editor"):
            member(directory, kind=tarfile.DIRTYPE, mode=0o755)
        member(".candace-source.json", json.dumps({"source_revision": REVISION, "source_tree": "0" * 40, "format_version": 1, "artifact_kind": "source_archive"}, separators=(",", ":")).encode())
        installer = installer or b'''#!/bin/sh
set -eu
mkdir -p "$(dirname "$CSF_INSTALL_PATH")" "$(dirname "$0")/tools/csf-operator/target"
printf '%s\\n' "$CSF_INSTALL_PATH" >> "$HOME/invocations"
printf 'verified fixture CLI\\n' > "$CSF_INSTALL_PATH"
'''
        member("install.sh", installer, mode=0o755)
        member("queries/highlights.scm", b"(comment) @comment\n")
        member("editor/highlights.scm", kind=tarfile.SYMTYPE, target="../queries/highlights.scm", mode=0o777)
        if extra:
            extra(member)
    return buffer.getvalue()


class BootstrapFixture:
    def __init__(self, temporary, archive_bytes=None):
        self.root = temporary
        self.home = temporary / "home"
        self.home.mkdir()
        self.releases = self.home / ".local/share/csf/releases"
        self.install_path = self.home / ".local/bin/csf"
        self.bin = temporary / "bin"
        self.bin.mkdir()
        self.environment = dict(os.environ, HOME=str(self.home), XDG_DATA_HOME=str(self.home / ".local/share"))
        for name in ("CSF_RELEASES_DIR", "CSF_INSTALL_PATH"):
            self.environment.pop(name, None)
        self.environment["PATH"] = str(self.bin) + os.pathsep + os.environ["PATH"]
        self.routes = {
            "/candacelabs/csf/releases/latest": (302, f"/candacelabs/csf/releases/tag/{VERSION}"),
            f"/candacelabs/csf/releases/tag/{VERSION}": b"release",
            f"/candacelabs/csf/{VERSION}/.candace-export.json": json.dumps({"source_revision": REVISION, "destination_repository": "candacelabs/csf", "source_path": "candace"}, indent=2).encode(),
        }
        self.set_archive(archive_bytes or archive())

    def set_archive(self, content):
        self.asset = f"/candacelabs/csf/releases/download/{VERSION}/{PREFIX}.tar.gz"
        self.routes[self.asset] = content
        digest = hashlib.sha256(content).hexdigest()
        self.routes[self.asset + ".sha256"] = f"{digest}  {PREFIX}.tar.gz\n".encode()

    @contextmanager
    def server(self):
        server = FixtureHTTPServer(self.routes)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.environment["CSF_FIXTURE_HTTP"] = f"http://127.0.0.1:{server.server_port}"
        try:
            yield server
        finally:
            server.shutdown()
            thread.join()
            server.server_close()

    @contextmanager
    def open_url(self, url):
        assert url.startswith(("https://github.com/candacelabs/csf/", "https://raw.githubusercontent.com/candacelabs/csf/"))
        local = self.environment["CSF_FIXTURE_HTTP"] + urllib.parse.urlsplit(url).path
        with urllib.request.urlopen(local) as response:
            effective = "https://github.com" + urllib.parse.urlsplit(response.geturl()).path
            response.geturl = lambda: effective
            yield response

    def run(self, *arguments):
        version = arguments[1] if arguments else None
        errors = io.StringIO()
        try:
            with self.server(), redirect_stderr(errors):
                source = bootstrap.prepare_install(
                    Path(self.environment.get("CSF_RELEASES_DIR", self.releases)), version, self.open_url
                )
        except (bootstrap.BootstrapError, OSError, tarfile.TarError, UnicodeError) as error:
            return subprocess.CompletedProcess(arguments, 1, "", errors.getvalue() + str(error))
        environment = dict(self.environment)
        environment.setdefault("CSF_INSTALL_PATH", str(self.install_path))
        return subprocess.run([str(source / "install.sh")], text=True, capture_output=True, env=environment)


@pytest.fixture
def fixture(tmp_path):
    return BootstrapFixture(tmp_path)


def test_latest_release_installs_and_reuses_verified_source(fixture):
    for arguments in ((), ("--version", VERSION)):
        result = fixture.run(*arguments)
        assert result.returncode == 0, result.stderr
    source = fixture.releases / VERSION / "source"
    assert source.is_dir()
    assert (source / "editor/highlights.scm").read_bytes() == b"(comment) @comment\n"
    assert fixture.install_path.read_text() == "verified fixture CLI\n"
    assert (fixture.home / "invocations").read_text().splitlines() == [str(fixture.install_path)] * 2
    assert not (fixture.releases / ".source-lock").exists()
    assert not list(fixture.releases.glob(".download.*"))


def test_override_install_and_release_paths(fixture):
    fixture.environment["CSF_INSTALL_PATH"] = str(fixture.home / "custom/bin/csf")
    fixture.environment["CSF_RELEASES_DIR"] = str(fixture.home / "custom/releases")
    result = fixture.run("--version", VERSION)
    assert result.returncode == 0, result.stderr
    assert (fixture.home / "custom/bin/csf").is_file()
    assert (fixture.home / "custom/releases" / VERSION / "source/install.sh").is_file()


@pytest.mark.parametrize("version", ["v1.2", "v01.2.3", "v1.2.3/../../evil", "main", "--version"])
def test_invalid_version_is_rejected(fixture, version):
    result = fixture.run("--version", version)
    assert result.returncode != 0
    assert not fixture.releases.exists()


def test_missing_public_release_does_not_fall_back(fixture):
    fixture.routes.clear()
    result = fixture.run()
    assert result.returncode != 0
    assert "No public CSF release" in result.stderr
    assert not fixture.install_path.exists()


def test_checksum_mismatch_is_rejected_before_extraction(fixture):
    fixture.routes[fixture.asset] += b"changed"
    result = fixture.run()
    assert result.returncode != 0
    assert "checksum mismatch" in result.stderr
    assert not (fixture.releases / VERSION).exists()
    assert not fixture.install_path.exists()


@pytest.mark.parametrize("provenance", [b'{}', b'{"source_revision":"abc"}', b'{\n "source_revision": "' + REVISION.encode() + b'",\n "source_revision": "' + REVISION.encode() + b'"\n}'])
def test_invalid_provenance_is_rejected(fixture, provenance):
    fixture.routes[f"/candacelabs/csf/{VERSION}/.candace-export.json"] = provenance
    result = fixture.run()
    assert result.returncode != 0
    assert "provenance" in result.stderr
    assert not fixture.install_path.exists()


@pytest.mark.parametrize("malicious", [
    lambda member: member("../../escaped", b"escape"),
    lambda member: member("evil", kind=tarfile.SYMTYPE, target="../../outside", mode=0o777),
    lambda member: member("hardlink", kind=tarfile.LNKTYPE, target="/etc/passwd"),
    lambda member: member("fifo", kind=tarfile.FIFOTYPE),
    lambda member: member("install.sh", b"duplicate", mode=0o755),
    lambda member: member("directory-link", kind=tarfile.SYMTYPE, target="editor/highlights.scm", mode=0o777),
    lambda member: member("name\nwith-newline", b"bad"),
])
def test_unsafe_archive_members_are_rejected(fixture, malicious):
    fixture.set_archive(archive(malicious))
    result = fixture.run()
    assert result.returncode != 0
    assert "archive" in result.stderr.lower()
    assert not fixture.install_path.exists()
    assert not (fixture.releases / VERSION).exists()
    assert not (fixture.root / "escaped").exists()


@pytest.mark.parametrize("change", ["modified", "added", "release-marker", "symlink"])
def test_changed_existing_release_is_not_overwritten(fixture, change):
    assert fixture.run().returncode == 0
    destination = fixture.releases / VERSION
    if change == "modified":
        (destination / "source/install.sh").write_text("changed\n")
    elif change == "added":
        (destination / "source/unreviewed-config").write_text("changed\n")
    elif change == "release-marker":
        (destination / bootstrap.RECEIPT_NAME).write_text("unrelated\n")
    else:
        shutil.move(destination, fixture.home / "elsewhere")
        destination.symlink_to(fixture.home / "elsewhere", target_is_directory=True)
    before = (fixture.home / "invocations").read_bytes()
    result = fixture.run()
    assert result.returncode != 0
    assert (fixture.home / "invocations").read_bytes() == before


def test_failure_keeps_prior_cli_and_retains_diagnosable_source(fixture):
    fixture.install_path.parent.mkdir(parents=True)
    fixture.install_path.write_text("previous CLI")
    fixture.set_archive(archive(installer=b"#!/bin/sh\nexit 7\n"))
    result = fixture.run()
    assert result.returncode == 7
    assert fixture.install_path.read_text() == "previous CLI"
    assert (fixture.releases / VERSION / "source/install.sh").exists()
    assert not (fixture.releases / ".source-lock").exists()


def test_unrelated_release_directory_is_preserved(fixture):
    destination = fixture.releases / VERSION
    destination.mkdir(parents=True)
    (destination / "keep").write_text("unrelated")
    result = fixture.run()
    assert result.returncode != 0
    assert (destination / "keep").read_text() == "unrelated"
    assert not (destination / "source").exists()


def test_symlink_escape_hidden_by_parent_normalization_is_rejected(fixture):
    def malicious(member):
        member("editor/up", kind=tarfile.SYMTYPE, target="..", mode=0o777)
        member("editor/outside", b"apparently contained")
        member("editor/evil", kind=tarfile.SYMTYPE, target="up/../outside", mode=0o777)
    fixture.set_archive(archive(malicious))
    result = fixture.run()
    assert result.returncode != 0
    assert "traverses a symlink" in result.stderr
    assert not fixture.install_path.exists()


def test_bazel_output_links_are_allowed_but_added_source_is_not(fixture):
    assert fixture.run().returncode == 0
    source = fixture.releases / VERSION / "source"
    (source / "bazel-bin").symlink_to(fixture.root / "external-cache")
    assert fixture.run().returncode == 0
    (source / "bazel-unreviewed-file").write_text("unexpected source")
    assert fixture.run().returncode != 0


def test_https_redirect_cannot_downgrade_to_http():
    request = urllib.request.Request("https://github.com/candacelabs/csf/releases/latest")
    with pytest.raises(bootstrap.BootstrapError, match="downgrade"):
        bootstrap.HTTPSOnlyRedirect().redirect_request(request, None, 302, "redirect", {}, "http://example.invalid/release")


@pytest.mark.parametrize("installer_exit", [0, 42])
def test_minimal_shell_uses_docker_and_executes_retained_installer(tmp_path, installer_exit):
    home = tmp_path / "home"
    releases = home / "data with spaces/releases"
    source = releases / VERSION / "source"
    source.mkdir(parents=True)
    installer = source / "install.sh"
    installer.write_text('#!/bin/sh\ntest -d "$CSF_RELEASES_DIR/.install-lock" || exit 5\nprintf installed > "$HOME/installed"\n' + f'exit {installer_exit}\n')
    installer.chmod(0o755)
    tools = tmp_path / "bin"
    tools.mkdir()
    curl = tools / "curl"
    curl.write_text('#!/bin/sh\nfor last do :; done\nprintf fixture > "$last"\n')
    curl.chmod(0o755)
    docker = tools / "docker"
    docker.write_text(f"#!{sys.executable}\n" + '''import json, os, pathlib, sys
if sys.argv[1] == "info":
    sys.exit(0)
pathlib.Path(os.environ["HOME"], "docker-arguments.json").write_text(json.dumps(sys.argv[1:]))
print(os.environ["FIXTURE_SOURCE"])
''')
    docker.chmod(0o755)
    environment = dict(os.environ, HOME=str(home), CSF_RELEASES_DIR=str(releases),
                       FIXTURE_SOURCE=str(source), PATH=str(tools) + os.pathsep + os.environ["PATH"])
    result = subprocess.run(["sh", "-s", "--", "--version", VERSION], input=BOOTSTRAP.read_text(),
                            text=True, capture_output=True, env=environment)
    assert result.returncode == installer_exit, result.stderr
    assert not (releases / ".install-lock").exists()
    assert (home / "installed").read_text() == "installed"
    arguments = json.loads((home / "docker-arguments.json").read_text())
    assert arguments[arguments.index("--user") + 1] == f"{os.getuid()}:{os.getgid()}"
    assert f"{releases}:{releases}" in arguments
    assert arguments[-2:] == ["--version", VERSION]
    assert any(value.startswith("python:3.12.11-bookworm@sha256:") for value in arguments)
    helper_mount = arguments[arguments.index("--volume", arguments.index("--volume") + 1) + 1]
    assert helper_mount.endswith(":/opt/csf-bootstrap.py:ro")
    assert not Path(helper_mount.split(":")[0]).exists()


@pytest.mark.parametrize("platform,machine", [("Darwin", "x86_64"), ("Linux", "aarch64")])
def test_shell_rejects_unsupported_platform_before_using_docker(tmp_path, platform, machine):
    uname = tmp_path / "uname"
    uname.write_text(f'#!/bin/sh\ncase "$1" in -s) echo {platform};; -m) echo {machine};; esac\n')
    uname.chmod(0o755)
    environment = dict(os.environ, PATH=str(tmp_path) + os.pathsep + os.environ["PATH"])
    result = subprocess.run(["sh", "-s"], input=BOOTSTRAP.read_text(), text=True,
                            capture_output=True, env=environment)
    assert result.returncode != 0
    assert "Linux x86_64" in result.stderr


def test_update_and_rollback_share_the_same_verified_release_mechanism(fixture):
    assert fixture.run().returncode == 0
    newer = "v1.2.4"
    for path, value in list(fixture.routes.items()):
        if VERSION in path:
            fixture.routes[path.replace(VERSION, newer)] = value
    fixture.routes["/candacelabs/csf/releases/latest"] = (302, f"/candacelabs/csf/releases/tag/{newer}")
    assert fixture.run().returncode == 0
    assert (fixture.releases / newer / "source/install.sh").exists()
    assert (fixture.releases / VERSION / "source/install.sh").exists()
    assert fixture.run("--version", VERSION).returncode == 0
