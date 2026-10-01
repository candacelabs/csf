# G2 baseline — raw data

Every run the harness emitted for [../../g2-baseline.md](../../g2-baseline.md), in
the layout `<cell>/<run-id>/`. equivalence-spec §6's audit rule is that the raw
directory contains **every run id the harness ever emitted for the final
report**, and that a gap in the id sequence is an audit failure: the ids here run
`r01`…`rNN` with no gaps, and no run was started and discarded.

| File | What it is |
|---|---|
| `m0.csv`, `mn.csv` | the two §3.6 windows — 60 samples at 1 Hz, one row each: `unix_ms,memory_current,file,anon,sock,slab,kernel`, read from the host out of the measured container's cgroup v2 directory |
| `floor-m0.csv`, `floor-mn.csv` | the labelled post-`debug.FreeOSMemory()` floor, 10 samples, taken **after** the headline window closed |
| `run.json` | the run manifest: N, window order, git SHA, image digest, cpusets, memory limit, `GOGC`/`GOMEMLIMIT`, warm-up count, settle time, the limits in force, host state before and after each window, the contended flag, and the TLS-boundary assertions |
| `window-m0.json`, `window-mn.json` | per-window manifests, embedded in `run.json` and kept separately for diffing |
| `introspect-*.json` | `runtime/metrics` and goroutine count at the close of each window |
| `floor-*.json` | the same, after the forced GC, with `forced_gc: true` on the record itself so the label travels with the number |
| `driver-mn.json` | the session driver's counters: target, dialed, mounted, live, closed, dial errors, read errors, acks, heartbeats |
| `host-pre-*.json`, `host-post-*.json` | uptime, cores, load average, memory, and the count and names of the unrelated containers running on this shared host |
| `campaign.log` | the harness's console output for the whole campaign, in order |

**One thing is deliberately not here.** Each M(N) window's `sut-mn.log` is
327 KB of provenance JSON — one mount record per session, and nothing after
that, because the workload is Idle. Committing 3.6 MB of it would bury the data
that produces the figure. The logs stayed on the measuring host; what they
contain is one `patch` record per session establishment, and equivalence-spec
§5.6's question about them ("where was the stream sunk") is answered in
`run.json` under `provenance_sink`.

Recompute any figure in the report from this directory, with no Go on the host:

```bash
docker run --rm -v "$PWD/gotth-live:/w" \
    -v "$PWD/gotth-live/docs/bench/data/g2-baseline/n1000-obs-on:/cell" \
    -w /w/test/memory dis-gotth-live:latest \
    bash -c 'go run ./cmd/memstat -cell /cell'
```
