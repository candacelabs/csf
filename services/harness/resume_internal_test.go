// Copyright 2026 Candace Labs

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/harness/session"
)

// restartingHost is the host a restart resumes onto: every resumed session
// adds a burst of one runnable thread to the load, as the launch check's
// derivation assumes, and each retry interval halves the burst as the
// one-minute average decays.
type restartingHost struct {
	cores    int
	baseline float64
	burst    float64
}

func (host *restartingHost) Cores() int                                      { return host.cores }
func (host *restartingHost) LoadAverage() (float64, error)                   { return host.baseline + host.burst, nil }
func (host *restartingHost) FreeBytes(path string) (uint64, error)           { return 64 << 30, nil }
func (host *restartingHost) DirectoryBytes(directory string) (uint64, error) { return 1 << 20, nil }
func (host *restartingHost) Pressure(resource proc.PressureResource) (proc.Pressure, error) {
	return proc.Pressure{}, nil
}
func (host *restartingHost) MemoryAvailable() (uint64, error) { return 64 << 30, nil }

// Thirty open runs on 32 cores, the scale of the 06:57Z restart, with an
// idle baseline that leaves room for all of them once the bursts decay.
const (
	restartRuns     = 30
	restartCores    = 32
	restartBaseline = 0.5
	largestRunBytes = 1 << 20
	maximumPasses   = 200
	// restartState is the state directory the fake host is asked about; it
	// never touches the disk.
	restartState = "/state"
)

var _ = Describe("resumeInOrder", func() {
	var runs []openRun

	BeforeEach(func() {
		runs = nil
		for index := range restartRuns {
			runs = append(runs, openRun{assignment: fmt.Sprintf("run-%02d", index)})
		}
	})

	It("keeps a restart with 30 open runs under the derived worker cap at every admission, and resumes all of them in order", func() {
		host := &restartingHost{cores: restartCores, baseline: restartBaseline}
		running := 0
		var admitted []string
		var queues []ResumeQueue
		passes := 0
		resumeInOrder(context.Background(), runs, func() time.Time { return time.Unix(int64(passes), 0) },
			func(_ context.Context, run openRun) error {
				check := launchCheck(host, restartState, uint32(running), largestRunBytes)
				if !check.GetAdmitted() {
					return fmt.Errorf("%w: %s", ErrResumeHeld, strings.Join(check.GetFindings(), "; "))
				}
				Expect(uint32(running+1)).To(BeNumerically("<=", check.GetWorkerCap()), "no resume beyond the cap derived at its admission")
				running++
				host.burst++
				admitted = append(admitted, run.assignment)
				return nil
			},
			func(_ context.Context) bool {
				passes++
				host.burst /= 2
				return passes < maximumPasses
			},
			func(queue ResumeQueue) { queues = append(queues, queue) },
			func(run openRun, err error) { Fail(fmt.Sprintf("%s failed: %v", run.assignment, err)) })

		Expect(admitted).To(HaveLen(restartRuns), "every open run is back")
		for index, assignment := range admitted {
			Expect(assignment).To(Equal(runs[index].assignment), "resumed in resume order")
		}
		Expect(len(queues)).To(BeNumerically(">", 1), "the restart was staggered over passes, not resumed at once")
		first := queues[0]
		Expect(first.Resumed).To(BeNumerically("<", restartRuns))
		Expect(first.Held).NotTo(BeEmpty())
		Expect(first.Held[0].Position).To(Equal(1))
		Expect(first.Held[0].AssignmentID).To(Equal(runs[first.Resumed].assignment), "the first held run is next in order")
		Expect(first.Held[0].Reason).To(ContainSubstring("leaves room for"))
		Expect(queues[len(queues)-1].Held).To(BeEmpty(), "the last record lists nothing held")
		Expect(queues[len(queues)-1].Resumed).To(Equal(restartRuns))
	})

	It("holds every run while free disk is below the floor, and stops when told to", func() {
		host := &restartingHost{cores: restartCores, baseline: restartBaseline}
		var queues []ResumeQueue
		resumeInOrder(context.Background(), runs, time.Now,
			func(_ context.Context, run openRun) error {
				check := launchCheck(lowDisk{host}, restartState, 0, largestRunBytes)
				if !check.GetAdmitted() {
					return fmt.Errorf("%w: %s", ErrResumeHeld, strings.Join(check.GetFindings(), "; "))
				}
				return nil
			},
			func(_ context.Context) bool { return false },
			func(queue ResumeQueue) { queues = append(queues, queue) },
			func(run openRun, err error) { Fail(err.Error()) })

		Expect(queues).To(HaveLen(1))
		Expect(queues[0].Resumed).To(BeZero())
		Expect(queues[0].Held).To(HaveLen(restartRuns))
		Expect(queues[0].Held[restartRuns-1].Position).To(Equal(restartRuns))
		Expect(queues[0].Held[0].Reason).To(ContainSubstring("below the floor"))
	})

	It("drops a run that fails for another reason and goes on with the next", func() {
		var failed []string
		resumed := 0
		resumeInOrder(context.Background(), runs[:3], time.Now,
			func(_ context.Context, run openRun) error {
				if run.assignment == runs[1].assignment {
					return os.ErrNotExist
				}
				resumed++
				return nil
			},
			func(_ context.Context) bool { return true },
			func(queue ResumeQueue) {},
			func(run openRun, err error) { failed = append(failed, run.assignment) })

		Expect(resumed).To(Equal(2))
		Expect(failed).To(Equal([]string{runs[1].assignment}))
	})
})

// lowDisk is a host whose free disk is below the floor.
type lowDisk struct{ *restartingHost }

func (lowDisk) FreeBytes(path string) (uint64, error) { return 1, nil }

var _ = Describe("listOpenRuns", func() {
	writeRun := func(state string, assignment string, queue string, modified time.Time) {
		directory := filepath.Join(state, assignment)
		Expect(os.MkdirAll(directory, 0o700)).To(Succeed())
		recipe, err := json.Marshal(map[string]any{"assignmentId": assignment})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(directory, session.RecipeFile), recipe, 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, session.RunStateFile), []byte("{}"), 0o600)).To(Succeed())
		Expect(os.Chtimes(filepath.Join(directory, session.RunStateFile), modified, modified)).To(Succeed())
		if queue != "" {
			Expect(os.WriteFile(filepath.Join(directory, QueueFile), []byte(queue), 0o600)).To(Succeed())
			Expect(os.Chtimes(filepath.Join(directory, QueueFile), modified, modified)).To(Succeed())
		}
	}

	It("orders runs with a turn running or queued first, oldest first, then idle runs, and skips ended runs", func() {
		state := GinkgoT().TempDir()
		base := time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC)
		writeRun(state, "idle-old", "", base)
		writeRun(state, "active-new", `{"inflight":{"text":"work"},"queued":[]}`, base.Add(3*time.Minute))
		writeRun(state, "queued-old", `{"queued":["next"]}`, base.Add(time.Minute))
		writeRun(state, "idle-empty-queue", `{"queued":[]}`, base.Add(2*time.Minute))
		writeRun(state, "ended", "", base)
		Expect(os.WriteFile(filepath.Join(state, "ended", EndedFile), []byte("CANCELED\n"), 0o600)).To(Succeed())

		runs, err := listOpenRuns(state)

		Expect(err).NotTo(HaveOccurred())
		var order []string
		for _, run := range runs {
			order = append(order, run.assignment)
		}
		Expect(order).To(Equal([]string{"queued-old", "active-new", "idle-old", "idle-empty-queue"}))
	})
})
