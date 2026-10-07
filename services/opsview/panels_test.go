// Copyright 2026 Candace Labs

package opsview_test

import (
	"os"
	"path/filepath"
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/services/opsview"
	"github.com/candacelabs/csf/services/views"
)

// panelLink finds a link to the Grafana panel of a metric family.
var panelLink = regexp.MustCompile(`href="` + views.PanelPath + `([a-z0-9_]+)"`)

var _ = Describe("the Workbench's history links", func() {
	It("link every tile's definition and the loop's headline numbers to a panel that shows their history", func() {
		state := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(state, opsview.LoopFile), []byte(loopSnapshot), 0o600)).To(Succeed())
		client := connect(mountView(state, newSpecWatcher(gomock.NewController(GinkgoT())).mock))
		for _, tile := range []string{"sessions", "running", "merges", "spend"} {
			client.Send(opsview.EventDefine, opsview.SummaryRegion, map[string]string{opsview.FieldTile: tile})
			tiles := client.WaitFor(opsview.SummaryRegion, contains(`data-opsview="definition"`))
			client.Ack(tiles.Patch.ServerSeq)
			html, _ := tiles.Patch.Fragment(opsview.SummaryRegion)
			links := panelLink.FindAllStringSubmatch(html, -1)
			Expect(links).To(HaveLen(1), tile)
			_, found := views.PanelFor(links[0][1])
			Expect(found).To(BeTrue(), "no panel shows "+links[0][1])
			client.Send(opsview.EventDefine, opsview.SummaryRegion, map[string]string{opsview.FieldTile: tile})
			client.Ack(client.WaitFor(opsview.SummaryRegion, func(html string) bool { return !contains(`data-opsview="definition"`)(html) }).Patch.ServerSeq)
		}

		// The loop panel as a viewer opens it: the links are read from what the
		// service renders, not from the template file, which no test binary can
		// assume sits beside its working directory.
		client.Send(opsview.EventSection, opsview.LoopRegion, map[string]string{opsview.FieldSection: "loop"})
		loop := client.WaitFor(opsview.LoopRegion, contains(`data-opsview="compounding-interval"`))
		client.Ack(loop.Patch.ServerSeq)
		html, _ := loop.Patch.Fragment(opsview.LoopRegion)
		links := panelLink.FindAllStringSubmatch(html, -1)
		Expect(links).To(HaveLen(2), "the loop's struggle rate and compounding factor")
		for _, link := range links {
			_, found := views.PanelFor(link[1])
			Expect(found).To(BeTrue(), "no panel shows "+link[1])
		}
	})
})
