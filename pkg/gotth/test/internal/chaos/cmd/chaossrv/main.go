// Command chaossrv is a live server in its own process, for the one chaos case
// that cannot be expressed inside the test binary.
//
// PRD Phase 3, case 3 is "server restarted under load". A restart of the
// application object inside the test process — close the App, build a new one,
// keep the listener — is a fair test of Mount and of Snapshot, and it is not a
// test of a restart: the listener never closes, the accept queue never drains,
// the sockets are never reset by the kernel, and the port is never rebound. The
// failure an operator actually meets on a deploy is the one where all four of
// those happen at once, so the server under test is a child process that gets
// SIGKILL.
//
// It is deliberately tiny. The application it serves is the same shape as the
// suite's own — one counter, one note, a commit effect against a file-backed
// ledger so that server truth survives the process that held it — and nothing
// else. Anything more here would be behaviour the suite tests through a process
// boundary for no reason.
//
// Its ledger is a file rather than memory because that is what makes the case
// checkable at all: after a SIGKILL there is no in-process state to compare a
// reconnected client's Snapshot against, and a restart whose "server truth"
// died with the server proves only that a new server starts.
//
// Usage:
//
//	chaossrv -addr 127.0.0.1:9000 -ledger /tmp/x.ledger
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// fileLedger is the commit log, appended one identifier per line and read back
// whole at mount. Durability beyond "the bytes were written" is not the point;
// surviving the process is.
type fileLedger struct {
	mu   sync.Mutex
	path string
}

func (l *fileLedger) commit(ref uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "%d\n", ref); err != nil {
		// Preserve the primary write failure while releasing the descriptor.
		_ = f.Close()
		return err
	}
	// This case restarts a process, not the host: Write has handed the bytes to
	// the kernel, whose page cache survives SIGKILL. Syncing every event would
	// turn the reconnect test into a storage-latency test and can starve mounts
	// behind the ledger mutex before they can send their first Snapshot.
	return f.Close()
}

func (l *fileLedger) total() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.path)
	if err != nil {
		return 0
	}
	// Read-only descriptor: there is no buffered write to acknowledge on close.
	defer func() { _ = f.Close() }()

	distinct := map[string]struct{}{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			distinct[line] = struct{}{}
		}
	}
	return len(distinct)
}

type state struct {
	Total int
	Note  string
}

// commitEffect is the effect that writes one reference to the ledger. It is a
// constructor over a concrete live.Effect[user], closing over the ledger it appends
// to, which is why nothing here needs a central executor.
func commitEffect(led *fileLedger, ref uint64) live.Effect[user] {
	return live.Effect[user]{
		Source: "chaos.commit",
		Run: func(ctx context.Context, session live.Session[user], emit live.Emitter) error {
			return led.commit(ref)
		},
	}
}

type user string

func (u user) Subject() string { return string(u) }

func text(format string, args ...any) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := fmt.Fprintf(w, format, args...)
		return err
	})
}

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "address to listen on")
	ledgerPath := flag.String("ledger", "", "path to the commit ledger")
	origin := flag.String("origin", "https://chaos.example", "the one allowed origin")
	flag.Parse()

	if *ledgerPath == "" {
		fmt.Fprintln(os.Stderr, "chaossrv: -ledger is required")
		os.Exit(2)
	}
	led := &fileLedger{path: *ledgerPath}

	// Warnings and errors to stderr, which the parent forwards. A child that
	// refuses every event for a reason nobody can see is a case that fails with
	// "no load reached the server" and no way to find out why; this is that
	// diagnosis, kept rather than removed after it did its job.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	app, err := live.New(live.Config[state, user]{
		Logger: logger,
		Init: func(ctx context.Context, session live.Session[user]) (state, []live.Effect[user], error) {
			return state{Total: led.total(), Note: "ok"}, nil, nil
		},
		Reduce: func(s state, ev live.Event) (state, []live.Effect[user]) {
			switch ev.Name {
			case "chaos.commit":
				ref, _ := strconv.ParseUint(ev.Fields.Get("ref"), 10, 64)
				s.Total++
				return s, []live.Effect[user]{commitEffect(led, ref)}
			case "chaos.note":
				s.Note = ev.Fields.Get("note")
			}
			return s, nil
		},
		Fragments: []live.Fragment[state]{
			{
				ID:     "total",
				Render: func(s state) templ.Component { return text("<b>%d</b>", s.Total) },
				Dirty:  func(prev, next state) bool { return prev.Total != next.Total },
			},
			{
				ID:     "note",
				Render: func(s state) templ.Component { return text("<i>%s</i>", s.Note) },
				Dirty:  func(prev, next state) bool { return prev.Note != next.Note },
			},
		},
		Events: []string{"chaos.commit", "chaos.note"},
		// The fleet is 25 concurrent connections under one identity, and
		// live.Limits.MaxSessionsPerIdentity defaults to 20 — so without this
		// the last five are refused with 503 and the case measures the default
		// rather than the restart. The default is documented and correct; a
		// restart fleet is simply not what it is sized for.
		Limits:       live.Limits{MaxSessionsPerIdentity: 200},
		Origins:      []string{*origin},
		Authenticate: func(request *http.Request) (user, error) { return user("chaos"), nil },
		Authorize:    live.AllowAll[user],
		CSRF:         live.NoCSRFCheck,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "chaossrv:", err)
		os.Exit(1)
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "chaossrv:", err)
		os.Exit(1)
	}

	// The parent waits for this line before dialling, so a restart is timed
	// from "the port is accepting" rather than from "the process was spawned".
	fmt.Printf("READY %s\n", ln.Addr().String())
	// os.Stdout is an unbuffered file/pipe; Sync on the parent's pipe is invalid.

	srv := &http.Server{Handler: app.Handler()}
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "chaossrv:", err)
		os.Exit(1)
	}
}
