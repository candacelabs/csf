// Copyright 2026 Candace Labs

package opsview_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/services/harness/endpoint"
	"github.com/candacelabs/csf/services/opsview"
)

var _ = Describe("The endpoints panel", func() {
	It("lists every endpoint at each address with how it is served and its users, and every retired address", func() {
		registry := endpoint.Registry{}.Register(endpoint.ServeEndpoints([]string{"127.0.0.1:14120", "127.0.0.1:14122"}, []string{"127.0.0.1:14121"}), "2026-10-05")
		registry, err := registry.Retire(endpoint.Retirement{Address: "127.0.0.1:14122", MovedTo: "127.0.0.1:14120", RetiredOn: "2026-10-06", Acknowledgement: "drop 14122"})
		Expect(err).NotTo(HaveOccurred())
		directory := GinkgoT().TempDir()
		client := connect(mountView(directory, newSpecWatcher(gomock.NewController(GinkgoT())).mock, opsview.WithEndpoints(registry)))

		html, present := unfold(client, opsview.EndpointsRegion, "endpoints"), true
		Expect(present).To(BeTrue(), "the panel is on the first paint")
		Expect(html).To(ContainSubstring(`<strong>Workbench</strong> <a href="http://127.0.0.1:14120/">http://127.0.0.1:14120/</a> <span class="via">listen, since 2026-10-05</span> <span class="users">used by the operator, in a browser</span>`))
		Expect(html).To(ContainSubstring(`<a href="http://127.0.0.1:14121/mcp">http://127.0.0.1:14121/mcp</a> <span class="via">redirects to port 14120, since 2026-10-05</span>`))
		Expect(strings.Count(html, "data-opsview-endpoint=")).To(Equal(6), "three endpoints at each of the two active addresses")
		Expect(html).NotTo(ContainSubstring(`data-opsview-endpoint="http://127.0.0.1:14122/"`))
		Expect(html).To(ContainSubstring(`<li data-opsview-retired="127.0.0.1:14122">127.0.0.1:14122 moved to 127.0.0.1:14120 on 2026-10-06: “drop 14122”</li>`))
	})

	It("says so when nothing is registered", func() {
		client := connect(mountView(GinkgoT().TempDir(), newSpecWatcher(gomock.NewController(GinkgoT())).mock))
		html := unfold(client, opsview.EndpointsRegion, "endpoints")
		Expect(html).To(ContainSubstring("No endpoint registered."))
	})
})
