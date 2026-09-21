package operator

import (
	"context"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/fleet"
)

var _ = Describe("fleet status tool projection", func() {
	It("exposes placement labels while keeping Warden leadership dynamic", func() {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{
              "view": {
                "self":"control", "role":"follower", "term":21,
                "leader_id":"worker-a", "authoritative":true,
                "updated_at":"2026-08-19T18:00:00Z",
                "membership":{"version":1,"created_in_term":21,"voters":[
                  {"id":"control","addr":"10.0.0.1:7717"},
                  {"id":"worker-a","addr":"10.0.0.2:7717"}]},
                "peers":[
                  {"node":{"id":"control","addr":"10.0.0.1:7717"},"status":"alive","last_seen":"2026-08-19T18:00:00Z","member":"voter"},
                  {"node":{"id":"worker-a","addr":"10.0.0.2:7717"},"status":"alive","last_seen":"2026-08-19T18:00:00Z","member":"voter"}]
              }
            }`))
		}))
		DeferCleanup(server.Close)
		client, err := fleet.NewWardenClient(server.URL, server.Client())
		Expect(err).NotTo(HaveOccurred())
		_, err = client.Refresh(context.Background())
		Expect(err).NotTo(HaveOccurred())
		controller := newTestController(&candaceosv1.CoreConfig{NodeLabels: []*candaceosv1.Node{
			{Id: "control", Labels: map[string]string{"role": "control"}},
			{Id: "worker-a", Labels: map[string]string{"role": "worker", "gpu": "nvidia"}},
		}}, client, nil)

		status, err := controller.configuredFleetStatus()

		Expect(err).NotTo(HaveOccurred())
		Expect(status.LeaderID).To(Equal("worker-a"))
		Expect(status.Nodes).To(HaveLen(2))
		Expect(status.Nodes[0].Role).To(Equal("control"))
		Expect(status.Nodes[1].Role).To(Equal("worker"), "being elected must not rewrite deployment role")
		Expect(status.Nodes[1].Labels).To(HaveKeyWithValue("gpu", "nvidia"))
	})

	It("fails closed when fleet status is unavailable", func() {
		controller := newTestController(&candaceosv1.CoreConfig{}, nil, nil)

		_, err := controller.configuredFleetStatus()

		Expect(err).To(MatchError("fleet status is unavailable"))
	})
})
