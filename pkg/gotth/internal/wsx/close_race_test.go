package wsx_test

import (
	"context"
	"fmt"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
)

// C-34: Close reported a successful drain over sessions it never touched.
//
// L9-1 measured it by dialling, starting a reading goroutine, calling Close and
// then checking: Close returned nil, Sessions() still reported 1, and the
// client's socket was still live 500 ms later — 32 of 300 rounds at af9057d1
// and 13 of 300 at the parent 9c85fe43. It is pre-existing and was widened
// ~2.5x by 5a2ca417 moving registration onto a goroutine ServeHTTP does not
// wait for.
//
// # Why this is a repeat-count spec and not a single round
//
// It is a race, so one round proves nothing: the unfixed code passes a single
// round about nine times in ten. The rate is the evidence, so the spec runs the
// rounds and reports the escape rate as a ReportEntry whether it passes or
// fails — a number that says "0 of 300" is worth more than a green dot, and a
// number that says "31 of 300" is what tells the next person the fix regressed
// rather than that the suite is flaky.
//
// Measured here, in dis-gotth-live:latest under -race:
//
//	before the fix   28 of 300 escaped   (Close returned nil over a live session)
//	after the fix     0 of 300
//
// # What an escape is
//
// Exactly what the documented contract forbids. `go doc` says "Close drains
// EVERY session" and docs/api-surface.md says "Drains sessions, closing each
// with GOING_AWAY". So a round escapes if Close returns nil and EITHER the
// registry still reports a session OR the client's socket never saw a close.
// Both halves are asserted, because a fix that only emptied the registry would
// satisfy one of them while leaving the socket open.
const closeRaceRounds = 300

var _ = Describe("Close, against a session still being established (C-34)", func() {
	It("never returns success over a session it did not drain", func() {
		var (
			registryEscapes int
			socketEscapes   int
		)

		for i := 0; i < closeRaceRounds; i++ {
			func() {
				s := newServer(nil)
				defer s.http.Close()

				ctx := contextWithTimeout(10 * time.Second)

				// The dial races Close deliberately. It is NOT followed by a
				// read of the mount snapshot: waiting for the snapshot would
				// wait out the very window this spec exists to hit, which is
				// how a spec of this shape passes on broken code.
				c, _, err := s.dial(ctx, nil)
				if err != nil {
					// A refused upgrade is the handler declining the session
					// outright, which is the correct outcome and not an escape:
					// there is nothing to drain.
					return
				}
				defer c.CloseNow() //nolint:errcheck // the round is over

				// A real client is always reading, and a graceful close is a
				// handshake rather than a hang-up.
				//
				// It reads in a LOOP, and that is not incidental. This round
				// deliberately does not consume the mount snapshot before
				// racing Close — waiting for the snapshot would wait out the
				// very window the spec exists to hit — so the first frame the
				// client sees may be the snapshot, the close, or a snapshot
				// followed by a close, depending on which side won the write
				// mutex. A single Read would score "received a snapshot" as
				// "never received a close", which is a spec defect that looks
				// exactly like the product defect. Reading to error is what a
				// browser does anyway.
				closed := make(chan error, 1)
				go func() {
					defer GinkgoRecover()
					for {
						_, _, readErr := c.Read(context.Background())
						if readErr != nil {
							closed <- readErr
							return
						}
					}
				}()

				if closeErr := s.handler.Close(ctx); closeErr != nil {
					// A Close that reports failure is not this defect: the
					// contract it breaks is reporting SUCCESS over a session it
					// never touched.
					return
				}

				if live := s.handler.Sessions(); live != 0 {
					registryEscapes++
					return
				}

				select {
				case readErr := <-closed:
					if websocket.CloseStatus(readErr) != websocket.StatusCode(protocol.CloseGoingAway) {
						socketEscapes++
					}
				case <-time.After(500 * time.Millisecond):
					// L9-1's probe used exactly this: the socket still live half
					// a second after a Close that returned nil.
					socketEscapes++
				}
			}()
		}

		AddReportEntry("C-34", fmt.Sprintf(
			"%d rounds: %d left a session in the registry after Close returned nil, "+
				"%d left the client's socket without a going-away close",
			closeRaceRounds, registryEscapes, socketEscapes))

		Expect(registryEscapes).To(BeZero(),
			"Close returned nil in %d of %d rounds while Sessions() still reported a live session, "+
				"so the drain is reporting success over sessions it never touched (C-34)",
			registryEscapes, closeRaceRounds)
		Expect(socketEscapes).To(BeZero(),
			"Close returned nil in %d of %d rounds without the client's socket ever receiving "+
				"GOING_AWAY, so a session outlived the Close that claimed to have drained it (C-34)",
			socketEscapes, closeRaceRounds)
	})

	It("refuses a session it cannot drain rather than half-starting one", func() {
		// The other half of the fix, and the reason register returns an error.
		// A connection admitted just before draining begins is closed with the
		// going-away code by the handler goroutine itself, before any session
		// goroutine exists for it — so the client is told, rather than being
		// left holding a socket nobody is serving.
		s := newServer(nil)
		defer s.http.Close()
		ctx := contextWithTimeout(5 * time.Second)

		Expect(s.handler.Close(ctx)).To(Succeed())

		_, resp, err := s.dial(ctx, nil)
		Expect(err).To(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(503),
			"a dial after draining began was not refused at the HTTP layer, so the admission "+
				"check is no longer the first gate")
		Expect(s.handler.Sessions()).To(BeZero())
	})
})
