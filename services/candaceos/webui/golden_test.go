package webui_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/browserroutes"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

// The fixtures under testdata are a historical record: they are the exact
// bytes the operator UI produced before the brand seam existed. They are never
// re-recorded from the current code, because that would make this suite agree
// with whatever the seam happens to render.
//
// goldenSnapshot is deliberately timestamp-free: every rendered time therefore
// resolves to the zero instant, so the recorded pages stay byte-stable no
// matter when the suite runs.
func goldenSnapshot() *candaceosv1.WebUISnapshot {
	return &candaceosv1.WebUISnapshot{
		System: &candaceosv1.WebUISystem{
			Name:           "CandaceOS",
			Status:         "healthy",
			Summary:        "3 nodes · quorum healthy",
			Version:        "0.1.0",
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_OPENCODE,
			HarnessModel:   "opencode/example-model",
			HarnessCapabilities: []candaceosv1.HarnessCapability{
				candaceosv1.HarnessCapability_HARNESS_CAPABILITY_WORKSPACE_WRITE,
				candaceosv1.HarnessCapability_HARNESS_CAPABILITY_ACTIVE_TURN_STEERING,
			},
		},
		Attention: []*candaceosv1.WebUIAttention{{
			Id:     "approval-deploy-1",
			Title:  "Deploy Garden Notes to core-1?",
			Detail: "This writes a new Compose service and publishes it.",
			Risk:   "writes production",
		}},
		Run: &candaceosv1.WebUIRun{
			Id:        "run-1",
			SessionId: "session-1",
			Title:     "Build a private garden notes app",
			Status:    "running",
			CanAbort:  true,
			Entries: []*candaceosv1.WebUIRunEntry{
				{Id: "entry-1", Kind: "message", Role: "user", Text: "Build me a garden notes app"},
				{Id: "entry-2", Kind: "tool", Name: "shell", Text: "Running focused tests", Detail: "go test ./services/garden/...", Status: "done"},
				{Id: "entry-3", Kind: "notice", Text: "Context was compacted", Status: "compacted"},
				{Id: "entry-4", Kind: "error", Text: "Worker unavailable", Status: "failed"},
				{Id: "entry-5", Kind: "message", Role: "assistant", Text: "Placing the app on core-1"},
			},
		},
		Fleet: &candaceosv1.WebUIFleet{
			LeaderId: "core-1",
			Term:     7,
			Quorum:   &candaceosv1.WebUIQuorum{Healthy: true, Online: 3, Required: 2},
			Nodes: []*candaceosv1.WebUINode{{
				Id:            "core-1",
				Name:          "Core One",
				Role:          "worker",
				Labels:        map[string]string{"role": "worker"},
				Status:        "healthy",
				Address:       "192.0.2.10",
				Apps:          2,
				CpuPercent:    22,
				MemoryPercent: 61,
			}},
		},
		Apps: []*candaceosv1.WebUIApp{{
			Id:       "garden-notes",
			Name:     "Garden Notes",
			Status:   "running",
			NodeId:   "core-1",
			Url:      "/apps/garden-notes",
			Revision: "a1b2c3d",
		}, {
			Id:     "unsummarized",
			Name:   "Unsummarized",
			Status: "pending",
		}},
		Activity: []*candaceosv1.WebUIActivity{{
			Id:        "activity-1",
			Kind:      "deploy",
			Title:     "Garden Notes deployed",
			Detail:    "Health check passed on core-1.",
			Status:    "succeeded",
			ReceiptId: "rcpt_01K2",
		}},
	}
}

func goldenPath(name string) string {
	return filepath.Join("testdata", name)
}

func readGolden(name string) string {
	GinkgoHelper()
	contents, err := os.ReadFile(goldenPath(name))
	Expect(err).NotTo(HaveOccurred())
	return string(contents)
}

// brandSeamDeltas applies the only differences the brand seam is allowed to
// introduce into the stock pages. The recorded fixtures are the bytes the UI
// produced before the seam existed; anything the seam changes beyond this
// transformation is a rebranding regression, not a refactor.
func brandSeamDeltas(recorded string) string {
	GinkgoHelper()
	// 1. The palette is delivered as a served same-origin stylesheet, linked
	//    after app.css so its :root declarations win.
	appCSSLink := fmt.Sprintf(`  <link rel="stylesheet" href=%q>`, browserroutes.AssetPath("app.css"))
	brandLink := fmt.Sprintf(`  <link rel="stylesheet" href=%q>`, browserroutes.BrandStylesheetPath())
	Expect(recorded).To(ContainSubstring(appCSSLink))
	linked := strings.Replace(recorded, appCSSLink+"\n", appCSSLink+"\n"+brandLink+"\n", 1)

	// 2. The inert first-paint snapshot carries the agent name the browser
	//    client now reads instead of hard-coding.
	system := `"system":{`
	agent := fmt.Sprintf(`"agent_name":%q,`, webui.DefaultAgentName)
	Expect(linked).To(ContainSubstring(system))
	named := strings.Replace(linked, system, system+agent, 1)

	// 3. The wordmark is now one fragment, so the mark and the lettering that
	//    the recorded pages carried on two lines are emitted adjacently. Both
	//    brand links are inline-flex containers, where whitespace between items
	//    produces no box, so the rendered result is unchanged.
	joined := wordmarkLineBreak.ReplaceAllString(named, "$1")
	Expect(joined).NotTo(Equal(named), "the recorded page must contain the stock wordmark")
	return joined
}

// wordmarkLineBreak matches only the insignificant whitespace between the two
// halves of the stock wordmark.
var wordmarkLineBreak = regexp.MustCompile(
	`\n[\t ]+(<span>Candace<span class="brand-os">OS</span></span>)`,
)

var _ = Describe("default brand rendering", func() {
	It("matches the recorded default pages", func() {
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return goldenSnapshot(), nil
		}))
		defer server.Close()

		_, index := get(server, browserroutes.Index)
		_, chat := get(server, browserroutes.ClawChatPath("session-1"))

		Expect(index).To(Equal(brandSeamDeltas(readGolden("index_default.html"))))
		Expect(chat).To(Equal(brandSeamDeltas(readGolden("chat_default.html"))))
	})
})
