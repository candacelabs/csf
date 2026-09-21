package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/a-h/templ"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The fragment identifiers. They are constants because a fragment ID is a
// contract in three places at once — the Config, the markup's
// data-gotth-region attribute, and every patch frame on the wire — and a typo
// in any one of them is a region that silently stops updating.
//
// There are three of them, and the split is the whole reason this example
// exists. The log is everybody's; the composer is yours alone; the roster is
// derived from who is connected. A single fragment covering all three would
// re-render your half-typed message every time somebody else spoke, which is
// exactly the failure FR-55 names.
const (
	FragmentLog      = "chat.log"
	FragmentComposer = "chat.composer"
	FragmentRoster   = "chat.roster"
)

// The event names the browser may send.
//
// Config.Events is default-deny: a name that is not registered there is
// refused with UNKNOWN_EVENT before the reducer runs. One name per operation
// rather than one name carrying a "kind" field, for the same reason the
// counter gives — an allowlist of three names bounds what a hostile client can
// ask for, where one name and a discriminator field bounds nothing.
const (
	EventSend  = "chat.send"
	EventDraft = "chat.draft"
	EventClear = "chat.clear"
	EventPurge = "chat.purge"
)

// The events the room emits into every subscribed session when it changes.
//
// They are deliberately NOT in Config.Events. Registration is what makes a
// name sendable by a browser, and a client that could send chat.posted could
// put words in another member's mouth: the author is stamped by the executor
// from the session's authenticated identity, and an event bypassing the
// executor bypasses that. Events an effect emits never came from the wire and
// never pass through the registration check.
const (
	EventPosted   = "chat.posted"
	EventPresence = "chat.presence"
	EventPurged   = "chat.purged"
)

// The field names on the wire and on the events the room emits.
const (
	fieldBody    = "body"
	fieldSeq     = "seq"
	fieldAuthor  = "author"
	fieldAtMilli = "at_ms"
	fieldVersion = "version"
	fieldMembers = "members"
	fieldBy      = "by"
)

// The injected panics, FR-23's three sites, reachable by typing them into the
// composer.
//
// They are commands rather than a build tag or a hidden flag because an error
// boundary you cannot provoke in the running application is an error boundary
// nobody has watched work. Each contains to the session that typed it; the
// other tabs in the room keep serving, which is the half of FR-23 that a
// single session cannot show.
const (
	CmdPanicReducer = "/panic reducer"
	CmdPanicEffect  = "/panic effect"
	CmdPanicRender  = "/panic render"
)

// MaxBodyRunes is the length limit the server enforces. It is counted in runes
// rather than bytes because the user is counting characters.
const MaxBodyRunes = 280

// MaxHistory is how many messages a session keeps. It matches the room's own
// cap: the room trims when it posts and a session trims when it folds, so a
// session that joined an hour ago and one that joined a second ago hold the
// same log.
const MaxHistory = 50

// Role is what an identity is allowed to do. It is part of the identity rather
// than of the session state, because Config.Authorize runs against the
// identity bound at the handshake and state has not been reduced yet when it
// runs.
type Role uint8

// The roles. Four rather than two, because per-event authorization is only
// demonstrated by a rule that says yes to one identity and no to another for
// the same event name.
const (
	RoleObserver  Role = iota + 1 // may read the room, may not post to it
	RoleMember                    // may post
	RoleModerator                 // may post and may clear the room
	RoleBanned                    // may do nothing; the connection closes
)

// String names the role for the roster line and for a denial's operator-facing
// reason.
func (r Role) String() string {
	switch r {
	case RoleObserver:
		return "observer"
	case RoleMember:
		return "member"
	case RoleModerator:
		return "moderator"
	case RoleBanned:
		return "banned"
	}
	return "unknown"
}

// Member is this application's live.IIdentity: a name and what it may do.
//
// Identity is immutable for the life of a connection, which is why the role
// lives here. A member promoted to moderator is a member until they reconnect,
// and that is a property of the library rather than of this example.
type Member struct {
	Name string
	Role Role
}

// Subject is the stable, non-secret identifier the library logs and counts
// sessions against. It is the member's name, which is also what the roster
// shows: a subject must not be a token, and this one could not be mistaken for
// one.
func (m Member) Subject() string { return m.Name }

// Message is one thing somebody said. It is a value, and the Log holding it is
// never mutated after construction, which is what lets a session's state stay
// comparable — see State.Room.
type Message struct {
	Seq         uint64
	Author      string
	Body        string
	AtUnixMilli int64
}

// Clock renders the timestamp the way the log shows it.
//
// A render may not read a clock — it must be a pure function of state, or two
// renders of the same state produce different bytes and the patch suppression
// that compares them breaks — so this formats a stamp the room took, and takes
// no reading of its own.
func (m Message) Clock() string {
	if m.AtUnixMilli == 0 {
		return "--:--"
	}
	return time.UnixMilli(m.AtUnixMilli).UTC().Format("15:04")
}

// Log is the room as one session currently sees it: a revision, the messages,
// and who is here.
//
// It is IMMUTABLE. Nothing appends to Messages in place and nothing sorts
// Members in place; a transition builds a new Log and points at that. Two
// things follow, and both are why it is written this way.
//
// A reducer must not mutate the state it was given — that is what makes panic
// recovery free, because the pre-transition state is still intact and correct.
// A slice in a state struct makes that rule easy to break by accident, and an
// immutable value replaced wholesale makes it impossible.
//
// And a *Log is comparable where a Log with a slice in it is not. The library
// compares consecutive states to decide whether the state version moved
// (internal/session/actor.go's sameState); a state type that is not comparable
// is reported as changed on every transition, so a no-op event bumps the
// version and every fragment's Dirty is asked about a change that did not
// happen. One pointer field keeps that machinery working.
type Log struct {
	Version  uint64
	Messages []Message
	Members  []string
}

// with returns the log that results from one new message, trimmed to
// MaxHistory. The receiver is not touched.
func (l *Log) with(msg Message, version uint64, members []string) *Log {
	base := l.messages()
	keep := len(base)
	if keep > MaxHistory-1 {
		keep = MaxHistory - 1
	}

	next := make([]Message, 0, keep+1)
	next = append(next, base[len(base)-keep:]...)
	next = append(next, msg)
	return &Log{Version: version, Messages: next, Members: members}
}

func (l *Log) messages() []Message {
	if l == nil {
		return nil
	}
	return l.Messages
}

// State is one browser tab's view of the room.
//
// The split between what is shared and what is this session's alone is the
// point of the whole example. Room came from the server and every session in
// the room holds the same one. Draft, DraftError and Notice are this tab's and
// nobody else's — they are what the person at this keyboard is doing, and no
// other member's message may disturb them.
type State struct {
	// Self identifies this session, so a render can tell your own message from
	// somebody else's.
	Self live.ID

	// Me and Role are the identity bound at the handshake, copied in at mount
	// so a render can reach them. They never change for the life of a session.
	Me   string
	Role Role

	// Room is the shared log, as of the last update this session folded in.
	Room *Log

	// Draft is what this session has typed and not yet sent.
	//
	// It lives on the server because that is what makes it survive a re-render.
	// The browser's input keeps its own value across a morph (FR-25), but the
	// value the server renders has to agree with it, or the first patch that
	// does touch this region — a validation message, a resync, a reconnect —
	// hands the browser an empty box.
	Draft string

	// DraftError is the server's verdict on Draft: FR-55's server-driven
	// validation feedback, computed in the reducer and rendered as data.
	DraftError string

	// Notice is the last thing that went wrong for this session — a denied
	// event, a failed effect, a room somebody cleared.
	Notice string

	// RenderPanic is the injected render panic, armed by CmdPanicRender and
	// scoped to this session. It is per-session and not per-room on purpose:
	// a poisoned message in the shared log would break every session's render
	// at once, which would demonstrate the opposite of containment.
	RenderPanic bool
}

// Messages, Members and Version read the shared log without assuming there is
// one. The zero State renders — the login page and every spec that builds a
// State by hand depends on that — and a nil *Log is what the zero State has.
func (s State) Messages() []Message { return s.Room.messages() }

func (s State) Members() []string {
	if s.Room == nil {
		return nil
	}
	return s.Room.Members
}

func (s State) Version() uint64 {
	if s.Room == nil {
		return 0
	}
	return s.Room.Version
}

// MemberLine is the roster as one string. The roster fragment's Dirty function
// compares exactly this, so the declaration and the markup cannot disagree
// about what "the roster changed" means.
func (s State) MemberLine() string { return strings.Join(s.Members(), ", ") }

// MessageCount is the number in the log's heading — and the injected render
// panic (FR-23's third site).
//
// The panic is here, in a method the template calls, rather than in the
// fragment's Render function, and the difference is not cosmetic: Render
// returns a templ.Component and the markup is produced later, when that
// component is rendered. Panicking while building the value and panicking
// while writing the bytes are two different sites, and this is the one the
// requirement is about.
func (s State) MessageCount() string {
	if s.RenderPanic {
		panic("chat: the injected render panic (" + CmdPanicRender + ")")
	}
	return strconv.Itoa(len(s.Messages()))
}

// Remaining is the character budget left, for the composer's hint.
func (s State) Remaining() int {
	return MaxBodyRunes - utf8.RuneCountInString(strings.TrimSpace(s.Draft))
}

// CanPost says whether this session's identity may put words in the room. An
// observer's composer is disabled in the markup and refused by Authorize, and
// both are needed: the first is courtesy and the second is the check.
func (s State) CanPost() bool { return s.Role != RoleObserver }

// CanPurge says whether this session's identity may clear the room. Same rule:
// the button is rendered for a moderator and the event is authorized for one,
// and a browser that fabricates the event without the button still meets
// Authorize.
func (s State) CanPurge() bool { return s.Role == RoleModerator }

// Mine reports whether a message is this session's own, for the "you" styling.
func (s State) Mine(m Message) bool { return m.Author == s.Me }

// Validate is the server's opinion of a message body. It returns the empty
// string when the body is acceptable, and the sentence to show the user
// otherwise.
//
// It is an ordinary pure function and it is called from two places: the draft
// event, so feedback arrives as you type, and the send event, so a client that
// never sent a draft event is still checked. That second call is the one that
// matters — validation the browser can skip is decoration.
//
// FR-55 calls server-driven validation "first-class" and the library ships no
// form or validation vocabulary at all, so this — a pure function, a field on
// the state, and hand-written aria-invalid in the template — is what
// first-class looks like today. It reads well enough that a typed helper would
// probably be a framework growing inside a library, which is why FRICTION.md
// item F-6 asks for a ruling rather than for code.
func Validate(body string) string {
	switch n := utf8.RuneCountInString(body); {
	case body == "":
		return "say something before you send it"
	case n > MaxBodyRunes:
		return fmt.Sprintf("keep it under %d characters — that one is %d", MaxBodyRunes, n)
	}
	return ""
}

// Reducer returns the pure state transition, bound to the room its effects act
// on.
//
// It is a constructor because a live.Effect[Member] carries its own behaviour since the
// 2026-09-03 ruling, so a reducer that schedules one has to hold what that
// effect closes over. It still reads no clock, performs no I/O, and reaches the
// room not at all: sending a message does not append one, it returns the room's
// post effect, the library performs it at the actor boundary, the room stamps
// and numbers it, and this session learns the result the same way every other
// session does — through an event the room pushed. That is what makes two tabs
// unable to disagree, and it is why the author on a message is not something
// this function gets to choose.
func Reducer(room *Room) live.Reducer[State, Member] {
	return func(state State, ev live.Event) (State, []live.Effect[Member]) {
		switch ev.Name {
		case EventDraft:
			return applyDraft(state, ev.Fields.Get(fieldBody)), nil
		case EventClear:
			return applyClear(state), nil
		case EventSend:
			return applySend(room, state, ev)
		case EventPurge:
			state.Notice = ""
			return state, []live.Effect[Member]{room.PurgeEffect(Purge{Cause: ev.ID})}
		case EventPosted:
			return applyPosted(state, ev), nil
		case EventPresence:
			return applyPresence(state, ev), nil
		case EventPurged:
			return applyPurged(state, ev), nil
		case live.EffectFailedEvent:
			return applyFailure(room, state, ev)
		}

		// An unregistered name cannot reach here from a browser — the library
		// refuses one before the reducer runs — so anything arriving here is
		// something the library synthesised that this application has no
		// answer for. Ignoring it is correct.
		return state, nil
	}
}

// applyDraft folds a keystroke into this session's own state.
//
// This is FR-55's per-field change event, and the reason the server wants it
// at all is the line below it: the draft is what a re-render puts back into
// the box. Without it the server would render an empty input on any transition
// that touched this fragment, and the user would watch their sentence vanish
// because somebody else said hello.
func applyDraft(state State, body string) State {
	state.Draft = body
	if strings.TrimSpace(body) == "" {
		// Nothing typed yet is not yet a mistake. Reporting "say something
		// first" while the box is empty would put an error on the screen
		// before the user has done anything at all.
		state.DraftError = ""
		return state
	}
	state.DraftError = Validate(strings.TrimSpace(body))
	return state
}

// applyClear empties this session's draft, and is Escape-to-clear's whole
// server half.
//
// It clears the validation verdict with it, because a message about a sentence
// that no longer exists is a message about nothing. It does not touch Notice:
// that is the room's last word to this member — a purge, an effect failure —
// and the member deciding to abandon what they were typing is not a reason to
// take it off their screen.
//
// The reason the browser's own value is not enough, and this is FR-55's point
// from the other end: State.Draft is what a re-render puts back into the box.
// Emptying the input in the browser without telling the server means the very
// next patch that touches this fragment — a validation message, a reconnect, a
// resync — restores the sentence the member just discarded.
func applyClear(state State) State {
	state.Draft, state.DraftError = "", ""
	return state
}

// applySend handles the form submission.
func applySend(room *Room, state State, ev live.Event) (State, []live.Effect[Member]) {
	raw := ev.Fields.Get(fieldBody)
	body := strings.TrimSpace(raw)

	switch body {
	case CmdPanicReducer:
		// FR-23's first site. The pre-transition state is intact and correct
		// because a reducer may not mutate what it was given, so the library
		// simply keeps it: no state change, no patch, one Error frame carrying
		// the causal ID of this event, and every other session unaffected.
		panic("chat: the injected reducer panic (" + CmdPanicReducer + ")")

	case CmdPanicEffect:
		// FR-23's second site. The reducer is fine and the state is consistent
		// — this transition applied — and the failure happens later, at the
		// actor boundary. It comes back as an ordinary event, which is why the
		// notice below is rendered by applyFailure and not from here.
		state.Draft, state.DraftError = "", ""
		return state, []live.Effect[Member]{room.PanicEffect()}

	case CmdPanicRender:
		// FR-23's third site, armed for this session only.
		state.Draft, state.DraftError = "", ""
		state.RenderPanic = true
		return state, nil
	}

	if problem := Validate(body); problem != "" {
		// The rejected text stays in the state, verbatim and untrimmed. Losing
		// what somebody typed because the server disliked it is the rudest
		// thing a form can do, and it is one assignment away either direction.
		state.Draft = raw
		state.DraftError = problem
		return state, nil
	}

	state.Draft = ""
	state.DraftError = ""
	state.Notice = ""
	return state, []live.Effect[Member]{room.PostEffect(Post{Body: body, Cause: ev.ID})}
}

// applyPosted folds a message the room pushed.
//
// A revision this session has already passed is dropped. Emitted events are
// best-effort — the library tells an effect when a mailbox is full rather than
// letting the event vanish — and this is the line that keeps a duplicate or a
// late delivery from showing the same sentence twice.
func applyPosted(state State, ev live.Event) State {
	version, ok := newerVersion(state, ev)
	if !ok {
		return state
	}
	seq, err := strconv.ParseUint(ev.Fields.Get(fieldSeq), 10, 64)
	if err != nil {
		return state
	}
	at, err := strconv.ParseInt(ev.Fields.Get(fieldAtMilli), 10, 64)
	if err != nil {
		return state
	}

	state.Room = state.Room.with(Message{
		Seq:         seq,
		Author:      ev.Fields.Get(fieldAuthor),
		Body:        ev.Fields.Get(fieldBody),
		AtUnixMilli: at,
	}, version, splitMembers(ev.Fields.Get(fieldMembers)))
	return state
}

// applyPresence folds a roster change. The message slice is carried over by
// reference and that is safe precisely because a Log is never mutated.
func applyPresence(state State, ev live.Event) State {
	version, ok := newerVersion(state, ev)
	if !ok {
		return state
	}
	state.Room = &Log{
		Version:  version,
		Messages: state.Messages(),
		Members:  splitMembers(ev.Fields.Get(fieldMembers)),
	}
	return state
}

// applyPurged folds a moderator's clearing of the room.
func applyPurged(state State, ev live.Event) State {
	version, ok := newerVersion(state, ev)
	if !ok {
		return state
	}
	state.Room = &Log{Version: version, Members: splitMembers(ev.Fields.Get(fieldMembers))}
	state.Notice = "the room was cleared by " + ev.Fields.Get(fieldBy)
	return state
}

// applyFailure decides what to do about an effect that failed or panicked.
//
// Note what reaches the browser and what does not. EffectFailedSourceField is
// a name this application chose — "chat.subscribe", "chat.post" — so it is
// safe to render. EffectFailedErrorField is NOT: it carries the error's own
// message, or the raw panic value, unredacted and in production, ungated by
// Config.Dev (live/doc.go, and the constant's own doc comment). Rendering it
// into a fragment would publish internal error text to every browser in the
// room. It is read here only for the classification beside it.
//
// The retry is the library's claim rather than this reducer's guess: an
// unreadable or absent classification parses as false and nothing is retried.
// The one failure worth retrying is a dead subscription, because a session
// without one keeps rendering the last log it saw and stops learning about
// anybody else — it looks right while being wrong.
func applyFailure(room *Room, state State, ev live.Event) (State, []live.Effect[Member]) {
	source := ev.Fields.Get(live.EffectFailedSourceField)
	state.Notice = "something went wrong on the server: " + source

	retryable, _ := strconv.ParseBool(ev.Fields.Get(live.EffectFailedRetryableField))
	if retryable && source == SourceSubscribe {
		return state, []live.Effect[Member]{room.SubscribeEffect()}
	}
	return state, nil
}

// newerVersion reads the room revision off an event and reports whether it is
// newer than the one this session holds.
func newerVersion(state State, ev live.Event) (uint64, bool) {
	version, err := strconv.ParseUint(ev.Fields.Get(fieldVersion), 10, 64)
	if err != nil || version <= state.Version() {
		return 0, false
	}
	return version, true
}

// splitMembers reads the roster back off an event. The room joins it sorted,
// so the result is deterministic and a render over it is too.
func splitMembers(joined string) []string {
	if joined == "" {
		return nil
	}
	return strings.Split(joined, ",")
}

// Directory is the member list this example authenticates against. Production
// replaces it with whatever already knows who is signed in.
type Directory map[string]Member

// IdentityCookie is the cookie /login sets and Authenticate reads.
//
// A cookie rather than a query parameter, because the client runtime opens the
// WebSocket at the mount path with nothing appended: whatever identifies the
// session has to be something the browser sends on its own. That is also the
// constraint behind FRICTION.md item F-2.
const IdentityCookie = "chat_user"

// DemoDirectory is the cast. Five identities and four roles, because per-event
// authorization is not demonstrated by a rule that says yes to everybody.
func DemoDirectory() Directory {
	return Directory{
		"alice":   {Name: "alice", Role: RoleMember},
		"bob":     {Name: "bob", Role: RoleMember},
		"mallory": {Name: "mallory", Role: RoleModerator},
		"olive":   {Name: "olive", Role: RoleObserver},
		"trudy":   {Name: "trudy", Role: RoleBanned},
	}
}

// Names returns the directory's members in a stable order, for the login page.
func (d Directory) Names() []string {
	out := make([]string, 0, len(d))
	for name := range d {
		out = append(out, name)
	}
	// A map has no iteration order and this renders into HTML, so it is sorted
	// for the same reason a fragment's render is: the same input must produce
	// the same bytes.
	slices.Sort(out)
	return out
}

// Authenticate derives the session identity from the upgrade request. It is
// Config.Authenticate, and it is a real one — live.Anonymous would make every
// tab the same subject and there would be nothing for Authorize to decide.
//
// A failure here is a refused handshake, before any per-session memory is
// allocated. The message names no untrusted input: it is an error an operator
// reads, and echoing back the cookie value would put whatever a client chose
// to send into a server log.
func (d Directory) Authenticate(r *http.Request) (Member, error) {
	cookie, err := r.Cookie(IdentityCookie)
	if err != nil {
		return Member{}, fmt.Errorf("chat: no %s cookie on the upgrade request: sign in at /login?user=alice first", IdentityCookie)
	}
	member, ok := d[cookie.Value]
	if !ok {
		return Member{}, fmt.Errorf("chat: the %s cookie does not name a member of this room", IdentityCookie)
	}
	return member, nil
}

// Authorize runs before the reducer for every event, at the single mailbox
// ingress, so a new event name cannot skip it. It is Config.Authorize.
//
// Both denial shapes are here because they mean different things. A DenyError
// refuses one event and leaves the session running: an observer trying to post
// has done nothing wrong, they simply may not. A FatalDenyError refuses the
// event and closes the connection: a banned member sending anything at all is
// not a permission question, it is a session that should not be open.
//
// The reason strings are operator-facing. The client is told the event was not
// permitted and nothing more, because an authorization reason is an
// authorization input.
func Authorize(_ context.Context, s live.Session[Member], ev live.Event) error {
	// No assertion, and none possible: the session is typed by the identity
	// Authenticate produced, so a shape this hook did not anticipate is a
	// compile error rather than a runtime branch. The "unreachable" deny that
	// used to stand here is what the type parameter replaced.
	member := s.Identity()

	if member.Role == RoleBanned {
		return &live.FatalDenyError{Reason: member.Name + " is banned from this room"}
	}

	switch ev.Name {
	case EventSend, EventDraft:
		// EventClear is deliberately NOT in this list, and the omission is a
		// decision rather than an oversight. This branch is about posting: an
		// observer may not put a sentence in the room and may not put one in
		// the server's copy of their draft. Discarding a draft is neither —
		// chat.clear sets this session's own Draft to the empty string, cannot
		// reach the room, and for an observer whose draft was never accepted
		// it sets "" to "". Denying it would be a permission with no referent,
		// and the deny message would have to say "may not post" about an act
		// that posts nothing.
		if member.Role == RoleObserver {
			return &live.DenyError{Reason: member.Name + " is an observer and may not post"}
		}
	case EventPurge:
		if member.Role != RoleModerator {
			return &live.DenyError{Reason: member.Name + " is not a moderator and may not clear the room"}
		}
	}
	return nil
}

// Config builds the live application over a room and a directory.
//
// Everything security-relevant is set here and nothing is left to a default,
// because live.New refuses a Config with a hole in it rather than starting
// with one.
func Config(room *Room, dir Directory, origins []string) live.Config[State, Member] {
	return live.Config[State, Member]{
		// Init is the mount hook, and it is where FR-56's subscribe-on-mount
		// happens. Join registers this session for pushes and reads the room
		// under one lock — split in two, a message landing between them is
		// either shown twice or missed entirely, and the window is exactly as
		// wide as a page load.
		Init: func(ctx context.Context, s live.Session[Member]) (State, []live.Effect[Member], error) {
			member := s.Identity()
			return State{
				Self: s.ID(),
				Me:   member.Name,
				Role: member.Role,
				Room: room.Join(s.ID(), member.Name),
			}, []live.Effect[Member]{room.SubscribeEffect()}, nil
		},

		Reduce: Reducer(room),

		Fragments: []live.Fragment[State]{
			{
				ID:     FragmentLog,
				Render: func(s State) templ.Component { return LogRegion(s) },
				Dirty: func(prev, next State) bool {
					return prev.Room != next.Room || prev.RenderPanic != next.RenderPanic
				},
			},
			{
				ID:     FragmentComposer,
				Render: func(s State) templ.Component { return ComposerRegion(s) },
				// This declaration is the whole of FR-55's hard case, and it is
				// three comparisons long. It names only what belongs to this
				// session — the draft, the verdict on it, the last notice — and
				// says nothing about the room. So a message from another member
				// changes State.Room, this returns false, and the composer is not
				// in the patch at all: the browser is never handed markup for the
				// box the user is typing into.
				//
				// Widening it to include the room would be legal, would pass
				// livetest.AssertDirtyComplete, and would break the feature.
				Dirty: func(prev, next State) bool {
					return prev.Draft != next.Draft ||
						prev.DraftError != next.DraftError ||
						prev.Notice != next.Notice
				},
			},
			{
				ID:     FragmentRoster,
				Render: func(s State) templ.Component { return RosterRegion(s) },
				Dirty: func(prev, next State) bool {
					return prev.MemberLine() != next.MemberLine()
				},
			},
		},

		// The allowlist. The three names the room emits are absent on purpose;
		// see their doc comment.
		Events: []string{EventSend, EventDraft, EventClear, EventPurge},

		// FR-56's other half. Teardown runs after the session's goroutine has
		// exited, with the final state, so a session that dropped its
		// connection does not leave a subscription behind. The leak test in
		// chat_test.go is what holds it.
		Teardown: func(_ context.Context, s live.Session[Member], _ State) { room.Leave(s.ID()) },

		// A real allowlist, not live.AnyOrigin. main.go derives it from the
		// listen address; production lists the scheme and host the app is
		// actually served from, and nothing else.
		Origins: origins,

		Authenticate: dir.Authenticate,
		Authorize:    Authorize,

		// The one escape hatch this example takes, named so that
		// `grep -rn 'live\.NoCSRFCheck'` finds it.
		//
		// It is only safe because Origins above is a real allowlist: the origin
		// check is then the whole of the CSRF posture, which is exactly the
		// condition the library's own doc comment states. A double-submit token
		// would be better and is not expressible today — the client runtime
		// sends nothing of its own on the upgrade, so Config.CSRF can only read
		// what a browser attaches automatically, which is the cookie it is
		// meant to be defending. FRICTION.md item F-2.
		CSRF: live.NoCSRFCheck,

		Limits: live.Limits{
			// One member, several tabs. The default is 20 and this is lower
			// only to make the bound visible in an example: a chat is the
			// first application where one subject legitimately holds many
			// concurrent sessions, which is what this limit is about. Every
			// other field stays at its documented default.
			MaxSessionsPerIdentity: 8,
		},
	}
}
