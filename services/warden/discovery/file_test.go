package discovery

import (
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
)

// filePoll is a short poll interval that keeps file tests fast but not flaky.
const filePoll = 10 * time.Millisecond

// writeAtomic writes contents to path via a temp file + rename, so a poller
// never observes a half-written file (which would otherwise be a spurious
// parse error). It is a file-IO simulator, not a mock.
func writeAtomic(path, contents string) {
	GinkgoHelper()
	tmp := path + ".tmp"
	Expect(os.WriteFile(tmp, []byte(contents), 0o600)).To(Succeed(), "write temp roster")
	Expect(os.Rename(tmp, path)).To(Succeed(), "rename roster")
}

var _ = Describe("File discoverer", func() {
	// TestFileInitialAndChangeOnly
	It("emits the initial roster and only re-emits on real change", func() {
		path := filepath.Join(GinkgoT().TempDir(), "roster.json")
		writeAtomic(path, `{"nodes":[{"id":"node-d","addr":"203.0.113.14:7717"}]}`)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, err := NewFile(path, filePoll).Discover(ctx)
		Expect(err).NotTo(HaveOccurred())

		first := recvRoster(ch)
		Expect(ids(first)).To(Equal([]warden.NodeID{"node-d"}))

		// Rewriting identical content must NOT produce a new snapshot.
		writeAtomic(path, `{"nodes":[{"id":"node-d","addr":"203.0.113.14:7717"}]}`)
		expectNoRoster(ch, 80*time.Millisecond)

		// A real change (added node) produces exactly one new snapshot.
		writeAtomic(path, `{"nodes":[{"id":"node-d","addr":"203.0.113.14:7717"},{"id":"node-a","addr":"203.0.113.11:7717"}]}`)
		second := recvRoster(ch)
		Expect(ids(second)).To(Equal([]warden.NodeID{"node-a", "node-d"})) // sorted by ID
	})

	// TestFileMalformedNoSendThenRecovery
	It("sends nothing for a malformed file, then recovers when fixed", func() {
		path := filepath.Join(GinkgoT().TempDir(), "roster.json")
		writeAtomic(path, `{"nodes":[ this is not json`)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := NewFile(path, filePoll).Discover(ctx)

		// Malformed file: no snapshot at all.
		expectNoRoster(ch, 80*time.Millisecond)

		// Fix the file: the next poll delivers the roster (recovery).
		writeAtomic(path, `{"nodes":[{"id":"node-d","addr":"203.0.113.14:7717"}]}`)
		got := recvRoster(ch)
		Expect(ids(got)).To(Equal([]warden.NodeID{"node-d"}))
	})

	// TestFileMissingThenAppears
	It("treats a missing file as no-send, then emits once it appears", func() {
		path := filepath.Join(GinkgoT().TempDir(), "roster.json") // does not exist yet

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := NewFile(path, filePoll).Discover(ctx)

		// Missing file behaves like an error: no send.
		expectNoRoster(ch, 80*time.Millisecond)

		writeAtomic(path, `{"nodes":[{"id":"a","addr":"203.0.113.24:7717"}]}`)
		got := recvRoster(ch)
		Expect(ids(got)).To(Equal([]warden.NodeID{"a"}))
	})

	// TestFileValidEmptyRosterIsSent
	It("delivers a valid empty roster, then the populated change", func() {
		path := filepath.Join(GinkgoT().TempDir(), "roster.json")
		writeAtomic(path, `{"nodes":[]}`)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := NewFile(path, filePoll).Discover(ctx)

		// An empty-but-valid roster is a real roster and must be delivered.
		got := recvRoster(ch)
		Expect(got.Nodes).To(BeEmpty())

		// Then a populated roster is a change and is sent.
		writeAtomic(path, `{"nodes":[{"id":"a","addr":"203.0.113.24:7717"}]}`)
		got = recvRoster(ch)
		Expect(ids(got)).To(Equal([]warden.NodeID{"a"}))
	})

	// TestFileRejectsMalformedNodeEntry
	It("never emits a partial roster for a node with a bad addr", func() {
		path := filepath.Join(GinkgoT().TempDir(), "roster.json")
		// Valid JSON, but a node addr that is not host:port => treated as an error,
		// never emitted as a partial roster.
		writeAtomic(path, `{"nodes":[{"id":"a","addr":"not-host-port"}]}`)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := NewFile(path, filePoll).Discover(ctx)
		expectNoRoster(ch, 80*time.Millisecond)
	})

	// TestFileClosesOnCtxEnd
	It("closes the channel when the context ends", func() {
		path := filepath.Join(GinkgoT().TempDir(), "roster.json")
		writeAtomic(path, `{"nodes":[{"id":"a","addr":"203.0.113.24:7717"}]}`)

		ctx, cancel := context.WithCancel(context.Background())
		ch, _ := NewFile(path, filePoll).Discover(ctx)
		_ = recvRoster(ch)
		cancel()
		expectClosed(ch)
	})

	// TestNewFileDefaultsPoll
	It("defaults the poll interval when given zero", func() {
		f := NewFile("/nope", 0)
		Expect(f.poll).To(Equal(defaultFilePollInterval))
	})
})
