# Case 021 — the config lock that stays and the one that goes

| | |
|---|---|
| **tier** | 2 (judged against `rubric.md`) |
| **kind** | **split** — one half of the answer is "keep the mutex", the other is "use a channel" |
| **rule under test** | CS-5, and specifically the counterweight that keeps it from meaning "always channels" |

Give the model the prompt below with the `house-code-style` skill available.
The scenario is synthetic; no such package exists in the repo, and the answer
must not depend on one existing.

---

```
new CS-5 rule, ok. so the widget host's config package. two things in it, one
lock.

the read path: every request looks up a feature flag, call it 40k/sec across
all the goroutines, it's an RWMutex around a map[string]bool and readers just
RLock, read, RUnlock.

the reload path: the file watcher goroutine, the /admin/reload handler and the
retry timer all take the same lock to move a reloadState field between valid /
reloading / failed, and the watcher won't start a reload if the handler
already did, and the retry timer backs off if the state is failed, and there's
a generation counter in there too so a late reload doesn't clobber a newer one.

do i convert this to channels or not
```

---

## Context the judge should assume the model can discover

- `scripts/check_style.py` reports **both** locks — it flags every
  `sync.Mutex`/`sync.RWMutex` declaration and cannot distinguish them. There is
  one declaration here, shared by both paths, so there is exactly one finding.
- The gate is report-only. No CI failure is pending on this, and nothing is
  asking for the change on a deadline.
- `-race` is currently clean. There is no reported bug; the question is
  design, not a crash.
