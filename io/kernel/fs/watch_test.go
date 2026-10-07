// Copyright 2026 Candace Labs

package fs_test

import (
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
)

// Integration specs for the watch capability: what a binary granting it, and
// a service holding it, can and cannot do through the exported API alone.
// The only host state they touch is a directory the test framework creates;
// the kernel's notification queue is the subject, so it is real here and a
// gomock double everywhere else.
const (
	watchedNote      = "note.jsonl"
	watchedSubdir    = "run"
	watchedNested    = "run/events.jsonl"
	notificationWait = 5 * time.Second
)

var _ iofs.IWatcher = (*iofs.HostWatcher)(nil)

var _ = Describe("Granting change notification", func() {
	It("grants an existing directory", func() {
		directory := GinkgoT().TempDir()
		watcher, err := iofs.NewHostWatcher(directory)
		Expect(err).NotTo(HaveOccurred())
		Expect(watcher.Directory()).To(Equal(directory))
	})

	It("refuses an empty directory name and a path that is not a directory", func() {
		_, err := iofs.NewHostWatcher("")
		Expect(err).To(MatchError(iofs.ErrNoDirectory))

		file := filepath.Join(GinkgoT().TempDir(), watchedNote)
		Expect(os.WriteFile(file, nil, 0o600)).To(Succeed())
		_, err = iofs.NewHostWatcher(file)
		Expect(err).To(MatchError(iofs.ErrNotDirectory))
	})

	DescribeTable("a HostWatcher nobody granted opens nothing",
		func(watcher *iofs.HostWatcher) {
			Expect(watcher.Directory()).To(BeEmpty())
			_, err := watcher.Watch(context.Background())
			Expect(err).To(MatchError(iofs.ErrNotGranted))
		},
		Entry("a nil pointer", (*iofs.HostWatcher)(nil)),
		Entry("a zero value", &iofs.HostWatcher{}),
	)
})

var _ = Describe("Watching a granted directory", func() {
	var (
		directory string
		watch     *iofs.Watch
		cancel    context.CancelFunc
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		watcher, err := iofs.NewHostWatcher(directory)
		Expect(err).NotTo(HaveOccurred())
		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
		watch, err = watcher.Watch(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(watch.Add(".")).To(Succeed())
	})

	It("reports a created file by its unrooted name, then the write to it", func() {
		path := filepath.Join(directory, watchedNote)
		Expect(os.WriteFile(path, []byte("{}\n"), 0o600)).To(Succeed())
		Eventually(watch.Changes, notificationWait).Should(Receive(Equal(iofs.Change{Name: watchedNote, Op: iofs.ChangeCreated})))

		appended, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		Expect(err).NotTo(HaveOccurred())
		_, err = appended.WriteString("{}\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(appended.Close()).To(Succeed())
		Eventually(watch.Changes, notificationWait).Should(Receive(Equal(iofs.Change{Name: watchedNote, Op: iofs.ChangeWritten})))
	})

	It("reports the entries of an added subdirectory with slash-separated names", func() {
		Expect(os.Mkdir(filepath.Join(directory, watchedSubdir), 0o700)).To(Succeed())
		Eventually(watch.Changes, notificationWait).Should(Receive(Equal(iofs.Change{Name: watchedSubdir, Op: iofs.ChangeCreated})))
		Expect(watch.Add(watchedSubdir)).To(Succeed())

		Expect(os.WriteFile(filepath.Join(directory, watchedNested), []byte("{}\n"), 0o600)).To(Succeed())
		Eventually(watch.Changes, notificationWait).Should(Receive(Equal(iofs.Change{Name: watchedNested, Op: iofs.ChangeCreated})))
	})

	It("refuses to add a directory that does not exist, naming the grant", func() {
		err := watch.Add("absent")
		Expect(err).To(MatchError(os.ErrNotExist))
		Expect(err).To(MatchError(ContainSubstring("ipc/fs: watch absent in " + directory)))
	})

	It("ends when its context ends: the channel closes and Add refuses", func() {
		cancel()
		Eventually(watch.Changes, notificationWait).Should(BeClosed())
		Expect(watch.Add(".")).To(MatchError(iofs.ErrWatchClosed))
	})
})

var _ = Describe("ChangeOp", func() {
	It("names every operation", func() {
		Expect(iofs.ChangeCreated.String()).To(Equal("created"))
		Expect(iofs.ChangeWritten.String()).To(Equal("written"))
		Expect(iofs.ChangeRemoved.String()).To(Equal("removed"))
		Expect(iofs.ChangeRenamed.String()).To(Equal("renamed"))
		Expect(iofs.ChangeOp(0).String()).To(Equal("unknown"))
	})
})
