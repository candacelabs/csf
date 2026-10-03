package conformance_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

// The browser is livetest.Browser: a minimal Chrome DevTools Protocol client
// over the WebSocket library the module already depends on, started through
// the process capability. It used to live here; it moved to livetest when a
// second suite needed the same launch, and this file keeps only what is this
// suite's — which environment variable names the binary, and the skip.

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

// launchChrome starts headless chromium and attaches to a fresh page.
func launchChrome() *livetest.Browser {
	GinkgoHelper()
	launcher, err := proc.NewHostLauncher()
	Expect(err).NotTo(HaveOccurred())
	return livetest.LaunchBrowser(GinkgoTB(), launcher, livetest.BrowserOptions{
		Executable: browserOnly(),
		Profile:    GinkgoT().TempDir(),
	})
}
