package conformance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ---------------------------------------------------------------------------
// A minimal Chrome DevTools Protocol client.
//
// Written rather than imported, and the reason is FR-74 rather than pride:
// every browser-automation library on offer arrives through npm with a
// lockfile and a post-install download, and the one property the bench
// quarantine exists to protect is that none of that can reach a consumer. This
// speaks CDP over the WebSocket library the module already depends on, so the
// browser evidence costs zero new dependencies in any go.mod and zero npm
// anywhere.
//
// It implements exactly what the three browser criteria need: launch, attach
// to a page, evaluate JavaScript, and read back a JSON value. It is not a
// general automation library and should not grow into one.
// ---------------------------------------------------------------------------

// browserOnly skips a spec unless a browser is available. CHROME_BIN is set by
// .dis/Dockerfile.bench; the library image deliberately has no browser, so
// these specs are invisible there rather than failing there.
func browserOnly() string {
	GinkgoHelper()
	bin := os.Getenv("CHROME_BIN")
	if bin == "" {
		Skip("browser: CHROME_BIN is unset — run in dis-gotth-live-bench:latest")
	}
	return bin
}

// chrome is a launched browser with one attached page.
type chrome struct {
	conn      *websocket.Conn
	ctx       context.Context
	sessionID string
	version   string

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan cdpReply
}

type cdpReply struct {
	Result json.RawMessage
	Err    *cdpError
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

func (e *cdpError) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("cdp error %d: %s: %s", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("cdp error %d: %s", e.Code, e.Message)
}

type cdpFrame struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

// launchChrome starts headless chromium and attaches to a fresh page.
func launchChrome() *chrome {
	GinkgoHelper()
	bin := browserOnly()

	// Chromium's profile directory is not managed by Ginkgo's TempDir, and the
	// reason is a race rather than a preference: the browser keeps writing to
	// it while it is being killed, so a cleanup that runs at the same instant
	// fails with "directory not empty" and turns a passing spec red. This
	// removes it after the process has actually exited.
	profile, err := os.MkdirTemp("", "qa-chrome-")
	Expect(err).NotTo(HaveOccurred())

	cmd := exec.Command(bin,
		"--headless=new",
		"--no-sandbox", // the container has no user namespaces; see Dockerfile.bench
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--no-first-run",
		"--no-default-browser-check",
		"--remote-debugging-port=0",
		"--user-data-dir="+profile,
		"about:blank",
	)
	Expect(cmd.Start()).To(Succeed())
	DeferCleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = os.RemoveAll(profile)
	})

	// Chromium writes the port it chose into the profile directory.
	portFile := filepath.Join(profile, "DevToolsActivePort")
	var port string
	Eventually(func() error {
		b, err := os.ReadFile(portFile)
		if err != nil {
			return err
		}
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		if len(lines) < 1 || lines[0] == "" {
			return fmt.Errorf("DevToolsActivePort is not written yet")
		}
		port = lines[0]
		return nil
	}, 60*time.Second, 100*time.Millisecond).Should(Succeed(), "chromium never opened a debugging port")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	DeferCleanup(cancel)

	base := "http://127.0.0.1:" + port
	var meta struct {
		Browser              string `json:"Browser"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	Eventually(func() error {
		resp, err := http.Get(base + "/json/version")
		if err != nil {
			return err
		}
		defer func() { Expect(resp.Body.Close()).To(Succeed()) }()
		return json.NewDecoder(resp.Body).Decode(&meta)
	}, 30*time.Second, 100*time.Millisecond).Should(Succeed())
	Expect(meta.WebSocketDebuggerURL).NotTo(BeEmpty())

	conn, _, err := websocket.Dial(ctx, meta.WebSocketDebuggerURL, nil)
	Expect(err).NotTo(HaveOccurred())
	conn.SetReadLimit(64 << 20)
	DeferCleanup(func() { _ = conn.CloseNow() })

	c := &chrome{
		conn: conn, ctx: ctx, version: meta.Browser,
		pending: map[int64]chan cdpReply{},
	}
	go c.pump()

	// One page, attached flat so every later command carries its session id.
	var created struct {
		TargetID string `json:"targetId"`
	}
	c.call("", "Target.createTarget", map[string]any{"url": "about:blank"}, &created)

	var attached struct {
		SessionID string `json:"sessionId"`
	}
	c.call("", "Target.attachToTarget",
		map[string]any{"targetId": created.TargetID, "flatten": true}, &attached)
	Expect(attached.SessionID).NotTo(BeEmpty())
	c.sessionID = attached.SessionID

	c.call(c.sessionID, "Page.enable", nil, nil)
	c.call(c.sessionID, "Runtime.enable", nil, nil)
	return c
}

func (c *chrome) pump() {
	for {
		_, data, err := c.conn.Read(c.ctx)
		if err != nil {
			return
		}
		var f cdpFrame
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		if f.ID == 0 {
			continue // an event; this client does not subscribe to any
		}
		c.mu.Lock()
		ch := c.pending[f.ID]
		delete(c.pending, f.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- cdpReply{Result: f.Result, Err: f.Error}
		}
	}
}

// call sends one command and decodes its result into out, which may be nil.
func (c *chrome) call(sessionID, method string, params any, out any) {
	GinkgoHelper()

	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		Expect(err).NotTo(HaveOccurred())
		raw = b
	}

	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan cdpReply, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	msg, err := json.Marshal(cdpFrame{ID: id, Method: method, Params: raw, SessionID: sessionID})
	Expect(err).NotTo(HaveOccurred())
	Expect(c.conn.Write(c.ctx, websocket.MessageText, msg)).To(Succeed())

	select {
	case reply := <-ch:
		Expect(reply.Err).To(BeNil(), "%s failed: %v", method, reply.Err)
		if out != nil && len(reply.Result) > 0 {
			Expect(json.Unmarshal(reply.Result, out)).To(Succeed())
		}
	case <-time.After(120 * time.Second):
		Fail(fmt.Sprintf("cdp: %s timed out", method))
	}
}

// navigate loads url and waits for the document to finish loading.
func (c *chrome) navigate(url string) {
	GinkgoHelper()
	c.call(c.sessionID, "Page.navigate", map[string]any{"url": url}, nil)

	Eventually(func() string {
		return c.evalString(`document.readyState`)
	}, 60*time.Second, 100*time.Millisecond).Should(Equal("complete"))
}

// onNewDocument installs a script that runs before any page script, on every
// document this page loads. It is how the CSP listener is in place before the
// runtime it is watching.
func (c *chrome) onNewDocument(source string) {
	GinkgoHelper()
	c.call(c.sessionID, "Page.addScriptToEvaluateOnNewDocument",
		map[string]any{"source": source}, nil)
}

// evalJSON evaluates an expression, awaiting a promise, and decodes the JSON
// value it produced into out.
func (c *chrome) evalJSON(expression string, out any) {
	GinkgoHelper()

	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}

	c.call(c.sessionID, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"awaitPromise":  true,
		"returnByValue": true,
	}, &res)

	if res.ExceptionDetails != nil {
		detail := res.ExceptionDetails.Text
		if res.ExceptionDetails.Exception != nil {
			detail = res.ExceptionDetails.Exception.Description
		}
		Fail("page threw while evaluating: " + detail)
	}
	if out != nil && len(res.Result.Value) > 0 {
		Expect(json.Unmarshal(res.Result.Value, out)).To(Succeed())
	}
}

func (c *chrome) evalString(expression string) string {
	GinkgoHelper()
	var s string
	c.evalJSON(expression, &s)
	return s
}

func (c *chrome) evalBool(expression string) bool {
	GinkgoHelper()
	var b bool
	c.evalJSON(expression, &b)
	return b
}
