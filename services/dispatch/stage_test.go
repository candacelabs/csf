// Copyright 2026 Candace Labs

package dispatch_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/dispatch"
)

var _ = Describe("the snapshot as a flow", func() {
	day := 24 * time.Hour
	snapshot := dispatch.Snapshot{
		At: frozen,
		Queue: []dispatch.SliceView{
			{SliceID: "ready", Rank: 1, UpdatedAt: frozen.Add(-time.Minute)},
			{SliceID: "blocked-new", UpdatedAt: frozen.Add(-time.Minute)},
			{SliceID: "blocked-old", UpdatedAt: frozen.Add(-3 * time.Hour)},
		},
		Held:    []dispatch.SliceView{{SliceID: "held", Rank: 2, Held: "operator", UpdatedAt: frozen.Add(-time.Hour)}},
		Running: []dispatch.SliceView{{SliceID: "running", UpdatedAt: frozen.Add(-30 * time.Minute)}},
		Finished: []dispatch.SliceView{
			{SliceID: "merged-today", State: "SLICE_STATE_MERGED", CreatedAt: frozen.Add(-5 * time.Hour), UpdatedAt: frozen.Add(-2 * time.Hour)},
			{SliceID: "merged-earlier", State: "SLICE_STATE_MERGED", CreatedAt: frozen.Add(-3 * time.Hour), UpdatedAt: frozen.Add(-time.Hour)},
			{SliceID: "merged-last-week", State: "SLICE_STATE_MERGED", CreatedAt: frozen.Add(-9 * day), UpdatedAt: frozen.Add(-7 * day)},
			{SliceID: "failed", State: "SLICE_STATE_FAILED", UpdatedAt: frozen},
		},
	}

	It("counts the slices in each stage and how long the oldest has stood there", func() {
		flow := snapshot.Flow(day)
		Expect(flow.Slices).To(Equal(map[dispatch.Stage]int{
			dispatch.StageReady: 1, dispatch.StageBlocked: 3, dispatch.StageRunning: 1, dispatch.StageMerged: 3, dispatch.StageFailed: 1,
		}), "a held slice is blocked whatever its rank")
		Expect(flow.Oldest[dispatch.StageBlocked]).To(Equal(3 * time.Hour))
		Expect(flow.Oldest[dispatch.StageRunning]).To(Equal(30 * time.Minute))
		Expect(flow.Oldest[dispatch.StageFailed]).To(BeZero())
	})

	It("measures lead time over the slices merged inside the window only", func() {
		lead := snapshot.Flow(day).LeadTime
		Expect(lead).NotTo(BeNil())
		Expect(*lead).To(Equal(150*time.Minute), "(3h + 2h) / 2; last week's merge is outside the day")
		Expect(dispatch.Snapshot{At: frozen}.Flow(day).LeadTime).To(BeNil(), "no merge, no lead time")
	})
})
