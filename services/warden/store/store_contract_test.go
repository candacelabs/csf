package store_test

// Contract tests for the warden.IStore implementations (FileStore, MemStore).
// The Store interface makes two promises the election safety argument leans on:
//   1. Load returns ok==false (NOT an error) when no state has ever been saved.
//   2. Save is durable before it returns (write-then-rename for the file store),
//      so a persisted term/vote survives a crash and a node can never vote twice
//      in a term across a restart.
// A malformed on-disk file is an error (ok==false), distinct from "never saved".
//
// On-disk format (Phase Q): FileStore now writes canonical protobuf-JSON
// (protojson, UseProtoNames, compacted) of the proto PersistentState, so the
// durable record and the gRPC wire share one source of truth. The read path
// accepts BOTH the new protojson form and the legacy encoding/json form written
// by earlier binaries; "migration from the legacy encoding/json format" proves a
// legacy file (including a persisted membership with created_in_term) loads
// losslessly.

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/store"
)

func TestStoreContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "store contract suite")
}

var _ = Describe("FileStore", func() {
	var (
		dir  string
		path string
		fs   *store.FileStore
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		path = filepath.Join(dir, "sub", "state.json")
		fs = store.NewFileStore(path)
	})

	Describe("Load with no prior Save", func() {
		It("returns ok=false and no error (never-saved is not an error)", func() {
			st, ok, err := fs.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
			Expect(st).To(Equal(warden.PersistentState{}))
		})
	})

	Describe("Save then Load", func() {
		It("round-trips the persisted term and vote", func() {
			in := warden.PersistentState{CurrentTerm: 7, VotedFor: "node-d"}
			Expect(fs.Save(in)).To(Succeed())
			out, ok, err := fs.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(out).To(Equal(in))
		})

		It("round-trips a persisted membership, including created_in_term", func() {
			in := warden.PersistentState{
				CurrentTerm: 9, VotedFor: "n2",
				Membership: &warden.Membership{Version: 3, CreatedInTerm: 6, Voters: []warden.Node{{ID: "a", Addr: "1:1"}, {ID: "b", Addr: "2:2"}}},
			}
			Expect(fs.Save(in)).To(Succeed())
			out, ok, err := fs.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(out.Membership).NotTo(BeNil())
			Expect(*out.Membership).To(Equal(*in.Membership))
			Expect(out.Membership.CreatedInTerm).To(Equal(warden.Term(6)))
		})

		It("creates the parent directory on first Save", func() {
			Expect(fs.Save(warden.PersistentState{CurrentTerm: 1})).To(Succeed())
			info, err := os.Stat(filepath.Dir(path))
			Expect(err).NotTo(HaveOccurred())
			Expect(info.IsDir()).To(BeTrue())
		})

		It("writes the frozen on-disk protobuf-JSON format (64-bit as a string)", func() {
			Expect(fs.Save(warden.PersistentState{CurrentTerm: 7, VotedFor: "node-d"})).To(Succeed())
			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			// Canonical protojson (UseProtoNames, compacted): uint64 fields render
			// as JSON strings, zero-valued fields are omitted.
			Expect(string(data)).To(Equal(`{"current_term":"7","voted_for":"node-d"}`))
		})

		It("omits zero-valued fields (a fresh node writes an empty object)", func() {
			Expect(fs.Save(warden.PersistentState{})).To(Succeed())
			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal(`{}`))
			// ...and an empty object reads back as the zero state.
			out, ok, err := fs.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(out).To(Equal(warden.PersistentState{}))
		})

		It("leaves no temp files behind (atomic write-then-rename)", func() {
			Expect(fs.Save(warden.PersistentState{CurrentTerm: 3, VotedFor: "n2"})).To(Succeed())
			entries, err := os.ReadDir(filepath.Dir(path))
			Expect(err).NotTo(HaveOccurred())
			for _, e := range entries {
				Expect(e.Name()).NotTo(ContainSubstring(".tmp-"),
					"a temp state file survived the save: %s", e.Name())
			}
		})

		It("overwrites on repeated Save, keeping only the latest state", func() {
			Expect(fs.Save(warden.PersistentState{CurrentTerm: 1, VotedFor: "a"})).To(Succeed())
			Expect(fs.Save(warden.PersistentState{CurrentTerm: 2, VotedFor: "b"})).To(Succeed())
			out, ok, _ := fs.Load()
			Expect(ok).To(BeTrue())
			Expect(out).To(Equal(warden.PersistentState{CurrentTerm: 2, VotedFor: "b"}))
		})
	})

	Describe("durability across a simulated restart", func() {
		It("a fresh FileStore on the same path reads the persisted vote", func() {
			Expect(fs.Save(warden.PersistentState{CurrentTerm: 5, VotedFor: "node-d"})).To(Succeed())
			// A brand-new FileStore models a process restart: the vote must survive.
			restarted := store.NewFileStore(path)
			out, ok, err := restarted.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(out).To(Equal(warden.PersistentState{CurrentTerm: 5, VotedFor: "node-d"}))
		})
	})

	Describe("migration from the legacy encoding/json format", func() {
		// Earlier binaries wrote PersistentState with encoding/json: 64-bit fields
		// as bare numbers, zero-valued fields present, snake_case names (which
		// match the proto field names). proto3 JSON accepts a number OR a string
		// for a 64-bit field, so the single protojson read path parses these
		// losslessly — this is the empirical proof the migration relies on.
		writeLegacy := func(body string) {
			Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
			Expect(os.WriteFile(path, []byte(body), 0o644)).To(Succeed())
		}

		It("loads a legacy term/vote file (bare numbers) losslessly", func() {
			writeLegacy(`{"current_term":7,"voted_for":"node-d"}`)
			out, ok, err := fs.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(out).To(Equal(warden.PersistentState{CurrentTerm: 7, VotedFor: "node-d"}))
		})

		It("loads a legacy zero-valued file (fields present as zeros)", func() {
			writeLegacy(`{"current_term":0,"voted_for":""}`)
			out, ok, err := fs.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(out).To(Equal(warden.PersistentState{}))
		})

		It("loads a legacy file WITH membership, preserving created_in_term", func() {
			writeLegacy(`{"current_term":7,"voted_for":"node-d","membership":{"version":2,"created_in_term":5,"voters":[{"id":"a","addr":"1:1"}]}}`)
			out, ok, err := fs.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(out.CurrentTerm).To(Equal(warden.Term(7)))
			Expect(out.VotedFor).To(Equal(warden.NodeID("node-d")))
			Expect(out.Membership).NotTo(BeNil())
			Expect(out.Membership.Version).To(Equal(uint64(2)))
			Expect(out.Membership.CreatedInTerm).To(Equal(warden.Term(5)))
			Expect(out.Membership.Voters).To(Equal([]warden.Node{{ID: "a", Addr: "1:1"}}))
		})

		It("tolerates a legacy null voters list", func() {
			writeLegacy(`{"current_term":1,"voted_for":"a","membership":{"version":1,"created_in_term":0,"voters":null}}`)
			out, ok, err := fs.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(out.Membership).NotTo(BeNil())
			Expect(out.Membership.Voters).To(BeEmpty())
		})

		It("rewrites a loaded legacy file in the new protobuf-JSON form on the next Save", func() {
			writeLegacy(`{"current_term":7,"voted_for":"node-d"}`)
			loaded, ok, err := fs.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(fs.Save(loaded)).To(Succeed())
			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal(`{"current_term":"7","voted_for":"node-d"}`))
		})
	})

	Describe("Load of a corrupt file", func() {
		It("returns an error (distinct from never-saved) and ok=false", func() {
			Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
			Expect(os.WriteFile(path, []byte("{not valid json"), 0o644)).To(Succeed())
			st, ok, err := fs.Load()
			Expect(err).To(HaveOccurred())
			Expect(ok).To(BeFalse())
			Expect(st).To(Equal(warden.PersistentState{}))
			Expect(err.Error()).To(ContainSubstring("unmarshaling state file"))
		})
	})
})

var _ = Describe("MemStore", func() {
	It("returns ok=false before any Save", func() {
		ms := store.NewMemStore()
		st, ok, err := ms.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		Expect(st).To(Equal(warden.PersistentState{}))
	})

	It("round-trips saved state and reports ok=true", func() {
		ms := store.NewMemStore()
		in := warden.PersistentState{CurrentTerm: 9, VotedFor: "n3"}
		Expect(ms.Save(in)).To(Succeed())
		out, ok, err := ms.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(out).To(Equal(in))
	})

	It("models an intact-state restart (a reused MemStore keeps the vote)", func() {
		ms := store.NewMemStore()
		Expect(ms.Save(warden.PersistentState{CurrentTerm: 4, VotedFor: "a"})).To(Succeed())
		// Reusing the same MemStore across a new consumer is the documented way to
		// model a restart with intact state.
		out, ok, _ := ms.Load()
		Expect(ok).To(BeTrue())
		Expect(out.CurrentTerm).To(Equal(warden.Term(4)))
		Expect(out.VotedFor).To(Equal(warden.NodeID("a")))
	})

	It("implements warden.IStore", func() {
		var _ warden.IStore = store.NewMemStore()
		var _ warden.IStore = store.NewFileStore(filepath.Join(GinkgoT().TempDir(), "s.json"))
	})
})
