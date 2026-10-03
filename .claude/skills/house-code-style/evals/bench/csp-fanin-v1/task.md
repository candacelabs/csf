# Bench task `csp-fanin-v1` — a concurrent fan-in aggregator

This is the brief the implementer receives. It is deliberately silent on how to
build the thing: the same brief is run in both A/B conditions, and a brief that
names channels or mutexes would decide the experiment before it started.

---

## What to build

Package `fanin`, in a module named `fanin`, in one or more files of your
choosing. The provided `fanin_test.go` must pass unmodified.

```go
type Item struct {
	Key   string
	Value int
}

type Totals struct {
	Counts map[string]int
	Total  int
}

func New(workerCount int, bufferSize int) *Aggregator
func (aggregator *Aggregator) Submit(ctx context.Context, item Item) error
func (aggregator *Aggregator) Snapshot() Totals
func (aggregator *Aggregator) Close()
```

An `Aggregator` accepts items from many goroutines at once and keeps a running
total per key, plus a grand total.

## The contract, in full

**`New(workerCount int, bufferSize int) *Aggregator`**

- Returns an aggregator ready to accept submissions.
- `workerCount >= 1` is how many goroutines the aggregator may use internally
  to consume submissions. Every value must be externally indistinguishable: the
  same submissions produce the same totals whether `workerCount` is 1 or 8.
- `bufferSize >= 0` is how much submitted work the aggregator may hold before a
  submitter has to wait. `0` is legal and means an unbuffered handoff.

**`Submit(ctx context.Context, item Item) error`**

- Adds `item.Value` to `Counts[item.Key]` and to `Total`.
- Safe to call from many goroutines at once.
- Returns `nil` if and only if the item **will be counted**. A `nil` return is
  a promise: that item appears in every snapshot taken after `Close` returns.
- Checks `ctx` **before** accepting the item. If `ctx` is already done, Submit
  returns an error wrapping `ctx.Err()` and the item is not counted. It also
  returns that error if the context becomes done while the submission is
  waiting to be accepted.
- Returns a non-nil error after `Close`. It must not block forever, panic, or
  silently drop the item.

**`Snapshot() Totals`**

- Returns the current totals. Safe to call from many goroutines at once, and
  concurrently with `Submit`.
- The returned `Totals` is the caller's: mutating `Counts` or `Total` must not
  affect the aggregator or any later snapshot.
- Internally consistent. For every snapshot, including one taken while
  submissions are in flight, `Total` equals the sum of the values in `Counts`.
  A snapshot may never show an item counted in one and not the other.
- Never goes backwards: `Total` is non-decreasing across successive snapshots.
- Legal before, during and after `Close`.

**`Close()`**

- Graceful drain: returns only after every item whose `Submit` returned `nil`
  has been counted. Nothing accepted is dropped.
- Idempotent, and safe to call concurrently from several goroutines. Repeated
  calls must not panic, hang, or change the totals.

## How this is graded

```bash
go vet ./...
go test -race ./...
```

Both must pass with `fanin_test.go` exactly as provided. Editing, deleting or
skipping any test in it voids the run.

There is no style requirement in this brief and no partial credit for elegance.
What is recorded is: did it compile, did the tests pass, was it race-clean, did
it use a mutex, and what did it cost in tokens and tool calls.
