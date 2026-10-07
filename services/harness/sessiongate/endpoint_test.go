// Copyright 2026 Candace Labs

package sessiongate_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/endpoint"
	"github.com/candacelabs/csf/services/harness/sessiongate"
)

const (
	servedAddress  = "127.0.0.1:14120"
	formerAddress  = "127.0.0.1:14121"
	hostPID        = 4242
	dashboard      = "csf-grafana"
	dashboardName  = "Grafana"
	dashboardUsers = "the operator, for measured history"
)

// servingRegistry is csf serve on two addresses, the second a redirect,
// plus an endpoint a container serves.
func servingRegistry() endpoint.Registry {
	registry := endpoint.Registry{HostPID: hostPID}.Register(endpoint.ServeEndpoints([]string{servedAddress}, []string{formerAddress}), "2026-10-05")
	return registry.Register([]endpoint.Endpoint{{Name: dashboardName, Path: "/", Owner: endpoint.OwnerServe, Container: dashboard, Users: dashboardUsers,
		Addresses: []endpoint.Address{{Address: "127.0.0.1:3000", Via: endpoint.ViaListen}}}}, "2026-10-05")
}

// snippets is each finding's command text, in order.
func snippets(findings []sessiongate.EndpointFinding) []string {
	found := []string{}
	for _, finding := range findings {
		found = append(found, finding.Snippet)
	}
	return found
}

var _ = Describe("FindEndpointStops", func() {
	DescribeTable("refuses a command that stops serving a registered endpoint",
		func(command string, expected ...string) {
			findings, err := sessiongate.FindEndpointStops(command, servingRegistry())
			Expect(err).NotTo(HaveOccurred())
			Expect(snippets(findings)).To(Equal(expected))
		},
		Entry("csf stop", `csf stop`, `csf stop`),
		Entry("csf stop by path, with its endpoint", `/usr/local/bin/csf stop -endpoint http://127.0.0.1:14120`, `/usr/local/bin/csf stop -endpoint http://127.0.0.1:14120`),
		Entry("the incident's csf view -stop", `csf view -stop`, `csf view -stop`),
		Entry("csf view --stop=true inside sh -c", `sh -c 'csf view --stop=true'`, `csf view --stop=true`),
		Entry("the deprecated name", `harness stop`, `harness stop`),
		Entry("kill of the registered host", `kill -TERM 4242`, `kill -TERM 4242`),
		Entry("pkill naming csf", `pkill -x csf`, `pkill -x csf`),
		Entry("killall naming csf serve", `killall csf`, `killall csf`),
		Entry("docker stop of an endpoint's container", `docker stop csf-grafana`, `docker stop csf-grafana`),
		Entry("docker container rm -f of it", `docker container rm -f csf-grafana`, `docker container rm -f csf-grafana`),
		Entry("the operator's retire verb", `csf endpoint retire -address 127.0.0.1:14121 -moved-to 127.0.0.1:14120 -acknowledgement ok`,
			`csf endpoint retire -address 127.0.0.1:14121 -moved-to 127.0.0.1:14120 -acknowledgement ok`),
	)

	DescribeTable("allows a command that keeps every registered endpoint served",
		func(command string) {
			findings, err := sessiongate.FindEndpointStops(command, servingRegistry())
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(BeEmpty())
		},
		Entry("a restart in one command", `csf stop && csf serve -listen 127.0.0.1:14120 -redirect 127.0.0.1:14121 -detach`),
		Entry("a restart inside sh -c", `bash -c "csf stop; csf serve -detach"`),
		Entry("csf view without -stop", `csf view`),
		Entry("the other csf verbs", `csf list && csf send -assignment a -message stop`),
		Entry("kill of another process", `kill 4243`),
		Entry("pkill of something else", `pkill -x chromium`),
		Entry("docker stop of a container no endpoint names", `docker stop csf-view-14121-forward`),
		Entry("docker ps naming the container", `docker ps --filter name=csf-grafana`),
		Entry("quoted text that mentions csf stop", `echo "csf stop"`),
	)

	It("names each endpoint the stop would end, at every address, with its users", func() {
		findings, err := sessiongate.FindEndpointStops(`csf stop`, servingRegistry())
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(HaveLen(1))
		Expect(findings[0].Message()).To(And(
			ContainSubstring("Workbench at http://127.0.0.1:14120/ and http://127.0.0.1:14121/ (used by the operator, in a browser)"),
			ContainSubstring("MCP at http://127.0.0.1:14120/mcp and http://127.0.0.1:14121/mcp (used by agent sessions and the orchestrator"),
			ContainSubstring("Restart in the same command"),
			ContainSubstring("csf endpoint retire -address")))
	})

	It("protects nothing the operator retired", func() {
		registry, err := endpoint.Registry{}.Register(endpoint.ServeEndpoints([]string{servedAddress}, nil), "2026-10-05").
			Retire(endpoint.Retirement{Address: servedAddress, MovedTo: formerAddress, RetiredOn: "2026-10-06", Acknowledgement: "move it"})
		Expect(err).NotTo(HaveOccurred())
		Expect(sessiongate.FindEndpointStops(`csf stop`, registry)).To(BeEmpty())
	})

	It("protects nothing with an empty registry, and recognises no kill without a recorded host", func() {
		Expect(sessiongate.FindEndpointStops(`csf stop; kill 0`, endpoint.Registry{})).To(BeEmpty())
	})

	It("reports a command that is not shell", func() {
		_, err := sessiongate.FindEndpointStops(`csf stop (`, servingRegistry())
		Expect(err).To(MatchError(sessiongate.ErrUnparsedCommand))
	})
})
