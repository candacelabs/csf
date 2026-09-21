package e2e

// Process-level contract tests for the warden binary: flag handling, exit
// behaviour on invalid config, clean SIGTERM shutdown within a bounded time,
// and the log-output format (JSON vs console) for BOTH the main wiring lines
// and subsystem (election) lines. These run the REAL compiled binary — the
// highest-fidelity contract there is.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/patience"
)

func TestCLIContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "warden CLI/process contract suite")
}

// wardenBin is the compiled binary, built once for the whole suite.
var wardenBin string

var _ = BeforeSuite(func() {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	Expect(err).NotTo(HaveOccurred())
	wardenBin = filepath.Join(GinkgoT().TempDir(), "warden")
	build := exec.Command("go", "build", "-o", wardenBin, "github.com/candacelabs/csf/app/warden/cmd")
	build.Dir = moduleRoot
	out, err := build.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "building warden: %s", out)
})

// freePort returns a currently-free localhost TCP port as a string.
func freePort() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	Expect(err).NotTo(HaveOccurred())
	return port
}

// proc wraps a running warden process with a thread-safe capture of its merged
// stdout+stderr.
type proc struct {
	cmd     *exec.Cmd
	mu      sync.Mutex
	buf     bytes.Buffer
	done    chan struct{}
	waitErr error
}

// soloEnv returns env for a single-node cluster (quorum 1) that becomes leader
// promptly, with fast timings so "became leader" appears within a second.
func soloEnv(port, logFormat string) []string {
	addr := "127.0.0.1:" + port
	env := []string{
		"WARDEN_NODE_ID=solo",
		"WARDEN_BIND=" + addr,
		"WARDEN_PEERS=solo=" + addr,
		"WARDEN_DATA_DIR=" + GinkgoT().TempDir(),
		"WARDEN_NOTIFY_MODE=log",
		"WARDEN_HEARTBEAT_INTERVAL=100ms",
		"WARDEN_SUSPECT_AFTER=500ms",
		"WARDEN_DEAD_AFTER=1s",
		"WARDEN_ELECTION_TIMEOUT_MIN=150ms",
		"WARDEN_ELECTION_TIMEOUT_MAX=300ms",
		"WARDEN_RPC_TIMEOUT=100ms",
		"WARDEN_LOG_LEVEL=info",
	}
	if logFormat != "" {
		env = append(env, "WARDEN_LOG_FORMAT="+logFormat)
	}
	return env
}

func startProc(env []string, args ...string) *proc {
	cmd := exec.Command(wardenBin, args...)
	cmd.Env = env
	p := &proc{cmd: cmd, done: make(chan struct{})}
	cmd.Stdout = &syncWriter{p: p}
	cmd.Stderr = &syncWriter{p: p}
	Expect(cmd.Start()).To(Succeed())
	go func() { p.waitErr = cmd.Wait(); close(p.done) }()
	return p
}

type syncWriter struct{ p *proc }

func (w *syncWriter) Write(b []byte) (int, error) {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	return w.p.buf.Write(b)
}

func (p *proc) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buf.String()
}

func (p *proc) signal(sig syscall.Signal) { _ = p.cmd.Process.Signal(sig) }

// waitExit blocks up to d for the process to exit and returns its exit code.
func (p *proc) waitExit(d time.Duration) (int, bool) {
	select {
	case <-p.done:
		if p.waitErr == nil {
			return 0, true
		}
		if ee, ok := p.waitErr.(*exec.ExitError); ok {
			return ee.ExitCode(), true
		}
		return -1, true
	case <-time.After(d):
		return 0, false
	}
}

func (p *proc) kill() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// bootBudget is how long a spec waits for one line to appear in a warden
// process's merged output.
//
// It used to be "5s", written inline in the one helper below, and that number
// is why this suite flaked. What is being waited for is a real process
// starting, binding a port, loading a config and winning an election, on a
// host that may be running anything else at the time — so five seconds of wall
// clock was a bet on the machine rather than a statement about the binary. A
// minute costs a slower failure on a run that was going to fail; twenty
// seconds too few costs a red build on a correct one.
var bootBudget = patience.Budget{Within: time.Minute, Interval: 20 * time.Millisecond}

// awaitOutput waits until the captured output contains sub, failing with
// everything the process did print instead.
func (p *proc) awaitOutput(sub string) {
	patience.Await(GinkgoTB(), fmt.Sprintf("the process to log %q", sub), bootBudget,
		p.output, func(output string) bool { return strings.Contains(output, sub) })
}

var _ = Describe("warden -version", func() {
	It("prints the version and exits 0 without starting the daemon", func() {
		out, err := exec.Command(wardenBin, "-version").CombinedOutput()
		Expect(err).NotTo(HaveOccurred())
		// Built without -ldflags, so the version is the default "dev".
		Expect(strings.TrimSpace(string(out))).To(Equal("dev"))
	})
})

var _ = Describe("invalid configuration", func() {
	It("exits non-zero and reports the validation error", func() {
		// node_id not present in the supplied peer set fails Validate.
		p := startProc([]string{
			"WARDEN_NODE_ID=ghost",
			"WARDEN_PEERS=node-a=203.0.113.11:7717,node-b=203.0.113.12:7717",
		})
		DeferCleanup(p.kill)
		code, exited := p.waitExit(10 * time.Second)
		Expect(exited).To(BeTrue(), "process did not exit; output:\n%s", p.output())
		Expect(code).NotTo(Equal(0))
		Expect(p.output()).To(ContainSubstring("not present in peers"))
	})

	It("exits non-zero on a malformed peer list", func() {
		p := startProc([]string{"WARDEN_NODE_ID=solo", "WARDEN_PEERS=solo"}) // missing '='
		DeferCleanup(p.kill)
		code, exited := p.waitExit(10 * time.Second)
		Expect(exited).To(BeTrue())
		Expect(code).NotTo(Equal(0))
	})
})

var _ = Describe("-config file loading", func() {
	It("boots from a YAML config file passed via -config (no env overrides)", func() {
		port := freePort()
		addr := "127.0.0.1:" + port
		dataDir := GinkgoT().TempDir()
		yaml := "" +
			"node_id: solo\n" +
			"bind: \"" + addr + "\"\n" +
			"data_dir: " + dataDir + "\n" +
			"log_level: info\n" +
			"peers:\n" +
			"  - id: solo\n" +
			"    addr: " + addr + "\n" +
			"timing:\n" +
			"  heartbeat_interval: 100ms\n" +
			"  suspect_after: 500ms\n" +
			"  dead_after: 1s\n" +
			"  election_timeout_min: 150ms\n" +
			"  election_timeout_max: 300ms\n" +
			"  rpc_timeout: 100ms\n" +
			"notify:\n" +
			"  mode: log\n"
		cfgPath := filepath.Join(GinkgoT().TempDir(), "warden.yaml")
		Expect(os.WriteFile(cfgPath, []byte(yaml), 0o644)).To(Succeed())

		// Empty env => the config values can ONLY have come from the -config file.
		p := startProc([]string{}, "-config", cfgPath)
		DeferCleanup(p.kill)
		p.awaitOutput("starting warden")
		p.awaitOutput("became leader")
		// The bind address from the file took effect.
		Expect(p.output()).To(ContainSubstring(addr))

		p.signal(syscall.SIGTERM)
		code, exited := p.waitExit(10 * time.Second)
		Expect(exited).To(BeTrue())
		Expect(code).To(Equal(0))
	})
})

var _ = Describe("clean shutdown", func() {
	It("shuts down cleanly (exit 0) within the grace window on SIGTERM", func() {
		p := startProc(soloEnv(freePort(), ""))
		DeferCleanup(p.kill)
		p.awaitOutput("starting warden")
		p.awaitOutput("became leader") // fully up as leader

		p.signal(syscall.SIGTERM)
		// shutdownGrace is 5s; the whole teardown must comfortably beat 10s.
		code, exited := p.waitExit(10 * time.Second)
		Expect(exited).To(BeTrue(), "did not exit after SIGTERM; output:\n%s", p.output())
		Expect(code).To(Equal(0))
		Expect(p.output()).To(ContainSubstring("shut down cleanly"))
	})

	It("also shuts down cleanly on SIGINT", func() {
		p := startProc(soloEnv(freePort(), ""))
		DeferCleanup(p.kill)
		p.awaitOutput("starting warden")
		p.signal(syscall.SIGINT)
		code, exited := p.waitExit(10 * time.Second)
		Expect(exited).To(BeTrue())
		Expect(code).To(Equal(0))
	})
})

var _ = Describe("log output format", func() {
	// jsonLineFor returns the first captured line whose parsed JSON "message"
	// field contains want, or "" if none.
	jsonLineFor := func(output, want string) map[string]any {
		for _, line := range strings.Split(output, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) != nil {
				continue
			}
			if msg, ok := m["message"].(string); ok && strings.Contains(msg, want) {
				return m
			}
		}
		return nil
	}

	It("emits structured JSON by default for BOTH main and subsystem lines", func() {
		p := startProc(soloEnv(freePort(), "")) // default => JSON
		DeferCleanup(p.kill)
		p.awaitOutput("starting warden")
		p.awaitOutput("became leader")
		p.signal(syscall.SIGTERM)
		p.waitExit(10 * time.Second)

		out := p.output()
		// Main wiring line.
		main := jsonLineFor(out, "starting warden")
		Expect(main).NotTo(BeNil(), "no JSON 'starting warden' line in:\n%s", out)
		Expect(main).To(HaveKey("level"))
		Expect(main["node_id"]).To(Equal("solo"))
		// Subsystem (election) line, proving the shared logger format applies.
		sub := jsonLineFor(out, "became leader")
		Expect(sub).NotTo(BeNil(), "no JSON 'became leader' line in:\n%s", out)
		Expect(sub).To(HaveKey("level"))
	})

	It("emits human-readable console lines when WARDEN_LOG_FORMAT=console", func() {
		p := startProc(soloEnv(freePort(), "console"))
		DeferCleanup(p.kill)
		p.awaitOutput("starting warden")
		p.awaitOutput("became leader")
		p.signal(syscall.SIGTERM)
		p.waitExit(10 * time.Second)

		out := p.output()
		Expect(out).To(ContainSubstring("starting warden")) // main line
		Expect(out).To(ContainSubstring("became leader"))   // subsystem line
		// Console lines are NOT JSON objects.
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "starting warden") || strings.Contains(line, "became leader") {
				Expect(strings.TrimSpace(line)).NotTo(HavePrefix("{"),
					"expected console format, got a JSON line: %s", line)
			}
		}
	})
})
