# 2026-10-06: "context canceled" from the adapter's store under Bazel

Question: why did two copilot-adapter integration specs fail under Bazel CI
with `{"code":"store_error","message":"context canceled"}` and HTTP 500 where
202 and 200 were expected, while all 110 specs passed under `go test`,
including with `-race`?

Verdict: **two harness and runtime defects, both fixed; the adapter's
production code was not at fault.** No spec cancelled anything, no server shut
down early, and no pgmem transaction misbehaved. The request context of a
request that was still being served ended by itself.

## The mechanism

`adaptertest.Serve` served the adapter over gRPC's `bufconn`, an in-memory
`net.Conn`. net/http's server keeps one goroutine reading a connection while a
handler runs, and treats a failed read as a closed connection: it cancels the
request's context. When a handler returns, the server stops that read by
setting the connection's read deadline to a time in the past, waits for the
read to return, and clears the deadline.

`bufconn.SetReadDeadline` stops the previous timer and clears its timed-out
flag, but a timer that has already fired can still be about to run its
callback. When the next request has already arrived, the background read
returns its first byte instead of the timeout, the deadline is cleared, and
the late callback then sets the flag. The flag is not noticed until the next
read of an empty pipe, which is the background read that starts once the next
request's body is read (or at once, for a request without one). That read
returns `i/o timeout` with no deadline set; net/http cancels the request; the
handler's next store call returns `context.Canceled`.

A trace of the server side of one such connection (times are from its first
byte; `PAST` is the expired deadline):

```
319.420065ms Read(1) begin                     background read for the request being served
319.489804ms SetReadDeadline(PAST)             handler returned
319.553786ms Read(1) -> n=1 err=<nil>          the next request's first byte, not the timeout
319.554515ms SetReadDeadline(zero)             the late callback is still to run
319.555085ms Read(4095) begin ... n=94         the rest of that request
319.557875ms Read(1) begin                     background read for the next handler
319.564746ms Read(1) -> n=0 err=i/o timeout    no deadline is set
```

Whether the next request arrives inside that window depends on the scheduler,
which is why a quiet machine never saw it and a CI machine running many test
targets at once did.

## How it was reproduced

Not by repeating the suite: 10 sequential `-race` runs at `GOMAXPROCS=2` and
24 concurrent ones on an idle 32-core host produced no `context canceled` (one
of the 24 failed with the second defect below). What did:

- A bare net/http server over `bufconn` with eight clients sending back-to-back
  requests cancelled 2529 of 24000 request contexts at `GOMAXPROCS=2`; the
  same server over `net.Pipe` cancelled 0 of 24000.
- Twelve copies of the integration suite at once on a 4-CPU budget
  (`--cpuset-cpus`, `GOMAXPROCS=4`, `-race`), which approximates a CI runner.
  Before the fix 1 copy of 12 passed: nine copies finished with 1 to 7 failed
  specs between them (24 `store_error` `context canceled` responses, six of
  them wrapped as `context canceled (SQLSTATE XX000)`, and three of the second
  defect below), and two hung. After the fix 24 copies of 24 passed.

The rate depends strongly on handler duration and `GOMAXPROCS` (one handler
shape cancelled 16% of requests at `GOMAXPROCS=2` and none at 32, another the
reverse), so a regression spec needs several concurrent clients and handlers of
mixed duration, and several servers, to be sensitive everywhere.

## A second defect found on the way

Under the same load a spec's cleanup also failed with `runtime:
adaptertest http: serve copilot-adapter.invalid:80 stopped before its scope was
canceled`. `Scope.start` judged an owner's return by `ctx.Err()` on the context
conc's pool gives each task, a child of the scope's context. Cancellation
reaches the children of a context one at a time; the HTTP listener drains from
a `context.AfterFunc` on the scope's context, so the serving goroutine can
return before the pool's child has been told. It is a real race in `runtime`,
about one close in 100000 under heavy CPU contention, not in the harness.

## What changed

- `adaptertest` accepts `net.Pipe` pairs from an in-package listener instead of
  `bufconn`. `net.Pipe` settles a cleared deadline exactly.
- `Scope.start` asks the scope's own context, which is already done by the time
  any callback or child is notified.

Each has a spec that fails reliably on the old code (20 of 20 runs at both
`GOMAXPROCS=2` and `GOMAXPROCS=32`) and passes 20 of 20 on the new.

## The two hangs

Both stuck copies were parked on a bare channel receive in a spec that expects
a 500: `Expect(<-modelChanges)` in `expectModelTransactionRollback` and
`firstSend := <-sends` in the scheduled-prompt spec. A request cancelled before
it reached the bridge also answers 500, so the status check passed and the
spec then waited forever for a bridge call that never came. They went away
with the cause; the receives themselves still turn any failure before the
bridge call into a hang instead of a message.

## Not changed

`pkg/gotth/live`'s service spec also serves HTTP over `bufconn`. It sends one
WebSocket upgrade per connection, so the next-request window never opens; it
should move off `bufconn` if it ever sends back-to-back requests.
