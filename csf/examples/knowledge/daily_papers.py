#!/usr/bin/env python3
"""Public HF daily-paper snapshots, with immutable evidence and no embeddings."""

from __future__ import annotations

import argparse
from datetime import date, datetime, timezone
import hashlib
import json
from pathlib import Path
import re
from urllib.parse import parse_qs, urlencode, urlsplit
from urllib.request import Request, urlopen

MAX_RESPONSE_BYTES = 5 * 1024 * 1024
PAPER_ID = re.compile(r"\d{4}\.\d{4,5}(?:v\d+)?\Z")


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def encoded(value: object) -> bytes:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()


def write_once(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    try:
        with path.open("xb") as output:
            output.write(data)
    except FileExistsError:
        if path.read_bytes() != data:
            raise ValueError(f"content mismatch at existing artifact: {path}") from None


def blob(out: Path, data: bytes, suffix: str) -> tuple[str, str]:
    sha = digest(data)
    ref = f"blobs/sha256/{sha}.{suffix}"
    write_once(out / ref, data)
    return sha, ref


def read_artifact(root: Path, ref: str, expected_hash: str, size: int | None = None) -> bytes:
    path = (root / ref).resolve()
    if not path.is_relative_to(root.resolve()):
        raise ValueError("artifact path escapes its root")
    data = path.read_bytes()
    if digest(data) != expected_hash or (size is not None and len(data) != size):
        raise ValueError(f"content mismatch: {ref}")
    return data


def parse_page(raw: bytes) -> list[dict]:
    if len(raw) > MAX_RESPONSE_BYTES:
        raise ValueError("response exceeds byte limit")
    entries = json.loads(raw)
    if not isinstance(entries, list) or any(not isinstance(entry, dict) for entry in entries):
        raise ValueError("daily feed must be an array of objects")
    return entries


def fetch_bytes(url: str) -> bytes:
    request = Request(url, headers={"Accept": "application/json", "User-Agent": "candace-brain-spine-papers/1"})
    with urlopen(request, timeout=30) as response:
        data = response.read(MAX_RESPONSE_BYTES + 1)
    if len(data) > MAX_RESPONSE_BYTES:
        raise ValueError("response exceeds byte limit")
    return data


def page_record(out: Path, raw: bytes, url: str, retrieved_at: str, feed_date: str, page: int, limit: int) -> dict:
    sha, ref = blob(out, raw, "json")
    entries = parse_page(raw)
    if len(entries) > limit:
        raise ValueError("feed returned more entries than the requested page limit")
    return {"date": feed_date, "page": page, "limit": limit, "url": url,
            "retrieved_at": retrieved_at, "sha256": sha, "artifact_ref": ref,
            "bytes": len(raw), "items": len(entries)}


def fetch_pages(out: Path, dates: list[str], limit: int = 100, max_pages: int = 100, fetch=fetch_bytes) -> list[dict]:
    if not 1 <= limit <= 100 or max_pages < 1:
        raise ValueError("limit must be 1..100 and max_pages must be positive")
    pages = []
    for feed_date in sorted(set(dates)):
        date.fromisoformat(feed_date)
        seen = set()
        for number in range(max_pages):
            url = "https://huggingface.co/api/daily_papers?" + urlencode(
                {"date": feed_date, "p": number, "limit": limit, "sort": "publishedAt"})
            raw = fetch(url)
            page = page_record(out, raw, url, datetime.now(timezone.utc).isoformat(), feed_date, number, limit)
            if page["sha256"] in seen and page["items"]:
                raise ValueError("pagination repeated a nonempty page")
            seen.add(page["sha256"])
            pages.append(page)
            if page["items"] < limit:
                break
        else:
            raise ValueError("pagination exceeded max_pages; snapshot is incomplete")
    return pages


def import_pages(out: Path, receipt: Path, source_root: Path) -> list[dict]:
    feeds = json.loads(receipt.read_bytes())
    if not isinstance(feeds, list):
        raise ValueError("import receipt must contain the original list of daily feeds")
    pages = []
    for feed in feeds:
        feed_date = feed["date"]
        date.fromisoformat(feed_date)
        imported = []
        for number, entry in enumerate(feed["pages"]):
            url = urlsplit(entry["url"])
            query = parse_qs(url.query)
            if (url.scheme != "https" or url.netloc != "huggingface.co" or
                    url.path != "/api/daily_papers" or query.get("date") != [feed_date] or
                    query.get("p") != [str(number)]):
                raise ValueError("receipt URL disagrees with feed date/page")
            limit = int(query["limit"][0])
            if not 1 <= limit <= 100:
                raise ValueError("invalid imported page limit")
            raw = read_artifact(source_root, entry["path"], entry["sha256"], entry["bytes"])
            page = page_record(out, raw, entry["url"], feed["retrieved_at"], feed_date, number, limit)
            if page["items"] != entry["items"]:
                raise ValueError("receipt item count mismatch")
            imported.append(page)
        if (not imported or imported[-1]["items"] >= imported[-1]["limit"] or
                any(page["items"] != page["limit"] for page in imported[:-1]) or
                sum(page["items"] for page in imported) != feed["count"]):
            raise ValueError("receipt does not establish complete pagination")
        pages.extend(imported)
    return pages


def semantic_record(paper: dict) -> dict:
    paper_id = paper.get("id", "")
    title = paper.get("title")
    if not isinstance(paper_id, str) or not PAPER_ID.fullmatch(paper_id) or not isinstance(title, str) or not title.strip():
        raise ValueError("paper is missing a valid id/title")
    summary = paper.get("summary")
    if summary is not None and not isinstance(summary, str):
        raise ValueError("paper summary must be text")
    text = summary.strip() if summary and summary.strip() else title.strip()
    links = {f"https://arxiv.org/abs/{paper_id}"}
    for key in ("githubRepo", "projectPage"):
        value = paper.get(key)
        if isinstance(value, str) and urlsplit(value).scheme in ("https", "http"):
            links.add(value)
    license_value = paper.get("license")
    return {"id": f"arxiv:{paper_id}", "source_id": f"arxiv:{paper_id}",
            "title": title.strip(), "text": text, "url": f"https://huggingface.co/papers/{paper_id}",
            "license": license_value if isinstance(license_value, str) and license_value else "NOASSERTION",
            "links": sorted(links), "media_type": "text/plain", "text_kind": "abstract" if summary and summary.strip() else "title",
            "paper_published_at": paper.get("publishedAt")}


def normalize(out: Path, pages: list[dict]) -> tuple[list[dict], dict]:
    records = {}
    for page in pages:
        entries = parse_page(read_artifact(out, page["artifact_ref"], page["sha256"], page["bytes"]))
        for index, entry in enumerate(entries):
            paper = entry.get("paper")
            if not isinstance(paper, dict):
                raise ValueError("daily feed entry has no paper object")
            document = semantic_record(paper)
            revision = "sha256:" + digest(encoded(document))
            key = (document["id"], revision)
            if key not in records:
                content_hash, artifact_ref = blob(out, document["text"].encode(), "txt")
                document.update({"revision": revision, "content_hash": content_hash, "artifact_ref": artifact_ref,
                                 "retrieved_at": page["retrieved_at"], "raw_source_content_hash": page["sha256"],
                                 "raw_source_artifact_ref": page["artifact_ref"], "feed_dates": [], "source_occurrences": []})
                records[key] = document
            record = records[key]
            record["feed_dates"] = sorted(set(record["feed_dates"]) | {page["date"]})
            occurrence = {"feed_date": page["date"], "page": page["page"], "index": index,
                          "url": page["url"], "retrieved_at": page["retrieved_at"],
                          "raw_source_content_hash": page["sha256"], "raw_source_artifact_ref": page["artifact_ref"]}
            if occurrence not in record["source_occurrences"]:
                record["source_occurrences"].append(occurrence)
    documents = [records[key] for key in sorted(records)]
    data = b"".join(encoded(document) + b"\n" for document in documents)
    write_once(out / "documents.jsonl", data)
    receipt = {"format": "candace.hf-daily-papers.v1", "normalizer_version": 1,
               "snapshot_atomic_across_pages": False, "complete": True, "pages": pages,
               "documents": {"artifact_ref": "documents.jsonl", "sha256": digest(data), "bytes": len(data), "count": len(documents)},
               "feed_counts": {day: sum(page["items"] for page in pages if page["date"] == day) for day in sorted({page["date"] for page in pages})}}
    write_once(out / "receipt.json", encoded(receipt) + b"\n")
    return documents, receipt


def verify(out: Path) -> dict:
    receipt = json.loads((out / "receipt.json").read_bytes())
    entry = receipt["documents"]
    data = read_artifact(out, entry["artifact_ref"], entry["sha256"], entry["bytes"])
    documents = [json.loads(line) for line in data.splitlines()]
    if len(documents) != entry["count"]:
        raise ValueError("document count mismatch")
    for page in receipt["pages"]:
        raw = read_artifact(out, page["artifact_ref"], page["sha256"], page["bytes"])
        if len(parse_page(raw)) != page["items"]:
            raise ValueError("page item count mismatch")
    for document in documents:
        text = read_artifact(out, document["artifact_ref"], document["content_hash"])
        if text != document["text"].encode():
            raise ValueError("document text and content artifact disagree")
        for occurrence in document["source_occurrences"]:
            raw = read_artifact(out, occurrence["raw_source_artifact_ref"], occurrence["raw_source_content_hash"])
            source = semantic_record(parse_page(raw)[occurrence["index"]]["paper"])
            if ("sha256:" + digest(encoded(source)) != document["revision"] or
                    any(document.get(key) != value for key, value in source.items())):
                raise ValueError("normalized document does not match its raw source")
    return {"verified": True, "documents": len(documents), "feed_counts": receipt["feed_counts"]}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    fetch = commands.add_parser("fetch")
    fetch.add_argument("--date", action="append", required=True)
    fetch.add_argument("--limit", type=int, default=100)
    fetch.add_argument("--max-pages", type=int, default=100)
    imported = commands.add_parser("import")
    imported.add_argument("--receipt", type=Path, required=True)
    imported.add_argument("--source-root", type=Path, required=True)
    check = commands.add_parser("verify")
    for command in (fetch, imported, check):
        command.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.command == "fetch":
            normalize(args.out, fetch_pages(args.out, args.date, args.limit, args.max_pages))
        elif args.command == "import":
            normalize(args.out, import_pages(args.out, args.receipt, args.source_root))
        print(json.dumps(verify(args.out), sort_keys=True))
    except (ValueError, OSError, KeyError, TypeError) as error:
        parser.exit(1, f"{error}\n")


if __name__ == "__main__":
    main()
