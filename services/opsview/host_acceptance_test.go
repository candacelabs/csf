//go:build acceptance

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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/ipc/docker"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/runtime"
	runtimeconfig "github.com/candacelabs/csf/runtime/config"
	"github.com/candacelabs/csf/services/opsview"
)

// The live run: the host panel over this machine's own Docker Engine,
// process table and harness state directory, applying the operator's
// "essentials" profile while the host already satisfies it. Its subject is
// the real host, so it is the opt-in acceptance suite. It passes no confirm,
// so a host that did not satisfy the profile is refused rather than changed.
// Run it in the bench image with the Engine socket:
//
//	docker run --rm --group-add <socket gid> -v /var/run/docker.sock:/var/run/docker.sock \
//	    -v <state>:<state> -e HOST_STATE=<state> -e HOST_ESSENTIALS=<name,name> \
//	    -e WORKBENCH_SHOT_DIR=/shots -v <shots>:/shots -v "$PWD:/workspace" -w /workspace \
//	    dis-gotth-live-bench:latest go test -tags acceptance ./services/opsview/ -run TestOpsView \
//	    -args -ginkgo.focus="live host"
const (
	hostStateVariable      = "HOST_STATE"
	hostEssentialsVariable = "HOST_ESSENTIALS"
	essentialsProfile      = "essentials"
)

var _ = Describe("The host panel on the live host", Label("browser"), func() {
	It("applies essentials while they are already satisfied, as a no-op diff", func() {
		environment := runtimeconfig.OSEnvironment()
		executable, stateDirectory, essentials := environment.String(chromeVariable, ""), environment.String(hostStateVariable, ""), environment.String(hostEssentialsVariable, "")
		if executable == "" || stateDirectory == "" || essentials == "" {
			Skip("live host: set " + chromeVariable + ", " + hostStateVariable + " and " + hostEssentialsVariable)
		}
		ctx := context.Background()
		engine, err := docker.NewContainerHost()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(engine.Close)
		processes, err := iofs.NewHostFiles("/proc")
		Expect(err).NotTo(HaveOccurred())
		state, err := iofs.NewHostFiles(stateDirectory)
		Expect(err).NotTo(HaveOccurred())
		operations, err := opsview.NewHostOperations(opsview.WithHostContainers(engine), opsview.WithProcessTable(processes), opsview.WithHostState(state))
		Expect(err).NotTo(HaveOccurred())
		_, err = operations.PutProfile(ctx, opsview.PutHostProfileInput{Profile: opsview.HostProfile{Name: essentialsProfile, Keep: strings.Split(essentials, ",")}})
		Expect(err).NotTo(HaveOccurred())

		plan, err := operations.ApplyProfile(ctx, opsview.ApplyHostProfileInput{Name: essentialsProfile, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Changes).To(BeEmpty(), "the host does not satisfy essentials: %s", plan.Result)
		report, err := operations.Host(ctx, opsview.GetHostInput{})
		Expect(err).NotTo(HaveOccurred())
		groups := map[opsview.HostGroup]int{}
		for _, found := range report.Containers {
			groups[found.Group]++
		}
		AddReportEntry("the live host", fmt.Sprintf("load %.1f on %d cores, %d containers: %d CSF work, %d other; essentials: %s",
			report.Gauges.LoadOneMinute, report.Gauges.Cores, len(report.Containers), groups[opsview.GroupCSF], groups[opsview.GroupOther], plan.Result))

		watcher, err := iofs.NewHostWatcher(stateDirectory)
		Expect(err).NotTo(HaveOccurred())
		server := httptest.NewUnstartedServer(nil)
		DeferCleanup(server.Close)
		origin := "http://" + server.Listener.Addr().String()
		page, err := opsview.NewOpsView(state, watcher, []string{origin}, slog.New(slog.DiscardHandler), opsview.WithHostOperations(operations))
		Expect(err).NotTo(HaveOccurred())
		router := httpserver.NewEngine(hostSpec)
		page.Register(router)
		server.Config.Handler = router
		server.Start()
		scope := runtime.NewScope(ctx, hostSpec)
		DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })
		Expect(page.Start(scope)).To(Succeed())

		launcher, err := proc.NewHostLauncher()
		Expect(err).NotTo(HaveOccurred())
		browser := livetest.LaunchBrowser(GinkgoTB(), launcher, livetest.BrowserOptions{Executable: executable, Profile: GinkgoT().TempDir()})
		browser.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": phoneWidth, "height": phoneHeight, "deviceScaleFactor": phoneScale, "mobile": true}, nil)
		browser.Navigate(opsview.PageURL(origin))
		const apply = `[data-opsview-profile="essentials"] [data-verified="profile/essentials"]`
		eventually.Await(GinkgoTB(), "the essentials button, verified", phoneBudget, func() string {
			return browser.EvalString(`(document.querySelector('` + apply + `') || {title: ""}).title`)
		}, func(title string) bool { return title != "" })
		Expect(browser.EvalString(`document.querySelector('` + apply + `').title`)).To(Equal("Nothing to change: the host already matches."))
		shot := func(name string) {
			if directory := environment.String(shotVariable, ""); directory != "" {
				Expect(os.WriteFile(filepath.Join(directory, name), browser.Screenshot(), 0o644)).To(Succeed())
			}
		}
		shot("host-live-before.png")
		browser.EvalJSON(`document.querySelector('`+apply+`').click(), null`, nil)
		eventually.Await(GinkgoTB(), "the no-op outcome", phoneBudget, func() string {
			return browser.EvalString(`(document.querySelector('[data-opsview="host-notice"]') || {textContent: ""}).textContent`)
		}, func(notice string) bool { return notice == "Nothing to change: the host already matches." })
		shot("host-live-applied.png")
	})
})
