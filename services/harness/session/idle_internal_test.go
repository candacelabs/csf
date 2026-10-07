// Copyright 2026 Candace Labs

package session

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// One resumed turn's records: the request, the executor's init event, the
// first assistant event three and a half seconds later, and the result.
const (
	ttftRequested = `{"time":"2026-10-04T10:05:00.000Z","level":"INFO","msg":"turn requested","turn":2,"event_type":"harness_turn_requested"}` + "\n"
	ttftInit      = `{"time":"2026-10-04T10:05:01.200Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"out","event_type":"system","event":{"type":"system","subtype":"init"}}` + "\n"
	ttftInput     = `{"time":"2026-10-04T10:05:01.300Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"in","event_type":"assistant","event":{"type":"assistant"}}` + "\n"
	ttftAssistant = `{"time":"2026-10-04T10:05:03.500Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"out","event_type":"assistant","event":{"type":"assistant"}}` + "\n"
	ttftResult    = `{"time":"2026-10-04T10:05:09.000Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"out","event_type":"result","event":{"type":"result"}}` + "\n"
)

var _ = Describe("the resumed turn's time to first token", func() {
	var directory string

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
	})

	It("is the time from the turn's request to the executor's first assistant event", func() {
		Expect(os.WriteFile(filepath.Join(directory, EventsFile), []byte(ttftRequested+ttftInit+ttftInput+ttftAssistant+ttftResult), 0o600)).To(Succeed())
		Expect(timeToFirstToken(directory)).To(Equal(3500.0))
	})

	It("is zero for a turn that reported no assistant event, and for no log at all", func() {
		Expect(os.WriteFile(filepath.Join(directory, EventsFile), []byte(ttftRequested+ttftInit+ttftResult), 0o600)).To(Succeed())
		Expect(timeToFirstToken(directory)).To(BeZero())
		Expect(timeToFirstToken(filepath.Join(directory, "missing"))).To(BeZero())
	})
})
