package main

import (
	"context"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/examples/widget/clusterheartbeats"
	"github.com/candacelabs/csf/examples/widget/nodestatus"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/pkg/widget/widgettest"
)

var _ = Describe("Generated keyed widgets through the registry adapter", func() {
	It("names each root, accessible title and animated scene under its assigned region", func() {
		var markup strings.Builder
		for _, region := range []string{"card.alpha", "card.beta"} {
			card, mountError := widgettest.Mount(context.Background(),
				clusterheartbeats.NewClusterHeartbeatsAt[live.AnonymousIdentity](region))
			Expect(mountError).ToNot(HaveOccurred())
			Expect(card.Region()).To(Equal(region))
			Expect(card.Apply(snapshot(7, true, 3))).To(BeEmpty())
			rendered, renderError := card.Render(context.Background())
			Expect(renderError).ToNot(HaveOccurred())
			markup.WriteString(rendered.String())
		}
		for _, region := range []string{"card.alpha", "card.beta"} {
			Expect(strings.Count(markup.String(), `data-gotth-region="`+region+`"`)).To(Equal(1))
			Expect(strings.Count(markup.String(), `id="`+region+`-title"`)).To(Equal(1))
			Expect(strings.Count(markup.String(), `aria-labelledby="`+region+`-title"`)).To(Equal(1))
			Expect(strings.Count(markup.String(), `id="`+region+`-tick-7"`)).To(Equal(1))
		}
		Expect(markup.String()).ToNot(ContainSubstring(clusterheartbeats.ClusterHeartbeatsTitleID))
	})

	It("preserves the default constructor's registered region and accessible title", func() {
		card, mountError := widgettest.Mount(context.Background(),
			clusterheartbeats.NewClusterHeartbeats[live.AnonymousIdentity]())
		Expect(mountError).ToNot(HaveOccurred())
		Expect(card.Region()).To(Equal(clusterheartbeats.ClusterHeartbeatsRegion))
		rendered, renderError := card.Render(context.Background())
		Expect(renderError).ToNot(HaveOccurred())
		Expect(rendered.String()).To(ContainSubstring(`id="` + clusterheartbeats.ClusterHeartbeatsTitleID + `"`))
	})
})

// snapshot is one cluster delivery, as the wire carries it.
func snapshot(sequence uint64, leaderKnown bool, aliveVoters int) live.Event {
	return live.Event{
		Name: clusterheartbeats.ClusterHeartbeatsEventSnapshot,
		Fields: live.NewFields(map[string]string{
			"sequence":      strconv.FormatUint(sequence, 10),
			"connected":     "true",
			"authoritative": "true",
			"leader_known":  strconv.FormatBool(leaderKnown),
			"has_quorum":    "true",
			"term":          "7",
			"voters":        "3",
			"alive_voters":  strconv.Itoa(aliveVoters),
		}),
	}
}

var _ = Describe("The generated widgets' dirty declarations", func() {
	// A generated widget answers for itself whether a transition reached its
	// region, from its document's computed dirty projection. Over-declaring
	// costs a suppressed render; under-declaring is a correctness bug the
	// compiler cannot see — a region that stops updating for reasons nothing
	// explains — so it is asserted against the markup rather than against the
	// projection it came from.
	It("declare every transition that moves their markup", func() {
		config, configError := hostWidgets().LiveConfig(widget.MountOptions[live.AnonymousIdentity]{
			Origins:      []string{"http://127.0.0.1:8080"},
			Authenticate: live.Anonymous,
			Authorize:    live.AllowAll[live.AnonymousIdentity],
			CSRF:         live.NoCSRFCheck,
		})
		Expect(configError).ToNot(HaveOccurred())

		initial, _, initError := config.Init(context.Background(), live.Session[live.AnonymousIdentity]{})
		Expect(initError).ToNot(HaveOccurred())

		// One of everything that can move either widget: a tick, an election,
		// the pause control, the health check, and the two notices the runtime
		// mints rather than a browser sending them.
		// GinkgoTB rather than GinkgoT: the library's assertion takes a
		// testing.TB, which only the wrapper satisfies.
		livetest.AssertDirtyComplete(GinkgoTB(), config, initial, []live.Event{
			snapshot(1, true, 3),
			snapshot(2, false, 2),
			snapshot(3, true, 3),
			// Two deliveries apart only in the tick. Nothing the bindings read
			// has moved, so this is the transition that catches a dirty
			// declaration which forgot that the scene's own identity is the
			// tick — the whole reason the projection counts it as read.
			snapshot(4, true, 3),
			{Name: clusterheartbeats.ClusterHeartbeatsEventToggleMotion},
			{Name: clusterheartbeats.ClusterHeartbeatsEventToggleMotion},
			{
				Name:   nodestatus.NodeStatusEventHealth,
				Fields: live.NewFields(map[string]string{"reachable": "false"}),
			},
			{
				Name:   nodestatus.NodeStatusEventHealth,
				Fields: live.NewFields(map[string]string{"reachable": "true"}),
			},
			{Name: live.SlowClientEvent},
			{Name: live.ClientRecoveredEvent},
			// A counter never walks backwards, so this delivery changes
			// nothing and neither region may claim it did.
			snapshot(2, true, 3),
		})
	})
})
