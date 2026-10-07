// Copyright 2026 Candace Labs

package opsview_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/ipc/proc"
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
	// screenshotVariable, when set, names the PNG the widget spec writes of
	// the page with every checkout definition installed: the widget recipe's
	// screenshot.
	screenshotVariable = "OPSVIEW_SCREENSHOT"
	installedCount     = `document.querySelectorAll('[data-opsview-installed="true"]').length`
	screenshotWidth    = 1280
	screenshotHeight   = 1600
)

var paintBudget = eventually.Budget{Within: 30 * time.Second, Interval: 200 * time.Millisecond}

// toolCallsExpression reads one card's tool-call count out of the live DOM.
func toolCallsExpression(assignment string) string {
	selector := fmt.Sprintf(`[data-opsview-assignment=%q]`, assignment)
	return `((document.querySelector(` + livetest.JSString(selector) + `) || {getAttribute: () => ""}).getAttribute("data-opsview-tool-calls") || "")`
}

var _ = Describe("The ops view in a real browser", Label("browser"), func() {
	It("shows a session's card and updates it the moment a line lands, without reloading", func() {
		executable := runtimeconfig.OSEnvironment().String(chromeVariable, "")
		if executable == "" {
			Skip("browser: " + chromeVariable + " is unset — run in dis-gotth-live-bench:latest")
		}

		directory := GinkgoT().TempDir()
		writeSession(directory, firstAssignment, strings.ReplaceAll(startedLine, "%s", firstAssignment), requestedLine, oneToolLine)
		files, err := iofs.NewHostFiles(directory)
		Expect(err).NotTo(HaveOccurred())
		watcher, err := iofs.NewHostWatcher(directory)
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

// serveView binds a listener, builds the view with its address as the one
// accepted Origin, and returns the page's address.
func serveView(state string, options ...opsview.Option) string {
	GinkgoHelper()
	files, err := iofs.NewHostFiles(state)
	Expect(err).NotTo(HaveOccurred())
	watcher, err := iofs.NewHostWatcher(state)
	Expect(err).NotTo(HaveOccurred())
	server := httptest.NewUnstartedServer(nil)
	DeferCleanup(server.Close)
	origin := "http://" + server.Listener.Addr().String()
	view, err := opsview.NewOpsView(files, watcher, []string{origin}, slog.New(slog.DiscardHandler), options...)
	Expect(err).NotTo(HaveOccurred())
	engine := httpserver.NewEngine(browserSpec)
	view.Register(engine)
	server.Config.Handler = engine
	server.Start()
	scope := runtime.NewScope(context.Background(), browserSpec)
	Expect(view.Start(scope)).To(Succeed())
	DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })
	return origin
}

var _ = Describe("Installed widgets in a real browser", Label("browser"), func() {
	It("draws every checkout definition the moment it lands, and removes one the moment it goes, without reloading", func() {
		environment := runtimeconfig.OSEnvironment()
		executable := environment.String(chromeVariable, "")
		if executable == "" {
			Skip("browser: " + chromeVariable + " is unset — run in dis-gotth-live-bench:latest")
		}
		state, definitions := GinkgoT().TempDir(), GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(state, opsview.LoopFile), []byte(loopSnapshot), 0o600)).To(Succeed())
		installedFiles, err := iofs.NewHostFiles(definitions)
		Expect(err).NotTo(HaveOccurred())
		definitionWatcher, err := iofs.NewHostWatcher(definitions)
		Expect(err).NotTo(HaveOccurred())
		origin := serveView(state, opsview.WithWidgetDefinitions(installedFiles, definitionWatcher))

		launcher, err := proc.NewHostLauncher()
		Expect(err).NotTo(HaveOccurred())
		browser := livetest.LaunchBrowser(GinkgoTB(), launcher, livetest.BrowserOptions{Executable: executable, Profile: GinkgoT().TempDir()})
		browser.Navigate(opsview.PageURL(origin))
		browser.EvalJSON(markExpression, nil)
		installed := func() int {
			var count int
			browser.EvalJSON(installedCount, &count)
			return count
		}
		Expect(installed()).To(Equal(0))

		checkout, err := os.ReadDir(checkoutWidgets)
		Expect(err).NotTo(HaveOccurred())
		var names []string
		landed := time.Now()
		for _, entry := range checkout {
			if entry.IsDir() {
				names = append(names, entry.Name())
				writeDefinition(definitions, entry.Name(), definitionFiles(entry.Name()))
			}
		}
		eventually.Await(GinkgoTB(), "every checkout definition drawn on the page", paintBudget,
			installed, func(count int) bool { return count == len(names) })
		visible := time.Since(landed)
		Expect(browser.EvalString(`document.querySelector('[data-opsview-widget="merge-train"] .widget-stats').textContent`)).To(Equal("5 merged12 refused"))

		if path := environment.String(screenshotVariable, ""); path != "" {
			browser.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": screenshotWidth, "height": screenshotHeight, "deviceScaleFactor": 1, "mobile": false}, nil)
			Expect(os.WriteFile(path, browser.Screenshot(), 0o644)).To(Succeed())
		}

		Expect(os.RemoveAll(filepath.Join(definitions, mergeTrain))).To(Succeed())
		eventually.Await(GinkgoTB(), "the removed definition gone from the page", paintBudget,
			installed, func(count int) bool { return count == len(names)-1 })
		Expect(browser.EvalString(markReadBack)).To(Equal(sameDocument), "the page reloaded instead of patching")
		AddReportEntry("installed widgets in a real browser", fmt.Sprintf("browser %s\n%d definitions (%s) visible %s after they landed; one removed, gone; same document",
			browser.Version(), len(names), strings.Join(names, ", "), visible.Round(time.Millisecond)))
	})
})
