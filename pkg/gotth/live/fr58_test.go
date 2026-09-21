package live_test

import (
	"context"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/session"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// FR-58, on the errors an application can hold.
//
// FR-58: "Every library-produced error MUST name the session, the causal ID
// where one exists, and the actionable next step. 'invalid frame' without
// context is a defect."
//
// docs/error-audit.md enumerates and grades all 117 error-authoring sites in
// the published module. This file is the half of that document a machine can
// keep true, and it is deliberately narrower than the document in one way and
// stronger in another.
//
// NARROWER: it covers the errors that reach APPLICATION CODE as values — the
// Emitter's refusals, ConfigError, the denial types, Script's mount refusals.
// Those are the ones whose whole diagnostic is the string, because the library
// does not control where the value goes next: an application can wrap it, log
// it through its own logger, or return it from an effect. Errors the library
// constructs and consumes itself reach a reader as a structured log record, and
// the session and causal identifier are asserted there, on the record's fields,
// by internal/session's own suite.
//
// STRONGER: where a session is named, this asserts it is the RIGHT session —
// the identifier the client on the other end of the connection is holding — and
// not merely that a 32-character hex run appears. An error that names some
// session is not what FR-58 asks for.
//
// The next-step clause is asserted per row against the specific instruction the
// message gives, rather than against a heuristic. A rule like "the message
// contains two colons" is passable by a bad message and failable by a good one,
// and a gate that is green because it is weak is worse than no gate. The
// judgement of whether each instruction is actionable is docs/error-audit.md's,
// made by a person; what this file holds is that the instruction is still there.
// ---------------------------------------------------------------------------

// scheduledBy matches the causal clause the Emitter's refusals carry. The
// identifier must be non-zero: these four are raised inside an effect a client
// event scheduled, so an event genuinely exists and "event 0" would mean the
// library had lost it.
var scheduledBy = regexp.MustCompile(`scheduled by event [1-9][0-9]*`)

var _ = Describe("FR-58: the errors an application holds", func() {
	Describe("the Emitter's four refusals", func() {
		// One session, four refusals, and each entry names the next step it is
		// supposed to carry. The session identifier is compared against the one
		// the client holds rather than pattern-matched, which is the assertion
		// that catches a plausible-looking identifier from somewhere else.
		DescribeTable("names the session, the scheduling event, and what to do instead",
			func(ev live.Event, nextStep string) {
				app, _, returned := emitting(ev)
				defer app.stop()

				var err error
				Eventually(returned, 5*time.Second).Should(Receive(&err))
				Expect(err).To(HaveOccurred())
				msg := err.Error()

				Expect(msg).To(ContainSubstring("session "+hex.EncodeToString(app.id)),
					"FR-58 clause 1: the error names no session, or names one that is not this "+
						"one. An effect emitting from a fan-out holds this string and nothing "+
						"else, so an operator cannot join it to the session's own records.\n"+
						"  message: %s", msg)

				Expect(msg).To(MatchRegexp(scheduledBy.String()),
					"FR-58 clause 2: the causal identifier is missing. The emitted event has "+
						"none of its own — that is the subject of one of these very refusals — "+
						"so the identifier that exists is the event whose transition returned "+
						"the effect, and it is what reaches the interaction behind the failure.\n"+
						"  message: %s", msg)

				Expect(msg).To(ContainSubstring(nextStep),
					"FR-58 clause 3: the actionable next step is gone. This message told the "+
						"application what to do instead and no longer does.\n"+
						"  message: %s", msg)
			},

			Entry("a server-minted causal identifier",
				live.Event{Name: "counter.relabel", ID: 7},
				"so leave it zero"),
			Entry("a timestamp the boundary stamps",
				live.Event{Name: "counter.relabel", At: time.Unix(1, 0)},
				"the actor boundary stamps it, so leave it zero"),
			Entry("zero among the contributing identifiers",
				relabel([]uint64{12, 0, 14}),
				"list the identifiers of real events, or leave the field nil"),
			Entry("more contributing identifiers than one event may claim",
				relabel(ids(session.MaxEventContributing+1)),
				"name the events whose state changes this event carries"),
		)
	})

	Describe("the construction-time errors", func() {
		// FR-58's session clause is INAPPLICABLE to every row here, and that is
		// a finding rather than an exemption: New runs before any connection
		// exists, so there is no session to name and a message that invented one
		// would be lying. The audit records the same thing per row, with this
		// reason. What is not inapplicable is the next step, and a Config error
		// with no next step is the exact defect FR-58 names — the reader is
		// holding a file they have to change one line of.
		DescribeTable("name the field at fault and what to set it to",
			func(mutate func(cfg *live.Config[counter, user]), field, nextStep string) {
				cfg := validConfig()
				mutate(&cfg)

				_, err := live.New(cfg)
				Expect(err).To(HaveOccurred())

				var cfgErr *live.ConfigError
				Expect(errors.As(err, &cfgErr)).To(BeTrue(), "got %v", err)
				msg := err.Error()

				Expect(msg).To(ContainSubstring("Config."+field),
					"FR-58: the error does not name the field.\n  message: %s", msg)
				Expect(msg).To(ContainSubstring(nextStep),
					"FR-58 clause 3: the actionable next step is gone.\n  message: %s", msg)
				Expect(msg).NotTo(MatchRegexp(`session [0-9a-f]{32}`),
					"a construction-time error named a session. None exists at New: if this "+
						"starts passing, the audit's per-row 'no session exists here' reason has "+
						"stopped being true and the row needs regrading.\n  message: %s", msg)
			},

			// There is no "no mount hook" entry any more. Config.Init became
			// optional — New substitutes the zero value of S rather than
			// refusing — so there is no construction-time error to hold to
			// FR-58's three clauses. The error this row used to check is gone
			// rather than weakened, and live_test.go's "accepts a Config with
			// no mount hook" is what now says so.
			Entry("no reducer", func(c *live.Config[counter, user]) { c.Reduce = nil },
				"Reduce", "set the reducer that advances state"),
			Entry("no live regions", func(c *live.Config[counter, user]) { c.Fragments = nil },
				"Fragments", "declare at least one live region"),
			Entry("no registered events", func(c *live.Config[counter, user]) { c.Events = nil },
				"Events", "unknown names are refused"),
			Entry("no allowed origins", func(c *live.Config[counter, user]) { c.Origins = nil },
				"Origins", "or set live.AnyOrigin for local development"),
			Entry("no authentication hook", func(c *live.Config[counter, user]) { c.Authenticate = nil },
				"Authenticate", "or live.Anonymous to opt out"),
			Entry("no authorization hook", func(c *live.Config[counter, user]) { c.Authorize = nil },
				"Authorize", "or live.AllowAll[YourIdentity] to opt out"),
			Entry("no CSRF hook", func(c *live.Config[counter, user]) { c.CSRF = nil },
				"CSRF", "or live.NoCSRFCheck to opt out"),
			Entry("a negative limit",
				func(c *live.Config[counter, user]) { c.Limits.MailboxDepth = -1 },
				"Limits.MailboxDepth", "leave it zero to take the documented default"),
			Entry("a fragment identity the wire schema cannot carry",
				func(c *live.Config[counter, user]) { c.Fragments[0].ID = strings.Repeat("x", 65) },
				"Fragments", "shorten it"),
			Entry("a build identity that cannot equal itself",
				func(c *live.Config[counter, user]) { c.DevBuildID = " abc " },
				"DevBuildID", "trim it here"),
		)
	})

	Describe("the denial types an authorization hook returns", func() {
		// These two are the one place FR-58 and §6.4's redaction rule pull in
		// opposite directions, and the audit records how it is resolved: the
		// operator-facing string carries the reason, the client is told a fixed
		// generic denial, and the causal identifier travels on the Error frame
		// rather than in this text. So the clause asserted here is the one the
		// type owns — that the reason survives to the reader who is allowed it.
		It("renders the operator-facing reason, and says which one closes the connection", func() {
			survivable := (&live.DenyError{Reason: "not a member of this room"}).Error()
			fatal := (&live.FatalDenyError{Reason: "token replay"}).Error()

			Expect(survivable).To(ContainSubstring("not a member of this room"))
			Expect(fatal).To(ContainSubstring("token replay"))
			Expect(fatal).To(ContainSubstring("closing the connection"),
				"a log line has to distinguish the two without the reader knowing which type "+
					"produced it")
			Expect(survivable).NotTo(ContainSubstring("closing the connection"))
		})
	})

	Describe("Script's mount-path refusals", func() {
		// No session and no causal identifier: Script renders on the page
		// request, before any connection. Every one of these is graded on the
		// next step alone, and each says what a browser will do with the path
		// rather than merely that it was refused.
		DescribeTable("say what the browser would do with the path",
			func(mount, want string) {
				err := live.Script(mount).Render(context.Background(), &strings.Builder{})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(want),
					"FR-58 clause 3: %q was refused without saying what a browser does with it",
					mount)
			},

			Entry("empty", "", `such as "/live"`),
			Entry("relative", "live", `it must begin with "/"`),
			Entry("a leading authority", "//live", "begins an authority"),
			Entry("a backslash", `/\live`, "begins an authority exactly as"),
			Entry("a query", "/live?x=1", "a query or fragment ends the path"),
			Entry("a control byte", "/live\n", "browsers remove tab, CR and LF"),
		)
	})
})
