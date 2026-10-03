// Copyright 2026 Candace Labs

package routing_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"

	"github.com/candacelabs/csf/services/harness/routing"
	"github.com/candacelabs/csf/services/harness/session"
)

const haiku = "claude-haiku-4-5-20251001"

func decisions(state string) []routing.Decision {
	file, err := os.Open(filepath.Join(state, routing.Directory, routing.DecisionsFile))
	Expect(err).NotTo(HaveOccurred())
	defer func() { Expect(file.Close()).To(Succeed()) }()
	logged := []routing.Decision{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var decision routing.Decision
		Expect(json.Unmarshal(scanner.Bytes(), &decision)).To(Succeed())
		logged = append(logged, decision)
	}
	return logged
}

// closeRun records that the virtual session's turn executor closed, as the
// session runner does.
func closeRun(state string, virtual string) {
	writeRun(state, virtual, `{"level":"INFO","`+session.KeyEventType+`":"`+session.EventTypeSessionClosed+`"}`)
}

var _ = Describe("Router", func() {
	var (
		ctx    context.Context
		state  string
		now    time.Time
		router *routing.Router
	)

	BeforeEach(func() {
		ctx = context.Background()
		state = GinkgoT().TempDir()
		now = time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
		var err error
		router, err = routing.NewRouter(routing.WithStateDirectory(state), routing.WithClock(func() time.Time { return now }))
		Expect(err).NotTo(HaveOccurred())
	})

	request := func(virtual string, key string, summary string) session.RouteRequest {
		return session.RouteRequest{Virtual: virtual, Session: "conversation-" + virtual, Model: haiku, Agent: "miner/0a1b2c3d4e5f", Key: key, Summary: summary}
	}
	route := func(request session.RouteRequest) string {
		real, err := router.Route(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		return real
	}

	It("opens a new real session when no existing one fits, and logs why", func() {
		Expect(route(request("v1", "ticket/1", "GRAPH: pkg/graph replaces pkg/dag"))).To(Equal("conversation-v1"))
		Expect(decisions(state)).To(ConsistOf(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
			"Virtual": Equal("v1"), "Real": Equal("conversation-v1"), "Arm": Equal(routing.ArmNew), "Reason": Equal(routing.ReasonNoFit),
		})))
	})

	It("attaches a virtual session on the same ticket to the closed real session, warm", func() {
		route(request("v1", "ticket/1", "GRAPH: pkg/graph replaces pkg/dag"))
		closeRun(state, "v1")
		now = now.Add(10 * time.Minute)
		Expect(route(request("v2", "ticket/1", "anything"))).To(Equal("conversation-v1"))
		Expect(decisions(state)[1]).To(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
			"Virtual": Equal("v2"), "Real": Equal("conversation-v1"), "Arm": Equal(routing.ArmAttach), "Reason": Equal(routing.ReasonKey),
			"Score": Equal(1.0), "PredictedHit": BeNumerically("~", 1-10.0/50, 1e-9),
		}))
	})

	It("attaches on title overlap at or above the threshold, and not below it", func() {
		route(request("v1", "ticket/1", "cache miner hit ratio baseline"))
		closeRun(state, "v1")
		// {cache, miner, hit, ratio} of 5 distinct words: 0.8.
		Expect(route(request("v2", "ticket/2", "cache miner hit ratio"))).To(Equal("conversation-v1"))
		Expect(decisions(state)[1].Reason).To(Equal(routing.ReasonSummary))
		Expect(decisions(state)[1].Score).To(BeNumerically("~", 0.8, 1e-9))
		closeRun(state, "v2")
		// The attached real session keeps its first title: {cache} of 7
		// distinct words.
		Expect(route(request("v3", "ticket/3", "cache docker images"))).To(Equal("conversation-v3"))
	})

	It("never attaches to a real session whose holder is still open, or that runs another model", func() {
		route(request("v1", "ticket/1", "same title"))
		Expect(route(request("v2", "ticket/1", "same title"))).To(Equal("conversation-v2"))
		closeRun(state, "v1")
		other := request("v3", "ticket/1", "same title")
		other.Model = "claude-opus-5-5"
		Expect(route(other)).To(Equal("conversation-v3"))
	})

	It("never attaches a conversation whose holder was closed and then resumed by a restart", func() {
		route(request("v1", "ticket/1", "same title"))
		writeRun(state, "v1",
			`{"level":"INFO","`+session.KeyEventType+`":"`+session.EventTypeSessionClosed+`"}`,
			`{"level":"INFO","`+session.KeyEventType+`":"`+session.EventTypeRunResumed+`"}`)
		Expect(route(request("v2", "ticket/1", "same title"))).To(Equal("conversation-v2"))
		Expect(decisions(state)[1].Arm).To(Equal(routing.ArmNew))
	})

	It("never attaches another agent's conversation, whose recorded system prompt is not its own", func() {
		route(request("v1", "ticket/1", "same title"))
		closeRun(state, "v1")
		other := request("v2", "ticket/1", "same title")
		other.Agent = "responder/6f5e4d3c2b1a"
		Expect(route(other)).To(Equal("conversation-v2"))
		Expect(decisions(state)[1].Arm).To(Equal(routing.ArmNew))
	})

	It("prefers a warm real session over a better-fitting cold one", func() {
		route(request("cold", "ticket/1", "seed directory cleanup"))
		closeRun(state, "cold")
		now = now.Add(routing.DefaultTTL)
		// 3 of 4 distinct words: 0.75, a worse fit than the cold one's key.
		route(request("warm", "ticket/2", "router cache affinity v1"))
		closeRun(state, "warm")
		Expect(route(request("v", "ticket/1", "router cache affinity"))).To(Equal("conversation-warm"))
	})
})
