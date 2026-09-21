package contract_test

import (
	"errors"
	"strings"
	"time"

	cron "github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/pkg/cron/contract"
	cronv1 "github.com/candacelabs/csf/pkg/cron/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ = Describe("Liquid Proto runtime boundary", func() {
	definition := func() cron.JobDefinition {
		schedule, err := cron.Spec(cron.Daily(cron.At(3).AM())).Definition()
		Expect(err).NotTo(HaveOccurred())
		return cron.JobDefinition{
			Name:     "daily-rollup",
			Schedule: schedule,
			CatchUp:  cron.CatchUpAll,
			Overlap:  cron.OverlapSkip,
		}
	}
	invocation := func(scheduledAt time.Time, attempt uint32) *cronv1.Invocation {
		message := &cronv1.Invocation{
			OccurrenceId: cron.OccurrenceID("daily-rollup", scheduledAt),
			JobName:      "daily-rollup",
			ScheduledAt:  timestamppb.New(scheduledAt),
			Attempt:      attempt,
		}
		if attempt > 0 {
			message.StartedAt = timestamppb.New(scheduledAt.Add(time.Second))
		}
		return message
	}

	It("round-trips desired definitions without SQLC rows", func() {
		message, err := contract.JobDefinitionToProto(definition())
		Expect(err).NotTo(HaveOccurred())
		Expect(message.GetCatchUpPolicy()).To(Equal(cronv1.CatchUpPolicy_CATCH_UP_POLICY_ALL))

		rebuilt, err := contract.JobDefinitionFromProto(message)
		Expect(err).NotTo(HaveOccurred())
		Expect(rebuilt).To(Equal(definition()))
	})

	It("rejects unspecified policies at the contract boundary", func() {
		message, err := contract.JobDefinitionToProto(definition())
		Expect(err).NotTo(HaveOccurred())
		message.CatchUpPolicy = cronv1.CatchUpPolicy_CATCH_UP_POLICY_UNSPECIFIED
		Expect(errors.Is(contract.ValidateJobDefinition(message), contract.ErrInvalid)).To(BeTrue())
	})

	It("maps deterministic skipped occurrences without inventing an attempt", func() {
		scheduledAt := time.Date(2026, time.August, 10, 3, 0, 0, 0, time.UTC)
		run, err := contract.OccurrenceToProto(cron.OccurrenceRecord{
			ID:          cron.OccurrenceID("daily-rollup", scheduledAt),
			JobName:     "daily-rollup",
			ScheduledAt: scheduledAt,
			Status:      cron.OccurrenceSkipped,
			FinishedAt:  scheduledAt.Add(time.Second),
			SkipReason:  "catch_up_none",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(run.GetState()).To(Equal(cronv1.RunState_RUN_STATE_SKIPPED))
		Expect(run.GetInvocation().GetAttempt()).To(BeZero())
	})

	It("rejects an occurrence ID forged for a different logical instant", func() {
		scheduledAt := time.Date(2026, time.August, 10, 3, 0, 0, 0, time.UTC)
		run, err := contract.OccurrenceToProto(cron.OccurrenceRecord{
			ID:          cron.OccurrenceID("daily-rollup", scheduledAt),
			JobName:     "daily-rollup",
			ScheduledAt: scheduledAt,
			Status:      cron.OccurrenceSkipped,
			FinishedAt:  scheduledAt.Add(time.Second),
			SkipReason:  "catch_up_none",
		})
		Expect(err).NotTo(HaveOccurred())
		run.Invocation.OccurrenceId = cron.OccurrenceID("daily-rollup", scheduledAt.Add(time.Minute))
		Expect(contract.ValidateRunSummary(run)).To(MatchError(ContainSubstring("does not match")))
	})

	It("derives stable status projections from durable domain state", func() {
		now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
		state := cron.JobState{Definition: definition(), NextRunAt: now.Add(time.Hour)}
		runningAt := now.Add(-time.Minute)
		snapshot, err := contract.StoreSnapshotToProto(cron.StoreSnapshot{
			Jobs: []cron.JobState{state},
			Occurrences: []cron.OccurrenceRecord{{
				ID:          cron.OccurrenceID("daily-rollup", runningAt),
				JobName:     "daily-rollup",
				ScheduledAt: runningAt,
				Status:      cron.OccurrenceRunning,
				Attempt:     1,
				StartedAt:   runningAt,
				LeaseOwner:  "worker-1",
			}},
		}, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.GetJobs()).To(HaveLen(1))
		Expect(snapshot.GetJobs()[0].GetActiveRuns()).To(Equal(uint32(1)))
		Expect(snapshot.GetJobs()[0].GetLastRun().GetInvocation().GetOccurrenceId()).To(HavePrefix("occ_"))
	})

	It("round-trips definitions through the validating codec", func() {
		codec, err := contract.NewJobDefinitionCodec()
		Expect(err).NotTo(HaveOccurred())
		Expect(codec.MessageType()).To(Equal("candace.cron.v1.JobDefinition"))

		message, err := contract.JobDefinitionToProto(definition())
		Expect(err).NotTo(HaveOccurred())
		wire, err := codec.Marshal(message)
		Expect(err).NotTo(HaveOccurred())
		decoded, err := codec.Unmarshal(wire)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(decoded, message)).To(BeTrue())

		_, err = codec.Marshal(&cronv1.JobDefinition{Name: "Not-A-Job"})
		Expect(err).To(MatchError(ContainSubstring("validate before marshal")))
	})

	It("round-trips status snapshots through the validating codec", func() {
		codec, err := contract.NewStatusSnapshotCodec()
		Expect(err).NotTo(HaveOccurred())
		Expect(codec.MessageType()).To(Equal("candace.cron.v1.StatusSnapshot"))

		message := &cronv1.StatusSnapshot{ObservedAt: timestamppb.New(time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC))}
		wire, err := codec.Marshal(message)
		Expect(err).NotTo(HaveOccurred())
		decoded, err := codec.Unmarshal(wire)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(decoded, message)).To(BeTrue())

		invalidWire, err := proto.Marshal(&cronv1.StatusSnapshot{})
		Expect(err).NotTo(HaveOccurred())
		_, err = codec.Unmarshal(invalidWire)
		Expect(err).To(MatchError(ContainSubstring("validate after unmarshal")))
	})

	DescribeTable("round-trips every job policy",
		func(catchUp cron.CatchUpPolicy, overlap cron.OverlapPolicy, wantCatchUp cronv1.CatchUpPolicy, wantOverlap cronv1.OverlapPolicy) {
			domain := definition()
			domain.CatchUp = catchUp
			domain.Overlap = overlap

			message, err := contract.JobDefinitionToProto(domain)
			Expect(err).NotTo(HaveOccurred())
			Expect(message.GetCatchUpPolicy()).To(Equal(wantCatchUp))
			Expect(message.GetOverlapPolicy()).To(Equal(wantOverlap))

			rebuilt, err := contract.JobDefinitionFromProto(message)
			Expect(err).NotTo(HaveOccurred())
			Expect(rebuilt).To(Equal(domain))
		},
		Entry("none and skip", cron.CatchUpNone, cron.OverlapSkip, cronv1.CatchUpPolicy_CATCH_UP_POLICY_NONE, cronv1.OverlapPolicy_OVERLAP_POLICY_SKIP),
		Entry("latest and allow", cron.CatchUpLatest, cron.OverlapAllow, cronv1.CatchUpPolicy_CATCH_UP_POLICY_LATEST, cronv1.OverlapPolicy_OVERLAP_POLICY_ALLOW),
		Entry("all and allow", cron.CatchUpAll, cron.OverlapAllow, cronv1.CatchUpPolicy_CATCH_UP_POLICY_ALL, cronv1.OverlapPolicy_OVERLAP_POLICY_ALLOW),
	)

	It("rejects malformed domain and boundary definitions", func() {
		invalidSchedule := definition()
		invalidSchedule.Schedule = cron.ScheduleDefinition{}
		_, err := contract.JobDefinitionToProto(invalidSchedule)
		Expect(err).To(MatchError(ContainSubstring("schedule")))

		invalidCatchUp := definition()
		invalidCatchUp.CatchUp = cron.CatchUpPolicy("sometimes")
		_, err = contract.JobDefinitionToProto(invalidCatchUp)
		Expect(err).To(MatchError(ContainSubstring("unknown catch-up policy")))

		invalidOverlap := definition()
		invalidOverlap.Overlap = cron.OverlapPolicy("queue")
		_, err = contract.JobDefinitionToProto(invalidOverlap)
		Expect(err).To(MatchError(ContainSubstring("unknown overlap policy")))

		_, err = contract.JobDefinitionFromProto(nil)
		Expect(err).To(MatchError(ContainSubstring("job definition is required")))
		_, err = contract.JobDefinitionFromProto(&cronv1.JobDefinition{Name: "Not-A-Job"})
		Expect(err).To(MatchError(ContainSubstring("scalar refinements")))

		message, err := contract.JobDefinitionToProto(definition())
		Expect(err).NotTo(HaveOccurred())
		message.Schedule = nil
		_, err = contract.JobDefinitionFromProto(message)
		Expect(err).To(MatchError(ContainSubstring("schedule is required")))

		message, err = contract.JobDefinitionToProto(definition())
		Expect(err).NotTo(HaveOccurred())
		message.OverlapPolicy = cronv1.OverlapPolicy_OVERLAP_POLICY_UNSPECIFIED
		_, err = contract.JobDefinitionFromProto(message)
		Expect(err).To(MatchError(ContainSubstring("overlap policy is required")))
	})

	It("validates standalone invocation identity and attempt semantics", func() {
		scheduledAt := time.Date(2026, time.August, 10, 3, 0, 0, 0, time.UTC)
		Expect(contract.ValidateInvocation(invocation(scheduledAt, 1))).To(Succeed())

		Expect(contract.ValidateInvocation(nil)).To(MatchError(ContainSubstring("invocation is required")))
		Expect(contract.ValidateInvocation(&cronv1.Invocation{})).To(MatchError(ContainSubstring("scalar refinements")))

		missingScheduledAt := invocation(scheduledAt, 1)
		missingScheduledAt.ScheduledAt = nil
		Expect(contract.ValidateInvocation(missingScheduledAt)).To(MatchError(ContainSubstring("scheduled_at is required")))

		invalidScheduledAt := invocation(scheduledAt, 1)
		invalidScheduledAt.ScheduledAt = &timestamppb.Timestamp{Seconds: 253402300800}
		Expect(contract.ValidateInvocation(invalidScheduledAt)).To(MatchError(ContainSubstring("scheduled_at")))

		forged := invocation(scheduledAt, 1)
		forged.OccurrenceId = cron.OccurrenceID("daily-rollup", scheduledAt.Add(time.Minute))
		Expect(contract.ValidateInvocation(forged)).To(MatchError(ContainSubstring("does not match")))

		Expect(contract.ValidateInvocation(invocation(scheduledAt, 0))).To(MatchError(ContainSubstring("nonzero attempt")))

		missingStartedAt := invocation(scheduledAt, 1)
		missingStartedAt.StartedAt = nil
		Expect(contract.ValidateInvocation(missingStartedAt)).To(MatchError(ContainSubstring("started_at")))

		invalidStartedAt := invocation(scheduledAt, 1)
		invalidStartedAt.StartedAt = &timestamppb.Timestamp{Seconds: 253402300800}
		Expect(contract.ValidateInvocation(invalidStartedAt)).To(MatchError(ContainSubstring("started_at")))
	})

	DescribeTable("maps every durable occurrence state",
		func(status cron.OccurrenceStatus, want cronv1.RunState, errorSummary, skipReason string, attempted bool) {
			scheduledAt := time.Date(2026, time.August, 10, 3, 0, 0, 0, time.UTC)
			record := cron.OccurrenceRecord{
				ID:          cron.OccurrenceID("daily-rollup", scheduledAt),
				JobName:     "daily-rollup",
				ScheduledAt: scheduledAt,
				Status:      status,
				FinishedAt:  scheduledAt.Add(2 * time.Second),
				Error:       errorSummary,
				SkipReason:  skipReason,
			}
			if attempted {
				record.Attempt = 1
				record.StartedAt = scheduledAt.Add(time.Second)
			}
			if status == cron.OccurrenceRunning {
				record.FinishedAt = time.Time{}
				record.LeaseOwner = "worker-1"
			}

			message, err := contract.OccurrenceToProto(record)
			Expect(err).NotTo(HaveOccurred())
			Expect(message.GetState()).To(Equal(want))
		},
		Entry("running", cron.OccurrenceRunning, cronv1.RunState_RUN_STATE_RUNNING, "", "", true),
		Entry("succeeded", cron.OccurrenceSucceeded, cronv1.RunState_RUN_STATE_SUCCEEDED, "", "", true),
		Entry("failed", cron.OccurrenceFailed, cronv1.RunState_RUN_STATE_FAILED, "boom", "", true),
		Entry("canceled", cron.OccurrenceCanceled, cronv1.RunState_RUN_STATE_CANCELED, "shutdown", "", true),
		Entry("skipped", cron.OccurrenceSkipped, cronv1.RunState_RUN_STATE_SKIPPED, "", "catch_up_none", false),
	)

	It("rejects an unknown durable occurrence state", func() {
		_, err := contract.OccurrenceToProto(cron.OccurrenceRecord{Status: cron.OccurrenceStatus("lost")})
		Expect(err).To(MatchError(ContainSubstring("unknown occurrence status")))
	})

	It("enforces state-dependent run summary fields", func() {
		scheduledAt := time.Date(2026, time.August, 10, 3, 0, 0, 0, time.UTC)
		pendingInvocation := invocation(scheduledAt, 0)
		pending := &cronv1.RunSummary{Invocation: pendingInvocation, State: cronv1.RunState_RUN_STATE_PENDING}
		Expect(contract.ValidateRunSummary(pending)).To(Succeed())

		Expect(contract.ValidateRunSummary(nil)).To(MatchError(ContainSubstring("run summary is required")))
		Expect(contract.ValidateRunSummary(&cronv1.RunSummary{WorkerId: strings.Repeat("w", 129)})).To(MatchError(ContainSubstring("scalar refinements")))

		unspecified := proto.Clone(pending).(*cronv1.RunSummary)
		unspecified.State = cronv1.RunState_RUN_STATE_UNSPECIFIED
		Expect(contract.ValidateRunSummary(unspecified)).To(MatchError(ContainSubstring("run state is required")))

		pendingWithStart := proto.Clone(pending).(*cronv1.RunSummary)
		pendingWithStart.Invocation.StartedAt = timestamppb.New(scheduledAt)
		Expect(contract.ValidateRunSummary(pendingWithStart)).To(MatchError(ContainSubstring("pending run")))

		running := &cronv1.RunSummary{
			Invocation: invocation(scheduledAt, 1),
			State:      cronv1.RunState_RUN_STATE_RUNNING,
			WorkerId:   "worker-1",
		}
		Expect(contract.ValidateRunSummary(running)).To(Succeed())
		running.WorkerId = ""
		Expect(contract.ValidateRunSummary(running)).To(MatchError(ContainSubstring("running run requires")))

		terminal := &cronv1.RunSummary{
			Invocation: invocation(scheduledAt, 1),
			State:      cronv1.RunState_RUN_STATE_SUCCEEDED,
		}
		Expect(contract.ValidateRunSummary(terminal)).To(MatchError(ContainSubstring("terminal run requires")))
		terminal.FinishedAt = &timestamppb.Timestamp{Seconds: 253402300800}
		Expect(contract.ValidateRunSummary(terminal)).To(MatchError(ContainSubstring("finished_at")))
		terminal.FinishedAt = timestamppb.New(scheduledAt.Add(2 * time.Second))
		terminal.ErrorSummary = "unexpected"
		Expect(contract.ValidateRunSummary(terminal)).To(MatchError(ContainSubstring("error_summary")))
		terminal.ErrorSummary = ""
		terminal.SkipReason = "unexpected"
		Expect(contract.ValidateRunSummary(terminal)).To(MatchError(ContainSubstring("skip_reason")))

		skipped := &cronv1.RunSummary{
			Invocation: pendingInvocation,
			State:      cronv1.RunState_RUN_STATE_SKIPPED,
			FinishedAt: timestamppb.New(scheduledAt.Add(time.Second)),
			SkipReason: "",
		}
		Expect(contract.ValidateRunSummary(skipped)).To(MatchError(ContainSubstring("skipped run requires")))

		unsupported := proto.Clone(pending).(*cronv1.RunSummary)
		unsupported.State = cronv1.RunState(99)
		unsupported.Invocation = invocation(scheduledAt, 1)
		Expect(contract.ValidateRunSummary(unsupported)).To(MatchError(ContainSubstring("unsupported run state")))
	})

	It("rejects malformed status projections", func() {
		observedAt := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
		message, err := contract.JobDefinitionToProto(definition())
		Expect(err).NotTo(HaveOccurred())
		validStatus := &cronv1.JobStatus{
			Definition: message,
			NextRunAt:  timestamppb.New(observedAt.Add(time.Hour)),
		}

		Expect(contract.ValidateStatusSnapshot(nil)).To(MatchError(ContainSubstring("status snapshot is required")))
		Expect(contract.ValidateStatusSnapshot(&cronv1.StatusSnapshot{})).To(MatchError(ContainSubstring("observed_at is required")))
		Expect(contract.ValidateStatusSnapshot(&cronv1.StatusSnapshot{
			ObservedAt: &timestamppb.Timestamp{Seconds: 253402300800},
		})).To(MatchError(ContainSubstring("observed_at")))
		Expect(contract.ValidateStatusSnapshot(&cronv1.StatusSnapshot{
			ObservedAt: timestamppb.New(observedAt),
			Jobs:       []*cronv1.JobStatus{nil},
		})).To(MatchError(ContainSubstring("nil job status")))

		scalarInvalid := proto.Clone(validStatus).(*cronv1.JobStatus)
		scalarInvalid.ActiveRuns = 1_000_001
		Expect(contract.ValidateStatusSnapshot(&cronv1.StatusSnapshot{
			ObservedAt: timestamppb.New(observedAt),
			Jobs:       []*cronv1.JobStatus{scalarInvalid},
		})).To(MatchError(ContainSubstring("scalar refinements")))

		invalidDefinition := proto.Clone(validStatus).(*cronv1.JobStatus)
		invalidDefinition.Definition.CatchUpPolicy = cronv1.CatchUpPolicy_CATCH_UP_POLICY_UNSPECIFIED
		Expect(contract.ValidateStatusSnapshot(&cronv1.StatusSnapshot{
			ObservedAt: timestamppb.New(observedAt),
			Jobs:       []*cronv1.JobStatus{invalidDefinition},
		})).NotTo(Succeed())

		duplicate := proto.Clone(validStatus).(*cronv1.JobStatus)
		Expect(contract.ValidateStatusSnapshot(&cronv1.StatusSnapshot{
			ObservedAt: timestamppb.New(observedAt),
			Jobs:       []*cronv1.JobStatus{validStatus, duplicate},
		})).To(MatchError(ContainSubstring("duplicate job status")))

		missingNext := proto.Clone(validStatus).(*cronv1.JobStatus)
		missingNext.NextRunAt = nil
		Expect(contract.ValidateStatusSnapshot(&cronv1.StatusSnapshot{
			ObservedAt: timestamppb.New(observedAt),
			Jobs:       []*cronv1.JobStatus{missingNext},
		})).To(MatchError(ContainSubstring("next_run_at is required")))

		invalidNext := proto.Clone(validStatus).(*cronv1.JobStatus)
		invalidNext.NextRunAt = &timestamppb.Timestamp{Seconds: 253402300800}
		Expect(contract.ValidateStatusSnapshot(&cronv1.StatusSnapshot{
			ObservedAt: timestamppb.New(observedAt),
			Jobs:       []*cronv1.JobStatus{invalidNext},
		})).To(MatchError(ContainSubstring("next_run_at")))

		invalidAnchor := proto.Clone(validStatus).(*cronv1.JobStatus)
		invalidAnchor.IntervalAnchor = &timestamppb.Timestamp{Seconds: 253402300800}
		Expect(contract.ValidateStatusSnapshot(&cronv1.StatusSnapshot{
			ObservedAt: timestamppb.New(observedAt),
			Jobs:       []*cronv1.JobStatus{invalidAnchor},
		})).To(MatchError(ContainSubstring("interval_anchor")))

		mismatchedRun := proto.Clone(validStatus).(*cronv1.JobStatus)
		mismatchedRun.LastRun = &cronv1.RunSummary{
			Invocation: &cronv1.Invocation{
				OccurrenceId: cron.OccurrenceID("other-job", observedAt),
				JobName:      "other-job",
				ScheduledAt:  timestamppb.New(observedAt),
			},
			State:      cronv1.RunState_RUN_STATE_SKIPPED,
			FinishedAt: timestamppb.New(observedAt),
			SkipReason: "not_due",
		}
		Expect(contract.ValidateStatusSnapshot(&cronv1.StatusSnapshot{
			ObservedAt: timestamppb.New(observedAt),
			Jobs:       []*cronv1.JobStatus{mismatchedRun},
		})).To(MatchError(ContainSubstring("name mismatch")))
	})

	It("rejects invalid snapshot inputs while projecting durable state", func() {
		now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
		_, err := contract.StoreSnapshotToProto(cron.StoreSnapshot{}, time.Time{})
		Expect(err).To(MatchError(ContainSubstring("observed_at is required")))

		invalidJob := definition()
		invalidJob.Overlap = cron.OverlapPolicy("queue")
		_, err = contract.StoreSnapshotToProto(cron.StoreSnapshot{
			Jobs: []cron.JobState{{Definition: invalidJob, NextRunAt: now}},
		}, now)
		Expect(err).To(MatchError(ContainSubstring("unknown overlap policy")))

		_, err = contract.StoreSnapshotToProto(cron.StoreSnapshot{
			Jobs: []cron.JobState{{Definition: definition(), NextRunAt: now.Add(time.Hour)}},
			Occurrences: []cron.OccurrenceRecord{{
				ID:          cron.OccurrenceID("daily-rollup", now),
				JobName:     "daily-rollup",
				ScheduledAt: now,
				Status:      cron.OccurrenceStatus("lost"),
			}},
		}, now)
		Expect(err).To(MatchError(ContainSubstring("unknown occurrence status")))
	})
})
