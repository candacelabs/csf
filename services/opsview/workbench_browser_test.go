// Copyright 2026 Candace Labs

package opsview_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/csf"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/runtime"
	runtimeconfig "github.com/candacelabs/csf/runtime/config"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/harness/session"
	sessionmocks "github.com/candacelabs/csf/services/harness/session/mocks"
	"github.com/candacelabs/csf/services/opsview"
)

// The Workbench on a phone: launch a session from a ticket, send it a
// message and cancel it, each with a tap, and see its card change without a
// reload. Everything is real but the agent: the harness service over a state
// directory the framework creates, whose turn executor is a double that runs
// a turn until it is interrupted; the CSF service's operations over it, the
// same ones csf serve grants the page; the kernel's change notification; a
// bound listener; and headless Chromium emulating a 390x844 phone through the
// process capability. It carries the browser label and skips where the bench
// image's CHROME_BIN is unset. Run it with:
//
//	docker run --rm -v "$PWD:/workspace" -w /workspace dis-gotth-live-bench:latest \
//	    go test ./services/opsview/ -run TestOpsView -v -args -ginkgo.label-filter=browser -ginkgo.v
//
// WORKBENCH_SHOT_DIR, when set, receives a screenshot of the page after the
// cancel.
const (
	phoneSpec     = "workbench-phone"
	phoneWidth    = 390
	phoneHeight   = 844
	phoneScale    = 3
	phoneLatency  = 100.0
	phoneDownload = 1600 * 1024 / 8
	phoneUpload   = 750 * 1024 / 8
	phoneTemplate = "phone"
	phoneMessage  = "Also push the fix."
	phoneTicket   = "https://example.invalid/issues/328"
	shotVariable  = "WORKBENCH_SHOT_DIR"
	gateCommand   = "/opt/csf/harness"
	gateVerb      = "gate"
	// roundTripCap is how long the page waits for an action's outcome before
	// it reports -1, in milliseconds.
	roundTripCap = 20000

	phoneInstrumentation = `
window.__workbench = {live: 0};
document.addEventListener("DOMContentLoaded", () => {
	const wentLive = () => {
		if (!window.__workbench.live && document.documentElement.getAttribute("data-gotth-status") === "live") {
			window.__workbench.live = performance.now();
		}
	};
	wentLive();
	new MutationObserver(wentLive).observe(document.documentElement, {attributes: true, attributeFilter: ["data-gotth-status"]});
});`
)

var phoneBudget = eventually.Budget{Within: 30 * time.Second, Interval: 100 * time.Millisecond}

// phoneRecipe is the template the launch form offers: the agent.json format
// csf submit reads, with an inline task.
func phoneRecipe(repository string) string {
	encoded, err := json.Marshal(map[string]any{
		"agent": map[string]any{"id": "scratch", "revision": 1, "displayName": "Scratch", "instructions": "Work in the worktree."},
		"task":  "Add one line to notes.txt and commit it.", "model": "sonnet", "repositoryId": "scratch",
		"workspace": map[string]any{"repositoryPath": repository, "baseBranch": "main", "branch": "wb/phone", "allowedTools": []string{"Bash"}, "pullRequestTitle": "Phone"},
	})
	Expect(err).NotTo(HaveOccurred())
	return string(encoded)
}

// phoneWorkbench is the harness, the CSF service, the page and a phone.
type phoneWorkbench struct {
	state   string
	origin  string
	browser *livetest.Browser
}

func newPhoneWorkbench(executable string) *phoneWorkbench {
	GinkgoHelper()
	controller := gomock.NewController(GinkgoT())
	launcher := NewMockILauncher(controller)
	launcher.EXPECT().Run(gomock.Any(), gomock.Any()).Return(proc.Result{}, nil).AnyTimes()
	state := GinkgoT().TempDir()
	Expect(harness.RecordModelPolicy(state, harness.ModelPolicy{Allowed: []harness.AllowedModel{{Model: "sonnet", Ruling: "spec", RuledBy: "spec", RuledOn: "2026-10-05"}}})).To(Succeed())
	runner, err := session.NewAgentSessionRunner(
		session.WithLauncher(launcher),
		session.WithStateDirectory(state),
		session.WithGateCommand(gateCommand, gateVerb),
		session.WithOpenTurnExecutors(func(_ context.Context, _ session.TurnExecutorSpec) (session.IOpenTurnExecutor, error) {
			// A turn runs until it is interrupted, which is what a cancel does.
			interrupted := make(chan struct{}, 4)
			executor := sessionmocks.NewMockIOpenTurnExecutor(controller)
			executor.EXPECT().Propose(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ *claudecode.Turn) (*model.Proposal[claudecode.Event], error) {
				select {
				case <-interrupted:
					return &model.Proposal[claudecode.Event]{Provider: claudecode.ProviderName}, nil
				case <-ctx.Done():
					return nil, &claudecode.TurnError{Err: errors.Join(claudecode.ErrTurnAbandoned, context.Cause(ctx))}
				}
			}).AnyTimes()
			executor.EXPECT().Interrupt(gomock.Any()).DoAndReturn(func(_ context.Context) error {
				select {
				case interrupted <- struct{}{}:
				default:
				}
				return nil
			}).AnyTimes()
			executor.EXPECT().Close(gomock.Any()).Return(nil).AnyTimes()
			return executor, nil
		}),
	)
	Expect(err).NotTo(HaveOccurred())
	sessions, err := harness.NewAgentSessionService(harness.WithSessionRunner(runner), harness.WithLauncher(launcher), harness.WithServiceLogger(slog.New(slog.DiscardHandler)))
	Expect(err).NotTo(HaveOccurred())
	api, err := csf.New(csf.WithAgentSessions(sessions))
	Expect(err).NotTo(HaveOccurred())
	scope := runtime.NewScope(context.Background(), phoneSpec)
	DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })
	Expect(sessions.Start(scope)).To(Succeed())

	templates := GinkgoT().TempDir()
	Expect(os.MkdirAll(filepath.Join(templates, phoneTemplate), 0o700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(templates, phoneTemplate, opsview.RecipeFile), []byte(phoneRecipe(GinkgoT().TempDir())), 0o600)).To(Succeed())
	templateFiles, err := iofs.NewHostFiles(templates)
	Expect(err).NotTo(HaveOccurred())
	files, err := iofs.NewHostFiles(state)
	Expect(err).NotTo(HaveOccurred())
	watcher, err := iofs.NewHostWatcher(state)
	Expect(err).NotTo(HaveOccurred())

	server := httptest.NewUnstartedServer(nil)
	DeferCleanup(server.Close)
	workbench := &phoneWorkbench{state: state, origin: "http://" + server.Listener.Addr().String()}
	page, err := opsview.NewOpsView(files, watcher, []string{workbench.origin}, slog.New(slog.DiscardHandler),
		opsview.WithOperations(api), opsview.WithRecipeTemplates(templateFiles))
	Expect(err).NotTo(HaveOccurred())
	engine := httpserver.NewEngine(phoneSpec)
	page.Register(engine)
	server.Config.Handler = engine
	server.Start()
	Expect(page.Start(scope)).To(Succeed())

	browserLauncher, err := proc.NewHostLauncher()
	Expect(err).NotTo(HaveOccurred())
	workbench.browser = livetest.LaunchBrowser(GinkgoTB(), browserLauncher, livetest.BrowserOptions{Executable: executable, Profile: GinkgoT().TempDir()})
	workbench.browser.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": phoneWidth, "height": phoneHeight, "deviceScaleFactor": phoneScale, "mobile": true}, nil)
	workbench.browser.Call("Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 5}, nil)
	// The chat page's phone network: the throttle shapes the page's requests;
	// a WebSocket's frames pass it untouched, so the action round trips are
	// loopback figures.
	workbench.browser.Call("Network.enable", nil, nil)
	workbench.browser.Call("Network.emulateNetworkConditions", map[string]any{"offline": false, "latency": phoneLatency, "downloadThroughput": phoneDownload, "uploadThroughput": phoneUpload}, nil)
	workbench.browser.OnNewDocument(phoneInstrumentation)
	return workbench
}

// act runs action in the page and returns the milliseconds until condition
// held, by the page's own clock, or -1 when it never did.
func (workbench *phoneWorkbench) act(action string, condition string) float64 {
	GinkgoHelper()
	var elapsed float64
	workbench.browser.EvalJSON(`(async () => {
		const done = () => Boolean(`+condition+`);
		const t0 = performance.now();
		const settled = new Promise(resolve => {
			const observer = new MutationObserver(() => { if (done()) { observer.disconnect(); resolve(performance.now() - t0); } });
			observer.observe(document.body, {subtree: true, childList: true, characterData: true, attributes: true});
			setTimeout(() => { observer.disconnect(); resolve(-1); }, `+fmt.Sprint(roundTripCap)+`);
		});
		`+action+`;
		return await settled;
	})()`, &elapsed)
	return elapsed
}

// controlActions are the actions the session's event log records, in order.
func (workbench *phoneWorkbench) controlActions(assignment string) []string {
	GinkgoHelper()
	content, err := os.ReadFile(filepath.Join(workbench.state, assignment, session.EventsFile))
	Expect(err).NotTo(HaveOccurred())
	var actions []string
	for _, line := range bytes.Split(content, []byte("\n")) {
		var record struct {
			EventType string `json:"event_type"`
			Action    string `json:"action"`
			Operator  bool   `json:"operator_authored"`
		}
		if json.Unmarshal(line, &record) == nil && record.EventType == session.EventTypeControlAction {
			actions = append(actions, fmt.Sprintf("%s operator=%t", record.Action, record.Operator))
		}
	}
	return actions
}

var _ = Describe("The Workbench on a phone", Label("browser"), func() {
	It("launches a session from a ticket, sends it a message and cancels it, and the card follows without a reload", func() {
		executable := runtimeconfig.OSEnvironment().String(chromeVariable, "")
		if executable == "" {
			Skip("browser: " + chromeVariable + " is unset — run in dis-gotth-live-bench:latest")
		}
		workbench := newPhoneWorkbench(executable)
		// The first load warms a cold renderer; the second is measured.
		workbench.browser.Navigate(opsview.PageURL(workbench.origin))
		workbench.browser.Navigate(opsview.PageURL(workbench.origin))
		live := eventually.Await(GinkgoTB(), "the runtime to go live", phoneBudget, func() float64 {
			var at float64
			workbench.browser.EvalJSON(`window.__workbench.live`, &at)
			return at
		}, func(at float64) bool { return at > 0 })
		var firstPaint, requestStart, contentLoaded float64
		workbench.browser.EvalJSON(`(performance.getEntriesByType("paint").find(e => e.name === "first-contentful-paint") || {startTime: 0}).startTime`, &firstPaint)
		workbench.browser.EvalJSON(`performance.getEntriesByType("navigation")[0].requestStart`, &requestStart)
		workbench.browser.EvalJSON(`performance.getEntriesByType("navigation")[0].domContentLoadedEventEnd`, &contentLoaded)
		workbench.browser.EvalJSON(markExpression, nil)

		opened := workbench.act(`document.querySelector('[data-opsview="launch-toggle"]').click()`, `document.querySelector('[data-opsview="ticket"]')`)
		Expect(opened).To(BeNumerically(">", 0), "the launch form never unfolded")
		eventually.Await(GinkgoTB(), "the template on offer", phoneBudget, func() string {
			return workbench.browser.EvalString(`(document.querySelector('[data-opsview="template"] option[value="phone"]') || {value: ""}).value`)
		}, func(value string) bool { return value == phoneTemplate })
		check := workbench.act(`document.querySelector('[data-opsview="ticket"]').value = `+livetest.JSString(phoneTicket)+`;
			document.querySelector('[data-opsview="template"]').value = `+livetest.JSString(phoneTemplate)+`;
			document.querySelector('[data-opsview="check"]').click()`,
			`document.querySelector('[data-opsview="launch"]')`)
		Expect(check).To(BeNumerically(">", 0), "the admission check never came back")
		Expect(workbench.browser.EvalString(`document.querySelector('[data-opsview="admitted"]').textContent`)).To(ContainSubstring("report only"))

		launch := workbench.act(`document.querySelector('[data-opsview="launch"]').click()`, `document.querySelector("[data-opsview-assignment]")`)
		Expect(launch).To(BeNumerically(">", 0), "the launched session's card never appeared")
		assignment := workbench.browser.EvalString(`document.querySelector("[data-opsview-assignment]").getAttribute("data-opsview-assignment")`)
		card := `document.querySelector('[data-opsview-assignment="` + assignment + `"]')`

		expand := workbench.act(card+`.querySelector('[data-opsview="expand"]').click()`, card+`.querySelector('[data-opsview="compose"]')`)
		Expect(expand).To(BeNumerically(">", 0), "the card never opened")
		compose := workbench.act(card+`.querySelector('[data-opsview="compose"]').click()`, card+`.querySelector('[data-opsview="message"]')`)
		Expect(compose).To(BeNumerically(">", 0), "the composer never opened")
		send := workbench.act(card+`.querySelector('[data-opsview="message"]').value = `+livetest.JSString(phoneMessage)+`;
			`+card+`.querySelector('[data-opsview="send"]').click()`,
			`(`+card+`.querySelector('[data-opsview="card-notice"]') || {textContent: ""}).textContent.includes("Sent: queued as turn 2.")`)
		Expect(send).To(BeNumerically(">", 0), "the send never came back as a queued turn")

		ask := workbench.act(card+`.querySelector('[data-opsview="cancel"]').click()`, card+`.querySelector('[data-opsview="confirm-cancel"]')`)
		Expect(ask).To(BeNumerically(">", 0), "cancel never asked to be confirmed")
		Expect(workbench.browser.EvalString(card+`.querySelector('[data-opsview="confirm"]').textContent`)).To(ContainSubstring("Cancel scratch?"), "the confirmation names the session")
		cancel := workbench.act(card+`.querySelector('[data-opsview="confirm-cancel"]').click()`, `!`+card)
		Expect(cancel).To(BeNumerically(">", 0), "the closed session never left the running rows")
		others := workbench.act(`document.querySelector('[data-opsview="others"]').click()`, card+` && `+card+`.classList.contains("s-closed")`)
		Expect(others).To(BeNumerically(">", 0), "the closed session is not in the idle and finished list")
		Expect(workbench.browser.EvalString(card+`.textContent`)).To(ContainSubstring("Message sent by the operator"), "it is still open where the viewer left it")
		Expect(workbench.browser.EvalString(markReadBack)).To(Equal(sameDocument), "the page reloaded instead of patching")
		Expect(workbench.controlActions(assignment)).To(Equal([]string{"submit operator=false", "send operator=true", "cancel operator=false"}))

		if directory := runtimeconfig.OSEnvironment().String(shotVariable, ""); directory != "" {
			Expect(os.WriteFile(filepath.Join(directory, "workbench-phone.png"), workbench.browser.Screenshot(), 0o644)).To(Succeed())
		}
		AddReportEntry("the Workbench on a phone: 390x844 @3x, 100 ms latency, 1.6 Mbps down", fmt.Sprintf(
			"browser %s\nrequest start %.0f ms\nfirst contentful paint %.0f ms (%.0f ms after the request)\nDOM content loaded %.0f ms\nruntime live %.0f ms\n"+
				"admission check round trip %.0f ms\nlaunch to card %.0f ms\nsend round trip %.0f ms\ncancel to closed card %.0f ms",
			workbench.browser.Version(), requestStart, firstPaint, firstPaint-requestStart, contentLoaded, live, check, launch, send, cancel))
	})
})
