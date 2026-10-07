#!/usr/bin/env python3
"""Tests for operator identifier patterns.

The positive fixtures below are deliberate matches for the patterns under test.
The standing gate scans this file like every other tracked file, so each
positive fixture is joined from its parts by a helper instead of being written
out: the assertions are unchanged, and no literal match is committed.
"""

import sys
from pathlib import Path

# Add tools directory to path
sys.path.insert(0, str(Path(__file__).parent.parent))

from check_operator_identifiers import compiled_patterns, scan_bytes


def home_path(account: str) -> bytes:
    """A Linux home-directory path for ``account``, joined from its segments."""
    return "/".join(("", "home", account, "")).encode()


def shorthand_reference(owner: str, repository: str, number: int) -> bytes:
    """An issue or pull-request reference in the owner/repository#N form."""
    return f"{owner}/{repository}#{number}".encode()


def url_reference(owner: str, repository: str, kind: str, number: int) -> bytes:
    """An issue or pull-request reference in its github.com URL form."""
    return "/".join(("github.com", owner, repository, kind, str(number))).encode()


def test_linux_home_path():
    """Linux home-directory path pattern matches account names."""
    pattern = compiled_patterns()[4]  # Linux home path is at index 4
    description, regex = pattern

    assert description == "Linux home-directory path naming an account"

    # Should match
    assert regex.search(home_path("username"))
    assert regex.search(home_path("dev"))
    assert regex.search(home_path("user-name"))
    assert regex.search(home_path("user_name"))
    assert regex.search(home_path("username").upper())  # case-insensitive

    # Should not match
    assert not regex.search(b"/home/")  # no account name
    assert not regex.search(b"/home")  # no trailing slash
    assert not regex.search(b"~/username/")  # tilde syntax


def test_non_candacelabs_reference():
    """Non-candacelabs issue/PR reference pattern."""
    pattern = compiled_patterns()[5]  # Non-candacelabs reference is at index 5
    description, regex = pattern

    assert description == "non-candacelabs issue or pull-request reference"

    # Should match shorthand format
    assert regex.search(shorthand_reference("acme", "app-server", 385))
    assert regex.search(shorthand_reference("acme-labs", "pg-mem", 123))
    assert regex.search(shorthand_reference("owner-with-dash", "repo-with-dash", 456))
    assert regex.search(shorthand_reference("example", "project", 1))

    # Should match URL format
    assert regex.search(url_reference("example", "project", "issues", 123))
    assert regex.search(url_reference("acme", "app-server", "pull", 385))
    assert regex.search(url_reference("owner-with-dash", "repo-with-dash", "issues", 789))

    # Should NOT match candacelabs references
    assert not regex.search(b"candacelabs/csf#123")
    assert not regex.search(b"github.com/candacelabs/csf/issues/456")
    assert not regex.search(b"github.com/candacelabs/something/pull/789")

    # Should NOT match incomplete references
    assert not regex.search(b"owner/repo")  # no issue number
    assert not regex.search(b"github.com/owner/repo")  # no issue path


def test_fixtures():
    """Test fixture blocks (positive examples for each pattern)."""
    patterns = compiled_patterns()

    # Linux home-directory fixture
    linux_home_findings = scan_bytes(
        b"docker run -v " + home_path("devuser") + b"project:/app",
        "test_linux_home.txt",
        [patterns[4]]
    )
    assert len(linux_home_findings) == 1
    assert "Linux home-directory" in linux_home_findings[0].description

    # Non-candacelabs issue/PR fixture
    non_org_findings = scan_bytes(
        b"Related: " + shorthand_reference("acme", "app-server", 385),
        "test_non_org.txt",
        [patterns[5]]
    )
    assert len(non_org_findings) == 1
    assert "non-candacelabs issue" in non_org_findings[0].description


if __name__ == "__main__":
    test_linux_home_path()
    print("✓ test_linux_home_path passed")
    test_non_candacelabs_reference()
    print("✓ test_non_candacelabs_reference passed")
    test_fixtures()
    print("✓ test_fixtures passed")
    print("All tests passed!")
