# Daily paper evidence for the knowledge base

`daily_papers.py` is a Python 3.10+ standard-library ingestor for the public
Hugging Face daily feed. It requires no token, model, embedding service, or host
package installation. It retains every returned paper; curation is a separate
artifact and never filters ingestion.

`curated.json` is a reference bibliography only. Its links are reading pointers;
historical feed snapshots, paper bytes, license reviews and ingestion receipts
are not included in the public package.

From the repository root:

```sh
python3 csf/examples/knowledge/daily_papers.py fetch \
  --date 2026-09-16 --date 2026-09-15 --limit 20 \
  --out csf/examples/knowledge/artifacts/new-snapshot

python3 csf/examples/knowledge/daily_papers.py verify \
  --out csf/examples/knowledge/artifacts/new-snapshot

PYTHONDONTWRITEBYTECODE=1 python3 -m pytest -q \
  csf/examples/knowledge/test_daily_papers.py
```

Use a new output directory for each live snapshot. Existing artifacts must match
byte for byte; the ingestor refuses to overwrite different evidence. Each page
is fetched with `p`, `limit`, `date`, and `sort=publishedAt`. A short page ends
pagination, including an empty terminal page after an exactly full page. Repeated
nonempty responses and exhausted page limits fail without a completed receipt.
Requests have a 30-second timeout and a 5 MiB response bound. A multi-page feed
is not an atomic snapshot; the receipt says so.

`documents.jsonl` contains one record per `(source_id, revision)`. Identical
appearances across dates/pages retain every occurrence. A changed semantic
record receives a new revision, rather than silently replacing the old one.

| Field | Meaning |
| --- | --- |
| `id`, `source_id` | Stable `arxiv:<ID>` source identifier |
| `title`, `url`, `links` | Paper title, HF paper URI, arXiv/repository/project links |
| `text`, `text_kind` | Author abstract; title fallback if unavailable; never the AI summary |
| `content_hash`, `artifact_ref` | SHA256 of exact UTF-8 `text`, and its `.txt` CAS artifact |
| `revision` | SHA256 of canonical semantic metadata; **not** an asserted arXiv version |
| `license` | Source-reported value or `NOASSERTION`; repository licenses stay separate |
| `retrieved_at`, `paper_published_at`, `feed_dates` | Observation, publication, and feed membership dates, kept distinct |
| `raw_source_content_hash`, `raw_source_artifact_ref` | Hash and CAS path of the primary raw JSON page |
| `source_occurrences` | Every feed date, URL, page, array index, timestamp, and raw-page hash |

Artifact references are relative to the snapshot directory. `revision` hashes
`semantic_record`'s sorted-key compact UTF-8 JSON. Engagement counts, generated
summaries, retrieval time, and feed membership do not change that revision. A
normalizer behavior change must increment `normalizer_version`.

`receipt.json` records URLs, byte counts, page hashes, per-date item counts, and
the normalized JSONL hash. `verify` resolves every retained content artifact and
rebuilds each document's semantic fields from its raw source occurrence. It
detects changed bytes, inconsistent normalized text, and source/content mismatch.
These checks establish artifact integrity, not the truth of a paper's claims.

The knowledge-base owner can register each text and raw page as a blob, then
register source revisions. Vectorization belongs to that owner. No synthetic
vectors or claimed semantic retrieval are supplied here.

## Selected semantic corpus through the shared host

`semantic_corpus.py` prepares an explicit five-file code slice from `git show`
from a checkout of the public repository at a supplied commit, plus consumer-supplied LITHE v1 text and HTML. It
verifies the paper hashes against the consumer-supplied `lithe-index.json` receipt in `--lithe-dir` (keys: `text_sha256`, `html_sha256`, `source`). No paper corpus or private run history is bundled. It
retains full immutable parent bytes with the existing daily-paper artifact
helper, and links chunks through `SourceDocument.raw_source_content_hash` and
the corpus manifest's parent hash. Paper chunks retain both original HTML and
extracted-text provenance. No untracked files, credentials, sessions, or broad
repository traversal enter the corpus.

```bash
uv run --script csf/examples/knowledge/semantic_corpus.py prepare \
  --source-root /absolute/source/checkout --revision <committed-sha> \
  --lithe-dir /absolute/retained/lithe --out /absolute/ignored/corpus
uv run --script csf/examples/knowledge/semantic_corpus.py ingest \
  --corpus /absolute/ignored/corpus/corpus.json \
  --binary /absolute/root-built/brain-spine --endpoint http://127.0.0.1:14111
```

Preparation uses the hash-pinned MiniLM tokenizer at a pinned upstream revision.
Every chunk preserves exact source text, contains at most 240 content tokens
and is checked against the model's 256-token bound including special tokens.
The script pins its Python dependencies inline for `uv run --script`.

Ingestion validates every retained parent/chunk hash and raw-parent reference
before its first write. It registers original UTF-8 sources first, through the
generated `SourceDocument`/`IngestDocumentRequest` and existing Go client; only
then does it submit chunks whose foreign keys reference those durable originals.
Both original and chunk bytes are retained in the runtime CAS. Parent requests
must fit the CLI's 256-KiB serialized input bound; unsupported originals fail
preflight without partial writes. Binary original import is not supplied by
this UTF-8 API.

Every response is retained with `kind: parent` or `kind: chunk`. Parent search
projection failures remain visible but do not prevent indexing chunks once the
original is durably registered. Chunk projection failure fails the run. The
token bound describes chunks only: full originals also pass through the existing
OpenSearch projection and may be truncated by its embedding model. Source-byte
integrity and full-model-token coverage are separate claims. Repeating ingestion
uses the same immutable source ID/revision; it does not launch a daemon.

Live acceptance must query the existing shared-host MCP `Search`, confirm
`mode=semantic`, and inspect each hit's source ID, revision, content hash and
excerpt against the corpus. Retain queries/results and invocation duration.
This selected slice is neither a whole-repository index nor a changed-file
overlay, and a few relevant queries do not establish general retrieval quality.

Tests: `uv run --with tokenizers==0.22.0 --with protobuf==7.35.1 --with pytest
python -m pytest csf/examples/knowledge/test_semantic_corpus.py -q`.

## Git history: pinned refs, broad metadata, bounded patches

```bash
uv run --script csf/examples/knowledge/semantic_corpus.py prepare-history \
  --source-root /absolute/source/checkout --out /absolute/ignored/git-history \
  --max-diff-commits 100 --max-diff-paths 50 \
  --max-patch-bytes 65536 --max-total-patch-bytes 8388608
```

This captures HEAD and every local ref (including remote-tracking refs and tags),
peels captured object IDs to commits, and indexes metadata, parents and exact
parent-relative changed paths for **all commits reachable from those captured
tips**. It does not index reflog-only or unreachable objects. The default
10,000-commit and 128-MiB metadata limits fail preparation rather than silently
returning a partial metadata corpus. Per-object and per-comparison limits are
also explicit in `prepare-history --help` and retained coverage.

Patches are additionally limited by selected commit count, paths per parent,
bytes per patch and total bytes. The default path selection covers
`go/csf`, `go/app/brainspine`, and `research/brain-spine`; repeat
`--diff-path <literal-relative-file-or-subtree>` to select a different slice.
Every merge is compared separately with each parent. Root commits compare with
the empty tree. NUL-delimited Git output and retained base64 path bytes preserve
renames, deletions, tabs, newlines and non-UTF-8 filenames. External diff and
textconv programs are disabled. Binary, non-UTF-8, oversized and over-budget
patches have explicit omission reasons; broad metadata remains present.

`history-snapshot.json` pins the captured tips on the first run. Reusing the
same output directory reuses that snapshot even if branches later move; use a
new directory for a fresh snapshot. Source IDs include commit/parent/path
identity, so immutable unchanged items keep the same identity across snapshots.
Preparation never invents branch-of-authorship or original-worktree history.
Current refs and worktrees are separately timestamped observations with hashed
raw evidence, not historical attribution.

Raw commit objects, raw NUL change records, selected exact patch bytes and all
searchable text chunks use the existing immutable artifact helper. The manifest
links generated source documents to raw hashes; `preparation-receipt.json`
records coverage, omissions, duration and the corpus hash. Use the **same**
`semantic_corpus.py ingest --corpus .../corpus.json --binary ... --endpoint ...`
command as the source/paper corpus. Preparation is not indexing success.

History checks use real temporary Git repositories:

```bash
uv run --with tokenizers==0.22.0 --with protobuf==7.35.1 --with pytest \
  python -m pytest csf/examples/knowledge/test_semantic_corpus.py \
  csf/examples/knowledge/test_git_history.py -q
```
