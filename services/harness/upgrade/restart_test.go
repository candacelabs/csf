// Copyright 2026 Candace Labs

package upgrade_test

import (
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/services/harness/upgrade"
)

// Budgets for hosts that are fakes in the same goroutine: generous for the
// passing specs, short for the one that waits for a host that never answers.
var (
	fakeStop  = eventually.Budget{Within: 2 * time.Second, Interval: time.Millisecond}
	fakeReady = eventually.Budget{Within: 2 * time.Second, Interval: time.Millisecond}
	neverDue  = eventually.Budget{Within: 30 * time.Millisecond, Interval: time.Millisecond}
)

const (
	oldPID    = 10
	newPID    = 20
	hostLog   = "/state/harness.log"
	deathLine = "csf serve: mkdir : no such file or directory"
)

// fakeHost is a host the restarter drives: the old one exits after
// stopPolls polls once asked to stop, and the new one answers after
// readyPolls polls, or exits at once when exitAtOnce is set.
type fakeHost struct {
	running    bool
	stopped    bool
	alivePolls int
	stopPolls  int
	readyPolls int
	readyCalls int
	exitAtOnce bool
	log        []string
}

func (host *fakeHost) control() upgrade.HostControl {
	return upgrade.HostControl{
		Current: func() (int, bool, error) { return oldPID, host.running, nil },
		Stop: func(_ context.Context, pid int) error {
			Expect(pid).To(Equal(oldPID))
			host.stopped = true
			return nil
		},
		Alive: func(pid int) bool {
			host.alivePolls++
			return host.alivePolls < host.stopPolls
		},
		Start: func(_ context.Context) (upgrade.StartedHost, error) {
			exited := make(chan upgrade.ExitStatus, 1)
			if host.exitAtOnce {
				exited <- upgrade.ExitStatus{Code: 1}
			}
			return upgrade.StartedHost{PID: newPID, Log: hostLog, Exited: exited}, nil
		},
		Ready: func(_ context.Context, pid int) (bool, string) {
			Expect(pid).To(Equal(newPID))
			host.readyCalls++
			host.log = append(host.log, "starting, poll "+string(rune('0'+host.readyCalls)))
			return host.readyPolls > 0 && host.readyCalls >= host.readyPolls, "the Workbench answered 200 OK"
		},
		LogTail: func(lines int) []string {
			if len(host.log) > lines {
				return host.log[len(host.log)-lines:]
			}
			return host.log
		},
	}
}

func restarterFor(host *fakeHost, lines *[]string, ready eventually.Budget) *upgrade.HostRestarter {
	restarter, err := upgrade.NewHostRestarter(host.control(),
		upgrade.WithRestartProgress(func(line string) { *lines = append(*lines, line) }),
		upgrade.WithBudgets(fakeStop, ready),
		upgrade.WithTick(0))
	Expect(err).NotTo(HaveOccurred())
	return restarter
}

var _ = Describe("Upgrade progress", func() {
	It("says the release, the download size, the checksum and the swap as each happens", func() {
		binary := filepath.Join(GinkgoT().TempDir(), "csf")
		Expect(os.WriteFile(binary, []byte(installedContent), 0o755)).To(Succeed())
		var asked, lines []string
		releases := map[string]release{upgrade.LatestTag: {binary: releasedContent, checksums: checksumLine(releasedContent, upgrade.Asset)}}
		upgrader, err := upgrade.NewUpgrader(upgrade.WithBinary(binary), upgrade.WithFetch(fetchFrom(releases, &asked)),
			upgrade.WithRestart(func(_ context.Context) error { return nil }),
			upgrade.WithProgress(func(line string) { lines = append(lines, line) }))
		Expect(err).NotTo(HaveOccurred())

		result, err := upgrader.Upgrade(context.Background(), "")

		Expect(err).NotTo(HaveOccurred())
		Expect(lines).To(Equal([]string{
			"release latest-main: downloading csf-linux-amd64",
			"downloaded csf-linux-amd64: 12 bytes",
			"checksum: expected " + result.SHA256 + ", actual " + result.SHA256 + ": match",
			"swapped: " + binary + " is the new binary, the old one kept as " + binary + ".previous",
		}))
	})

	It("says a checksum mismatch with both sums", func() {
		binary := filepath.Join(GinkgoT().TempDir(), "csf")
		Expect(os.WriteFile(binary, []byte(installedContent), 0o755)).To(Succeed())
		var asked, lines []string
		releases := map[string]release{upgrade.LatestTag: {binary: "tampered", checksums: checksumLine(releasedContent, upgrade.Asset)}}
		upgrader, err := upgrade.NewUpgrader(upgrade.WithBinary(binary), upgrade.WithFetch(fetchFrom(releases, &asked)),
			upgrade.WithRestart(func(_ context.Context) error { return nil }),
			upgrade.WithProgress(func(line string) { lines = append(lines, line) }))
		Expect(err).NotTo(HaveOccurred())

		_, err = upgrader.Upgrade(context.Background(), "")

		Expect(err).To(MatchError(upgrade.ErrChecksumMismatch))
		Expect(lines[len(lines)-1]).To(MatchRegexp(`^checksum: expected [0-9a-f]{64}, actual [0-9a-f]{64}: MISMATCH, nothing installed$`))
	})
})

var _ = Describe("HostRestarter", func() {
	var lines []string

	BeforeEach(func() { lines = nil })

	It("says each stop with its elapsed time, the start with the new pid, a tick while waiting and the answer", func() {
		host := &fakeHost{running: true, stopPolls: 3, readyPolls: 3}

		Expect(restarterFor(host, &lines, fakeReady).Restart(context.Background())).To(Succeed())

		Expect(host.stopped).To(BeTrue())
		Expect(lines).To(HaveLen(6))
		Expect(lines[0]).To(Equal("stopping pid 10"))
		Expect(lines[1]).To(MatchRegexp(`^pid 10 exited after \S+$`))
		Expect(lines[2]).To(Equal("started pid 20, log " + hostLog))
		Expect(lines[3]).To(MatchRegexp(`^waiting \S+: starting, poll 1$`))
		Expect(lines[4]).To(MatchRegexp(`^waiting \S+: starting, poll 2$`))
		Expect(lines[5]).To(MatchRegexp(`^pid 20 answered after \S+: the Workbench answered 200 OK$`))
	})

	It("reports a new host that exits at once on the first poll, with its exit status and last log lines", func() {
		host := &fakeHost{running: true, stopPolls: 1, exitAtOnce: true, log: []string{"opening the database", deathLine}}

		err := restarterFor(host, &lines, fakeReady).Restart(context.Background())

		Expect(err).To(MatchError(upgrade.ErrHostExited))
		Expect(err.Error()).To(ContainSubstring(deathLine))
		Expect(host.readyCalls).To(BeZero(), "reported within one tick: the exit is seen before the host is ever probed")
		Expect(lines).To(ContainElement(MatchRegexp(`^pid 20 exited with status 1 after \S+; its last log lines:$`)))
		Expect(lines[len(lines)-2:]).To(Equal([]string{"  | opening the database", "  | " + deathLine}))
	})

	It("says so and starts the new host when none is running", func() {
		host := &fakeHost{running: false, readyPolls: 1}

		Expect(restarterFor(host, &lines, fakeReady).Restart(context.Background())).To(Succeed())

		Expect(host.stopped).To(BeFalse())
		Expect(lines[0]).To(Equal("no host is running; starting one"))
	})

	It("prints the last log lines of a new host that never answers and returns ErrHostStuck", func() {
		host := &fakeHost{running: false, readyPolls: 0}

		err := restarterFor(host, &lines, neverDue).Restart(context.Background())

		Expect(err).To(MatchError(upgrade.ErrHostStuck))
		Expect(lines).To(ContainElement(MatchRegexp(`^pid 20 did not answer after \S+; its last log lines:$`)))
		Expect(lines[len(lines)-1]).To(HavePrefix("  | starting, poll "))
	})

	Describe("misuse", func() {
		It("refuses a control with a missing operation", func() {
			control := (&fakeHost{}).control()
			control.LogTail = nil
			_, err := upgrade.NewHostRestarter(control)
			Expect(err).To(MatchError(upgrade.ErrInvalidOption))
		})

		It("refuses zero budgets, a negative tick and nil progress", func() {
			control := (&fakeHost{}).control()
			_, err := upgrade.NewHostRestarter(control, upgrade.WithBudgets(eventually.Budget{}, fakeReady))
			Expect(err).To(MatchError(upgrade.ErrInvalidOption))
			_, err = upgrade.NewHostRestarter(control, upgrade.WithTick(-time.Second))
			Expect(err).To(MatchError(upgrade.ErrInvalidOption))
			_, err = upgrade.NewHostRestarter(control, upgrade.WithRestartProgress(nil))
			Expect(err).To(MatchError(upgrade.ErrInvalidOption))
			_, err = upgrade.NewHostRestarter(control, upgrade.WithRestartClock(nil))
			Expect(err).To(MatchError(upgrade.ErrInvalidOption))
		})
	})
})
