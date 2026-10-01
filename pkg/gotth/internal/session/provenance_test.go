package session_test

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
)

// The provenance properties, as executable specs rather than as prose.
//
// They are checked against the wire capture and the provenance log jointly,
// which is the same join an auditor performs, and they are checked over a run
// that includes suppressed renders, a resync and a server-initiated
// transition — the three cases where a naive implementation loses a row.
var _ = Describe("Provenance", func() {
	var (
		app *testApp
		h   *harness
	)

	BeforeEach(func() {
		app = newTestApp()
		app.events["test.result"] = true
		app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
			s := state.(counterState)
			switch ev.Name {
			case "counter.increment":
				s.N++
			case "counter.relabel":
				s.Label = ev.Fields[0].Value
			case "counter.effect":
				return s, []session.Effect[subject]{app.effect(testEffect{Source: "test.fetch", Reply: "fetched"})}
			case "test.result":
				s.Label = ev.Fields[0].Value
			}
			return s, nil
		}
		app.events["counter.effect"] = true
		app.execute = func(_ context.Context, _ session.Peer[subject], e testEffect, emit session.Emit) error {
			return emit(session.Event{
				Name:   "test.result",
				Fields: []session.Field{{Key: "label", Value: e.Reply}},
			})
		}
		h = newHarness(app, session.DefaultLimits())
		h.start()
		DeferCleanup(h.stop)

		h.sendEvent("counter.increment")
		Eventually(h.sink.patches).Should(HaveLen(1))
		// A relabel to the value already in place: a real transition that
		// produces no patch, which is the row a naive implementation drops.
		h.sendEvent("counter.relabel", &pb.EventField{Key: "label", Value: "hits"})
		h.sendEvent("counter.effect")
		Eventually(h.sink.patches).Should(HaveLen(2))
		h.sendResync(1)
		Eventually(h.sink.snapshots).Should(HaveLen(2))
	})

	// P1. No orphan patches: every sequenced frame names what caused it.
	It("emits no frame without an origin", func() {
		for _, f := range h.sink.all() {
			var origin *pb.Origin
			switch {
			case f.GetPatch() != nil:
				origin = f.GetPatch().GetOrigin()
			case f.GetSnapshot() != nil:
				origin = f.GetSnapshot().GetOrigin()
			default:
				continue
			}
			Expect(origin).NotTo(BeNil())
			Expect(origin.GetSource()).NotTo(BeEmpty())
			Expect(origin.GetKind()).NotTo(Equal(pb.OriginKind_ORIGIN_KIND_UNSPECIFIED))
		}
	})

	// P3. Sequence integrity, per session.
	It("numbers every sequenced frame consecutively from one", func() {
		var seqs []uint64
		for _, f := range h.sink.all() {
			if p := f.GetPatch(); p != nil {
				seqs = append(seqs, p.GetServerSeq())
			}
			if s := f.GetSnapshot(); s != nil {
				seqs = append(seqs, s.GetServerSeq())
			}
		}
		Expect(seqs).NotTo(BeEmpty())
		for i, s := range seqs {
			Expect(s).To(Equal(uint64(i + 1)))
		}
	})

	// P4. Transition semantics, read off the provenance log.
	It("records every transition, including the ones that produced no patch", func() {
		rows := h.logs.provenance()
		Expect(rows).NotTo(BeEmpty())

		var suppressed int
		seen := map[uint64]bool{}
		var lastVersion uint64
		for _, r := range rows {
			id := r["transition_id"].(uint64)
			Expect(seen).NotTo(HaveKey(id), "transition %d was recorded twice", id)
			seen[id] = true

			version := r["state_version"].(uint64)
			Expect(version).To(BeNumerically(">=", lastVersion),
				"the state version went backwards at transition %d", id)
			lastVersion = version

			if r["patch_id"].(uint64) == 0 {
				suppressed++
			}
		}
		Expect(suppressed).To(BeNumerically(">=", 1),
			"a transition that produced no patch left no record, so the state-version property is unverifiable")
	})

	// The two-way completeness check the audit table calls for: neither a
	// missing record nor a phantom one passes.
	It("has a record for every patch, and a patch for every record that names one", func() {
		framePatches := map[uint64]bool{}
		for _, f := range h.sink.all() {
			if p := f.GetPatch(); p != nil {
				framePatches[p.GetPatchId()] = true
			}
			if s := f.GetSnapshot(); s != nil {
				framePatches[s.GetPatchId()] = true
			}
		}

		logged := map[uint64]bool{}
		for _, r := range h.logs.provenance() {
			if id := r["patch_id"].(uint64); id != 0 {
				logged[id] = true
			}
		}

		for id := range framePatches {
			Expect(logged).To(HaveKey(id), "patch %d is on the wire with no provenance record", id)
		}
		for id := range logged {
			Expect(framePatches).To(HaveKey(id), "provenance names patch %d, which was never sent", id)
		}
	})

	// P6. Standalone resolvability: given one patch frame and nothing else,
	// the pair of session and patch identifiers resolves to its transition and
	// either its originating event or its named server-effect source.
	It("resolves a patch captured in isolation, from both arms of the disjunction", func() {
		rows := h.logs.provenance()
		byPatch := map[uint64]map[string]any{}
		for _, r := range rows {
			if id := r["patch_id"].(uint64); id != 0 {
				byPatch[id] = r
			}
		}

		var sawClientEvent, sawServerInitiated bool
		for _, f := range h.sink.all() {
			p := f.GetPatch()
			if p == nil {
				continue
			}
			row, ok := byPatch[p.GetPatchId()]
			Expect(ok).To(BeTrue(), "patch %d did not resolve", p.GetPatchId())
			Expect(row["session_id"]).To(Equal(h.actor.ID().String()))
			Expect(row["transition_id"]).To(Equal(p.GetTransitionId()))
			Expect(row["fragment_ids"]).NotTo(BeNil())

			switch p.GetOrigin().GetKind() {
			case pb.OriginKind_CLIENT_EVENT:
				sawClientEvent = true
				Expect(row["event_id"]).To(Equal(p.GetOrigin().GetEventId()))
				Expect(row["client_ref"]).To(Equal(p.GetOrigin().GetClientRef()))
			default:
				sawServerInitiated = true
				Expect(row["origin_source"]).To(Equal(p.GetOrigin().GetSource()))
				Expect(row["event_id"]).To(BeZero())
			}
		}
		Expect(sawClientEvent).To(BeTrue(), "the client-caused arm was never exercised")
		Expect(sawServerInitiated).To(BeTrue(), "the server-initiated arm was never exercised")
	})

	It("records the supersession range on the resync snapshot and nowhere else", func() {
		var withRange int
		for _, r := range h.logs.provenance() {
			from, ok := r["superseded_from_seq"]
			if !ok {
				continue
			}
			withRange++
			Expect(from).NotTo(BeZero())
			Expect(r["origin_kind"]).To(Equal(pb.OriginKind_RESYNC.String()))
		}
		Expect(withRange).To(Equal(1))
	})
})

var _ = Describe("Goroutine lifecycle", func() {
	// Every goroutine this library starts has an owner, a stop condition and
	// something that waits for it. The assertion is over the process count,
	// because a goroutine with a name and no exit is still a leak.
	It("returns to its baseline after a session opens and closes", func() {
		baseline := stableGoroutines()

		for i := 0; i < 50; i++ {
			app := newTestApp()
			app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
				s := state.(counterState)
				if ev.Name == "counter.increment" {
					s.N++
					return s, []session.Effect[subject]{app.effect(testEffect{Source: "test.noop"})}
				}
				return s, nil
			}
			h := newHarness(app, session.DefaultLimits())
			h.start()
			h.sendEvent("counter.increment")
			Eventually(h.sink.patches).Should(HaveLen(1))
			h.stop()
		}

		Eventually(stableGoroutines, 5*time.Second).Should(BeNumerically("<=", baseline+2),
			"goroutines outlived the sessions that owned them")
	})

	It("abandons an effect that will not return rather than leaking the session with it", func() {
		release := make(chan struct{})
		defer close(release)

		app := newTestApp()
		app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
			return state, []session.Effect[subject]{app.effect(testEffect{Source: "test.hang"})}
		}
		app.execute = func(ctx context.Context, peer session.Peer[subject], effect testEffect, emit session.Emit) error {
			<-release
			return nil
		}
		lim := session.DefaultLimits()
		lim.EffectDrainTimeout = 20 * time.Millisecond

		h := newHarness(app, lim)
		h.start()
		h.sendEvent("counter.increment")
		Eventually(app.executeCount).Should(Equal(1))

		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			h.stop()
			close(done)
		}()

		Eventually(done, 2*time.Second).Should(BeClosed(),
			"shutdown blocked on an effect that will not return")
	})
})

// stableGoroutines lets the scheduler settle before counting, so the figure is
// the one that matters rather than whatever was mid-exit.
//
// CS-9 keep: this is a best-effort quiesce, not an await. It asserts nothing
// and cannot fail — a count that never settles is returned anyway — because
// its caller is an Eventually that polls THIS function, and a fatal failure
// inside a poll would abort the retry that is doing the actual waiting.
func stableGoroutines() int {
	var last int
	for i := 0; i < 20; i++ {
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
		n := runtime.NumGoroutine()
		if n == last {
			return n
		}
		last = n
	}
	return last
}
