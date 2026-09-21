package terminaladapter_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/candacelabs/csf/pkg/patience"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	"github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/terminaladapter"
)

const (
	testShellPath              = "/bin/sh"
	testScriptShebang          = "#!" + testShellPath + "\n"
	testTerminalOutputMarker   = "terminal-output-that-is-deliberately-longer-than-the-replay-boundary"
	testTerminalOutputCommand  = "printf '" + testTerminalOutputMarker + "\\n'\n"
	testWakeOutputCommand      = "printf 'wake-terminal\\n'\n"
	testExitSevenCommand       = "exit 7\n"
	testBackgroundChildCommand = "sleep 30 & echo CHILD:$!\n"
	testExitZeroCommand        = "exit 0\n"
	testSplitUTF8ShellScript   = testScriptShebang + "printf '\\342\\202'\nsleep 0.05\nprintf '\\254'\nsleep 0.05\nprintf '\\377'\n"
	testExitNineShellScript    = testScriptShebang + "exit 9\n"
	testExitZeroShellScript    = testScriptShebang + testExitZeroCommand
	testUTF8OutputName         = "utf8-output"
	testExitNowName            = "exit-now"
	testBoundedUTF8OutputName  = "bounded-utf8-output"
	testChildOutputPattern     = `CHILD:([0-9]+)`
	testBoundedShellScript     = testScriptShebang + "printf '%s'\n"
)

var terminalBudget = patience.Budget{Within: 5 * time.Second, Interval: 10 * time.Millisecond}

var _ = Describe("TerminalManager", func() {
	It("owns a PTY through input, replay, resize, exit and cleanup", func() {
		manager, err := terminaladapter.NewTerminalManager(terminaladapter.Config{Shell: testShellPath, ReplayBytes: 64, ExitedHistoryLimit: 2})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(manager.Close)
		worktreeID := uuid.New()
		created, err := manager.Create(context.Background(), copilotadapter.TerminalSpec{
			WorktreeID: worktreeID, Directory: GinkgoT().TempDir(), Rows: 24, Columns: 80,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Status).To(Equal(string(api.TerminalStatusRunning)))
		Expect(manager.List(worktreeID)).To(ContainElement(HaveField("ID", Equal(created.ID))))

		resized, err := manager.Resize(created.ID, 40, 120)
		Expect(err).NotTo(HaveOccurred())
		Expect(resized.Rows).To(BeEquivalentTo(40))
		Expect(resized.Columns).To(BeEquivalentTo(120))
		_, err = manager.Resize(created.ID, 0, 80)
		Expect(err).To(MatchError(ContainSubstring("positive")))
		_, err = manager.Write(created.ID, "")
		Expect(err).To(MatchError(ContainSubstring("must not be empty")))
		_, err = manager.Write(created.ID, strings.Repeat("x", 65537))
		Expect(err).To(MatchError(ContainSubstring("exceeds 65536 characters")))

		marker := testTerminalOutputMarker
		_, err = manager.Write(created.ID, testTerminalOutputCommand)
		Expect(err).NotTo(HaveOccurred())
		output := patience.Await(GinkgoT(), "terminal output", terminalBudget, func() string {
			replay, _ := manager.EventsAfter(created.ID, 0)
			var output strings.Builder
			for _, event := range replay.Events {
				output.WriteString(event.Data)
			}
			return output.String()
		}, func(output string) bool { return strings.Contains(output, marker[len(marker)-32:]) })
		Expect(output).To(ContainSubstring(marker[len(marker)-32:]))

		replay, found := manager.EventsAfter(created.ID, 0)
		Expect(found).To(BeTrue())
		Expect(replay.Events).NotTo(BeEmpty())
		Expect(replay.Events[0].ReplayTruncated).To(BeTrue())
		_, err = manager.Write(created.ID, testExitSevenCommand)
		Expect(err).NotTo(HaveOccurred())
		status := patience.Await(GinkgoT(), "terminal natural exit", terminalBudget, func() string {
			snapshot, _ := manager.Get(created.ID)
			return snapshot.Status
		}, func(status string) bool { return status == string(api.TerminalStatusExited) })
		Expect(status).To(Equal(string(api.TerminalStatusExited)))
		stopped, err := manager.Stop(created.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(stopped.ExitCode).To(PointTo(BeEquivalentTo(7)))
		Expect(manager.Close()).To(Succeed())
	})

	It("wakes every subscriber when output arrives after an atomic replay read", func() {
		manager, err := terminaladapter.NewTerminalManager(terminaladapter.Config{Shell: testShellPath, ReplayBytes: 4096, ExitedHistoryLimit: 2})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(manager.Close)
		created, err := manager.Create(context.Background(), copilotadapter.TerminalSpec{
			WorktreeID: uuid.New(), Directory: GinkgoT().TempDir(), Rows: 24, Columns: 80,
		})
		Expect(err).NotTo(HaveOccurred())
		first, found := manager.EventsAfter(created.ID, 0)
		Expect(found).To(BeTrue())
		second, found := manager.EventsAfter(created.ID, 0)
		Expect(found).To(BeTrue())
		Expect(first.Changed).NotTo(BeNil())
		_, err = manager.Write(created.ID, testWakeOutputCommand)
		Expect(err).NotTo(HaveOccurred())
		Eventually(first.Changed).WithTimeout(terminalBudget.Within).Should(BeClosed())
		Eventually(second.Changed).WithTimeout(terminalBudget.Within).Should(BeClosed())
		latest, found := manager.EventsAfter(created.ID, 0)
		Expect(found).To(BeTrue())
		Expect(latest.Changed).NotTo(Equal(first.Changed))
		Expect(latest.Events).NotTo(BeEmpty())
	})

	It("preserves UTF-8 characters split across PTY reads and sanitizes malformed output", func() {
		directory := GinkgoT().TempDir()
		shell := filepath.Join(directory, testUTF8OutputName)
		body := testSplitUTF8ShellScript
		Expect(os.WriteFile(shell, []byte(body), 0o700)).To(Succeed())
		manager, err := terminaladapter.NewTerminalManager(terminaladapter.Config{
			Shell: shell, ReplayBytes: 64, ExitedHistoryLimit: 2,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(manager.Close)
		created, err := manager.Create(context.Background(), copilotadapter.TerminalSpec{
			WorktreeID: uuid.New(), Directory: directory, Rows: 24, Columns: 80,
		})
		Expect(err).NotTo(HaveOccurred())
		patience.Await(GinkgoT(), "UTF-8 terminal exit", terminalBudget, func() string {
			snapshot, _ := manager.Get(created.ID)
			return snapshot.Status
		}, func(status string) bool { return status == string(api.TerminalStatusExited) })

		replay, found := manager.EventsAfter(created.ID, 0)
		Expect(found).To(BeTrue())
		var output strings.Builder
		for _, event := range replay.Events {
			Expect(utf8.ValidString(event.Data)).To(BeTrue())
			if event.Kind == string(api.TerminalEventKindOutput) {
				output.WriteString(event.Data)
			}
		}
		Expect(output.String()).To(Equal("€�"))
	})

	It("publishes the terminal snapshot and terminal event in one replay read", func() {
		directory := GinkgoT().TempDir()
		shell := filepath.Join(directory, testExitNowName)
		Expect(os.WriteFile(shell, []byte(testExitNineShellScript), 0o700)).To(Succeed())
		manager, err := terminaladapter.NewTerminalManager(terminaladapter.Config{
			Shell: shell, ReplayBytes: 128, ExitedHistoryLimit: 2,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(manager.Close)
		created, err := manager.Create(context.Background(), copilotadapter.TerminalSpec{
			WorktreeID: uuid.New(), Directory: directory, Rows: 24, Columns: 80,
		})
		Expect(err).NotTo(HaveOccurred())

		replay := patience.Await(GinkgoT(), "terminal replay", terminalBudget, func() copilotadapter.TerminalEventReplay {
			replay, _ := manager.EventsAfter(created.ID, 0)
			return replay
		}, func(replay copilotadapter.TerminalEventReplay) bool {
			if replay.Snapshot.Status != string(api.TerminalStatusExited) && replay.Snapshot.Status != string(api.TerminalStatusFailed) {
				return false
			}
			for _, event := range replay.Events {
				if event.Kind == replay.Snapshot.Status {
					return true
				}
			}
			return false
		})
		terminalEvent := replay.Events[len(replay.Events)-1]
		Expect(replay.Snapshot.Status).To(Equal(terminalEvent.Kind))
		Expect(replay.Snapshot.ExitCode).To(Equal(terminalEvent.ExitCode))
	})

	DescribeTable("keeps only complete UTF-8 runes inside the replay byte bound", func(replayBytes int, escapedOutput string, expected string) {
		directory := GinkgoT().TempDir()
		shell := filepath.Join(directory, testBoundedUTF8OutputName)
		Expect(os.WriteFile(shell, []byte(fmt.Sprintf(testBoundedShellScript, escapedOutput)), 0o700)).To(Succeed())
		manager, err := terminaladapter.NewTerminalManager(terminaladapter.Config{
			Shell: shell, ReplayBytes: replayBytes, ExitedHistoryLimit: 2,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(manager.Close)
		created, err := manager.Create(context.Background(), copilotadapter.TerminalSpec{
			WorktreeID: uuid.New(), Directory: directory, Rows: 24, Columns: 80,
		})
		Expect(err).NotTo(HaveOccurred())
		patience.Await(GinkgoT(), "bounded UTF-8 terminal exit", terminalBudget, func() string {
			snapshot, _ := manager.Get(created.ID)
			return snapshot.Status
		}, func(status string) bool { return status == string(api.TerminalStatusExited) })

		replay, found := manager.EventsAfter(created.ID, 0)
		Expect(found).To(BeTrue())
		Expect(replay.Events).NotTo(BeEmpty())
		Expect(replay.Events[0].ReplayTruncated).To(BeTrue())
		var output strings.Builder
		retainedBytes := 0
		for _, event := range replay.Events {
			Expect(utf8.ValidString(event.Data)).To(BeTrue())
			retainedBytes += len(event.Data)
			if event.Kind == string(api.TerminalEventKindOutput) {
				output.WriteString(event.Data)
			}
		}
		Expect(retainedBytes).To(BeNumerically("<=", replayBytes))
		Expect(output.String()).To(Equal(expected))
	},
		Entry("when only an ASCII suffix fits", 3, "\\342\\202\\254a", "a"),
		Entry("when no complete suffix rune fits", 1, "\\342\\202\\254", ""),
	)

	It("lists terminal sessions newest first", func() {
		manager, err := terminaladapter.NewTerminalManager(terminaladapter.Config{
			Shell: testShellPath, ReplayBytes: 128, ExitedHistoryLimit: 3,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(manager.Close)
		worktreeID := uuid.New()
		created := make([]copilotadapter.TerminalSnapshot, 0, 3)
		for range 3 {
			terminal, createErr := manager.Create(context.Background(), copilotadapter.TerminalSpec{
				WorktreeID: worktreeID, Directory: GinkgoT().TempDir(), Rows: 24, Columns: 80,
			})
			Expect(createErr).NotTo(HaveOccurred())
			created = append(created, terminal)
		}
		listed := manager.List(worktreeID)
		Expect(listed).To(HaveLen(3))
		Expect(listed[0].ID).To(Equal(created[2].ID))
		Expect(listed[1].ID).To(Equal(created[1].ID))
		Expect(listed[2].ID).To(Equal(created[0].ID))
	})

	It("kills the shell process group before closing its PTY", func() {
		manager, err := terminaladapter.NewTerminalManager(terminaladapter.Config{
			Shell: testShellPath, ReplayBytes: 4096, ExitedHistoryLimit: 2,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(manager.Close)
		created, err := manager.Create(context.Background(), copilotadapter.TerminalSpec{
			WorktreeID: uuid.New(), Directory: GinkgoT().TempDir(), Rows: 24, Columns: 80,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = manager.Write(created.ID, testBackgroundChildCommand)
		Expect(err).NotTo(HaveOccurred())
		pattern := regexp.MustCompile(testChildOutputPattern)
		child := patience.Await(GinkgoT(), "child process id", terminalBudget, func() int {
			replay, _ := manager.EventsAfter(created.ID, 0)
			var output strings.Builder
			for _, event := range replay.Events {
				output.WriteString(event.Data)
			}
			match := pattern.FindStringSubmatch(output.String())
			if len(match) != 2 {
				return 0
			}
			identifier, _ := strconv.Atoi(match[1])
			return identifier
		}, func(identifier int) bool { return identifier > 0 })
		_, err = manager.Stop(created.ID)
		Expect(err).NotTo(HaveOccurred())
		gone := patience.Await(GinkgoT(), "terminal child process cleanup", terminalBudget, func() bool {
			err := syscall.Kill(child, 0)
			if errors.Is(err, syscall.ESRCH) {
				return true
			}
			body, readErr := os.ReadFile("/proc/" + strconv.Itoa(child) + "/stat")
			return os.IsNotExist(readErr) || bytes.Contains(body, []byte(") Z "))
		}, func(gone bool) bool { return gone })
		Expect(gone).To(BeTrue())
	})

	It("bounds exited terminal replay histories", func() {
		manager, err := terminaladapter.NewTerminalManager(terminaladapter.Config{
			Shell: testShellPath, ReplayBytes: 128, ExitedHistoryLimit: 2,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(manager.Close)
		identifiers := make([]uuid.UUID, 0, 3)
		for range 3 {
			created, createErr := manager.Create(context.Background(), copilotadapter.TerminalSpec{
				WorktreeID: uuid.New(), Directory: GinkgoT().TempDir(), Rows: 24, Columns: 80,
			})
			Expect(createErr).NotTo(HaveOccurred())
			identifiers = append(identifiers, created.ID)
			_, writeErr := manager.Write(created.ID, testExitZeroCommand)
			Expect(writeErr).NotTo(HaveOccurred())
			patience.Await(GinkgoT(), "terminal retained after exit", terminalBudget, func() string {
				snapshot, _ := manager.Get(created.ID)
				return snapshot.Status
			}, func(status string) bool { return status == string(api.TerminalStatusExited) })
		}
		_, oldestRetained := manager.Get(identifiers[0])
		Expect(oldestRetained).To(BeFalse())
		_, newestRetained := manager.Get(identifiers[2])
		Expect(newestRetained).To(BeTrue())
	})

	It("applies retention before immediately exiting terminals are done", func() {
		directory := GinkgoT().TempDir()
		shell := filepath.Join(directory, testExitNowName)
		Expect(os.WriteFile(shell, []byte(testExitZeroShellScript), 0o700)).To(Succeed())
		manager, err := terminaladapter.NewTerminalManager(terminaladapter.Config{
			Shell: shell, ReplayBytes: 128, ExitedHistoryLimit: 2,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(manager.Close)
		worktreeID := uuid.New()
		for range 12 {
			_, createErr := manager.Create(context.Background(), copilotadapter.TerminalSpec{
				WorktreeID: worktreeID, Directory: directory, Rows: 24, Columns: 80,
			})
			Expect(createErr).NotTo(HaveOccurred())
		}
		Expect(manager.Close()).To(Succeed())
		retained := manager.List(worktreeID)
		Expect(retained).To(HaveLen(2))
		Expect(retained).To(HaveEach(HaveField("Status", Equal(string(api.TerminalStatusExited)))))
	})
})
