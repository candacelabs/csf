// Copyright 2026 Candace Labs

package verbs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/harness/session"
)

func TestCSFCommand(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "app/csf/cmd Suite")
}

const recipeWithBrief = `{
  "assignment_id": "0caf2d51-1ee7-468a-882f-ac478a437640",
  "agent": {"id": "scratch", "revision": 1, "display_name": "Scratch", "instructions": "Commit once."},
  "ticket_url": "https://example.invalid/issues/1",
  "model": "sonnet",
  "repository_id": "scratch",
  "workspace": {
    "repository_path": "/repository", "base_branch": "main", "branch": "h1/scratch",
    "brief_path": "brief.md", "allowed_tools": ["Bash", "Edit"], "pull_request_title": "H1 scratch"
  }
}`

var _ = Describe("the harness command", func() {
	var directory string

	BeforeEach(func() { directory = GinkgoT().TempDir() })

	write := func(name string, content string) string {
		path := filepath.Join(directory, name)
		Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
		return path
	}

	It("reads the task from the brief beside the recipe", func() {
		write("brief.md", "Add one line and commit.\n")
		recipe, err := readRecipe(write("agent.json", recipeWithBrief))
		Expect(err).NotTo(HaveOccurred())
		Expect(recipe.GetTask()).To(Equal("Add one line and commit.\n"))
		Expect(recipe.GetWorkspace().GetAllowedTools()).To(Equal([]string{"Bash", "Edit"}))
	})

	It("refuses a recipe with both a task and a brief", func() {
		write("brief.md", "brief")
		path := write("agent.json", strings.Replace(recipeWithBrief, `"model"`, `"task": "inline", "model"`, 1))
		_, err := readRecipe(path)
		Expect(err).To(MatchError(errBriefConflict))
	})

	Describe("the message of a send", func() {
		It("comes from the flag, the file or standard input", func() {
			text, err := messageText("hello", "", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(text).To(Equal("hello"))
			text, err = messageText("", write("message.txt", "from a file"), nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(text).To(Equal("from a file"))
			text, err = messageText("", "-", strings.NewReader("from stdin"))
			Expect(err).NotTo(HaveOccurred())
			Expect(text).To(Equal("from stdin"))
		})

		It("refuses a message given twice, and none at all", func() {
			_, err := messageText("a", "b", nil)
			Expect(err).To(MatchError(errMessageBoth))
			_, err = messageText("", "", nil)
			Expect(err).To(MatchError(errNoMessage))
		})
	})

	Describe("the endpoint of a client verb", func() {
		It("prefers the flag", func() {
			endpoint, err := resolveEndpoint("http://127.0.0.1:1", directory)
			Expect(err).NotTo(HaveOccurred())
			Expect(endpoint).To(Equal("http://127.0.0.1:1"))
		})

		It("reads the running host's record", func() {
			Expect(writeHostRecord(directory, HostRecordFile, hostRecord{PID: 7, Endpoint: "http://127.0.0.1:14120", Listen: []string{"127.0.0.1:14120"}})).To(Succeed())
			endpoint, err := resolveEndpoint("", directory)
			Expect(err).NotTo(HaveOccurred())
			Expect(endpoint).To(Equal("http://127.0.0.1:14120"))
		})

		It("reports that no host is running", func() {
			_, err := resolveEndpoint("", directory)
			Expect(err).To(MatchError(errNoHost))
		})
	})

	It("rejects an unknown verb and a verb without its arguments", func() {
		launcher, err := proc.NewHostLauncher()
		Expect(err).NotTo(HaveOccurred())
		var output, diagnostics bytes.Buffer
		Expect(run(context.Background(), launcher, "dance", nil, nil, &output, &diagnostics)).To(MatchError(errUsage))
		Expect(run(context.Background(), launcher, verbSend, []string{"-endpoint", "http://127.0.0.1:1"}, nil, &output, &diagnostics)).To(MatchError(errNoAssignment))
		Expect(run(context.Background(), launcher, verbSubmit, []string{"-endpoint", "http://127.0.0.1:1"}, nil, &output, &diagnostics)).To(MatchError(errUsage))
	})

	Describe("the init verb's sample", func() {
		It("fills the recipe for the repository and its branch, beside the brief", func() {
			assignment, err := writeSample(directory, "trunk")
			Expect(err).NotTo(HaveOccurred())
			recipe, err := readRecipe(filepath.Join(directory, SampleDirectory, recipeFile))
			Expect(err).NotTo(HaveOccurred())
			Expect(recipe.GetAssignmentId()).To(Equal(assignment))
			Expect(recipe.GetRepositoryId()).To(Equal(filepath.Base(directory)))
			Expect(recipe.GetWorkspace().GetRepositoryPath()).To(Equal(directory))
			Expect(recipe.GetWorkspace().GetBaseBranch()).To(Equal("trunk"))
			Expect(recipe.GetWorkspace().GetBranch()).To(Equal("csf/sample-" + assignment[:8]))
			Expect(recipe.GetTask()).To(ContainSubstring("CSF_SAMPLE.md"))
		})

		It("writes a fresh assignment each time", func() {
			first, err := writeSample(directory, "main")
			Expect(err).NotTo(HaveOccurred())
			second, err := writeSample(directory, "main")
			Expect(err).NotTo(HaveOccurred())
			Expect(second).NotTo(Equal(first))
		})
	})

	Describe("the chat verb", func() {
		It("prints the session's chat address on the recorded host", func() {
			Expect(writeHostRecord(directory, HostRecordFile, hostRecord{PID: 7, Endpoint: "http://127.0.0.1:14120", Listen: []string{"127.0.0.1:14120"}})).To(Succeed())
			var output bytes.Buffer
			Expect(chatAddress([]string{"-state", directory, "-assignment", "abc"}, &output)).To(Succeed())
			Expect(output.String()).To(Equal("http://127.0.0.1:14120/chat/abc\n"))
		})

		It("refuses a missing assignment and a missing host", func() {
			var output bytes.Buffer
			Expect(chatAddress([]string{"-state", directory}, &output)).To(MatchError(errNoAssignment))
			Expect(chatAddress([]string{"-state", directory, "-assignment", "abc"}, &output)).To(MatchError(errNoHost))
			Expect(output.Len()).To(BeZero())
		})
	})

	Describe("following a session's events", func() {
		latest := func(turns int64, err error) func(ctx context.Context) (int64, error) {
			return func(ctx context.Context) (int64, error) { return turns, err }
		}
		finished := func(turn int) string {
			return fmt.Sprintf(`{"event_type": %q, "turn": %d}`, session.EventTypeRunFinished, turn)
		}

		It("stops at the latest turn's finish", func() {
			done, err := latestTurnFinished(context.Background(), finished(1), latest(1, nil))
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
		})

		It("keeps following past an earlier turn's finish while a newer turn runs", func() {
			done, err := latestTurnFinished(context.Background(), finished(1), latest(2, nil))
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
		})

		It("keeps following every other record without asking the host", func() {
			done, err := latestTurnFinished(context.Background(), `{"event_type": "session_gate_decision", "turn": 1}`, latest(0, errNoHost))
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
		})

		It("reports a host that cannot say the latest turn", func() {
			_, err := latestTurnFinished(context.Background(), finished(1), latest(0, errNoHost))
			Expect(err).To(MatchError(errNoHost))
		})
	})

	It("names init first in its usage", func() {
		Expect(errUsage.Error()).To(HavePrefix("usage: csf init|"))
	})

	It("blocks a gate call it cannot answer", func() {
		launcher, err := proc.NewHostLauncher()
		Expect(err).NotTo(HaveOccurred())
		var output, diagnostics bytes.Buffer
		Expect(gate(context.Background(), launcher, []string{session.HookPreToolUse}, strings.NewReader("{}"), &output, &diagnostics)).To(Equal(exitBlocked))
		Expect(gate(context.Background(), launcher, []string{session.HookPreToolUse, directory}, strings.NewReader("{"), &output, &diagnostics)).To(Equal(exitBlocked))
		Expect(output.Len()).To(BeZero())
	})

	It("answers in operator mode for a directory with no recorded run", func() {
		launcher, err := proc.NewHostLauncher()
		Expect(err).NotTo(HaveOccurred())
		var output, diagnostics bytes.Buffer
		Expect(gate(context.Background(), launcher, []string{session.HookPreToolUse, directory}, strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`), &output, &diagnostics)).To(BeZero())
		Expect(output.Len()).To(BeZero())
	})
})

// writeSession seeds one session in a state root as the harness writes it: the
// run record beside the event log, one JSON record a line.
func writeSession(state, assignment string, run session.RunState, records ...session.Record) {
	Expect(os.MkdirAll(filepath.Join(state, assignment), 0o700)).To(Succeed())
	content, err := json.Marshal(run)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(state, assignment, session.RunStateFile), content, 0o600)).To(Succeed())
	if len(records) == 0 {
		return
	}
	log := &strings.Builder{}
	for _, record := range records {
		line, err := json.Marshal(record)
		Expect(err).NotTo(HaveOccurred())
		fmt.Fprintln(log, string(line))
	}
	Expect(os.WriteFile(filepath.Join(state, assignment, session.EventsFile), []byte(log.String()), 0o600)).To(Succeed())
}

var _ = Describe("the observe verbs", func() {
	var (
		state    string
		launcher proc.ILauncher
		output   bytes.Buffer
		at       time.Time
	)

	BeforeEach(func() {
		state = GinkgoT().TempDir()
		var err error
		launcher, err = proc.NewHostLauncher()
		Expect(err).NotTo(HaveOccurred())
		output.Reset()
		at = time.Date(2026, time.October, 6, 0, 27, 31, 0, time.UTC)
		writeSession(state, "a1", session.RunState{AssignmentID: "a1", AgentID: "alpha"},
			session.Record{Time: at, EventType: session.EventTypeRunStarted},
			session.Record{Time: at, EventType: session.EventTypeControlAction, Action: session.ActionMerge},
		)
		writeSession(state, "b2", session.RunState{AssignmentID: "b2", AgentID: "beta"},
			session.Record{Time: at.Add(time.Minute), EventType: session.EventTypeRunStarted},
			session.Record{Time: at.Add(time.Minute), EventType: session.EventTypeTurnRequested, Turn: 2},
		)
	})

	It("shows every session in one table, labelled and never as JSON", func() {
		Expect(statusVerb(context.Background(), launcher, []string{"-state", state}, &output)).To(Succeed())
		Expect(output.String()).To(ContainSubstring("AGENT"))
		Expect(output.String()).To(ContainSubstring("alpha"))
		Expect(output.String()).To(ContainSubstring("beta"))
		Expect(output.String()).NotTo(ContainSubstring("{"))
	})

	It("shows only the agents it is asked for", func() {
		Expect(statusVerb(context.Background(), launcher, []string{"-state", state, "beta"}, &output)).To(Succeed())
		Expect(output.String()).To(ContainSubstring("beta"))
		Expect(output.String()).NotTo(ContainSubstring("alpha"))
	})

	It("tails every session as readable lines labelled by agent", func() {
		Expect(tailVerb([]string{"-state", state}, &output)).To(Succeed())
		Expect(output.String()).To(Equal(strings.Join([]string{
			"00:27:31 alpha turn session started",
			"00:27:31 alpha merge pull request merged",
			"00:28:31 beta turn session started",
			"00:28:31 beta turn turn 2",
		}, "\n") + "\n"))
		Expect(output.String()).NotTo(ContainSubstring("{"))
	})

	It("tails one agent, and only the kinds it is asked for", func() {
		Expect(tailVerb([]string{"-state", state, "-kinds", "merge", "-n", "3", "alpha"}, &output)).To(Succeed())
		Expect(output.String()).To(Equal("00:27:31 alpha merge pull request merged\n"))
	})

	It("keeps only the last n lines", func() {
		Expect(tailVerb([]string{"-state", state, "-n", "1"}, &output)).To(Succeed())
		Expect(output.String()).To(Equal("00:28:31 beta turn turn 2\n"))
	})

	It("refuses an unknown kind and a negative count", func() {
		Expect(tailVerb([]string{"-state", state, "-kinds", "bogus"}, &output)).To(MatchError(ContainSubstring("unknown kind")))
		Expect(tailVerb([]string{"-state", state, "-n", "-1"}, &output)).To(MatchError(ContainSubstring("negative")))
	})
})
