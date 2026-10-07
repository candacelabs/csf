// Copyright 2026 Candace Labs

package housekeeping_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/housekeeping"
)

const (
	liveCheckout = "/live/widgets"
	localHead    = "1111111"
	originMain   = "2222222"
)

// onMainBehind is a live checkout on main whose origin/main is ahead.
func onMainBehind(machine *host) {
	machine.git["symbolic-ref --quiet --short HEAD"] = answer{stdout: "main\n"}
	machine.git["rev-parse HEAD"] = answer{stdout: localHead + "\n"}
	machine.git["rev-parse refs/remotes/origin/main"] = answer{stdout: originMain + "\n"}
}

var _ = Describe("the live_checkout trigger", func() {
	var machine *host

	BeforeEach(func() { machine = newHost() })

	run := func(options ...housekeeping.HousekeeperOption) error {
		return machine.housekeeper(append(options, housekeeping.WithLiveCheckout(liveCheckout))...).
			Run(context.Background(), housekeeping.TriggerLiveCheckout)
	}

	It("fetches origin main and fast-forwards the checkout, recording the range", func() {
		onMainBehind(machine)

		Expect(run()).To(Succeed())
		Expect(machine.ran("git", "-C", liveCheckout, "fetch", "--quiet", "origin", "main")).To(BeTrue())
		Expect(machine.ran("git", "-C", liveCheckout, "merge", "--ff-only", "--quiet", "refs/remotes/origin/main")).To(BeTrue())
		Expect(machine.recorded(housekeeping.RecordFastForward, liveCheckout)).To(HaveField("Detail", localHead+".."+originMain))
	})

	It("does nothing and records nothing when the checkout is current", func() {
		onMainBehind(machine)
		machine.git["rev-parse HEAD"] = answer{stdout: originMain + "\n"}

		Expect(run()).To(Succeed())
		Expect(machine.ran("git", "-C", liveCheckout, "merge")).To(BeFalse())
		Expect(machine.records).To(BeEmpty())
	})

	It("records a typed finding and changes nothing when main has commits origin lacks", func() {
		onMainBehind(machine)
		machine.git["merge-base --is-ancestor HEAD refs/remotes/origin/main"] = answer{err: &proc.ExitError{Executable: "git", Code: 1}}

		Expect(run()).To(Succeed())
		Expect(machine.ran("git", "-C", liveCheckout, "merge")).To(BeFalse())
		finding := machine.recorded(housekeeping.RecordFinding, liveCheckout)
		Expect(finding).NotTo(BeNil())
		Expect(finding.Kind).To(Equal(housekeeping.KindLiveCheckout))
		Expect(finding.Detail).To(ContainSubstring("not a fast-forward"))
	})

	It("records a typed finding when the fast-forward would overwrite local changes", func() {
		onMainBehind(machine)
		machine.git["merge --ff-only --quiet refs/remotes/origin/main"] = answer{err: errors.New("Your local changes would be overwritten")}

		Expect(run()).To(Succeed())
		Expect(machine.recorded(housekeeping.RecordFinding, liveCheckout)).To(HaveField("Kind", housekeeping.KindLiveCheckout))
		Expect(machine.ranNaming("git", "reset")).To(BeFalse())
	})

	It("records a typed finding and fetches nothing when the checkout is on another branch", func() {
		machine.git["symbolic-ref --quiet --short HEAD"] = answer{stdout: "dev/widget\n"}

		Expect(run()).To(Succeed())
		Expect(machine.ran("git", "-C", liveCheckout, "fetch")).To(BeFalse())
		Expect(machine.recorded(housekeeping.RecordFinding, liveCheckout)).To(HaveField("Detail", ContainSubstring(`"dev/widget"`)))
	})

	It("plans the fast-forward without merging in a dry run", func() {
		onMainBehind(machine)

		Expect(run(housekeeping.WithDryRun())).To(Succeed())
		Expect(machine.ran("git", "-C", liveCheckout, "merge")).To(BeFalse())
		Expect(machine.recorded(housekeeping.RecordFastForward, liveCheckout)).To(HaveField("DryRun", BeTrue()))
	})

	It("is declared only for a host that names a live checkout", func() {
		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerLiveCheckout)).To(MatchError(housekeeping.ErrUnknownTrigger))
		_, err := housekeeping.NewHousekeeper(housekeeping.WithLiveCheckout(""))
		Expect(err).To(MatchError(housekeeping.ErrInvalidOption))
	})
})
