// Copyright 2026 Candace Labs

package jobs_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/ipc/db/csfpg"
	"github.com/candacelabs/csf/services/jobs"
	"github.com/candacelabs/csf/services/jobs/jobsmock"
)

// overnightSpec is the shape a consumer such as an overnight-compute catalog
// would admit: the ledger stores it and hands it back typed.
type overnightSpec struct {
	Command string `json:"command"`
	Shards  int    `json:"shards"`
}

const (
	overnightKind     jobs.Kind         = "overnight.compute"
	overnightExecutor jobs.ExecutorName = "aws_batch"
	hostExecutor      jobs.ExecutorName = "local"
)

var overnightBudget = jobs.Budget{Account: "overnight", LimitUSDMicros: 1000}

func overnightAdmission() jobs.Admission[overnightSpec] {
	return jobs.Admission[overnightSpec]{
		ID: "nightly-1", Kind: overnightKind, Executor: overnightExecutor, Spec: overnightSpec{Command: "sweep", Shards: 4},
		TotalUnits: 4, Managed: true, Target: "queue", Image: "definition:1", ArtifactURI: "s3://bucket/nightly-1/",
		ReservationUSDMicros: 600, Timeout: time.Hour,
	}
}

// storedJob is the row the database holds for an admission.
func storedJob(admission jobs.Admission[overnightSpec], state csfpg.CsfJobState) csfpg.CsfJob {
	request, err := json.Marshal(admission.Spec)
	Expect(err).NotTo(HaveOccurred())
	digest := sha256.Sum256(request)
	now := pgtype.Timestamptz{Time: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Valid: true}
	return csfpg.CsfJob{
		JobID: admission.ID, Kind: string(admission.Kind), Executor: string(admission.Executor), BudgetAccount: overnightBudget.Account,
		Request: request, RequestSha256: hex.EncodeToString(digest[:]), State: state, TotalUnits: admission.TotalUnits,
		Managed: admission.Managed, ExecutorTarget: admission.Target, ExecutorImage: admission.Image, ArtifactUri: admission.ArtifactURI,
		ReservationUsdMicros: admission.ReservationUSDMicros, TimeoutSeconds: int32(admission.Timeout.Seconds()), CreatedAt: now, UpdatedAt: now,
	}
}

// ledgerFixture is a ledger over gomock doubles: database answers outside
// a transaction and tx answers inside every Transact.
type ledgerFixture struct {
	database *jobsmock.MockIJobDatabase
	tx       *jobsmock.MockIJobQueries
}

func newLedgerFixture() ledgerFixture {
	control := gomock.NewController(GinkgoT())
	fixture := ledgerFixture{database: jobsmock.NewMockIJobDatabase(control), tx: jobsmock.NewMockIJobQueries(control)}
	fixture.database.EXPECT().Transact(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, work func(queries jobs.IJobQueries) error) error {
		return work(fixture.tx)
	}).AnyTimes()
	return fixture
}

func (fixture ledgerFixture) ledger(options ...jobs.LedgerOption[overnightSpec]) *jobs.JobLedger[overnightSpec] {
	options = append([]jobs.LedgerOption[overnightSpec]{jobs.WithProgressMetric[overnightSpec]("shards_done")}, options...)
	ledger, err := jobs.NewJobLedger(fixture.database, overnightBudget, []jobs.Kind{overnightKind}, options...)
	Expect(err).NotTo(HaveOccurred())
	return ledger
}

var _ = Describe("NewJobLedger", func() {
	var database *jobsmock.MockIJobDatabase
	BeforeEach(func() { database = jobsmock.NewMockIJobDatabase(gomock.NewController(GinkgoT())) })
	DescribeTable("refuses an unusable configuration with its defined error",
		func(build func() error, want error) { Expect(build()).To(MatchError(want)) },
		Entry("no database", func() error {
			_, err := jobs.NewJobLedger[overnightSpec](nil, overnightBudget, []jobs.Kind{overnightKind})
			return err
		}, jobs.ErrDatabaseRequired),
		Entry("an invalid budget account", func() error {
			_, err := jobs.NewJobLedger[overnightSpec](database, jobs.Budget{Account: "../x", LimitUSDMicros: 1}, []jobs.Kind{overnightKind})
			return err
		}, jobs.ErrInvalidBudget),
		Entry("a negative budget", func() error {
			_, err := jobs.NewJobLedger[overnightSpec](database, jobs.Budget{Account: "overnight", LimitUSDMicros: -1}, []jobs.Kind{overnightKind})
			return err
		}, jobs.ErrInvalidBudget),
		Entry("no kinds", func() error {
			_, err := jobs.NewJobLedger[overnightSpec](database, overnightBudget, nil)
			return err
		}, jobs.ErrKindsRequired),
		Entry("a repeated kind", func() error {
			_, err := jobs.NewJobLedger[overnightSpec](database, overnightBudget, []jobs.Kind{overnightKind, overnightKind})
			return err
		}, jobs.ErrKindsRequired),
		Entry("a nil executor", func() error {
			_, err := jobs.NewJobLedger(database, overnightBudget, []jobs.Kind{overnightKind}, jobs.WithRemoteExecutor[overnightSpec](overnightExecutor, nil))
			return err
		}, jobs.ErrInvalidExecutor),
		Entry("two executors under one name", func() error {
			control := gomock.NewController(GinkgoT())
			_, err := jobs.NewJobLedger(database, overnightBudget, []jobs.Kind{overnightKind},
				jobs.WithRemoteExecutor[overnightSpec](overnightExecutor, jobsmock.NewMockIRemoteExecutor[overnightSpec](control)),
				jobs.WithHostExecutor[overnightSpec](overnightExecutor, jobsmock.NewMockIHostExecutor[overnightSpec](control)))
			return err
		}, jobs.ErrInvalidExecutor),
		Entry("an executor name the schema refuses", func() error {
			_, err := jobs.NewJobLedger(database, overnightBudget, []jobs.Kind{overnightKind},
				jobs.WithHostExecutor[overnightSpec]("Local Docker", jobsmock.NewMockIHostExecutor[overnightSpec](gomock.NewController(GinkgoT()))))
			return err
		}, jobs.ErrInvalidExecutor),
		Entry("a metric limit below one", func() error {
			_, err := jobs.NewJobLedger(database, overnightBudget, []jobs.Kind{overnightKind}, jobs.WithMetricLimit[overnightSpec](0))
			return err
		}, jobs.ErrInvalidObservation),
	)
})

var _ = Describe("JobLedger admission", func() {
	var fixture ledgerFixture
	var ledger *jobs.JobLedger[overnightSpec]
	var admission jobs.Admission[overnightSpec]
	ctx := context.Background()
	BeforeEach(func() {
		fixture = newLedgerFixture()
		ledger = fixture.ledger()
		admission = overnightAdmission()
	})
	opened := func() {
		fixture.tx.EXPECT().EnsureJobBudget(ctx, csfpg.EnsureJobBudgetParams{Account: "overnight", LimitUsdMicros: 1000}).Return(csfpg.CsfJobBudget{Account: "overnight", LimitUsdMicros: 1000}, nil)
		fixture.tx.EXPECT().LockJobBudget(ctx, "overnight").Return(csfpg.CsfJobBudget{}, nil)
	}

	It("reserves the budget and records the typed request in one transaction", func() {
		opened()
		stored := storedJob(admission, csfpg.CsfJobStatePending)
		gomock.InOrder(
			fixture.tx.EXPECT().GetJob(ctx, csfpg.GetJobParams{JobID: "nightly-1", Kinds: []string{"overnight.compute"}}).Return(csfpg.CsfJob{}, pgx.ErrNoRows),
			fixture.tx.EXPECT().ReserveJobBudget(ctx, csfpg.ReserveJobBudgetParams{Account: "overnight", Amount: 600}).Return(int64(1), nil),
			fixture.tx.EXPECT().InsertJob(ctx, gomock.Any()).DoAndReturn(func(ctx context.Context, params csfpg.InsertJobParams) (csfpg.CsfJob, error) {
				Expect(params.Kind).To(Equal("overnight.compute"))
				Expect(params.TotalUnits).To(Equal(int64(4)))
				Expect(params.TimeoutSeconds).To(Equal(int32(3600)))
				Expect(string(params.Request)).To(Equal(`{"command":"sweep","shards":4}`))
				Expect(params.RequestSha256).To(Equal(stored.RequestSha256))
				return stored, nil
			}),
		)
		job, err := ledger.Submit(ctx, admission)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Spec).To(Equal(overnightSpec{Command: "sweep", Shards: 4}))
		Expect(job.State).To(Equal(jobs.StatePending))
		Expect(job.Timeout).To(Equal(time.Hour))
	})
	It("returns the recorded job for an identical repeat without reserving again", func() {
		opened()
		fixture.tx.EXPECT().GetJob(ctx, gomock.Any()).Return(storedJob(admission, csfpg.CsfJobStateRunning), nil)
		job, err := ledger.Submit(ctx, admission)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.State).To(Equal(jobs.StateRunning))
	})
	It("refuses to reuse an identity for another request", func() {
		opened()
		fixture.tx.EXPECT().GetJob(ctx, gomock.Any()).Return(storedJob(admission, csfpg.CsfJobStatePending), nil)
		admission.Spec.Shards = 5
		_, err := ledger.Submit(ctx, admission)
		Expect(err).To(MatchError(jobs.ErrIdentityReused))
	})
	It("refuses an admission the account cannot cover", func() {
		opened()
		fixture.tx.EXPECT().GetJob(ctx, gomock.Any()).Return(csfpg.CsfJob{}, pgx.ErrNoRows)
		fixture.tx.EXPECT().ReserveJobBudget(ctx, gomock.Any()).Return(int64(0), nil)
		_, err := ledger.Submit(ctx, admission)
		Expect(err).To(MatchError(jobs.ErrBudgetExhausted))
	})
	It("refuses to change an account's limit", func() {
		fixture.tx.EXPECT().EnsureJobBudget(ctx, gomock.Any()).Return(csfpg.CsfJobBudget{}, pgx.ErrNoRows)
		_, err := ledger.Submit(ctx, admission)
		Expect(err).To(MatchError(jobs.ErrBudgetChanged))
	})
	DescribeTable("refuses a malformed admission before touching the database",
		func(change func(admission *jobs.Admission[overnightSpec]), want error) {
			change(&admission)
			_, err := ledger.Submit(ctx, admission)
			Expect(err).To(MatchError(want))
		},
		Entry("an identity the schema refuses", func(admission *jobs.Admission[overnightSpec]) { admission.ID = "-dash" }, jobs.ErrInvalidAdmission),
		Entry("a kind this ledger does not decode", func(admission *jobs.Admission[overnightSpec]) { admission.Kind = "simulation.carla" }, jobs.ErrUnknownKind),
		Entry("an invalid executor name", func(admission *jobs.Admission[overnightSpec]) { admission.Executor = "" }, jobs.ErrInvalidAdmission),
		Entry("no units of work", func(admission *jobs.Admission[overnightSpec]) { admission.TotalUnits = 0 }, jobs.ErrInvalidAdmission),
		Entry("a negative reservation", func(admission *jobs.Admission[overnightSpec]) { admission.ReservationUSDMicros = -1 }, jobs.ErrInvalidAdmission),
		Entry("a fractional timeout", func(admission *jobs.Admission[overnightSpec]) { admission.Timeout = 1500 * time.Millisecond }, jobs.ErrInvalidAdmission),
	)
	It("reports missing jobs and an unopened account with their defined errors", func() {
		fixture.database.EXPECT().GetJob(ctx, gomock.Any()).Return(csfpg.CsfJob{}, pgx.ErrNoRows)
		_, err := ledger.Get(ctx, "absent")
		Expect(err).To(MatchError(jobs.ErrJobNotFound))
		fixture.database.EXPECT().RequestJobCancellation(ctx, gomock.Any()).Return(csfpg.CsfJob{}, pgx.ErrNoRows)
		_, err = ledger.Cancel(ctx, "absent")
		Expect(err).To(MatchError(jobs.ErrJobNotFound))
		fixture.tx.EXPECT().LockJob(ctx, gomock.Any()).Return(csfpg.CsfJob{}, pgx.ErrNoRows)
		Expect(ledger.Record(ctx, "absent", jobs.Observation{})).To(MatchError(jobs.ErrJobNotFound))
		fixture.database.EXPECT().GetJobBudget(ctx, "overnight").Return(csfpg.CsfJobBudget{}, pgx.ErrNoRows)
		_, err = ledger.BudgetUsage(ctx)
		Expect(err).To(MatchError(jobs.ErrBudgetNotOpened))
	})
})

var _ = Describe("a remote job's lifecycle", func() {
	var fixture ledgerFixture
	var executor *jobsmock.MockIRemoteExecutor[overnightSpec]
	var ledger *jobs.JobLedger[overnightSpec]
	var admission jobs.Admission[overnightSpec]
	ctx := context.Background()
	evidence := strings.Repeat("e", 64)
	recorded := time.Date(2026, 10, 1, 12, 5, 0, 0, time.UTC)
	BeforeEach(func() {
		fixture = newLedgerFixture()
		executor = jobsmock.NewMockIRemoteExecutor[overnightSpec](gomock.NewController(GinkgoT()))
		ledger = fixture.ledger(jobs.WithRemoteExecutor[overnightSpec](overnightExecutor, executor))
		admission = overnightAdmission()
	})
	noPendingWork := func() {
		fixture.database.EXPECT().MarkInterruptedJobSubmissions(gomock.Any(), gomock.Any()).Return(nil)
		fixture.database.EXPECT().ClaimJobSubmission(gomock.Any(), gomock.Any()).Return(csfpg.CsfJob{}, pgx.ErrNoRows)
	}

	It("submits once, records progress and measurements, and confirms cleanup only after cancellation completes", func() {
		claimed := storedJob(admission, csfpg.CsfJobStateSubmitting)
		queued := claimed
		queued.State, queued.ExternalID = csfpg.CsfJobStateQueued, "aws-1"
		By("submitting the claimed job exactly once")
		gomock.InOrder(
			fixture.database.EXPECT().MarkInterruptedJobSubmissions(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, params csfpg.MarkInterruptedJobSubmissionsParams) error {
				Expect(params.Executor).To(Equal("aws_batch"))
				Expect(params.GraceSeconds).To(BeNumerically(">", 0))
				return nil
			}),
			fixture.database.EXPECT().ClaimJobSubmission(gomock.Any(), csfpg.ClaimJobSubmissionParams{Executor: "aws_batch", Kinds: []string{"overnight.compute"}}).Return(claimed, nil),
			executor.EXPECT().Submit(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, job jobs.Job[overnightSpec]) (string, error) {
				Expect(job.Spec.Command).To(Equal("sweep"))
				return "aws-1", nil
			}),
			fixture.database.EXPECT().FinishJobSubmission(gomock.Any(), csfpg.FinishJobSubmissionParams{JobID: "nightly-1", State: csfpg.CsfJobStateQueued, ExternalID: "aws-1"}).Return(int64(1), nil),
			fixture.database.EXPECT().PollableJobs(gomock.Any(), gomock.Any()).Return(nil, nil),
		)
		Expect(ledger.Reconcile(ctx)).To(Succeed())

		By("observing the running job and recording the log page's measurements")
		noPendingWork()
		fixture.database.EXPECT().PollableJobs(gomock.Any(), gomock.Any()).Return([]csfpg.CsfJob{queued}, nil)
		executor.EXPECT().Observe(gomock.Any(), gomock.Any()).Return(jobs.Execution{State: jobs.StateRunning, ExternalID: "aws-1", LogStream: "stream"}, nil)
		fixture.database.EXPECT().UpdateJobExecution(gomock.Any(), csfpg.UpdateJobExecutionParams{JobID: "nightly-1", State: csfpg.CsfJobStateRunning, ExternalID: "aws-1", LogStream: "stream"}).Return(nil)
		executor.EXPECT().ReadLog(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, job jobs.Job[overnightSpec]) (jobs.LogPage, error) {
			Expect(job.LogStream).To(Equal("stream"))
			return jobs.LogPage{NextCursor: "page-1", Observation: jobs.Observation{EvidenceHash: evidence, Entries: []jobs.ObservationEntry{
				jobs.DefinitionEntry(jobs.MetricDefinition{Name: "shards_done", Unit: "count", Description: "finished shards"}),
				jobs.MeasurementEntry(jobs.Measurement{Metric: "shards_done", Step: 2, Value: 2, RecordedAt: recorded}),
			}}}, nil
		})
		running := queued
		running.State = csfpg.CsfJobStateRunning
		gomock.InOrder(
			fixture.tx.EXPECT().LockJob(gomock.Any(), csfpg.LockJobParams{JobID: "nightly-1", Kinds: []string{"overnight.compute"}}).Return(running, nil),
			fixture.tx.EXPECT().ListJobMetricDefinitions(gomock.Any(), "nightly-1").Return(nil, nil),
			fixture.tx.EXPECT().InsertJobMetricDefinition(gomock.Any(), csfpg.InsertJobMetricDefinitionParams{JobID: "nightly-1", Name: "shards_done", Unit: "count", Description: "finished shards"}).Return(int64(1), nil),
			fixture.tx.EXPECT().InsertJobMeasurement(gomock.Any(), csfpg.InsertJobMeasurementParams{JobID: "nightly-1", Metric: "shards_done", Step: 2, Value: 2, RecordedAt: pgtype.Timestamptz{Time: recorded, Valid: true}, EvidenceHash: evidence}).Return(int64(1), nil),
			fixture.tx.EXPECT().RecordJobProgress(gomock.Any(), csfpg.RecordJobProgressParams{JobID: "nightly-1", CompletedUnits: 2, State: csfpg.CsfJobStateRunning}).Return(nil),
		)
		fixture.database.EXPECT().SetJobLogCursor(gomock.Any(), csfpg.SetJobLogCursorParams{JobID: "nightly-1", LogCursor: "page-1"}).Return(nil)
		Expect(ledger.Reconcile(ctx)).To(Succeed())

		By("reading the measurements back with their evidence")
		fixture.database.EXPECT().LatestJobMeasurements(ctx, "nightly-1").Return([]csfpg.CsfJobMeasurement{{JobID: "nightly-1", Metric: "shards_done", Step: 2, Value: 2, RecordedAt: pgtype.Timestamptz{Time: recorded, Valid: true}, EvidenceHash: evidence}}, nil)
		measurements, err := ledger.Measurements(ctx, "nightly-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(measurements).To(ConsistOf(jobs.RecordedMeasurement{Measurement: jobs.Measurement{Metric: "shards_done", Step: 2, Value: 2, RecordedAt: recorded}, EvidenceHash: evidence}))

		By("requesting cancellation, which a running job keeps until the executor confirms it")
		cancelling := running
		cancelling.CompletedUnits, cancelling.CancellationRequested, cancelling.LogStream = 2, true, "stream"
		fixture.database.EXPECT().RequestJobCancellation(ctx, csfpg.RequestJobCancellationParams{JobID: "nightly-1", Kinds: []string{"overnight.compute"}}).Return(cancelling, nil)
		job, err := ledger.Cancel(ctx, "nightly-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(job.CancellationRequested).To(BeTrue())
		Expect(job.CleanupConfirmed).To(BeFalse())

		By("confirming cleanup once the executor reports the external job terminal")
		noPendingWork()
		fixture.database.EXPECT().PollableJobs(gomock.Any(), gomock.Any()).Return([]csfpg.CsfJob{cancelling}, nil)
		executor.EXPECT().Observe(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, job jobs.Job[overnightSpec]) (jobs.Execution, error) {
			Expect(job.CancellationRequested).To(BeTrue())
			return jobs.Execution{State: jobs.StateCancelled, ExternalID: "aws-1", Reason: "operator", CleanupConfirmed: true}, nil
		})
		fixture.database.EXPECT().UpdateJobExecution(gomock.Any(), csfpg.UpdateJobExecutionParams{JobID: "nightly-1", State: csfpg.CsfJobStateCancelled, ExternalID: "aws-1", Reason: "operator", CleanupConfirmed: true}).Return(nil)
		Expect(ledger.Reconcile(ctx)).To(Succeed())
	})
	It("keeps a lost submission reply as submission_unknown and never resubmits it", func() {
		fixture.database.EXPECT().MarkInterruptedJobSubmissions(gomock.Any(), gomock.Any()).Return(nil).Times(2)
		gomock.InOrder(
			fixture.database.EXPECT().ClaimJobSubmission(gomock.Any(), gomock.Any()).Return(storedJob(admission, csfpg.CsfJobStateSubmitting), nil),
			executor.EXPECT().Submit(gomock.Any(), gomock.Any()).Return("", errors.New("response lost after request sent")),
			fixture.database.EXPECT().FinishJobSubmission(gomock.Any(), csfpg.FinishJobSubmissionParams{JobID: "nightly-1", State: csfpg.CsfJobStateSubmissionUnknown, Reason: "response lost after request sent"}).Return(int64(1), nil),
			fixture.database.EXPECT().ClaimJobSubmission(gomock.Any(), gomock.Any()).Return(csfpg.CsfJob{}, pgx.ErrNoRows),
		)
		fixture.database.EXPECT().PollableJobs(gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)
		Expect(ledger.Reconcile(ctx)).To(Succeed())
		Expect(ledger.Reconcile(ctx)).To(Succeed())
	})
	It("treats an empty provider identity as an ambiguous submission", func() {
		fixture.database.EXPECT().MarkInterruptedJobSubmissions(gomock.Any(), gomock.Any()).Return(nil)
		fixture.database.EXPECT().ClaimJobSubmission(gomock.Any(), gomock.Any()).Return(storedJob(admission, csfpg.CsfJobStateSubmitting), nil)
		executor.EXPECT().Submit(gomock.Any(), gomock.Any()).Return("", nil)
		fixture.database.EXPECT().FinishJobSubmission(gomock.Any(), csfpg.FinishJobSubmissionParams{JobID: "nightly-1", State: csfpg.CsfJobStateSubmissionUnknown, Reason: jobs.ErrNoExternalIdentity.Error()}).Return(int64(1), nil)
		fixture.database.EXPECT().PollableJobs(gomock.Any(), gomock.Any()).Return(nil, nil)
		Expect(ledger.Reconcile(ctx)).To(Succeed())
	})
	It("records an executor failure on the job instead of failing the pass", func() {
		noPendingWork()
		fixture.database.EXPECT().PollableJobs(gomock.Any(), gomock.Any()).Return([]csfpg.CsfJob{storedJob(admission, csfpg.CsfJobStateQueued)}, nil)
		executor.EXPECT().Observe(gomock.Any(), gomock.Any()).Return(jobs.Execution{}, jobs.ErrForeignResource)
		fixture.database.EXPECT().SetJobInspectionError(gomock.Any(), csfpg.SetJobInspectionErrorParams{JobID: "nightly-1", InspectionError: jobs.ErrForeignResource.Error()}).Return(nil)
		Expect(ledger.Reconcile(ctx)).To(Succeed())
	})
	It("refuses a replayed measurement with another value", func() {
		fixture.tx.EXPECT().LockJob(ctx, gomock.Any()).Return(storedJob(admission, csfpg.CsfJobStateRunning), nil)
		fixture.tx.EXPECT().ListJobMetricDefinitions(ctx, "nightly-1").Return([]csfpg.CsfJobMetricDefinition{{JobID: "nightly-1", Name: "shards_done", Unit: "count"}}, nil)
		fixture.tx.EXPECT().InsertJobMeasurement(ctx, gomock.Any()).Return(int64(0), nil)
		err := ledger.Record(ctx, "nightly-1", jobs.Observation{EvidenceHash: evidence, Entries: []jobs.ObservationEntry{jobs.MeasurementEntry(jobs.Measurement{Metric: "shards_done", Step: 1, Value: 1, RecordedAt: recorded})}})
		Expect(err).To(MatchError(jobs.ErrConflictingMeasurement))
	})
})

var _ = Describe("a host job's reconciliation", func() {
	var fixture ledgerFixture
	var executor *jobsmock.MockIHostExecutor[overnightSpec]
	var ledger *jobs.JobLedger[overnightSpec]
	ctx := context.Background()
	BeforeEach(func() {
		fixture = newLedgerFixture()
		executor = jobsmock.NewMockIHostExecutor[overnightSpec](gomock.NewController(GinkgoT()))
		ledger = fixture.ledger(jobs.WithHostExecutor[overnightSpec](hostExecutor, executor))
	})
	It("does nothing while another process holds the executor lock", func() {
		fixture.tx.EXPECT().LockJobExecutor(gomock.Any(), "local").Return(false, nil)
		Expect(ledger.Reconcile(ctx)).To(Succeed())
	})
	It("records the execution and then the executor's problem under the lock", func() {
		admission := overnightAdmission()
		admission.Executor = hostExecutor
		gomock.InOrder(
			fixture.tx.EXPECT().LockJobExecutor(gomock.Any(), "local").Return(true, nil),
			fixture.tx.EXPECT().NextHostJob(gomock.Any(), csfpg.NextHostJobParams{Executor: "local", Kinds: []string{"overnight.compute"}}).Return(storedJob(admission, csfpg.CsfJobStatePending), nil),
			executor.EXPECT().Reconcile(gomock.Any(), gomock.Any()).Return(jobs.Execution{State: jobs.StateSubmitting, ExternalID: "container"}, errors.New("lost reply")),
			fixture.tx.EXPECT().UpdateJobExecution(gomock.Any(), csfpg.UpdateJobExecutionParams{JobID: "nightly-1", State: csfpg.CsfJobStateSubmitting, ExternalID: "container"}).Return(nil),
			fixture.tx.EXPECT().SetJobInspectionError(gomock.Any(), csfpg.SetJobInspectionErrorParams{JobID: "nightly-1", InspectionError: "lost reply"}).Return(nil),
		)
		Expect(ledger.Reconcile(ctx)).To(Succeed())
	})
	It("passes a database failure back to the caller", func() {
		failure := errors.New("connection reset")
		fixture.tx.EXPECT().LockJobExecutor(gomock.Any(), "local").Return(false, failure)
		Expect(ledger.Reconcile(ctx)).To(MatchError(failure))
	})
})

var _ = Describe("trace delivery", func() {
	var fixture ledgerFixture
	var ledger *jobs.JobLedger[overnightSpec]
	ctx := context.Background()
	delivery := jobs.TraceDelivery{Destination: "langfuse", JobID: "nightly-1", TraceID: strings.Repeat("d", 32)}
	BeforeEach(func() {
		fixture = newLedgerFixture()
		ledger = fixture.ledger()
	})
	It("claims a delivery once and records an unconfirmed send as ambiguous", func() {
		fixture.database.EXPECT().ClaimJobTraceDelivery(ctx, csfpg.ClaimJobTraceDeliveryParams{Destination: "langfuse", JobID: "nightly-1", TraceID: delivery.TraceID}).Return(delivery.TraceID, nil)
		claimed, err := ledger.ClaimTraceDelivery(ctx, delivery)
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeTrue())
		fixture.database.EXPECT().FinishJobTraceDelivery(ctx, csfpg.FinishJobTraceDeliveryParams{Destination: "langfuse", TraceID: delivery.TraceID, State: csfpg.CsfTraceDeliveryStateAmbiguous, Error: "lost acknowledgement"}).Return(delivery.TraceID, nil)
		Expect(ledger.FinishTraceDelivery(ctx, delivery, errors.New("lost acknowledgement"))).To(Succeed())
		fixture.database.EXPECT().ClaimJobTraceDelivery(ctx, gomock.Any()).Return("", pgx.ErrNoRows)
		claimed, err = ledger.ClaimTraceDelivery(ctx, delivery)
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeFalse(), "a second claim of the same trace is refused")
		fixture.database.EXPECT().GetJobTraceDelivery(ctx, gomock.Any()).Return(csfpg.CsfJobTraceDelivery{State: csfpg.CsfTraceDeliveryStateAmbiguous}, nil)
		Expect(ledger.TraceDeliveryState(ctx, delivery)).To(Equal(jobs.DeliveryAmbiguous))
	})
	It("refuses to finish a delivery that is not attempted", func() {
		fixture.database.EXPECT().FinishJobTraceDelivery(ctx, gomock.Any()).Return("", pgx.ErrNoRows)
		Expect(ledger.FinishTraceDelivery(ctx, delivery, nil)).To(HaveOccurred())
	})
})
