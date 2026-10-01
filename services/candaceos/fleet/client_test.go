package fleet_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/candaceos/fleet"
)

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CandaceOS Fleet Suite")
}

var _ = Describe("Warden client", func() {
	It("derives a quorum-gated, sorted fleet snapshot", func(ctx SpecContext) {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			Expect(request.URL.Path).To(Equal("/api/status"))
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{
              "view": {
                "self":"n2", "role":"follower", "term":9, "leader_id":"n1",
                "source":"n1", "authoritative":true, "updated_at":"2026-08-17T00:00:00Z",
                "elections_started":0,
                "membership":{"version":1,"created_in_term":9,"voters":[
                  {"id":"n1","addr":"10.0.0.1:7717"},
                  {"id":"n2","addr":"10.0.0.2:7717"},
                  {"id":"n3","addr":"10.0.0.3:7717"}]},
                "peers":[
                  {"node":{"id":"n2","addr":"10.0.0.2:7717"},"status":"alive","last_seen":"2026-08-17T00:00:00Z","latency_ms":1,"member":"voter"},
                  {"node":{"id":"n1","addr":"10.0.0.1:7717"},"status":"alive","last_seen":"2026-08-17T00:00:00Z","latency_ms":1,"member":"voter"},
                  {"node":{"id":"n3","addr":"10.0.0.3:7717"},"status":"dead","last_seen":"2026-08-16T00:00:00Z","latency_ms":0,"member":"voter"}]
              }, "incidents": []
            }`))
		}))
		DeferCleanup(server.Close)

		client, err := fleet.NewWardenClient(server.URL, server.Client())
		Expect(err).NotTo(HaveOccurred())
		snapshot, err := client.Refresh(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.CanMutate()).To(BeTrue())
		Expect(snapshot.Online).To(Equal(2))
		Expect(snapshot.Required).To(Equal(2))
		Expect(snapshot.Nodes).To(HaveLen(3))
		Expect(snapshot.Nodes[0].ID).To(Equal("n1"))
		Expect(snapshot.Nodes[0].Role).To(Equal("leader"))
	})

	It("fails closed while preserving the last useful node list", func(ctx SpecContext) {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			http.Error(response, "down", http.StatusServiceUnavailable)
		}))
		DeferCleanup(server.Close)
		client, err := fleet.NewWardenClient(server.URL, server.Client())
		Expect(err).NotTo(HaveOccurred())
		snapshot, err := client.Refresh(ctx)
		Expect(err).To(HaveOccurred())
		Expect(snapshot.CanMutate()).To(BeFalse())
		Expect(snapshot.Error).To(ContainSubstring("503"))
	})

	It("keeps polling while a slow observer receives coalesced snapshots", func(ctx SpecContext) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			term := requests.Add(1)
			response.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(response, `{
              "view": {
                "self":"n1", "role":"leader", "term":%d, "leader_id":"n1",
                "authoritative":true, "updated_at":"2026-08-17T00:00:00Z",
                "membership":{"version":1,"created_in_term":1,"voters":[{"id":"n1","addr":"10.0.0.1:7717"}]},
                "peers":[{"node":{"id":"n1","addr":"10.0.0.1:7717"},"status":"alive","last_seen":"2026-08-17T00:00:00Z","member":"voter"}]
              }
            }`, term)
		}))
		DeferCleanup(server.Close)
		client, err := fleet.NewWardenClient(server.URL, server.Client())
		Expect(err).NotTo(HaveOccurred())

		runContext, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)
		release := make(chan struct{})
		observed := make(chan fleet.Snapshot, 2)
		var blockedFirst atomic.Bool
		runDone := make(chan struct{})
		go func() {
			defer close(runDone)
			client.Run(runContext, 10*time.Millisecond, func(snapshot fleet.Snapshot) {
				select {
				case observed <- snapshot:
				default:
				}
				if blockedFirst.CompareAndSwap(false, true) {
					select {
					case <-release:
					case <-runContext.Done():
					}
				}
			})
		}()

		var first fleet.Snapshot
		Eventually(observed).Should(Receive(&first))
		Expect(first.Term).To(Equal(uint64(1)))
		Eventually(requests.Load).Should(BeNumerically(">=", 4),
			"polling must not wait for the durable observer")
		close(release)
		var latest fleet.Snapshot
		Eventually(observed).Should(Receive(&latest))
		Expect(latest.Term).To(BeNumerically(">", first.Term),
			"the single pending callback must retain the latest observation")
		cancel()
		Eventually(runDone).Should(BeClosed(), "Run must join its callback worker before returning")
	})

	It("keeps configured deployment roles separate from dynamic leadership", func() {
		observed := fleet.Snapshot{
			LeaderID: "control", Term: 14, Authoritative: true, HasQuorum: true,
			Nodes: []fleet.Node{
				{ID: "control", Name: "control", Role: "leader", Status: "alive"},
				{ID: "worker", Name: "worker", Role: "worker", Status: "alive"},
			},
		}
		labels := map[string]map[string]string{
			"control": {"role": "control"},
			"worker":  {"role": "worker", "gpu": "nvidia"},
		}

		configured := fleet.WithConfiguration(observed, labels)

		Expect(configured.LeaderID).To(Equal("control"))
		Expect(configured.Nodes[0].Role).To(Equal("control"))
		Expect(configured.Nodes[1].Role).To(Equal("worker"))
		Expect(configured.Nodes[1].Labels).To(Equal(map[string]string{"role": "worker", "gpu": "nvidia"}))
		labels["worker"]["gpu"] = "changed"
		Expect(configured.Nodes[1].Labels["gpu"]).To(Equal("nvidia"), "status must own a stable label snapshot")
		Expect(observed.Nodes[0].Role).To(Equal("leader"), "Warden observation must remain unchanged")
	})
})
