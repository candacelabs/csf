package session

import (
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// FR-58, on the two errors an effect is handed when its emission is refused.
//
// # Why this is a table-driven standard-library test and not a Ginkgo spec
//
// The house rule is Ginkgo v2 with Gomega for behaviour, and every other suite
// in this package follows it. This one does not, deliberately, and the reason
// is the package clause on line 1: `package session`, not `package
// session_test`. emissionRefused and causalClause are unexported and there is
// no behaviour to drive to reach them — a spec that saturated a real mailbox
// would be asserting on scheduling, which is backpressure_test.go's subject and
// already covered there. What is left is a pure function from (session,
// scheduling event, sentinel) to a string, and four rows of table over it says
// everything there is to say.
//
// The package already holds one file of this shape, union_internal_test.go, for
// the same reason: the internal seam is the thing under test. Ginkgo's bootstrap
// lives in the external test package, so an in-package spec here would be a
// second suite in one directory for two assertions.
//
// # What it holds
//
// That an effect handed one of these two refusals can answer all three of
// FR-58's questions from the string alone. It matters more here than anywhere
// else in the library, because this error crosses into application code and
// then stops being the library's: an effect wraps it, logs it through its own
// logger, or returns it — and by the time a person reads it, the only context
// it has is the context it was built with.
// ---------------------------------------------------------------------------

// testSubject is the identity these internal specs instantiate the Actor on.
//
// The Actor became generic in the identity type on 2026-09-03, so an internal
// spec has to name one. Nothing below reads it: what these tests exercise is
// the emission-refusal message and the contributing union, neither of which
// touches an identity.
type testSubject string

// Subject satisfies IIdentity for testSubject.
func (s testSubject) Subject() string { return string(s) }

func TestEmissionRefusedNamesSessionCauseAndNextStep(t *testing.T) {
	const id = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	a := &Actor[testSubject]{idStr: id}

	cases := []struct {
		name        string
		source      string
		scheduledBy uint64
		sentinel    error
		// wantCause is the causal clause the message must carry. FR-58 says
		// "the causal ID where one exists", and for a server-initiated effect
		// none does — so the message says so in words rather than printing the
		// zero, which would send a reader looking for an event 0 there is no
		// such thing as.
		wantCause string
		wantNext  string
	}{
		{
			name:        "a full mailbox, on an effect a client event scheduled",
			source:      "counter.watch",
			scheduledBy: 41,
			sentinel:    ErrSessionSaturated,
			wantCause:   "scheduled by event 41",
			wantNext:    "back off and emit again, or raise Config.Limits.MailboxDepth",
		},
		{
			name:        "a full mailbox, on an effect the server scheduled itself",
			source:      "feed.subscribe",
			scheduledBy: 0,
			sentinel:    ErrSessionSaturated,
			wantCause:   "scheduled by the server itself",
			wantNext:    "raise Config.Limits.MailboxDepth",
		},
		{
			name:        "a closing session, on an effect a client event scheduled",
			source:      "chat.publish",
			scheduledBy: 7,
			sentinel:    ErrSessionClosing,
			wantCause:   "scheduled by event 7",
			wantNext:    "return from the effect rather than retrying",
		},
		{
			name:        "a closing session, on an effect the server scheduled itself",
			source:      "feed.tick",
			scheduledBy: 0,
			sentinel:    ErrSessionClosing,
			wantCause:   "scheduled by the server itself",
			wantNext:    "return from the effect rather than retrying",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := a.emissionRefused(c.source, c.scheduledBy, c.sentinel)
			msg := err.Error()

			// FR-58 clause 1. The identifier is the whole point: an effect that
			// fans out across sessions holds this string and no other handle on
			// which session it belongs to.
			if !strings.Contains(msg, "session "+id) {
				t.Errorf("FR-58 clause 1: the message does not name the session %s\n  got: %s", id, msg)
			}
			// FR-58 clause 2.
			if !strings.Contains(msg, c.wantCause) {
				t.Errorf("FR-58 clause 2: the message does not carry the causal clause %q\n  got: %s",
					c.wantCause, msg)
			}
			// FR-58 clause 3.
			if !strings.Contains(msg, c.wantNext) {
				t.Errorf("FR-58 clause 3: the actionable next step %q is gone\n  got: %s", c.wantNext, msg)
			}
			// The effect that failed. Not one of FR-58's three, and asserted
			// anyway: an application whose reducer schedules four kinds of
			// effect needs to know which one was refused before any of the
			// three clauses is useful to it.
			if !strings.Contains(msg, c.source) {
				t.Errorf("the message does not name the effect %q\n  got: %s", c.source, msg)
			}
			// And the wrapping is real wrapping. The sentinel is how the
			// library itself tells the two refusals apart, and a message
			// carrying the words without the value would leave errors.Is
			// answering false for a failure it names.
			if !errors.Is(err, c.sentinel) {
				t.Errorf("errors.Is does not reach the sentinel through the wrapper\n  got: %s", msg)
			}
		})
	}
}
