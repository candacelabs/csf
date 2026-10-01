package conformance_test

import (
	"fmt"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// ---------------------------------------------------------------------------
// The provenance properties of docs/protocol.md §7, re-implemented here.
//
// These are QA-1's own implementations, written from the specification text
// rather than adapted from internal/session/provenance_test.go. That is the
// point of the duplication: a property checked only by the package that
// implements it is checked by the same understanding that produced the bug.
//
// The mechanism throughout is a join between two artifacts written by
// different code on different paths — the wire capture, produced by the framer,
// and the provenance log, produced by the actor in step (protocol.md §4A.5).
// Agreement between them is evidence; agreement of either with itself is not.
// ---------------------------------------------------------------------------

// carrier is a Patch or a Snapshot reduced to the fields the properties are
// about, so both can be walked by one loop. Every sequenced server→client
// frame is one of the two.
type carrier struct {
	kindName     string
	serverSeq    uint64
	patchID      uint64
	transitionID uint64
	stateVersion uint64
	origin       *pb.Origin
	fragments    []string
	fromSeq      uint64
	throughSeq   uint64
}

// carriers is every sequenced server→client frame captured so far, reduced to
// the fields the properties are about.
func (d *driven) carriers() []carrier {
	frames, _ := d.captured()
	return carriersOf(frames)
}

func carriersOf(frames []*pb.Frame) []carrier {
	var out []carrier
	for _, f := range frames {
		switch {
		case f.GetPatch() != nil:
			p := f.GetPatch()
			out = append(out, carrier{
				kindName: "Patch", serverSeq: p.GetServerSeq(), patchID: p.GetPatchId(),
				transitionID: p.GetTransitionId(), stateVersion: p.GetStateVersion(),
				origin: p.GetOrigin(), fragments: fragmentIDsOf(p.GetUpdates()),
			})
		case f.GetSnapshot() != nil:
			s := f.GetSnapshot()
			out = append(out, carrier{
				kindName: "Snapshot", serverSeq: s.GetServerSeq(), patchID: s.GetPatchId(),
				transitionID: s.GetTransitionId(), stateVersion: s.GetStateVersion(),
				origin: s.GetOrigin(), fragments: fragmentIDsOf(s.GetUpdates()),
				fromSeq: s.GetSupersededFromSeq(), throughSeq: s.GetSupersededThroughSeq(),
			})
		}
	}
	return out
}

func fragmentIDsOf(us []*pb.FragmentUpdate) []string {
	out := make([]string, 0, len(us))
	for _, u := range us {
		out = append(out, u.GetFragmentId())
	}
	return out
}

// mixedRounds drives the mixed interaction log on an already-dialled session,
// starting the mix at phase so a caller can resume it across an interruption.
// The mix is deliberate: increments change state, relabels change a different
// fragment, and no-ops change nothing, so the "state_version rises iff state
// changed" arm of P4 has both cases to check.
func (d *driven) mixedRounds(phase, n int) {
	GinkgoHelper()
	for i := phase; i < phase+n; i++ {
		seq := d.highestSeq()
		switch i % 3 {
		case 0:
			d.event("qa.increment", seq)
			d.nextPatch()
		case 1:
			d.event("qa.relabel", seq, [2]string{"label", fmt.Sprintf("round-%d", i)})
			d.nextPatch()
		case 2:
			// A no-op changes nothing, so nothing is expected back. Waiting for
			// silence is the assertion as much as the synchronisation: a frame
			// arriving here would mean a suppressed render emitted a patch.
			d.event("qa.noop", seq)
			d.drainUntilQuiet(100 * time.Millisecond)
		}
		// Acknowledge as we go, so the outbound window stays open and the run
		// exercises the ordinary path rather than the coalescing one. The
		// coalescing path has its own spec.
		if high := d.highestSeq(); high > 0 {
			d.ack(high)
		}
	}
}

// exercise drives a session through a mixed interaction log and returns it with
// everything captured. It sends no ResyncRequest, so a capture it produces
// contains no RESYNC origin at all: every property that has a resync clause
// takes one of the two builders below instead.
func exercise(rounds int) *driven {
	GinkgoHelper()
	d := dial(nil)
	d.mixedRounds(0, rounds)
	d.drainUntilQuiet(300 * time.Millisecond)
	return d
}

// mountSeq is the sequence number of a session's first Snapshot, and therefore
// the smallest value a ResyncRequest may carry: `last_applied_seq` is refined
// `this > 0`, so a client cannot ask to supersede the mount itself. Resyncing
// from here supersedes everything the session has emitted since.
const mountSeq = 1

// resync asks for a re-render from lastApplied and returns the Snapshot that
// answers it.
func (d *driven) resync(lastApplied uint64) *pb.Snapshot {
	GinkgoHelper()
	Expect(d.writeFrame(d.envelope(&pb.ResyncRequest{
		LastAppliedSeq: lastApplied, Reason: pb.ResyncReason_GAP,
	}))).To(Succeed())

	snap := d.nextSnapshot()
	Expect(snap.GetOrigin().GetKind()).To(Equal(pb.OriginKind_RESYNC),
		"the resync was answered with something other than a resync snapshot")
	return snap
}

// resyncGap opens a gap of unacknowledged patches and closes it with a resync.
//
// The gap is built deliberately rather than hoped for. A ResyncRequest whose
// last_applied_seq already equals the server's is answered with an Ack and
// produces no Snapshot at all (H-14), so a resync sent at a quiescent moment
// would leave the RESYNC arm unexercised — which is the vacuity protocol.md §7
// P2, P5 and P6 were restated to forbid, arriving through the back door.
func (d *driven) resyncGap(patches int) *pb.Snapshot {
	GinkgoHelper()
	applied := d.highestSeq()
	for i := 0; i < patches; i++ {
		d.event("qa.increment", applied)
		d.nextPatch()
	}
	Expect(d.highestSeq()).To(BeNumerically(">", applied),
		"the run emitted nothing to be superseded, so the resync would be a no-op")
	return d.resync(applied)
}

// exerciseWithResync is exercise plus a forced resync, so the capture contains
// both event-bearing origin kinds.
func exerciseWithResync(rounds int) *driven {
	GinkgoHelper()
	d := exercise(rounds)
	d.resyncGap(3)
	d.drainUntilQuiet(300 * time.Millisecond)
	return d
}

// exerciseResyncing is the long-run form: the same mixed log, interrupted by a
// gap and a resync every `every` rounds, so a soak-length capture holds RESYNC
// snapshots throughout it rather than one bolted on at the end.
//
// The cadence is bounded below by H-14's own rate budget — minimum interval one
// second, burst three — so `every` must be large enough that the requests are
// spaced further apart than that. A rate-limited request is answered with an
// Error and no Snapshot, which would hang the run rather than fail it.
func exerciseResyncing(rounds, every int) *driven {
	GinkgoHelper()
	Expect(every).To(BeNumerically(">=", 50),
		"resyncs closer together than this risk H-14's rate budget answering one with an Error")

	d := dial(nil)
	for done := 0; done < rounds; done += every {
		n := every
		if remaining := rounds - done; remaining < n {
			n = remaining
		}
		d.mixedRounds(done, n)
		d.resyncGap(3)
	}
	d.drainUntilQuiet(300 * time.Millisecond)
	return d
}

// floodAndFlush is the coalescing run P5 is stated over: forty transitions that
// are never acknowledged, which drives the outbound window through the coalesce
// stage into degrade, and then one resync.
//
// The resync is not decoration. A full window stops emitting entirely, so the
// run ends holding deferred provenance that never reached the wire — measured
// here, eight identifiers on the wire against twenty-five the log says were
// swallowed. A resync Snapshot renders everything and takes the deferred set
// with it, because the snapshot path folds pending transitions in exactly as
// the patch path does. It is therefore the frame that carries the provenance of
// every event the full window swallowed, and without it P5's set equality is
// not merely unexercised but false.
//
// An acknowledgement would also flush. A resync is used because it is the arm
// C-22 exists to cover, and because it flushes the whole set in one frame.
func floodAndFlush() *driven {
	GinkgoHelper()
	d := dial(nil)
	for i := 0; i < 40; i++ {
		d.event("qa.increment", mountSeq)
	}
	d.drainUntilQuiet(time.Second)

	d.resync(mountSeq)
	d.drainUntilQuiet(400 * time.Millisecond)
	return d
}

// resyncSnapshots is every RESYNC-origin frame in a capture.
//
// Every clause below that is about a resync asserts this is non-empty rather
// than walking an empty set: a property satisfied because its arm never ran is
// satisfied by anything, which is the defect class this suite exists to catch.
func resyncSnapshots(cs []carrier) []carrier {
	var out []carrier
	for _, c := range cs {
		if c.origin.GetKind() == pb.OriginKind_RESYNC {
			out = append(out, c)
		}
	}
	return out
}

// vacuousWithoutResync is the shared half of those three failure messages. It
// is one constant because §6's note fixes the vocabulary in one place and a
// suite that spells the rule three times is a suite where two copies rot.
const vacuousWithoutResync = "the run produced no resync snapshot, so this property's RESYNC arm " +
	"never executed. protocol.md §7 requires that to fail rather than pass vacuously: the resync " +
	"event_id is the identifier cycle 2's B-7 added in order to preserve provenance across a " +
	"resync boundary, and an arm that never runs is satisfied by anything"

var _ = Describe("P1 — no orphan patches", func() {
	It("gives every sequenced frame a non-empty source and a declared kind", func() {
		d := exercise(9)

		cs := d.carriers()
		Expect(cs).NotTo(BeEmpty(), "the run produced nothing to check")

		for _, c := range cs {
			Expect(c.origin).NotTo(BeNil(),
				"%s at server_seq %d carries no origin", c.kindName, c.serverSeq)
			Expect(c.origin.GetSource()).NotTo(BeEmpty(),
				"%s at server_seq %d has an empty origin source", c.kindName, c.serverSeq)
			Expect(c.origin.GetKind()).NotTo(Equal(pb.OriginKind_ORIGIN_KIND_UNSPECIFIED),
				"%s at server_seq %d has an unspecified origin kind", c.kindName, c.serverSeq)
			Expect(c.origin.GetSource()).NotTo(Equal("unknown"),
				"FR-42 forbids unknown as an origin value")
		}
	})
})

// eventBearing is protocol.md §6's vocabulary, in the one place this suite
// spells it. An origin kind is event-bearing when it names an inbound frame of
// this session, and exactly two kinds are.
//
// It is a function rather than an inline comparison in each spec because §6
// says adding a third kind must move H-6, P2, P6 and the implementation's own
// eventBearing together — and a suite that spells the set twice is a suite
// where half of it gets moved.
func eventBearing(kind pb.OriginKind) bool {
	return kind == pb.OriginKind_CLIENT_EVENT || kind == pb.OriginKind_RESYNC
}

var _ = Describe("P2 — chain closure", func() {
	// The property: a frame that says a client frame caused it must name one
	// this session actually received, and must agree with that frame's own
	// correlation handle. This is the join FR-41 is really about.
	//
	// Both event-bearing kinds are in scope, and the RESYNC arm is the reason
	// this spec was rewritten. Cycle 2's B-7 gave a resync Snapshot the
	// identifiers of the ResyncRequest that caused it, precisely so that the
	// provenance survives a resync boundary — and this property, the only one
	// that joins such an identifier back to an inbound frame, skipped every
	// kind but CLIENT_EVENT. The identifier added to preserve provenance was
	// the one identifier no conformance property checked.
	It("resolves every event-bearing frame to the inbound frame it names", func() {
		d := exerciseWithResync(9)

		rows := d.logs.provenance()
		Expect(rows).NotTo(BeEmpty(), "the provenance log is empty: the reverse index does not exist")

		// The identifiers the server minted, from the log: what each resolves
		// to, and under which origin kind the server recorded it.
		mintedRef := map[uint64]uint64{}
		mintedKind := map[uint64]string{}
		for _, r := range rows {
			if r.EventID != 0 {
				mintedRef[r.EventID] = r.ClientRef
				mintedKind[r.EventID] = r.OriginKind
			}
		}

		seen := map[pb.OriginKind]int{}
		for _, c := range d.carriers() {
			if !eventBearing(c.origin.GetKind()) {
				continue
			}
			seen[c.origin.GetKind()]++

			Expect(c.origin.GetEventId()).NotTo(BeZero(),
				"%s at server_seq %d has an event-bearing origin %s and no event_id",
				c.kindName, c.serverSeq, c.origin.GetKind())

			ref, ok := mintedRef[c.origin.GetEventId()]
			Expect(ok).To(BeTrue(),
				"%s at server_seq %d names event_id %d, which this session never received",
				c.kindName, c.serverSeq, c.origin.GetEventId())
			Expect(c.origin.GetClientRef()).To(Equal(ref),
				"%s at server_seq %d reports client_ref %d for event %d, but the session recorded %d",
				c.kindName, c.serverSeq, c.origin.GetClientRef(), c.origin.GetEventId(), ref)

			// What the identifier resolves to, which is the half of P2 that
			// distinguishes the two arms: an Event for CLIENT_EVENT, the
			// ResyncRequest for RESYNC. The wire and the log are written by
			// different code, so their agreement about which is evidence.
			Expect(mintedKind[c.origin.GetEventId()]).To(Equal(c.origin.GetKind().String()),
				"%s at server_seq %d carries origin %s, but the log records event_id %d under %s",
				c.kindName, c.serverSeq, c.origin.GetKind(),
				c.origin.GetEventId(), mintedKind[c.origin.GetEventId()])
		}

		Expect(seen[pb.OriginKind_CLIENT_EVENT]).To(BeNumerically(">", 0),
			"the run produced no client-caused patch, so the first arm was not exercised")
		Expect(seen[pb.OriginKind_RESYNC]).To(BeNumerically(">", 0),
			"the run produced no resync snapshot, so the second arm was not exercised. "+
				"protocol.md §7 P2 requires this to fail rather than pass vacuously: the resync "+
				"event_id is the identifier B-7 added in order to preserve provenance, and this "+
				"is the property that checks it")
	})

	// H-6 in its wire-observable form. A server-initiated frame must not carry
	// borrowed causal identifiers, and a frame naming a client frame must not
	// omit them.
	//
	// It runs over a capture containing a resync so that the RESYNC arm is a
	// real observation rather than a branch nothing reaches: without one, every
	// frame in the run takes the else arm and the spec asserts half of H-6.
	It("keeps the causal identifiers set exactly on the kinds that name a client frame", func() {
		d := exerciseWithResync(6)

		var bearing int
		for _, c := range d.carriers() {
			if eventBearing(c.origin.GetKind()) {
				bearing++
				Expect(c.origin.GetEventId()).NotTo(BeZero(),
					"%s origin %s has a zero event_id", c.kindName, c.origin.GetKind())
				Expect(c.origin.GetClientRef()).NotTo(BeZero(),
					"%s origin %s has a zero client_ref", c.kindName, c.origin.GetKind())
			} else {
				Expect(c.origin.GetEventId()).To(BeZero(),
					"%s origin %s carries event_id %d, which it cannot have caused",
					c.kindName, c.origin.GetKind(), c.origin.GetEventId())
				Expect(c.origin.GetClientRef()).To(BeZero(),
					"%s origin %s carries client_ref %d", c.kindName, c.origin.GetKind(), c.origin.GetClientRef())
			}
		}
		Expect(bearing).To(BeNumerically(">", 0),
			"nothing in the run took the event-bearing arm, so only half of H-6 was checked")
	})
})

var _ = Describe("P3 — sequence integrity", func() {
	It("starts at one and increases by exactly one across every sequenced frame", func() {
		d := exercise(9)

		cs := d.carriers()
		Expect(cs).NotTo(BeEmpty())
		Expect(cs[0].serverSeq).To(Equal(uint64(1)), "the first sequenced frame is not server_seq 1")

		for i := 1; i < len(cs); i++ {
			Expect(cs[i].serverSeq).To(Equal(cs[i-1].serverSeq+1),
				"sequence jumped from %d to %d at frame %d (%s): a gap means a second write path or a lost frame",
				cs[i-1].serverSeq, cs[i].serverSeq, i, cs[i].kindName)
		}
	})

	It("mints a patch identifier once and never reuses it", func() {
		d := exercise(9)

		seen := map[uint64]int{}
		for _, c := range d.carriers() {
			Expect(c.patchID).NotTo(BeZero(), "%s at server_seq %d has no patch_id", c.kindName, c.serverSeq)
			seen[c.patchID]++
			Expect(seen[c.patchID]).To(Equal(1), "patch_id %d was emitted twice", c.patchID)
		}
	})
})

var _ = Describe("P4 — transition semantics", func() {
	// This is the one property the wire alone cannot answer, because a
	// transition that changed nothing emits no frame. It is checked against the
	// provenance log, which records the suppressed transitions precisely so the
	// property is checkable at all.
	It("makes transition identifiers unique and the state version rise only on a change", func() {
		d := exercise(9)

		rows := d.logs.provenance()
		Expect(len(rows)).To(BeNumerically(">=", 4), "too few transitions to say anything")

		seenTransition := map[uint64]bool{}
		for i, r := range rows {
			Expect(seenTransition[r.TransitionID]).To(BeFalse(),
				"transition_id %d appears twice (row %d)", r.TransitionID, i)
			seenTransition[r.TransitionID] = true

			if i == 0 {
				continue
			}
			Expect(r.StateVersion).To(BeNumerically(">=", rows[i-1].StateVersion),
				"state_version fell from %d to %d at row %d",
				rows[i-1].StateVersion, r.StateVersion, i)
		}
	})

	It("holds the state version still across a transition that changed nothing", func() {
		d := dial(nil)

		d.event("qa.increment", d.highestSeq())
		d.nextPatch()
		d.drainUntilQuiet(300 * time.Millisecond)
		before := lastRow(d).StateVersion

		d.event("qa.noop", d.highestSeq())
		d.drainUntilQuiet(500 * time.Millisecond)
		after := lastRow(d)

		Expect(after.StateVersion).To(Equal(before),
			"a no-op transition moved the state version from %d to %d", before, after.StateVersion)
		Expect(after.TransitionID).To(BeNumerically(">", 0))
		Expect(after.PatchID).To(BeZero(),
			"a transition that changed nothing emitted a patch anyway")
	})
})

var _ = Describe("P6 — standalone resolvability (FR-41)", func() {
	// The requirement is deliberately hostile to a design argument: given ONLY
	// the bytes of one patch frame, and nothing else from the session, resolve
	// the originating event, the transition, and the render. The spec below
	// therefore re-parses the frame from its captured bytes and uses nothing
	// from the harness except the operator's log index.
	It("resolves an arbitrary captured patch from its bytes alone", func() {
		d := exercise(9)

		frames, raw := d.captured()
		var patchBytes []byte
		for i, f := range frames {
			if f.GetPatch() != nil && f.GetPatch().GetOrigin().GetKind() == pb.OriginKind_CLIENT_EVENT {
				patchBytes = raw[i]
				break
			}
		}
		Expect(patchBytes).NotTo(BeEmpty(), "the run produced no client-caused patch to resolve")

		// Everything below this line has only these bytes and the log.
		var isolated pb.Frame
		Expect(proto.Unmarshal(patchBytes, &isolated)).To(Succeed(),
			"a captured frame did not parse as a Frame")

		sessionID := fmt.Sprintf("%x", isolated.GetSessionId())
		patch := isolated.GetPatch()
		Expect(patch).NotTo(BeNil())

		row, found := resolve(d.logs.provenance(), sessionID, patch.GetPatchId())

		Expect(found).To(BeTrue(),
			"patch_id %d in session %s does not resolve to a transition", patch.GetPatchId(), sessionID)
		Expect(row.TransitionID).To(Equal(patch.GetTransitionId()),
			"the log and the frame disagree about which transition produced this patch")
		Expect(row.StateVersion).To(Equal(patch.GetStateVersion()),
			"the log and the frame disagree about the state version")
		Expect(row.EventID).To(Equal(patch.GetOrigin().GetEventId()),
			"the log and the frame disagree about the originating event")
		Expect(row.EventID).NotTo(BeZero())
		Expect(row.FragmentIDs).To(Equal(fragmentIDsOf(patch.GetUpdates())),
			"the log does not name the fragments the frame carries, so the render is not resolvable")
	})

	// The second arm of PRD G4's disjunction. A server-initiated patch has no
	// originating event by design, and must resolve to a named effect source
	// instead. The mount snapshot is the one every session has.
	It("resolves a server-initiated frame to its named source rather than to an event", func() {
		d := dial(nil)
		d.drainUntilQuiet(300 * time.Millisecond)

		frames, raw := d.captured()
		var snapBytes []byte
		for i, f := range frames {
			if f.GetSnapshot() != nil {
				snapBytes = raw[i]
				break
			}
		}
		Expect(snapBytes).NotTo(BeEmpty())

		var isolated pb.Frame
		Expect(proto.Unmarshal(snapBytes, &isolated)).To(Succeed())
		snap := isolated.GetSnapshot()

		Expect(snap.GetOrigin().GetKind()).To(Equal(pb.OriginKind_MOUNT))
		Expect(snap.GetOrigin().GetSource()).To(Equal("mount"))
		Expect(snap.GetOrigin().GetEventId()).To(BeZero(),
			"a mount snapshot has no originating event, by design")

		row, found := resolve(d.logs.provenance(),
			fmt.Sprintf("%x", isolated.GetSessionId()), snap.GetPatchId())

		Expect(found).To(BeTrue(), "the mount snapshot does not resolve")
		Expect(row.OriginSource).To(Equal("mount"))
		Expect(row.OriginSource).NotTo(BeEmpty(), "G4 forbids an unresolvable origin")
	})

	// The resync arm, which §7 P6 requires to be in the capture by name: "a
	// RESYNC Snapshot resolves through the first arm and must be in the
	// capture, or the resync half of P6 is untested".
	//
	// It is the interesting case of G4's disjunction rather than a third one. A
	// resync Snapshot is server-sent, so the shape of the frame says
	// server-initiated, and yet it resolves through the arm reserved for frames
	// a client caused — because a ResyncRequest is a nameable client frame and
	// §4.2 keeps the identifiers it already had. An analyst holding only these
	// bytes must be able to cross the resync boundary, which is the whole point
	// of §4.3, and this is the spec that says so from the bytes alone.
	It("resolves a resync snapshot from its bytes alone, through its originating event", func() {
		d := exerciseWithResync(6)

		frames, raw := d.captured()
		var snapBytes []byte
		for i, f := range frames {
			if s := f.GetSnapshot(); s != nil && s.GetOrigin().GetKind() == pb.OriginKind_RESYNC {
				snapBytes = raw[i]
				break
			}
		}
		Expect(snapBytes).NotTo(BeEmpty(), vacuousWithoutResync)

		// Everything below this line has only these bytes and the log.
		var isolated pb.Frame
		Expect(proto.Unmarshal(snapBytes, &isolated)).To(Succeed(),
			"a captured frame did not parse as a Frame")

		sessionID := fmt.Sprintf("%x", isolated.GetSessionId())
		snap := isolated.GetSnapshot()
		Expect(snap).NotTo(BeNil())

		row, found := resolve(d.logs.provenance(), sessionID, snap.GetPatchId())

		Expect(found).To(BeTrue(),
			"patch_id %d in session %s does not resolve to a transition", snap.GetPatchId(), sessionID)
		Expect(row.TransitionID).To(Equal(snap.GetTransitionId()),
			"the log and the frame disagree about which transition produced this snapshot")
		Expect(row.StateVersion).To(Equal(snap.GetStateVersion()),
			"the log and the frame disagree about the state version")

		// The first arm of the disjunction, which is what distinguishes this
		// spec from the mount one below.
		Expect(snap.GetOrigin().GetEventId()).NotTo(BeZero(),
			"a resync snapshot names the ResyncRequest that caused it (§4.2, H-6) and this one carries no event_id")
		Expect(row.EventID).To(Equal(snap.GetOrigin().GetEventId()),
			"the log and the frame disagree about the originating event")
		Expect(row.ClientRef).To(Equal(snap.GetOrigin().GetClientRef()),
			"the log and the frame disagree about the client's correlation handle")
		Expect(row.OriginSource).To(Equal("resync"),
			"a resync snapshot resolves to origin source %q", row.OriginSource)
		Expect(row.FragmentIDs).To(Equal(fragmentIDsOf(snap.GetUpdates())),
			"the log does not name the fragments the frame carries, so the render is not resolvable")

		// §4.3's supersession edge, read off the same bytes: without it the
		// events behind the markup the user is now looking at are unreachable
		// across the boundary, which is what makes this snapshot's provenance
		// worth having at all.
		Expect(snap.GetSupersededFromSeq()).NotTo(BeZero(),
			"a resync snapshot replaces a range and must say which (H-13)")
		Expect(row.FromSeq).To(Equal(snap.GetSupersededFromSeq()),
			"the log and the frame disagree about where the superseded range begins")
		Expect(row.ThroughSeq).To(Equal(snap.GetSupersededThroughSeq()),
			"the log and the frame disagree about where the superseded range ends")
	})

	// The totality claim, at the scale a default `go test` can afford. The
	// soak-class spec below runs the same assertion over a long run.
	//
	// Both run over a capture that contains a resync, because "every sequenced
	// frame resolves" said of a run with no RESYNC frame in it is a weaker
	// sentence than it looks: the kind whose resolvability is least obvious is
	// then the kind that was not there.
	It("resolves every sequenced frame in the run, with zero unknown", func() {
		d := exerciseWithResync(12)

		rows := d.logs.provenance()
		cs := d.carriers()
		var unresolved []uint64
		for _, c := range cs {
			if _, ok := resolve(rows, fmt.Sprintf("%x", d.sessionID), c.patchID); !ok {
				unresolved = append(unresolved, c.patchID)
			}
		}

		Expect(unresolved).To(BeEmpty(),
			"%d of %d frames did not resolve: %v", len(unresolved), len(cs), unresolved)
		Expect(resyncSnapshots(cs)).NotTo(BeEmpty(), vacuousWithoutResync)
	})

	It("resolves every sequenced frame over a long run", Label("soak"), func() {
		soakOnly()
		d := exerciseResyncing(400, 100)

		rows := d.logs.provenance()
		cs := d.carriers()
		Expect(len(cs)).To(BeNumerically(">", 100), "the soak did not produce enough frames")

		var unresolved []uint64
		for _, c := range cs {
			if _, ok := resolve(rows, fmt.Sprintf("%x", d.sessionID), c.patchID); !ok {
				unresolved = append(unresolved, c.patchID)
			}
		}
		Expect(unresolved).To(BeEmpty(), "%d frames did not resolve", len(unresolved))

		// The soak is the run that would hide a rare unresolvable kind, so it
		// is the run where an absent arm matters most. Four resyncs is what a
		// cadence of one per hundred rounds produces over four hundred.
		Expect(len(resyncSnapshots(cs))).To(BeNumerically(">=", 4),
			"%s — this run held %d of them", vacuousWithoutResync, len(resyncSnapshots(cs)))
	})
})

var _ = Describe("P7 — ack closure with a causal edge", func() {
	// Every emitted sequence number must be acknowledged, superseded by a
	// resync snapshot's range, or accounted for by the close. A sequence that
	// is none of the three was emitted into the void.
	It("accounts for every emitted sequence number", func() {
		d := exercise(9)

		cs := d.carriers()
		highest := cs[len(cs)-1].serverSeq

		// Acknowledge everything, then confirm the server accepted it: an ack
		// the window refuses closes the connection, so a clean read afterwards
		// is the assertion.
		d.ack(highest)
		d.drainUntilQuiet(400 * time.Millisecond)

		superseded := map[uint64]bool{}
		for _, c := range cs {
			if c.fromSeq == 0 {
				continue
			}
			for s := c.fromSeq; s <= c.throughSeq; s++ {
				superseded[s] = true
			}
		}

		for _, c := range cs {
			accounted := c.serverSeq <= highest || superseded[c.serverSeq]
			Expect(accounted).To(BeTrue(),
				"server_seq %d was emitted and is neither acknowledged nor superseded", c.serverSeq)
		}
	})

	// H-13's wire form, and the property that makes a resync boundary
	// traversable: the range a resync snapshot replaces must be contiguous with
	// what the client said it had, and must end before the snapshot itself.
	It("closes the gap exactly across a resync boundary", func() {
		d := dial(nil)

		// Build a gap: emit patches without acknowledging, then ask for a
		// resync from an earlier point.
		for i := 0; i < 3; i++ {
			d.event("qa.increment", d.highestSeq())
			d.nextPatch()
		}
		before := d.highestSeq()
		Expect(before).To(BeNumerically(">=", 4))

		Expect(d.writeFrame(d.envelope(&pb.ResyncRequest{
			LastAppliedSeq: 2, Reason: pb.ResyncReason_GAP,
		}))).To(Succeed())

		snap := d.nextSnapshot()

		Expect(snap.GetOrigin().GetKind()).To(Equal(pb.OriginKind_RESYNC))
		Expect(snap.GetOrigin().GetEventId()).NotTo(BeZero(),
			"a resync is caused by a nameable client frame and must carry its event id")
		Expect(snap.GetSupersededFromSeq()).To(Equal(uint64(3)),
			"the superseded range must begin one past what the client applied")
		Expect(snap.GetSupersededThroughSeq()).To(Equal(before))
		Expect(snap.GetSupersededThroughSeq()).To(BeNumerically("<", snap.GetServerSeq()),
			"H-13: the superseded range must end before the snapshot that replaces it")
	})

	It("answers a resync that describes no gap with an ack rather than a re-render", func() {
		d := dial(nil)
		d.event("qa.increment", d.highestSeq())
		d.nextPatch()

		high := d.highestSeq()
		snapshotsBefore := len(d.snapshots())

		Expect(d.writeFrame(d.envelope(&pb.ResyncRequest{
			LastAppliedSeq: high, Reason: pb.ResyncReason_CLIENT_REQUEST,
		}))).To(Succeed())

		f := d.until(func(f *pb.Frame) bool { return f.GetAck() != nil || f.GetSnapshot() != nil })

		Expect(f.GetAck()).NotTo(BeNil(),
			"a resync describing no gap must be answered with an Ack, not a full snapshot")
		Expect(f.GetAck().GetServerSeq()).To(Equal(high))
		Expect(d.snapshots()).To(HaveLen(snapshotsBefore))
	})
})

var _ = Describe("P8 — the single write path", func() {
	// The framer is meant to be the only thing that writes. The independent
	// check available to a test is a two-way bijection between the sequenced
	// frames in the capture and the provenance rows that claim to have emitted
	// one, because those are written by different code on different paths.
	It("puts exactly the frames on the wire that the actor recorded emitting", func() {
		d := exercise(9)

		onWire := map[uint64]bool{}
		for _, c := range d.carriers() {
			onWire[c.patchID] = true
		}

		inLog := map[uint64]bool{}
		for _, r := range d.logs.provenance() {
			if r.PatchID != 0 {
				inLog[r.PatchID] = true
			}
		}

		for id := range onWire {
			Expect(inLog).To(HaveKey(id),
				"patch_id %d reached the wire with no transition recording it: a second write path exists", id)
		}
		for id := range inLog {
			Expect(onWire).To(HaveKey(id),
				"the actor recorded emitting patch_id %d and it never reached the wire", id)
		}
		Expect(onWire).NotTo(BeEmpty())
	})
})

var _ = Describe("P5 — coalescing preserves provenance", func() {
	// H-4 calls the contributing-event bound a flush trigger and never a
	// truncation, and FR-43 forbids losing provenance to save bytes. The
	// observable form: when the window fills and patches are collapsed, the
	// patch that finally goes out must name the events that were folded into
	// it.
	It("names every folded event on the patch that carries them", func() {
		d := floodAndFlush()

		var coalesced *pb.Origin
		for _, c := range d.carriers() {
			if len(c.origin.GetContributingEventIds()) > 0 {
				coalesced = c.origin
				break
			}
		}

		// This was a Skip, and a Skip is a pass. The run is deterministic —
		// forty unacknowledged transitions against an ack window of sixteen
		// reach the coalesce stage every time — so nothing being folded is a
		// change in the backpressure ladder, which is a thing this property
		// should say out loud rather than step around.
		Expect(coalesced).NotTo(BeNil(),
			"the run coalesced nothing at all, so this property was checked over no frame: "+
				"either the outbound window no longer fills at forty unacknowledged transitions "+
				"or the coalesce stage stopped folding provenance forward")

		Expect(len(coalesced.GetContributingEventIds())).To(BeNumerically("<=", 1024),
			"H-4: the contributing union must be flushed before its schema ceiling, never truncated at it")
		Expect(coalesced.GetSource()).NotTo(BeEmpty())
	})

	// §7 P5 in full: "the union of those ids over a run equals the set of
	// events that produced a state change and were not individually patched",
	// as set equality and not sampling.
	//
	// The two sides are computed from artifacts written by different code on
	// different paths — the union from the wire capture, which the framer
	// produced, and the set from the provenance log, which the actor produced
	// in step. Agreement between them is evidence; agreement of either with
	// itself is not.
	It("puts exactly the events the window swallowed onto the frames that flush them", func() {
		d := floodAndFlush()
		cs := d.carriers()

		counts := contributingCounts(cs)
		swallowed := swallowedEvents(d.logs.provenance())

		Expect(swallowed).NotTo(BeEmpty(),
			"the log records no swallowed event, so the run never deferred a transition and "+
				"P5's right-hand side is empty")

		var twice []uint64
		onWire := make([]uint64, 0, len(counts))
		for id, n := range counts {
			onWire = append(onWire, id)
			if n > 1 {
				twice = append(twice, id)
			}
		}
		Expect(twice).To(BeEmpty(),
			"event(s) %v are named as contributing on more than one frame: an event is either the "+
				"cause of a patch or a contributor to one, and counting it twice makes the union "+
				"larger than the set it must equal", twice)

		Expect(onWire).To(ConsistOf(swallowed),
			"the union on the wire is %v and the log says the swallowed set is %v: "+
				"P5 is set equality, so a difference either way is provenance created or lost",
			sorted(onWire), sorted(swallowed))
	})

	// The clause C-22 exists for. The union above is only complete because a
	// RESYNC snapshot carried the deferred set out; without it the run ends
	// with provenance the framer never emitted, and P5's equality is false
	// rather than merely unexercised.
	It("flushes the events a full window swallowed onto the resync snapshot", func() {
		d := floodAndFlush()
		cs := d.carriers()

		flushes := resyncSnapshots(cs)
		Expect(flushes).NotTo(BeEmpty(), vacuousWithoutResync)

		var flushed []uint64
		for _, f := range flushes {
			flushed = append(flushed, f.origin.GetContributingEventIds()...)
		}
		Expect(flushed).NotTo(BeEmpty(),
			"the resync snapshot named no contributing event, so the transitions the full window "+
				"swallowed reached the wire on no frame at all: H-4 calls the bound a flush "+
				"trigger and FR-43 forbids losing provenance, and a resync that renders "+
				"everything must carry what it renders")

		// Everything the resync flushed was swallowed, and everything the
		// resync did not flush was carried by a coalesced patch instead. The
		// second half is what stops this passing on a snapshot that named one
		// identifier and dropped the rest.
		swallowed := swallowedEvents(d.logs.provenance())
		Expect(swallowed).To(ContainElements(flushed),
			"the resync snapshot named %v as contributing, which the log does not record as "+
				"swallowed transitions of this session", sorted(flushed))
		Expect(len(flushed)).To(BeNumerically("<", len(swallowed)),
			"the resync flushed the entire swallowed set, which means the coalesce stage folded "+
				"nothing forward and only the degrade stage was exercised")
	})

	// D-14's pending spec is no longer here. It was pre-registered as a PIt
	// by QA-1 — "holds P5 under every CoalesceFlushAt an application is
	// allowed to set" — with the note "un-pend this when either live.New
	// rejects a CoalesceFlushAt above the ceiling or Normalize clamps it to
	// one". New now rejects, so the requirement runs rather than pends, in
	// limits_test.go's "The coalescing flush trigger an application
	// configures": one spec holds P5 at the largest legal setting, and one
	// holds that an illegal one never reaches a session. It lives there
	// because the property is now about the configuration boundary as much as
	// about provenance, and limits_test.go is where this suite keeps what an
	// application is allowed to ask for.
})

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// resolve is the operator's reverse lookup: (session_id, patch_id) → the
// transition that produced it. It is deliberately written as a scan over the
// log rather than against any library index, because an operator holding a log
// stream has exactly this.
func resolve(rows []provRecord, sessionID string, patchID uint64) (provRecord, bool) {
	for _, r := range rows {
		if r.SessionID == sessionID && r.PatchID == patchID {
			return r, true
		}
	}
	return provRecord{}, false
}

// contributingCounts is P5's left-hand side, taken from the wire alone: how
// many frames named each contributing event. It counts rather than unions so
// that the spec can tell an event named twice from an event named once, which
// unionEdges' own comment calls the direction that is hardest to notice.
func contributingCounts(cs []carrier) map[uint64]int {
	counts := map[uint64]int{}
	for _, c := range cs {
		for _, id := range c.origin.GetContributingEventIds() {
			counts[id]++
		}
	}
	return counts
}

// swallowedEvents is P5's right-hand side, taken from the provenance log alone:
// the events that produced a state change and never got a patch of their own.
//
// "Produced a state change" is read off the state version, which §4.1 says
// rises iff the transition changed state — so the rise, and not the reducer's
// intent, is what the log makes checkable. "Not individually patched" is the
// absence of any row for that event carrying a patch identifier: a transition
// that was deferred and later flushed as the proximate cause of a patch has two
// rows, and the second one is its own patch, so it was not swallowed.
func swallowedEvents(rows []provRecord) []uint64 {
	changed := map[uint64]bool{}
	patched := map[uint64]bool{}
	var order []uint64

	var previousVersion uint64
	for _, r := range rows {
		if r.PatchID != 0 {
			patched[r.EventID] = true
		}
		// The mount and the library's own synthesized transitions carry event
		// id 0, which names no inbound frame and belongs in neither set.
		if r.EventID != 0 && r.StateVersion > previousVersion && !changed[r.EventID] {
			changed[r.EventID] = true
			order = append(order, r.EventID)
		}
		previousVersion = r.StateVersion
	}

	out := make([]uint64, 0, len(order))
	for _, id := range order {
		if !patched[id] {
			out = append(out, id)
		}
	}
	return out
}

// sorted is for failure messages only: an unordered difference between two
// sets is unreadable, and a reader comparing them by eye should not have to.
func sorted(ids []uint64) []uint64 {
	out := append([]uint64(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func lastRow(d *driven) provRecord {
	GinkgoHelper()
	rows := d.logs.provenance()
	Expect(rows).NotTo(BeEmpty())
	return rows[len(rows)-1]
}
