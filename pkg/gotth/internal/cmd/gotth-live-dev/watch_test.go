package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The watcher's specs.
//
// Two layers, and the split is deliberate. The tree-walking half is pure and
// is specified against a real temporary directory rather than a filesystem
// mock: what is actually being asserted is which entries filepath.WalkDir
// hands over and what this program does with them, and a mock of WalkDir would
// assert that the mock behaves the way the author expected it to.
//
// The supervising half is specified against real processes, for the same
// reason. "SIGINT, then SIGKILL after the grace period" and "a failed build
// leaves the running process alone" are statements about process lifetimes,
// and the only honest way to check one is to start a process and end it. Those
// specs skip, loudly, where the thing they need is absent.
//
// No gomock anywhere here: nothing in this package takes an interface, so
// there is no expectation-based collaboration to record. Introducing one to
// have something to mock would be the tail wagging the dog.

func defaults() options {
	return options{
		exts:    normalizeExts(splitList(".go,.templ,.html,.css")),
		exclude: splitList("node_modules,vendor,testdata"),
	}
}

// write creates a file, and the directories above it, with known content.
func write(root, rel, content string) string {
	GinkgoHelper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
	return path
}

var _ = Describe("watchable", func() {
	DescribeTable("decides which directories are descended into",
		func(name string, want bool) {
			descend, source := watchable(name, true, defaults())
			Expect(descend).To(Equal(want))
			Expect(source).To(BeFalse(), "a directory is never source, whatever it is called")
		},
		Entry("an ordinary package directory", "live", true),
		Entry("the root, which the walk hands over as \".\"", ".", true),
		// .git is the one that matters. An ordinary commit rewrites hundreds
		// of files under it, and every one of them would look like a source
		// change to a watcher that descended.
		Entry("the git directory", ".git", false),
		Entry("any other dotted directory", ".idea", false),
		Entry("a named exclusion", "node_modules", false),
		Entry("another named exclusion", "testdata", false),
	)

	DescribeTable("decides which files count as source",
		func(name string, want bool) {
			descend, source := watchable(name, false, defaults())
			Expect(source).To(Equal(want))
			Expect(descend).To(BeFalse(), "a file is never descended into")
		},
		Entry("a Go file", "app.go", true),
		Entry("a templ source", "view.templ", true),
		Entry("generated templ output, which is a Go file like any other", "view_templ.go", true),
		Entry("a stylesheet", "counter.css", true),
		Entry("a Go test file, because a test that stops compiling stops the build", "app_test.go", true),
		Entry("a compiled binary", "counter", false),
		Entry("a lock file", "go.sum", false),
		// A dotted FILE is ordinary. Only dotted DIRECTORIES are skipped, and
		// conflating the two would drop .templ files whose names begin with a
		// dot and, more usefully, keeps the two rules from being written once.
		Entry("a dotfile with no watched extension", ".gitignore", false),
		Entry("a dotfile that IS a watched extension", ".hidden.go", true),
	)
})

var _ = Describe("scan", func() {
	var root string

	BeforeEach(func() {
		root = GinkgoT().TempDir()
		write(root, "main.go", "package main")
		write(root, "view.templ", "templ hello() {}")
		write(root, "counter.css", "b{}")
		write(root, "README.md", "# no")
		write(root, "internal/store/store.go", "package store")
		write(root, ".git/objects/ab/cdef", "binary")
		write(root, "node_modules/left-pad/index.js", "module.exports=1")
		write(root, "testdata/golden.go", "package testdata")
	})

	It("stamps every watched source file, by slash-separated path relative to the root", func() {
		found, err := scan(root, defaults())
		Expect(err).NotTo(HaveOccurred())

		Expect(found).To(HaveKey("main.go"))
		Expect(found).To(HaveKey("view.templ"))
		Expect(found).To(HaveKey("counter.css"))
		Expect(found).To(HaveKey("internal/store/store.go"))
		Expect(found).To(HaveLen(4))
	})

	It("descends into neither .git, node_modules nor testdata", func() {
		found, err := scan(root, defaults())
		Expect(err).NotTo(HaveOccurred())

		for path := range found {
			Expect(path).NotTo(HavePrefix(".git/"))
			Expect(path).NotTo(HavePrefix("node_modules/"))
			Expect(path).NotTo(HavePrefix("testdata/"))
		}
	})

	It("records size as well as time, so a same-second edit is still an edit", func() {
		before, err := scan(root, defaults())
		Expect(err).NotTo(HaveOccurred())

		write(root, "main.go", "package main // longer than it was")
		after, err := scan(root, defaults())
		Expect(err).NotTo(HaveOccurred())

		Expect(after["main.go"]).NotTo(Equal(before["main.go"]))
		Expect(after["main.go"].size).NotTo(Equal(before["main.go"].size))
	})

	It("is stable when nothing has been touched", func() {
		first, err := scan(root, defaults())
		Expect(err).NotTo(HaveOccurred())
		second, err := scan(root, defaults())
		Expect(err).NotTo(HaveOccurred())

		Expect(changed(first, second)).To(BeEmpty(),
			"a scan that disagrees with itself is a watcher that rebuilds forever with nobody typing")
	})
})

var _ = Describe("changed", func() {
	stampAt := func(sec int, size int64) stamp {
		return stamp{mod: time.Unix(int64(sec), 0), size: size}
	}

	It("reports an added file", func() {
		Expect(changed(
			map[string]stamp{"a.go": stampAt(1, 1)},
			map[string]stamp{"a.go": stampAt(1, 1), "b.go": stampAt(1, 1)},
		)).To(Equal([]string{"b.go"}))
	})

	// A deleted .templ is as much a reason to regenerate and rebuild as an
	// edited one. A watcher that only noticed writes would go quiet exactly
	// when a developer moves a file.
	It("reports a removed file", func() {
		Expect(changed(
			map[string]stamp{"a.go": stampAt(1, 1), "b.go": stampAt(1, 1)},
			map[string]stamp{"a.go": stampAt(1, 1)},
		)).To(Equal([]string{"b.go"}))
	})

	It("reports a file whose stamp moved", func() {
		Expect(changed(
			map[string]stamp{"a.go": stampAt(1, 1)},
			map[string]stamp{"a.go": stampAt(2, 1)},
		)).To(Equal([]string{"a.go"}))
	})

	It("reports nothing when nothing moved", func() {
		one := map[string]stamp{"a.go": stampAt(1, 1), "b.templ": stampAt(3, 9)}
		Expect(changed(one, map[string]stamp{"a.go": stampAt(1, 1), "b.templ": stampAt(3, 9)})).To(BeEmpty())
	})

	It("sorts, so the line a developer reads is the same line twice", func() {
		Expect(changed(
			map[string]stamp{},
			map[string]stamp{"z.go": stampAt(1, 1), "a.go": stampAt(1, 1), "m.templ": stampAt(1, 1)},
		)).To(Equal([]string{"a.go", "m.templ", "z.go"}))
	})
})

var _ = Describe("needsTempl", func() {
	// Running templ generate unconditionally would be simpler and is
	// deliberately not done: it rewrites every _templ.go it owns, the next
	// scan reads those as changes, and the watcher regenerates forever.
	It("is true only when a .templ actually moved", func() {
		Expect(needsTempl([]string{"view.templ"})).To(BeTrue())
		Expect(needsTempl([]string{"main.go", "view.templ"})).To(BeTrue())
		Expect(needsTempl([]string{"main.go", "view_templ.go"})).To(BeFalse())
		Expect(needsTempl(nil)).To(BeFalse())
	})
})

var _ = Describe("the flag parsers", func() {
	It("accepts extensions with or without their dot, because both are what people type", func() {
		Expect(normalizeExts(splitList("go,.templ, css "))).To(Equal([]string{".go", ".templ", ".css"}))
	})

	It("drops empty entries rather than watching a file called \"\"", func() {
		Expect(splitList(",, ,")).To(BeEmpty())
		Expect(splitList("a,,b")).To(Equal([]string{"a", "b"}))
	})

	// A watcher pointed at a typo would otherwise sit forever reporting no
	// changes, which looks exactly like a watcher that is working.
	It("refuses a -dir that is missing or is not a directory", func() {
		root := GinkgoT().TempDir()
		file := write(root, "main.go", "package main")

		_, err := abs(filepath.Join(root, "nope"))
		Expect(err).To(HaveOccurred())

		_, err = abs(file)
		Expect(err).To(HaveOccurred())

		resolved, err := abs(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(BeADirectory())
	})
})

var _ = Describe("summarize", func() {
	It("keeps a repeated path once, because one save lands it several times", func() {
		Expect(summarize([]string{"view.templ", "view.templ", "main.go"})).
			To(Equal([]string{"view.templ", "main.go"}))
	})

	It("truncates a templ generate's worth of paths and says how many it hid", func() {
		Expect(summarize([]string{"a", "b", "c", "d", "e", "f", "g", "h"})).
			To(Equal([]string{"a", "b", "c", "d", "e", "f", "… and 2 more"}))
	})
})

var _ = Describe("the supervisor", func() {
	// The property FR-57 leans on hardest, and the one a developer notices
	// within a minute of using this: a typo costs a red line in the terminal,
	// not the session in the browser. A failed build must not stop what is
	// running, because a stopped process would drop the socket, change nothing
	// about the build identity when it came back, and lose the session for no
	// reason at all.
	It("leaves the running application alone when the build fails", func() {
		if _, err := exec.LookPath("go"); err != nil {
			Skip("no go toolchain on PATH: the failed-build spec did not run")
		}

		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "go.mod"),
			[]byte("module watch.example\n\ngo 1.25.0\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "main.go"),
			// time.Sleep and not `select {}`: the Go runtime reports an
			// all-goroutines-asleep deadlock for the latter and exits, which
			// is a process that stops on its own and makes this spec pass for
			// the wrong reason.
			[]byte("package main\n\nimport \"time\"\n\nfunc main() { time.Sleep(time.Hour) }\n"), 0o644)).To(Succeed())

		var log bytes.Buffer
		sup := &supervisor{
			dir:    dir,
			pkg:    ".",
			binary: binaryName(GinkgoT().TempDir()),
			grace:  2 * time.Second,
			out:    &log,
		}
		DeferCleanup(sup.stop)

		sup.cycle(nil, true)
		Expect(sup.running).NotTo(BeNil(), "the first cycle did not start anything: %s", log.String())
		started := sup.running

		Expect(os.WriteFile(filepath.Join(dir, "main.go"),
			[]byte("package main\n\nfunc main() { this is not Go }\n"), 0o644)).To(Succeed())
		log.Reset()
		sup.cycle([]string{"main.go"}, false)

		Expect(sup.running).To(BeIdenticalTo(started),
			"a failed build replaced the running process")
		Expect(log.String()).To(ContainSubstring("go build FAILED"))
		Expect(log.String()).To(ContainSubstring("left alone"))
		Expect(started.done).NotTo(BeClosed(), "a failed build killed the running application")
	})

	// SIGINT first is what gives an application its own shutdown path — the
	// counter example drains its live sessions with App.Close on exactly this
	// signal — and the kill after the grace period is what guarantees the port
	// is free before the next build tries to listen on it.
	It("ends the running application, and returns only once it is gone", func() {
		sleep, err := exec.LookPath("sleep")
		if err != nil {
			Skip("no sleep(1) on PATH: the process-lifetime spec did not run")
		}

		sup := &supervisor{dir: GinkgoT().TempDir(), binary: sleep, args: []string{"60"},
			grace: 3 * time.Second, out: &bytes.Buffer{}}
		Expect(sup.start()).To(Succeed())

		running := sup.running
		Expect(running.done).NotTo(BeClosed())

		sup.stop()

		Expect(sup.running).To(BeNil())
		Expect(running.done).To(BeClosed(),
			"stop returned while the old process was still holding the port the next build wants")
	})

	It("is safe to stop when nothing is running, which is what the deferred stop does", func() {
		sup := &supervisor{grace: time.Second, out: &bytes.Buffer{}}
		Expect(sup.stop).NotTo(Panic())
	})

	// A missing templ is a warning, not a failure. An application with no
	// .templ files at all is a perfectly ordinary thing to point this at, and
	// refusing to build one because a generator it does not need is absent
	// would be this program inventing a dependency for its user. Found by
	// pointing the first cycle at a module with no templ on PATH, where the
	// abort-on-generate-failure arm stopped the application from ever
	// starting.
	It("builds and starts with no templ on PATH, saying what that cost", func() {
		if _, err := exec.LookPath("go"); err != nil {
			Skip("no go toolchain on PATH: the missing-templ spec did not run")
		}

		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "go.mod"),
			[]byte("module notempl.example\n\ngo 1.25.0\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "main.go"),
			[]byte("package main\n\nimport \"time\"\n\nfunc main() { time.Sleep(time.Hour) }\n"), 0o644)).To(Succeed())

		var log bytes.Buffer
		sup := &supervisor{dir: dir, pkg: ".", binary: binaryName(GinkgoT().TempDir()),
			grace: 2 * time.Second, templ: "", out: &log}
		DeferCleanup(sup.stop)

		sup.cycle(nil, true)
		Expect(sup.running).NotTo(BeNil(), "the first cycle refused to start anything: %s", log.String())
		Expect(log.String()).NotTo(ContainSubstring("templ generate"))

		log.Reset()
		sup.cycle([]string{"view.templ"}, false)
		Expect(sup.running).NotTo(BeNil())
		Expect(log.String()).To(ContainSubstring("templ is not on PATH"))
		Expect(log.String()).To(ContainSubstring("building anyway"))
	})
})
