//go:build acceptance

// Copyright 2026 Candace Labs

package opsview_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// The line over this host's own harness state directory, read-only, with the
// merges of the day read from the forge through gh and the build this host
// runs: tonight's real board, graph, workers and ring, on a phone and a
// desktop. With LINE_WATCH set to a duration it then watches the board and keeps a
// frame of a card each time it moves stage, until it has seen one move into
// the pull request stage and one move into a merged stage, or the duration
// runs out. Its subject is the real host, so it is the opt-in acceptance suite:
//
//	docker run --rm --user 1000:1000 -v <state>:<state>:ro -e HOST_STATE=<state> \
//	    -v /usr/bin/gh:/usr/bin/gh:ro -v <gh config>:/var/lib/gh/.config/gh:ro -e HOME=/var/lib/gh \
//	    -e LINE_BUILD_REVISION=<sha> -e LINE_BUILD_TIME=<RFC 3339> -e WORKBENCH_SHOT_DIR=/shots \
//	    -v <shots>:/shots -v "$PWD:/workspace" -w /workspace dis-gotth-live-bench:latest \
//	    go test -tags acceptance ./services/opsview/ -run TestOpsView -args -ginkgo.focus="the line on the live host"
const (
	lineBuildRevisionVariable = "LINE_BUILD_REVISION"
	lineBuildTimeVariable     = "LINE_BUILD_TIME"
	lineWatchVariable         = "LINE_WATCH"
	lineSpec                  = "line-live"
	desktopWidth              = 1440
	desktopHeight             = 1000
	proofScale                = 2
	// browserSetup is the browser session's time beyond the watch: loading,
	// two layouts and their screenshots.
	browserSetup = 10 * time.Minute
	// stagesOnBoard reads every board card's stage, by key.
	stagesOnBoard = `JSON.stringify(Object.fromEntries([...document.querySelectorAll("li.slice[data-opsview-line]")].map((card) => [card.dataset.opsviewLine, card.closest("[data-opsview-stage]").dataset.opsviewStage])))`
)

var lineSettle = eventually.Budget{Within: 60 * time.Second, Interval: 250 * time.Millisecond}

var _ = Describe("the line on the live host", Label("browser"), func() {
	It("draws tonight's board, graph, workers and ring, and the merged work this build lacks", func() {
		environment := runtimeconfig.OSEnvironment()
		executable, stateDirectory, shots := environment.String(chromeVariable, ""), environment.String(hostStateVariable, ""), environment.String(shotVariable, "")
		if executable == "" || stateDirectory == "" || shots == "" {
			Skip("the line on the live host: set " + chromeVariable + ", " + hostStateVariable + " and " + shotVariable)
		}
		built, err := time.Parse(time.RFC3339, environment.String(lineBuildTimeVariable, ""))
		Expect(err).NotTo(HaveOccurred(), lineBuildTimeVariable)
		build := opsview.Build{Revision: environment.String(lineBuildRevisionVariable, ""), Time: built}
		watching, err := environment.Duration(lineWatchVariable, 0)
		Expect(err).NotTo(HaveOccurred(), lineWatchVariable)
		ctx := context.Background()

		state, err := iofs.NewHostFiles(stateDirectory)
		Expect(err).NotTo(HaveOccurred())
		watcher, err := iofs.NewHostWatcher(stateDirectory)
		Expect(err).NotTo(HaveOccurred())
		launcher, err := proc.NewHostLauncher()
		Expect(err).NotTo(HaveOccurred())
		server := httptest.NewUnstartedServer(nil)
		DeferCleanup(server.Close)
		origin := "http://" + server.Listener.Addr().String()
		page, err := opsview.NewOpsView(state, watcher, []string{origin}, slog.New(slog.DiscardHandler), opsview.WithGitHub(launcher), opsview.WithBuild(build))
		Expect(err).NotTo(HaveOccurred())
		router := httpserver.NewEngine(lineSpec)
		page.Register(router)
		server.Config.Handler = router
		server.Start()
		scope := runtime.NewScope(ctx, lineSpec)
		DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })
		Expect(page.Start(scope)).To(Succeed())

		browser := livetest.LaunchBrowser(GinkgoTB(), launcher, livetest.BrowserOptions{Executable: executable, Profile: GinkgoT().TempDir(), Timeout: watching + browserSetup})
		await := func(what string, expression string) {
			GinkgoHelper()
			eventually.Await(GinkgoTB(), what, lineSettle, func() bool { return browser.EvalBool("Boolean(" + expression + ")") }, func(held bool) bool { return held })
		}
		// shoot keeps a PNG of one element, clipped to it, in page pixels.
		shoot := func(name string, selector string) {
			GinkgoHelper()
			var box struct{ X, Y, Width, Height float64 }
			browser.EvalJSON(`(() => { const r = document.querySelector('`+selector+`').getBoundingClientRect(); return {X: r.left + scrollX, Y: r.top + scrollY, Width: r.width, Height: r.height}; })()`, &box)
			var result struct {
				Data string `json:"data"`
			}
			browser.Call("Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": true,
				"clip": map[string]any{"x": box.X, "y": box.Y, "width": box.Width, "height": box.Height, "scale": 1}}, &result)
			image, err := base64.StdEncoding.DecodeString(result.Data)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(shots, name), image, 0o644)).To(Succeed())
		}
		const region = "#" + `opsview\\.line`

		browser.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": phoneWidth, "height": phoneHeight, "deviceScaleFactor": proofScale, "mobile": true}, nil)
		browser.Navigate(opsview.PageURL(origin))
		await("the line's fold", `document.querySelector('[data-opsview="line-toggle"]')`)
		browser.EvalJSON(`document.querySelector('[data-opsview="line-toggle"]').click(), null`, nil)
		await("the line open, its graph drawn", `document.querySelector("svg.graph") || document.querySelector("section.line h3 + p.muted")`)
		await("the merges of the day read", `document.querySelector('[data-opsview-stage="live"] li, [data-opsview-stage="merged"] li')`)
		shoot("line-phone.png", region)
		notLive := browser.EvalString(`(document.querySelector('[data-opsview="upgrade"]') || {textContent: ""}).textContent`)
		merged := browser.EvalString(`String(document.querySelectorAll('[data-opsview-stage="merged"] li').length)`)
		if merged != "0" {
			Expect(notLive).To(ContainSubstring("csf upgrade"), "merged work outside the build names the upgrade")
		}

		browser.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": desktopWidth, "height": desktopHeight, "deviceScaleFactor": 1, "mobile": false}, nil)
		await("the desktop layout", `innerWidth === `+fmt.Sprint(desktopWidth))
		shoot("line-desktop.png", region)
		var stages map[string]string
		Expect(json.Unmarshal([]byte(browser.EvalString(stagesOnBoard)), &stages)).To(Succeed())
		AddReportEntry("the line on the live host", fmt.Sprintf("build %s at %s; %d cards on the board; %s merged and not live: %q", build.Revision, build.Time.Format(time.RFC3339), len(stages), merged, notLive))

		if watching == 0 {
			return
		}
		watch := eventually.Budget{Within: watching, Interval: 2 * time.Second}
		seenPR, seenMerged, frame := false, false, 0
		for !seenPR || !seenMerged {
			before := stages
			after := eventually.Await(GinkgoTB(), "a card moving stage", watch, func() map[string]string {
				var now map[string]string
				Expect(json.Unmarshal([]byte(browser.EvalString(stagesOnBoard)), &now)).To(Succeed())
				return now
			}, func(now map[string]string) bool { return !maps.Equal(now, before) })
			for key, stage := range after {
				if previous, known := before[key]; known && previous != stage {
					frame++
					browser.EvalJSON(`document.querySelector('[data-opsview-line="`+key+`"]').scrollIntoView({block: "center"}), null`, nil)
					shoot(fmt.Sprintf("line-frame-%02d-%s-%s-to-%s.png", frame, key, previous, stage), `[data-opsview-line="`+key+`"]`)
					shoot(fmt.Sprintf("line-frame-%02d-board.png", frame), ".stages")
					AddReportEntry("a card moved", fmt.Sprintf("frame %d at %s: %s %s → %s", frame, time.Now().UTC().Format(time.TimeOnly), key, previous, stage))
					seenPR = seenPR || stage == string(opsview.StagePR)
					seenMerged = seenMerged || stage == "merged" || stage == string(opsview.StageLive)
				}
			}
			stages = after
		}
	})
})
