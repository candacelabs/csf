// Copyright 2026 Candace Labs

package chat_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/eventually"
)

// The transcript, live: the proof session's turn (testdata/proof-turn.jsonl,
// a reduced turn in Claude Code's recorded shapes) lands in a real session's
// event log one record at a time while a phone watches the page. Each record
// the page shows is timed from landing to the browser's next mutation, and
// with CHAT_SHOT_DIR set the spec writes the page mid-turn on a phone and a
// desktop and one phone frame per record. Run it as mobile_browser_test.go
// says, with -ginkgo.label-filter=transcript.
const (
	proofFile      = "testdata/proof-turn.jsonl"
	desktopWidth   = 1280
	desktopHeight  = 900
	runningToolID  = "toolu_06"
	recordMessage  = "msg"
	recordTime     = "time"
	recordLevel    = "level"
	settleReported = "transcript settle per record, browser clock (ms)"
)

// mutationClock records when a patch changed the page, on the browser's
// clock; the bar's running-turn clock ticks on its own and is not a patch.
const mutationClock = `
window.__mutations = [];
document.addEventListener("DOMContentLoaded", () => {
	const ticking = (node) => (node.nodeType === 1 ? node : node.parentElement)?.closest("[data-chat-since]");
	new MutationObserver((records) => {
		if (records.some((record) => !ticking(record.target))) window.__mutations.push(performance.now());
	}).observe(document.body, {subtree: true, childList: true, characterData: true, attributes: true});
});`

// invisible is whether a record changes nothing the page shows: a gate
// decision the transcript leaves out, or the executor's init event.
func invisible(line string) bool {
	return strings.Contains(line, `"decision":"skip"`) || strings.Contains(line, `"gate":"wait","hook_event":"PreToolUse","decision":"allow"`) ||
		strings.Contains(line, `"subtype":"init"`)
}

// replay lands one fixture record in the session's event log as its writer
// would: every key but the logger's own, the executor event verbatim.
func (harnessSpec *mobileHarness) replay(line string) time.Time {
	GinkgoHelper()
	var fields map[string]json.RawMessage
	Expect(json.Unmarshal([]byte(line), &fields)).To(Succeed())
	var message string
	Expect(json.Unmarshal(fields[recordMessage], &message)).To(Succeed())
	var attributes []any
	for key, raw := range fields {
		if key == recordMessage || key == recordTime || key == recordLevel {
			continue
		}
		// A record's values are whatever its writer chose: numbers, strings,
		// the executor's event.
		var value any = json.RawMessage(raw)
		if key != "event" {
			Expect(json.Unmarshal(raw, &value)).To(Succeed())
		}
		attributes = append(attributes, slog.Any(key, value))
	}
	landed := time.Now()
	harnessSpec.events.Info(message, attributes...)
	return landed
}

func (harnessSpec *mobileHarness) mutations() []float64 {
	var times []float64
	harnessSpec.browser.EvalJSON(`window.__mutations`, &times)
	return times
}

// settled waits for the page to change after landed and returns how long that
// took, by the browser's clock.
func (harnessSpec *mobileHarness) settled(before int, landed time.Time) float64 {
	GinkgoHelper()
	times := eventually.Await(GinkgoTB(), "the page to change", mobileBudget, harnessSpec.mutations, func(times []float64) bool { return len(times) > before })
	var origin float64
	_, err := fmt.Sscan(harnessSpec.browser.EvalString(`String(performance.timeOrigin)`), &origin)
	Expect(err).NotTo(HaveOccurred())
	return origin + times[before] - float64(landed.UnixMicro())/1000
}

var _ = Describe("The transcript, live", Label("browser", "transcript"), func() {
	It("streams the proof turn's rows in as its records land, and measures each record's settle time", func() {
		content, err := os.ReadFile(proofFile)
		Expect(err).NotTo(HaveOccurred())
		lines := strings.Split(strings.TrimSpace(string(content)), "\n")

		harnessSpec := newMobileHarness(browserExecutable())
		harnessSpec.browser.OnNewDocument(mutationClock)
		harnessSpec.browser.Navigate(harnessSpec.pageURL())
		harnessSpec.awaitLive()

		var settles []float64
		for index, line := range lines {
			before := len(harnessSpec.mutations())
			landed := harnessSpec.replay(line)
			if !invisible(line) {
				settles = append(settles, harnessSpec.settled(before, landed))
			}
			harnessSpec.screenshot(fmt.Sprintf("frame-%02d.png", index))
			if strings.Contains(line, `"id":"`+runningToolID+`"`) {
				By("showing the turn mid-tool: a running row, an inline diff, the task list and the status bar")
				Expect(harnessSpec.browser.EvalString(`[...document.querySelectorAll('[data-chat="tool"]')].pop().dataset.chatState`)).To(Equal("running"))
				Expect(harnessSpec.browser.EvalString(`document.querySelector('[data-chat="phase-now"]').textContent`)).To(Equal("running a tool"))
				harnessSpec.browser.EvalJSON(`(() => { for (const row of document.querySelectorAll('li.tool')) { if (row.querySelector('.verb').textContent === "Edit") row.querySelector("details").open = true; } return null; })()`, nil)
				Expect(harnessSpec.browser.EvalBool(`!!document.querySelector('[data-chat="diff"] tr.added')`)).To(BeTrue(), "the edit's diff is inline")
				harnessSpec.screenshot("transcript-phone-midturn.png")
				harnessSpec.browser.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": desktopWidth, "height": desktopHeight, "deviceScaleFactor": 1, "mobile": false}, nil)
				harnessSpec.screenshot("transcript-desktop-midturn.png")
				harnessSpec.browser.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": phoneWidth, "height": phoneHeight, "deviceScaleFactor": phoneScale, "mobile": true}, nil)
			}
		}

		By("ending the turn: every task ticked, every row finished, the bar idle")
		eventually.Await(GinkgoTB(), "the bar to go idle", mobileBudget, func() string {
			return harnessSpec.browser.EvalString(`document.querySelector('[data-chat="phase-now"]').textContent`)
		}, func(phase string) bool { return phase == "idle" })
		Expect(harnessSpec.browser.EvalString(`document.querySelector('[data-chat="tasks"] summary').textContent`)).To(Equal("Tasks · 3 of 3 done"))
		Expect(harnessSpec.browser.EvalString(`[...document.querySelectorAll('[data-chat="tool"]')].map(row => row.dataset.chatState).join(",")`)).To(Equal("done,done,done"))
		Expect(harnessSpec.browser.EvalString(`document.querySelector('[data-chat="turn-cost"]').textContent`)).To(Equal("$0.04"))
		Expect(harnessSpec.browser.EvalBool(`document.documentElement.scrollWidth <= window.innerWidth`)).To(BeTrue(), "the page did not grow sideways")
		harnessSpec.screenshot("transcript-phone-end.png")

		sorted := slices.Clone(settles)
		slices.Sort(sorted)
		AddReportEntry(settleReported, fmt.Sprintf("records %d, median %.1f, p95 %.1f, max %.1f, all %v",
			len(sorted), sorted[len(sorted)/2], sorted[len(sorted)*95/100], sorted[len(sorted)-1], settles))
	})
})
