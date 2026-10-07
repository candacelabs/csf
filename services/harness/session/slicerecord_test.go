// Copyright 2026 Candace Labs

package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/proc"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/services/harness/session"
)

const (
	authorAssignment = "3c4d5e6f-7081-4293-a4b5-c6d7e8f90a12"
	authorBranch     = "dev/ir"
	authorBase       = "origin/main"
	authorTicket     = "https://github.com/candacelabs/example/issues/1"
	authorHead       = "0123456789abcdef0123456789abcdef01234567"
	authorMergeBase  = "fedcba9876543210fedcba9876543210fedcba98"
)

var _ = Describe("the slice record", func() {
	var (
		launcher *MockILauncher
		state    string
		worktree string
	)

	BeforeEach(func() {
		launcher = NewMockILauncher(gomock.NewController(GinkgoT()))
		state = GinkgoT().TempDir()
		worktree = filepath.Join(state, authorAssignment, session.WorktreeDirectory)
		Expect(os.MkdirAll(session.RunDirectory(state, authorAssignment), 0o700)).To(Succeed())
		Expect(session.WriteRunState(session.RunDirectory(state, authorAssignment), &session.RunState{
			AssignmentID: authorAssignment, TicketURL: authorTicket, Worktree: worktree, Branch: authorBranch, BaseBranch: authorBase,
		})).To(Succeed())
	})

	gitAnswers := func() {
		launcher.EXPECT().Run(gomock.Any(), launched("git", "-C", worktree, "rev-parse", authorBranch)).
			Return(proc.Result{Stdout: []byte(authorHead + "\n")}, nil)
		launcher.EXPECT().Run(gomock.Any(), launched("git", "-C", worktree, "merge-base", authorBase, authorHead)).
			Return(proc.Result{Stdout: []byte(authorMergeBase + "\n")}, nil)
		launcher.EXPECT().Run(gomock.Any(), launched("git", "-C", worktree, "diff", "--name-only", authorMergeBase, authorHead)).
			Return(proc.Result{Stdout: []byte("a/a.go\nb/b_test.go\n")}, nil)
	}

	It("reads the author run's ticket, branch and diff, with no verdict before the verifier answers", func() {
		gitAnswers()
		record, err := session.ReadSliceRecord(context.Background(), launcher, state, authorAssignment)
		Expect(err).NotTo(HaveOccurred())
		Expect(record).To(Equal(session.SliceRecord{
			AssignmentID: authorAssignment, TicketURL: authorTicket, Branch: authorBranch,
			Diff:      session.SliceDiff{Base: authorMergeBase, Head: authorHead, Files: []string{"a/a.go", "b/b_test.go"}},
			Decisions: []session.Decision{}, OpenQuestions: []string{},
		}))
	})

	It("carries the verdict of the verifier station for its head", func() {
		gitAnswers()
		writeLog(session.RunDirectory(state, session.VerifierAssignmentID(authorAssignment, authorHead)), turnRequested, verifierReply)
		record, err := session.ReadSliceRecord(context.Background(), launcher, state, authorAssignment)
		Expect(err).NotTo(HaveOccurred())
		Expect(record.Verdict).NotTo(BeNil())
		Expect(*record.Verdict.Pass).To(BeTrue())
	})

	It("fails naming git when the branch does not resolve", func() {
		launcher.EXPECT().Run(gomock.Any(), launched("git")).Return(proc.Result{ExitCode: 128}, errors.New("unknown revision"))
		_, err := session.ReadSliceRecord(context.Background(), launcher, state, authorAssignment)
		Expect(err).To(MatchError(ContainSubstring("slice record: git rev-parse")))
	})

	It("refuses an assignment with no recorded run", func() {
		_, err := session.ReadSliceRecord(context.Background(), launcher, state, siblingAssignment)
		Expect(err).To(MatchError(session.ErrNoRunState))
	})

	It("keys the verifier to the author run and head", func() {
		Expect(session.VerifierAssignmentID(authorAssignment, authorHead)).To(Equal(session.VerifierAssignmentID(authorAssignment, authorHead)))
		Expect(session.VerifierAssignmentID(authorAssignment, authorHead)).NotTo(Equal(session.VerifierAssignmentID(authorAssignment, authorMergeBase)))
	})

	It("builds a verifier recipe that reads only the record and its diff", func() {
		record := session.SliceRecord{
			AssignmentID: authorAssignment, TicketURL: authorTicket, Branch: authorBranch,
			Diff:      session.SliceDiff{Base: authorMergeBase, Head: authorHead, Files: []string{"a/a.go"}},
			Decisions: []session.Decision{{Statement: "s", Reason: "r"}}, OpenQuestions: []string{},
			Verdict: &session.Verdict{},
		}
		author := &pb.AgentAssignmentRecipe{
			Model: "claude-opus-5-5", RepositoryId: "csf", Workspace: &pb.AgentWorkspace{RepositoryPath: "/repository"},
		}
		recipe, err := session.VerifierRecipe(record, author)
		Expect(err).NotTo(HaveOccurred())
		Expect(recipe.GetAssignmentId()).To(Equal(session.VerifierAssignmentID(authorAssignment, authorHead)))
		Expect(recipe.GetAgent().GetId()).To(Equal(session.VerifierAgentID))
		Expect(recipe.GetModel()).To(Equal("claude-opus-5-5"))
		Expect(recipe.GetWorkspace().GetAllowedTools()).To(Equal([]string{"Bash(git diff:*)"}))
		Expect(recipe.GetWorkspace().GetBaseBranch()).To(Equal(authorHead))
		Expect(recipe.GetWorkspace().GetBranch()).To(Equal("verify/dev/ir-0123456789ab"))
		Expect(recipe.GetTask()).To(ContainSubstring("git diff " + authorMergeBase + " " + authorHead))
		Expect(recipe.GetTask()).To(ContainSubstring("```verdict\n"))
		Expect(recipe.GetTask()).NotTo(ContainSubstring(`"verdict"`))

		statement := recipe.GetTask()[strings.Index(recipe.GetTask(), "```json\n")+len("```json\n"):]
		var carried session.SliceRecord
		Expect(json.Unmarshal([]byte(statement[:strings.Index(statement, "\n```")]), &carried)).To(Succeed())
		record.Verdict = nil
		Expect(carried).To(Equal(record))
	})
})
