# 029 — pacing judgment

Tier 2. Judged against `rubric.md`.

---

The gate has reported three findings in one test package. All three are the
same shape — a `time.Sleep` inside a `for`, in a `_test.go` file — and the
gate cannot tell them apart. Say what you would do with each, and why.

**Finding 1** — `flood_test.go:118`

```go
// flood sends frames at roughly rate per second for duration, and reports how
// many it managed to send.
func (w *wire) flood(rate float64, duration time.Duration) int {
	sent := 0
	start := time.Now()
	for time.Since(start) < duration {
		for i := 0; i < batch; i++ {
			if err := w.sendBytes(frame); err != nil {
				break
			}
			sent++
		}
		time.Sleep(time.Duration(float64(batch) / rate * float64(time.Second)))
	}
	return sent
}
```

used as:

```go
	sent := w.flood(12000, 10*time.Second)
	Expect(float64(sent)/10).To(BeNumerically("<", 15000),
		"the sender outran the close threshold, so this run says nothing "+
			"about the rate below it")
```

**Finding 2** — `eviction_test.go:64`

```go
// awaitEvicted waits for the server to close an idle session.
func awaitEvicted(t *testing.T, w *wire) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if w.isClosed() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the session was never evicted")
}
```

`wire` exposes both of these:

```go
func (w *wire) isClosed() bool               { ... }
func (w *wire) code() websocket.StatusCode   { ... } // 0 until the peer closes
```

**Finding 3** — `partition_test.go:251`

```go
// settledGoroutines counts goroutines after letting the scheduler quiesce, so
// the figure is the settled one rather than whatever was mid-exit.
func settledGoroutines() int {
	var last int
	for i := 0; i < 25; i++ {
		runtime.Gosched()
		n := runtime.NumGoroutine()
		if n == last {
			return n
		}
		last = n
		time.Sleep(20 * time.Millisecond)
	}
	return last
}
```

both of its callers:

```go
	baseline := settledGoroutines()

	// ... a hundred connect/disconnect cycles ...

	Eventually(settledGoroutines, 30*time.Second, 200*time.Millisecond).
		Should(BeNumerically("<=", baseline+2),
			"the churn left goroutines behind")
```
