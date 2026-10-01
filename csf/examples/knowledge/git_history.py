"""Bounded Git-object evidence adapter for the existing semantic corpus pipeline."""
import argparse
import base64
from collections import Counter
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import subprocess
import time
from urllib.parse import urlsplit

import semantic_corpus as corpus

DEFAULT_DIFF_PATHS = ('csf', 'app/csf', 'services/copilot-adapter')
OID = re.compile(r'(?:[0-9a-f]{40}|[0-9a-f]{64})\Z')


class OutputLimit(ValueError):
    pass


def git(root, arguments, limit, stdin=None):
    """Bound stdout before materializing a patch; external diff/textconv are disabled."""
    env = {**os.environ, 'GIT_NO_REPLACE_OBJECTS': '1', 'GIT_TERMINAL_PROMPT': '0', 'GIT_OPTIONAL_LOCKS': '0', 'LC_ALL': 'C'}
    with subprocess.Popen(['git', '--no-pager', '-c', 'core.quotePath=true', '-C', str(root), *arguments],
            stdin=subprocess.PIPE if stdin is not None else subprocess.DEVNULL,
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, env=env) as process:
        if stdin is not None:
            process.stdin.write(stdin)
            process.stdin.close()
        data = process.stdout.read(limit + 1)
        if len(data) > limit:
            process.kill()
            process.wait()
            raise OutputLimit('Git output exceeds configured byte bound')
        if process.wait():
            raise ValueError('Git command failed: ' + arguments[0])
    return data


def evidence(out, parents, raw, label, uri=''):
    digest, ref = corpus.daily_papers.blob(out, raw, 'raw')
    parents.append({'source_id': label, 'sha256': digest, 'artifact_ref': ref, 'bytes': len(raw), 'source_uri': uri})
    return digest, ref


def capture_snapshot(root, out, parents):
    now = datetime.now(timezone.utc).isoformat()
    refs_raw = git(root, ['for-each-ref', '--format=%(refname)%00%(objectname)'], 8 * 1024 * 1024)
    refs = []
    for line in refs_raw.splitlines():
        name, oid = line.split(b'\0')
        # Peel captured immutable object IDs, never the moving ref names.
        try:
            commit = git(root, ['rev-parse', '--verify', oid.decode() + '^{commit}'], 128).decode().strip()
        except ValueError:
            commit = None
        refs.append({'name': name.decode('utf-8', 'backslashreplace'), 'object_id': oid.decode(), 'commit': commit})
    head = git(root, ['rev-parse', '--verify', 'HEAD^{commit}'], 128).decode().strip()
    worktrees_raw = git(root, ['worktree', 'list', '--porcelain', '-z'], 8 * 1024 * 1024)
    refs_hash, refs_ref = evidence(out, parents, refs_raw, 'observed-git-refs')
    trees_hash, trees_ref = evidence(out, parents, worktrees_raw, 'observed-git-worktrees')
    snapshot = {'schema_version': 1, 'observed_at': now, 'head': head, 'refs': refs,
        'refs_evidence': {'sha256': refs_hash, 'artifact_ref': refs_ref},
        'worktrees_evidence': {'sha256': trees_hash, 'artifact_ref': trees_ref},
        'boundary': 'Current separately timestamped observations only; neither branch-of-authorship nor original worktree is known.'}
    return snapshot


def parse_changes(raw):
    """Git --name-status -z records; rename/copy records contain two paths."""
    fields = raw.split(b'\0')
    if fields[-1] != b'':
        raise ValueError('unterminated Git change record')
    fields.pop()
    records = []
    cursor = 0
    while cursor < len(fields):
        status = fields[cursor].decode('ascii'); cursor += 1
        count = 2 if status.startswith(('R', 'C')) else 1
        paths = fields[cursor:cursor + count]; cursor += count
        if len(paths) != count or any(not path for path in paths) or not re.fullmatch(r'[ACDMRTUXB][0-9]*', status):
            raise ValueError('malformed Git change record')
        records.append({'status': status, 'paths': [os.fsdecode(path) for path in paths],
                        'paths_base64': [base64.b64encode(path).decode() for path in paths]})
    return records


def binary_numstat(raw):
    fields = raw.split(b'\0')
    if fields[-1] != b'':
        raise ValueError('unterminated Git numstat record')
    fields.pop()
    cursor = 0
    binary = False
    while cursor < len(fields):
        columns = fields[cursor].split(b'\t', 2); cursor += 1
        if len(columns) != 3:
            raise ValueError('malformed Git numstat record')
        binary |= columns[:2] == [b'-', b'-']
        if columns[2] == b'':
            if cursor + 2 > len(fields):
                raise ValueError('missing Git rename paths')
            cursor += 2
    return binary


def positive(value):
    number = int(value)
    if number <= 0:
        raise argparse.ArgumentTypeError('limit must be a positive integer')
    return number


def configure_parser(commands):
    app = commands.add_parser('prepare-history', help='Snapshot refs and prepare broad Git metadata plus bounded selected patches')
    app.add_argument('--source-root', type=Path, default=corpus.ROOT)
    app.add_argument('--out', type=Path, required=True)
    app.add_argument('--repository-url', default='https://github.com/candacelabs/csf')
    app.add_argument('--diff-path', action='append', help='Literal repository-relative file/subtree; repeatable')
    for name, default in [('max-commits', 10000), ('max-object-bytes', 1048576), ('max-change-bytes', 4194304),
                          ('max-metadata-bytes', 134217728), ('max-diff-commits', 100), ('max-diff-paths', 50),
                          ('max-patch-bytes', 65536), ('max-total-patch-bytes', 8388608)]:
        app.add_argument('--' + name, type=positive, default=default)


def prepare_history(args):
    started = time.monotonic()
    limits = {name: getattr(args, name) for name in ('max_commits', 'max_object_bytes', 'max_change_bytes',
        'max_metadata_bytes', 'max_diff_commits', 'max_diff_paths', 'max_patch_bytes', 'max_total_patch_bytes')}
    if any(type(value) is not int or value <= 0 for value in limits.values()):
        raise ValueError('limits must be positive integers')
    paths = args.diff_path if args.diff_path is not None else list(DEFAULT_DIFF_PATHS)
    if not paths or any(not path or path.startswith('/') or '..' in Path(path).parts or path.startswith(':') for path in paths):
        raise ValueError('diff paths must be literal relative files/subtrees')
    url = urlsplit(args.repository_url)
    if url.scheme not in ('http', 'https') or not url.netloc or url.username or url.password or url.query or url.fragment:
        raise ValueError('repository URL must be a credential-free HTTP(S) base')
    root, out = args.source_root.resolve(), args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    parents, documents = [], []
    snapshot_path = out / 'history-snapshot.json'
    if snapshot_path.exists():
        snapshot = json.loads(snapshot_path.read_text())
        for name in ('refs_evidence', 'worktrees_evidence'):
            record = snapshot[name]
            raw = corpus.daily_papers.read_artifact(out, record['artifact_ref'], record['sha256'])
            evidence(out, parents, raw, 'observed-git-' + name.removesuffix('_evidence'))
    else:
        snapshot = capture_snapshot(root, out, parents)
        corpus.daily_papers.write_once(snapshot_path, corpus.daily_papers.encoded(snapshot))
    snapshot_hash, _ = evidence(out, parents, snapshot_path.read_bytes(), 'git-ref-snapshot')
    tips = sorted({snapshot['head'], *(ref['commit'] for ref in snapshot['refs'] if ref['commit'])})
    if any(not isinstance(oid, str) or not OID.fullmatch(oid) for oid in tips):
        raise ValueError('invalid captured commit object ID')
    revisions = git(root, ['rev-list', '--topo-order', '--stdin'], (args.max_commits + 1) * 65,
                    ('\n'.join(tips) + '\n').encode()).decode().splitlines()
    if len(revisions) > args.max_commits:
        raise ValueError('reachable commit count exceeds max-commits; no partial metadata corpus written')
    tokenizer = corpus.load_tokenizer(out)
    now = snapshot['observed_at']
    commits, omissions = [], []
    metadata_bytes = patch_bytes = patches = parent_edges = changed_paths = selected_paths = 0
    selected_commits = set()
    prefix = args.repository_url.rstrip('/')
    for revision in revisions:
        uri = prefix + '/commit/' + revision
        raw = git(root, ['cat-file', 'commit', revision], args.max_object_bytes)
        raw_hash, raw_ref = evidence(out, parents, raw, 'git-commit-object:' + revision, uri)
        metadata_bytes += len(raw)
        headers = raw.partition(b'\n\n')[0].splitlines()
        ancestor_ids = [line[7:].decode('ascii') for line in headers if line.startswith(b'parent ')]
        commit = {'commit': revision, 'parents': ancestor_ids, 'raw_object_sha256': raw_hash, 'raw_object_ref': raw_ref, 'changes': []}
        searchable = ['Git commit ' + revision, 'Commit object (invalid UTF-8 bytes escaped):', raw.decode('utf-8', 'backslashreplace')]
        for parent in ancestor_ids or [None]:
            parent_edges += 1
            comparison = [parent, revision] if parent else ['--root', revision]
            common = ['diff-tree', '-r', '--no-commit-id', '--no-ext-diff', '--no-textconv', '-M', '-l1000', '--ignore-submodules=none']
            changed = git(root, [*common, '--name-status', '-z', *comparison], args.max_change_bytes)
            metadata_bytes += len(changed)
            if metadata_bytes > args.max_metadata_bytes:
                raise ValueError('metadata byte budget exceeded; no partial metadata corpus written')
            change_hash, change_ref = evidence(out, parents, changed, 'git-changes:' + revision + ':' + (parent or 'root'), uri)
            records = parse_changes(changed) if changed else []
            commit['changes'].append({'parent': parent, 'sha256': change_hash, 'artifact_ref': change_ref, 'records': records})
            searchable.append('Changed paths relative to ' + (parent or 'empty tree (root)') + ':\n' + json.dumps(records, ensure_ascii=True))
            selected = [record for record in records if any(path == allow or path.startswith(allow.rstrip('/') + '/') for path in record['paths'] for allow in paths)]
            changed_paths += len(records)
            selected_paths += len(selected)
            if selected and revision not in selected_commits and len(selected_commits) < args.max_diff_commits:
                selected_commits.add(revision)
            for number, record in enumerate(selected):
                item = {'commit': revision, 'parent': parent, **record}
                reason = 'commit_limit' if revision not in selected_commits else 'path_limit' if number >= args.max_diff_paths else 'total_patch_byte_limit' if patch_bytes >= args.max_total_patch_bytes else None
                if reason:
                    omissions.append({**item, 'reason': reason}); continue
                path_args = [':(literal)' + path for path in record['paths']]
                # numstat binary markers are numeric columns, independent of filenames.
                stats = git(root, [*common, '--numstat', '-z', *comparison, '--', *path_args], args.max_change_bytes)
                if binary_numstat(stats):
                    omissions.append({**item, 'reason': 'binary_patch'}); continue
                remaining = args.max_total_patch_bytes - patch_bytes
                try:
                    patch = git(root, [*common, '-p', '--full-index', '--no-color', '--src-prefix=a/', '--dst-prefix=b/', '--unified=3', '--diff-algorithm=myers', '--no-indent-heuristic', '--no-relative', '--submodule=short', *comparison, '--', *path_args], min(args.max_patch_bytes, remaining))
                except OutputLimit:
                    omissions.append({**item, 'reason': 'patch_byte_limit' if args.max_patch_bytes <= remaining else 'total_patch_byte_limit'}); continue
                patch_hash, patch_ref = evidence(out, parents, patch, 'git-patch:' + revision + ':' + (parent or 'root'), uri)
                patch_bytes += len(patch)
                try:
                    patch.decode('utf-8')
                except UnicodeDecodeError:
                    omissions.append({**item, 'reason': 'non_utf8_patch', 'sha256': patch_hash, 'artifact_ref': patch_ref}); continue
                key = corpus.daily_papers.digest(corpus.daily_papers.encoded({'status': record['status'], 'paths_base64': record['paths_base64']}))[:24]
                identity = 'git-history:patch:' + revision + ':' + (parent or 'root') + ':' + key
                patch_uri = uri + '#diff-' + corpus.daily_papers.digest(base64.b64decode(record['paths_base64'][-1]))
                corpus.add_source(out, parents, documents, tokenizer, now, identity, patch, revision, patch_uri,
                    'Git parent-relative patch ' + revision + ' from ' + (parent or 'root'), raw_hash=patch_hash)
                item.update({'sha256': patch_hash, 'artifact_ref': patch_ref, 'bytes': len(patch)})
                commit.setdefault('patches', []).append(item)
                patches += 1
        corpus.add_source(out, parents, documents, tokenizer, now, 'git-history:commit:' + revision,
            '\n\n'.join(searchable).encode(), revision, uri, 'Git commit metadata ' + revision, raw_hash=raw_hash)
        commits.append(commit)
    coverage = {'reachable_commits': len(revisions), 'metadata_commits': len(commits), 'parent_comparisons_including_roots': parent_edges,
        'metadata_raw_bytes': metadata_bytes, 'changed_path_records': changed_paths, 'selected_patch_path_records': selected_paths, 'outside_patch_path_selection': changed_paths - selected_paths, 'diff_commits_selected': len(selected_commits), 'patches_indexable': patches,
        'patch_bytes_retained': patch_bytes, 'omissions_by_reason': dict(Counter(item['reason'] for item in omissions)),
        'patch_path_selection': paths, 'limits': limits, 'documents': len(documents)}
    manifest = {'schema_version': 1, 'revision': snapshot['head'], 'prepared_at': now, 'history_snapshot_sha256': snapshot_hash,
        'tokenizer': {'url': corpus.TOKENIZER_URL, 'sha256': corpus.TOKENIZER_SHA256, 'maximum_tokens_including_special': 256},
        'parents': parents, 'documents': documents, 'commits': commits, 'patch_omissions': omissions, 'coverage': coverage,
        'boundary': 'All commits reachable from captured HEAD and refs; no reflog-only/unreachable objects. Metadata and parent-relative paths broad; patches bounded and selected. No branch-of-authorship or original-worktree inference.'}
    (out / 'corpus.json').write_text(json.dumps(manifest, indent=2) + '\n')
    receipt = {'schema_version': 1, 'snapshot_sha256': snapshot_hash, 'coverage': coverage,
        'duration_seconds': time.monotonic() - started, 'corpus_sha256': corpus.daily_papers.digest((out / 'corpus.json').read_bytes()),
        'status': 'prepared_not_ingested', 'git_version': git(root, ['--version'], 256).decode().strip()}
    (out / 'preparation-receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
    print(json.dumps(receipt))
