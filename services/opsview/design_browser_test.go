// Copyright 2026 Candace Labs

package opsview_test

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
	runtimeconfig "github.com/candacelabs/csf/runtime/config"
	"github.com/candacelabs/csf/services/opsview"
)

// The Workbench's view state and motion in a real browser on a phone: a
// viewer opens a card and filters the idle list, a line lands in the open
// session's log, and the page patches without losing either choice; a reload
// in the same browser session brings both back from the server. Then motion:
// a board patch that reorders rows runs inside a view transition, and does
// not when the viewer asks for reduced motion, which also stops every
// animation. Everything is real but the sessions, which are run directories
// the spec writes. Run it with:
//
//	docker run --rm -v "$PWD:/workspace" -w /workspace dis-gotth-live-bench:latest \
//	    go test ./services/opsview/ -run TestOpsView -v -args -ginkgo.label-filter=browser -ginkgo.v
const (
	// transitionCounter wraps the browser's view transition entry point, so
	// the spec can count the transitions the runtime asked for.
	transitionCounter = `
window.__transitions = 0;
if (document.startViewTransition) {
	const start = document.startViewTransition.bind(document);
	document.startViewTransition = (update) => { window.__transitions++; return start(update); };
}`
	transitions      = `window.__transitions`
	runningAnimation = `getComputedStyle(document.querySelector("article.row.s-running"), "::after").animationName`
	thirdAssignment  = "9a1e6c2d-3b4f-4e5a-8c7d-6e5f4a3b2c1d"
)

func designRow(assignment string) string {
	return `document.querySelector('[data-opsview-assignment="` + assignment + `"]')`
}

func emulatePhone(browser *livetest.Browser, motion string) {
	browser.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": phoneWidth, "height": phoneHeight, "deviceScaleFactor": phoneScale, "mobile": true}, nil)
	browser.Call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-reduced-motion", "value": motion}}}, nil)
}

var _ = Describe("The Workbench's view state and motion in a real browser", Label("browser"), func() {
	var browser *livetest.Browser

	BeforeEach(func() {
		executable := runtimeconfig.OSEnvironment().String(chromeVariable, "")
		if executable == "" {
			Skip("browser: " + chromeVariable + " is unset — run in dis-gotth-live-bench:latest")
		}
		launcher, err := proc.NewHostLauncher()
		Expect(err).NotTo(HaveOccurred())
		browser = livetest.LaunchBrowser(GinkgoTB(), launcher, livetest.BrowserOptions{Executable: executable, Profile: GinkgoT().TempDir()})
		browser.OnNewDocument(transitionCounter)
	})

	await := func(what string, expression string) {
		GinkgoHelper()
		eventually.Await(GinkgoTB(), what, paintBudget, func() bool { return browser.EvalBool("Boolean(" + expression + ")") }, func(held bool) bool { return held })
	}

	It("keeps an open card and the idle list's filter through a live patch, and brings both back on reload", func() {
		directory := GinkgoT().TempDir()
		writeSession(directory, firstAssignment, strings.ReplaceAll(startedLine, "%s", firstAssignment), requestedLine, oneToolLine)
		writeSession(directory, secondAssignment, strings.ReplaceAll(startedLine, "%s", secondAssignment), requestedLine, closedLine)
		origin := serveView(directory)
		emulatePhone(browser, "no-preference")
		browser.Navigate(opsview.PageURL(origin))
		browser.EvalJSON(markExpression, nil)
		running := designRow(firstAssignment)
		await("the running row", running)

		browser.EvalJSON(running+`.querySelector('[data-opsview="expand"]').click(), null`, nil)
		await("the card open", running+`.querySelector('[data-opsview="expand"]').getAttribute("aria-expanded") === "true"`)
		browser.EvalJSON(`document.querySelector('[data-opsview="others"]').click(), null`, nil)
		await("the idle list open", `document.querySelector('[data-opsview="filter"]')`)
		await("the closed session listed", `document.querySelector('[data-opsview="matching"]').textContent === "1 of 1"`)
		browser.EvalJSON(`(() => { const input = document.querySelector('[data-opsview="filter"]'); input.value = "no such session"; input.dispatchEvent(new Event("input", {bubbles: true})); })(), null`, nil)
		await("the filter applied", `document.querySelector('[data-opsview="matching"]').textContent === "0 of 1"`)

		appendLine(directory, firstAssignment, twoToolsLine)
		await("the live patch", running+`.getAttribute("data-opsview-tool-calls") === "3"`)
		Expect(browser.EvalString(running + `.querySelector('[data-opsview="activity"]').textContent`)).To(Equal("Searching for “term harness”"))
		Expect(browser.EvalString(running+`.querySelector('[data-opsview="expand"]').getAttribute("aria-expanded")`)).To(Equal("true"), "the open card survived the patch")
		Expect(browser.EvalString(`document.querySelector('[data-opsview="filter"]').value`)).To(Equal("no such session"), "the typed filter survived the patch")
		Expect(browser.EvalString(markReadBack)).To(Equal(sameDocument), "the page reloaded instead of patching")

		browser.Navigate(opsview.PageURL(origin))
		await("the card open again after a reload", running+` && `+running+`.querySelector('[data-opsview="expand"]').getAttribute("aria-expanded") === "true"`)
		Expect(browser.EvalString(`document.querySelector('[data-opsview="filter"]').value`)).To(Equal("no such session"), "the server remembered the filter")
		Expect(browser.EvalString(`document.querySelector('[data-opsview="matching"]').textContent`)).To(Equal("0 of 1"))
		AddReportEntry("view state in a real browser", fmt.Sprintf("browser %s\nopen card and filter kept through a live patch and a reload", browser.Version()))
	})

	It("reorders rows inside a view transition, and with reduced motion neither transitions nor animates", func() {
		directory := GinkgoT().TempDir()
		writeSession(directory, firstAssignment, strings.ReplaceAll(startedLine, "%s", firstAssignment), requestedLine, oneToolLine)
		origin := serveView(directory)

		emulatePhone(browser, "reduce")
		browser.Navigate(opsview.PageURL(origin))
		await("the running row", designRow(firstAssignment))
		Expect(browser.EvalString(runningAnimation)).To(Equal("none"), "reduced motion stops the running row's sweep")
		writeSession(directory, secondAssignment, strings.ReplaceAll(startedLine, "%s", secondAssignment), requestedLine)
		await("a second row added to the board", designRow(secondAssignment))
		var reduced int
		browser.EvalJSON(transitions, &reduced)
		Expect(reduced).To(Equal(0), "no view transition under reduced motion")

		emulatePhone(browser, "no-preference")
		browser.Navigate(opsview.PageURL(origin))
		await("both rows", designRow(secondAssignment))
		Expect(browser.EvalString(runningAnimation)).To(Equal("sweep"), "a running row says it is alive")
		writeSession(directory, thirdAssignment, strings.ReplaceAll(startedLine, "%s", thirdAssignment), requestedLine)
		await("a third row added to the board", designRow(thirdAssignment))
		var moved int
		browser.EvalJSON(transitions, &moved)
		Expect(moved).To(BeNumerically(">=", 1), "the board patch that reordered the rows ran inside a view transition")
		AddReportEntry("motion in a real browser", fmt.Sprintf("browser %s\nreduced motion: 0 view transitions, running sweep %q\nno preference: %d view transitions on a reorder", browser.Version(), "none", moved))
	})
})
