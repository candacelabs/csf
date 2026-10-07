// Copyright 2026 Candace Labs

package claudecode

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/net/model"
)

var _ = Describe("eventStream", func() {
	session := uuid.MustParse("3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a01")
	initEvent := `{"type":"system","subtype":"init","session_id":"3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a01"}`
	resultEvent := `{"type":"result","subtype":"success"}`

	newStream := func() *eventStream {
		report, err := model.NewTurnReport(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), ProviderName, session, 1)
		Expect(err).NotTo(HaveOccurred())
		return newEventStream(session, report)
	}

	It("reassembles lines split across writes", func() {
		stream := newStream()
		whole := initEvent + "\n" + resultEvent + "\n"
		for _, chunk := range []string{whole[:7], whole[7:40], whole[40:]} {
			written, err := stream.Write([]byte(chunk))
			Expect(err).NotTo(HaveOccurred())
			Expect(written).To(Equal(len(chunk)))
		}
		stream.finish()

		Expect(stream.events).To(HaveLen(2))
		Expect(string(stream.events[0].Raw)).To(Equal(initEvent))
		Expect(stream.events[1].Type).To(Equal(EventTypeResult))
		Expect(stream.established).To(BeTrue())
		Expect(stream.resulted).To(BeTrue())
		Expect(stream.failure).NotTo(HaveOccurred())
	})

	It("reads a last line written without a newline", func() {
		stream := newStream()
		_, err := stream.Write([]byte(initEvent + "\n" + resultEvent))
		Expect(err).NotTo(HaveOccurred())
		Expect(stream.events).To(HaveLen(1))

		stream.finish()

		Expect(stream.events).To(HaveLen(2))
		Expect(stream.resulted).To(BeTrue())
	})

	It("skips blank lines and does not count them as events", func() {
		stream := newStream()
		_, err := stream.Write([]byte("\n  \n" + resultEvent + "\r\n"))
		Expect(err).NotTo(HaveOccurred())
		stream.finish()

		Expect(stream.events).To(HaveLen(1))
		Expect(string(stream.events[0].Raw)).To(Equal(resultEvent))
	})

	It("keeps the first malformed line and keeps reading", func() {
		stream := newStream()
		_, err := stream.Write([]byte("oops\n[1,2]\n" + resultEvent + "\n"))
		Expect(err).NotTo(HaveOccurred())

		var malformed *MalformedEventError
		Expect(stream.failure).To(BeAssignableToTypeOf(malformed))
		Expect(stream.failure.(*MalformedEventError).Line).To(Equal(1))
		Expect(stream.events).To(HaveLen(1))
	})

	It("does not alias the bytes it was handed", func() {
		stream := newStream()
		chunk := []byte(resultEvent + "\n")
		_, err := stream.Write(chunk)
		Expect(err).NotTo(HaveOccurred())
		chunk[2] = 'X'

		Expect(string(stream.events[0].Raw)).To(Equal(resultEvent))
	})
})

var _ = Describe("encodeTurn", func() {
	It("frames each message as one compact line", func() {
		input, err := encodeTurn(&Turn{Messages: []json.RawMessage{
			json.RawMessage(" { \"type\" : \"user\" } "),
			json.RawMessage(`{"type":"user"}`),
		}})

		Expect(err).NotTo(HaveOccurred())
		Expect(string(input)).To(Equal("{\"type\":\"user\"}\n{\"type\":\"user\"}\n"))
	})

	It("names the message that is not an object", func() {
		_, err := encodeTurn(&Turn{Messages: []json.RawMessage{json.RawMessage(`{"type":"user"}`), json.RawMessage(`"text"`)}})

		Expect(err).To(MatchError(ErrMalformedInput))
		Expect(err.Error()).To(ContainSubstring("message 1"))
	})
})

var _ = Describe("argumentsFor", func() {
	It("creates the session on a first turn and resumes it afterwards", func() {
		session := uuid.MustParse("3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a01")
		brain := &ClaudeCodeBrain{session: session, arguments: []string{"--model", "sonnet"}}

		Expect(brain.argumentsFor(sessionState{})).To(Equal([]string{
			"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
			"--session-id", session.String(), "--model", "sonnet",
		}))
		Expect(brain.argumentsFor(sessionState{established: true})).To(ContainElements("--resume", session.String()))
	})
})

var _ = Describe("launch prefix", func() {
	session := uuid.MustParse("3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a01")

	It("starts Claude Code directly when no prefix is set", func() {
		brain := &ClaudeCodeBrain{session: session, executable: "claude"}

		Expect(brain.processExecutable()).To(Equal("claude"))
		Expect(brain.processArguments(sessionState{})).To(Equal(brain.argumentsFor(sessionState{})))
	})

	It("starts the prefix's head with the executor behind the prefix's tail", func() {
		brain := &ClaudeCodeBrain{
			session:      session,
			executable:   "claude",
			launchPrefix: []string{"launcher", "--policy", "/run/policy.json", "--"},
		}

		Expect(brain.processExecutable()).To(Equal("launcher"))
		argv := brain.processArguments(sessionState{})
		Expect(argv[:4]).To(Equal([]string{"--policy", "/run/policy.json", "--", "claude"}))
		Expect(argv[4:]).To(Equal(brain.argumentsFor(sessionState{})))
	})
})
