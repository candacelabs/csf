package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

var (
	tabA = live.ID{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf}
	tabB = live.ID{0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf}
)

// baseTime is the wall clock the specs use. Nothing under test reads a clock,
// so it is a constant rather than a fixture.
var baseTime = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

var testOrigins = []string{"http://127.0.0.1:3000"}

func render(c templ.Component) string {
	GinkgoHelper()
	var buf bytes.Buffer
	Expect(c.Render(context.Background(), &buf)).To(Succeed())
	return buf.String()
}

// composerSpec is the string ComposerBinding() puts in data-gotth-on.
//
// The attribute name is spelled out rather than read from the library, because
// live's own constant is unexported and a consumer of this API sees exactly what
// is written here — which is the position these apps are built to measure from.
func composerSpec() string {
	GinkgoHelper()
	v, ok := ComposerBinding()["data-gotth-on"]
	Expect(ok).To(BeTrue(), "OnAll emits exactly one attribute and it is data-gotth-on")
	s, ok := v.(string)
	Expect(ok).To(BeTrue(), "the binding spec is a string")
	return s
}

// componentOf is one ":" component of one ";" binding of the composer's spec,
// and "" for a component the trailing-empty trimming removed.
//
// The grammar is
// <domEvent>:<eventName>:<key>:<debounceMs>:<throttleMs>:<fields>:<noMods>:<preventDefault>,
// so the subscripts the specs below use are that list, zero-based. A trimmed
// binding is SHORTER than eight, which is why a missing component reads as ""
// rather than panicking — that is exactly what the client's own `+undefined ||
// 0` does with the same string.
func componentOf(binding, component int) string {
	GinkgoHelper()
	all := strings.Split(composerSpec(), ";")
	Expect(binding).To(BeNumerically("<", len(all)), "no such binding in the composer's spec")
	parts := strings.Split(all[binding], ":")
	if component >= len(parts) {
		return ""
	}
	return parts[component]
}

// initialState is a mounted session in alpha, as Config.Init would build it.
func initialState() State {
	return State{Self: tabA, Me: DefaultName, Room: RoomIDs[0], NowMs: baseTime.UnixMilli()}
}

func sent(body string, id uint64, at time.Time) live.Event {
	return live.Event{
		Name: EventSend, FragmentID: FragmentComposer, ID: id, At: at,
		Fields: live.NewFields(map[string]string{fieldBody: body}),
	}
}

func typed(body string, id uint64, at time.Time) live.Event {
	return live.Event{
		Name: EventDraft, FragmentID: FragmentComposer, ID: id, At: at,
		Fields: live.NewFields(map[string]string{fieldBody: body}),
	}
}

func switched(room string, id uint64, at time.Time) live.Event {
	return live.Event{
		Name: EventSwitch, FragmentID: FragmentRooms, ID: id, At: at,
		Fields: live.NewFields(map[string]string{fieldRoom: room}),
	}
}

func entered(room string, at time.Time) live.Event {
	return live.Event{
		Name: EventEntered, At: at,
		Fields: live.NewFields(map[string]string{fieldRoom: room}),
	}
}

func post(room string, seq int, author, body, clientID string, at time.Time) live.Event {
	return live.Event{
		Name: EventPosted, At: at,
		Fields: live.NewFields(map[string]string{
			fieldRoom:     room,
			fieldSeq:      strconv.Itoa(seq),
			fieldAuthor:   author,
			fieldBody:     body,
			fieldAtMs:     strconv.FormatInt(at.UnixMilli(), 10),
			fieldClientID: clientID,
		}),
	}
}

func roster(room string, presence, typing []string, at time.Time) live.Event {
	return live.Event{
		Name: EventRoster, At: at,
		Fields: live.NewFields(map[string]string{
			fieldRoom:     room,
			fieldPresence: strings.Join(presence, ","),
			fieldTyping:   strings.Join(typing, ","),
		}),
	}
}

// reduce is the reducer under test, bound to rooms the pure specs never reach:
// the transition builds effects that close over them, and a spec that cares
// about what an effect DOES builds its own rooms and runs the effect.
var reduce = Reducer(testRooms())

// sources projects what a transition scheduled into the one thing a
// specification can compare: live.Effect[Member] carries its behaviour in a function
// field, and Go cannot compare two function values.
func sources(effects []live.Effect[Member]) []string {
	if len(effects) == 0 {
		return nil
	}
	names := make([]string, 0, len(effects))
	for _, effect := range effects {
		names = append(names, effect.Source)
	}
	return names
}

var _ = Describe("§2.3 F-CHT — the feature table", func() {
	Describe("F-CHT-1: the message list is capped at 200, server-side", func() {
		It("drops the oldest and keeps the cap exactly", func() {
			state := initialState()
			for i := 1; i <= MessageCap+25; i++ {
				state, _ = reduce(state, post(RoomIDs[0], i, "ana", "m"+strconv.Itoa(i), "", baseTime))
			}
			Expect(state.Log().Len()).To(Equal(MessageCap))
			first := state.Messages()[0]
			last, _ := state.Log().Last()
			Expect(first.Seq).To(Equal(26), "the oldest 25 were dropped")
			Expect(last.Seq).To(Equal(MessageCap + 25))
		})

		// The cap is applied on the server on BOTH stacks, so the two documents
		// hold the same number of nodes by construction rather than by two
		// clients agreeing to trim the same amount. No virtualization on either
		// side — §2.3 forbids it.
		It("renders one node per message and no more", func() {
			state := initialState()
			for i := 1; i <= 5; i++ {
				state, _ = reduce(state, post(RoomIDs[0], i, "ana", "hello", "", baseTime))
			}
			Expect(strings.Count(render(LogRegion(state)), `data-bench-id="message"`)).To(Equal(5))
		})
	})

	Describe("F-CHT-2: what a message renders", func() {
		It("carries the author, the initial, the body and both timestamps", func() {
			state := initialState()
			state.NowMs = baseTime.Add(90 * time.Second).UnixMilli()
			m := Message{Seq: 1, Author: "ana", Body: "hello", AtMs: baseTime.UnixMilli()}

			html := render(MessageRow(state, m))
			Expect(html).To(ContainSubstring(`data-bench-value="hello"`))
			Expect(html).To(ContainSubstring(`data-bench-state="confirmed"`))
			Expect(html).To(ContainSubstring(`>A</span>`), "the avatar initial is a CSS circle, no image")
			Expect(html).To(ContainSubstring(`>ana</span>`))
			Expect(html).To(ContainSubstring(`>1m ago</span>`))
			Expect(html).NotTo(ContainSubstring("<img"))
		})

		DescribeTable("the relative timestamp, from two server numbers only",
			func(ageMs int64, want string) {
				s := State{NowMs: baseTime.UnixMilli() + ageMs}
				Expect(s.Age(baseTime.UnixMilli())).To(Equal(want))
			},
			Entry("under two seconds", int64(1500), "just now"),
			Entry("seconds", int64(42_000), "42s ago"),
			Entry("minutes", int64(3*60_000), "3m ago"),
			Entry("hours", int64(5*3_600_000), "5h ago"),
		)

		It("formats the absolute timestamp as UTC HH:MM:SS", func() {
			Expect(ClockLabel(baseTime.UnixMilli())).To(Equal("12:00:00"))
			Expect(ClockLabel(baseTime.Add(3661 * time.Second).UnixMilli())).To(Equal("13:01:01"))
		})
	})

	// F-CHT-3's product surface is "composer: <textarea> + Send button; Enter
	// sends, Shift+Enter newlines". The button half has always been here; the
	// keyboard half is what the library could not express until 2026-08-05.
	//
	// WHAT THESE SPECS CAN AND CANNOT SEE. They assert the SPEC THIS APP EMITS,
	// which is the half this module owns. Whether a browser then does the right
	// thing with that spec is the library's property and is driven through
	// Chromium in test/internal/conformance/keybinding_modifiers_test.go — Enter
	// raising the event with the box unchanged, Shift+Enter raising nothing with
	// the line break inserted, and the server's draft carrying it. Restating
	// that here against a rendered string would be a spec that passes on a
	// runtime that ignores the components entirely.
	//
	// So the assertion is deliberately the exact literal rather than a set of
	// ContainSubstrings: a component that moved slot — which is what a stray
	// separator or a reordering does, silently, and is the failure the grammar's
	// own panic exists to catch — changes this string and turns the spec red
	// naming both sides.
	Describe("F-CHT-3: Enter sends, Shift+Enter newlines", func() {
		It("emits the send binding and the draft binding as one attribute", func() {
			Expect(composerSpec()).To(Equal(
				"keydown:chat.send:Enter::::1:1;input:chat.draft::150"))
		})

		It("renders that attribute on the textarea, and only there", func() {
			html := render(ComposerRegion(initialState()))
			Expect(html).To(ContainSubstring(
				`data-gotth-on="keydown:chat.send:Enter::::1:1;input:chat.draft::150"`))
			// The Send button keeps its own unfiltered click binding: F-CHT-3
			// asks for a textarea AND a Send button, so the keyboard path is an
			// addition and not a replacement.
			Expect(html).To(ContainSubstring(`data-gotth-on="click:chat.send"`))
		})

		// The three reasons bench/README.md gave for this being inexpressible,
		// asserted as the three properties that replaced them — BY COMPONENT
		// SUBSCRIPT and not by substring.
		//
		// The substring form was written first and is the reason this comment
		// exists. Four mutants were run against it in a throwaway copy of this
		// module and two of them survived the entry that claims to catch them:
		// deleting PreventDefault leaves ":1;" in the string that "reason 2"
		// looked for, and moving the debounce back onto the SEND binding leaves
		// the draft binding "reason 3" looked for untouched. Both mutants are the
		// exact regressions the entries are named after. A substring of a
		// colon-separated grammar is not an assertion about a component, and the
		// spec that says so has to index.
		DescribeTable("the three components that closed the three reasons",
			func(binding, component int, want, why string) {
				Expect(componentOf(binding, component)).To(Equal(want), why)
			},
			Entry("reason 1 — component 7, the modifier state is compared", 0, 6, "1",
				"without it Shift+Enter matches the key filter and sends"),
			Entry("reason 2 — component 8, the key is taken from the browser", 0, 7, "1",
				"without it Enter sends AND the browser inserts the newline"),
			Entry("reason 3a — the SEND binding carries no debounce of its own", 0, 3, "",
				"an interval here is a send that Enter's own trailing input event can cancel"),
			Entry("reason 3b — the DRAFT binding carries its own, and only its own", 1, 3, "150",
				"before 2ab18690 this was read from the element, so the send binding above inherited it"),
		)

		// A send raised by a key is the same event a send raised by a click is,
		// and it reaches the same reducer with the same field, because both
		// bindings serialise the composer's form. Nothing in Reduce branches on
		// which one it was, and this is the spec that says so rather than
		// leaving it to be inferred from the two bindings naming one event.
		It("takes the same reducer path as the button's click", func() {
			next, effects := reduce(initialState(), sent("hi", 1, baseTime))
			Expect(next.DraftError).To(BeEmpty())
			Expect(effects).To(HaveLen(1), "one send asks the rooms once, whatever raised it")
		})
	})

	Describe("F-CHT-4: server-side validation", func() {
		DescribeTable("the message a violation renders",
			func(body, want string) {
				Expect(ValidateBody(body)).To(Equal(want))
			},
			Entry("empty", "", "Say something first."),
			Entry("one character is legal", "x", ""),
			Entry("500 is legal", strings.Repeat("x", 500), ""),
			Entry("501 is CHT-5", strings.Repeat("x", 501), "Too long by 1 characters (max 500)."),
		)

		// "violation renders an inline error next to the composer WITHOUT
		// clearing the input". The composer's own value is the browser's — the
		// textarea is rendered with no text content on purpose — so what this
		// asserts is the half the server owns: no confirmation, no generation
		// bump, and the error rendered where CHT-5's predicate looks.
		It("keeps the draft and renders the error, and does not bump the composer", func() {
			state := initialState()
			next, effects := reduce(state, sent(strings.Repeat("x", 501), 1, baseTime))

			Expect(effects).To(BeEmpty(), "a rejected send asks the rooms for nothing")
			Expect(next.DraftError).To(Equal("Too long by 1 characters (max 500)."))
			Expect(next.ComposerGen).To(Equal(state.ComposerGen),
				"the box is only cleared by a CONFIRMED send")
			Expect(render(ComposerRegion(next))).To(ContainSubstring(`data-bench-id="error"`))
		})
	})

	Describe("F-CHT-5 and F-CHT-6: presence and the typing indicator", func() {
		It("renders the roster the server sorted and decayed", func() {
			state, _ := reduce(initialState(),
				roster(RoomIDs[0], []string{"ana", "bo", "you"}, []string{"ana", "bo"}, baseTime))
			Expect(state.Presence()).To(Equal([]string{"ana", "bo", "you"}))
			Expect(state.TypingLabel()).To(Equal("2 people are typing"))
		})

		It("never counts this tab as typing", func() {
			state, _ := reduce(initialState(),
				roster(RoomIDs[0], []string{"you"}, []string{"you"}, baseTime))
			Expect(state.Typing()).To(BeEmpty())
			Expect(state.TypingLabel()).To(BeEmpty(), "nobody needs telling that they are typing")
		})

		DescribeTable("the label",
			func(names []string, want string) {
				state, _ := reduce(initialState(), roster(RoomIDs[0], nil, names, baseTime))
				Expect(state.TypingLabel()).To(Equal(want))
			},
			Entry("nobody", []string(nil), ""),
			Entry("one", []string{"ana"}, "ana is typing"),
			Entry("several", []string{"ana", "bo", "cy"}, "3 people are typing"),
		)

		// The decay is the room's job and it is swept by the replay, so this is
		// where the rule is checked rather than in the reducer.
		It("drops a name three seconds after its last signal", func() {
			rooms := testRooms()
			now := baseTime
			rooms.now = func() time.Time { return now }

			rooms.Typing(RoomIDs[0], "ana")
			Expect(rooms.rooms[0].rosterUpdate(0, now).typing).To(ContainElement("ana"))

			now = baseTime.Add(TypingDecay + time.Millisecond)
			Expect(rooms.rooms[0].rosterUpdate(0, now).typing).To(BeEmpty())
		})
	})

	Describe("F-CHT-7: the room switcher and its unread badges", func() {
		It("counts a message in another room and not one in this room", func() {
			state := initialState()
			state, _ = reduce(state, post(RoomIDs[0], 1, "ana", "here", "", baseTime))
			state, _ = reduce(state, post(RoomIDs[1], 1, "bo", "elsewhere", "", baseTime))
			state, _ = reduce(state, post(RoomIDs[1], 2, "cy", "elsewhere again", "", baseTime))

			Expect(state.UnreadIn(RoomIDs[0])).To(Equal(0))
			Expect(state.UnreadIn(RoomIDs[1])).To(Equal(2))
		})

		It("does not count the same message twice", func() {
			state := initialState()
			state, _ = reduce(state, post(RoomIDs[1], 1, "bo", "once", "", baseTime))
			state, _ = reduce(state, post(RoomIDs[1], 1, "bo", "once", "", baseTime))
			Expect(state.UnreadIn(RoomIDs[1])).To(Equal(1),
				"emitted events are best-effort; a redelivery must not be a second message")
		})

		// CHT-4 must be a ROUND TRIP on both stacks (§2.2's category error, and
		// BENCH-1's reading R-1). The reducer therefore does not move the room
		// when the click arrives — it asks — and the room moves when the server
		// answers.
		It("does not change room on the click, only on the server's answer", func() {
			state := initialState()
			asked, effects := reduce(state, switched(RoomIDs[1], 7, baseTime))
			Expect(asked.Room).To(Equal(RoomIDs[0]), "a local flip would make CHT-4 a same-frame paint")
			Expect(sources(effects)).To(Equal([]string{SourceSwitch}))

			arrived, _ := reduce(asked, entered(RoomIDs[1], baseTime))
			Expect(arrived.Room).To(Equal(RoomIDs[1]))
		})

		It("clears the badge of the room it enters", func() {
			state := initialState()
			state, _ = reduce(state, post(RoomIDs[1], 1, "bo", "unread", "", baseTime))
			Expect(state.UnreadIn(RoomIDs[1])).To(Equal(1))

			state, _ = reduce(state, entered(RoomIDs[1], baseTime))
			Expect(state.UnreadIn(RoomIDs[1])).To(Equal(0))
		})

		It("refuses a room that is not one of the three", func() {
			_, effects := reduce(initialState(), switched("../etc/passwd", 1, baseTime))
			Expect(effects).To(BeEmpty())
		})
	})

	Describe("F-CHT-8: the composer survives other people's messages", func() {
		// The composer fragment is separate from the log fragment, and its
		// Dirty function is what makes a peer's message leave it alone. A single
		// fragment covering both would re-render the composer every time
		// somebody spoke, which is the failure FR-55 names.
		It("does not re-render the composer when a peer speaks", func() {
			cfg := Config(testRooms(), testOrigins)
			composer := cfg.Fragments[1]
			Expect(composer.ID).To(Equal(FragmentComposer))

			prev := initialState()
			next, _ := reduce(prev, post(RoomIDs[0], 1, "ana", "hello", "", baseTime))
			Expect(composer.Dirty(prev, next)).To(BeFalse())
		})

		// And the deeper half: the textarea is rendered with NO text content, so
		// the runtime's controlled/uncontrolled rule leaves the user's draft
		// alone even if the composer does re-render. This is the assertion that
		// would catch somebody "helpfully" rendering the draft back into it.
		It("renders the textarea with no text content", func() {
			state := initialState()
			state.Draft = "half a sentence"
			Expect(render(ComposerRegion(state))).To(ContainSubstring(`></textarea>`),
				"a non-empty textarea is server-controlled and morph would overwrite the user's draft")
		})

		// The other side of that rule: an empty render cannot CLEAR the box, so
		// a confirmed send changes the element's identity instead.
		It("changes the textarea's id only on a confirmed send", func() {
			state := initialState()
			before := state.ComposerID()

			state, _ = reduce(state, typed("hello", 1, baseTime))
			Expect(state.ComposerID()).To(Equal(before), "typing does not replace the node")

			state, _ = reduce(state, sent("hello", 2, baseTime))
			Expect(state.ComposerID()).To(Equal(before), "nor does asking")

			state, _ = reduce(state, post(RoomIDs[0], 1, DefaultName, "hello", "2", baseTime))
			Expect(state.ComposerID()).NotTo(Equal(before), "the confirmation is what clears it")
			Expect(state.Draft).To(BeEmpty())
		})

		It("recognises its own confirmation by identifier and not by body", func() {
			state := initialState()
			state, _ = reduce(state, sent("hello", 42, baseTime))
			before := state.ComposerID()

			// Somebody else's identical message must not clear this composer.
			state, _ = reduce(state, post(RoomIDs[0], 1, "ana", "hello", "", baseTime))
			Expect(state.ComposerID()).To(Equal(before))
			Expect(state.PendingSend).To(Equal(uint64(42)))

			state, _ = reduce(state, post(RoomIDs[0], 2, DefaultName, "hello", "42", baseTime))
			Expect(state.ComposerID()).NotTo(Equal(before))
			Expect(state.PendingSend).To(BeZero())
		})
	})

	Describe("F-CHT-9: the read-only participant is refused server-side", func() {
		It("refuses a valid message from the read-only name, with a visible error", func() {
			state := initialState()
			state.Me = ReadonlyName
			state.Readonly = true

			next, effects := reduce(state, sent("perfectly fine", 1, baseTime))
			Expect(effects).To(BeEmpty(), "no effect means no message reaches the room")
			Expect(next.DraftError).To(Equal(ReadonlyError))
			Expect(render(ComposerRegion(next))).To(ContainSubstring(ReadonlyError))
		})

		// The refusal is checked twice, and not redundantly: the reducer's is
		// what a reader SEES (F-CHT-9 requires a visible error, and a
		// live.DenyError produces no render), and the effect's own is what a
		// reader cannot get past.
		It("refuses again at the effect boundary", func() {
			rooms := testRooms()
			err := rooms.SendEffect(Send{Room: RoomIDs[0], Body: "hello", Cause: 1}).
				Run(context.Background(),
					livetest.NewSession(GinkgoTB(), tabA, Member{Name: ReadonlyName, Readonly: true}),
					func(live.Event) error { return nil })
			Expect(err).To(MatchError(ContainSubstring("read-only")))
			Expect(rooms.LogOf(RoomIDs[0]).Len()).To(BeZero())
		})

		It("reads the participant name from the cookie the harness sets", func() {
			r, _ := http.NewRequest(http.MethodGet, "/chat/live", nil)
			r.AddCookie(&http.Cookie{Name: WhoCookie, Value: ReadonlyName})
			id, err := DirectoryAuthenticate(r)
			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(Equal(Member{Name: ReadonlyName, Readonly: true}))
			Expect(id.Subject()).To(Equal(ReadonlyName))
		})

		DescribeTable("a name that is not a name is the default",
			func(raw, want string) {
				Expect(NormalizeName(raw)).To(Equal(want))
			},
			Entry("empty", "", DefaultName),
			Entry("a comma would break the roster's wire form", "a,b", DefaultName),
			Entry("a plausible name", "alice", "alice"),
			Entry("the read-only name", ReadonlyName, ReadonlyName),
		)
	})
})

var _ = Describe("§2.0 the markup hooks the harness drives", func() {
	var page string

	BeforeEach(func() {
		state := initialState()
		state, _ = reduce(state, roster(RoomIDs[0], []string{"ana", "you"}, []string{"ana"}, baseTime))
		state, _ = reduce(state, post(RoomIDs[0], 1, "ana", "hello", "", baseTime))
		state, _ = reduce(state, post(RoomIDs[1], 1, "bo", "elsewhere", "", baseTime))
		page = render(Page(state))
	})

	DescribeTable("every data-bench-id the CHT-* interaction files select",
		func(id string) {
			Expect(page).To(ContainSubstring(`data-bench-id="` + id + `"`))
		},
		Entry("CHT-1/5/7/8 composer", "composer"),
		Entry("CHT-2/5/8 Send", "send"),
		Entry("CHT-2/3 message nodes", "message"),
		Entry("CHT-6 scroll container", "messages"),
		Entry("CHT-4 predicate subject", "room-title"),
		Entry("CHT-4 drive", "room-beta"),
		Entry("CHT-4 drive", "room-gamma"),
		Entry("F-CHT-7 badge", "unread-alpha"),
		Entry("F-CHT-5 roster", "presence"),
		Entry("F-CHT-6 indicator", "typing"),
	)

	It("marks the four live regions with the letters the harness observes", func() {
		for _, region := range []string{"A", "B", "C", "D"} {
			Expect(page).To(ContainSubstring(`data-bench-region="` + region + `"`))
		}
	})

	// Q-A in bench/README.md: §2.0 reads data-bench-value as the element whose
	// textContent is the predicate's subject, §2.3 reads it as an attribute
	// holding the body. BENCH-1 satisfied both; so does this.
	It("satisfies both readings of data-bench-value on a message", func() {
		Expect(page).To(ContainSubstring(`data-bench-value="hello"`))
		Expect(page).To(ContainSubstring(`<span class="body">hello</span>`))
	})

	It("renders the error hook only when there is an error", func() {
		Expect(page).NotTo(ContainSubstring(`data-bench-id="error"`))

		state := initialState()
		state.DraftError = "boom"
		Expect(render(Page(state))).To(ContainSubstring(`data-bench-id="error"`))
	})

	It("loads the shim before the runtime, undeferred, per §3.2", func() {
		shim := strings.Index(page, ShimRoute)
		runtime := strings.Index(page, "gotth-live.min.js")
		Expect(shim).To(BeNumerically(">", 0))
		Expect(runtime).To(BeNumerically(">", shim))
		Expect(page).To(ContainSubstring(`<script src="` + ShimRoute + `"></script>`))
	})

	// E5 — bounded DOM. §2.3: "≤ 200 message nodes × ≤ 8 elements = ≤ 1600;
	// whole document ≤ 2000 elements."
	It("stays inside §2.3's element bounds at a full 200-message log", func() {
		state := initialState()
		state, _ = reduce(state, roster(RoomIDs[0], []string{"ana", "bo", "cy", "dee", "eli", "fen", "gus", "hana"}, nil, baseTime))
		for i := 1; i <= MessageCap; i++ {
			state, _ = reduce(state, post(RoomIDs[0], i, "ana", "a message body", "", baseTime))
		}
		Expect(countElements(render(LogRegion(state)))).To(BeNumerically("<=", 1600))
		Expect(countElements(render(Page(state)))).To(BeNumerically("<=", 2000))
	})
})

var _ = Describe("§2.5 the committed fixture", func() {
	It("reads the same bytes the Next.js side reads, and says so", func() {
		fixture, err := LoadFixture(DefaultFixtureDir)
		if errors.Is(err, fs.ErrNotExist) {
			Skip("run `npm run fixtures` in bench/ first (§2.5)")
		}
		Expect(err).NotTo(HaveOccurred())

		want, err := os.ReadFile(DefaultFixtureDir + "/chat/ticks.jsonl.sha256")
		Expect(err).NotTo(HaveOccurred())
		Expect(fixture.SHA256).To(Equal(strings.Fields(string(want))[0]),
			"neither server generates data; both read the same bytes")
		Expect(fixture.Base.Presence).To(HaveLen(8), "§2.3's eight simulated peers")
		Expect(fixture.Ticks).NotTo(BeEmpty())
	})

	// C-33. The skip above is the only thing standing between a CI run on a
	// fresh checkout — where bench/fixtures/*/ticks.jsonl is gitignored and
	// absent — and a red suite, and it was guarded with os.IsNotExist, which
	// DOES NOT UNWRAP. LoadFixture wraps with %w to name the file and the
	// command that regenerates it, so the guard was false for the one error it
	// exists to catch and five specs across two modules failed where the gate
	// printed "skipped".
	//
	// This pins both halves: the wrap stays recognisable, and the idiom that
	// could not see it is asserted to still not see it, so a future author who
	// reaches for the shorter spelling finds out here rather than in CI.
	It("wraps a missing fixture so the skip guard can still recognise it", func() {
		_, err := LoadFixture(GinkgoT().TempDir())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, fs.ErrNotExist)).To(BeTrue(),
			"the guard on the digest spec is errors.Is(err, fs.ErrNotExist); keep LoadFixture wrapping with %w")
		Expect(os.IsNotExist(err)).To(BeFalse(),
			"os.IsNotExist does not unwrap, which is exactly how this guard was wrong")
	})

	It("replays a tick into the rooms without generating anything", func() {
		rooms := testRooms()
		rooms.now = func() time.Time { return baseTime }
		rooms.applyTick(Tick{N: 0, E: []ChatEvent{
			{Kind: FixtureMsg, Room: RoomIDs[0], Author: "ana", Body: "from the fixture"},
			{Kind: FixtureTyping, Room: RoomIDs[0], Author: "bo"},
		}})

		last, ok := rooms.LogOf(RoomIDs[0]).Last()
		Expect(ok).To(BeTrue())
		Expect(last.Body).To(Equal("from the fixture"))
		Expect(last.Author).To(Equal("ana"))
		Expect(last.ClientID).To(BeEmpty(), "a fixture peer has no client to echo")
	})
})

var _ = Describe("§2.0 the shared assets, byte for byte", func() {
	It("resolves the harness shim from the app's own directory", func() {
		shim, err := LoadShim(DefaultShimPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(shim)).To(ContainSubstring("window.__bench = bench;"))
	})

	It("serves the stylesheet the Next.js side serves", func() {
		want, err := os.ReadFile("../next/src/app/chat.css")
		if errors.Is(err, fs.ErrNotExist) {
			Skip("the Next.js side is not in this checkout")
		}
		Expect(err).NotTo(HaveOccurred())
		Expect(stylesheet).To(Equal(want))
	})
})

var _ = Describe("Determinism (FR-15)", func() {
	// FR-15's mandatory harness. It is also the property §2.5's conformance test
	// rests on: both servers must emit the same logical state for tick N, and a
	// reducer whose output depended on when it ran could not.
	It("replays the whole session to the same state and the same effects", func() {
		livetest.ReplayN(GinkgoTB(), reduce, initialState(), mixedLog(), 25)
	})

	It("replays to the conversation the log describes", func() {
		state := initialState()
		for _, ev := range mixedLog() {
			state, _ = reduce(state, ev)
		}
		Expect(state.Room).To(Equal(RoomIDs[1]))
		Expect(state.Logs[0].Len()).To(Equal(3))
		Expect(state.Logs[1].Len()).To(Equal(1))
		Expect(state.Draft).To(BeEmpty(), "the confirmed send cleared the composer")
		Expect(state.ComposerGen).To(Equal(1))
	})

	It("declares every fragment that its own markup changes", func() {
		livetest.AssertDirtyComplete(GinkgoTB(), Config(testRooms(), testOrigins), initialState(), mixedLog())
	})

	// Over-declaring is safe but not free, and §4.6's wire-byte row is counting
	// the patches these decide not to send.
	DescribeTable("a fragment stays clean when its own inputs did not move",
		func(index int, id string, mutate func(State) State) {
			cfg := Config(testRooms(), testOrigins)
			fragment := cfg.Fragments[index]
			Expect(fragment.ID).To(Equal(id))

			prev := initialState()
			Expect(fragment.Dirty(prev, mutate(prev))).To(BeFalse())
		},
		Entry("the log ignores an unread badge", 0, FragmentLog,
			func(s State) State { s.Unread[1] = 3; return s }),
		Entry("the composer ignores the roster", 1, FragmentComposer,
			func(s State) State { s.Rosters[0] = &Roster{Presence: []string{"ana"}}; return s }),
		Entry("the roster ignores a draft", 2, FragmentRoster,
			func(s State) State { s.Draft = "typing"; return s }),
		Entry("the switcher ignores a message", 3, FragmentRooms,
			func(s State) State { s.Logs[0] = s.Logs[0].with(Message{Seq: 1}); return s }),
	)
})

// mixedLog is one session's whole event log: a draft, a rejected send, a
// confirmed send, peer traffic in two rooms, a roster change and a room switch.
// It is what ReplayN and AssertDirtyComplete replay.
func mixedLog() []live.Event {
	at := baseTime
	next := func(d time.Duration) time.Time { at = at.Add(d); return at }

	return []live.Event{
		roster(RoomIDs[0], []string{"ana", "bo", "you"}, nil, next(time.Second)),
		post(RoomIDs[0], 1, "ana", "morning", "", next(time.Second)),
		typed("hel", 2, next(200*time.Millisecond)),
		typed("hello", 3, next(200*time.Millisecond)),
		sent(strings.Repeat("x", 501), 4, next(time.Second)),
		sent("hello", 5, next(time.Second)),
		post(RoomIDs[0], 2, DefaultName, "hello", "5", next(5*time.Millisecond)),
		roster(RoomIDs[0], []string{"ana", "bo", "you"}, []string{"bo"}, next(time.Second)),
		post(RoomIDs[1], 1, "cy", "in beta", "", next(time.Second)),
		post(RoomIDs[0], 3, "bo", "and back", "", next(time.Second)),
		switched(RoomIDs[1], 6, next(time.Second)),
		entered(RoomIDs[1], next(5*time.Millisecond)),
	}
}

// testRooms builds an empty set of rooms with no fixture behind it. The replay
// is never started, so nothing in a spec races a tick.
func testRooms() *Rooms {
	return NewRooms(&Fixture{Base: Base{Presence: []string{"ana", "bo"}}})
}

// countElements counts opening tags, which is close enough for an E5 guard and
// deliberately does not parse: the browser's own count is what the smoke run
// reports against the same bound.
func countElements(html string) int {
	n := 0
	for i := 0; i+1 < len(html); i++ {
		if html[i] != '<' {
			continue
		}
		c := html[i+1]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			n++
		}
	}
	return n
}
