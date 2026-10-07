// Copyright 2026 Candace Labs

package session

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseIdent", func() {
	DescribeTable("splits git's committer ident",
		func(ident string, name string, email string, found bool) {
			gotName, gotEmail, gotFound := parseIdent(ident)
			Expect([]any{gotName, gotEmail, gotFound}).To(Equal([]any{name, email, found}))
		},
		Entry("a full ident", "CSF Agent <agent@example.invalid> 1700000000 +0000\n", "CSF Agent", "agent@example.invalid", true),
		Entry("no name", " <agent@example.invalid> 1700000000 +0000", "", "agent@example.invalid", false),
		Entry("no email", "CSF Agent", "", "", false),
		Entry("nothing", "", "", "", false),
	)
})

var _ = Describe("conversationKey", func() {
	DescribeTable("names a working directory's conversations the way the executor does",
		func(directory string, key string) { Expect(conversationKey(directory)).To(Equal(key)) },
		Entry("a hidden directory and an underscore", "/srv/agent/csf_work/.local/run-1/worktree", "-srv-agent-csf-work--local-run-1-worktree"),
		Entry("letters and digits kept", "/a/B9", "-a-B9"),
	)
})

var _ = Describe("adoptConversations", func() {
	It("moves what a private home saved into the store and keeps what the store already holds", func() {
		private, store := GinkgoT().TempDir(), GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(private, "saved.jsonl"), []byte("private"), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(private, "both.jsonl"), []byte("private"), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(store, "both.jsonl"), []byte("store"), 0o600)).To(Succeed())
		Expect(adoptConversations(private, store)).To(Succeed())
		Expect(os.ReadFile(filepath.Join(store, "saved.jsonl"))).To(Equal([]byte("private")))
		Expect(os.ReadFile(filepath.Join(store, "both.jsonl"))).To(Equal([]byte("store")))
		Expect(filepath.Join(private, "saved.jsonl")).NotTo(BeAnExistingFile())
	})

	It("does nothing when no private conversations exist", func() {
		store := GinkgoT().TempDir()
		Expect(adoptConversations(filepath.Join(store, "absent"), filepath.Join(store, "key"))).To(Succeed())
		Expect(filepath.Join(store, "key")).NotTo(BeADirectory())
	})
})

var _ = Describe("within", func() {
	DescribeTable("reports a path at or below a directory",
		func(path string, inside bool) { Expect(within(path, "/run/s1")).To(Equal(inside)) },
		Entry("the directory", "/run/s1", true),
		Entry("below it", "/run/s1/bazel", true),
		Entry("a sibling sharing its prefix", "/run/s10", false),
		Entry("its parent", "/run", false),
	)
})
