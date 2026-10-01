// The gotth-live side of equivalence-spec §2.3's chat room.
//
// It is built to §2.3's F-CHT table and not to examples/chat, which is a
// different program: that example has one room, no typing indicator and no
// unread badges, and §10 puts this app at bench/apps/chat/gotth precisely
// because the two are not the same thing (bench/README.md, ambiguity Q-E).
//
// Every visible string here is a transcription of
// bench/apps/chat/next/src/lib/core.ts, because E1 ("same product surface") and
// E3 ("same data") are checked by comparing rendered DOM: "just now", "N people
// are typing", "Too long by N characters (max 500)." and the avatar initial are
// the wire format of the equivalence claim, and neither side may derive one of
// them from the browser's clock or locale.
package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/a-h/templ"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// Rooms is §2.3's "three fixed rooms". The order is the render order and the
// index into every per-room array in State, so it is a slice and never a map:
// a fragment's render must be a pure function of state and produce
// byte-identical HTML for the same state, and Go's map iteration order is the
// standard way to break that without noticing.
var RoomIDs = []string{"alpha", "beta", "gamma"}

// RoomIndex returns the position of a room id, or -1. It is the bounds check
// every fold does before touching a per-room array.
func RoomIndex(id string) int { return slices.Index(RoomIDs, id) }

const (
	// MessageCap is F-CHT-1's "hard cap 200 rendered messages (oldest
	// dropped)". It is applied on the SERVER so the two stacks' DOM node counts
	// are equal by construction rather than by two clients agreeing to trim the
	// same amount. No virtualization on either side — forbidden.
	MessageCap = 200

	// BodyMin and BodyMax are F-CHT-4's "body length 1..500".
	BodyMin = 1
	BodyMax = 500

	// TypingDecay is F-CHT-6's window. A name stops counting as typing three
	// seconds after its last signal, on the server, so every viewer agrees on
	// the count.
	TypingDecay = 3 * time.Second

	// ReadonlyName is F-CHT-9's designated read-only participant. Identity is a
	// cookie, not a login: the harness sets bench_who=readonly before CHT-8 and
	// the server refuses that name's sends. Keeping the refusal server-side is
	// the point of the feature — a disabled button would prove nothing, because
	// the thing under test is that the SERVER says no.
	ReadonlyName = "readonly"

	// DefaultName is who a browser with no bench_who cookie is.
	DefaultName = "you"
)

// The fragment identifiers. A fragment ID is a contract in three places at
// once — the Config, the markup's data-gotth-region attribute, and every patch
// frame on the wire — so each is a constant and a typo in one of them is a
// region that silently stops updating.
//
// The split is the point of the app. §2.3 asks for a message list, a composer,
// a roster and a room switcher that update independently; one fragment covering
// all four would re-render the composer every time a peer spoke, which is the
// failure F-CHT-8 and FR-55 name. The letters are the data-bench-region values
// the Next.js component uses, kept identical because §3.1's ROI for the
// paint_present cross-check is the region's bounding box and the two stacks
// must be hashing the same pixels.
const (
	FragmentLog      = "chat.log"      // region A
	FragmentComposer = "chat.composer" // region B
	FragmentRoster   = "chat.roster"   // region C
	FragmentRooms    = "chat.rooms"    // region D
)

// The event names a browser may send. Config.Events is default-deny: a name not
// registered there is refused with UNKNOWN_EVENT before the reducer runs. One
// name per operation rather than one name carrying a discriminator, because an
// allowlist of three names bounds what a hostile client can ask for where one
// name and a "kind" field bounds nothing.
const (
	EventSend   = "chat.send"
	EventDraft  = "chat.draft"
	EventSwitch = "chat.switch"
)

// The events the rooms emit into subscribed sessions.
//
// They are deliberately NOT in Config.Events. Registration is what makes a name
// sendable by a browser, and a client that could send chat.posted could put
// words in another participant's mouth: the author is stamped by the executor
// from the session's authenticated identity, and an event bypassing the
// executor bypasses that stamp. Events an effect emits never came from the wire
// and never pass through the registration check.
const (
	EventPosted  = "chat.posted"
	EventRoster  = "chat.roster"
	EventEntered = "chat.entered"
)

// The field names on the wire and on the events the rooms emit.
const (
	fieldBody     = "body"
	fieldRoom     = "room"
	fieldSeq      = "seq"
	fieldAuthor   = "author"
	fieldAtMs     = "at_ms"
	fieldClientID = "client_id"
	fieldPresence = "presence"
	fieldTyping   = "typing"
)

// Message is one line of a room's log. It is a value: a session holds a
// consistent picture rather than a window onto something still moving.
type Message struct {
	// Seq is monotonic per room. It is the render key, the unread high-water
	// mark, and the DOM's data-bench-seq.
	Seq int
	// Author is stamped by the executor from the session's identity, never from
	// the wire.
	Author string
	Body   string
	AtMs   int64
	// ClientID echoes the sending session's own event identifier, so the sender
	// can recognise its own message coming back and clear the composer. It is
	// empty for fixture peers.
	ClientID string
}

// Log is one room's messages as one immutable value.
//
// Immutable and held behind a pointer, for the reason examples/chat and
// examples/dashboard both write out at length: internal/session's state
// comparison reports a state type that is not comparable as changed on EVERY
// transition, so a State carrying a slice directly would bump the version and
// ask every fragment's Dirty about a change that did not happen.
type Log struct {
	Version uint64
	Entries []Message
}

func (l *Log) entries() []Message {
	if l == nil {
		return nil
	}
	return l.Entries
}

// Len is the rendered message count, which F-CHT-1 caps at 200.
func (l *Log) Len() int { return len(l.entries()) }

// Last is the newest message, for a spec and for the "is this mine" check.
func (l *Log) Last() (Message, bool) {
	e := l.entries()
	if len(e) == 0 {
		return Message{}, false
	}
	return e[len(e)-1], true
}

// with returns the log that results from one new message, trimmed to
// MessageCap. The receiver is not touched.
//
// It copies. That is the cost of an immutable log and it is a real one — at 200
// entries it is a 200-element copy per message per session — and it is the cost
// the benchmark should be measuring rather than one an author optimised away
// with a mutable shared slice that no reducer could safely hold.
func (l *Log) with(m Message) *Log {
	base := l.entries()
	keep := min(len(base), MessageCap-1)

	next := make([]Message, 0, keep+1)
	next = append(next, base[len(base)-keep:]...)
	next = append(next, m)
	return &Log{Version: l.version() + 1, Entries: next}
}

func (l *Log) version() uint64 {
	if l == nil {
		return 0
	}
	return l.Version
}

// Roster is F-CHT-5's participant list and F-CHT-6's typing set for one room,
// as one immutable value. Both are already sorted and already decayed by the
// server, so every viewer agrees and no render sorts.
type Roster struct {
	Version  uint64
	Presence []string
	Typing   []string
}

func (r *Roster) version() uint64 {
	if r == nil {
		return 0
	}
	return r.Version
}

func (r *Roster) presence() []string {
	if r == nil {
		return nil
	}
	return r.Presence
}

func (r *Roster) typing() []string {
	if r == nil {
		return nil
	}
	return r.Typing
}

// State is one browser tab's view of the chat.
//
// Every field is either something the server owns or something derived from it.
// There is no client state: a reload throws this away and rebuilds it from the
// rooms.
//
// The per-room arrays are the shape the library's event payload forces, and it
// is worth naming rather than discovering. live.Fields carries strings, so an
// emitted event cannot hand a session a pointer to a shared immutable log — and
// a room switch that had to be answered with 200 messages' worth of events
// would fill a 64-deep mailbox. So a session folds every room's traffic (which
// it must do anyway for F-CHT-7's unread badge) and a switch is then a change
// of index. The cost is three logs' worth of slice headers per session instead
// of one, and it is stated in bench/README.md rather than left in the memory
// numbers for somebody to find.
type State struct {
	Self     live.ID
	Me       string
	Readonly bool

	// Room is the room this tab is looking at. It is changed by the SERVER's
	// answer to a switch (EventEntered) and never by the reducer's own reading
	// of the request, because CHT-4 must be a round trip on both stacks: a
	// reducer that flipped it locally would make this a same-frame paint here
	// and a Server Action there, which is the category error §2.2 exists to
	// keep out of the tables.
	Room string

	Logs    [3]*Log
	Rosters [3]*Roster

	// Unread is F-CHT-7's per-room badge, and LastSeq is the high-water mark it
	// needs: without one, every roster tick would increment the badge.
	Unread  [3]int
	LastSeq [3]int

	// Draft is the server's copy of the composer, debounced. The TEXTAREA IS
	// NOT RENDERED FROM IT — see view.templ — so this is only what the
	// character counter and the validation message are computed from, and a
	// stale copy costs a slightly stale counter rather than a lost keystroke.
	Draft      string
	DraftError string

	// ComposerGen changes only on a CONFIRMED send, and it is the textarea's id
	// suffix. That is how the box is cleared: the runtime treats a textarea the
	// server renders empty as uncontrolled and leaves the user's text alone
	// (which is what preserves a draft across a peer's message, F-CHT-8), so an
	// empty render cannot clear it. Changing the id makes morph's id match fail,
	// the node is replaced rather than reconciled, and the replacement is empty.
	ComposerGen int

	// PendingSend is the identifier of the event whose send has not come back
	// yet. The confirmation is recognised by it rather than by the body, so two
	// identical messages do not clear each other's composer.
	PendingSend uint64

	// NowMs is the server's clock as of the transition being rendered. A render
	// may not read a clock — the same state must produce byte-identical HTML,
	// or the comparison that suppresses an unnecessary patch breaks — so the
	// relative timestamps are computed from this and the message's own stamp.
	NowMs int64
}

/* ------------------------------------------------------------- derived ---- */

// Log returns the current room's log.
func (s State) Log() *Log {
	if i := RoomIndex(s.Room); i >= 0 {
		return s.Logs[i]
	}
	return nil
}

// Messages is what region A renders: at most MessageCap entries, oldest first.
func (s State) Messages() []Message { return s.Log().entries() }

// Presence is F-CHT-5's list for the current room.
func (s State) Presence() []string { return s.roster().presence() }

// Typing is F-CHT-6's already-decayed list for the current room, without this
// tab's own name: a session never counts itself as typing, because nobody needs
// telling. The exclusion is here rather than in the roster because the roster is
// one shared value every viewer of the room holds, and "who is typing" is the
// one thing in it that differs per viewer.
func (s State) Typing() []string {
	all := s.roster().typing()
	out := make([]string, 0, len(all))
	for _, name := range all {
		if name != s.Me {
			out = append(out, name)
		}
	}
	return out
}

func (s State) roster() *Roster {
	if i := RoomIndex(s.Room); i >= 0 {
		return s.Rosters[i]
	}
	return nil
}

// UnreadIn is F-CHT-7's badge for one room.
func (s State) UnreadIn(room string) int {
	if i := RoomIndex(room); i >= 0 {
		return s.Unread[i]
	}
	return 0
}

// TypingLabel is F-CHT-6's "N people are typing", or nothing at all.
func (s State) TypingLabel() string {
	names := s.Typing()
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0] + " is typing"
	default:
		return strconv.Itoa(len(names)) + " people are typing"
	}
}

// TypingCount is the same fact as a number, for data-bench-value.
func (s State) TypingCount() string { return strconv.Itoa(len(s.Typing())) }

// MessageCount is the rendered message count, which the harness reads as
// [data-bench-id=count].
func (s State) MessageCount() string { return strconv.Itoa(s.Log().Len()) }

// Remaining is the character counter beside the composer.
//
// It is the one thing in region B that changes when a key is pressed, and that
// is not decoration: §3.1's paint signal is a MutationObserver, and typing into
// a textarea changes an IDL property without mutating the DOM at all. Without a
// sibling that moves, CHT-1's observer would never fire on either stack. The
// Next.js side's counter is local state and paints in the same frame; this one
// is a server round trip, and bench/README.md says so in those words rather
// than leaving the number to be read as an implementation defect.
func (s State) Remaining() string { return strconv.Itoa(BodyMax - utf8.RuneCountInString(s.Draft)) }

// ComposerID is the textarea's id, and the label's `for`. See State.ComposerGen.
func (s State) ComposerID() string { return "chat-body-" + strconv.Itoa(s.ComposerGen) }

// Mine reports whether an author is this tab's participant, for the message's
// own class.
func (s State) Mine(author string) bool { return author == s.Me }

// Age is F-CHT-2's relative timestamp, computed from two server numbers so the
// browser's clock never enters the arithmetic.
func (s State) Age(atMs int64) string {
	ms := s.NowMs - atMs
	switch {
	case ms < 2000:
		return "just now"
	case ms < 60_000:
		return strconv.FormatInt(ms/1000, 10) + "s ago"
	case ms < 3_600_000:
		return strconv.FormatInt(ms/60_000, 10) + "m ago"
	default:
		return strconv.FormatInt(ms/3_600_000, 10) + "h ago"
	}
}

// Initial is F-CHT-2's avatar initial: a CSS circle, no image (§2's no-images
// bound).
func Initial(author string) string {
	if author == "" {
		return "?"
	}
	return strings.ToUpper(author[:1])
}

// ClockLabel is F-CHT-2's absolute timestamp.
//
// It is formatted with explicit arithmetic rather than a locale-aware formatter
// because the container's locale and timezone are not part of the equivalence
// contract, and the Next.js side formats the same UTC HH:MM:SS from the same
// epoch milliseconds.
func ClockLabel(atMs int64) string {
	total := atMs / 1000
	return pad(int((total/3600)%24)) + ":" + pad(int((total/60)%60)) + ":" + pad(int(total%60))
}

func pad(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// ValidateBody is F-CHT-4's validation, as a pure function.
//
// It runs on the server, which is what the feature asks for, and the client
// does NOT pre-empt it: a client-side length guard would make CHT-5 a local
// paint on this stack and a round trip on the other.
func ValidateBody(body string) string {
	n := utf8.RuneCountInString(body)
	if n < BodyMin {
		return "Say something first."
	}
	if n > BodyMax {
		return fmt.Sprintf("Too long by %d characters (max %d).", n-BodyMax, BodyMax)
	}
	return ""
}

// ReadonlyError is F-CHT-9's refusal, word for word as the Next.js side renders
// it.
const ReadonlyError = "You are a read-only participant in this room."

/* ------------------------------------------------------------- reducer ---- */

// Reducer returns the pure state transition, bound to the rooms its effects
// act on.
//
// It is a constructor because a live.Effect[Member] carries its own behaviour since the
// 2026-09-03 ruling, so a reducer scheduling one has to hold what that effect
// closes over.
//
// It reads no clock, performs no I/O and touches the shared rooms not at all. A
// send does not append a message: it returns an effect asking the rooms to
// accept one, and this session learns the result the same way every other tab
// does — through a posted event. That is what server-authoritative means
// concretely, and it is why CHT-2's predicate can require
// data-bench-state="confirmed" and mean it.
func Reducer(rooms *Rooms) live.Reducer[State, Member] {
	return func(state State, ev live.Event) (State, []live.Effect[Member]) {
		if !ev.At.IsZero() {
			state.NowMs = ev.At.UnixMilli()
		}

		switch ev.Name {
		case EventDraft:
			// CHT-1. The character itself is already on screen — the browser put it
			// there — and this is the debounced copy the counter and the validation
			// message are computed from. It also IS this session's typing signal:
			// F-CHT-6 needs one, and a separate heartbeat would be a second event
			// carrying the same fact.
			state.Draft = ev.Fields.Get(fieldBody)
			return state, []live.Effect[Member]{rooms.TypingEffect(state.Room)}

		case EventSend:
			return send(rooms, state, ev)

		case EventSwitch:
			room := ev.Fields.Get(fieldRoom)
			if RoomIndex(room) < 0 || room == state.Room {
				return state, nil
			}
			// State.Room is NOT changed here; see its doc comment. The effect asks
			// the rooms to move this session, and EventEntered is the answer.
			return state, []live.Effect[Member]{rooms.SwitchEffect(Switch{Room: room, Cause: ev.ID})}

		case EventPosted:
			return posted(state, ev)

		case EventRoster:
			i := RoomIndex(ev.Fields.Get(fieldRoom))
			if i < 0 {
				return state, nil
			}
			state.Rosters[i] = &Roster{
				Version:  state.Rosters[i].version() + 1,
				Presence: splitNames(ev.Fields.Get(fieldPresence)),
				Typing:   splitNames(ev.Fields.Get(fieldTyping)),
			}
			return state, nil

		case EventEntered:
			room := ev.Fields.Get(fieldRoom)
			i := RoomIndex(room)
			if i < 0 {
				return state, nil
			}
			state.Room = room
			// F-CHT-7: entering a room clears its badge.
			state.Unread[i] = 0
			return state, nil

		case live.EffectFailedEvent:
			return state, retrySubscription(rooms, ev)
		}

		// An unknown name cannot reach here from a browser — the library refuses
		// unregistered names before the reducer runs — so anything arriving here is
		// something the library synthesised and this application has no answer for.
		return state, nil
	}
}

func send(rooms *Rooms, state State, ev live.Event) (State, []live.Effect[Member]) {
	body := ev.Fields.Get(fieldBody)
	// The draft is set from the submitted body rather than left at whatever the
	// debounce last saw, so the counter and the error agree with what was
	// actually sent.
	state.Draft = body

	// F-CHT-9 first, because it is about authorization and not about the body:
	// a read-only participant sending a VALID message must still be refused,
	// and the error a reader sees must say why.
	if state.Readonly {
		state.DraftError = ReadonlyError
		return state, nil
	}
	if invalid := ValidateBody(body); invalid != "" {
		state.DraftError = invalid
		return state, nil
	}

	state.DraftError = ""
	state.PendingSend = ev.ID
	return state, []live.Effect[Member]{rooms.SendEffect(Send{Room: state.Room, Body: body, Cause: ev.ID})}
}

func posted(state State, ev live.Event) (State, []live.Effect[Member]) {
	i := RoomIndex(ev.Fields.Get(fieldRoom))
	if i < 0 {
		return state, nil
	}
	seq, err := strconv.Atoi(ev.Fields.Get(fieldSeq))
	if err != nil || seq <= state.LastSeq[i] {
		// Out of order or already folded. Dropping is correct rather than
		// merely safe: emitted events are best-effort, and a message folded
		// twice would appear twice.
		return state, nil
	}
	atMs, _ := strconv.ParseInt(ev.Fields.Get(fieldAtMs), 10, 64)

	m := Message{
		Seq:      seq,
		Author:   ev.Fields.Get(fieldAuthor),
		Body:     ev.Fields.Get(fieldBody),
		AtMs:     atMs,
		ClientID: ev.Fields.Get(fieldClientID),
	}
	state.LastSeq[i] = seq
	state.Logs[i] = state.Logs[i].with(m)

	if RoomIDs[i] != state.Room {
		// F-CHT-7: a message in a room this tab is not looking at is one unread.
		state.Unread[i]++
	}

	// CHT-2's confirmation. Recognised by the identifier this session minted,
	// not by the body, so two identical messages do not clear each other's
	// composer.
	if state.PendingSend != 0 && m.ClientID == strconv.FormatUint(state.PendingSend, 10) {
		state.PendingSend = 0
		state.Draft = ""
		state.DraftError = ""
		state.ComposerGen++
	}
	return state, nil
}

// retrySubscription decides what to do about a failed effect.
//
// The only failure worth acting on is a dead subscription: without one the tab
// keeps rendering the last log it saw and stops learning about anybody else's
// messages — it looks right while being wrong, where a failed send is visible
// immediately because the message does not appear. It re-subscribes only when
// the library says the failure was transient; re-running a terminal failure
// re-runs whatever made it terminal, and an unreadable classification parses as
// false.
func retrySubscription(rooms *Rooms, ev live.Event) []live.Effect[Member] {
	retryable, _ := strconv.ParseBool(ev.Fields.Get(live.EffectFailedRetryableField))
	if retryable && ev.Fields.Get(live.EffectFailedSourceField) == SourceSubscribe {
		return []live.Effect[Member]{rooms.SubscribeEffect()}
	}
	return nil
}

// splitNames parses a comma-joined name list back out of an event field.
//
// Names are either fixture peers or a normalised bench_who, both of which match
// [A-Za-z0-9_-]+, so the comma is unambiguous. That is asserted by the
// normalisation in bench.go rather than assumed here.
func splitNames(joined string) []string {
	if joined == "" {
		return nil
	}
	return strings.Split(joined, ",")
}

/* -------------------------------------------------------------- config ---- */

// Config builds the live application over the shared rooms.
//
// Everything security-relevant is set here and nothing is left to a default,
// because live.New refuses a Config with a hole in it rather than starting with
// one. The production posture for each field is on the field.
func Config(rooms *Rooms, origins []string) live.Config[State, Member] {
	return live.Config[State, Member]{
		// Init runs once per connection, before the first snapshot. It joins
		// every room — which both reads the current logs and registers this
		// session for pushes, under one lock, so no message can slip through the
		// gap between the two — and asks for the subscription pump.
		Init: func(ctx context.Context, s live.Session[Member]) (State, []live.Effect[Member], error) {
			member := s.Identity()
			room := RoomFromContext(ctx)
			snap := rooms.Join(s.ID(), member.Name, room)

			state := State{
				Self:     s.ID(),
				Me:       member.Name,
				Readonly: member.Readonly,
				Room:     room,
				Logs:     snap.Logs,
				Rosters:  snap.Rosters,
				LastSeq:  snap.LastSeq,
				NowMs:    time.Now().UnixMilli(),
			}
			return state, []live.Effect[Member]{rooms.SubscribeEffect()}, nil
		},

		Reduce: Reducer(rooms),

		Fragments: []live.Fragment[State]{
			{
				ID:     FragmentLog,
				Render: func(s State) templ.Component { return LogRegion(s) },
				// Everything LogRegion renders and nothing else. Widening this
				// is free — a render whose bytes did not move is suppressed —
				// and narrowing it is the one mistake that produces a stale
				// region in production and nothing at all in development.
				// livetest.AssertDirtyComplete is what holds it.
				//
				// NowMs is in the comparison because every message's relative
				// timestamp is derived from it: a log whose entries did not
				// change still renders differently a second later.
				Dirty: func(prev, next State) bool {
					return prev.Room != next.Room ||
						prev.Log() != next.Log() ||
						prev.NowMs/1000 != next.NowMs/1000
				},
			},
			{
				ID:     FragmentComposer,
				Render: func(s State) templ.Component { return ComposerRegion(s) },
				Dirty: func(prev, next State) bool {
					return prev.Draft != next.Draft ||
						prev.DraftError != next.DraftError ||
						prev.ComposerGen != next.ComposerGen ||
						prev.Me != next.Me
				},
			},
			{
				ID:     FragmentRoster,
				Render: func(s State) templ.Component { return RosterRegion(s) },
				Dirty: func(prev, next State) bool {
					return prev.Room != next.Room || prev.roster() != next.roster()
				},
			},
			{
				ID:     FragmentRooms,
				Render: func(s State) templ.Component { return RoomsRegion(s) },
				Dirty: func(prev, next State) bool {
					return prev.Room != next.Room || prev.Unread != next.Unread
				},
			},
		},

		Events: []string{EventSend, EventDraft, EventSwitch},

		Teardown: func(_ context.Context, s live.Session[Member], _ State) { rooms.Leave(s.ID()) },

		// A real allowlist, not live.AnyOrigin. PRODUCTION replaces it with the
		// one scheme-and-host the page is served from.
		Origins: origins,

		// A real Authenticate, not live.Anonymous: F-CHT-9 needs an identity to
		// refuse, and live.Anonymous would make every tab the same subject with
		// nothing for the room to distinguish.
		Authenticate: DirectoryAuthenticate,

		// Authorize is a real check and it is deliberately NOT where F-CHT-9's
		// refusal lands. A live.DenyError rejects the event before the reducer
		// runs, which means no render, which means no VISIBLE error — and
		// "rejected server-side with a visible error" is the whole of F-CHT-9.
		// The library has no application hook that can render a denial (there
		// is no patch hook by design, api-surface §7.1), so the refusal is in
		// the reducer, where it can be rendered, and this hook enforces the one
		// rule that does not need to be seen: an identity that is not a Member
		// of this application closes the connection.
		Authorize: Authorize,

		// live.NoCSRFCheck is safe here ONLY because Origins above is a real
		// allowlist: the origin check is then the whole of the CSRF posture,
		// which is the condition the library's own doc comment states.
		// PRODUCTION that authenticates with a cookie adds a token bound to the
		// application session.
		CSRF: live.NoCSRFCheck,
	}
}

// Member is this application's live.IIdentity: a participant name and whether it
// may post.
//
// It is immutable for the life of a connection, which is why the read-only role
// is decided at Authenticate and not re-read per event.
type Member struct {
	Name     string
	Readonly bool
}

// Subject is the stable, non-secret identifier the library logs and counts
// sessions per.
func (m Member) Subject() string { return m.Name }

// Authorize runs before the reducer for every event, at the single mailbox
// ingress, so a new event name cannot skip it.
//
// It permits everything. The shape check that used to stand here — "is this
// identity a Member" — is a compile-time fact since the session became typed by
// the identity DirectoryAuthenticate produced, so there is nothing left for a
// runtime branch to be wrong about.
func Authorize(_ context.Context, s live.Session[Member], _ live.Event) error {
	return nil
}
