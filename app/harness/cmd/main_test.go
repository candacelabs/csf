// Copyright 2026 Candace Labs

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/services/harness/session"
)

func TestHarnessCommand(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "app/harness/cmd Suite")
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

	Describe("the view verb", func() {
		It("refuses -detach with -stop, a positional argument and a state directory that is not one", func() {
			launcher, err := proc.NewHostLauncher()
			Expect(err).NotTo(HaveOccurred())
			var output, diagnostics bytes.Buffer
			Expect(run(context.Background(), launcher, verbView, []string{"-detach", "-stop", "-state", directory}, nil, &output, &diagnostics)).To(MatchError(errViewConflict))
			Expect(run(context.Background(), launcher, verbView, []string{"extra"}, nil, &output, &diagnostics)).To(MatchError(errUsage))
			Expect(run(context.Background(), launcher, verbView, []string{"-state", filepath.Join(directory, "absent")}, nil, &output, &diagnostics)).To(MatchError(os.ErrNotExist))
		})

		It("reports that no view is running when asked to stop one", func() {
			var output bytes.Buffer
			Expect(stopView(directory, &output)).To(MatchError(errNoView))
			Expect(output.Len()).To(BeZero())
		})

		It("records a view host under its own file name", func() {
			Expect(writeHostRecord(directory, ViewRecordFile, hostRecord{PID: 7, Endpoint: "http://127.0.0.1:14121", Listen: []string{"127.0.0.1:14121"}})).To(Succeed())
			Expect(filepath.Join(directory, ViewRecordFile)).To(BeAnExistingFile())
			_, err := resolveEndpoint("", directory)
			Expect(err).To(MatchError(errNoHost), "the view's record is not the harness's")
		})
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
		Expect(gate(context.Background(), launcher, []string{session.HookPreToolUse, directory}, strings.NewReader("{}"), &output, &diagnostics)).To(Equal(exitBlocked))
		Expect(output.Len()).To(BeZero())
	})
})
