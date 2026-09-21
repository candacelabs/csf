package discovery

import (
	"context"
	"time"

	"github.com/candacelabs/csf/services/warden"
)

// errGate rate-limits repeated identical errors so a source logs once per
// distinct error transition rather than on every poll. Call shouldLog(err) on
// each failed attempt: it reports true only when the error text differs from
// the last logged one. Call clear() after a success so the next error (even the
// same message) is treated as a fresh transition and logs again.
type errGate struct{ last string }

func (g *errGate) shouldLog(err error) bool {
	k := err.Error()
	if k == g.last {
		return false
	}
	g.last = k
	return true
}

func (g *errGate) clear() { g.last = "" }

// nodesEqual reports whether two rosters describe the same members. Both slices
// are expected to be sorted by ID (sortNodes); comparison is element-wise over
// ID and Addr so a changed address counts as a change.
func nodesEqual(a, b []warden.Node) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Addr != b[i].Addr {
			return false
		}
	}
	return true
}

// copyNodes returns a defensive copy so a snapshot handed to a consumer never
// aliases the source's internal slice.
func copyNodes(nodes []warden.Node) []warden.Node {
	return append([]warden.Node(nil), nodes...)
}

// pollLoop drives a change-only discovery source. It polls fetch immediately,
// then every interval, until ctx ends. fetch returns the current roster nodes
// (already sorted) or an error. On error pollLoop logs via logErr — but only
// once per distinct error transition — and sends nothing (consumers keep their
// last roster). It sends the first successful snapshot and thereafter only when
// the node set changes. It closes ch and returns when ctx ends. interval must
// be > 0 (callers default it).
func pollLoop(
	ctx context.Context,
	ch chan<- warden.Roster,
	interval time.Duration,
	fetch func(ctx context.Context) ([]warden.Node, error),
	logErr func(err error),
) {
	defer close(ch)

	var (
		haveSent bool
		last     []warden.Node
		gate     errGate
	)

	poll := func() {
		nodes, err := fetch(ctx)
		if err != nil {
			if gate.shouldLog(err) && logErr != nil {
				logErr(err)
			}
			return
		}
		gate.clear()
		if haveSent && nodesEqual(last, nodes) {
			return // change-only: no duplicate snapshot
		}
		select {
		case ch <- warden.Roster{Nodes: copyNodes(nodes)}:
			last = nodes
			haveSent = true
		case <-ctx.Done():
		}
	}

	poll()
	tk := time.NewTicker(interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			poll()
		}
	}
}
