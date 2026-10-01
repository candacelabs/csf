package conformance_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// Handshake rejection (FR-45, FR-46, FR-48)
//
// The ordering in protocol.md §8.1 is itself the security property: origin,
// then identity, then CSRF, and only then is any per-session memory allocated.
// These specs assert the outcome rather than the ordering, because the outcome
// is what a cross-origin page experiences.
// ---------------------------------------------------------------------------

// attempt dials without the harness's assumptions and reports what happened.
func attempt(cfg live.Config[tally, qaUser], origin string, sendOrigin bool) (int, error) {
	GinkgoHelper()

	app, err := live.New(cfg)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = app.Close(context.Background()) })

	ts := httptest.NewServer(app.Handler())
	DeferCleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	DeferCleanup(cancel)

	headers := http.Header{}
	if sendOrigin {
		headers.Set("Origin", origin)
	}

	conn, resp, err := websocket.Dial(ctx, wsURL(ts.URL), &websocket.DialOptions{
		HTTPHeader:   headers,
		Subprotocols: []string{"gotth-live.v1"},
	})
	if conn != nil {
		DeferCleanup(func() { _ = conn.CloseNow() })
	}
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	return status, err
}

var _ = Describe("The handshake, as a cross-origin attacker experiences it (FR-48)", func() {
	It("refuses an origin that is not on the allowlist", func() {
		status, err := attempt(qaConfig(), "https://evil.example", true)

		Expect(err).To(HaveOccurred(), "a cross-origin page established a live session")
		Expect(status).To(Equal(http.StatusForbidden))
	})

	// Deny by default. A request with no Origin header is not "same origin"; it
	// is a request whose origin cannot be checked, and the allowlist has no
	// entry for that.
	It("refuses a request that sends no origin at all", func() {
		status, err := attempt(qaConfig(), "", false)

		Expect(err).To(HaveOccurred(), "a request with no Origin header established a session")
		Expect(status).To(Equal(http.StatusForbidden))
	})

	It("refuses an origin that merely has the allowed one as a prefix", func() {
		status, err := attempt(qaConfig(), allowedOrigin+".evil.example", true)

		Expect(err).To(HaveOccurred(), "prefix matching would admit an attacker-owned domain")
		Expect(status).To(Equal(http.StatusForbidden))
	})

	It("refuses a connection whose identity hook fails, before any session exists", func() {
		cfg := qaConfig()
		cfg.Authenticate = func(request *http.Request) (qaUser, error) {
			return "", errors.New("no session cookie")
		}

		status, err := attempt(cfg, allowedOrigin, true)

		Expect(err).To(HaveOccurred(), "an unauthenticated request established a live session")
		Expect(status).To(Equal(http.StatusUnauthorized))
	})

	It("refuses a connection whose CSRF token does not check out", func() {
		cfg := qaConfig()
		cfg.CSRF = func(request *http.Request) error { return errors.New("bad token") }

		status, err := attempt(cfg, allowedOrigin, true)

		Expect(err).To(HaveOccurred(), "a request with no valid CSRF token established a session")
		Expect(status).To(Equal(http.StatusForbidden))
	})

	It("admits the configured origin", func() {
		_, err := attempt(qaConfig(), allowedOrigin, true)

		Expect(err).NotTo(HaveOccurred())
	})
})

// ---------------------------------------------------------------------------
// FR-47 — the authorization hook cannot be routed around
// ---------------------------------------------------------------------------

var _ = Describe("The per-event authorization hook (FR-47)", func() {
	// The requirement is that no frame kind bypasses the hook, and the reducer
	// is what "bypass" would mean in practice. So the assertion is over the
	// reducer: whatever the client sends, if Authorize refused it then Reduce
	// never ran.
	It("runs before the reducer, and no frame kind reaches state without it", func() {
		var mu sync.Mutex
		var authorized []string
		var reduced []string

		d := dial(func(c *live.Config[tally, qaUser]) {
			c.Authorize = func(_ context.Context, _ live.Session[qaUser], ev live.Event) error {
				mu.Lock()
				authorized = append(authorized, ev.Name)
				mu.Unlock()
				return &live.DenyError{Reason: "QA denies everything"}
			}
			c.Reduce = func(s tally, ev live.Event) (tally, []live.Effect[qaUser]) {
				mu.Lock()
				reduced = append(reduced, ev.Name)
				mu.Unlock()
				s.N++
				return s, nil
			}
		})

		// Every client→server kind, including the three that are plumbing.
		d.event("qa.increment", 1)
		Expect(d.writeFrame(d.envelope(&pb.ResyncRequest{
			LastAppliedSeq: 1, Reason: pb.ResyncReason_GAP,
		}))).To(Succeed())
		Expect(d.writeFrame(d.envelope(&pb.Ack{ServerSeq: 1}))).To(Succeed())
		Expect(d.writeFrame(d.envelope(&pb.Heartbeat{Nonce: 1, IntervalMs: 20000}))).To(Succeed())
		Expect(d.writeFrame(d.envelope(&pb.ClientTelemetry{
			PatchId: 1, MorphMicros: 10, ApplyMicros: 10,
		}))).To(Succeed())

		d.drainUntilQuiet(700 * time.Millisecond)

		mu.Lock()
		defer mu.Unlock()

		Expect(reduced).To(BeEmpty(),
			"the reducer ran for %v despite every event being denied", reduced)
		Expect(authorized).To(ContainElement("qa.increment"),
			"the ordinary event did not reach the authorization hook")
		Expect(authorized).To(ContainElement("gotth.resync"),
			"a resync request reached the actor without passing the authorization hook")
		Expect(d.patches()).To(BeEmpty(), "a denied session produced a patch")
	})

	It("leaves state untouched when the hook denies", func() {
		deny := true
		d := dial(func(c *live.Config[tally, qaUser]) {
			c.Authorize = func(_ context.Context, _ live.Session[qaUser], ev live.Event) error {
				if deny {
					return &live.DenyError{Reason: "not yet"}
				}
				return nil
			}
		})

		d.event("qa.increment", 1)
		Expect(d.nextError().GetCode()).To(Equal(pb.ErrorCode_UNAUTHORIZED))

		deny = false
		d.event("qa.increment", d.highestSeq())
		patch := d.nextPatch()

		Expect(patch.GetUpdates()[0].GetHtml()).To(Equal("<b>1</b>"),
			"the denied event mutated state: the count should be 1, not 2")
	})

	It("closes the connection on a fatal denial", func() {
		d := dial(func(c *live.Config[tally, qaUser]) {
			c.Authorize = func(ctx context.Context, session live.Session[qaUser], event live.Event) error {
				return &live.FatalDenyError{Reason: "revoked"}
			}
		})

		d.event("qa.increment", 1)

		Expect(d.closed(5*time.Second)).To(BeTrue(),
			"a fatal denial must close the connection")
	})
})

// ---------------------------------------------------------------------------
// Event semantics the browser half will rely on in checkpoint 2
// ---------------------------------------------------------------------------

var _ = Describe("Double submission, at the protocol level", func() {
	// RFC-0001 chooses at-most-once delivery, so the library does not
	// deduplicate: two frames are two events, even when the client reuses its
	// own correlation handle. That is the semantics, and pinning it here is
	// what stops it drifting into an accidental dedup that a caller starts to
	// depend on.
	It("treats a repeated client_ref as two distinct events, not one", func() {
		d := dial(nil)

		same := &pb.Event{
			ClientRef: 7, Name: "qa.increment", FragmentId: "count", SeenServerSeq: 1,
		}
		Expect(d.writeFrame(d.envelope(same))).To(Succeed())
		first := d.nextPatch()
		Expect(d.writeFrame(d.envelope(same))).To(Succeed())
		second := d.nextPatch()

		Expect(first.GetUpdates()[0].GetHtml()).To(Equal("<b>1</b>"))
		Expect(second.GetUpdates()[0].GetHtml()).To(Equal("<b>2</b>"),
			"the library deduplicated a repeated client_ref: at-most-once delivery does not mean at-most-once effect")

		Expect(second.GetOrigin().GetEventId()).NotTo(Equal(first.GetOrigin().GetEventId()),
			"two events shared one server-minted event id, which would collapse their provenance")
		Expect(second.GetOrigin().GetClientRef()).To(Equal(first.GetOrigin().GetClientRef()),
			"the client's own handle is echoed verbatim, duplicate or not")
	})
})

var _ = Describe("A stale view, at the protocol level (H-8)", func() {
	// seen_server_seq is the causation edge: what the user was looking at when
	// they acted. It may lag arbitrarily — that is an ordinary stale view — but
	// it may not lead, because a client cannot have seen a patch the server has
	// not sent.
	It("accepts an event whose view is behind the current sequence", func() {
		d := dial(nil)
		for i := 0; i < 3; i++ {
			d.event("qa.increment", d.highestSeq())
			d.nextPatch()
		}
		Expect(d.highestSeq()).To(BeNumerically(">=", 4))

		// Deliberately stale: claims to have seen only the first snapshot.
		d.event("qa.increment", 1)

		patch := d.nextPatch()
		Expect(patch.GetUpdates()[0].GetHtml()).To(Equal("<b>4</b>"),
			"a stale-but-legal view was refused: lagging is not a violation")
	})

	It("accepts an event at exactly the highest emitted sequence", func() {
		d := dial(nil)
		d.event("qa.increment", d.highestSeq())
		d.nextPatch()

		high := d.highestSeq()
		d.event("qa.increment", high)

		Expect(d.nextPatch()).NotTo(BeNil(), "seen_server_seq == highest emitted must be legal")
	})

	It("refuses an event claiming to have seen one patch more than exists", func() {
		d := dial(nil)
		d.event("qa.increment", d.highestSeq())
		d.nextPatch()

		high := d.highestSeq()
		d.event("qa.increment", high+1)

		e := d.nextError()
		Expect(e.GetCode()).To(Equal(pb.ErrorCode_INVALID_FRAME))
		Expect(e.GetEventId()).NotTo(BeZero(), "the refusal must name the event it refused")
	})

	It("keeps serving after refusing a forged causation claim", func() {
		d := dial(nil)
		d.event("qa.increment", d.highestSeq())
		d.nextPatch()

		d.event("qa.increment", d.highestSeq()+99)
		Expect(d.nextError().GetCode()).To(Equal(pb.ErrorCode_INVALID_FRAME))

		// A forged causation claim is refused per event, not per connection.
		d.event("qa.increment", d.highestSeq())
		Expect(d.nextPatch()).NotTo(BeNil(),
			"one forged causation claim closed a session that should have survived it")
	})
})

var _ = Describe("Unknown names, which are default-deny", func() {
	It("refuses an event name the application never declared", func() {
		d := dial(nil)

		Expect(d.writeFrame(d.envelope(&pb.Event{
			ClientRef: 1, Name: "qa.undeclared", FragmentId: "count", SeenServerSeq: 1,
		}))).To(Succeed())

		e := d.nextError()
		Expect(e.GetCode()).To(Equal(pb.ErrorCode_UNKNOWN_EVENT))
		Expect(d.patches()).To(BeEmpty())
	})

	It("refuses an event naming a fragment the application never declared", func() {
		d := dial(nil)

		Expect(d.writeFrame(d.envelope(&pb.Event{
			ClientRef: 1, Name: "qa.increment", FragmentId: "nosuchfragment", SeenServerSeq: 1,
		}))).To(Succeed())

		Expect(d.nextError().GetCode()).To(Equal(pb.ErrorCode_UNKNOWN_FRAGMENT))
	})
})

var _ = Describe("The first frame on a connection (H-10)", func() {
	It("is a Snapshot carrying the session parameters", func() {
		d := dial(nil)

		Expect(d.snapshot).NotTo(BeNil())
		Expect(d.snapshot.GetServerSeq()).To(Equal(uint64(1)))
		Expect(d.snapshot.GetOrigin().GetKind()).To(Equal(pb.OriginKind_MOUNT))
		Expect(d.snapshot.GetSupersededFromSeq()).To(BeZero(),
			"H-13: a session's first snapshot supersedes nothing")
		Expect(d.snapshot.GetSupersededThroughSeq()).To(BeZero())

		// The parameters the client needs in order to behave, in band.
		Expect(d.snapshot.GetHeartbeatIntervalMs()).To(BeNumerically(">=", 1000))
		Expect(d.snapshot.GetMaxInboundFrameBytes()).To(BeNumerically(">=", 1024))
		Expect(d.snapshot.GetAckWindow()).To(BeNumerically(">=", 1))
		Expect(len(d.sessionID)).To(Equal(16))
	})

	It("renders every declared fragment, not only the ones a patch would carry", func() {
		d := dial(nil)

		var ids []string
		for _, u := range d.snapshot.GetUpdates() {
			ids = append(ids, u.GetFragmentId())
		}
		Expect(ids).To(ConsistOf("count", "label"))
	})
})

var _ = Describe("Every payload on the wire (FR-3, G5)", func() {
	// The wire audit, in the form a single session can support: every binary
	// payload received parses as a Frame and re-encodes to the same bytes. The
	// pump asserts the parse half on arrival; this asserts the re-encode half,
	// which is what makes "it is a Frame" stronger than "it did not error".
	It("parses as a Frame and re-encodes byte-identically", func() {
		d := exercise(9)

		frames, raw := d.captured()
		Expect(frames).NotTo(BeEmpty())

		for i, f := range frames {
			reencoded, err := marshalDeterministic(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(reencoded).To(Equal(raw[i]),
				"frame %d did not re-encode to the bytes it arrived as", i)
		}
	})
})

// marshalDeterministic re-encodes a decoded frame. The schema contains no map
// field by construction, so the only source of encoder nondeterminism the
// protobuf runtime has is absent and the round trip is a genuine byte
// comparison rather than a normalisation.
func marshalDeterministic(f *pb.Frame) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.Marshal(f)
}
