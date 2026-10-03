// Copyright 2026 Candace Labs

package livetest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/candacelabs/csf/ipc/proc"
)

// BrowserOptions configures a launch.
type BrowserOptions struct {
	// Executable is the Chromium binary. The bench image names it in
	// CHROME_BIN; a spec reads that variable and skips when it is unset,
	// because the library image deliberately has no browser.
	Executable string

	// Profile is a directory the browser may write its profile into. The
	// caller owns it: a spec passes tb.TempDir(), and the directory is
	// removed after the process has exited rather than while it still writes.
	Profile string

	// Timeout bounds the whole browser session. It defaults to five minutes.
	Timeout time.Duration
}

// Browser is one headless Chromium with one attached page, driven over the
// Chrome DevTools Protocol by the WebSocket library this module already
// depends on.
//
// It is written rather than imported for the reason FR-74 states: every
// browser-automation library on offer arrives through npm with a lockfile and
// a post-install download, and the bench quarantine exists so that none of
// that reaches a consumer. It implements what a browser spec needs — launch,
// attach to a page, evaluate JavaScript, read back a JSON value, take a
// screenshot — and is not a general automation library.
//
// The browser starts through the process capability, so a spec that holds a
// gomock launcher proves the same crossing a binary would make, and the real
// one starts the child in its own process group and kills the group on Kill.
type Browser struct {
	tb        testing.TB
	conn      *websocket.Conn
	ctx       context.Context
	sessionID string
	version   string

	// mu is a leaf lock over pending: one map, single-step operations, held
	// across no call that could block.
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

// cdpFrame is one protocol message either way. Params is any because it is
// whatever a command takes, encoded once with the frame around it.
type cdpFrame struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    any             `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

const (
	// devToolsPrefix is the line Chromium writes to standard error once its
	// debugging endpoint is open. Reading it there rather than from the
	// DevToolsActivePort file in the profile keeps the launch to one crossing,
	// the process, and no file read.
	devToolsPrefix = "DevTools listening on "

	cdpCallTimeout     = 120 * time.Second
	browserTimeout     = 5 * time.Minute
	browserStartWait   = 60 * time.Second
	browserReadLimit   = 64 << 20
	documentReadyState = "complete"
	readyPoll          = 100 * time.Millisecond

	methodCreateTarget   = "Target.createTarget"
	methodAttachToTarget = "Target.attachToTarget"
	methodPageEnable     = "Page.enable"
	methodRuntimeEnable  = "Runtime.enable"
	methodNavigate       = "Page.navigate"
	methodOnNewDocument  = "Page.addScriptToEvaluateOnNewDocument"
	methodEvaluate       = "Runtime.evaluate"
	methodScreenshot     = "Page.captureScreenshot"
	methodGetVersion     = "Browser.getVersion"
	blankPage            = "about:blank"
)

// endpointWriter is the browser's standard error: it keeps the diagnostics
// and hands the debugging endpoint, once, to whoever launched it. It is
// written by the one copying goroutine the process capability owns, so it
// needs no lock of its own.
type endpointWriter struct {
	diagnostics bytes.Buffer
	endpoint    chan string
	found       bool
}

func (writer *endpointWriter) Write(chunk []byte) (int, error) {
	writer.diagnostics.Write(chunk)
	if !writer.found {
		scanner := bufio.NewScanner(bytes.NewReader(writer.diagnostics.Bytes()))
		for scanner.Scan() {
			if url, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), devToolsPrefix); ok {
				writer.found = true
				writer.endpoint <- url
				break
			}
		}
	}
	return len(chunk), nil
}

// LaunchBrowser starts headless Chromium through launcher and attaches to a
// fresh page. Everything it starts is released through tb.Cleanup: the page's
// connection, then the process, then the profile the caller owns.
func LaunchBrowser(tb testing.TB, launcher proc.ILauncher, options BrowserOptions) *Browser {
	tb.Helper()
	switch {
	case launcher == nil:
		tb.Fatalf("livetest.LaunchBrowser: the launcher is nil. Pass the process capability the binary would grant.")
		return nil
	case options.Executable == "":
		tb.Fatalf("livetest.LaunchBrowser: BrowserOptions.Executable is empty. Read CHROME_BIN and skip when it is unset.")
		return nil
	case options.Profile == "":
		tb.Fatalf("livetest.LaunchBrowser: BrowserOptions.Profile is empty. Pass a directory the browser may write, such as tb.TempDir().")
		return nil
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = browserTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	stderr := &endpointWriter{endpoint: make(chan string, 1)}
	process, err := launcher.Start(ctx, proc.Command{
		Executable: options.Executable,
		Arguments: []string{
			"--headless=new",
			"--no-sandbox", // the container has no user namespaces; see Dockerfile.bench
			"--disable-gpu",
			"--disable-dev-shm-usage",
			"--no-first-run",
			"--no-default-browser-check",
			// Direct connections only: in a container with no desktop bus,
			// proxy auto-detection stalls the first request for seconds and
			// the stall lands in every timing a spec takes.
			"--no-proxy-server",
			"--remote-debugging-port=0",
			"--user-data-dir=" + options.Profile,
			blankPage,
		},
		Stdout: io.Discard,
		Stderr: stderr,
	})
	if err != nil {
		cancel()
		tb.Fatalf("livetest.LaunchBrowser: %v", err)
		return nil
	}
	// Cleanup runs last-registered first: the connection closes before the
	// process is killed, and the process is reaped before the caller's
	// profile directory goes.
	tb.Cleanup(func() {
		cancel()
		_ = process.Kill()
		_, _ = process.Wait()
	})

	var endpoint string
	select {
	case endpoint = <-stderr.endpoint:
	case <-time.After(browserStartWait):
		tb.Fatalf("livetest.LaunchBrowser: chromium never announced its debugging endpoint; standard error so far:\n%s", stderr.diagnostics.String())
		return nil
	}

	conn, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		tb.Fatalf("livetest.LaunchBrowser: dialling %s: %v", endpoint, err)
		return nil
	}
	conn.SetReadLimit(browserReadLimit)
	tb.Cleanup(func() { _ = conn.CloseNow() })

	browser := &Browser{tb: tb, conn: conn, ctx: ctx, pending: map[int64]chan cdpReply{}}
	// The pump exits when the connection closes, which the cleanup above
	// forces; the context bounds its reads.
	go browser.pump()

	var version struct {
		Product string `json:"product"`
	}
	browser.call("", methodGetVersion, nil, &version)
	browser.version = version.Product

	// One page, attached flat so every later command carries its session id.
	var created struct {
		TargetID string `json:"targetId"`
	}
	browser.call("", methodCreateTarget, map[string]string{"url": blankPage}, &created)
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	browser.call("", methodAttachToTarget, map[string]any{"targetId": created.TargetID, "flatten": true}, &attached)
	if attached.SessionID == "" {
		tb.Fatalf("livetest.LaunchBrowser: attaching to the page returned no session")
		return nil
	}
	browser.sessionID = attached.SessionID
	browser.call(browser.sessionID, methodPageEnable, nil, nil)
	browser.call(browser.sessionID, methodRuntimeEnable, nil, nil)
	return browser
}

// Version is the browser's product string, for a report entry.
func (browser *Browser) Version() string { return browser.version }

// Call sends one protocol command to the attached page and decodes its result
// into out, which may be nil, failing the spec on any error. It is the escape
// hatch for what the typed methods do not cover — synthesised input, most
// often — and params and out are any because the protocol's envelope is.
func (browser *Browser) Call(method string, params any, out any) {
	browser.tb.Helper()
	browser.call(browser.sessionID, method, params, out)
}

func (browser *Browser) pump() {
	for {
		_, data, err := browser.conn.Read(browser.ctx)
		if err != nil {
			return
		}
		var frame cdpFrame
		if json.Unmarshal(data, &frame) != nil || frame.ID == 0 {
			continue // an event; this client subscribes to none
		}
		browser.mu.Lock()
		reply := browser.pending[frame.ID]
		delete(browser.pending, frame.ID)
		browser.mu.Unlock()
		if reply != nil {
			reply <- cdpReply{Result: frame.Result, Err: frame.Error}
		}
	}
}

// call sends one command and decodes its result into out, failing the spec
// on any error; try is the same call handing the error back.
func (browser *Browser) call(sessionID string, method string, params any, out any) {
	browser.tb.Helper()
	if err := browser.try(sessionID, method, params, out); err != nil {
		browser.tb.Fatalf("livetest.Browser: %v", err)
	}
}

// try sends one command and decodes its result into out, which may be nil.
// params and out are any because they mirror the protocol's JSON envelope,
// which this client does not type. Every error names the command, because
// the command is what a reader of a spec's failure can act on.
func (browser *Browser) try(sessionID string, method string, params any, out any) error {
	browser.mu.Lock()
	browser.nextID++
	id := browser.nextID
	reply := make(chan cdpReply, 1)
	browser.pending[id] = reply
	browser.mu.Unlock()

	message, err := json.Marshal(cdpFrame{ID: id, Method: method, Params: params, SessionID: sessionID})
	if err != nil {
		return fmt.Errorf("encode the %s command: %w: the parameters a caller passed do not marshal, so pass a map or a struct with exported fields", method, err)
	}
	if err := browser.conn.Write(browser.ctx, websocket.MessageText, message); err != nil {
		return fmt.Errorf("send %s: %w: the DevTools connection is gone, so the browser exited or the session's timeout ended it; read the browser's standard error in the launch failure if there was one", method, err)
	}
	select {
	case answer := <-reply:
		if answer.Err != nil {
			return fmt.Errorf("%s failed: %w: the browser refused the command, so check its parameters against the DevTools protocol and, for a page command, that the page still exists", method, answer.Err)
		}
		if out != nil && len(answer.Result) > 0 {
			if err := json.Unmarshal(answer.Result, out); err != nil {
				return fmt.Errorf("decode the result of %s: %w: pass out as a pointer to a value shaped like the command's result, or nil to ignore it", method, err)
			}
		}
		return nil
	case <-time.After(cdpCallTimeout):
		return fmt.Errorf("%s did not answer within %s: the browser hung or exited, so read its standard error in the launch failure if there was one, and the page's console if it is a page command", method, cdpCallTimeout)
	}
}

// Navigate loads url and returns once the document has finished loading.
func (browser *Browser) Navigate(url string) {
	browser.tb.Helper()
	browser.call(browser.sessionID, methodNavigate, map[string]string{"url": url}, nil)
	deadline := time.Now().Add(browserStartWait)
	for browser.EvalString(`document.readyState`) != documentReadyState {
		if time.Now().After(deadline) {
			browser.tb.Fatalf("livetest.Browser: %s never finished loading", url)
			return
		}
		// Pacing between polls of the document's own ready state; the
		// deadline above bounds it.
		time.Sleep(readyPoll)
	}
}

// OnNewDocument installs a script that runs before any page script, on every
// document this page loads: how a listener is in place before the runtime it
// is watching.
func (browser *Browser) OnNewDocument(source string) {
	browser.tb.Helper()
	browser.call(browser.sessionID, methodOnNewDocument, map[string]string{"source": source}, nil)
}

// EvalJSON evaluates expression in the page, awaiting a promise, and decodes
// the JSON value it produced into out, failing the spec on any error. out is
// any for the reason json.Unmarshal's is: the page decides the shape.
func (browser *Browser) EvalJSON(expression string, out any) {
	browser.tb.Helper()
	if err := browser.TryEvalJSON(expression, out); err != nil {
		browser.tb.Fatalf("livetest.Browser: %v", err)
	}
}

// TryEvalJSON is EvalJSON handing the error back instead of failing the
// spec: a protocol error, an exception the page threw, or a value that does
// not decode. A spec polling a page across a reload needs it, because an
// evaluate that lands between the old execution context and the new one is
// refused, and that refusal is on that spec's happy path.
func (browser *Browser) TryEvalJSON(expression string, out any) error {
	var result struct {
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
	if err := browser.try(browser.sessionID, methodEvaluate, map[string]any{
		"expression":    expression,
		"awaitPromise":  true,
		"returnByValue": true,
	}, &result); err != nil {
		return err
	}
	if result.ExceptionDetails != nil {
		detail := result.ExceptionDetails.Text
		if result.ExceptionDetails.Exception != nil {
			detail = result.ExceptionDetails.Exception.Description
		}
		return fmt.Errorf("the page threw while evaluating: %s: fix the expression, or read through TryEvalJSON if throwing is on the spec's own path, as it is across a reload", detail)
	}
	if out == nil {
		return nil
	}
	if len(result.Result.Value) == 0 {
		return errors.New("the page returned no value for the expression: return a JSON-serialisable value from it, or pass out as nil to evaluate for effect")
	}
	if err := json.Unmarshal(result.Result.Value, out); err != nil {
		return fmt.Errorf("decode the page's value: %w: the expression's value does not fit out, so shape out like the value or return a different one", err)
	}
	return nil
}

// EvalString evaluates an expression producing a string.
func (browser *Browser) EvalString(expression string) string {
	browser.tb.Helper()
	var value string
	browser.EvalJSON(expression, &value)
	return value
}

// EvalBool evaluates an expression producing a boolean.
func (browser *Browser) EvalBool(expression string) bool {
	browser.tb.Helper()
	var value bool
	browser.EvalJSON(expression, &value)
	return value
}

// Screenshot captures the page as PNG bytes, for evidence a report keeps.
func (browser *Browser) Screenshot() []byte {
	browser.tb.Helper()
	var result struct {
		Data string `json:"data"`
	}
	browser.call(browser.sessionID, methodScreenshot, map[string]string{"format": "png"}, &result)
	image, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		browser.tb.Fatalf("livetest.Browser: decode the screenshot: %v", err)
		return nil
	}
	return image
}

// JSString renders a Go string as a JavaScript string literal, through the
// JSON encoder, so a selector containing double quotes stays one literal.
func JSString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
