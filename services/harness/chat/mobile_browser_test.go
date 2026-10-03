// Copyright 2026 Candace Labs

package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/ipc/model"
	"github.com/candacelabs/csf/ipc/model/claudecode"
	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
	"github.com/candacelabs/csf/pkg/httpserver"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	runtimeconfig "github.com/candacelabs/csf/runtime/config"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/harness/chat"
	"github.com/candacelabs/csf/services/harness/session"
	sessionmocks "github.com/candacelabs/csf/services/harness/session/mocks"
)

// The chat page on a phone over the tailnet, measured and then held to its
// targets. Everything is real but the agent: a harness service over a run
// directory the framework creates, whose turn executor is a double that
// writes records into the session's event log when the spec says so; a
// bound listener; headless Chromium emulating a phone over a throttled
// network, through the process capability. It carries the browser label and
// skips where the bench image's CHROME_BIN is unset. Run it with:
//
//	docker run --rm -v "$PWD:/workspace" -w /workspace dis-gotth-live-bench:latest \
//	    go test ./services/harness/chat/ -run TestChatMobile -v -args -ginkgo.label-filter=browser -ginkgo.v
//
// CHAT_SHOT_DIR, when set, receives a screenshot of the page as measured.
const (
	chromeVariable  = "CHROME_BIN"
	shotVariable    = "CHAT_SHOT_DIR"
	mobileSpec      = "chat-mobile"
	mobileAssign    = "0caf2d51-1ee7-468a-882f-ac478a437640"
	mobileBranch    = "h2/mobile"
	gateExecutable  = "/opt/csf/harness"
	gateVerb        = "gate"
	phoneWidth      = 390
	phoneHeight     = 844
	phoneScale      = 3
	keyboardHeight  = 336
	networkLatency  = 100.0
	downloadBytes   = 1600 * 1024 / 8
	uploadBytes     = 750 * 1024 / 8
	paintSamples    = 5
	overflowEntries = 24
	statusLive      = "live"
	mobileMessage   = "Also push the fix."
	queuedNotice    = "Queued as turn"
	readableFont    = 16.0
	tapTarget       = 44.0
	scrollSlack     = 2.0
	scrollUpBy      = 300
)

var (
	mobileBudget = eventually.Budget{Within: 30 * time.Second, Interval: 100 * time.Millisecond}
	assistantAt  = time.Date(2026, 10, 2, 1, 10, 4, 0, time.UTC)
)

// assistantMarkdown is what one assistant message says: Markdown with a
// fenced block holding a long line, which a phone has to render without
// breaking its layout.
const assistantMarkdown = "Here is the **plan** for the fix:\n\n1. Build it\n2. Test it with `go test ./...`\n\n```go\nfunc main() { fmt.Println(\"a very long line that must scroll sideways on a phone rather than break the layout of the whole page\") }\n```"

// assistantRecord is the executor's record of that message, in the shape the
// turn executor forwards.
var assistantRecord = recordOf(assistantMarkdown)

func recordOf(text string) string {
	type block struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	encoded, err := json.Marshal(map[string]any{
		"type":    "assistant",
		"message": map[string]any{"role": "assistant", "content": []block{{Type: "text", Text: text}}},
	})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// instrumentation is installed before any page script: it records when the
// runtime went live, and when each transcript change was painted, so every
// timing is the browser's own clock and not the spec's.
const instrumentation = `
window.__chat = {live: 0, paints: []};
document.addEventListener("DOMContentLoaded", () => {
	const wentLive = () => {
		if (!window.__chat.live && document.documentElement.getAttribute("data-gotth-status") === "live") {
			window.__chat.live = performance.now();
		}
	};
	wentLive();
	new MutationObserver(wentLive).observe(document.documentElement, {attributes: true, attributeFilter: ["data-gotth-status"]});
	new MutationObserver(() => {
		const count = document.querySelectorAll('[data-gotth-region="chat.transcript"] li').length;
		requestAnimationFrame(() => requestAnimationFrame(() => {
			window.__chat.paints.push({count: count, at: performance.now()});
		}));
	}).observe(document.body, {subtree: true, childList: true, characterData: true});
});`

// mobileHarness is one session under a chat page under a browser.
type mobileHarness struct {
	server  *httptest.Server
	origin  string
	logger  chan *slog.Logger
	release chan struct{}
	browser *livetest.Browser
	events  *slog.Logger
}

// newMobileHarness builds the service with a scripted executor, mounts the
// chat on an engine the way the harness host app does, binds a listener and
// opens a phone-emulating browser over a throttled network.
func newMobileHarness(executable string) *mobileHarness {
	GinkgoHelper()
	controller := gomock.NewController(GinkgoT())
	launcher := NewMockILauncher(controller)
	launcher.EXPECT().Run(gomock.Any(), gomock.Any()).Return(proc.Result{}, nil).AnyTimes()
	harnessSpec := &mobileHarness{logger: make(chan *slog.Logger, 1), release: make(chan struct{})}
	runner, err := session.NewAgentSessionRunner(
		session.WithLauncher(launcher),
		session.WithStateDirectory(GinkgoT().TempDir()),
		session.WithGateCommand(gateExecutable, gateVerb),
		session.WithOpenTurnExecutors(func(_ context.Context, spec session.TurnExecutorSpec) (session.IOpenTurnExecutor, error) {
			executor := sessionmocks.NewMockIOpenTurnExecutor(controller)
			executor.EXPECT().Propose(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ *claudecode.Turn) (*model.Proposal[claudecode.Event], error) {
				select {
				case <-harnessSpec.release:
					return &model.Proposal[claudecode.Event]{Provider: claudecode.ProviderName}, nil
				case <-ctx.Done():
					return nil, &claudecode.TurnError{Err: errors.Join(claudecode.ErrTurnAbandoned, context.Cause(ctx))}
				}
			}).AnyTimes()
			executor.EXPECT().Interrupt(gomock.Any()).Return(nil).AnyTimes()
			executor.EXPECT().Close(gomock.Any()).Return(nil).AnyTimes()
			harnessSpec.logger <- spec.Logger
			return executor, nil
		}),
	)
	Expect(err).NotTo(HaveOccurred())
	service, err := harness.NewAgentSessionService(harness.WithSessionRunner(runner), harness.WithServiceLogger(slog.New(slog.DiscardHandler)))
	Expect(err).NotTo(HaveOccurred())
	scope := runtime.NewScope(context.Background(), mobileSpec)
	Expect(service.Start(scope)).To(Succeed())
	DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })

	harnessSpec.server = httptest.NewUnstartedServer(nil)
	DeferCleanup(harnessSpec.server.Close)
	harnessSpec.origin = "http://" + harnessSpec.server.Listener.Addr().String()
	page, err := chat.NewChat(service, []string{harnessSpec.origin}, slog.New(slog.DiscardHandler))
	Expect(err).NotTo(HaveOccurred())
	engine := httpserver.NewEngine(mobileSpec)
	page.Register(engine)
	harnessSpec.server.Config.Handler = engine
	harnessSpec.server.Start()
	Expect(page.Start(scope)).To(Succeed())

	_, err = service.Submit(context.Background(), &harnessv1.SubmitAgentSessionRequest{Recipe: &pb.AgentAssignmentRecipe{
		AssignmentId: mobileAssign,
		Agent:        &pb.AgentDefinition{Id: "scratch", Revision: 1, DisplayName: "Scratch", Instructions: "Work in the worktree."},
		TicketUrl:    "https://example.invalid/issues/1",
		Task:         "Add one line to notes.txt and commit it.",
		Model:        "sonnet",
		RepositoryId: "scratch",
		Workspace: &pb.AgentWorkspace{
			RepositoryPath: GinkgoT().TempDir(), BaseBranch: "main", Branch: mobileBranch,
			AllowedTools: []string{"Bash", "Edit"}, PullRequestTitle: "H2 mobile",
		},
	}})
	Expect(err).NotTo(HaveOccurred())
	Eventually(harnessSpec.logger, mobileBudget.Within).Should(Receive(&harnessSpec.events), "the session opens its executor")

	launcher2, err := proc.NewHostLauncher()
	Expect(err).NotTo(HaveOccurred())
	harnessSpec.browser = livetest.LaunchBrowser(GinkgoTB(), launcher2, livetest.BrowserOptions{Executable: executable, Profile: GinkgoT().TempDir()})
	harnessSpec.browser.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": phoneWidth, "height": phoneHeight, "deviceScaleFactor": phoneScale, "mobile": true}, nil)
	harnessSpec.browser.Call("Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 5}, nil)
	// The throttle shapes the page's requests; a WebSocket's frames pass it
	// untouched, so the round trips below are loopback figures.
	harnessSpec.browser.Call("Network.enable", nil, nil)
	harnessSpec.browser.Call("Network.emulateNetworkConditions", map[string]any{"offline": false, "latency": networkLatency, "downloadThroughput": downloadBytes, "uploadThroughput": uploadBytes}, nil)
	harnessSpec.browser.OnNewDocument(instrumentation)
	return harnessSpec
}

// append lands one executor record in the session's event log, the way the
// turn executor forwards one, and returns the wall-clock time it landed.
func (harnessSpec *mobileHarness) append(record string) time.Time {
	GinkgoHelper()
	Expect(json.Valid([]byte(record))).To(BeTrue(), "the fixture record is JSON")
	landed := time.Now()
	harnessSpec.events.Info("turn executor event", slog.String("event_type", "assistant"), slog.Int("turn", 1),
		slog.String("direction", "out"), slog.Any("event", json.RawMessage(record)))
	return landed
}

func (harnessSpec *mobileHarness) pageURL() string {
	return chat.PageURL(harnessSpec.origin, mobileAssign)
}

// paints reads the painted transcript sizes recorded in the page.
func (harnessSpec *mobileHarness) paints() []paint {
	var painted []paint
	harnessSpec.browser.EvalJSON(`window.__chat.paints`, &painted)
	return painted
}

type paint struct {
	Count int     `json:"count"`
	At    float64 `json:"at"`
}

// paintedAt is the wall-clock time the transcript first painted count entries.
func (harnessSpec *mobileHarness) paintedAt(count int) time.Time {
	GinkgoHelper()
	painted := eventually.Await(GinkgoTB(), fmt.Sprintf("the transcript to paint %d entries", count), mobileBudget,
		harnessSpec.paints, func(painted []paint) bool {
			return slices.ContainsFunc(painted, func(sample paint) bool { return sample.Count >= count })
		})
	index := slices.IndexFunc(painted, func(sample paint) bool { return sample.Count >= count })
	origin := harnessSpec.browser.EvalString(`String(performance.timeOrigin)`)
	var originMS float64
	_, err := fmt.Sscan(origin, &originMS)
	Expect(err).NotTo(HaveOccurred())
	return time.UnixMilli(int64(originMS + painted[index].At))
}

func (harnessSpec *mobileHarness) entries() int {
	var count float64
	harnessSpec.browser.EvalJSON(`document.querySelectorAll('[data-gotth-region="chat.transcript"] li').length`, &count)
	return int(count)
}

// timings is what the page measured, in milliseconds.
type timings struct {
	requestStart         float64
	firstContentfulPaint float64
	timeToLive           float64
	recordToPaint        []float64
	sendRoundTrip        float64
}

func median(samples []float64) float64 {
	sorted := slices.Clone(samples)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}

func (measured timings) String() string {
	return fmt.Sprintf("request start %.0f ms\nfirst contentful paint %.0f ms (%.0f ms after the request)\nruntime live %.0f ms\nrecord to paint median %.0f ms over %d (min %.0f, max %.0f)\nsend round trip %.0f ms",
		measured.requestStart, measured.firstContentfulPaint, measured.firstContentfulPaint-measured.requestStart, measured.timeToLive, median(measured.recordToPaint), len(measured.recordToPaint),
		slices.Min(measured.recordToPaint), slices.Max(measured.recordToPaint), measured.sendRoundTrip)
}

// awaitLive waits for the runtime to connect and returns when it did, in
// milliseconds since the document's navigation started.
func (harnessSpec *mobileHarness) awaitLive() float64 {
	GinkgoHelper()
	return eventually.Await(GinkgoTB(), "the runtime to go live", mobileBudget,
		func() float64 {
			var at float64
			harnessSpec.browser.EvalJSON(`window.__chat.live`, &at)
			return at
		}, func(at float64) bool { return at > 0 })
}

// grow lands one more assistant record and waits for it to paint.
func (harnessSpec *mobileHarness) grow() {
	GinkgoHelper()
	before := harnessSpec.entries()
	harnessSpec.append(assistantRecord)
	harnessSpec.paintedAt(before + 1)
}

// measure loads the page and takes the timings. The page is loaded once to
// warm the renderer and measured on the second load: a cold renderer in a
// container spends a variable number of seconds before its first request,
// which is the machine's figure and not the page's.
func (harnessSpec *mobileHarness) measure() timings {
	GinkgoHelper()
	harnessSpec.browser.Navigate(harnessSpec.pageURL())
	harnessSpec.browser.Navigate(harnessSpec.pageURL())
	var measured timings
	measured.timeToLive = harnessSpec.awaitLive()
	harnessSpec.browser.EvalJSON(`(performance.getEntriesByType("paint").find(e => e.name === "first-contentful-paint") || {startTime: 0}).startTime`, &measured.firstContentfulPaint)
	harnessSpec.browser.EvalJSON(`performance.getEntriesByType("navigation")[0].requestStart`, &measured.requestStart)

	for range paintSamples {
		before := harnessSpec.entries()
		landed := harnessSpec.append(assistantRecord)
		painted := harnessSpec.paintedAt(before + 1)
		measured.recordToPaint = append(measured.recordToPaint, float64(painted.Sub(landed).Microseconds())/1000)
	}

	harnessSpec.browser.EvalJSON(`(async () => {
		const field = document.querySelector('textarea[name="message"]');
		field.value = `+livetest.JSString(mobileMessage)+`;
		field.dispatchEvent(new Event("input", {bubbles: true}));
		const t0 = performance.now();
		const settled = new Promise(resolve => {
			const observer = new MutationObserver(() => {
				if (document.body.textContent.includes(`+livetest.JSString(queuedNotice)+`)) { observer.disconnect(); resolve(performance.now() - t0); }
			});
			observer.observe(document.body, {subtree: true, childList: true, characterData: true});
			setTimeout(() => { observer.disconnect(); resolve(-1); }, 15000);
		});
		document.querySelector('button[type="submit"]').click();
		return await settled;
	})()`, &measured.sendRoundTrip)
	Expect(measured.sendRoundTrip).To(BeNumerically(">", 0), "the send never came back as a queued turn")
	return measured
}

func (harnessSpec *mobileHarness) screenshot(name string) {
	GinkgoHelper()
	directory := runtimeconfig.OSEnvironment().String(shotVariable, "")
	if directory == "" {
		return
	}
	Expect(os.WriteFile(filepath.Join(directory, name), harnessSpec.browser.Screenshot(), 0o644)).To(Succeed())
}

func browserExecutable() string {
	GinkgoHelper()
	executable := runtimeconfig.OSEnvironment().String(chromeVariable, "")
	if executable == "" {
		Skip("browser: " + chromeVariable + " is unset — run in dis-gotth-live-bench:latest")
	}
	return executable
}

var _ = Describe("The chat page on a phone", Label("browser"), func() {
	It("measures first paint, time to live, record to paint and the send round trip", Label("measure"), func() {
		harnessSpec := newMobileHarness(browserExecutable())
		measured := harnessSpec.measure()
		harnessSpec.screenshot("chat-mobile-measured.png")
		AddReportEntry("chat on a phone: 390x844 @3x, 100 ms latency, 1.6 Mbps down", measured.String())
	})

	It("keeps the input pinned above the keyboard, the text readable, code scrolling sideways and the view on the newest message", func() {
		harnessSpec := newMobileHarness(browserExecutable())
		harnessSpec.browser.Navigate(harnessSpec.pageURL())
		harnessSpec.awaitLive()

		By("rendering Markdown and a code block that scrolls instead of widening the page")
		harnessSpec.grow()
		Expect(harnessSpec.browser.EvalBool(`!!document.querySelector('[data-gotth-region="chat.transcript"] strong')`)).To(BeTrue(), "bold Markdown rendered as an element")
		Expect(harnessSpec.browser.EvalBool(`!!document.querySelector('[data-gotth-region="chat.transcript"] pre code')`)).To(BeTrue(), "the fenced block rendered as code")
		overflow := harnessSpec.browser.EvalString(`getComputedStyle(document.querySelector('[data-gotth-region="chat.transcript"] pre')).overflowX`)
		Expect(overflow).To(BeElementOf("auto", "scroll"))
		Expect(harnessSpec.browser.EvalBool(`document.documentElement.scrollWidth <= window.innerWidth`)).To(BeTrue(), "the page did not grow sideways")

		By("being readable without zooming, with large tap targets")
		var fontSize float64
		harnessSpec.browser.EvalJSON(`parseFloat(getComputedStyle(document.querySelector('[data-gotth-region="chat.transcript"] li')).fontSize)`, &fontSize)
		Expect(fontSize).To(BeNumerically(">=", readableFont))
		var buttonHeight float64
		harnessSpec.browser.EvalJSON(`document.querySelector('button[type="submit"]').getBoundingClientRect().height`, &buttonHeight)
		Expect(buttonHeight).To(BeNumerically(">=", tapTarget))

		By("staying on the newest message as the transcript grows past the screen")
		for range overflowEntries {
			harnessSpec.grow()
		}
		// Distance from the bottom, in either scroll direction: a bottom-anchored
		// container counts its scroll offset from the bottom.
		atBottom := `(() => { const s = document.querySelector("[data-chat-scroll]"); return getComputedStyle(s).flexDirection === "column-reverse" ? Math.abs(s.scrollTop) : s.scrollHeight - s.clientHeight - s.scrollTop; })()`
		var gap float64
		harnessSpec.browser.EvalJSON(atBottom, &gap)
		Expect(gap).To(BeNumerically("<=", scrollSlack), "the newest message is in view")

		By("holding the operator's place once they scroll up")
		harnessSpec.browser.EvalJSON(fmt.Sprintf(`document.querySelector("[data-chat-scroll]").scrollBy(0, -%d), null`, scrollUpBy), nil)
		harnessSpec.browser.EvalJSON(atBottom, &gap)
		Expect(gap).To(BeNumerically("~", scrollUpBy, scrollSlack), "the operator scrolled away from the bottom")
		// What the operator sees is the invariant: the entry under their eyes
		// keeps its place on the screen while a new one lands below.
		anchorTop := `document.querySelector('[data-gotth-region="chat.transcript"] li').getBoundingClientRect().top`
		var before float64
		harnessSpec.browser.EvalJSON(anchorTop, &before)
		harnessSpec.grow()
		var after float64
		harnessSpec.browser.EvalJSON(anchorTop, &after)
		Expect(after).To(BeNumerically("~", before, scrollSlack), "a new message moved what the operator was reading")

		By("keeping the input pinned to the bottom when the keyboard opens, without the view jumping")
		harnessSpec.browser.EvalJSON(`document.querySelector("[data-chat-scroll]").scrollTo(0, document.querySelector("[data-chat-scroll]").scrollHeight), null`, nil)
		harnessSpec.browser.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": phoneWidth, "height": phoneHeight - keyboardHeight, "deviceScaleFactor": phoneScale, "mobile": true}, nil)
		eventually.Await(GinkgoTB(), "the composer to sit inside the shorter viewport", mobileBudget,
			func() bool {
				return harnessSpec.browser.EvalBool(`(() => { const r = document.querySelector('textarea[name="message"]').getBoundingClientRect(); return r.bottom <= window.innerHeight && r.top >= 0; })()`)
			}, func(inside bool) bool { return inside })
		harnessSpec.browser.EvalJSON(atBottom, &gap)
		Expect(gap).To(BeNumerically("<=", scrollSlack), "the newest message stayed in view above the keyboard")
		harnessSpec.screenshot("chat-mobile-keyboard.png")
	})
})
