package main

import (
	"flag"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// envOf builds the getenv applyEnvFallbacks reads, so no spec has to mutate the
// process environment. The injection is the reason these specs can run in
// parallel with everything else in the suite: os.Setenv is process-global and a
// spec that used it would be a spec whose result depends on what ran beside it.
func envOf(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

// flagsUnderTest declares exactly the flags run() declares, with exactly its
// defaults.
//
// It is a mirror, and a mirror can go stale — what it cannot do is go stale
// silently in the direction that matters: a flag run() gains and this omits
// makes the spec below cover less than it claims, and a flag that changes name
// makes applyEnvFallbacks skip it, which docker/gotth.Dockerfile's smoke check
// catches as a server listening on the wrong address. The end-to-end proof that
// the real binary honours the real environment is that smoke check, run through
// the proxy; these specs are the precedence rules, which a container cannot
// show you.
func flagsUnderTest() *flag.FlagSet {
	fs := flag.NewFlagSet("counter-gotth", flag.ContinueOnError)
	fs.String("addr", "127.0.0.1:3000", "address to listen on")
	fs.String("origin", "", "comma-separated extra browser Origins to allow")
	fs.String("shim", DefaultShimPath, "path to bench/harness/shim.js")
	return fs
}

var _ = Describe("§5.2's container environment, translated into this app's flags", func() {
	var fs *flag.FlagSet

	BeforeEach(func() {
		fs = flagsUnderTest()
	})

	value := func(name string) string {
		GinkgoHelper()
		f := fs.Lookup(name)
		Expect(f).NotTo(BeNil(), "the mirror in flagsUnderTest is missing -"+name)
		return f.Value.String()
	}

	Describe("the listen address", func() {
		It("joins BENCH_HOST and PORT, which is what compose sets", func() {
			Expect(fs.Parse(nil)).To(Succeed())
			Expect(applyEnvFallbacks(fs, envOf(map[string]string{
				"BENCH_HOST": "0.0.0.0", "PORT": "3000",
			}))).To(Succeed())
			Expect(value("addr")).To(Equal("0.0.0.0:3000"))
		})

		It("defaults the half that is missing, the way scripts/start-app.mjs does", func() {
			Expect(fs.Parse(nil)).To(Succeed())
			Expect(applyEnvFallbacks(fs, envOf(map[string]string{"PORT": "3100"}))).To(Succeed())
			Expect(value("addr")).To(Equal("0.0.0.0:3100"))
		})

		It("leaves the flag's own loopback default when neither variable is set", func() {
			Expect(fs.Parse(nil)).To(Succeed())
			Expect(applyEnvFallbacks(fs, envOf(nil))).To(Succeed())
			Expect(value("addr")).To(Equal("127.0.0.1:3000"))
		})

		It("lets an explicit flag win over the environment", func() {
			Expect(fs.Parse([]string{"-addr", "127.0.0.1:3100"})).To(Succeed())
			Expect(applyEnvFallbacks(fs, envOf(map[string]string{
				"BENCH_HOST": "0.0.0.0", "PORT": "3000",
			}))).To(Succeed())
			Expect(value("addr")).To(Equal("127.0.0.1:3100"))
		})
	})

	Describe("the Origin allowlist", func() {
		// The 403 trap. In the §3.6 topology the browser's origin is the
		// PROXY's https://127.0.0.1:<port>, not the app's http://0.0.0.0:3000,
		// and an allowlist derived from the listen address alone rejects every
		// upgrade with a 403 that looks like a server that is simply down.
		It("carries the proxy's origin all the way into the allowlist", func() {
			Expect(fs.Parse(nil)).To(Succeed())
			Expect(applyEnvFallbacks(fs, envOf(map[string]string{
				"BENCH_HOST": "0.0.0.0", "PORT": "3000",
				"BENCH_ORIGIN": "https://127.0.0.1:18443",
			}))).To(Succeed())
			Expect(allowedOrigins(value("addr"), value("origin"))).To(
				ContainElement("https://127.0.0.1:18443"))
		})

		It("treats an empty variable as an absent one", func() {
			Expect(fs.Parse(nil)).To(Succeed())
			Expect(applyEnvFallbacks(fs, envOf(map[string]string{"BENCH_ORIGIN": ""}))).To(Succeed())
			Expect(value("origin")).To(BeEmpty())
		})
	})

	It("takes the shim path from the environment, since the image's copy is not beside the source", func() {
		Expect(fs.Parse(nil)).To(Succeed())
		Expect(applyEnvFallbacks(fs, envOf(map[string]string{
			"BENCH_SHIM_PATH": "/bench/harness/shim.js",
		}))).To(Succeed())
		Expect(value("shim")).To(Equal("/bench/harness/shim.js"))
	})

	// What makes the three copies of applyEnvFallbacks identical rather than
	// merely similar: it names every flag the family defines and skips the ones
	// this app does not. The counter has no fixture and no replay.
	It("ignores the variables this app has no flag for", func() {
		Expect(fs.Parse(nil)).To(Succeed())
		Expect(applyEnvFallbacks(fs, envOf(map[string]string{
			"BENCH_FIXTURE_DIR": "/bench/fixtures",
			"BENCH_TICK_MS":     "10",
			"BENCH_HTMX_PATH":   "/bench/vendor/htmx-2.0.10.min.js",
		}))).To(Succeed())
		Expect(fs.Lookup("fixtures")).To(BeNil())
		Expect(fs.Lookup("tick")).To(BeNil())
		Expect(fs.Lookup("htmx")).To(BeNil())
	})
})
