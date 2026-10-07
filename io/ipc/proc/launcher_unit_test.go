// Copyright 2026 Candace Labs

// The unit specs drive real children and read /proc to prove they are gone.

//go:build linux

package proc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"

	"github.com/candacelabs/csf/pkg/eventually"
)

const (
	shell         = "/bin/sh"
	shellCommand  = "-c"
	probeVariable = "CSF_PROC_PROBE"
	pidFile       = "grandchild.pid"
	zombieState   = "Z"
)

// Budgets for a real child process on a loaded machine: spawning a shell and
// having it write a file, and the kernel tearing down a killed group.
var (
	spawnBudget  = eventually.Budget{Within: 30 * time.Second, Interval: 20 * time.Millisecond}
	reaperBudget = eventually.Budget{Within: 30 * time.Second, Interval: 20 * time.Millisecond}
)

// running reports whether pid names a live, non-zombie process.
func running(pid int) bool {
	body, err := os.ReadFile(filepath.Join(procDirectory, strconv.Itoa(pid), procStatFilename))
	if err != nil {
		return false
	}
	closeName := strings.LastIndexByte(string(body), ')')
	fields := strings.Fields(string(body[closeName+1:]))
	return len(fields) > 0 && fields[0] != zombieState
}

// readPid polls a file the child writes its background job's pid into.
func readPid(path string) int {
	return eventually.Await(GinkgoTB(), "the child to record its background job's pid", spawnBudget,
		func() int {
			body, err := os.ReadFile(path)
			if err != nil {
				return 0
			}
			pid, _ := strconv.Atoi(strings.TrimSpace(string(body)))
			return pid
		},
		func(pid int) bool { return pid > 0 })
}

func newLauncher(options ...HostLauncherOption) *HostLauncher {
	GinkgoHelper()
	launcher, err := NewHostLauncher(options...)
	Expect(err).NotTo(HaveOccurred())
	return launcher
}

var _ = Describe("HostLauncher", func() {
	var baseline goleak.Option

	BeforeEach(func() { baseline = goleak.IgnoreCurrent() })
	AfterEach(func() {
		Expect(goleak.Find(baseline)).To(Succeed(), "every goroutine a child needed must be joined by its Wait")
	})

	It("runs an argument vector and returns its captured output as a structured result", func(ctx SpecContext) {
		launcher := newLauncher()
		var _ ILauncher = launcher

		result, err := launcher.Run(ctx, Command{
			Executable: shell, Arguments: []string{shellCommand, "printf out; printf err >&2"},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(Result{ExitCode: 0, Stdout: []byte("out"), Stderr: []byte("err")}))
	})

	It("reports an unsuccessful exit as an ExitError carrying the status and diagnostics", func(ctx SpecContext) {
		result, err := newLauncher().Run(ctx, Command{
			Executable: shell, Arguments: []string{shellCommand, "echo broken >&2; exit 3"},
		})

		var exitError *ExitError
		Expect(errors.As(err, &exitError)).To(BeTrue(), "got %v", err)
		Expect(exitError.Code).To(Equal(3))
		Expect(string(exitError.Stderr)).To(Equal("broken\n"))
		Expect(err.Error()).To(ContainSubstring("broken"))
		Expect(result.ExitCode).To(Equal(3))
	})

	It("feeds standard input, writes to caller writers and extends the inherited environment", func(ctx SpecContext) {
		var stdout, stderr bytes.Buffer
		result, err := newLauncher().Run(ctx, Command{
			Executable:       shell,
			Arguments:        []string{shellCommand, `cat; printf %s "$` + probeVariable + `" >&2`},
			Directory:        GinkgoT().TempDir(),
			ExtraEnvironment: []string{probeVariable + "=granted"},
			Stdin:            strings.NewReader("piped"),
			Stdout:           &stdout,
			Stderr:           &stderr,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(stdout.String()).To(Equal("piped"))
		Expect(stderr.String()).To(Equal("granted"))
		Expect(result.Stdout).To(BeNil(), "output a caller asked for is not captured twice")
		Expect(result.Stderr).To(BeNil())
	})

	It("replaces the environment when one is given", func(ctx SpecContext) {
		result, err := newLauncher().Run(ctx, Command{
			Executable:  shell,
			Arguments:   []string{shellCommand, `printf %s "${HOME:-unset}"`},
			Environment: []string{},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(string(result.Stdout)).To(Equal("unset"))
	})

	It("bounds captured diagnostics and says so", func(ctx SpecContext) {
		result, err := newLauncher(WithDiagnosticBytes(4)).Run(ctx, Command{
			Executable: shell, Arguments: []string{shellCommand, "printf abcdefgh >&2"},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(string(result.Stderr)).To(Equal("abcd"))
		Expect(result.StderrTruncated).To(BeTrue())
	})

	It("kills the whole process group on cancellation and leaves no zombie", func(ctx SpecContext) {
		directory := GinkgoT().TempDir()
		record := filepath.Join(directory, pidFile)
		runContext, cancel := context.WithCancel(ctx)
		defer cancel()
		type outcome struct {
			result Result
			err    error
		}
		finished := make(chan outcome, 1)
		launcher := newLauncher()

		// The spec's own goroutine is the caller that blocks in Run; it is the
		// subject of the test, joined below through finished.
		go func() {
			result, err := launcher.Run(runContext, Command{
				Executable: shell,
				Arguments:  []string{shellCommand, "sleep 600 & echo $! > " + pidFile + "; wait"},
				Directory:  directory,
			})
			finished <- outcome{result, err}
		}()
		grandchild := readPid(record)
		Expect(running(grandchild)).To(BeTrue())

		cancel()

		var done outcome
		Eventually(finished).WithTimeout(reaperBudget.Within).Should(Receive(&done))
		Expect(done.err).To(MatchError(context.Canceled))
		Expect(done.result.ExitCode).To(Equal(-1), "a killed child has no exit status")
		eventually.Await(GinkgoTB(), "the shell's background job to die with its group", reaperBudget,
			func() bool { return running(grandchild) }, func(alive bool) bool { return !alive })
	}, SpecTimeout(2*time.Minute))

	It("streams a started process's standard output through a pipe", func(ctx SpecContext) {
		process, err := newLauncher().Start(ctx, Command{
			Executable: shell, Arguments: []string{shellCommand, "printf streamed"}, StdoutPipe: true,
		})
		Expect(err).NotTo(HaveOccurred())

		body, err := io.ReadAll(process.Stdout())
		Expect(err).NotTo(HaveOccurred())
		result, err := process.Wait()

		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("streamed"))
		Expect(result.Stdout).To(BeNil())
		Expect(process.Terminal()).To(BeNil())
	})

	It("signals the child alone, so it can run its own shutdown", func(ctx SpecContext) {
		directory := GinkgoT().TempDir()
		process, err := newLauncher().Start(ctx, Command{
			Executable: shell,
			Arguments:  []string{shellCommand, "trap 'exit 7' INT; echo $$ > " + pidFile + "; while :; do sleep 0.05; done"},
			Directory:  directory,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(readPid(filepath.Join(directory, pidFile))).To(Equal(process.Pid()))

		Expect(process.Signal(os.Interrupt)).To(Succeed())
		result, err := process.Wait()

		var exitError *ExitError
		Expect(errors.As(err, &exitError)).To(BeTrue(), "got %v", err)
		Expect(result.ExitCode).To(Equal(7))
	}, SpecTimeout(2*time.Minute))

	It("runs an interactive terminal and kills its whole session, background jobs included", func(ctx SpecContext) {
		directory := GinkgoT().TempDir()
		process, err := newLauncher().Start(context.WithoutCancel(ctx), Command{
			Executable: shell, Directory: directory, Terminal: &TerminalSize{Rows: 24, Columns: 80},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(process.Resize(TerminalSize{Rows: 40, Columns: 100})).To(Succeed())

		// A shell on a terminal runs with job control and moves this job into
		// a process group of its own; it keeps the session.
		_, err = io.WriteString(process.Terminal(), "sleep 600 & echo $! > "+pidFile+"\n")
		Expect(err).NotTo(HaveOccurred())
		background := readPid(filepath.Join(directory, pidFile))
		Expect(running(background)).To(BeTrue())

		Expect(process.Kill()).To(Succeed())
		_, err = process.Wait()

		Expect(err).To(HaveOccurred(), "a killed shell has no successful exit")
		eventually.Await(GinkgoTB(), "the terminal's background job to die with its session", reaperBudget,
			func() bool { return running(background) }, func(alive bool) bool { return !alive })
	}, SpecTimeout(2*time.Minute))

	It("refuses to resize a process that has no terminal", func(ctx SpecContext) {
		process, err := newLauncher().Start(ctx, Command{Executable: shell, Arguments: []string{shellCommand, "exit 0"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(process.Resize(TerminalSize{Rows: 1, Columns: 1})).To(MatchError(ErrNoTerminal))
		_, err = process.Wait()
		Expect(err).NotTo(HaveOccurred())
	})

	It("reports a program that cannot start without an exit status", func(ctx SpecContext) {
		launcher := newLauncher()
		_, err := launcher.Run(ctx, Command{Executable: filepath.Join(GinkgoT().TempDir(), "missing")})
		Expect(err).To(MatchError(syscall.ENOENT))
		_, err = launcher.LookPath("csf-proc-no-such-program")
		Expect(err).To(HaveOccurred())
		resolved, err := launcher.LookPath("sh")
		Expect(err).NotTo(HaveOccurred())
		Expect(filepath.IsAbs(resolved)).To(BeTrue())
	})
})
