// Copyright 2026 Candace Labs

package endpoint_test

import (
	"net/http"
	"net/http/httptest"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/endpoint"
)

const (
	primary   = "127.0.0.1:14120"
	tailnet   = "192.0.2.7:14120"
	formerOps = "127.0.0.1:14121"
	firstDay  = "2026-10-05"
	laterDay  = "2026-10-06"
)

var _ = Describe("the endpoint registry", func() {
	Describe("ServeEndpoints", func() {
		It("serves the Workbench, the API and MCP on every listen and redirect address", func() {
			served := endpoint.ServeEndpoints([]string{primary, tailnet}, []string{formerOps})
			Expect(served).To(HaveLen(3))
			for _, each := range served {
				Expect(each.Owner).To(Equal(endpoint.OwnerServe))
				Expect(each.Addresses).To(Equal([]endpoint.Address{
					{Address: primary, Via: endpoint.ViaListen},
					{Address: tailnet, Via: endpoint.ViaListen},
					{Address: formerOps, Via: endpoint.ViaRedirect, RedirectsTo: "14120"},
				}))
			}
			Expect(served[0].URL(formerOps)).To(Equal("http://127.0.0.1:14121/"))
			Expect(served[2].URL(primary)).To(Equal("http://127.0.0.1:14120/mcp"))
		})
	})

	Describe("a serve that drops an address", func() {
		var registry endpoint.Registry

		BeforeEach(func() {
			registry = endpoint.Registry{}.Register(endpoint.ServeEndpoints([]string{primary}, []string{formerOps}), firstDay)
		})

		It("is unserved for every endpoint at the dropped address, naming its users", func() {
			unserved := registry.Unserved(endpoint.ServeEndpoints([]string{primary}, nil))
			Expect(unserved).To(HaveLen(3))
			Expect(unserved[0].Address).To(Equal(formerOps))
			Expect(unserved[0].String()).To(Equal("Workbench at http://127.0.0.1:14121/ (used by the operator, in a browser)"))
		})

		It("is served again by a listen in place of the redirect", func() {
			Expect(registry.Unserved(endpoint.ServeEndpoints([]string{primary, formerOps}, nil))).To(BeEmpty())
		})

		It("is served once the operator retires the address, which leaves the active set", func() {
			retired, err := registry.Retire(endpoint.Retirement{Address: formerOps, MovedTo: primary, RetiredOn: laterDay, Acknowledgement: "yes, drop 14121"})
			Expect(err).NotTo(HaveOccurred())
			Expect(retired.Unserved(endpoint.ServeEndpoints([]string{primary}, nil))).To(BeEmpty())
			Expect(retired.Active()[0].Addresses).To(Equal([]endpoint.Address{{Address: primary, Via: endpoint.ViaListen, RegisteredOn: firstDay}}))
			By("leaving the registry it was given untouched")
			Expect(registry.Retirements).To(BeEmpty())
		})
	})

	Describe("Register", func() {
		It("keeps the first day an address was served and takes how it is served now", func() {
			first := endpoint.Registry{}.Register(endpoint.ServeEndpoints([]string{primary, formerOps}, nil), firstDay)
			again := first.Register(endpoint.ServeEndpoints([]string{primary}, []string{formerOps}), laterDay)
			Expect(again.Endpoints[0].Addresses).To(Equal([]endpoint.Address{
				{Address: primary, Via: endpoint.ViaListen, RegisteredOn: firstDay},
				{Address: formerOps, Via: endpoint.ViaRedirect, RedirectsTo: "14120", RegisteredOn: firstDay},
			}))
			By("never removing an address a later serve leaves out")
			Expect(again.Register(endpoint.ServeEndpoints([]string{primary}, nil), laterDay).Endpoints[0].Addresses).To(HaveLen(2))
			Expect(first.Endpoints[0].Addresses[1].Via).To(Equal(endpoint.ViaListen))
		})
	})

	Describe("Retire", func() {
		registry := endpoint.Registry{}.Register(endpoint.ServeEndpoints([]string{primary}, nil), firstDay)
		complete := endpoint.Retirement{Address: primary, MovedTo: tailnet, RetiredOn: laterDay, Acknowledgement: "fine"}

		It("refuses an address never registered", func() {
			unknown := complete
			unknown.Address = formerOps
			_, err := registry.Retire(unknown)
			Expect(err).To(MatchError(endpoint.ErrNotRegistered))
		})

		It("refuses a retirement without the operator's acknowledgement or the new address", func() {
			_, err := registry.Retire(endpoint.Retirement{Address: primary, MovedTo: tailnet, RetiredOn: laterDay, Acknowledgement: "  "})
			Expect(err).To(MatchError(endpoint.ErrIncompleteRetirement))
			_, err = registry.Retire(endpoint.Retirement{Address: primary, RetiredOn: laterDay, Acknowledgement: "fine"})
			Expect(err).To(MatchError(endpoint.ErrIncompleteRetirement))
		})

		It("refuses retiring one address twice", func() {
			retired, err := registry.Retire(complete)
			Expect(err).NotTo(HaveOccurred())
			_, err = retired.Retire(complete)
			Expect(err).To(MatchError(endpoint.ErrAlreadyRetired))
		})
	})

	Describe("the registry file", func() {
		It("reads as empty when it was never written", func() {
			Expect(endpoint.ReadRegistry(fstest.MapFS{})).To(Equal(endpoint.Registry{}))
		})

		It("round-trips through Encode and ReadRegistry", func() {
			registry := endpoint.Registry{HostPID: 4242}.Register(endpoint.ServeEndpoints([]string{primary}, []string{formerOps}), firstDay)
			content, err := registry.Encode()
			Expect(err).NotTo(HaveOccurred())
			Expect(endpoint.ReadRegistry(fstest.MapFS{endpoint.RegistryFile: {Data: content}})).To(Equal(registry))
		})

		It("reports a file that is not a registry", func() {
			_, err := endpoint.ReadRegistry(fstest.MapFS{endpoint.RegistryFile: {Data: []byte("{")}})
			Expect(err).To(MatchError(ContainSubstring("decode registry.json")))
		})
	})

	Describe("AliasRedirect", func() {
		It("sends every request to the same host name, path and query on the primary port", func() {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "http://192.0.2.7:14121/chat/abc?from=3", nil)
			endpoint.NewAliasRedirect("14120").ServeHTTP(recorder, request)
			Expect(recorder.Code).To(Equal(http.StatusPermanentRedirect))
			Expect(recorder.Header().Get("Location")).To(Equal("http://192.0.2.7:14120/chat/abc?from=3"))
		})

		It("keeps a host name given without a port", func() {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "http://workbench.example/", nil)
			endpoint.NewAliasRedirect("14120").ServeHTTP(recorder, request)
			Expect(recorder.Header().Get("Location")).To(Equal("http://workbench.example:14120/"))
		})
	})
})
