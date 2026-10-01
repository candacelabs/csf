package store_test

import (
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/store"
)

var _ = Describe("FileStore", func() {
	// TestFileStoreRoundtrip
	It("round-trips persisted state and overwrites atomically", func() {
		path := filepath.Join(GinkgoT().TempDir(), "state.json")
		fs := store.NewFileStore(path)

		want := warden.PersistentState{CurrentTerm: 42, VotedFor: "node-a"}
		Expect(fs.Save(want)).To(Succeed())
		got, ok, err := fs.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(want))

		// Overwrite with new state; the rename must replace atomically.
		want2 := warden.PersistentState{CurrentTerm: 43, VotedFor: ""}
		Expect(fs.Save(want2)).To(Succeed())
		got, ok, err = fs.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(want2))
	})

	// TestFileStoreNoPartialFileOrTempLeftover
	It("leaves no partial file or temp leftover after repeated saves", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "state.json")
		fs := store.NewFileStore(path)

		for i := 0; i < 5; i++ {
			Expect(fs.Save(warden.PersistentState{CurrentTerm: warden.Term(i), VotedFor: "n1"})).To(Succeed())
		}

		entries, err := os.ReadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		// Exactly the state file must remain: no temp files left behind.
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		Expect(entries).To(HaveLen(1), "expected only the state file, found: %v", names)
		Expect(entries[0].Name()).To(Equal("state.json"))
	})

	// TestFileStoreCreatesParentDirs
	It("creates missing parent directories on save", func() {
		path := filepath.Join(GinkgoT().TempDir(), "a", "b", "c", "state.json")
		fs := store.NewFileStore(path)
		Expect(fs.Save(warden.PersistentState{CurrentTerm: 1, VotedFor: "x"})).To(Succeed(), "Save into nested dirs")
		_, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred(), "state file not created")
	})

	// TestFileStoreLoadMissing
	It("reports ok=false and zero state for a missing file, without error", func() {
		fs := store.NewFileStore(filepath.Join(GinkgoT().TempDir(), "does-not-exist.json"))
		st, ok, err := fs.Load()
		Expect(err).NotTo(HaveOccurred(), "Load of missing file should not error")
		Expect(ok).To(BeFalse(), "Load of missing file should report ok=false")
		Expect(st).To(Equal(warden.PersistentState{}), "Load of missing file should return zero state")
	})

	// TestFileStoreCorruptFile
	It("errors and reports ok=false for a corrupt file", func() {
		path := filepath.Join(GinkgoT().TempDir(), "state.json")
		Expect(os.WriteFile(path, []byte("{not valid json"), 0o644)).To(Succeed(), "seeding corrupt file")
		fs := store.NewFileStore(path)
		_, ok, err := fs.Load()
		Expect(err).To(HaveOccurred(), "Load of corrupt file should error")
		Expect(ok).To(BeFalse(), "Load of corrupt file should report ok=false")
	})
})

var _ = Describe("MemStore", func() {
	// TestMemStoreRoundtripAndEmpty
	It("reports empty before save and round-trips after save", func() {
		ms := store.NewMemStore()

		st, ok, err := ms.Load()
		Expect(err).NotTo(HaveOccurred(), "Load before save")
		Expect(ok).To(BeFalse(), "Load before save should report ok=false")
		Expect(st).To(Equal(warden.PersistentState{}), "Load before save should return zero state")

		want := warden.PersistentState{CurrentTerm: 9, VotedFor: "z"}
		Expect(ms.Save(want)).To(Succeed())
		got, ok, err := ms.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(want))
	})

	// TestMemStoreConcurrent
	It("is safe under concurrent Save/Load (race)", func() {
		ms := store.NewMemStore()
		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				for j := 0; j < 200; j++ {
					_ = ms.Save(warden.PersistentState{CurrentTerm: warden.Term(i), VotedFor: "n"})
					_, _, _ = ms.Load()
				}
			}(i)
		}
		wg.Wait()
	})
})
