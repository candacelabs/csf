// Package security is the compiled source for docs/guide/security.md.
//
// It is one small chat room, chosen because a room is the smallest application
// with a real authorization question in it: some identities may post, some may
// only watch, and one of those refusals has to be visible to the person it
// refuses while the other must not be bypassable by anyone.
package security

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

const (
	// EventPost is a message the composer sends.
	EventPost = "room.post"
	// EventPurge clears the room.
	EventPurge = "room.purge"

	// FieldBody is the composer's form field.
	FieldBody = "body"

	// FragmentLog is the message list.
	FragmentLog = "room.log"
	// FragmentNotice is the one-line region a refusal is rendered into. It
	// exists because a denial has to have somewhere to appear.
	FragmentNotice = "room.notice"
)

// ObserverRefusal is what a read-only participant is told, in the words they
// see. It is a constant because the reducer sets it and a spec asserts on it,
// and a message asserted against a copy of itself asserts nothing.
const ObserverRefusal = "you are an observer in this room and may not post"

// Origins is the whole of the allowlist for an application served from one
// public origin, and a literal list is the recommendation.
//
// An entry is compared against the browser's Origin header with
// strings.EqualFold and nothing else: no prefix match, no wildcard, no
// normalisation. So an entry is exactly scheme + "://" + host, with the port
// only when the URL has one, and never a trailing slash and never a path.
// "https://app.example.com/" is a different string from what any browser sends
// and matches nothing.
//
// Deny by default is the library's rule: a request whose Origin is absent is
// refused too, because an absent Origin is not an allowed one.
func Origins() []string {
	return []string{
		"https://app.example.com",
		// A second entry, not a wildcard, is how a second hostname is allowed.
		"https://www.app.example.com",
	}
}

// CSP is the Content-Security-Policy the client runtime is known to work
// under, byte for byte: it is the policy test/internal/conformance's CP1-13
// spec serves in front of a real application and drives with a real browser,
// asserting zero securitypolicyviolation events while the runtime boots, opens
// its WebSocket and patches the DOM.
//
// Two clauses are worth reading twice:
//
//   - connect-src 'self' is what the live WebSocket needs, and 'self' means
//     the PAGE's origin. If the live handler is served from a different origin
//     than the page, this clause has to name that origin explicitly — the
//     library cannot help, because the browser is enforcing a policy about a
//     URL your own page constructed.
//   - script-src has no 'unsafe-eval' and needs none. The runtime contains no
//     eval and no new Function, which is PRD NFR-4 and is scanned in CI over
//     the shipped, minified artifact rather than over the sources.
const CSP = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"connect-src 'self'; " +
	"img-src 'self' data:; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'none'"

// WithCSP sends the policy on every response, including the upgrade.
//
// The library sends no security header of its own and will not start: which
// headers an application sends is the application's decision, and a library
// that set them would be overriding a policy it cannot see the rest of.
func WithCSP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", CSP)
		next.ServeHTTP(w, r)
	})
}

// Role is what an identity may do in the room.
type Role int

const (
	// RoleObserver may read and may not post. Its refusal has to be SEEN.
	RoleObserver Role = iota
	// RoleMember may post.
	RoleMember
	// RoleModerator may post and may purge.
	RoleModerator
)

// Member is the application's identity. It satisfies live.IIdentity, is bound at
// the handshake by Authenticate, and is immutable for the connection's life:
// a session cannot outlive its connection, so there is no re-authentication and
// no privilege change mid-session. A role change takes effect on the next
// connection.
type Member struct {
	Name string
	Role Role
}

// Subject is the stable, non-secret identifier the library logs and counts
// sessions against. It must not be a token: it reaches the provenance log, the
// per-identity session limit, and every span attribute for the session.
func (m Member) Subject() string { return m.Name }

// State is one session's view of the room.
type State struct {
	Me       string
	Role     Role
	Messages []string
	Notice   string
}

// CanPost is the same rule the markup uses to decide whether to render an
// enabled composer. Rendering it is courtesy; the two checks below are the
// enforcement, and both are needed.
func (s State) CanPost() bool { return s.Role != RoleObserver }

// Authorize is the enforcement half of the rule, and it is deliberately not
// where the observer's refusal lives.
//
// It runs before the reducer, for every event, at the single mailbox ingress,
// so a new event name cannot skip it. That is also why a denial here cannot be
// rendered: a *live.DenyError rejects the event before the reducer runs, so
// there is no transition, so there is no render, so there is nothing for the
// user to see. The library has no application hook that can render a denial —
// there is no patch hook, by design.
//
// So the rules divide by whether the person refused has to be told:
//
//   - An identity that is not a Member of this application is a session that
//     should not be open: *live.FatalDenyError, and the connection closes with
//     4006 UNAUTHORIZED. Nobody needs to read an explanation of that.
//   - Purging is a moderator's button and is not rendered for anyone else, so
//     an event asking to purge did not come from a rendered control:
//     *live.DenyError, the event is dropped, the session continues.
//   - Posting as an observer is refused in the reducer instead, where it can
//     be rendered — see Reduce. This hook lets it through on purpose.
//
// Any error that is neither type is treated as a DenyError. An authorization
// hook that failed open on a shape it did not anticipate would have the one
// failure mode an authorization hook must not have.
func Authorize(_ context.Context, sess live.Session[Member], ev live.Event) error {
	// No assertion. The session is typed by the identity Authenticate produced,
	// so "the identity is not what I expected" is a compile error rather than a
	// deny this hook has to remember to write.
	member := sess.Identity()

	if ev.Name == EventPurge && member.Role != RoleModerator {
		return &live.DenyError{Reason: member.Name + " is not a moderator and may not purge the room"}
	}
	return nil
}

// Reducer is the visible half of the rule.
//
// The observer's refusal is here, not in Authorize, because this is the only
// place in the library where a refusal can become markup: the reducer returns a
// state whose Notice field is rendered by FragmentNotice, and the browser sees
// a sentence rather than an event that vanished.
//
// The function it returns is pure: it performs no I/O, reads no clock, and
// returns the write it wants as a value for the library to perform.
func Reducer(room *Room) live.Reducer[State, Member] {
	return func(s State, ev live.Event) (State, []live.Effect[Member]) {
		switch ev.Name {
		case EventPost:
			if !s.CanPost() {
				s.Notice = ObserverRefusal
				return s, nil
			}
			body := strings.TrimSpace(ev.Fields.Get(FieldBody))
			if body == "" {
				s.Notice = "a message needs some words in it"
				return s, nil
			}
			s.Notice = ""
			return s, []live.Effect[Member]{room.PostEffect(s.Me, body)}

		case EventPurge:
			s.Notice = ""
			s.Messages = nil
		}
		return s, nil
	}
}

// Room is the application-owned store the effect writes to.
type Room struct{ Posted []string }

// PostEffect is the write that leaves the process, and it enforces the same
// rule a third time.
//
// This is not redundant with the reducer. The reducer's refusal is what a
// reader SEES; this one is what a reader cannot get past, and it is here
// because an effect is reachable from anywhere a reducer can be wrong — a new
// event name, a refactor, a branch nobody replayed. The identity is a parameter
// of Run rather than something to fish out of a context, which is what makes an
// effect that forgot to ask impossible to write.
func (r *Room) PostEffect(author, body string) live.Effect[Member] {
	return live.Effect[Member]{
		Source: "room.post",
		Run: func(ctx context.Context, sess live.Session[Member], emit live.Emitter) error {
			member := sess.Identity()
			if member.Role == RoleObserver {
				return fmt.Errorf("room: %s is an observer and may not post", member.Name)
			}
			r.Posted = append(r.Posted, author+": "+body)
			return nil
		},
	}
}

// Config is the application. All four security fields are required — there is
// no nil that means "off" — so turning a check off is something written down
// and greppable.
func Config(room *Room, origins []string) live.Config[State, Member] {
	return live.Config[State, Member]{
		Init: func(ctx context.Context, sess live.Session[Member]) (State, []live.Effect[Member], error) {
			member := sess.Identity()
			return State{Me: member.Name, Role: member.Role}, nil, nil
		},
		Reduce: Reducer(room),
		Fragments: []live.Fragment[State]{
			{
				ID:     FragmentLog,
				Render: func(s State) templ.Component { return text(strings.Join(s.Messages, "\n")) },
				Dirty:  func(prev, next State) bool { return len(prev.Messages) != len(next.Messages) },
			},
			{
				ID:     FragmentNotice,
				Render: func(s State) templ.Component { return text(s.Notice) },
				Dirty:  func(prev, next State) bool { return prev.Notice != next.Notice },
			},
		},
		Events:  []string{EventPost, EventPurge},
		Origins: origins,

		// A real hook, not live.Anonymous: this application has an identity to
		// refuse, and live.Anonymous would make every tab the same subject.
		Authenticate: Authenticate,
		Authorize:    Authorize,

		// live.NoCSRFCheck is safe here ONLY because Origins above is a real
		// allowlist: the origin check is then the whole of the CSRF posture,
		// which is the condition the library's own doc comment states. An
		// application that authenticates with a cookie adds a token bound to
		// the authenticated application session instead.
		CSRF: live.NoCSRFCheck,

		// Off, and it is not an escape hatch. It gates the session inspector's
		// route, dev reload's routes, and the panic value and stack in an Error
		// frame.
		Dev: false,
	}
}

// Authenticate derives the identity from the upgrade request. It runs on the
// HTTP request, before any per-session memory is allocated, and a failure is a
// 401 rather than a close code.
//
// A real application reads whatever it already trusts here — a session cookie,
// a bearer token — and turns it into a live.IIdentity. This one reads a header
// so the sample has no session store in it.
func Authenticate(r *http.Request) (Member, error) {
	name := r.Header.Get("X-Room-Member")
	if name == "" {
		return Member{}, fmt.Errorf("room: no member on the request")
	}
	role := RoleMember
	switch r.Header.Get("X-Room-Role") {
	case "observer":
		role = RoleObserver
	case "moderator":
		role = RoleModerator
	}
	return Member{Name: name, Role: role}, nil
}

func text(s string) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, templ.EscapeString(s))
		return err
	})
}
