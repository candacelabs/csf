package dashboard

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
)

var _ = Describe("dashboard helpers", func() {
	// TestHumanizeAge
	Describe("humanizeAge", func() {
		// Fixed reference instant; every case is measured backwards from here.
		now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

		DescribeTable("renders a relative age string",
			func(t time.Time, want string) {
				Expect(humanizeAge(now, t)).To(Equal(want))
			},
			Entry("zero time renders dash", time.Time{}, "—"),
			Entry("future clamps to zero", now.Add(5*time.Second), "0s ago"),
			Entry("sub-second floors to zero seconds", now.Add(-500*time.Millisecond), "0s ago"),
			Entry("exactly now", now, "0s ago"),
			Entry("three seconds", now.Add(-3*time.Second), "3s ago"),
			Entry("fifty-nine seconds", now.Add(-59*time.Second), "59s ago"),
			Entry("one minute boundary", now.Add(-60*time.Second), "1m ago"),
			Entry("ninety seconds is one minute", now.Add(-90*time.Second), "1m ago"),
			Entry("fifty-nine minutes", now.Add(-59*time.Minute), "59m ago"),
			Entry("one hour boundary", now.Add(-time.Hour), "1h ago"),
			Entry("two hours", now.Add(-2*time.Hour), "2h ago"),
			Entry("twenty-three hours", now.Add(-23*time.Hour), "23h ago"),
			Entry("one day boundary", now.Add(-24*time.Hour), "1d ago"),
			Entry("three days", now.Add(-72*time.Hour), "3d ago"),
		)
	})

	// TestFormatLatency
	DescribeTable("formatLatency renders a latency string",
		func(ms float64, want string) {
			Expect(formatLatency(ms)).To(Equal(want))
		},
		Entry("zero renders dash", 0.0, "—"),
		Entry("negative renders dash", -1.0, "—"),
		Entry("one point two", 1.2, "1.2 ms"),
		Entry("rounds to one decimal", 1.24, "1.2 ms"),
		Entry("sub-millisecond", 0.4, "0.4 ms"),
		Entry("large value", 1234.5, "1234.5 ms"),
	)

	// TestStatusPillClass
	DescribeTable("statusPillClass maps a status to a colored pill class",
		func(status warden.PeerStatus, color string) {
			Expect(containsToken(statusPillClass(status), color)).To(BeTrue(),
				"statusPillClass(%q) should be a %q-colored class", status, color)
		},
		Entry("alive", warden.StatusAlive, "green"),
		Entry("suspect", warden.StatusSuspect, "amber"),
		Entry("dead", warden.StatusDead, "red"),
		Entry("unknown", warden.StatusUnknown, "slate"),
		Entry("garbage falls back to slate", warden.PeerStatus("garbage"), "slate"),
	)

	// TestRoleBadgeClass
	DescribeTable("roleBadgeClass maps a role to a colored badge class",
		func(role warden.Role, color string) {
			Expect(containsToken(roleBadgeClass(role), color)).To(BeTrue(),
				"roleBadgeClass(%q) should be a %q-colored class", role, color)
		},
		Entry("leader", warden.RoleLeader, "green"),
		Entry("candidate", warden.RoleCandidate, "amber"),
		Entry("follower", warden.RoleFollower, "slate"),
		Entry("garbage falls back to slate", warden.Role("garbage"), "slate"),
	)

	// TestIncidentBadge
	DescribeTable("incident badge class and label",
		func(typ warden.IncidentType, color, wantLabel string) {
			Expect(containsToken(incidentBadgeClass(typ), color)).To(BeTrue(),
				"incidentBadgeClass(%q) should be a %q-colored class", typ, color)
			Expect(incidentBadgeLabel(typ)).To(Equal(wantLabel))
		},
		Entry("peer dead", warden.IncidentPeerDead, "red", "DEAD"),
		Entry("peer recovered", warden.IncidentPeerRecovered, "green", "RECOVERED"),
	)
})

// containsToken reports whether needle appears anywhere in s. Kept local so the
// helper tests do not depend on strings import conventions elsewhere.
func containsToken(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
