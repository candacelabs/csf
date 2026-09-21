package main

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

// tabA, tabB and tabC stand for three sessions. A session identifier is
// sixteen bytes the server mints; these are fixed so a spec can assert on
// "you" versus "somebody else" without a running server.
//
// Two of them belong to one member in several specs below, which is the case
// Limits.MaxSessionsPerIdentity exists for and the reason
// livetest.NewSession takes an identifier and an identity separately rather
// than deriving one from the other.
var (
	tabA = live.ID{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf}
	tabB = live.ID{0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf}
	tabC = live.ID{0xc0, 0xc1, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xcb, 0xcc, 0xcd, 0xce, 0xcf}
)

// The cast, by role.
var (
	alice   = Member{Name: "alice", Role: RoleMember}
	bob     = Member{Name: "bob", Role: RoleMember}
	mallory = Member{Name: "mallory", Role: RoleModerator}
	olive   = Member{Name: "olive", Role: RoleObserver}
	trudy   = Member{Name: "trudy", Role: RoleBanned}
)

// baseTime is the wall clock the specs use. Nothing under test reads a clock,
// so it is a constant rather than a fixture.
var baseTime = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

const testOrigin = "http://127.0.0.1:8081"

// sessionFor builds the live.Session[Member] a Config hook is called with.
//
// This is what livetest.NewSession is for: Init, Authorize, Teardown and
// Execute all take a Session, whose fields are unexported because identity is
// bound at the handshake and nothing downstream may mint one. Without the
// constructor an application can build a Session whose Identity() is nil and
// cannot build a useful one, so every hook that reads an identity — which is
// every hook this example has — would be reachable only through a running
// server.
func sessionFor(id live.ID, member Member) live.Session[Member] {
	GinkgoHelper()
	return livetest.NewSession(GinkgoTB(), id, member)
}

// reduce is the reducer under test, bound to a room the pure specs never reach:
// the transition builds effects that close over it, and a spec that cares about
// what an effect DOES builds its own room and runs the effect.
var reduce = Reducer(NewRoom())

// memberSession is sessionFor under the name the effect specs read better
// with: an effect's Run is handed the session it acts for, and several specs
// below run one effect for two different members to show that the author comes
// from there rather than from the reducer.
func memberSession(id live.ID, member Member) live.Session[Member] { return sessionFor(id, member) }

// noEmit is the Emitter handed to an effect whose emissions a spec does not
// read.
func noEmit(live.Event) error { return nil }

// sources projects what a transition scheduled into the one thing a
// specification can compare: live.Effect[Member] carries its behaviour in a function
// field, and Go cannot compare two function values.
func sources(effects []live.Effect[Member]) []string {
	if len(effects) == 0 {
		// nil rather than an empty slice, so "scheduled nothing" compares equal
		// to the nil a reducer returns for it.
		return nil
	}
	names := make([]string, 0, len(effects))
	for _, effect := range effects {
		names = append(names, effect.Source)
	}
	return names
}

func newState(id live.ID, member Member) State {
	return State{Self: id, Me: member.Name, Role: member.Role, Room: &Log{}}
}

// submit is the event the client runtime sends when the composer's form is
// submitted.
func submit(body string, id uint64, at time.Time) live.Event {
	return live.Event{
		Name:       EventSend,
		FragmentID: FragmentComposer,
		ID:         id,
		At:         at,
		Fields:     live.NewFields(map[string]string{fieldBody: body}),
	}
}

// typed is the debounced per-field change event.
func typed(body string, id uint64, at time.Time) live.Event {
	return live.Event{
		Name:       EventDraft,
		FragmentID: FragmentComposer,
		ID:         id,
		At:         at,
		Fields:     live.NewFields(map[string]string{fieldBody: body}),
	}
}

// cleared is the event Escape raises on the composer.
//
// It carries the body field the runtime serializes off the form like every
// other composer event, and the reducer deliberately ignores it: "clear" means
// the empty string whatever the box happened to contain when the key was
// pressed.
func cleared(body string, id uint64, at time.Time) live.Event {
	return live.Event{
		Name:       EventClear,
		FragmentID: FragmentComposer,
		ID:         id,
		At:         at,
		Fields:     live.NewFields(map[string]string{fieldBody: body}),
	}
}

// pushed is an event the room's subscription pump emits into one session.
//
// It is built by the room's own update.event, not by a parallel copy of it in
// the test file, so the field names the pump writes and the field names the
// reducer reads are held together by the code rather than by two people
// remembering the same string.
func pushed(u update, to live.ID, at time.Time) live.Event {
	ev := u.event(to)
	ev.At = at
	return ev
}

func postedUpdate(seq, version uint64, author, body string, members []string) update {
	return update{
		kind: EventPosted, version: version, members: members,
		msg: Message{Seq: seq, Author: author, Body: body, AtUnixMilli: baseTime.UnixMilli()},
	}
}

// ---------------------------------------------------------------------------

// The names on the wire are pinned to literals here, once.
//
// L9-1's checkpoint-2 batch §8 names the trap this closes: a spec that builds
// its input from the same constant the code under test matches on is testing
// the branch and not the name. Every other spec in this file does exactly that
// — it must, or it would be a spelling test in disguise at every call site —
// so the names themselves are held in one place instead.
//
// They are worth holding. A fragment identifier appears in the Config, in the
// markup's data-gotth-region attribute and in every patch frame; an event name
// appears in Config.Events, in the markup's data-gotth-on attribute and on the
// wire. Renaming one is a client-visible change and this is where it announces
// itself.
var _ = Describe("The names this application puts on the wire", func() {
	It("spells the fragment identifiers the way the markup and the patches do", func() {
		Expect(FragmentLog).To(Equal("chat.log"))
		Expect(FragmentComposer).To(Equal("chat.composer"))
		Expect(FragmentRoster).To(Equal("chat.roster"))
	})

	It("spells the event names the browser may send", func() {
		Expect(EventSend).To(Equal("chat.send"))
		Expect(EventDraft).To(Equal("chat.draft"))
		Expect(EventClear).To(Equal("chat.clear"))
		Expect(EventPurge).To(Equal("chat.purge"))
	})

	It("spells the event names only the room may emit", func() {
		Expect(EventPosted).To(Equal("chat.posted"))
		Expect(EventPresence).To(Equal("chat.presence"))
		Expect(EventPurged).To(Equal("chat.purged"))
	})

	It("spells the effect sources an operator greps provenance for", func() {
		Expect(SourceSubscribe).To(Equal("chat.subscribe"))
		Expect(SourcePost).To(Equal("chat.post"))
		Expect(SourcePurge).To(Equal("chat.purge"))
		Expect(SourcePanic).To(Equal("chat.panic"))
	})

	It("spells the commands a person types to provoke each error boundary", func() {
		Expect(CmdPanicReducer).To(Equal("/panic reducer"))
		Expect(CmdPanicEffect).To(Equal("/panic effect"))
		Expect(CmdPanicRender).To(Equal("/panic render"))
	})

	// Default-deny, asserted rather than assumed. The three names the room
	// emits must not be registered: registration is what makes a name sendable
	// by a browser, and a client that could send chat.posted could attribute a
	// sentence to somebody who never wrote it.
	It("registers what a browser may send and nothing the room emits", func() {
		cfg := Config(NewRoom(), DemoDirectory(), []string{testOrigin})

		Expect(cfg.Events).To(ConsistOf(EventSend, EventDraft, EventClear, EventPurge))
		Expect(cfg.Events).NotTo(ContainElement(EventPosted))
		Expect(cfg.Events).NotTo(ContainElement(EventPresence))
		Expect(cfg.Events).NotTo(ContainElement(EventPurged))
		Expect(cfg.Events).NotTo(ContainElement(live.EffectFailedEvent))
	})
})

// ---------------------------------------------------------------------------

var _ = Describe("The reducer", func() {
	var state State

	BeforeEach(func() {
		state = newState(tabA, alice)
	})

	// The reducer never records a message. That is the whole
	// server-authoritative claim in one property: sending asks the room to
	// record something and this session finds out the same way every other
	// session does.
	It("turns a submission into an effect and records nothing itself", func() {
		state.Draft = "hello everyone"

		next, effects := reduce(state, submit("hello everyone", 7, baseTime))

		Expect(sources(effects)).To(Equal([]string{SourcePost}))
		Expect(next.Messages()).To(BeEmpty(), "a reducer must not append the message itself")
		Expect(next.Draft).To(BeEmpty(), "an accepted message leaves the box empty")
		Expect(next.DraftError).To(BeEmpty())
	})

	// The effect carries a body and no author. Who said it is read from the
	// session inside the effect's own Run, so a reducer cannot attribute a
	// sentence to somebody else even by mistake — which is what running the
	// effect for two different sessions shows.
	It("puts no author on the effect it returns", func() {
		room := NewRoom()
		room.Join(tabA, "alice")
		_, effects := Reducer(room)(state, submit("hi", 1, baseTime))

		Expect(effects).To(HaveLen(1))
		Expect(effects[0].Run(context.Background(), memberSession(tabA, alice), noEmit)).To(Succeed())
		Expect(room.Log().Messages).To(HaveLen(1))
		Expect(room.Log().Messages[0].Author).To(Equal("alice"))
		Expect(room.Log().Messages[0].Body).To(Equal("hi"))
	})

	It("trims the body it accepts but keeps what was typed when it refuses", func() {
		room := NewRoom()
		room.Join(tabA, "alice")
		_, effects := Reducer(room)(state, submit("   spaced   ", 1, baseTime))
		Expect(sources(effects)).To(Equal([]string{SourcePost}))
		Expect(effects[0].Run(context.Background(), memberSession(tabA, alice), noEmit)).To(Succeed())
		Expect(room.Log().Messages[0].Body).To(Equal("spaced"),
			"the body the effect carries is the trimmed one")

		next, effects := reduce(state, submit("   ", 2, baseTime))
		Expect(effects).To(BeEmpty())
		Expect(next.Draft).To(Equal("   "),
			"the rejected text stays in the state: losing what somebody typed is the rudest thing a form can do")
		Expect(next.DraftError).NotTo(BeEmpty())
	})

	It("refuses a body past the length limit and says how long it was", func() {
		long := strings.Repeat("é", MaxBodyRunes+1)

		next, effects := reduce(state, submit(long, 1, baseTime))

		Expect(effects).To(BeEmpty())
		Expect(next.Draft).To(Equal(long))
		Expect(next.DraftError).To(ContainSubstring(strconv.Itoa(MaxBodyRunes + 1)))
		Expect(next.DraftError).To(ContainSubstring(strconv.Itoa(MaxBodyRunes)))
	})

	// FR-55's per-field change event. The draft lives on the server so that a
	// re-render of this region puts it back, which is the half of input
	// preservation the browser cannot do for you.
	It("keeps the draft the server will have to render back", func() {
		next, effects := reduce(state, typed("half a sen", 1, baseTime))

		Expect(effects).To(BeEmpty(), "typing is not a reason to do I/O")
		Expect(next.Draft).To(Equal("half a sen"))
		Expect(next.DraftError).To(BeEmpty())
	})

	// Escape-to-clear, the server half. FRICTION.md F-3 asked for this
	// interaction from checkpoint 1 and this example did not implement it; the
	// binding became expressible at 591c275a and correct at FR-54 failure 2.
	It("empties the draft and its verdict when the composer is cleared", func() {
		long := strings.Repeat("é", MaxBodyRunes+1)
		state = applyDraft(state, long)
		Expect(state.DraftError).NotTo(BeEmpty(), "the fixture must start with a verdict to clear")

		next, effects := reduce(state, cleared(long, 3, baseTime))

		Expect(effects).To(BeEmpty(), "discarding a draft is not a reason to do I/O")
		Expect(next.Draft).To(BeEmpty())
		Expect(next.DraftError).To(BeEmpty(),
			"a message about a sentence that no longer exists is a message about nothing")
	})

	// Notice is the room's last word to this member — a purge, an effect
	// failure — and abandoning a half-typed sentence is not a reason to take it
	// off their screen. Asserted because the obvious implementation of "clear
	// the composer" clears the whole fragment's state.
	It("leaves the room's last notice alone when the composer is cleared", func() {
		state.Draft = "half a sen"
		state.Notice = "the room was cleared by moderator"

		next, _ := reduce(state, cleared("half a sen", 4, baseTime))

		Expect(next.Draft).To(BeEmpty())
		Expect(next.Notice).To(Equal("the room was cleared by moderator"))
	})

	// The event's own body field is ignored, so a client that presses Escape
	// while the box holds something cannot use the clear to SET the draft.
	It("ignores whatever the clear event happens to carry", func() {
		state.Draft = "half a sen"

		next, _ := reduce(state, cleared("something else entirely", 5, baseTime))

		Expect(next.Draft).To(BeEmpty())
	})

	It("validates as you type, but says nothing about an empty box", func() {
		typedLong, _ := reduce(state, typed(strings.Repeat("x", MaxBodyRunes+5), 1, baseTime))
		Expect(typedLong.DraftError).NotTo(BeEmpty())

		cleared, _ := reduce(typedLong, typed("", 2, baseTime))
		Expect(cleared.Draft).To(BeEmpty())
		Expect(cleared.DraftError).To(BeEmpty(),
			"an empty box is not yet a mistake; reporting one puts an error on screen before the user acted")
	})

	It("folds a message the room pushed", func() {
		next, effects := reduce(state, pushed(
			postedUpdate(1, 4, "bob", "morning", []string{"alice", "bob"}), tabA, baseTime))

		Expect(effects).To(BeEmpty(), "folding a push is not itself a reason to do I/O")
		Expect(next.Version()).To(Equal(uint64(4)))
		Expect(next.Messages()).To(HaveLen(1))
		Expect(next.Messages()[0].Author).To(Equal("bob"))
		Expect(next.Messages()[0].Body).To(Equal("morning"))
		Expect(next.Members()).To(Equal([]string{"alice", "bob"}))
	})

	// The property that makes a duplicate or a late delivery harmless. A
	// revision this session has already passed carries nothing new, and
	// applying it twice would show the same sentence twice.
	It("ignores a push at a revision it has already passed", func() {
		state.Room = &Log{Version: 9}

		next, _ := reduce(state, pushed(postedUpdate(1, 9, "bob", "again", nil), tabA, baseTime))
		Expect(next.Messages()).To(BeEmpty())

		next, _ = reduce(state, pushed(postedUpdate(1, 8, "bob", "older", nil), tabA, baseTime))
		Expect(next.Messages()).To(BeEmpty())
		Expect(next.Version()).To(Equal(uint64(9)))
	})

	DescribeTable("ignores a push it cannot read rather than half-applying it",
		func(fields map[string]string) {
			state.Room = &Log{Version: 1}

			next, _ := reduce(state, live.Event{
				Name: EventPosted, At: baseTime, Fields: live.NewFields(fields),
			})

			Expect(next.Messages()).To(BeEmpty())
			Expect(next.Version()).To(Equal(uint64(1)))
		},
		Entry("no revision", map[string]string{fieldSeq: "2", fieldAtMilli: "1"}),
		Entry("an unreadable revision", map[string]string{fieldVersion: "later", fieldSeq: "2", fieldAtMilli: "1"}),
		Entry("an unreadable sequence", map[string]string{fieldVersion: "2", fieldSeq: "x", fieldAtMilli: "1"}),
		Entry("an unreadable timestamp", map[string]string{fieldVersion: "2", fieldSeq: "2", fieldAtMilli: "noon"}),
	)

	It("folds a roster change without disturbing the log", func() {
		state, _ = reduce(state, pushed(postedUpdate(1, 2, "bob", "hi", []string{"alice", "bob"}), tabA, baseTime))

		next, _ := reduce(state, pushed(update{
			kind: EventPresence, version: 3, members: []string{"alice", "bob", "mallory"},
		}, tabA, baseTime))

		Expect(next.Members()).To(Equal([]string{"alice", "bob", "mallory"}))
		Expect(next.Messages()).To(HaveLen(1), "who is in the room is not a fact about what was said")
	})

	It("folds a purge, naming who did it", func() {
		state, _ = reduce(state, pushed(postedUpdate(1, 2, "bob", "hi", []string{"alice", "bob"}), tabA, baseTime))

		next, _ := reduce(state, pushed(update{
			kind: EventPurged, version: 3, members: []string{"alice", "bob"}, by: "mallory",
		}, tabA, baseTime))

		Expect(next.Messages()).To(BeEmpty())
		Expect(next.Notice).To(ContainSubstring("mallory"))
		Expect(next.Members()).To(Equal([]string{"alice", "bob"}))
	})

	It("asks the room to clear itself, naming no actor", func() {
		next, effects := reduce(state, live.Event{Name: EventPurge, ID: 5, At: baseTime})

		Expect(sources(effects)).To(Equal([]string{SourcePurge}))
		Expect(next.Messages()).To(BeEmpty())
	})

	// The library refuses an unregistered name before the reducer runs, so a
	// name reaching this branch is one the library synthesised and this
	// application has nothing to say about.
	It("leaves state alone for a name it does not handle", func() {
		state.Draft = "mid-sentence"

		next, effects := reduce(state, live.Event{Name: "chat.nothing_emits_this", At: baseTime})

		Expect(effects).To(BeEmpty())
		Expect(next).To(Equal(state))
	})

	It("keeps only the last MaxHistory messages", func() {
		for i := 1; i <= MaxHistory+10; i++ {
			state, _ = reduce(state, pushed(
				postedUpdate(uint64(i), uint64(i), "bob", "line "+strconv.Itoa(i), nil), tabA, baseTime))
		}

		Expect(state.Messages()).To(HaveLen(MaxHistory))
		Expect(state.Messages()[0].Body).To(Equal("line 11"))
		Expect(state.Messages()[MaxHistory-1].Body).To(Equal("line " + strconv.Itoa(MaxHistory+10)))
	})

	// A reducer must not mutate the state it was given: that is what makes
	// panic recovery free, because the pre-transition state is still intact.
	// A slice in the state is where that rule is easy to break by accident.
	It("does not write through the log it was handed", func() {
		state, _ = reduce(state, pushed(postedUpdate(1, 2, "bob", "first", nil), tabA, baseTime))
		before := state.Room

		next, _ := reduce(state, pushed(postedUpdate(2, 3, "bob", "second", nil), tabA, baseTime))

		Expect(next.Room).NotTo(BeIdenticalTo(before))
		Expect(before.Messages).To(HaveLen(1))
		Expect(before.Messages[0].Body).To(Equal("first"))
	})

	// The sharper form of the same rule, and the one that actually bites.
	//
	// The spec above passes even if `with` appends onto the slice it was
	// handed, because an append never overwrites an element that is already
	// there — it writes past the end. What an in-place append does break is
	// TWO transitions from one prior state: both write the same index of one
	// backing array and the first result silently becomes the second.
	//
	// That is not a hypothetical shape. livetest.ReplayN folds the same
	// initial state n times and compares the results, which is exactly this
	// pattern, and the library keeps the pre-transition state on a reducer
	// panic. Five messages first, because the hazard needs a slice with spare
	// capacity to exist at all and a correct `with` never leaves any.
	It("gives two transitions from one state two independent logs", func() {
		for i := 1; i <= 5; i++ {
			state, _ = reduce(state, pushed(
				postedUpdate(uint64(i), uint64(i+1), "bob", "line "+strconv.Itoa(i), nil), tabA, baseTime))
		}

		left, _ := reduce(state, pushed(postedUpdate(9, 90, "bob", "the left branch", nil), tabA, baseTime))
		right, _ := reduce(state, pushed(postedUpdate(9, 91, "bob", "the right branch", nil), tabA, baseTime))

		Expect(state.Messages()).To(HaveLen(5), "the state both branches came from must not have moved")
		Expect(left.Messages()).To(HaveLen(6))
		Expect(right.Messages()).To(HaveLen(6))
		Expect(left.Messages()[5].Body).To(Equal("the left branch"),
			"the second transition wrote through the first one's log")
		Expect(right.Messages()[5].Body).To(Equal("the right branch"))
	})
})

// ---------------------------------------------------------------------------

var _ = Describe("The failed-effect path", func() {
	failure := func(source, detail, retryable string) live.Event {
		return live.Event{
			Name: live.EffectFailedEvent,
			At:   baseTime,
			Fields: live.NewFields(map[string]string{
				live.EffectFailedSourceField:    source,
				live.EffectFailedErrorField:     detail,
				live.EffectFailedRetryableField: retryable,
			}),
		}
	}

	DescribeTable("re-subscribes only when the library says a retry is safe",
		func(source, retryable string, want []string) {
			next, effects := reduce(newState(tabA, alice), failure(source, "boom", retryable))

			Expect(sources(effects)).To(Equal(want))
			Expect(next.Messages()).To(BeEmpty(), "a failed effect is not something somebody said")
		},
		Entry("a transient subscription failure is re-subscribed",
			SourceSubscribe, "true", []string{SourceSubscribe}),
		Entry("a terminal subscription failure is not",
			SourceSubscribe, "false", nil),
		Entry("an unreadable classification is terminal",
			SourceSubscribe, "", nil),
		Entry("a transient failure of an effect with nothing to retry is left alone",
			SourcePost, "true", nil),
		Entry("a panicking effect is terminal, and the library says so",
			SourcePanic, "false", nil),
	)

	// The disclosure C-24 documented, held here rather than trusted.
	//
	// EffectFailedErrorField carries the error's own message, or the raw panic
	// value, unredacted and in production, with no relation to Config.Dev. A
	// reducer that renders it publishes whatever an upstream library chose to
	// put in an error — a connection string, a query, an internal hostname —
	// to every browser holding this fragment. EffectFailedSourceField is a
	// name this application chose, and is the one that is safe to show.
	It("shows the effect's name and never the error text it came with", func() {
		leak := "dial postgres://chat:hunter2@db.internal:5432: connection refused"

		next, _ := reduce(newState(tabA, alice), failure(SourcePost, leak, "false"))
		html := render(ComposerRegion(next))

		Expect(next.Notice).To(ContainSubstring(SourcePost))
		Expect(html).To(ContainSubstring(SourcePost))
		Expect(html).NotTo(ContainSubstring("hunter2"))
		Expect(html).NotTo(ContainSubstring("db.internal"))
		Expect(html).NotTo(ContainSubstring("connection refused"))
	})
})

// ---------------------------------------------------------------------------

var _ = Describe("Validation", func() {
	DescribeTable("is the server's opinion, computed from the body alone",
		func(body string, wantProblem bool) {
			problem := Validate(body)
			if wantProblem {
				Expect(problem).NotTo(BeEmpty())
			} else {
				Expect(problem).To(BeEmpty())
			}
		},
		Entry("an ordinary sentence", "hello", false),
		Entry("one character", "x", false),
		Entry("exactly the limit", strings.Repeat("x", MaxBodyRunes), false),
		Entry("exactly the limit in multi-byte runes", strings.Repeat("é", MaxBodyRunes), false),
		Entry("one rune over", strings.Repeat("x", MaxBodyRunes+1), true),
		Entry("nothing at all", "", true),
	)

	// Counted in runes, not bytes, because the person is counting characters.
	// A byte count would refuse 141 emoji and accept 280 of them at random.
	It("counts characters rather than bytes", func() {
		Expect(Validate(strings.Repeat("🙂", MaxBodyRunes))).To(BeEmpty())
		Expect(Validate(strings.Repeat("🙂", MaxBodyRunes+1))).NotTo(BeEmpty())
	})
})

// ---------------------------------------------------------------------------

var _ = Describe("Authorization", func() {
	// FR-47's hook, exercised with more than one identity, on the same event
	// names, with the answers differing only by role.
	DescribeTable("answers per identity and per event",
		func(member Member, eventName, want string) {
			err := Authorize(context.Background(),
				sessionFor(tabA, member), live.Event{Name: eventName, ID: 1, At: baseTime})

			switch want {
			case "allow":
				Expect(err).NotTo(HaveOccurred())
			case "deny":
				var deny *live.DenyError
				Expect(errors.As(err, &deny)).To(BeTrue(), "got %v", err)
				Expect(deny.Reason).To(ContainSubstring(member.Name))
			case "close":
				var fatal *live.FatalDenyError
				Expect(errors.As(err, &fatal)).To(BeTrue(), "got %v", err)
			default:
				Fail("unknown expectation " + want)
			}
		},
		Entry("a member may post", alice, EventSend, "allow"),
		Entry("a member may type", alice, EventDraft, "allow"),
		Entry("a member may not clear the room", alice, EventPurge, "deny"),
		Entry("a moderator may post", mallory, EventSend, "allow"),
		Entry("a moderator may clear the room", mallory, EventPurge, "allow"),
		Entry("an observer may not post", olive, EventSend, "deny"),
		Entry("an observer may not even type", olive, EventDraft, "deny"),
		Entry("an observer may not clear the room either", olive, EventPurge, "deny"),
		// chat.clear is not in the posting branch, deliberately: it sets this
		// session's own Draft to the empty string and cannot reach the room, so
		// for an observer whose draft was never accepted it sets "" to "".
		// Denying it would be a permission with no referent. The banned entry
		// below is the one that matters — a banned member is closed on
		// ANYTHING, which is a check that runs before the switch.
		Entry("an observer may discard a draft nobody accepted", olive, EventClear, "allow"),
		Entry("a banned member is disconnected for discarding a draft", trudy, EventClear, "close"),
		Entry("a banned member is disconnected for posting", trudy, EventSend, "close"),
		Entry("a banned member is disconnected for typing", trudy, EventDraft, "close"),
		Entry("a banned member is disconnected for anything at all", trudy, EventPurge, "close"),
	)

	// Two sessions, one identity: the case Limits.MaxSessionsPerIdentity is
	// about, and the reason livetest.NewSession takes the identifier and the
	// identity separately instead of deriving one from the other.
	It("answers the same for two tabs of one member", func() {
		ev := live.Event{Name: EventPurge, ID: 1, At: baseTime}

		Expect(Authorize(context.Background(), sessionFor(tabA, alice), ev)).To(HaveOccurred())
		Expect(Authorize(context.Background(), sessionFor(tabB, alice), ev)).To(HaveOccurred())
		Expect(sessionFor(tabA, alice).ID()).NotTo(Equal(sessionFor(tabB, alice).ID()))
		Expect(sessionFor(tabA, alice).Identity()).To(Equal(sessionFor(tabB, alice).Identity()))
	})

	// The "denies an identity it does not recognise" spec is gone, and its
	// absence is the finding rather than a gap. A live.Session is typed by the
	// identity Authenticate produced since 2026-09-03, so an identity of an
	// unexpected shape is a COMPILE error at the call site: this spec could
	// only be written by constructing a Session[stranger], which no hook of this
	// application accepts. The failure mode an authorization hook must not have
	// is now one it cannot have.

	It("binds the session to a member from the cookie and to nothing without one", func() {
		dir := DemoDirectory()

		identity, err := dir.Authenticate(requestWithCookie("mallory"))
		Expect(err).NotTo(HaveOccurred())
		Expect(identity).To(Equal(mallory))
		Expect(identity.Subject()).To(Equal("mallory"))

		_, err = dir.Authenticate(requestWithCookie("nobody"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("nobody"),
			"an authentication error is read by an operator; echoing the value a client chose puts it in the log")

		_, err = dir.Authenticate(requestWithoutCookie())
		Expect(err).To(HaveOccurred())
	})
})

type stranger struct{}

func (stranger) Subject() string { return "stranger" }

// ---------------------------------------------------------------------------

// The FR-55 property that breaks naive implementations, stated on the
// declaration that makes it true.
//
// The whole mechanism is the composer's Dirty function naming three fields
// that belong to this session and nothing about the room. Widening it to
// include the room would still pass livetest.AssertDirtyComplete — over-
// declaring is safe as far as that helper is concerned — and would break the
// feature, so it needs a spec of its own.
var _ = Describe("Input preservation", func() {
	var composer live.Fragment[State]

	BeforeEach(func() {
		cfg := Config(NewRoom(), DemoDirectory(), []string{testOrigin})
		composer = cfg.Fragments[1]
		Expect(composer.ID).To(Equal(FragmentComposer))
	})

	It("does not re-render the composer when another member's message arrives", func() {
		before := newState(tabA, alice)
		before.Draft = "half a sentence I have not finished"

		after, _ := reduce(before, pushed(
			postedUpdate(1, 2, "bob", "sorry to interrupt", []string{"alice", "bob"}), tabA, baseTime))

		Expect(after.Room).NotTo(BeIdenticalTo(before.Room), "the room did move")
		Expect(composer.Dirty(before, after)).To(BeFalse(),
			"another member's message must not put the composer in the patch: the browser would be "+
				"handed markup for the box somebody is typing into")
	})

	It("does not re-render the composer when the roster moves either", func() {
		before := newState(tabA, alice)
		before.Draft = "still typing"

		after, _ := reduce(before, pushed(update{
			kind: EventPresence, version: 2, members: []string{"alice", "bob"},
		}, tabA, baseTime))

		Expect(composer.Dirty(before, after)).To(BeFalse())
	})

	DescribeTable("does re-render the composer when this session's own half moved",
		func(mutate func(*State)) {
			before := newState(tabA, alice)
			after := before
			mutate(&after)

			Expect(composer.Dirty(before, after)).To(BeTrue())
		},
		Entry("the draft", func(s *State) { s.Draft = "typing" }),
		Entry("the verdict on it", func(s *State) { s.DraftError = "too long" }),
		Entry("a notice", func(s *State) { s.Notice = "something went wrong" }),
	)

	// The other half. When the composer IS re-rendered — a validation message,
	// a resync, a reconnect — the value attribute has to carry the session's
	// own text back, or the browser is handed an empty box.
	It("renders the session's own draft back into the input", func() {
		state := newState(tabA, alice)
		state.Draft = "a sentence in progress"

		Expect(render(ComposerRegion(state))).To(ContainSubstring(`value="a sentence in progress"`))
	})

	It("renders the draft back even when the server rejected it", func() {
		state := newState(tabA, alice)
		long := strings.Repeat("x", MaxBodyRunes+1)

		rejected, _ := reduce(state, submit(long, 1, baseTime))
		html := render(ComposerRegion(rejected))

		Expect(html).To(ContainSubstring(`value="` + long + `"`))
		Expect(html).To(ContainSubstring(rejected.DraftError))
	})

	// A patch that arrives for the log while the composer is untouched is the
	// end-to-end shape of the property. This is the server half of it; the
	// wire spec in wire_test.go is the same claim measured on the frames.
	It("puts only the log and the roster in a transition another member caused", func() {
		cfg := Config(NewRoom(), DemoDirectory(), []string{testOrigin})
		before := newState(tabA, alice)
		before.Draft = "mid-sentence"
		before.Room = &Log{Version: 1, Members: []string{"alice"}}

		after, _ := reduce(before, pushed(
			postedUpdate(1, 2, "bob", "hello", []string{"alice", "bob"}), tabA, baseTime))

		dirty := map[string]bool{}
		for _, f := range cfg.Fragments {
			dirty[f.ID] = f.Dirty(before, after)
		}
		Expect(dirty).To(Equal(map[string]bool{
			FragmentLog:      true,
			FragmentRoster:   true,
			FragmentComposer: false,
		}))
	})
})

// ---------------------------------------------------------------------------

// mixedLog is a session's life as a member of a busy room: what alice typed,
// what she sent, what came back, what bob said, who arrived and left, and a
// message the server refused.
//
// The /panic commands are deliberately absent. Two of the three would make
// livetest.AssertDirtyComplete panic while rendering rather than fail, which
// is not a determinism result; they have specs of their own below and on the
// wire.
func mixedLog() []live.Event {
	at := baseTime
	id := uint64(0)
	next := func() (uint64, time.Time) {
		id++
		at = at.Add(300 * time.Millisecond)
		return id, at
	}

	var log []live.Event
	version := uint64(1)
	seq := uint64(0)
	members := []string{"alice"}

	push := func(u update) {
		at = at.Add(3 * time.Millisecond)
		log = append(log, pushed(u, tabA, at))
	}

	i, t := next()
	log = append(log, typed("hel", i, t))
	i, t = next()
	log = append(log, typed("hello ther", i, t))

	i, t = next()
	log = append(log, submit("hello there", i, t))
	seq++
	version++
	push(postedUpdate(seq, version, "alice", "hello there", members))

	members = []string{"alice", "bob"}
	version++
	push(update{kind: EventPresence, version: version, members: members})

	seq++
	version++
	push(postedUpdate(seq, version, "bob", "hi alice", members))

	i, t = next()
	log = append(log, typed("this one is going to be far too", i, t))
	i, t = next()
	log = append(log, submit(strings.Repeat("x", MaxBodyRunes+1), i, t))

	i, t = next()
	log = append(log, typed("shorter", i, t))
	i, t = next()
	log = append(log, submit("shorter", i, t))
	seq++
	version++
	push(postedUpdate(seq, version, "alice", "shorter", members))

	version++
	push(update{kind: EventPurged, version: version, members: members, by: "mallory"})

	members = []string{"alice"}
	version++
	push(update{kind: EventPresence, version: version, members: members})

	return log
}

var _ = Describe("Determinism", func() {
	initial := func() State { return newState(tabA, alice) }

	// FR-15's mandatory harness, pointed at this example's reducer. A reducer
	// that read a clock, a random source or the iteration order of a map would
	// fail it; nothing else in a pure function of two values can differ
	// between runs.
	It("replays the whole session to the same state and the same effects", func() {
		livetest.ReplayN(GinkgoTB(), reduce, initial(), mixedLog(), 25)
	})

	It("replays to the room the log describes", func() {
		state := initial()
		for _, ev := range mixedLog() {
			state, _ = reduce(state, ev)
		}

		Expect(state.Messages()).To(BeEmpty(), "mallory cleared the room at the end")
		Expect(state.Notice).To(ContainSubstring("mallory"))
		Expect(state.Members()).To(Equal([]string{"alice"}))
		Expect(state.Draft).To(BeEmpty())
	})

	// The dual mistake, in rendering rather than in reducing: a fragment that
	// declared itself unchanged while its markup moved. That is the one bug
	// that produces a stale region in production and nothing at all in
	// development, because some other transition usually re-renders it before
	// anybody looks.
	It("declares every fragment that its own markup changes", func() {
		livetest.AssertDirtyComplete(GinkgoTB(),
			Config(NewRoom(), DemoDirectory(), []string{testOrigin}), initial(), mixedLog())
	})

	// The half AssertDirtyComplete cannot check: that a declaration is tight.
	// A Dirty function that always returns true is a Dirty function that is
	// not doing its job, and for the composer it is also the FR-55 bug.
	It("does not re-render the roster when only the log moved", func() {
		cfg := Config(NewRoom(), DemoDirectory(), []string{testOrigin})
		roster := cfg.Fragments[2]
		Expect(roster.ID).To(Equal(FragmentRoster))

		before := newState(tabA, alice)
		before.Room = &Log{Version: 1, Members: []string{"alice", "bob"}}

		after, _ := reduce(before, pushed(
			postedUpdate(1, 2, "bob", "hello", []string{"alice", "bob"}), tabA, baseTime))
		Expect(roster.Dirty(before, after)).To(BeFalse())

		grown, _ := reduce(after, pushed(update{
			kind: EventPresence, version: 3, members: []string{"alice", "bob", "olive"},
		}, tabA, baseTime))
		Expect(roster.Dirty(after, grown)).To(BeTrue())
	})

	// The library compares consecutive states to decide whether the state
	// version moved, and a state type that is not comparable is reported as
	// changed on every transition. That is what the *Log indirection buys, and
	// it is one field away from being lost.
	It("keeps the state type comparable", func() {
		a := newState(tabA, alice)
		b := a
		Expect(a == b).To(BeTrue())

		moved, _ := reduce(a, pushed(postedUpdate(1, 2, "bob", "hi", nil), tabA, baseTime))
		Expect(a == moved).To(BeFalse())
	})
})

// ---------------------------------------------------------------------------

var _ = Describe("The room", func() {
	var room *Room

	BeforeEach(func() {
		room = NewRoom()
		room.now = func() time.Time { return baseTime }
	})

	It("registers a session and reads the room under one lock", func() {
		log := room.Join(tabA, "alice")

		Expect(log.Members).To(Equal([]string{"alice"}))
		Expect(log.Messages).To(BeEmpty())
		Expect(room.Occupants()).To(Equal(1))
	})

	It("grows and shrinks the roster, sorted, as sessions come and go", func() {
		room.Join(tabB, "bob")
		room.Join(tabA, "alice")
		Expect(room.Log().Members).To(Equal([]string{"alice", "bob"}))

		room.Leave(tabB)
		Expect(room.Log().Members).To(Equal([]string{"alice"}))
		Expect(room.Occupants()).To(Equal(1))
	})

	// Two tabs, one member: the roster shows the name twice because the roster
	// is of sessions, and that is the honest thing for it to show.
	It("keeps both sessions of one member", func() {
		room.Join(tabA, "alice")
		room.Join(tabB, "alice")

		Expect(room.Log().Members).To(Equal([]string{"alice", "alice"}))
		Expect(room.Occupants()).To(Equal(2))

		room.Leave(tabA)
		Expect(room.Occupants()).To(Equal(1))
	})

	It("ignores a session leaving that was never here", func() {
		room.Join(tabA, "alice")
		before := room.Log().Version

		room.Leave(tabC)

		Expect(room.Occupants()).To(Equal(1))
		Expect(room.Log().Version).To(Equal(before))
	})

	It("numbers and stamps a message, and keeps the room's revision moving", func() {
		room.Join(tabA, "alice")

		first := room.Post("alice", Post{Body: "one"}, tabA)
		second := room.Post("alice", Post{Body: "two"}, tabA)

		Expect(first.Seq).To(Equal(uint64(1)))
		Expect(second.Seq).To(Equal(uint64(2)))
		Expect(first.AtUnixMilli).To(Equal(baseTime.UnixMilli()))
		Expect(room.Log().Version).To(BeNumerically(">", first.Seq))
	})

	It("keeps only the last MaxHistory messages", func() {
		room.Join(tabA, "alice")
		for i := 1; i <= MaxHistory+5; i++ {
			room.Post("alice", Post{Body: "line " + strconv.Itoa(i)}, tabA)
		}

		log := room.Log()
		Expect(log.Messages).To(HaveLen(MaxHistory))
		Expect(log.Messages[0].Body).To(Equal("line 6"))
	})

	// A *Log handed out earlier is promised to be immutable. Trimming by
	// re-slicing the old backing array would let a later post write through
	// one somebody is still holding.
	It("never writes through a log it already handed out", func() {
		room.Join(tabA, "alice")
		for i := 1; i <= MaxHistory; i++ {
			room.Post("alice", Post{Body: "line " + strconv.Itoa(i)}, tabA)
		}
		held := room.Log()
		firstBody := held.Messages[0].Body

		for i := 1; i <= 10; i++ {
			room.Post("alice", Post{Body: "later " + strconv.Itoa(i)}, tabA)
		}

		Expect(held.Messages[0].Body).To(Equal(firstBody))
	})

	It("clears the log on a purge and keeps the roster", func() {
		room.Join(tabA, "alice")
		room.Post("alice", Post{Body: "something"}, tabA)

		room.Purge("mallory", Purge{}, tabA)

		log := room.Log()
		Expect(log.Messages).To(BeEmpty())
		Expect(log.Members).To(Equal([]string{"alice"}))
	})

	// The reason an effect's Run takes a live.Session[Member]. The same effect,
	// performed for two sessions, produces two different authors — so a
	// reducer cannot attribute a sentence to somebody who did not write it,
	// because a reducer never gets to say who wrote it.
	//
	// The spec that used to sit above this one — "refuses an effect it has no
	// executor for" — is gone with the executor. An effect this room does not
	// own is an effect this room cannot be handed now that a live.Effect[Member]
	// carries its own Run.
	It("stamps the author from the session, not from the effect", func() {
		room.Join(tabA, "alice")
		room.Join(tabB, "bob")
		post := room.PostEffect(Post{Body: "the very same effect"})

		Expect(post.Run(GinkgoT().Context(), sessionFor(tabA, alice), noEmit)).To(Succeed())
		Expect(post.Run(GinkgoT().Context(), sessionFor(tabB, bob), noEmit)).To(Succeed())

		log := room.Log()
		Expect(log.Messages).To(HaveLen(2))
		Expect(log.Messages[0].Author).To(Equal("alice"))
		Expect(log.Messages[1].Author).To(Equal("bob"))
	})

	It("stamps the purger from the session too", func() {
		room.Join(tabA, "alice")
		room.Join(tabB, "bob")
		room.Post("alice", Post{Body: "something"}, tabA)

		Expect(room.PurgeEffect(Purge{Cause: 3}).
			Run(GinkgoT().Context(), sessionFor(tabB, mallory), noEmit)).To(Succeed())

		var got live.Event
		Eventually(func() bool {
			select {
			case u := <-room.subs[tabA].queue:
				if u.kind == EventPurged {
					got = u.event(tabA)
					return true
				}
			default:
			}
			return false
		}).Should(BeTrue())
		Expect(got.Fields.Get(fieldBy)).To(Equal("mallory"))
	})

	// The "refuses an effect for a session whose identity is not a member" spec
	// is gone with the assertion it exercised. An effect's Run takes a
	// live.Session[Member]; a Session carrying anything else is a compile error
	// at the call site, so there is no runtime refusal left to specify.

	// FR-23's second site, at the boundary rather than through it. The library
	// is what turns this into an event; this asserts only that the effect does
	// what it says.
	It("panics on the injected panic effect", func() {
		Expect(func() {
			_ = room.PanicEffect().Run(GinkgoT().Context(), sessionFor(tabA, alice), noEmit)
		}).To(PanicWith(ContainSubstring(CmdPanicEffect)))
	})
})

// ---------------------------------------------------------------------------

var _ = Describe("The subscription pump", func() {
	var room *Room

	BeforeEach(func() {
		room = NewRoom()
		room.now = func() time.Time { return baseTime }
	})

	// The end-to-end shape of the push channel, with the library's Emitter
	// replaced by a channel a spec can read: what one session says reaches
	// every other session as an event carrying the message.
	It("emits one event per room change, to every subscribed session", func() {
		room.Join(tabA, "alice")
		room.Join(tabB, "bob")

		ctx, cancel := context.WithCancel(GinkgoT().Context())
		defer cancel()

		emitted := make(chan live.Event, 16)
		go func() {
			_ = room.SubscribeEffect().Run(ctx, sessionFor(tabB, bob), func(ev live.Event) error {
				emitted <- ev
				return nil
			})
		}()

		// bob's own arrival is not pushed to bob — Config.Init already
		// returned it as the initial state — but alice's tab was told about
		// it, which is the roster growing without anybody touching anything.
		var ev live.Event
		room.Post("alice", Post{Body: "morning"}, tabA)

		Eventually(emitted).Should(Receive(&ev))
		Expect(ev.Name).To(Equal(EventPosted))
		Expect(ev.Fields.Get(fieldAuthor)).To(Equal("alice"))
		Expect(ev.Fields.Get(fieldBody)).To(Equal("morning"))
		Expect(ev.Fields.Get(fieldMembers)).To(Equal("alice,bob"))

		// And the event a reducer receives folds to the message alice sent.
		next, _ := reduce(newState(tabB, bob), live.Event{Name: ev.Name, Fields: ev.Fields, At: baseTime})
		Expect(next.Messages()).To(HaveLen(1))
		Expect(next.Messages()[0].Body).To(Equal("morning"))
	})

	// The contributing edge, and who it belongs to. It is a claim about one
	// recipient's own event; identifiers are session-scoped, so naming another
	// session's event is not something that can be true.
	It("names the submission only for the session that made it", func() {
		room.Join(tabA, "alice")
		room.Join(tabB, "bob")

		u := update{
			kind: EventPosted, version: 3, members: []string{"alice", "bob"},
			msg:      Message{Seq: 1, Author: "alice", Body: "hi", AtUnixMilli: baseTime.UnixMilli()},
			causeFor: tabA, cause: 42,
		}

		Expect(u.event(tabA).Contributing).To(Equal([]uint64{42}))
		Expect(u.event(tabB).Contributing).To(BeEmpty())
	})

	It("retries an update the session could not accept", func() {
		room.Join(tabA, "alice")

		ctx, cancel := context.WithCancel(GinkgoT().Context())
		defer cancel()

		accepted := make(chan live.Event, 4)
		refusals := 0
		go func() {
			_ = room.SubscribeEffect().Run(ctx, sessionFor(tabA, alice), func(ev live.Event) error {
				if refusals < 2 {
					refusals++
					return errSaturated
				}
				accepted <- ev
				return nil
			})
		}()

		room.Post("alice", Post{Body: "eventually"}, tabA)

		var ev live.Event
		Eventually(accepted, 2*time.Second).Should(Receive(&ev))
		Expect(ev.Fields.Get(fieldBody)).To(Equal("eventually"))
	})

	// The bound on that retry, and the handoff it creates. A pump that retried
	// forever would keep a subscription that is going nowhere looking alive.
	It("gives up after a run of refusals rather than hiding a stuck subscription", func() {
		room.Join(tabA, "alice")

		ctx, cancel := context.WithCancel(GinkgoT().Context())
		defer cancel()

		done := make(chan error, 1)
		go func() {
			done <- room.SubscribeEffect().Run(ctx, sessionFor(tabA, alice),
				func(live.Event) error { return errSaturated })
		}()

		room.Post("alice", Post{Body: "nobody will get this"}, tabA)

		var err error
		Eventually(done, 5*time.Second).Should(Receive(&err))
		Expect(err).To(MatchError(ContainSubstring(strconv.Itoa(maxRefusals) + " updates in a row")))
		// The transient half of the pair below, asserted on the mark itself.
		// This used to read errors.Unwrap(err) != nil, and L9-1 measured what
		// that was worth: with live.Retryable replaced by a plain fmt.Errorf,
		// the mark gone, this spec stayed green while chat.go stopped
		// re-subscribing.
		Expect(live.IsRetryable(err)).To(BeTrue(),
			"a saturated mailbox is transient, so the pump must have wrapped it with live.Retryable")
	})

	// A session that fell behind is a different failure from one that is busy,
	// and it is TERMINAL. Re-subscribing cannot refill a gap: it would restore
	// the appearance of a working subscription over a log missing a sentence.
	It("reports falling behind as terminal rather than retrying into a hole", func() {
		room.Join(tabA, "alice")
		sub := room.subs[tabA]
		for range backlogDepth + 5 {
			sub.offer(update{kind: EventPresence, version: 1})
		}
		Expect(sub.behind.Load()).To(BeTrue())

		err := room.SubscribeEffect().Run(GinkgoT().Context(), sessionFor(tabA, alice),
			func(live.Event) error { return nil })

		Expect(err).To(MatchError(ContainSubstring("fell more than")))
		// That it is terminal, asserted on the classification rather than on
		// the shape of the error. The wrapping test this replaces was the
		// weaker half of the pair: it would have gone on passing if this error
		// grew a %w for some unrelated reason.
		Expect(live.IsRetryable(err)).To(BeFalse(),
			"falling behind must not be marked retryable: re-subscribing cannot refill a gap")
	})

	It("returns as soon as the session's context is cancelled", func() {
		room.Join(tabA, "alice")

		ctx, cancel := context.WithCancel(GinkgoT().Context())
		done := make(chan error, 1)
		go func() {
			done <- room.SubscribeEffect().Run(ctx, sessionFor(tabA, alice),
				func(live.Event) error { return nil })
		}()

		cancel()
		Eventually(done).Should(Receive(MatchError(context.Canceled)))
	})

	It("reports a session that was never joined rather than blocking forever", func() {
		err := room.SubscribeEffect().Run(GinkgoT().Context(), sessionFor(tabA, alice),
			func(live.Event) error { return nil })

		Expect(err).To(MatchError(ContainSubstring("not in the room")))
	})
})

var errSaturated = errors.New("the session mailbox is full")

// ---------------------------------------------------------------------------

// FR-50, through the one surface a stranger controls: the body of a message,
// and the draft that is echoed back into an attribute.
//
// The two are different escaping contexts and both matter. A body lands in
// text content; a draft lands inside a quoted attribute value, where a single
// unescaped double quote is enough to break out and add an event handler.
var _ = Describe("Escaping", func() {
	// A slice rather than a map: a map has no iteration order, so a suite
	// built from one reports its own specs in a different order every run.
	payloads := []struct{ name, payload string }{
		{"a script element", `<script>alert(1)</script>`},
		{"an attribute break-out", `"><img src=x onerror=alert(1)>`},
		{"a tag close and re-open", `</span><script>alert("xss")</script><span>`},
		{"an inline handler", `<img src=x onerror="alert(document.cookie)">`},
		{"a javascript URL", `<a href="javascript:alert(1)">click</a>`},
		{"an already-escaped sequence", `&lt;script&gt;alert(1)&lt;/script&gt;`},
		{"a style and svg break-out", `'"--></style></script><svg/onload=alert(1)>`},
		{"a single-quoted attribute", `' onmouseover='alert(1)`},
		{"an entity that only decodes once", `&amp;lt;script&amp;gt;`},
	}

	// benign is the same markup with nothing hostile in it. Comparing the
	// number of "<" against it is the assertion that matters: a payload that
	// escaped correctly contributes &lt; and changes no element structure at
	// all, and a payload that broke out contributes at least one more tag.
	// Substring assertions on "onerror" cannot say this — an escaped
	// onerror= is still the literal text onerror=, and asserting its absence
	// asserts that the message was thrown away rather than that it was safe.
	const benignBody = "nothing to see here"

	for _, tc := range payloads {
		It("escapes "+tc.name+" in a message body", func() {
			state := newState(tabA, alice)
			hostile, _ := reduce(state, pushed(
				postedUpdate(1, 2, "bob", tc.payload, []string{"alice", "bob"}), tabA, baseTime))
			safe, _ := reduce(state, pushed(
				postedUpdate(1, 2, "bob", benignBody, []string{"alice", "bob"}), tabA, baseTime))

			html := render(LogRegion(hostile))

			Expect(html).NotTo(ContainSubstring("<script"))
			Expect(html).NotTo(ContainSubstring(tc.payload),
				"the payload reached the markup verbatim: nothing escaped it")
			Expect(strings.Count(html, "<")).To(Equal(strings.Count(render(LogRegion(safe)), "<")),
				"an escaped payload changes no element structure; this one added markup")
			// And it was shown rather than dropped. Refusing to display what
			// somebody typed would satisfy every assertion above and be a
			// different bug.
			Expect(html).To(ContainSubstring(templ.EscapeString(tc.payload)))
		})

		It("escapes "+tc.name+" in the draft echoed back into the input", func() {
			hostile, _ := reduce(newState(tabA, alice), typed(tc.payload, 1, baseTime))
			safe, _ := reduce(newState(tabA, alice), typed(benignBody, 1, baseTime))

			html := render(ComposerRegion(hostile))

			Expect(html).NotTo(ContainSubstring("<script"))
			Expect(html).NotTo(ContainSubstring(tc.payload))
			Expect(strings.Count(html, "<input")).To(Equal(1),
				"a draft that broke out of the value attribute would have added markup")
			Expect(strings.Count(html, "<")).To(Equal(strings.Count(render(ComposerRegion(safe)), "<")))
			Expect(html).To(ContainSubstring(`value="` + templ.EscapeString(tc.payload) + `"`))
		})
	}

	// The room's own notice path. A purge names who did it, and who did it is
	// a member name — but the assertion is on the escaping rather than on the
	// directory, because a directory is a thing an operator edits.
	It("escapes a name in a notice", func() {
		state := newState(tabA, alice)
		state, _ = reduce(state, pushed(update{
			kind: EventPurged, version: 2, by: `<script>alert(1)</script>`, members: []string{"alice"},
		}, tabA, baseTime))

		html := render(ComposerRegion(state))

		Expect(html).NotTo(ContainSubstring("<script"))
		Expect(html).To(ContainSubstring(templ.EscapeString(`<script>alert(1)</script>`)))
	})

	// The roster is the third place a string somebody chose reaches markup.
	// The names come from the directory today, which is not attacker-
	// controlled, and the escaping must not depend on that staying true.
	It("escapes a name in the roster", func() {
		state := newState(tabA, alice)
		state, _ = reduce(state, pushed(update{
			kind: EventPresence, version: 2, members: []string{"alice", `<b onclick=alert(1)>bob`},
		}, tabA, baseTime))

		html := render(RosterRegion(state))

		Expect(html).NotTo(ContainSubstring("<b "))
		Expect(html).To(ContainSubstring(templ.EscapeString(`<b onclick=alert(1)>bob`)))
	})

	// The whole page, so that nothing composes its way past the fragments. The
	// only script element on this page is the client runtime's, which the
	// library writes.
	It("puts exactly one script element on the page, and it is the runtime's", func() {
		state := newState(tabA, alice)
		state, _ = reduce(state, pushed(
			postedUpdate(1, 2, "bob", `</head><script>alert(1)</script>`, []string{"alice"}), tabA, baseTime))

		html := render(Page(chatApp(), state))

		Expect(strings.Count(html, "<script")).To(Equal(1))
		Expect(html).To(ContainSubstring(`<script src="` + MountPath + `/gotth-live.min.js"`))
	})
})

// ---------------------------------------------------------------------------

var _ = Describe("The markup", func() {
	// The attribute vocabulary is a contract with the client runtime, and a
	// disagreement is a silent no-op in the browser rather than an error
	// anywhere. Asserting on the rendered bytes is what makes it loud.
	It("marks each live region with the fragment ID the patches name", func() {
		html := render(Page(chatApp(), newState(tabA, alice)))

		for _, id := range []string{FragmentLog, FragmentComposer, FragmentRoster} {
			Expect(html).To(ContainSubstring(`data-gotth-region="` + id + `"`))
		}
	})

	It("binds every control to a registered event", func() {
		html := render(ComposerRegion(newState(tabA, mallory)))

		Expect(html).To(ContainSubstring(`data-gotth-on="submit:` + EventSend + `"`))
		Expect(html).To(ContainSubstring(`data-gotth-on="click:` + EventPurge + `"`))

		// The composer's input carries two bindings, so the assertion is on
		// the whole attribute rather than on a substring of it: the key
		// binding must come FIRST — the client matches in order and the first
		// match wins — and the 150 ms must sit inside the input binding rather
		// than beside both of them, which is what stops the Escape inheriting
		// it. Both are invisible to a ContainSubstring on either half.
		Expect(html).To(ContainSubstring(
			`data-gotth-on="keydown:` + EventClear + `:Escape;input:` + EventDraft + `::150"`))
	})

	// FR-54: the bindings come from templ helpers and there is no hand-written
	// JavaScript anywhere on the page. No inline handler attribute, no
	// javascript: URL, and exactly one script element — the runtime's, with a
	// src rather than a body, which is also what makes the strict CSP in the
	// quickstart work.
	It("carries no hand-written JavaScript at all", func() {
		html := render(Page(chatApp(), newState(tabA, mallory)))

		for _, forbidden := range []string{
			"onclick=", "oninput=", "onsubmit=", "onchange=", "onkeydown=", "javascript:",
		} {
			Expect(strings.ToLower(html)).NotTo(ContainSubstring(forbidden))
		}
		Expect(strings.Count(html, "<script")).To(Equal(1))
		// The one script element has a src and no body, which is also what
		// makes the strict CSP in the quickstart work: no inline script means
		// no 'unsafe-inline'.
		open := strings.Index(html, "<script")
		close := strings.Index(html[open:], "</script>")
		Expect(close).To(BeNumerically(">", 0))
		tag := html[open : open+close]
		Expect(tag).To(HaveSuffix(">"), "the script element carries a body")
		Expect(strings.Count(tag, ">")).To(Equal(1))
	})

	// The runtime resolves an event's fragment by walking up from the target
	// to the nearest data-gotth-region ancestor. A control outside every
	// region raises nothing at all — a silent no-op in the browser rather than
	// an error anywhere.
	It("keeps every control inside its region", func() {
		html := render(ComposerRegion(newState(tabA, mallory)))

		region := strings.Index(html, "data-gotth-region")
		Expect(region).To(BeNumerically(">=", 0))
		for _, binding := range []string{
			"submit:" + EventSend,
			"keydown:" + EventClear + ":Escape",
			"input:" + EventDraft,
			"click:" + EventPurge,
		} {
			Expect(strings.Index(html, binding)).To(BeNumerically(">", region))
		}
	})

	// The per-field change event is debounced, or every keystroke is a frame —
	// and the binding beside it is not, which is FR-54 failure 2. Until
	// 2026-08-05 the interval was an attribute of the ELEMENT: the Escape
	// binding would have inherited it, and a character typed inside the window
	// would have destroyed the pending clear with nothing on the wire to see.
	It("debounces the per-field change event and nothing else on that element", func() {
		html := render(ComposerRegion(newState(tabA, alice)))

		Expect(html).To(ContainSubstring(`input:` + EventDraft + `::150`))
		Expect(html).NotTo(ContainSubstring(`data-gotth-debounce`),
			"a debounce beside the bindings is one every binding on the element reads")
		Expect(html).NotTo(ContainSubstring(EventClear+`:Escape:150`),
			"the key binding asked for no interval and must not be given one")
	})

	// The composer serializes as a form, so the client's one code path —
	// FormData over the enclosing form — carries the body for both the submit
	// and the per-field change.
	It("names the field the reducer reads", func() {
		html := render(ComposerRegion(newState(tabA, alice)))

		Expect(html).To(ContainSubstring(`name="` + fieldBody + `"`))
	})

	// live.Script is given the mount path this application chose. It used to
	// default to /live, so an application mounted anywhere else served a page
	// whose script 404'd, with no server-side error anywhere. This example is
	// mounted somewhere else on purpose.
	It("points the browser at the mount path this application chose", func() {
		html := render(Page(chatApp(), newState(tabA, alice)))

		Expect(MountPath).NotTo(Equal("/live"))
		Expect(html).To(ContainSubstring(`src="` + MountPath + `/gotth-live.min.js"`))
		Expect(html).To(ContainSubstring(`data-gotth-url="` + MountPath + `"`))
	})

	DescribeTable("shows each role the controls its identity may actually use",
		func(member Member, wantEnabled, wantPurge bool) {
			html := render(ComposerRegion(newState(tabA, member)))

			Expect(strings.Contains(html, "disabled")).To(Equal(!wantEnabled))
			Expect(strings.Contains(html, `data-chat-id="purge"`)).To(Equal(wantPurge))
		},
		Entry("a member types and cannot purge", alice, true, false),
		Entry("a moderator types and can purge", mallory, true, true),
		Entry("an observer does neither", olive, false, false),
	)

	// The page and the fragments must render the same bytes for the same
	// state, or the first patch after connecting would visibly rewrite a page
	// that was already correct.
	It("composes the page from the same components the fragments render", func() {
		state := newState(tabA, alice)
		state, _ = reduce(state, pushed(
			postedUpdate(1, 2, "bob", "hello", []string{"alice", "bob"}), tabA, baseTime))

		page := render(Page(chatApp(), state))
		Expect(page).To(ContainSubstring(render(LogRegion(state))))
		Expect(page).To(ContainSubstring(render(ComposerRegion(state))))
		Expect(page).To(ContainSubstring(render(RosterRegion(state))))
	})

	// A render must be a pure function of state: the same state must produce
	// byte-identical HTML, or the comparison that suppresses a patch nobody
	// needs compares two things that were never going to be equal. The hazard
	// this catches is ranging over a map, which both the roster and the login
	// page were one line away from doing.
	It("renders the same state to the same bytes, every time", func() {
		state := newState(tabA, alice)
		for i := 1; i <= 4; i++ {
			state, _ = reduce(state, pushed(postedUpdate(uint64(i), uint64(i+1), "bob",
				"line "+strconv.Itoa(i), []string{"alice", "bob", "mallory"}), tabA, baseTime))
		}

		first := render(Page(chatApp(), state))
		for range 20 {
			Expect(render(Page(chatApp(), state))).To(Equal(first))
		}

		names := DemoDirectory().Names()
		firstLogin := render(LoginPage(chatApp(), names))
		for range 20 {
			Expect(render(LoginPage(chatApp(), DemoDirectory().Names()))).To(Equal(firstLogin))
		}
	})

	// FR-23's third site, contained to the fragment that has it armed. The
	// composer and the roster still render, which is what lets the library
	// patch them in the same pass and leave one region stale.
	It("panics only in the log fragment when the render panic is armed", func() {
		state := newState(tabA, alice)
		state.RenderPanic = true

		Expect(func() { render(LogRegion(state)) }).To(PanicWith(ContainSubstring(CmdPanicRender)))
		Expect(func() { render(ComposerRegion(state)) }).NotTo(Panic())
		Expect(func() { render(RosterRegion(state)) }).NotTo(Panic())
	})
})

func render(c templ.Component) string {
	GinkgoHelper()
	var buf bytes.Buffer
	Expect(c.Render(context.Background(), &buf)).To(Succeed())
	return buf.String()
}

// chatApp is an application for the page specs to render through. Page and
// LoginPage need one because app.Document is a method: what it writes into the
// document's head depends on this Config, and Dev is false here, which is what
// the "exactly one script element" spec above is counting.
func chatApp() *live.App[State, Member] {
	GinkgoHelper()

	app, err := live.New(Config(NewRoom(), DemoDirectory(), []string{testOrigin}))
	Expect(err).NotTo(HaveOccurred())
	return app
}

var _ = Describe("Startup", func() {
	Describe("the Origin allowlist", func() {
		It("names both loopback spellings, because a browser sends the host you typed", func() {
			Expect(allowedOrigins("127.0.0.1:8081", "")).To(ConsistOf(
				"http://127.0.0.1:8081", "http://localhost:8081"))
			Expect(allowedOrigins("localhost:8081", "")).To(ConsistOf(
				"http://localhost:8081", "http://127.0.0.1:8081"))
		})

		// The README's container invocation is "-addr 0.0.0.0:8081". No browser
		// ever sends 0.0.0.0 as an Origin, so without the bind-all arm the
		// documented way to run this example allows exactly one Origin nothing
		// can produce, and every upgrade is refused with 403.
		It("names them for the bind-all address the README tells you to use", func() {
			Expect(allowedOrigins("0.0.0.0:8081", "")).To(ContainElements(
				"http://127.0.0.1:8081", "http://localhost:8081"))
		})

		It("appends what the operator asked for and nothing else", func() {
			Expect(allowedOrigins("127.0.0.1:8081", "http://192.168.1.10:8081 , ")).To(ContainElement(
				"http://192.168.1.10:8081"))
		})

		It("never produces the wildcard, whatever it is given", func() {
			for _, addr := range []string{"127.0.0.1:8081", "0.0.0.0:8081", "localhost:8081", ":8081"} {
				Expect(allowedOrigins(addr, "")).NotTo(ContainElement(live.AnyOrigin), "addr %q", addr)
			}
		})
	})
})
