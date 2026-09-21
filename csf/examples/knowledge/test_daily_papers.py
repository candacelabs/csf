"""Evidence-boundary tests; no network, downloaded code, or model calls."""

import json
from urllib.parse import parse_qs, urlsplit

import pytest

import daily_papers as papers


def entry(paper_id, summary="An author abstract."):
    return {"paper": {"id": paper_id, "title": f"Paper {paper_id}", "summary": summary,
                      "ai_summary": "This must never become the source abstract.", "publishedAt": "2026-09-15T00:00:00Z"}}


def feed(out, entries, limit=2):
    calls = []

    def fetch(url):
        query = parse_qs(urlsplit(url).query)
        number = int(query["p"][0])
        calls.append(number)
        return papers.encoded(entries[number * limit:(number + 1) * limit])

    pages = papers.fetch_pages(out, ["2026-09-15"], limit=limit, fetch=fetch)
    return papers.normalize(out, pages), calls


def test_all_pages_preserved_and_empty_terminal_page_requested(tmp_path):
    (documents, receipt), calls = feed(tmp_path, [entry(f"2609.{number:05d}") for number in range(4)])
    assert calls == [0, 1, 2]
    assert [page["items"] for page in receipt["pages"]] == [2, 2, 0]
    assert len(documents) == 4
    assert papers.verify(tmp_path)["feed_counts"] == {"2026-09-15": 4}
    for document in documents:
        assert (tmp_path / document["artifact_ref"]).read_bytes() == document["text"].encode()
        assert document["content_hash"] == papers.digest(document["text"].encode())
        assert document["license"] == "NOASSERTION"


def test_hashes_ignore_engagement_and_retrieval_time_but_not_content(tmp_path):
    original = entry("2609.14858")
    (first, _), _ = feed(tmp_path / "first", [original])
    changed = json.loads(json.dumps(original))
    changed["paper"]["upvotes"] = 1000
    changed["paper"]["ai_summary"] = "A different machine summary."
    (second, _), _ = feed(tmp_path / "second", [changed])
    assert first[0]["content_hash"] == second[0]["content_hash"]
    assert first[0]["revision"] == second[0]["revision"]
    changed["paper"]["summary"] += " Corrected observation."
    (third, _), _ = feed(tmp_path / "third", [changed])
    assert first[0]["revision"] != third[0]["revision"]
    assert first[0]["content_hash"] != third[0]["content_hash"]


def test_repeated_paper_keeps_distinct_dates_and_source_pointers(tmp_path):
    raw = papers.encoded([entry("2609.14858")])
    pages = [papers.page_record(tmp_path, raw, f"https://example.invalid/{day}", day + "T00:00:00Z", day, 0, 100)
             for day in ("2026-09-15", "2026-09-16")]
    documents, receipt = papers.normalize(tmp_path, pages)
    assert len(documents) == 1
    assert documents[0]["feed_dates"] == ["2026-09-15", "2026-09-16"]
    assert len(documents[0]["source_occurrences"]) == 2
    assert receipt["feed_counts"] == {"2026-09-15": 1, "2026-09-16": 1}
    original = (tmp_path / "documents.jsonl").read_bytes()
    papers.normalize(tmp_path, pages)
    assert (tmp_path / "documents.jsonl").read_bytes() == original


@pytest.mark.parametrize("which", ["artifact_ref", "raw_source_artifact_ref"])
def test_corrupted_retained_content_is_rejected(tmp_path, which):
    (documents, _), _ = feed(tmp_path, [entry("2609.14858")])
    (tmp_path / documents[0][which]).write_bytes(b"tampered")
    with pytest.raises(ValueError, match="content mismatch"):
        papers.verify(tmp_path)


def test_updated_manifest_cannot_hide_mismatch_with_original_source(tmp_path):
    (documents, _), _ = feed(tmp_path, [entry("2609.14858")])
    documents[0]["title"] = "Invented title"
    data = papers.encoded(documents[0]) + b"\n"
    (tmp_path / "documents.jsonl").write_bytes(data)
    receipt = json.loads((tmp_path / "receipt.json").read_bytes())
    receipt["documents"].update(sha256=papers.digest(data), bytes=len(data))
    (tmp_path / "receipt.json").write_bytes(papers.encoded(receipt))
    with pytest.raises(ValueError, match="does not match its raw source"):
        papers.verify(tmp_path)


def test_repeating_server_page_fails_without_complete_receipt(tmp_path):
    with pytest.raises(ValueError, match="repeated"):
        papers.fetch_pages(tmp_path, ["2026-09-15"], limit=1,
                           fetch=lambda _: papers.encoded([entry("2609.14858")]))
    assert not (tmp_path / "receipt.json").exists()


def test_import_rejects_source_hash_mismatch(tmp_path):
    raw = papers.encoded([entry("2609.14858")])
    (tmp_path / "page.json").write_bytes(raw)
    receipt = [{"date": "2026-09-15", "retrieved_at": "2026-09-16T00:00:00Z", "count": 1,
                "pages": [{"url": "https://huggingface.co/api/daily_papers?date=2026-09-15&p=0&limit=100",
                           "path": "page.json", "sha256": "0" * 64, "bytes": len(raw), "items": 1}]}]
    (tmp_path / "receipt.json").write_bytes(papers.encoded(receipt))
    with pytest.raises(ValueError, match="content mismatch"):
        papers.import_pages(tmp_path / "out", tmp_path / "receipt.json", tmp_path)


def test_source_path_cannot_escape_artifact_root(tmp_path):
    with pytest.raises(ValueError, match="escapes"):
        papers.read_artifact(tmp_path, "../outside", "0" * 64)
