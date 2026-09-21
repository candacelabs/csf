package csf

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/pkg/patience"
	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var boardConnectionBudget = patience.Budget{Within: 10 * time.Second}

var _ = Describe("Live-board connection inspection", func() {
	It("projects the actual connection registry into HTML and the existing metrics route", func() {
		path := filepath.Join(GinkgoT().TempDir(), "work.json")
		Expect(os.WriteFile(path, []byte(`{}`), 0600)).To(Succeed())
		board, err := NewFarmDashboard([]string{"https://board.example"}, WithFarmWorkPath(path))
		Expect(err).NotTo(HaveOccurred())
		inspection := NewInspection(WithInspectionBrowserConnections(board.ActiveConnections))
		router := httpserver.NewEngine("connection-inspection")
		board.Register(router)
		inspection.Register(router)
		server := httptest.NewServer(router)
		DeferCleanup(server.Close)
		ctx, cancel := context.WithTimeout(context.Background(), boardConnectionBudget.Within)
		DeferCleanup(cancel)
		DeferCleanup(func() { Expect(board.Close(ctx)).To(Succeed()) })
		Expect(inspectionGauges(scrapeInspection(inspection), browserConnectionsMetric, "")).To(Equal(map[string]float64{"": 0}))
		for range 2 {
			connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+farmMount,
				&websocket.DialOptions{Subprotocols: []string{"gotth-live.v1"}, HTTPHeader: map[string][]string{"Origin": {"https://board.example"}}})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = connection.CloseNow() })
			_, _, err = connection.Read(ctx)
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(inspectionGauges(scrapeInspection(inspection), browserConnectionsMetric, "")).To(Equal(map[string]float64{"": 2}))
		board.publish(time.Now())
		markup, err := board.RenderFarm()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(markup)).To(ContainSubstring(`<span>LIVE-BOARD CONNECTIONS</span><b>2</b>`))
		Expect(board.Close(ctx)).To(Succeed())
		patience.Await(GinkgoT(), "board connection cleanup", boardConnectionBudget,
			board.ActiveConnections, func(count int) bool { return count == 0 })
		Expect(inspectionGauges(scrapeInspection(inspection), browserConnectionsMetric, "")).To(Equal(map[string]float64{"": 0}))
	})

	It("omits the connection series when no board is mounted", func() {
		Expect(scrapeInspection(NewInspection())).NotTo(HaveKey(inspectionPrefix + browserConnectionsMetric))
	})
})

var _ = Describe("Shared live workshop", func() {
	It("counts only passed checklist items with retained evidence", func() {
		path := filepath.Join(GinkgoT().TempDir(), "work.json")
		content, err := json.Marshal(FarmView{Checkpoint: FarmCheckpoint{Title: "Handoff", Items: []FarmCheck{
			{ID: "a", Status: "passed", Evidence: "receipt-a"},
			{ID: "b", Status: "passed"},
			{ID: "c", Status: "pending", Evidence: "receipt-c"},
		}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, content, 0600)).To(Succeed())
		board, err := NewFarmDashboard([]string{"http://localhost:14111"}, WithFarmWorkPath(path))
		Expect(err).NotTo(HaveOccurred())
		checkpoint := board.Snapshot().Checkpoint
		Expect(checkpoint.Total).To(Equal(3))
		Expect(checkpoint.Done).To(Equal(1))
		Expect(checkpoint.Percent).To(Equal(33))
	})

	It("renders current measurements and marks old agent reports unknown", func() {
		path := filepath.Join(GinkgoT().TempDir(), "work.json")
		content, err := json.Marshal(FarmView{Title: "<script>unsafe</script>", Containers: 9,
			DashboardLinks: []FarmLink{{Name: "Consumer Grafana", URL: "https://metrics.example.invalid/d/runtime"}, {Name: "Unsafe link", URL: "javascript:alert(1)"}},
			Agents:         []FarmAgent{{Name: "tester", State: "running", ObservedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, content, 0600)).To(Succeed())
		board, err := NewFarmDashboard([]string{"http://localhost:14111"}, WithFarmWorkPath(path))
		Expect(err).NotTo(HaveOccurred())
		output, err := board.RenderFarm()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(output)).To(ContainSubstring("&lt;script&gt;unsafe&lt;/script&gt;"))
		Expect(string(output)).To(ContainSubstring("data-gotth-region=\"farm\""))
		Expect(string(output)).To(ContainSubstring(`href="https://metrics.example.invalid/d/runtime">Consumer Grafana`))
		Expect(string(output)).To(ContainSubstring(`href="/metrics"`))
		Expect(string(output)).NotTo(ContainSubstring("javascript:"))
		Expect(board.view.Load().Agents[0].State).To(Equal(farmUnknownState))
		Expect(board.view.Load().RuntimeCount).To(Equal(1))
	})
	It("retains task order across reconstruction and refuses unknown task identities", func() {
		path := filepath.Join(GinkgoT().TempDir(), "work.json")
		content, err := json.Marshal(FarmView{Title: "Order", Tasks: []FarmTask{{ID: "one"}, {ID: "two"}, {ID: "three"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, content, 0600)).To(Succeed())
		board, err := NewFarmDashboard([]string{"http://localhost:14111"}, WithFarmWorkPath(path))
		Expect(err).NotTo(HaveOccurred())
		Expect(board.move("three", "one")).To(Succeed())
		Expect(board.move("missing", "one")).NotTo(Succeed())
		reloaded, err := NewFarmDashboard([]string{"http://localhost:14111"}, WithFarmWorkPath(path))
		Expect(err).NotTo(HaveOccurred())
		Expect(reloaded.view.Load().Tasks).To(Equal([]FarmTask{{ID: "three"}, {ID: "one"}, {ID: "two"}}))
		Expect(filepath.Join(filepath.Dir(path), farmOrderReceiptFile)).To(BeAnExistingFile())
	})
})

var _ = Describe("live Workbench observations", func() {
	It("replaces stale duplicate reports with live sessions and exposes observation failures", func() {
		path := filepath.Join(GinkgoT().TempDir(), "work.json")
		content, err := json.Marshal(FarmView{Agents: []FarmAgent{{Name: "stale", SessionID: "same"}, {Name: "other"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, content, 0600)).To(Succeed())
		unavailable := false
		board, err := NewFarmDashboard([]string{"http://localhost:14111"}, WithFarmWorkPath(path), WithFarmAgentSource(func(now time.Time) ([]FarmAgent, error) {
			if unavailable {
				return nil, errors.New("database offline")
			}
			return []FarmAgent{{Name: "Current worker", SessionID: "same", State: "idle", ObservedAt: now.UTC().Format(time.RFC3339)}}, nil
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(board.Snapshot().Agents).To(HaveLen(2))
		Expect(board.Snapshot().Agents[0].Name).To(Equal("Current worker"))
		Expect(board.Snapshot().Agents[0].State).To(Equal("idle"))
		Expect(board.Snapshot().Agents[0].Tokens).To(BeNil())
		unavailable = true
		board.publish(time.Now())
		Expect(board.Snapshot().AgentSourceError).NotTo(BeEmpty())
		Expect(board.Snapshot().SourceError).To(BeEmpty())
	})
})
