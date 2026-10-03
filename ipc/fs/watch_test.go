// Copyright 2026 Candace Labs

package fs_test

import (
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ipcfs "github.com/candacelabs/csf/ipc/fs"
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

var _ ipcfs.IWatcher = (*ipcfs.HostWatcher)(nil)

var _ = Describe("Granting change notification", func() {
	It("grants an existing directory", func() {
		directory := GinkgoT().TempDir()
		watcher, err := ipcfs.NewHostWatcher(directory)
		Expect(err).NotTo(HaveOccurred())
		Expect(watcher.Directory()).To(Equal(directory))
	})

	It("refuses an empty directory name and a path that is not a directory", func() {
		_, err := ipcfs.NewHostWatcher("")
		Expect(err).To(MatchError(ipcfs.ErrNoDirectory))

		file := filepath.Join(GinkgoT().TempDir(), watchedNote)
		Expect(os.WriteFile(file, nil, 0o600)).To(Succeed())
		_, err = ipcfs.NewHostWatcher(file)
		Expect(err).To(MatchError(ipcfs.ErrNotDirectory))
	})

	DescribeTable("a HostWatcher nobody granted opens nothing",
		func(watcher *ipcfs.HostWatcher) {
			Expect(watcher.Directory()).To(BeEmpty())
			_, err := watcher.Watch(context.Background())
			Expect(err).To(MatchError(ipcfs.ErrNotGranted))
		},
		Entry("a nil pointer", (*ipcfs.HostWatcher)(nil)),
		Entry("a zero value", &ipcfs.HostWatcher{}),
	)
})

var _ = Describe("Watching a granted directory", func() {
	var (
		directory string
		watch     *ipcfs.Watch
		cancel    context.CancelFunc
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		watcher, err := ipcfs.NewHostWatcher(directory)
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
		Eventually(watch.Changes, notificationWait).Should(Receive(Equal(ipcfs.Change{Name: watchedNote, Op: ipcfs.ChangeCreated})))

		appended, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		Expect(err).NotTo(HaveOccurred())
		_, err = appended.WriteString("{}\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(appended.Close()).To(Succeed())
		Eventually(watch.Changes, notificationWait).Should(Receive(Equal(ipcfs.Change{Name: watchedNote, Op: ipcfs.ChangeWritten})))
	})

	It("reports the entries of an added subdirectory with slash-separated names", func() {
		Expect(os.Mkdir(filepath.Join(directory, watchedSubdir), 0o700)).To(Succeed())
		Eventually(watch.Changes, notificationWait).Should(Receive(Equal(ipcfs.Change{Name: watchedSubdir, Op: ipcfs.ChangeCreated})))
		Expect(watch.Add(watchedSubdir)).To(Succeed())

		Expect(os.WriteFile(filepath.Join(directory, watchedNested), []byte("{}\n"), 0o600)).To(Succeed())
		Eventually(watch.Changes, notificationWait).Should(Receive(Equal(ipcfs.Change{Name: watchedNested, Op: ipcfs.ChangeCreated})))
	})

	It("refuses to add a directory that does not exist, naming the grant", func() {
		err := watch.Add("absent")
		Expect(err).To(MatchError(os.ErrNotExist))
		Expect(err).To(MatchError(ContainSubstring("ipc/fs: watch absent in " + directory)))
	})

	It("ends when its context ends: the channel closes and Add refuses", func() {
		cancel()
		Eventually(watch.Changes, notificationWait).Should(BeClosed())
		Expect(watch.Add(".")).To(MatchError(ipcfs.ErrWatchClosed))
	})
})

var _ = Describe("ChangeOp", func() {
	It("names every operation", func() {
		Expect(ipcfs.ChangeCreated.String()).To(Equal("created"))
		Expect(ipcfs.ChangeWritten.String()).To(Equal("written"))
		Expect(ipcfs.ChangeRemoved.String()).To(Equal("removed"))
		Expect(ipcfs.ChangeRenamed.String()).To(Equal("renamed"))
		Expect(ipcfs.ChangeOp(0).String()).To(Equal("unknown"))
	})
})
