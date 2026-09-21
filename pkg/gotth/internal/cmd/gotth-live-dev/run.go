package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// supervisor builds the application and keeps exactly one of it running.
type supervisor struct {
	dir     string        // the module directory
	pkg     string        // the package pattern handed to go build
	binary  string        // where the built executable is written
	args    []string      // the application's own arguments
	grace   time.Duration // how long a stopped process gets before it is killed
	templ   string        // the templ binary, or "" if there is none on PATH
	out     io.Writer     // where this watcher's own lines go
	running *child
}

// child is one running application and the signal that it has exited.
//
// The channel is what makes stop portable. Probing liveness with signal 0 is
// the unix idiom and does not exist on Windows; a channel closed by the same
// goroutine that owns Wait says the same thing everywhere, and says it without
// a second Wait racing the first for the exit status.
type child struct {
	proc *os.Process
	done chan struct{}
}

// generate runs `templ generate` in the module directory. The caller has
// already established that templ is on PATH.
func (s *supervisor) generate() error {
	cmd := exec.Command(s.templ, "generate")
	cmd.Dir = s.dir
	cmd.Stdout = s.out
	cmd.Stderr = s.out
	return cmd.Run()
}

// build compiles the application to s.binary.
//
// `go build -o <path>` rather than `go run`. The difference is not cosmetic:
// `go run` compiles to its own cache and then spawns the binary as a CHILD, so
// the process this watcher can see and signal is the toolchain rather than the
// application, and killing it leaves the application listening on the port the
// next one wants. Building first makes the process this watcher supervises the
// process the browser is talking to, on every platform, with no process-group
// tricks.
//
// The compiler's own output goes straight through: a build error is the most
// useful thing this program ever prints, and reformatting it would only make
// it harder for an editor to jump to.
func (s *supervisor) build() error {
	cmd := exec.Command("go", "build", "-o", s.binary, s.pkg)
	cmd.Dir = s.dir
	cmd.Stdout = s.out
	cmd.Stderr = s.out
	return cmd.Run()
}

// start launches the built binary.
func (s *supervisor) start() error {
	cmd := exec.Command(s.binary, s.args...)
	cmd.Dir = s.dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	s.running = &child{proc: cmd.Process, done: done}
	// Reaped on its own goroutine so that a process which exits by itself —
	// a panic, a port already in use, a deliberate os.Exit — does not become a
	// zombie waiting for a Wait that only happens on the next file change.
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	return nil
}

// stop ends the running application, politely first.
//
// SIGINT, then SIGKILL after the grace period. The interrupt is what gives an
// application its own shutdown path — the counter example drains its live
// sessions with App.Close on exactly this signal — and the kill is what
// guarantees the port is free before the next build starts listening on it.
//
// On a platform where os.Interrupt cannot be delivered, the interrupt attempt
// fails immediately and the kill happens at once. That is Windows, and it is
// the honest behaviour there rather than a hang.
func (s *supervisor) stop() {
	if s.running == nil {
		return
	}
	running := s.running
	s.running = nil

	if err := running.proc.Signal(os.Interrupt); err != nil {
		_ = running.proc.Kill()
		<-running.done
		return
	}
	select {
	case <-running.done:
	case <-time.After(s.grace):
		_ = running.proc.Kill()
		<-running.done
	}
}

// cycle is one rebuild-and-restart. It reports what it did, in the order it
// did it, because those lines are the only evidence a developer has that the
// watcher saw their change.
//
// A failing `templ generate` or `go build` deliberately leaves the running
// process ALONE. That is the difference between a typo costing a red line in
// the terminal and a typo costing the session in the browser: the old build
// keeps serving, its build identity has not changed, and the page does not
// reload. Fix the error, save, and the next cycle restarts it.
func (s *supervisor) cycle(paths []string, first bool) {
	started := time.Now()

	// The first cycle regenerates unconditionally: the watcher was not running
	// when the last edit happened, so a .templ on disk may be newer than the
	// _templ.go beside it and no path in this cycle would say so. Every cycle
	// after it regenerates only when a .templ actually moved — see needsTempl
	// for why running it every time is a loop rather than a cost.
	//
	// A missing templ is a warning and not a failure. An application with no
	// .templ files at all is a perfectly ordinary thing to point this at, and
	// refusing to build one because a generator it does not need is absent
	// would be this program inventing a dependency for its user. The startup
	// banner already said templ was missing; this line says what it cost.
	if first || needsTempl(paths) {
		switch {
		case s.templ == "" && !first:
			fmt.Fprintln(s.out, "gotth-live-dev: a .templ changed and templ is not on PATH, "+
				"so the generated Go is whatever it was; building anyway")
		case s.templ == "":
		default:
			if err := s.generate(); err != nil {
				fmt.Fprintf(s.out, "gotth-live-dev: templ generate FAILED: %v — the running build is left alone\n", err)
				return
			}
			fmt.Fprintln(s.out, "gotth-live-dev: templ generate ok")
		}
	}

	if err := s.build(); err != nil {
		fmt.Fprintf(s.out, "gotth-live-dev: go build FAILED: %v — the running build is left alone\n", err)
		return
	}
	fmt.Fprintf(s.out, "gotth-live-dev: build ok in %s\n", time.Since(started).Round(time.Millisecond))

	s.stop()
	if err := s.start(); err != nil {
		fmt.Fprintf(s.out, "gotth-live-dev: starting the application failed: %v\n", err)
		return
	}
	fmt.Fprintf(s.out, "gotth-live-dev: restarted — the browser reloads when it sees the new build identity\n")
}

// binaryName is where the built executable goes inside the scratch directory.
func binaryName(dir string) string {
	name := "app"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}
