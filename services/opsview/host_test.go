// Copyright 2026 Candace Labs

package opsview_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing/fstest"
	"time"

	"github.com/moby/moby/api/types/container"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/kernel/clock"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/opsview"
)

// hostFixture is a machine of six containers over a state directory with one
// run directory: two CSF work containers (the session's, by its label, and a
// build container mounting its worktree), three others running and one
// stopped, and a container whose label names no run, which is not CSF work.
type hostFixture struct {
	state      string
	engine     *hostEngine
	operations *opsview.HostOperations
}

const hostRun = "run-a"

func newHostFixture(options ...opsview.HostOperationsOption) *hostFixture {
	GinkgoHelper()
	state := GinkgoT().TempDir()
	Expect(os.MkdirAll(filepath.Join(state, hostRun, session.WorktreeDirectory), 0o700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(state, hostRun, session.RunStateFile), []byte("{}"), 0o600)).To(Succeed())
	double, engine := newHostEngine(gomock.NewController(GinkgoT()),
		hostContainer("session-a", container.StateRunning, hostRun, ""),
		hostContainer("build-a", container.StateRunning, "", filepath.Join(state, hostRun, session.WorktreeDirectory)),
		hostContainer("homepage", container.StateRunning, "", ""),
		hostContainer("runner", container.StateRunning, "", ""),
		hostContainer("stray", container.StateRunning, "not-a-run", ""),
		hostContainer("cache", container.StateExited, "", ""),
	)
	files, err := iofs.NewHostFiles(state)
	Expect(err).NotTo(HaveOccurred())
	processes := fstest.MapFS{
		"loadavg": {Data: []byte("67.10 50.00 40.00 3/900 1234\n")},
		"meminfo": {Data: []byte("MemTotal:       65536000 kB\nMemFree:  1000 kB\nMemAvailable:   32768000 kB\n")},
		// The 06:57Z restart's pressure; io is absent, as on a kernel without it.
		"pressure/cpu":    {Data: []byte("some avg10=36.00 avg60=20.10 avg300=8.00 total=123456\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")},
		"pressure/memory": {Data: []byte("some avg10=0.08 avg60=0.02 avg300=0.00 total=99\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")},
	}
	operations, err := opsview.NewHostOperations(append([]opsview.HostOperationsOption{
		opsview.WithHostContainers(double), opsview.WithProcessTable(processes), opsview.WithHostState(files),
		opsview.WithHostClock(clock.NewManualClock(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC))),
	}, options...)...)
	Expect(err).NotTo(HaveOccurred())
	return &hostFixture{state: state, engine: engine, operations: operations}
}

var _ = Describe("The host operations", func() {
	ctx := context.Background()

	It("groups CSF work from the harness's records and reads the gauges", func() {
		fixture := newHostFixture()
		report, err := fixture.operations.Host(ctx, opsview.GetHostInput{})
		Expect(err).NotTo(HaveOccurred())
		groups := map[string]opsview.HostGroup{}
		for _, found := range report.Containers {
			groups[found.Name] = found.Group
		}
		Expect(groups).To(Equal(map[string]opsview.HostGroup{
			"session-a": opsview.GroupCSF, "build-a": opsview.GroupCSF,
			"homepage": opsview.GroupOther, "runner": opsview.GroupOther, "stray": opsview.GroupOther, "cache": opsview.GroupOther,
		}))
		Expect(report.Gauges.LoadOneMinute).To(Equal(67.1))
		Expect(report.Gauges.MemoryAvailableBytes).To(Equal(uint64(32768000 * 1024)))
		Expect(report.Gauges.DiskTotalBytes).To(BeNumerically(">", 0))
		Expect(report.Settings.Profiles).To(ConsistOf(opsview.HostProfile{Name: opsview.OnlyCSFWork, KeepCSFWork: true, StopOthers: true}))
	})

	It("reads the pressure gauges beside load, and the resume queue the last restart recorded", func() {
		fixture := newHostFixture()
		queue := harness.ResumeQueue{Resumed: 2, Held: []harness.HeldResume{{AssignmentID: hostRun, Position: 1, Reason: "load 54.55 on 32 cores leaves room for 1 sessions; 22 running"}}}
		content, err := json.Marshal(queue)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(fixture.state, harness.ResumeQueueFile), content, 0o600)).To(Succeed())

		report, err := fixture.operations.Host(ctx, opsview.GetHostInput{})

		Expect(err).NotTo(HaveOccurred())
		Expect(report.Gauges.PressureCPU).To(Equal(36.0))
		Expect(report.Gauges.PressureMemory).To(Equal(0.08))
		Expect(report.Gauges.PressureIO).To(Equal(-1.0), "a resource the kernel reports no pressure for is unmeasured")
		Expect(report.Resumes).NotTo(BeNil())
		Expect(report.Resumes.Resumed).To(Equal(2))
		Expect(report.Resumes.Held).To(HaveLen(1))
		Expect(report.Resumes.Held[0].Reason).To(ContainSubstring("leaves room for 1 sessions"))
	})

	It("reports no resume queue before any restart recorded one", func() {
		report, err := newHostFixture().operations.Host(ctx, opsview.GetHostInput{})
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Resumes).To(BeNil())
	})

	It("applies only CSF work after one confirm of its diff, records it, and undoes it", func() {
		fixture := newHostFixture()
		plan, err := fixture.operations.ApplyProfile(ctx, opsview.ApplyHostProfileInput{Name: opsview.OnlyCSFWork, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Stop).To(ConsistOf("homepage", "runner", "stray"))
		Expect(plan.Start).To(BeEmpty())
		Expect(plan.Confirm).To(Equal([]string{"homepage", "runner", "stray"}))
		Expect(plan.Result).To(Equal("stops 3: homepage, runner, stray."))
		Expect(fixture.engine.changes()).To(BeEmpty(), "a dry run changed the host")

		_, err = fixture.operations.ApplyProfile(ctx, opsview.ApplyHostProfileInput{Name: opsview.OnlyCSFWork})
		Expect(err).To(MatchError(opsview.ErrUnconfirmed))
		_, err = fixture.operations.ApplyProfile(ctx, opsview.ApplyHostProfileInput{Name: opsview.OnlyCSFWork, Confirm: []string{"homepage", "runner"}})
		Expect(err).To(MatchError(opsview.ErrUnconfirmed))
		Expect(fixture.engine.changes()).To(BeEmpty())

		_, err = fixture.operations.ApplyProfile(ctx, opsview.ApplyHostProfileInput{Name: opsview.OnlyCSFWork, Confirm: plan.Confirm})
		Expect(err).NotTo(HaveOccurred())
		for name, state := range map[string]string{"homepage": "exited", "runner": "exited", "stray": "exited", "session-a": "running", "build-a": "running"} {
			Expect(fixture.engine.state(name)).To(Equal(state), name)
		}
		report, err := fixture.operations.Host(ctx, opsview.GetHostInput{})
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Undoable).NotTo(BeNil())
		Expect(report.Undoable.Profile).To(Equal(opsview.OnlyCSFWork))

		undo, err := fixture.operations.Undo(ctx, opsview.UndoHostProfileInput{DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(undo.Start).To(ConsistOf("homepage", "runner", "stray"))
		_, err = fixture.operations.Undo(ctx, opsview.UndoHostProfileInput{Confirm: undo.Confirm})
		Expect(err).NotTo(HaveOccurred())
		for _, name := range []string{"homepage", "runner", "stray"} {
			Expect(fixture.engine.state(name)).To(Equal("running"), name)
		}
		_, err = fixture.operations.Undo(ctx, opsview.UndoHostProfileInput{DryRun: true})
		Expect(err).To(MatchError(opsview.ErrNothingToUndo))
		ledger, err := os.ReadFile(filepath.Join(fixture.state, opsview.HostLedgerFile))
		Expect(err).NotTo(HaveOccurred())
		Expect(ledger).To(ContainSubstring(`"undoes":"2026-10-05T12:00:00Z"`))
	})

	It("refuses to stop a protected container, keeps it out of a profile, and draws its stop button disabled with the reason", func() {
		fixture := newHostFixture()
		_, err := fixture.operations.Protect(ctx, opsview.ProtectContainersInput{Names: []string{"runner"}, Protected: true})
		Expect(err).NotTo(HaveOccurred())

		_, err = fixture.operations.Control(ctx, opsview.ControlContainersInput{Action: opsview.ActionStop, Names: []string{"runner"}, Confirm: []string{"runner"}})
		Expect(err).To(MatchError(opsview.ErrProtected))
		_, err = fixture.operations.Control(ctx, opsview.ControlContainersInput{Action: opsview.ActionPause, Names: []string{"runner"}, DryRun: true})
		Expect(err).To(MatchError(opsview.ErrProtected))
		plan, err := fixture.operations.ApplyProfile(ctx, opsview.ApplyHostProfileInput{Name: opsview.OnlyCSFWork, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Stop).To(ConsistOf("homepage", "stray"))
		group, err := fixture.operations.Control(ctx, opsview.ControlContainersInput{Action: opsview.ActionStop, Group: opsview.GroupOther, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(group.Stop).To(ConsistOf("homepage", "stray"))

		panel := fixture.operations.Panel(ctx)
		stop, found := widget.FindVerified(panel.Actions, "stop/runner")
		Expect(found).To(BeTrue())
		Expect(stop.Enabled()).To(BeFalse())
		Expect(stop.Refusal).To(Equal("runner: protected by the operator"))
		Expect(fixture.engine.changes()).To(BeEmpty())
		Expect(fixture.engine.state("runner")).To(Equal("running"))
	})

	It("refuses a confirm made against a host that has moved since", func() {
		fixture := newHostFixture()
		plan, err := fixture.operations.ApplyProfile(ctx, opsview.ApplyHostProfileInput{Name: opsview.OnlyCSFWork, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		_, err = fixture.operations.Control(ctx, opsview.ControlContainersInput{Action: opsview.ActionStart, Names: []string{"cache"}})
		Expect(err).NotTo(HaveOccurred())
		_, err = fixture.operations.ApplyProfile(ctx, opsview.ApplyHostProfileInput{Name: opsview.OnlyCSFWork, Confirm: plan.Confirm})
		Expect(err).To(MatchError(opsview.ErrUnconfirmed))
		Expect(err.Error()).To(ContainSubstring("cache, homepage, runner, stray"))
		Expect(fixture.engine.changes()).To(Equal([]string{"start cache"}))
	})

	It("applies a profile the host already matches as a no-op, recording nothing", func() {
		fixture := newHostFixture()
		_, err := fixture.operations.PutProfile(ctx, opsview.PutHostProfileInput{Profile: opsview.HostProfile{Name: "essentials", Keep: []string{"homepage", "runner"}}})
		Expect(err).NotTo(HaveOccurred())
		applied, err := fixture.operations.ApplyProfile(ctx, opsview.ApplyHostProfileInput{Name: "essentials"})
		Expect(err).NotTo(HaveOccurred())
		Expect(applied.Changes).To(BeEmpty())
		Expect(applied.Result).To(Equal("Nothing to change: the host already matches."))
		Expect(filepath.Join(fixture.state, opsview.HostLedgerFile)).NotTo(BeAnExistingFile())
		check, err := fixture.operations.Check(ctx, opsview.HostRequest{Operation: opsview.OperationProfile, Profile: "essentials"})
		Expect(err).NotTo(HaveOccurred())
		Expect(check.Confirm).To(BeEmpty())
	})

	It("reports a change the Engine accepted but the host did not make", func() {
		fixture := newHostFixture()
		fixture.engine.stuck["cache"] = true
		_, err := fixture.operations.Control(ctx, opsview.ControlContainersInput{Action: opsview.ActionStart, Names: []string{"cache"}})
		Expect(err).To(MatchError(opsview.ErrAcceptance))
		Expect(err.Error()).To(ContainSubstring("cache is exited, expected running"))
	})

	DescribeTable("refuses misuse with its defined error",
		func(call func(operations *opsview.HostOperations) error, want error) {
			Expect(call(newHostFixture().operations)).To(MatchError(want))
		},
		Entry("an unknown profile", func(operations *opsview.HostOperations) error {
			_, err := operations.ApplyProfile(ctx, opsview.ApplyHostProfileInput{Name: "nope", DryRun: true})
			return err
		}, opsview.ErrNoProfile),
		Entry("an undo with nothing applied", func(operations *opsview.HostOperations) error {
			_, err := operations.Undo(ctx, opsview.UndoHostProfileInput{DryRun: true})
			return err
		}, opsview.ErrNothingToUndo),
		Entry("a profile with no name", func(operations *opsview.HostOperations) error {
			_, err := operations.PutProfile(ctx, opsview.PutHostProfileInput{Profile: opsview.HostProfile{Keep: []string{"homepage"}}})
			return err
		}, opsview.ErrInvalidProfile),
		Entry("a stop of more than one container confirmed with the wrong names", func(operations *opsview.HostOperations) error {
			_, err := operations.Control(ctx, opsview.ControlContainersInput{Action: opsview.ActionStop, Names: []string{"homepage", "stray"}, Confirm: []string{"homepage"}})
			return err
		}, opsview.ErrUnconfirmed),
	)

	It("will not build without the container capability", func() {
		_, err := opsview.NewHostOperations()
		Expect(err).To(MatchError(opsview.ErrNoHostContainers))
		_, err = opsview.NewHostOperations(nil)
		Expect(err).To(MatchError(opsview.ErrNoHostContainers))
	})
})
