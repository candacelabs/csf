// Copyright 2026 Candace Labs

package opsview_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ipcfs "github.com/candacelabs/csf/ipc/fs"
	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/runtime"
	runtimeconfig "github.com/candacelabs/csf/runtime/config"
	"github.com/candacelabs/csf/services/opsview"
)

// The browser conformance spec: the one claim only a browser can answer,
// that a card on the page changes when a line lands in a session's event
// log, with no page refresh. Everything here is real — the kernel's
// notification queue, a bound listener, headless Chromium through the
// process capability — so it carries the browser label and skips where the
// bench image's CHROME_BIN is unset. Run it with:
//
//	docker run --rm -v "$PWD:/workspace" -w /workspace dis-gotth-live-bench:latest \
//	    go test ./services/opsview/ -run TestOpsView -v -args -ginkgo.label-filter=browser -ginkgo.v
const (
	chromeVariable = "CHROME_BIN"
	markExpression = `document.body.__opsviewDocument = "same-document", null`
	markReadBack   = `document.body.__opsviewDocument || ""`
	sameDocument   = "same-document"
	browserSpec    = "opsview-browser"
)

var paintBudget = eventually.Budget{Within: 30 * time.Second, Interval: 200 * time.Millisecond}

// toolCallsExpression reads one card's tool-call count out of the live DOM.
func toolCallsExpression(assignment string) string {
	selector := fmt.Sprintf(`[data-opsview-assignment=%q] [data-opsview="tool-calls"]`, assignment)
	return `(document.querySelector(` + livetest.JSString(selector) + `) || {textContent: ""}).textContent.trim()`
}

var _ = Describe("The ops view in a real browser", Label("browser"), func() {
	It("shows a session's card and updates it the moment a line lands, without reloading", func() {
		executable := runtimeconfig.OSEnvironment().String(chromeVariable, "")
		if executable == "" {
			Skip("browser: " + chromeVariable + " is unset — run in dis-gotth-live-bench:latest")
		}

		directory := GinkgoT().TempDir()
		writeSession(directory, firstAssignment, strings.ReplaceAll(startedLine, "%s", firstAssignment), requestedLine, oneToolLine)
		files, err := ipcfs.NewHostFiles(directory)
		Expect(err).NotTo(HaveOccurred())
		watcher, err := ipcfs.NewHostWatcher(directory)
		Expect(err).NotTo(HaveOccurred())

		// The browser sends the server's own address as its Origin, and the
		// allowlist is fixed at construction, so the listener is bound first
		// and the view is built with its address.
		server := httptest.NewUnstartedServer(nil)
		DeferCleanup(server.Close)
		origin := "http://" + server.Listener.Addr().String()
		view, err := opsview.NewOpsView(files, watcher, []string{origin}, slog.New(slog.DiscardHandler))
		Expect(err).NotTo(HaveOccurred())
		engine := httpserver.NewEngine(browserSpec)
		view.Register(engine)
		server.Config.Handler = engine
		server.Start()
		scope := runtime.NewScope(context.Background(), browserSpec)
		Expect(view.Start(scope)).To(Succeed())
		DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })

		launcher, err := proc.NewHostLauncher()
		Expect(err).NotTo(HaveOccurred())
		browser := livetest.LaunchBrowser(GinkgoTB(), launcher, livetest.BrowserOptions{Executable: executable, Profile: GinkgoT().TempDir()})
		browser.Navigate(opsview.PageURL(origin))
		browser.EvalJSON(markExpression, nil)

		reads := func() string { return browser.EvalString(toolCallsExpression(firstAssignment)) }
		eventually.Await(GinkgoTB(), "the card's tool-call count after the first read of the log", paintBudget,
			reads, func(count string) bool { return count == "1" })

		appendLine(directory, firstAssignment, twoToolsLine)
		eventually.Await(GinkgoTB(), "the card's tool-call count after a line landed", paintBudget,
			reads, func(count string) bool { return count == "3" })

		Expect(browser.EvalString(markReadBack)).To(Equal(sameDocument), "the page reloaded instead of patching")
		AddReportEntry("ops view in a real browser", fmt.Sprintf("browser %s\ntool calls 1 → 3 on one appended line, same document", browser.Version()))
	})
})
