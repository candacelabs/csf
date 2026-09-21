"""Validate exact-byte chunking and fail-before-dispatch provenance checks."""
import json
from pathlib import Path
import subprocess
from types import SimpleNamespace
from unittest import mock

import pytest
from tokenizers import Tokenizer, models, normalizers, pre_tokenizers, processors

import semantic_corpus as corpus


def test_prepare_reads_public_code_paths_and_consumer_lithe_receipt(tmp_path, monkeypatch):
    source = tmp_path / "source"
    source.mkdir()
    for path in corpus.CODE_PATHS:
        destination = source / path
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text("public source fixture\n")
    subprocess.run(["git", "init", "--quiet", str(source)], check=True)
    subprocess.run(["git", "-C", str(source), "add", "."], check=True)
    subprocess.run(["git", "-C", str(source), "-c", "user.name=Test",
                    "-c", "user.email=test@example.invalid", "commit", "--quiet", "-m", "fixture"], check=True)
    revision = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
    lithe = tmp_path / "lithe"
    lithe.mkdir()
    text, html = b"paper text fixture\n", b"<p>paper text fixture</p>\n"
    (lithe / "2603.07442v1.txt").write_bytes(text)
    (lithe / "2603.07442v1.html").write_bytes(html)
    receipt = {"text_sha256": corpus.daily_papers.digest(text),
               "html_sha256": corpus.daily_papers.digest(html), "source": "https://example.invalid/paper"}
    (lithe / "lithe-index.json").write_text(json.dumps(receipt))
    tokenizer = Tokenizer(models.WordLevel(vocab={"[UNK]": 0}, unk_token="[UNK]"))
    monkeypatch.setattr(corpus, "load_tokenizer", lambda out: tokenizer)
    output = tmp_path / "corpus"
    corpus.prepare(SimpleNamespace(source_root=source, revision=revision, lithe_dir=lithe, out=output))
    manifest = json.loads((output / "corpus.json").read_text())
    assert manifest["revision"] == revision
    assert len(manifest["parents"]) == len(corpus.CODE_PATHS) + 2
    for parent in manifest["parents"]:
        assert corpus.daily_papers.read_artifact(output, parent["artifact_ref"], parent["sha256"], parent["bytes"])
    (lithe / "2603.07442v1.html").write_bytes(b"changed after receipt")
    with pytest.raises(ValueError, match="LITHE parent evidence hash mismatch"):
        corpus.prepare(SimpleNamespace(source_root=source, revision=revision, lithe_dir=lithe, out=tmp_path / "rejected"))
    assert not (tmp_path / "rejected/corpus.json").exists()


def test_token_bound_and_exact_unicode_source_reconstruction():
    tokenizer = Tokenizer(models.WordPiece(vocab={'[UNK]': 0, '[CLS]': 1, '[SEP]': 2, 'hello': 3, 'world': 4, '!': 5}))
    tokenizer.normalizer = normalizers.BertNormalizer(lowercase=True)
    tokenizer.pre_tokenizer = pre_tokenizers.BertPreTokenizer()
    tokenizer.post_processor = processors.TemplateProcessing(single='[CLS] $A [SEP]', special_tokens=[('[CLS]', 1), ('[SEP]', 2)])
    text = ('  Héllo world!\n雪 and source-code();\n' * 100) + '\n'
    chunks = list(corpus.chunks(text, tokenizer))
    assert ''.join(chunk for _, chunk, _ in chunks) == text
    assert len(chunks) > 1
    assert all(tokens <= 242 for _, _, tokens in chunks)
    assert all(text[offset:offset + len(chunk)] == chunk for offset, chunk, _ in chunks)


def test_corrupt_parent_rejected_before_any_binary_dispatch(tmp_path):
    raw = b'original source'
    digest, ref = corpus.daily_papers.blob(tmp_path, raw, 'txt')
    (tmp_path / ref).write_bytes(b'tampered source')
    manifest = {'parents': [{'artifact_ref': ref, 'sha256': digest, 'bytes': len(raw)}], 'documents': []}
    path = tmp_path / 'corpus.json'; path.write_text(json.dumps(manifest))
    with mock.patch.object(corpus.subprocess, 'run') as run:
        with pytest.raises(ValueError, match='content mismatch'):
            corpus.ingest(SimpleNamespace(corpus=path, binary=Path('/unused'), endpoint='http://unused'))
    run.assert_not_called()


def test_chunk_request_cannot_disagree_with_hashed_artifact(tmp_path):
    raw = b'original source'
    digest, ref = corpus.daily_papers.blob(tmp_path, raw, 'txt')
    manifest = {'parents': [], 'documents': [{'artifact_ref': ref, 'tokens_with_special': 4,
        'request': {'document': {'source_id': 'example', 'content_hash': digest, 'revision': 'one'}, 'text': 'changed'}}]}
    path = tmp_path / 'corpus.json'; path.write_text(json.dumps(manifest))
    with mock.patch.object(corpus.subprocess, 'run') as run:
        with pytest.raises(ValueError, match='request/evidence mismatch'):
            corpus.ingest(SimpleNamespace(corpus=path, binary=Path('/unused'), endpoint='http://unused'))
    run.assert_not_called()


@pytest.mark.parametrize('wrapped', [False, True])
def test_projection_failure_is_not_counted_as_indexed(tmp_path, wrapped):
    raw = b'original source'
    digest, ref = corpus.daily_papers.blob(tmp_path, raw, 'txt')
    manifest = {'parents': [], 'documents': [{'artifact_ref': ref, 'tokens_with_special': 4,
        'request': {'document': {'source_id': 'example', 'content_hash': digest, 'revision': 'one'}, 'text': raw.decode()}}]}
    path = tmp_path / 'corpus.json'; path.write_text(json.dumps(manifest))
    response = {'indexed': False, 'projectionError': 'offline'}
    if wrapped:
        response = {'result': response}
    with mock.patch.object(corpus.subprocess, 'run', return_value=SimpleNamespace(returncode=0, stdout=json.dumps(response), stderr='')):
        with pytest.raises(RuntimeError, match='ingestion failed'):
            corpus.ingest(SimpleNamespace(corpus=path, binary=Path('/unused'), endpoint='http://unused'))
    assert json.loads((tmp_path / 'ingestion.json').read_text())['results'][0]['response'] == response


def parent_and_chunk(tmp_path):
    raw = b'original source with citation'
    digest, ref = corpus.daily_papers.blob(tmp_path, raw, 'txt')
    chunk = b'with citation'
    chunk_hash, chunk_ref = corpus.daily_papers.blob(tmp_path, chunk, 'txt')
    manifest = {'prepared_at': '2026-09-16T00:00:00Z',
        'parents': [{'source_id': 'source', 'revision': 'one', 'source_uri': 'https://example.invalid/source',
            'sha256': digest, 'artifact_ref': ref, 'bytes': len(raw)}],
        'documents': [{'artifact_ref': chunk_ref, 'tokens_with_special': 4,
            'request': {'document': {'source_id': 'source#chunk-0', 'revision': 'one',
                'content_hash': chunk_hash, 'raw_source_content_hash': digest}, 'text': chunk.decode()}}]}
    path = tmp_path / 'corpus.json'; path.write_text(json.dumps(manifest))
    return path, manifest


def test_parent_registration_precedes_chunk_even_when_parent_projection_fails(tmp_path):
    path, manifest = parent_and_chunk(tmp_path)
    registered = set()
    def dispatch(argv, *, input, **kwargs):
        request = json.loads(input)
        document = request['document']
        assert not document.get('rawSourceContentHash') or document['rawSourceContentHash'] in registered
        registered.add(document['contentHash'])
        indexed = document['sourceId'].endswith('#chunk-0')
        return SimpleNamespace(returncode=0, stdout=json.dumps({'result': {'document': document,
            'indexed': indexed, 'projectionError': '' if indexed else 'parent too long'}}), stderr='')
    with mock.patch.object(corpus.subprocess, 'run', side_effect=dispatch) as run:
        corpus.ingest(SimpleNamespace(corpus=path, binary=Path('/unused'), endpoint='http://unused'))
    assert run.call_count == 2
    results = json.loads((tmp_path / 'ingestion.json').read_text())['results']
    assert [item['kind'] for item in results] == ['parent', 'chunk']
    assert results[0]['response']['result']['indexed'] is False
    assert results[1]['response']['result']['indexed'] is True


def test_queued_parent_and_chunk_are_accepted_with_truthful_counts(tmp_path):
    path, _ = parent_and_chunk(tmp_path)
    def dispatch(argv, *, input, **kwargs):
        request = json.loads(input)
        document = request['document']
        return SimpleNamespace(returncode=0, stdout=json.dumps({'result': {
            'document': document, 'indexed': False, 'queued': True, 'projectionError': ''}}), stderr='')
    with mock.patch.object(corpus.subprocess, 'run', side_effect=dispatch):
        corpus.ingest(SimpleNamespace(corpus=path, binary=Path('/unused'), endpoint='http://unused'))
    receipt = json.loads((tmp_path / 'ingestion.json').read_text())
    assert receipt['summary'] == {'accepted': 2, 'queued': 2, 'indexed': 0, 'parents': 1, 'chunks': 1}
    assert all(item['accepted'] and item['queued'] and not item['indexed'] for item in receipt['results'])


def test_missing_parent_fails_before_any_dispatch(tmp_path):
    path, manifest = parent_and_chunk(tmp_path)
    manifest['parents'] = []
    path.write_text(json.dumps(manifest))
    with mock.patch.object(corpus.subprocess, 'run') as run:
        with pytest.raises(ValueError, match='unregistered parent'):
            corpus.ingest(SimpleNamespace(corpus=path, binary=Path('/unused'), endpoint='http://unused'))
    run.assert_not_called()


def test_corrupt_child_cannot_partially_register_parents(tmp_path):
    path, manifest = parent_and_chunk(tmp_path)
    (tmp_path / manifest['documents'][0]['artifact_ref']).write_bytes(b'corrupt')
    with mock.patch.object(corpus.subprocess, 'run') as run:
        with pytest.raises(ValueError, match='content mismatch'):
            corpus.ingest(SimpleNamespace(corpus=path, binary=Path('/unused'), endpoint='http://unused'))
    run.assert_not_called()
