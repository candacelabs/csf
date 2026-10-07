// Copyright 2026 Candace Labs

package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/io/ipc/proc"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

// The verifier station's recipe: one agent, a branch per verified head, and
// the one tool that reads the diff.
const (
	VerifierAgentID       = "slice-verifier"
	verifierAgentRevision = 1
	verifierDisplayName   = "Slice verifier"
	verifierInstructions  = "You are a CSF verifier station. You read only the slice record in the task and the diff it names; " +
		"you change nothing. End with the typed verdict the task shows."
	verifierBranchPrefix = "verify/"
	verifierTitlePrefix  = "VERIFY "
	verifierTool         = "Bash(git diff:*)"
	verifierBriefFormat  = "Verify one slice from its record and its diff alone.\n\n" +
		"Slice record:\n```json\n%s\n```\n\n" +
		"Diff: `git diff %s %s`\n\n" +
		"A defect is a statement of the record the diff does not bear out, or a change the diff makes that no statement of the record covers. " +
		"Give each defect the statement, the record field it is in (or `diff`) and its evidence, each a path:line of the diff or a record field. " +
		"End with a fenced block tagged %s holding one JSON object; pass is true exactly when defects is empty:\n```%s\n%s\n```"
	shortHead = 12

	gitMergeBase = "merge-base"
	gitRevParse  = "rev-parse"
	gitDiff      = "diff"
	gitNameOnly  = "--name-only"
)

// verifierNamespace keys a verifier's assignment to the author run and head
// it verifies, so the record finds its verdict without a table.
var verifierNamespace = uuid.MustParse("7d1f0c52-3b8e-4e0a-9a61-5c2d8e4b7f13")

// SliceRecord is the slice IR (#420): the typed record every station of a
// slice reads and writes. The author's run gives the ticket, the branch and
// the diff; the author states its decisions and open questions; the
// verifier station adds its verdict.
type SliceRecord struct {
	AssignmentID  string     `json:"assignment_id"`
	TicketURL     string     `json:"ticket_url"`
	Branch        string     `json:"branch"`
	Diff          SliceDiff  `json:"diff"`
	Decisions     []Decision `json:"decisions"`
	OpenQuestions []string   `json:"open_questions"`
	Verdict       *Verdict   `json:"verdict,omitempty"`
}

// SliceDiff is the change a slice makes: its merge base with the base
// branch, its head, and every path it touches.
type SliceDiff struct {
	Base  string   `json:"base"`
	Head  string   `json:"head"`
	Files []string `json:"files"`
}

// Decision is one choice the author made, with its reason.
type Decision struct {
	Statement string `json:"statement"`
	Reason    string `json:"reason"`
}

// VerifierAssignmentID is the verifier station's assignment for one author
// run at one head.
func VerifierAssignmentID(author string, head string) string {
	return uuid.NewSHA1(verifierNamespace, []byte(author+"@"+head)).String()
}

// ReadSliceRecord reads the slice record of the author run assignment in
// stateDirectory: its run state, its diff through git, and the verdict of
// the verifier station for its head when one has answered.
func ReadSliceRecord(ctx context.Context, launcher proc.ILauncher, stateDirectory string, assignment string) (SliceRecord, error) {
	state, err := ReadRunState(RunDirectory(stateDirectory, assignment))
	if err != nil {
		return SliceRecord{}, err
	}
	git := func(arguments ...string) (string, error) {
		result, err := launcher.Run(ctx, proc.Command{
			Executable: gitExecutable, Arguments: append([]string{gitDirectory, state.Worktree}, arguments...), Directory: state.Worktree,
		})
		if err != nil {
			return "", fmt.Errorf("harness session: slice record: git %s: %w", arguments[0], err)
		}
		return strings.TrimSpace(string(result.Stdout)), nil
	}
	record := SliceRecord{
		AssignmentID: assignment, TicketURL: state.TicketURL, Branch: state.Branch,
		Decisions: []Decision{}, OpenQuestions: []string{},
	}
	if record.Diff.Head, err = git(gitRevParse, state.Branch); err != nil {
		return SliceRecord{}, err
	}
	if record.Diff.Base, err = git(gitMergeBase, state.BaseBranch, record.Diff.Head); err != nil {
		return SliceRecord{}, err
	}
	files, err := git(gitDiff, gitNameOnly, record.Diff.Base, record.Diff.Head)
	if err != nil {
		return SliceRecord{}, err
	}
	record.Diff.Files = strings.Fields(files)
	record.Verdict, err = readVerdict(RunDirectory(stateDirectory, VerifierAssignmentID(assignment, record.Diff.Head)))
	return record, err
}

// readVerdict is the verdict of the verifier run in directory: none when it
// has not run or not answered yet.
func readVerdict(directory string) (*Verdict, error) {
	records, err := ReadRecords(directory, func(record *Record) bool { return record.EventType == EventTypeAssistant })
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	verdict, found, err := ParseVerdict(LastReply(records))
	if !found || err != nil {
		return nil, err
	}
	return &verdict, nil
}

// VerifierRecipe is the verifier station's recipe for a slice record: a
// session on the author's model and repository whose task is the record
// and whose one tool reads its diff, in a worktree at the record's head.
func VerifierRecipe(record SliceRecord, author *pb.AgentAssignmentRecipe) (*pb.AgentAssignmentRecipe, error) {
	statement := record
	statement.Verdict = nil
	content, err := json.MarshalIndent(statement, "", "  ")
	if err != nil {
		return nil, err
	}
	head := record.Diff.Head
	if len(head) > shortHead {
		head = head[:shortHead]
	}
	return &pb.AgentAssignmentRecipe{
		AssignmentId: VerifierAssignmentID(record.AssignmentID, record.Diff.Head),
		Agent: &pb.AgentDefinition{
			Id: VerifierAgentID, Revision: verifierAgentRevision, DisplayName: verifierDisplayName, Instructions: verifierInstructions,
		},
		TicketUrl:    record.TicketURL,
		Task:         fmt.Sprintf(verifierBriefFormat, content, record.Diff.Base, record.Diff.Head, VerdictFence, VerdictFence, VerdictSkeleton()),
		Model:        author.GetModel(),
		RepositoryId: author.GetRepositoryId(),
		Executor:     author.GetExecutor(),
		Workspace: &pb.AgentWorkspace{
			RepositoryPath:   author.GetWorkspace().GetRepositoryPath(),
			BaseBranch:       record.Diff.Head,
			Branch:           verifierBranchPrefix + record.Branch + "-" + head,
			AllowedTools:     []string{verifierTool},
			PullRequestTitle: verifierTitlePrefix + record.Branch,
		},
	}, nil
}

// VerdictSkeleton is a verdict with every field shown, as the verifier's
// task shows it.
func VerdictSkeleton() string {
	pass := false
	skeleton, _ := json.Marshal(Verdict{Pass: &pass, Defects: []Defect{{Statement: "", Field: "", Evidence: []string{""}}}})
	return string(skeleton)
}
