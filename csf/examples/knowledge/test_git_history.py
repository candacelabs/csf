"""Real Git fixtures exercise immutable graph provenance and exact-byte evidence."""
import argparse
from collections import defaultdict
import hashlib
import json
import os
from pathlib import Path
import subprocess
from unittest import mock

import pytest
from tokenizers import Tokenizer, models, pre_tokenizers

import git_history as history
import semantic_corpus as corpus


def run(root, *args):
    return subprocess.check_output(['git', '-C', str(root), *args], stderr=subprocess.DEVNULL)


def commit(root, message):
    run(root, 'add', '-A')
    run(root, 'commit', '-m', message)
    return run(root, 'rev-parse', 'HEAD').decode().strip()


@pytest.fixture
def repository(tmp_path):
    root = tmp_path / 'repo'; root.mkdir()
    run(root, 'init', '-b', 'main')
    run(root, 'config', 'user.name', 'Fixture Author')
    run(root, 'config', 'user.email', 'fixture@example.invalid')
    (root / 'src').mkdir()
    (root / 'src/original.txt').write_text('controller holds last safe command\n' * 10)
    (root / 'src/odd\tline\nname.txt').write_text('unusual path\n')
    (root / 'src/binary.dat').write_bytes(b'\x00\x01binary')
    raw_path = os.fsencode(root / 'src') + b'/nonutf8-\xff.txt'
    with open(raw_path, 'wb') as stream:
        stream.write(b'raw filename\n')
    initial = commit(root, 'Root metadata\n\nA full multiline message.')
    run(root, 'checkout', '-b', 'side')
    (root / 'src/side.txt').write_text('side branch contribution\n')
    side = commit(root, 'Side contribution')
    run(root, 'checkout', 'main')
    run(root, 'mv', 'src/original.txt', 'src/renamed.txt')
    renamed = commit(root, 'Rename preserves content')
    run(root, 'merge', '--no-ff', 'side', '-m', 'Merge both parent histories')
    merged = run(root, 'rev-parse', 'HEAD').decode().strip()
    (root / 'src/renamed.txt').unlink()
    deleted = commit(root, 'Delete renamed file')
    run(root, 'tag', '-a', 'fixture-tag', '-m', 'Annotated tag', initial)
    return root, {'initial': initial, 'side': side, 'renamed': renamed, 'merged': merged, 'deleted': deleted}


def arguments(root, out, **overrides):
    values = dict(source_root=root, out=out, repository_url='https://example.invalid/project', diff_path=['src'],
        max_commits=100, max_object_bytes=1048576, max_change_bytes=4194304, max_metadata_bytes=134217728,
        max_diff_commits=100, max_diff_paths=50, max_patch_bytes=65536, max_total_patch_bytes=8388608)
    values.update(overrides)
    return argparse.Namespace(**values)


def prepare(args):
    tokenizer = Tokenizer(models.WordLevel({'[UNK]': 0}, unk_token='[UNK]'))
    tokenizer.pre_tokenizer = pre_tokenizers.Whitespace()
    with mock.patch.object(corpus, 'load_tokenizer', return_value=tokenizer):
        history.prepare_history(args)
    return json.loads((args.out / 'corpus.json').read_text())


def test_real_graph_root_merge_rename_delete_unusual_paths_and_exact_evidence(repository, tmp_path):
    root, ids = repository
    args = arguments(root, tmp_path / 'out')
    manifest = prepare(args)
    commits = {row['commit']: row for row in manifest['commits']}
    assert set(commits) == set(ids.values())
    assert commits[ids['initial']]['parents'] == []
    assert len(commits[ids['merged']]['changes']) == 2
    assert len(commits[ids['merged']]['parents']) == 2
    assert commits[ids['renamed']]['changes'][0]['records'][0]['status'] == 'R100'
    assert commits[ids['deleted']]['changes'][0]['records'][0]['status'] == 'D'
    changes = commits[ids['initial']]['changes'][0]['records']
    paths = [path for row in changes for path in row['paths']]
    assert 'src/odd\tline\nname.txt' in paths
    assert 'src/nonutf8-\udcff.txt' in paths
    assert manifest['coverage']['omissions_by_reason']['binary_patch'] == 1
    assert manifest['coverage']['metadata_commits'] == 5
    assert manifest['coverage']['parent_comparisons_including_roots'] == 6
    for item in manifest['parents']:
        corpus.daily_papers.read_artifact(args.out, item['artifact_ref'], item['sha256'], item['bytes'])
    for revision, row in commits.items():
        raw = corpus.daily_papers.read_artifact(args.out, row['raw_object_ref'], row['raw_object_sha256'])
        assert raw == run(root, 'cat-file', 'commit', revision)
    by_parent = defaultdict(list)
    for row in manifest['documents']:
        by_parent[(row['parent_content_hash'], row['request']['document']['source_id'].rsplit('#chunk-', 1)[0])].append(row)
    for (digest, _), rows in by_parent.items():
        rows.sort(key=lambda row: row['character_offset'])
        reconstructed = ''.join(row['request']['text'] for row in rows).encode()
        assert hashlib.sha256(reconstructed).hexdigest() == digest
    assert 'branch_of_authorship' not in json.dumps(manifest)
    assert 'original_worktree' not in json.dumps(manifest)
    snapshot = json.loads((args.out / 'history-snapshot.json').read_text())
    assert snapshot['observed_at']
    assert snapshot['worktrees_evidence']['sha256']
    assert any(ref['name'] == 'refs/tags/fixture-tag' and ref['commit'] == ids['initial'] for ref in snapshot['refs'])


def test_rerun_uses_captured_tips_and_stable_document_identities(repository, tmp_path):
    root, _ = repository
    args = arguments(root, tmp_path / 'out')
    first = prepare(args)
    (root / 'src/new.txt').write_text('not in captured refs')
    new = commit(root, 'Later commit excluded by pinned snapshot')
    second = prepare(args)
    assert new not in {row['commit'] for row in second['commits']}
    assert first['documents'] == second['documents']
    assert first['coverage'] == second['coverage']
    assert first['history_snapshot_sha256'] == second['history_snapshot_sha256']
    assert first == second


def test_patch_bounds_report_omissions_without_narrowing_metadata(repository, tmp_path):
    root, _ = repository
    manifest = prepare(arguments(root, tmp_path / 'out', max_diff_commits=1, max_diff_paths=1, max_patch_bytes=1))
    assert manifest['coverage']['metadata_commits'] == 5
    assert manifest['coverage']['patches_indexable'] == 0
    assert manifest['coverage']['omissions_by_reason']['patch_byte_limit'] >= 1
    assert manifest['coverage']['omissions_by_reason']['commit_limit'] >= 1


@pytest.mark.parametrize('field', ['max_commits', 'max_diff_commits', 'max_patch_bytes', 'max_metadata_bytes'])
@pytest.mark.parametrize('value', [0, -1, True, '3'])
def test_malformed_limits_rejected_before_git(tmp_path, field, value):
    with mock.patch.object(history, 'git') as git:
        with pytest.raises(ValueError, match='positive integers'):
            history.prepare_history(arguments(tmp_path, tmp_path / 'out', **{field: value}))
    git.assert_not_called()


def test_commit_cap_fails_without_partial_metadata_corpus(repository, tmp_path):
    root, _ = repository
    args = arguments(root, tmp_path / 'out', max_commits=2)
    with pytest.raises(ValueError):
        prepare(args)
    assert not (args.out / 'corpus.json').exists()


def test_binary_numstat_and_path_grammar_are_unambiguous():
    assert history.binary_numstat(b'-\t-\t\x00old\tname\x00new\nname\x00')
    assert not history.binary_numstat(b'1\t2\tfile-\t-\tname\x00')
    with pytest.raises(ValueError):
        history.parse_changes(b'R100\x00missing-new\x00')
    with pytest.raises(ValueError):
        history.parse_changes(b'M\x00unterminated')
