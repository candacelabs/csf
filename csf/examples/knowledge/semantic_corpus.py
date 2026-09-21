#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["protobuf==7.35.1", "tokenizers==0.22.0"]
# ///
"""Prepare immutable public/source chunks; ingest via the existing generated Go CLI."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import subprocess
import sys
from urllib.request import urlopen

import daily_papers

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "csf/tools/codegen/generated/python"))
from candace.brainspine.v1.brainspine_pb2 import IngestDocumentRequest, SourceDocument
from google.protobuf.json_format import MessageToDict, MessageToJson, ParseDict

TOKENIZER_URL = "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/1110a243fdf4706b3f48f1d95db1a4f5529b4d41/tokenizer.json"
TOKENIZER_SHA256 = "be50c3628f2bf5bb5e3a7f17b1f74611b2561a3a27eeab05e5aa30f411572037"
CODE_PATHS = (
    "csf/runtime.go", "csf/compiler.go",
    "csf/knowledge.go", "csf/opensearch.go",
    "csf/internal/brainspinedb/schema.sql",
)


def chunks(text, tokenizer, limit=240):
    """Split exact text at tokenizer offsets; never truncate or normalize source bytes."""
    remaining = text
    offset = 0
    while remaining:
        encoded = tokenizer.encode(remaining, add_special_tokens=False)
        end = encoded.offsets[limit][0] if len(encoded.ids) > limit else len(remaining)
        if end <= 0:
            raise ValueError("tokenizer did not advance")
        chunk = remaining[:end]
        count = len(tokenizer.encode(chunk, add_special_tokens=True).ids)
        if count > 256:
            raise ValueError("chunk exceeds MiniLM token bound")
        yield offset, chunk, count
        offset += end
        remaining = remaining[end:]


def load_tokenizer(out):
    from tokenizers import Tokenizer
    out.mkdir(parents=True, exist_ok=True)
    tokenizer_path = out / "tokenizer.json"
    if not tokenizer_path.exists():
        with urlopen(TOKENIZER_URL, timeout=30) as response:
            data = response.read(2 * 1024 * 1024)
        if daily_papers.digest(data) != TOKENIZER_SHA256:
            raise ValueError("pinned tokenizer hash mismatch")
        daily_papers.write_once(tokenizer_path, data)
    if daily_papers.digest(tokenizer_path.read_bytes()) != TOKENIZER_SHA256:
        raise ValueError("tokenizer hash mismatch")
    tokenizer = Tokenizer.from_file(str(tokenizer_path))
    tokenizer.no_truncation()
    return tokenizer


def add_source(out, parents, documents, tokenizer, now, identity, raw, source_revision, uri, title, raw_hash=None):
    parent_hash, parent_ref = daily_papers.blob(out, raw, "txt")
    parents.append({"source_id": identity, "revision": source_revision, "sha256": parent_hash, "artifact_ref": parent_ref, "bytes": len(raw), "source_uri": uri})
    text = raw.decode("utf-8")
    for number, (offset, chunk, tokens) in enumerate(chunks(text, tokenizer)):
        data = chunk.encode("utf-8")
        digest, ref = daily_papers.blob(out, data, "txt")
        line = text[:offset].count("\n") + 1
        document = SourceDocument(source_id=identity + "#chunk-" + str(number), revision=source_revision,
            content_hash=digest, source_uri=uri, title=title + f" (chunk {number}, line {line})",
            media_type="text/plain; charset=utf-8", license="unknown", retrieved_at=now,
            raw_source_content_hash=raw_hash or parent_hash, size_bytes=len(data))
        request = IngestDocumentRequest(document=document, text=chunk)
        documents.append({"request": MessageToDict(request, preserving_proto_field_name=True), "artifact_ref": ref,
            "parent_content_hash": parent_hash, "character_offset": offset, "tokens_with_special": tokens})


def prepare(args):
    out = args.out.resolve()
    tokenizer = load_tokenizer(out)
    revision = subprocess.check_output(["git", "-C", str(args.source_root), "rev-parse", args.revision + "^{commit}"], text=True).strip()
    now = datetime.now(timezone.utc).isoformat()
    parents, documents = [], []

    def source(*values, **options):
        add_source(out, parents, documents, tokenizer, now, *values, **options)

    for path in CODE_PATHS:
        raw = subprocess.check_output(["git", "-C", str(args.source_root), "show", revision + ":" + path])
        source("git:" + path, raw, revision, "https://github.com/candacelabs/csf/blob/" + revision + "/" + path, path)
    receipt = json.loads((args.lithe_dir / "lithe-index.json").read_text())
    raw = (args.lithe_dir / "2603.07442v1.txt").read_bytes()
    html = (args.lithe_dir / "2603.07442v1.html").read_bytes()
    if daily_papers.digest(raw) != receipt["text_sha256"] or daily_papers.digest(html) != receipt["html_sha256"]:
        raise ValueError("LITHE parent evidence hash mismatch")
    html_hash, html_ref = daily_papers.blob(out, html, "html")
    parents.append({"source_id": "arxiv:2603.07442v1:html", "revision": "2603.07442v1", "sha256": html_hash, "artifact_ref": html_ref, "bytes": len(html), "source_uri": receipt["source"]})
    source("arxiv:2603.07442v1", raw, "2603.07442v1", receipt["source"], "LITHE paper", raw_hash=html_hash)
    manifest = {"schema_version": 1, "revision": revision, "prepared_at": now,
        "tokenizer": {"url": TOKENIZER_URL, "sha256": TOKENIZER_SHA256, "maximum_tokens_including_special": 256},
        "parents": parents, "documents": documents, "boundary": "Explicit tracked code allowlist and hash-verified paper bytes only; no private sessions or credentials."}
    (out / "corpus.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(json.dumps({"corpus": str(out / "corpus.json"), "documents": len(documents), "parents": len(parents), "maximum_tokens": max(d["tokens_with_special"] for d in documents)}))


def ingest(args):
    manifest_path = args.corpus.resolve()
    manifest = json.loads(manifest_path.read_text())
    root = manifest_path.parent
    parents = []
    for parent in manifest["parents"]:
        raw = daily_papers.read_artifact(root, parent["artifact_ref"], parent["sha256"], parent["bytes"])
        parents.append((parent, raw))
    # Validate every input before making the first mutation.
    requests = []
    for row in manifest["documents"]:
        request = ParseDict(row["request"], IngestDocumentRequest())
        raw = daily_papers.read_artifact(root, row["artifact_ref"], request.document.content_hash)
        if raw != request.text.encode("utf-8") or not 0 < row["tokens_with_special"] <= 256:
            raise ValueError("chunk request/evidence mismatch")
        requests.append(request)
    # The FK points at durable original bytes, not at the local manifest. Use
    # the same generated API to register originals before any derived chunks.
    # Originals are also search projections; only chunks have a token bound.
    parent_requests = []
    parent_hashes = {parent["sha256"] for parent, _ in parents}
    for request in requests:
        if request.document.raw_source_content_hash and request.document.raw_source_content_hash not in parent_hashes:
            raise ValueError("chunk references an unregistered parent hash")
    for parent, raw in parents:
        text = raw.decode("utf-8")
        document = SourceDocument(source_id=parent["source_id"],
            revision=parent.get("revision") or parent["sha256"], content_hash=parent["sha256"],
            source_uri=parent.get("source_uri") or "urn:sha256:" + parent["sha256"],
            title="Original source: " + parent["source_id"],
            media_type="text/html; charset=utf-8" if parent["artifact_ref"].endswith(".html") else "text/plain; charset=utf-8",
            license="unknown", retrieved_at=manifest["prepared_at"], size_bytes=len(raw))
        request = IngestDocumentRequest(document=document, text=text)
        if not text or len(MessageToJson(request).encode("utf-8")) > 256 * 1024:
            raise ValueError("parent requires nonempty UTF-8 within the CLI request bound")
        parent_requests.append(request)
    results = []
    for kind, request in [("parent", request) for request in parent_requests] + [("chunk", request) for request in requests]:
        result = subprocess.run([str(args.binary.resolve()), "call", "--endpoint", args.endpoint, "IngestDocument"],
            input=MessageToJson(request), text=True, capture_output=True)
        item = {"kind": kind, "source_id": request.document.source_id, "revision": request.document.revision,
            "content_hash": request.document.content_hash, "exit_code": result.returncode}
        if result.returncode == 0:
            item["response"] = json.loads(result.stdout)
        else:
            item["error"] = result.stderr
        results.append(item)
        response = item.get("response", {})
        state = response.get("result", response)
        projection_error = state.get("projectionError", state.get("projection_error", ""))
        accepted = bool(state.get("indexed", False) or state.get("queued", False))
        if accepted:
            item["accepted"] = True
        item["indexed"] = bool(state.get("indexed", False))
        item["queued"] = bool(state.get("queued", False))
        item["projection_error"] = projection_error
        receipt = {"results": results, "summary": {
            "accepted": sum(item.get("accepted", False) for item in results),
            "queued": sum(item.get("queued", False) for item in results),
            "indexed": sum(item.get("indexed", False) for item in results),
            "parents": len(parent_requests), "chunks": len(requests)}}
        (root / "ingestion.json").write_text(json.dumps(receipt, indent=2) + "\n")
        # A parent projection error remains visible as an actual failure in the
        # receipt, but the durable original must still precede its chunks. A
        # chunk projection error is terminal because no derived item follows.
        if result.returncode or (kind == "chunk" and (projection_error or not accepted)):
            raise RuntimeError("ingestion failed; inspect retained ingestion.json")
    summary = receipt["summary"]
    summary["receipt"] = str(root / "ingestion.json")
    print(json.dumps(summary))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    app = commands.add_parser("prepare")
    app.add_argument("--source-root", type=Path, default=ROOT)
    app.add_argument("--revision", required=True)
    app.add_argument("--lithe-dir", type=Path, required=True)
    app.add_argument("--out", type=Path, required=True)
    from git_history import configure_parser, prepare_history
    configure_parser(commands)
    app = commands.add_parser("ingest")
    app.add_argument("--corpus", type=Path, required=True)
    app.add_argument("--binary", type=Path, required=True)
    app.add_argument("--endpoint", required=True)
    args = parser.parse_args()
    {"prepare": prepare, "prepare-history": prepare_history, "ingest": ingest}[args.command](args)


if __name__ == "__main__":
    main()
