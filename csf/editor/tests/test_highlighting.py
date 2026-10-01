"""Exercise the shipped parser and query at their Python consumer boundary."""
from pathlib import Path

import pytest
from tree_sitter import Language, Parser, Query, QueryCursor
import tree_sitter_csf

LANGUAGE = Language(tree_sitter_csf.language())
QUERY = Query(LANGUAGE, tree_sitter_csf.HIGHLIGHTS_QUERY)
CSF_ROOT = Path(__file__).resolve().parents[2]


def parse(source):
    return Parser(LANGUAGE).parse(source.encode()).root_node


def captures(source):
    encoded = source.encode()
    tree = Parser(LANGUAGE).parse(encoded)
    return {
        name: [encoded[n.start_byte:n.end_byte].decode() for n in nodes]
        for name, nodes in QueryCursor(QUERY).captures(tree.root_node).items()
    }


def test_real_architecture_and_all_declarations():
    source = (CSF_ROOT / "architecture/architecture.csf").read_text()
    assert not parse(source).has_error
    source = '''architecture demo version 1 {
      process host kind go entrypoint "app/cmd";
      process peer kind external;
      scope root under host;
      service worker in host scope root source "services/worker" state existing lifecycle scoped verification test "test";
      manager boss in host scope root state planned lifecycle borrowed verification pending;
      library lib in host scope root state planned lifecycle borrowed verification pending;
      adapter net in host scope root state planned lifecycle scoped verification pending;
      gateway proc in host scope root state planned lifecycle scoped verification pending;
      resource db in host scope root state planned lifecycle scoped verification pending;
      requires worker->lib;
      connect worker -> lib via call state planned;
      connect worker -> lib via channel state planned;
      connect worker -> peer via subprocess boundary proc state planned;
      connect worker -> peer via remote boundary net state planned;
      connect worker -> peer via device boundary net state planned;
      scan "services";
      generated "generated";
    }'''
    assert not parse(source).has_error


@pytest.mark.parametrize("name", ["worker", "_worker", "worker-one", "worker-", "worker--", "process_worker", "process-worker"])
def test_identifiers_and_unspaced_arrows(name):
    source = f"architecture demo version 1 {{ requires {name}->provider; }}"
    assert not parse(source).has_error
    found = captures(source)
    assert name in found["variable"]
    assert "->" in found["operator"]


@pytest.mark.parametrize("source", [
    "architecturedemo version 1 {}",
    "architecture demo version1 {}",
    "architecture process version 1 {}",
    "architecture demo version 1 { process service kind go; }",
    "architecture demo version 1 { scan 'not-json'; }",
    'architecture demo version 1 { scan "bad\\q"; }',
    'architecture demo version 1 { scan "raw\nnewline"; }',
])
def test_lexical_errors(source):
    assert parse(source).has_error


def test_unicode_strings_escapes_and_keyword_isolation():
    source = r'''architecture demo version 1 {
      // service worker in host scope root
      scan "service <tag> café 😀 \"quoted\" \\path \n \u03bb";
    }'''
    assert not parse(source).has_error
    found = captures(source)
    assert "service" not in found["keyword"]
    assert found["comment"] == ["// service worker in host scope root"]
    assert "café 😀" in found["string"][0]
    assert found["keyword"].count("scan") == 1


def test_recovery_retains_highlighting_while_typing():
    source = 'architecture demo version 1 { process host kind go; scan "unfinished'
    assert parse(source).has_error
    found = captures(source)
    assert "process" in found["keyword"]
    assert "host" in found["variable"]
    assert "1" in found["number"]


def test_incremental_edit_reuses_tree():
    parser = Parser(LANGUAGE)
    old = b'architecture demo version 1 { scan "hello"; }'
    tree = parser.parse(old)
    start = old.index(b"hello")
    new = old[:start] + b"world!" + old[start + 5:]
    tree.edit(start, start + 5, start + 6, (0, start), (0, start + 5), (0, start + 6))
    updated = parser.parse(new, tree)
    assert not updated.root_node.has_error
    found = QueryCursor(QUERY).captures(updated.root_node)
    node = found["string"][0]
    assert new[node.start_byte:node.end_byte] == b'"world!"'
