// Copyright 2026 Candace Labs

package opsview_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"

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

// The host panel on a phone: apply "only CSF work", read its diff, confirm,
// see the containers stop; undo it in one tap and see them start, while the
// container the operator protected is never touched. Everything is real but
// the Docker Engine: the host operations and the Workbench page over a state
// directory with one run directory, a bound listener and headless Chromium
// emulating a 390x844 phone; the Engine is the gomock double over a table of
// containers. It carries the browser label and skips where CHROME_BIN is
// unset. Run it with:
//
//	docker run --rm -v "$PWD:/workspace" -w /workspace dis-gotth-live-bench:latest \
//	    go test ./services/opsview/ -run TestOpsView -v -args -ginkgo.label-filter=browser -ginkgo.focus="host panel" -ginkgo.v
//
// WORKBENCH_SHOT_DIR, when set, receives the page with the diff shown, after
// the profile applied, and after the undo.
const hostSpec = "host-phone"

var _ = Describe("The host panel on a phone", Label("browser"), func() {
	It("applies only CSF work after its diff, stops the containers, and undoes it in one tap", func() {
		executable := runtimeconfig.OSEnvironment().String(chromeVariable, "")
		if executable == "" {
			Skip("browser: " + chromeVariable + " is unset — run in dis-gotth-live-bench:latest")
		}
		ctx := context.Background()
		fixture := newHostFixture()
		_, err := fixture.operations.Protect(ctx, opsview.ProtectContainersInput{Names: []string{"runner"}, Protected: true})
		Expect(err).NotTo(HaveOccurred())

		files, err := iofs.NewHostFiles(fixture.state)
		Expect(err).NotTo(HaveOccurred())
		watcher, err := iofs.NewHostWatcher(fixture.state)
		Expect(err).NotTo(HaveOccurred())
		server := httptest.NewUnstartedServer(nil)
		DeferCleanup(server.Close)
		origin := "http://" + server.Listener.Addr().String()
		page, err := opsview.NewOpsView(files, watcher, []string{origin}, slog.New(slog.DiscardHandler), opsview.WithHostOperations(fixture.operations))
		Expect(err).NotTo(HaveOccurred())
		engine := httpserver.NewEngine(hostSpec)
		page.Register(engine)
		server.Config.Handler = engine
		server.Start()
		scope := runtime.NewScope(ctx, hostSpec)
		DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })
		Expect(page.Start(scope)).To(Succeed())

		launcher, err := proc.NewHostLauncher()
		Expect(err).NotTo(HaveOccurred())
		browser := livetest.LaunchBrowser(GinkgoTB(), launcher, livetest.BrowserOptions{Executable: executable, Profile: GinkgoT().TempDir()})
		browser.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": phoneWidth, "height": phoneHeight, "deviceScaleFactor": phoneScale, "mobile": true}, nil)
		browser.Call("Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 5}, nil)
		browser.Navigate(opsview.PageURL(origin))
		state := func(name string) string {
			return browser.EvalString(`(document.querySelector('[data-opsview-container="` + name + `"]') || {dataset: {}}).dataset.opsviewState || ""`)
		}
		await := func(what string, condition func() bool) {
			GinkgoHelper()
			eventually.Await(GinkgoTB(), what, phoneBudget, condition, func(held bool) bool { return held })
		}
		shot := func(name string) {
			if directory := runtimeconfig.OSEnvironment().String(shotVariable, ""); directory != "" {
				Expect(os.WriteFile(filepath.Join(directory, name), browser.Screenshot(), 0o644)).To(Succeed())
			}
		}
		await("the panel to show the host's containers", func() bool { return state("homepage") == "running" })
		browser.EvalJSON(markExpression, nil)

		Expect(browser.EvalString(`document.querySelector('[data-verified="stop/runner"]').disabled + " " + document.querySelector('[data-verified-refusal="stop/runner"]').textContent`)).
			To(Equal("true runner: protected by the operator"))

		const apply = `[data-opsview-profile="only CSF work"] [data-verified="profile/only CSF work"]`
		browser.EvalJSON(`document.querySelector('`+apply+`').click(), null`, nil)
		await("the diff before the confirm", func() bool {
			return browser.EvalString(`(document.querySelector('[data-opsview="host-diff"]') || {textContent: ""}).textContent`) != ""
		})
		Expect(browser.EvalString(`document.querySelector('[data-opsview="host-diff"]').textContent`)).To(Equal("stops 2: homepage, stray."))
		Expect(fixture.engine.changes()).To(BeEmpty(), "the diff changed the host before the confirm")
		shot("host-phone-diff.png")

		browser.EvalJSON(`document.querySelector('[data-opsview="host-confirm-button"]').click(), null`, nil)
		await("the containers to stop", func() bool { return state("homepage") == "exited" && state("stray") == "exited" })
		Expect([]string{state("session-a"), state("build-a"), state("runner")}).To(Equal([]string{"running", "running", "running"}))
		Expect(browser.EvalString(`document.querySelector('[data-opsview="host-undoable"]').textContent`)).To(ContainSubstring("only CSF work"))
		shot("host-phone-applied.png")

		browser.EvalJSON(`document.querySelector('[data-verified="undo"]').click(), null`, nil)
		await("the undo's diff", func() bool {
			return browser.EvalString(`(document.querySelector('[data-opsview="host-diff"]') || {textContent: ""}).textContent`) == "starts 2: homepage, stray."
		})
		browser.EvalJSON(`document.querySelector('[data-opsview="host-confirm-button"]').click(), null`, nil)
		await("the containers to start again", func() bool { return state("homepage") == "running" && state("stray") == "running" })
		shot("host-phone-undone.png")

		Expect(browser.EvalString(markReadBack)).To(Equal(sameDocument), "the page reloaded instead of patching")
		Expect(fixture.engine.changes()).To(ConsistOf("stop homepage", "stop stray", "start homepage", "start stray"))
		AddReportEntry("the host panel on a phone", fmt.Sprintf("browser %s, 390x844 @3x: diff, confirm, stop, undo, start", browser.Version()))
	})
})
