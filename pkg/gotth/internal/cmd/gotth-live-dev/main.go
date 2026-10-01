// Command gotth-live-dev is the server half of FR-57: it watches a gotth-live
// application's source, rebuilds it when a Go or templ file changes, and
// restarts it. The browser half is client/dev-reload.js, which notices that
// the build identity moved and reloads the page.
//
// Neither half needs the other to be this program in particular. Anything that
// rebuilds and restarts the process — air, wgo, reflex, entr, a shell loop —
// produces a new executable and therefore a new build identity, and the page
// reloads exactly the same way. This one exists so the feature has a default
// that is in the repository, needs nothing installed, and is held by the same
// CI as the library.
//
// # Running it
//
//	# from your application's module, against the checked-out library:
//	go run github.com/candacelabs/csf/pkg/gotth/internal/cmd/gotth-live-dev
//
//	# with arguments for your application, after --:
//	go run github.com/candacelabs/csf/pkg/gotth/internal/cmd/gotth-live-dev -- -addr 0.0.0.0:8080
//
// It lives under internal/ and is still reachable from a consumer's module:
// Go's internal rule governs IMPORTS, and a main package named on the command
// line is not imported by anything. The alternative — a package at the module
// root — would be a third exported package, which internal/arch caps at two
// and which needs a ruling rather than a directory.
//
// # What it does on a change
//
//	.templ changed   templ generate, then go build, then restart
//	.go changed      go build, then restart
//	build failed     nothing is restarted; the previous build keeps serving,
//	                 its build identity is unchanged, and the page in the
//	                 browser does not reload. Fix and save again.
//
// It builds with `go build -o` and runs the resulting binary rather than using
// `go run`, so the process it supervises is the application itself and not a
// toolchain that spawned it. See supervisor.build.
//
// # What it does not do
//
// It does not know about the browser, the WebSocket, or the session. It does
// not preserve anything across the restart, because nothing here can: the
// server-held state of a session belongs to the process that is being
// replaced. docs/guide/dev-reload.md says what survives and what does not, in
// those terms.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gotth-live-dev:", err)
		os.Exit(1)
	}
}

func run() error {
	dir := flag.String("dir", ".", "the module directory to watch, build and run")
	pkg := flag.String("pkg", ".", "the package to build, relative to -dir")
	exts := flag.String("ext", ".go,.templ,.html,.css",
		"comma-separated file extensions that count as source")
	exclude := flag.String("exclude", "node_modules,vendor,testdata",
		"comma-separated directory names never descended into; every dotted directory is skipped anyway")
	interval := flag.Duration("interval", 250*time.Millisecond, "how often the tree is scanned")
	settle := flag.Duration("settle", 300*time.Millisecond,
		"how long changes must stop before a rebuild starts; a save that writes several files is one rebuild")
	grace := flag.Duration("grace", 3*time.Second,
		"how long the application gets to exit after SIGINT before it is killed")
	flag.Parse()

	root, err := abs(*dir)
	if err != nil {
		return fmt.Errorf("-dir %q could not be resolved to an absolute path: %w: "+
			"pass the directory of the module you want watched, or leave -dir unset to watch "+
			"the current one", *dir, err)
	}

	scratch, err := os.MkdirTemp("", "gotth-live-dev-")
	if err != nil {
		return fmt.Errorf("could not create the scratch directory the rebuilt binary is written to: %w: "+
			"check TMPDIR and the space behind it", err)
	}
	defer os.RemoveAll(scratch)

	opts := options{exts: normalizeExts(splitList(*exts)), exclude: splitList(*exclude)}

	// templ is resolved once, at startup, and its absence is reported once.
	// Looking it up per cycle would turn "you have not installed templ" into a
	// line that scrolls past on every save.
	templ, lookErr := exec.LookPath("templ")
	if lookErr != nil {
		templ = ""
	}

	sup := &supervisor{
		dir:    root,
		pkg:    *pkg,
		binary: binaryName(scratch),
		args:   flag.Args(),
		grace:  *grace,
		templ:  templ,
		out:    os.Stderr,
	}
	defer sup.stop()

	fmt.Fprintf(os.Stderr, "gotth-live-dev: watching %s for %v\n", root, opts.exts)
	if templ == "" {
		fmt.Fprintln(os.Stderr,
			"gotth-live-dev: templ is not on PATH; .templ changes will rebuild but will not regenerate. "+
				"go install github.com/a-h/templ/cmd/templ@latest")
	}

	prev, err := scan(root, opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "gotth-live-dev: %d source files\n", len(prev))

	// The first cycle regenerates whether or not a .templ looks new, because
	// the watcher was not running when the last edit happened and the
	// committed _templ.go may be older than the .templ beside it. Every cycle
	// after this one regenerates only when a .templ actually moved.
	sup.cycle(nil, true)
	if after, err := scan(root, opts); err == nil {
		prev = after
	}

	stopping := make(chan os.Signal, 1)
	signal.Notify(stopping, os.Interrupt)

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	// pending holds the paths seen since the last quiet moment. A single
	// editor save can write a .templ, its backup and its generated Go file
	// over several hundred milliseconds, and `templ generate` writes more; the
	// settle window is what makes that ONE rebuild rather than four, and it is
	// the difference between a reload and a flicker of reloads.
	var pending []string
	var quietAt time.Time

	for {
		select {
		case <-stopping:
			fmt.Fprintln(os.Stderr, "gotth-live-dev: stopping")
			return nil
		case <-ticker.C:
		}

		next, err := scan(root, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gotth-live-dev: scanning %s failed: %v\n", root, err)
			continue
		}
		if moved := changed(prev, next); len(moved) > 0 {
			pending = append(pending, moved...)
			quietAt = time.Now().Add(*settle)
			prev = next
			continue
		}
		if len(pending) == 0 || time.Now().Before(quietAt) {
			continue
		}

		fmt.Fprintf(os.Stderr, "gotth-live-dev: changed: %v\n", summarize(pending))
		sup.cycle(pending, false)
		pending = nil

		// Re-scanned after the cycle because `templ generate` and `go build`
		// both write into the tree being watched. Without this the generated
		// _templ.go files show up as the next change and the watcher rebuilds
		// itself in a loop — which is exactly what the first draft did.
		if after, err := scan(root, opts); err == nil {
			prev = after
		}
	}
}

// summarize keeps the changed-file line short and free of repeats.
//
// Both halves earn their place. A save that is written, backed up and
// regenerated lands the same path in the pending list several times over the
// settle window, and a `templ generate` run legitimately touches dozens of
// files at once — a line naming all of them is a line nobody reads.
func summarize(paths []string) []string {
	const most = 6
	seen := map[string]bool{}
	var uniq []string
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		uniq = append(uniq, path)
	}
	if len(uniq) <= most {
		return uniq
	}
	return append(uniq[:most:most], fmt.Sprintf("… and %d more", len(uniq)-most))
}
