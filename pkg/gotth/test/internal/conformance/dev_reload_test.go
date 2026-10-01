package conformance_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/patience"
)

// ---------------------------------------------------------------------------
// FR-57 — the dev-reload loop, end to end, in a browser.
//
// This file exists because the evidence for FR-57 was produced twice by two
// different agents and thrown away both times. docs/gates/phase-4.md §6 has
// carried "DEV-2's browser loop is not in CI" as a condition on Phase 4's exit
// through four consecutive revisions, and its own summary of why is the reason
// this file is committed rather than run once: "It is not that nobody can
// write it. It is that nothing forces anybody to keep it."
//
// Run it:
//
//	docker run --rm -v "$PWD:/workspace" -w /workspace/candace/pkg/gotth \
//	    dis-gotth-live-bench:latest \
//	    bash -c 'GOTTHLIVE_E2E=1 go test ./test/internal/conformance/ -count=1 \
//	        -args -ginkgo.label-filter=browser -ginkgo.fail-on-empty'
//
// ci.sh runs exactly that command in the browser step, and announces these
// specs by name where no browser exists.
//
// # What the loop is, so the assertions can be read against a mechanism
//
// (*live.App).DevReloadScript stamps the identity of the build that rendered
// the document into data-gotth-dev-build on its own script tag.
// client/dev-reload.js polls <mount>/gotth-live-dev-build and reloads the
// document when the answer differs from that stamp. The identity is a hash of
// the running executable, so it moves when and only when the code moves.
//
// # What is asserted, and what is deliberately not
//
// Asserted: the PROPERTY. The document reloaded, or it did not. A marker set
// on `window` before the edit is the discriminator, and it is the only honest
// one available — the client runtime's own reconnect-and-resync repaints every
// live region after any restart, so "the number changed" cannot tell a reload
// from a resync. `window` does not survive a document reload and does survive
// a resync, so the marker's absence is a reload and its presence is not.
//
// Not asserted: milliseconds. The two runs this file replaces measured 1,810 ms
// for the templ change and 2,715 ms for the Go change, on one tree, on a host
// whose other load was not recorded. This host is a shared 32-core VM that also
// serves live traffic, and a threshold taken from somebody else's afternoon is
// a flake with a requirement number on it. Durations are reported as
// observations, with the host beside them, and gate nothing.
//
// # These specs were shown able to fail
//
// live.executableBuildID was made to return a constant in a throwaway copy of
// the tree — the feature's identity frozen while everything else kept working
// — and rebuilt. The two reload specs went red on "the document never
// reloaded: the marker set before the edit is still on window", after 180 s
// each, and the negative control stayed green with "build identity unchanged:
// MUTANT-C-frozen-identity" and the watcher's restart count moving 1 → 2. That
// is the right pair: the control cannot see a frozen identity, which is why
// the two positive specs exist. The commit body carries the transcript.
// ---------------------------------------------------------------------------

// devMount is where examples/counter mounts its live handler — the MountPath
// constant in its main.go. The build-identity route hangs off it, and this
// file addresses it from Go as well as from the page, so it is named once.
const devMount = "/live"

// devSourceExts is what is copied out of examples/counter into the watched
// scratch directory.
//
// An allowlist rather than "everything", because examples/counter/.gitignore
// permits a 16 MB built binary called `counter` to sit in that directory and
// copying it costs a second per spec for a file the watcher does not watch.
// A source file this list forgets fails the copy assertion below rather than
// producing a mysterious build error.
var devSourceExts = map[string]bool{
	".go": true, ".templ": true, ".css": true, ".html": true,
	".mod": true, ".sum": true, ".md": true,
}

// devWatched is examples/counter, copied out of the checkout, running under
// internal/cmd/gotth-live-dev.
//
// The COPY is the point. These specs edit source files and the checkout is
// shared: this suite runs beside other agents' work and inside a container
// that mounts the repository read-write, so a spec that edited
// examples/counter in place would be one panic away from leaving DEV-3's
// example with "[TEMPL CHANGE]" in its heading. Everything below happens in
// Ginkgo's temporary directory, and the checkout is only ever read.
type devWatched struct {
	dir  string // the watched copy of examples/gotth/counter
	addr string // where the counter is listening

	mu    sync.Mutex
	lines []string
}

// startWatchedCounter copies the example, starts the watcher over it, and
// returns once the application it built is serving.
func startWatchedCounter() *devWatched {
	GinkgoHelper()

	root, err := filepath.Abs("../../..")
	Expect(err).NotTo(HaveOccurred())
	// The export root: the examples left this tree when gotth-live became
	// candace/pkg/gotth, and the module the scratch copy builds against is
	// rooted there rather than here.
	exportRoot := filepath.Dir(filepath.Dir(root))

	// The watcher is built rather than `go run`: `go run` makes the process
	// this spec can signal the toolchain rather than the watcher, and the
	// watcher's own stop() then never runs, which leaks a counter holding a
	// port for the rest of the suite. run.go makes the same argument one
	// level down about the application it supervises.
	tmp := GinkgoT().TempDir()
	watcher := filepath.Join(tmp, "gotth-live-dev")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", watcher, "./internal/cmd/gotth-live-dev")
	build.Dir = root
	out, err := build.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "building internal/cmd/gotth-live-dev failed:\n%s", out)

	dir := filepath.Join(tmp, "counter")
	copySources(filepath.Join(exportRoot, "examples", "gotth", "counter"), dir)

	// The example is a package of the candace module rather than a module of
	// its own, so the scratch copy has no go.mod to inherit and is given one:
	// a throwaway module path, one requirement, and a replace pointing at the
	// checkout under test. That replace is what the example's own committed
	// go.mod used to carry, and it is still what makes the copy build against
	// this working tree instead of a published version.
	for _, args := range [][]string{
		{"mod", "init", "gotthlive.test/scratch/counter"},
		{"mod", "edit",
			"-require=github.com/candacelabs/csf@v0.0.0",
			"-replace=github.com/candacelabs/csf=" + exportRoot},
		{"mod", "tidy"},
	} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "go %s in the scratch module failed:\n%s",
			strings.Join(args, " "), out)
	}

	w := &devWatched{dir: dir, addr: freeAddr()}

	reader, writer, err := os.Pipe()
	Expect(err).NotTo(HaveOccurred())

	cmd := exec.Command(watcher,
		"-dir", dir,
		// Tighter than the defaults so a spec waits on the loop rather than on
		// the poll cadence. Both are the watcher's own flags, so this changes
		// nothing about what is being tested.
		"-interval", "100ms",
		"-settle", "200ms",
		// Everything after -- is the application's own argv. The counter
		// derives its Origin allowlist from -addr, so this is also what makes
		// the browser's Origin acceptable.
		"--", "-addr", w.addr)
	cmd.Stdout = writer
	cmd.Stderr = writer
	Expect(cmd.Start()).To(Succeed())
	Expect(writer.Close()).To(Succeed())

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		buf := make([]byte, 4096)
		var partial string
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				partial += string(buf[:n])
				for {
					idx := strings.IndexByte(partial, '\n')
					if idx < 0 {
						break
					}
					w.mu.Lock()
					w.lines = append(w.lines, partial[:idx])
					w.mu.Unlock()
					partial = partial[idx+1:]
				}
			}
			if err != nil {
				return
			}
		}
	}()

	DeferCleanup(func() {
		// SIGINT rather than Kill: the watcher's own deferred stop() is what
		// ends the counter it supervises, and a killed watcher leaves that
		// process alive and listening. Kill is the fallback, after the grace
		// period the watcher itself uses plus the application's shutdown.
		_ = cmd.Process.Signal(os.Interrupt)
		exited := make(chan struct{})
		go func() { _, _ = cmd.Process.Wait(); close(exited) }()
		select {
		case <-exited:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
		_ = reader.Close()
		<-drained
	})

	// The first cycle is `templ generate` then `go build` then start, so this
	// waits on a compile and not on a process spawn.
	//
	// The failure message is a func rather than format arguments, and that is
	// load-bearing: Gomega evaluates a description's arguments when the
	// assertion is CONSTRUCTED, so `..., "%s", w.transcript()` would print the
	// empty transcript from before the wait rather than the one that explains
	// the timeout.
	Eventually(w.identity, 180*time.Second, 200*time.Millisecond).ShouldNot(BeEmpty(), func() string {
		return "the watched counter never served a build identity; the watcher said:\n" + w.transcript()
	})

	return w
}

// copySources copies the example's source files into dst.
func copySources(src, dst string) {
	GinkgoHelper()

	Expect(os.MkdirAll(dst, 0o755)).To(Succeed())
	entries, err := os.ReadDir(src)
	Expect(err).NotTo(HaveOccurred())

	var copied []string
	for _, e := range entries {
		if e.IsDir() || !devSourceExts[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644)).To(Succeed())
		copied = append(copied, e.Name())
	}

	// Asserted rather than assumed: a rename in examples/gotth/counter must fail
	// here, naming what it could not find, rather than three minutes later as
	// a build error inside a watcher subprocess.
	// go.mod and go.sum are not on this list any more: the example is a
	// package of the candace module, and the scratch copy is given a module of
	// its own by the caller rather than inheriting one.
	for _, needed := range []string{"main.go", "counter.go", "view.templ", "counter.css"} {
		Expect(copied).To(ContainElement(needed),
			"examples/gotth/counter no longer contains %s, which these specs copy and edit", needed)
	}
}

// identity is the build identity the running application reports, which is the
// value the browser polls for. Empty means nothing answered.
func (w *devWatched) identity() string {
	req, err := http.NewRequest(http.MethodGet, "http://"+w.addr+devMount+"/gotth-live-dev-build", nil)
	if err != nil {
		return ""
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { Expect(resp.Body.Close()).To(Succeed()) }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// restarts counts the watcher's own "restarted" lines.
//
// It is the vacuity guard on the negative control below: "nothing reloaded" is
// worth nothing unless the process really was rebuilt and restarted underneath
// the page, and this is the watcher saying so in its own words.
func (w *devWatched) restarts() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, line := range w.lines {
		if strings.Contains(line, "gotth-live-dev: restarted") {
			n++
		}
	}
	return n
}

func (w *devWatched) transcript() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.lines, "\n")
}

// edit replaces exactly one occurrence of old in the named source file.
func (w *devWatched) edit(name, old, replacement string) {
	GinkgoHelper()

	path := filepath.Join(w.dir, name)
	before, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	Expect(strings.Count(string(before), old)).To(Equal(1),
		"%s does not contain exactly one %q, so this edit is not the edit it says it is", name, old)
	after := strings.Replace(string(before), old, replacement, 1)
	Expect(os.WriteFile(path, []byte(after), 0o644)).To(Succeed())
}

// touch moves a file's modification time without changing a byte of it.
//
// This is the negative control's instrument. The watcher compares size and
// modification time, so this is seen as a change and produces a real rebuild
// and a real restart; the executable it produces is byte-identical, so the
// build identity does not move and the page must not reload.
func (w *devWatched) touch(names ...string) {
	GinkgoHelper()
	when := time.Now()
	for _, name := range names {
		path := filepath.Join(w.dir, name)
		before, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Chtimes(path, when, when)).To(Succeed())
		after, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(Equal(before), "touching %s changed its bytes", name)
	}
}

// ---------------------------------------------------------------------------
// Reading the page across a reload
// ---------------------------------------------------------------------------

// devPage is everything these specs read off the document, in one round trip.
type devPage struct {
	Mark       string `json:"mark"`
	Heading    string `json:"heading"`
	Parity     string `json:"parity"`
	Build      string `json:"build"`
	Status     string `json:"status"`
	Visibility string `json:"visibility"`
}

const devMarkExpr = `(() => {
	const q = sel => document.querySelector(sel);
	const text = sel => { const n = q(sel); return n ? n.textContent.trim() : ""; };
	const tag = q("script[data-gotth-dev-build]");
	return {
		mark: String(window.__gotthDevMark || ""),
		heading: text("h1"),
		parity: text(".parity"),
		build: tag ? (tag.getAttribute("data-gotth-dev-build") || "") : "",
		status: document.documentElement.getAttribute("data-gotth-status") || "",
		visibility: document.visibilityState,
	};
})()`

// devRead reads the page, tolerating the one CDP failure a reload causes.
//
// evalJSON fails the spec when Runtime.evaluate returns an error, which is
// right for every other browser spec in this suite and wrong for exactly this
// one: a document that is reloading destroys its execution context and creates
// a new one, and an evaluate that lands in the gap between them comes back
// "Cannot find context with specified id". These specs POLL a page across a
// reload, so that gap is on the happy path, and a hard failure there would
// make the spec that proves the reload works fail because it worked.
//
// It is a new function in this file rather than a change to cdp_test.go's
// call(), because every existing caller wants the hard failure.
func devRead(c *chrome) (devPage, error) {
	var page devPage

	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}

	if err := c.callSoft(c.sessionID, "Runtime.evaluate", map[string]any{
		"expression":    devMarkExpr,
		"awaitPromise":  true,
		"returnByValue": true,
	}, &res); err != nil {
		return page, err
	}
	if res.ExceptionDetails != nil {
		return page, fmt.Errorf("the page threw while being read: %s", res.ExceptionDetails.Text)
	}
	if len(res.Result.Value) == 0 {
		return page, fmt.Errorf("the page returned no value")
	}
	if err := json.Unmarshal(res.Result.Value, &page); err != nil {
		return page, err
	}
	return page, nil
}

// callSoft is call() returning an error instead of failing the spec. See
// devRead for why one of the two exists.
func (c *chrome) callSoft(sessionID, method string, params any, out any) error {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}

	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan cdpReply, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	msg, err := json.Marshal(cdpFrame{ID: id, Method: method, Params: raw, SessionID: sessionID})
	if err != nil {
		return err
	}
	if err := c.conn.Write(c.ctx, websocket.MessageText, msg); err != nil {
		return err
	}

	select {
	case reply := <-ch:
		if reply.Err != nil {
			return reply.Err
		}
		if out != nil && len(reply.Result) > 0 {
			return json.Unmarshal(reply.Result, out)
		}
		return nil
	case <-time.After(60 * time.Second):
		return fmt.Errorf("cdp: %s timed out", method)
	}
}

// mark stamps the document so a later read can tell a reload from a resync.
func devMark(c *chrome, value string) {
	GinkgoHelper()
	c.evalJSON(`(() => { window.__gotthDevMark = `+jsStr(value)+`; return null; })()`, nil)
	page, err := devRead(c)
	Expect(err).NotTo(HaveOccurred())
	Expect(page.Mark).To(Equal(value), "the marker this spec depends on did not stick")
}

// devPollInterval is how often the two waits below look. A CDP round trip is
// not free, and neither wait is measuring latency at this resolution: the
// elapsed figure awaitReload reports is a reload that takes seconds.
const devPollInterval = 200 * time.Millisecond

// devLiveBudget is how long the reloaded document has to come back live. It is
// the second half of a reload the caller already waited for, on a browser
// sharing a VM with everything else — see devHost — so it is stated at a
// minute rather than at the couple of seconds it takes when the box is quiet.
var devLiveBudget = patience.Budget{Within: 60 * time.Second, Interval: devPollInterval}

// awaitReload waits for the document to be replaced, and reports how long it
// took. The marker being GONE is the reload; nothing else here is evidence of
// one.
//
// The timing loop is patience's; what is left here is the domain: a read that
// lands inside the reload is the event being waited for rather than a failure,
// so it reports the marker as still present and the wait continues.
func awaitReload(c *chrome, marker string, within time.Duration) (devPage, time.Duration) {
	GinkgoHelper()

	read := func() devPage {
		page, err := devRead(c)
		if err != nil {
			return devPage{Mark: marker}
		}
		return page
	}

	started := time.Now()
	patience.Await(GinkgoTB(),
		"the document to be replaced: the marker set before the edit to leave window",
		patience.Budget{Within: within, Interval: devPollInterval},
		read, func(page devPage) bool { return page.Mark != marker })
	elapsed := time.Since(started)

	// The reload is a fresh document, so wait for it to be live again before
	// anything reads what it renders.
	reloaded := patience.Await(GinkgoTB(),
		"the reloaded document to reach data-gotth-status=live",
		devLiveBudget,
		read, func(page devPage) bool { return page.Status == "live" })

	return reloaded, elapsed
}

// devHiddenVacuity is the precondition every spec here rests on, and the
// negative control rests on it hardest.
//
// client/dev-reload.js stops polling while document.hidden, by design — a
// background tab is not watching for a reload. A page this suite drove into
// the background would therefore never poll, never reload, and pass the
// negative control for a reason that has nothing to do with the build
// identity. So visibility is asserted rather than assumed.
const devHiddenVacuity = "the page is hidden, so client/dev-reload.js has stopped polling: " +
	"every result in this spec would then be about a tab that was not watching"

// devHost is the host state that belongs beside every duration below.
func devHost(c *chrome) string {
	return fmt.Sprintf("%s/%s, %d cpus, %s, %s — a shared VM, other load not controlled",
		runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version(), c.version)
}

// ---------------------------------------------------------------------------
// The specs
// ---------------------------------------------------------------------------

var _ = Describe("The dev-reload loop against examples/counter (FR-57)", Label("browser", "e2e", "devreload"), func() {

	// The case the feature exists for. The <h1> is in Page, outside every
	// live region, so no patch could ever carry it: a resync repaints regions
	// and this markup is not in one. If the browser is showing the new
	// heading, the document was replaced.
	It("reloads the page for a templ change to markup outside every live region", func() {
		e2eOnly()
		browserOnly()

		w := startWatchedCounter()
		baseline := w.identity()
		Expect(baseline).NotTo(BeEmpty())

		c := launchChrome()
		c.navigate("http://" + w.addr + "/")
		waitLive(c)

		before, err := devRead(c)
		Expect(err).NotTo(HaveOccurred())
		Expect(before.Visibility).To(Equal("visible"), devHiddenVacuity)
		Expect(before.Heading).To(Equal("gotth-live counter"))
		Expect(before.Build).To(Equal(baseline),
			"the stamp on the page is not the identity the server reports for the build that rendered it")
		devMark(c, "gen-0")

		w.edit("view.templ",
			"<h1>gotth-live counter</h1>",
			"<h1>gotth-live counter [TEMPL CHANGE]</h1>")

		after, elapsed := awaitReload(c, "gen-0", 180*time.Second)

		Expect(after.Heading).To(Equal("gotth-live counter [TEMPL CHANGE]"),
			"the document reloaded but is not showing the changed markup")
		Expect(after.Build).NotTo(Equal(baseline),
			"the page reloaded without the build identity moving, so something other than FR-57 reloaded it")
		Expect(w.identity()).To(Equal(after.Build),
			"the stamp on the reloaded document is not the identity of the build now running")

		AddReportEntry("FR-57 — a templ change outside every live region (OBSERVATION, not a gate)",
			fmt.Sprintf("reloaded after %s (templ generate + go build + restart + up to one poll)\n"+
				"build identity %s -> %s\nheading %q -> %q\nhost: %s\n"+
				"The duration is reported and asserted on by nothing: this is a shared host.",
				elapsed.Round(time.Millisecond), baseline, after.Build,
				before.Heading, after.Heading, devHost(c)))
	})

	It("reloads the page for a Go change", func() {
		e2eOnly()
		browserOnly()

		w := startWatchedCounter()
		baseline := w.identity()

		c := launchChrome()
		c.navigate("http://" + w.addr + "/")
		waitLive(c)

		before, err := devRead(c)
		Expect(err).NotTo(HaveOccurred())
		devMark(c, "gen-1")

		// A pure-Go edit: no .templ moves, so the watcher's templ step does
		// not run and this is the go-build-only path through cycle().
		w.edit("counter.go", `return "even"`, `return "even [GO CHANGE]"`)

		after, elapsed := awaitReload(c, "gen-1", 180*time.Second)

		Expect(after.Parity).To(ContainSubstring("[GO CHANGE]"),
			"the document reloaded but is not rendering the changed Go")
		Expect(after.Build).NotTo(Equal(baseline))

		AddReportEntry("FR-57 — a Go change (OBSERVATION, not a gate)",
			fmt.Sprintf("reloaded after %s (go build + restart + up to one poll)\n"+
				"build identity %s -> %s\nparity %q -> %q\nhost: %s",
				elapsed.Round(time.Millisecond), baseline, after.Build,
				before.Parity, after.Parity, devHost(c)))
	})

	// The negative control, and the one that matters.
	//
	// Every assertion above is satisfied by a client that reloads on a timer.
	// This is the spec such a client fails: the process is genuinely rebuilt
	// and restarted, the socket genuinely drops, and NOTHING reloads — because
	// the build identity is a hash of the executable and the executable did
	// not change. That is the whole of FR-57's "without losing the session
	// where state permits", and it is the half a stopwatch cannot check.
	It("restarts into an identical build without reloading anything", func() {
		e2eOnly()
		browserOnly()

		w := startWatchedCounter()
		baseline := w.identity()

		c := launchChrome()
		c.navigate("http://" + w.addr + "/")
		waitLive(c)
		devMark(c, "gen-2")

		restartsBefore := w.restarts()

		// Modification times only. Both files, because the watcher's first
		// cycle after a .templ moves also runs `templ generate`, and a
		// regeneration that produced different bytes would be a real change
		// this control has to be exposed to rather than protected from.
		w.touch("counter.go", "view.templ")

		Eventually(w.restarts, 180*time.Second, 200*time.Millisecond).
			Should(BeNumerically(">", restartsBefore), func() string {
				return "the watcher never rebuilt and restarted, so this control asserts nothing:\n" +
					w.transcript()
			})

		// The watcher reports restarted after spawning, before the new process
		// necessarily listens. An empty response is unready, not a build change.
		restartReadyBudget := patience.Budget{Within: 180 * time.Second, Interval: 200 * time.Millisecond}
		identity := patience.Await(GinkgoTB(), "restarted counter serves its build identity", restartReadyBudget,
			w.identity, func(value string) bool { return value != "" })
		Expect(identity).To(Equal(baseline),
			"a rebuild that changed no source bytes produced a different executable, so the build "+
				"identity is not a function of the build")

		// The client polls at 1 s when linked and at 250 ms while the server
		// is not answering. Ten seconds is dozens of polls across the restart,
		// and every one of them must decide "linked".
		Consistently(func() string {
			page, err := devRead(c)
			if err != nil {
				return "gen-2"
			}
			return page.Mark
		}, 10*time.Second, 500*time.Millisecond).Should(Equal("gen-2"),
			"the document reloaded across a restart that changed no bytes")

		page, err := devRead(c)
		Expect(err).NotTo(HaveOccurred())
		Expect(page.Mark).To(Equal("gen-2"))
		Expect(page.Build).To(Equal(baseline))
		Expect(page.Visibility).To(Equal("visible"), devHiddenVacuity)

		// The socket, which is the other half of the claim. waitLive proves it
		// the only way it can be proved: a real click that moves the number.
		Eventually(func() string {
			page, err := devRead(c)
			if err != nil {
				return ""
			}
			return page.Status
		}, 60*time.Second, 200*time.Millisecond).Should(Equal("live"),
			"the runtime never reconnected after the restart")
		waitLive(c)

		AddReportEntry("FR-57 — the negative control: a rebuild that changed no bytes",
			fmt.Sprintf("watcher restarts observed: %d -> %d\nbuild identity unchanged: %s\n"+
				"marker set before the restart: still %q\nreconnected and clicking again: yes\nhost: %s\n\n"+
				"watcher transcript:\n%s",
				restartsBefore, w.restarts(), baseline, page.Mark, devHost(c), w.transcript()))
	})
})
