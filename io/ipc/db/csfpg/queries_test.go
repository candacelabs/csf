// Copyright 2026 Candace Labs

package csfpg_test

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	"github.com/candacelabs/csf/pkg/pgmem"
)

// The generated queries run against CSF real schema on pgmem through the
// pgx surface, so these specs exercise the SQL sqlc generated, not a double.
var _ csfpg.IDB = (*pgmem.PGX)(nil)

const (
	budgetAccount = "budget-pgmem"
	jobID         = "job-pgmem"
	jobKind       = "overnight.compute"
	agentID       = "agent-pgmem"
	sourceID      = "source-pgmem"
	revision      = "v1"
)

var _ = Describe("the generated queries on pgmem", func() {
	var (
		ctx      context.Context
		database *pgmem.PGX
		queries  *csfpg.Queries
	)

	BeforeEach(func() {
		ctx = context.Background()
		engine := pgmem.MustNew()
		DeferCleanup(engine.Close)
		handle := engine.Open()
		DeferCleanup(handle.Close)
		Expect(csfpg.ApplySchema(ctx, handle)).To(Succeed())
		database = engine.Public().OpenPGX()
		DeferCleanup(database.Close)
		queries = csfpg.New(database)
	})

	It("runs queries.sql: an idempotent document create, a source revision and its projection task", func() {
		hash := strings.Repeat("a", 64)
		document, err := queries.CreateDocument(ctx, csfpg.CreateDocumentParams{ContentHash: hash, ByteSize: 7, ArtifactRef: "documents/a.txt"})
		Expect(err).NotTo(HaveOccurred())
		Expect(document.CreatedAt.Valid).To(BeTrue())
		again, err := queries.CreateDocument(ctx, csfpg.CreateDocumentParams{ContentHash: hash, ByteSize: 7, ArtifactRef: "documents/elsewhere.txt"})
		Expect(err).NotTo(HaveOccurred())
		Expect(again.ArtifactRef).To(Equal("documents/a.txt"), "a repeat keeps the first locator")

		retrieved := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
		_, err = queries.CreateSourceRevision(ctx, csfpg.CreateSourceRevisionParams{
			SourceID: sourceID, Revision: revision, ContentHash: hash, SourceUri: "urn:pgmem", Title: "Fixture",
			MediaType: "text/plain", License: "test-fixture", RetrievedAt: pgtype.Timestamptz{Time: retrieved, Valid: true},
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.EnqueueProjectionTask(ctx, csfpg.EnqueueProjectionTaskParams{SourceID: sourceID, Revision: revision})
		Expect(err).NotTo(HaveOccurred())
		task, err := queries.GetProjectionTask(ctx, csfpg.GetProjectionTaskParams{SourceID: sourceID, Revision: revision})
		Expect(err).NotTo(HaveOccurred())
		Expect(task.Status).To(Equal(csfpg.CsfProjectionStatusPending))
		Expect(task.LeaseUntil.Valid).To(BeFalse())

		_, err = queries.GetDocument(ctx, strings.Repeat("b", 64))
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
	})

	It("runs agent_configurations.sql: one create, then a duplicate reports no row", func() {
		created, err := queries.CreateAgentConfiguration(ctx, csfpg.CreateAgentConfigurationParams{AgentID: agentID, OpensearchIndex: "agent-events"})
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Revision).To(Equal(int64(1)))
		read, err := queries.GetAgentConfiguration(ctx, agentID)
		Expect(err).NotTo(HaveOccurred())
		Expect(read.OpensearchIndex).To(Equal("agent-events"))
		_, err = queries.CreateAgentConfiguration(ctx, csfpg.CreateAgentConfigurationParams{AgentID: agentID})
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
	})

	It("runs jobs.sql inside a transaction: a budget, a job and its metric definitions", func() {
		transaction, err := database.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = transaction.Rollback(context.Background()) })
		scoped := queries.WithTx(transaction)
		_, err = scoped.EnsureJobBudget(ctx, csfpg.EnsureJobBudgetParams{Account: budgetAccount, LimitUsdMicros: 1000000})
		Expect(err).NotTo(HaveOccurred())
		job, err := scoped.InsertJob(ctx, csfpg.InsertJobParams{
			JobID: jobID, Kind: jobKind, Executor: "aws_batch", BudgetAccount: budgetAccount,
			Request: []byte(`{"shards":4}`), RequestSha256: strings.Repeat("b", 64), TotalUnits: 10,
			Managed: true, ReservationUsdMicros: 1000, TimeoutSeconds: 600,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(job.State).To(Equal(csfpg.CsfJobStatePending))
		Expect(job.Managed).To(BeTrue())
		inserted, err := scoped.InsertJobMetricDefinition(ctx, csfpg.InsertJobMetricDefinitionParams{JobID: jobID, Name: "reward", Unit: "points", Description: "Episode reward"})
		Expect(err).NotTo(HaveOccurred())
		Expect(inserted).To(Equal(int64(1)), "the command tag carries the affected-row count")
		Expect(transaction.Commit(ctx)).To(Succeed())

		definitions, err := queries.ListJobMetricDefinitions(ctx, jobID)
		Expect(err).NotTo(HaveOccurred())
		Expect(definitions).To(HaveLen(1))
		// The kind-filtered reads (kind = ANY($n::TEXT[])) and the FOR UPDATE
		// locks need array parameters and row locks pgmem does not emulate
		// yet (COMPATIBILITY.md); the acceptance tier runs them on PostgreSQL.
	})

	It("runs ouroboros.sql: an item, a finding recorded once, a proposal, its fixer, a merge and a series point", func() {
		at := pgtype.Timestamptz{Time: time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC), Valid: true}
		Expect(queries.UpsertOuroborosItem(ctx, csfpg.UpsertOuroborosItemParams{Miner: "_template", Item: "run/events.jsonl", ByteSize: 10, MinedAt: at})).To(Succeed())
		inserted, err := queries.InsertOuroborosFinding(ctx, csfpg.InsertOuroborosFindingParams{Miner: "_template", Rule: "invisible", Subject: "run", Item: "run/events.jsonl", Severity: "SEVERITY_S3", Scope: "SCOPE_GENERIC", Finding: []byte(`{}`), FoundAt: at})
		Expect(err).NotTo(HaveOccurred())
		Expect(inserted).To(Equal(int64(1)))
		inserted, err = queries.InsertOuroborosFinding(ctx, csfpg.InsertOuroborosFindingParams{Miner: "_template", Rule: "invisible", Subject: "run", Item: "run/events.jsonl", Severity: "SEVERITY_S3", Scope: "SCOPE_GENERIC", Finding: []byte(`{}`), FoundAt: at})
		Expect(err).NotTo(HaveOccurred())
		Expect(inserted).To(BeZero(), "a repeated finding counts no row")
		proposal, err := queries.UpsertOuroborosProposal(ctx, csfpg.UpsertOuroborosProposalParams{Ticket: 249, Proposal: "IC_1", Miner: "no_self_merge", Corpus: "pull_request", Instances: []byte(`["#226"]`), Verdict: "launch", Reason: "found", CheckedAt: at})
		Expect(err).NotTo(HaveOccurred())
		Expect(proposal.Verdict).To(Equal("launch"))
		fixer, err := queries.InsertOuroborosFixer(ctx, csfpg.InsertOuroborosFixerParams{AssignmentID: "0caf2d51-1ee7-468a-882f-ac478a437640", Ticket: 249, Proposal: "IC_1", Miner: "no_self_merge", Model: "claude-haiku-4-5-20251001", StartedAt: at})
		Expect(err).NotTo(HaveOccurred())
		Expect(fixer.Outcome).To(Equal("running"))
		updated, err := queries.UpdateOuroborosFixer(ctx, csfpg.UpdateOuroborosFixerParams{FinishedAt: at, Outcome: "ready", PullRequestUrl: "https://example.invalid/pull/1", CostUsdMicros: 741776, AssignmentID: fixer.AssignmentID})
		Expect(err).NotTo(HaveOccurred())
		Expect(updated).To(Equal(int64(1)))
		merge, err := queries.InsertOuroborosMerge(ctx, csfpg.InsertOuroborosMergeParams{PullRequest: 1, HeadSha: "abc1234", Merger: "csf-serve/1", Merged: false, ExitCode: 1, Reason: "REFUSED", RecordedAt: at})
		Expect(err).NotTo(HaveOccurred())
		Expect(merge.Merged).To(BeFalse())
		value := 4.2
		Expect(queries.UpsertOuroborosSeries(ctx, csfpg.UpsertOuroborosSeriesParams{Series: "ouroboros.struggle_rate_per_1k_tool_calls", Day: "2026-10-04", Value: &value, Numerator: 3, Denominator: 714, ComputedAt: at})).To(Succeed())
		points, err := queries.ListOuroborosSeries(ctx, "ouroboros.struggle_rate_per_1k_tool_calls")
		Expect(err).NotTo(HaveOccurred())
		Expect(points).To(HaveLen(1))
		Expect(*points[0].Value).To(Equal(4.2))
	})

	It("rolls a savepoint back without losing the enclosing transaction", func() {
		transaction, err := database.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.WithTx(transaction).EnsureJobBudget(ctx, csfpg.EnsureJobBudgetParams{Account: "kept", LimitUsdMicros: 1})
		Expect(err).NotTo(HaveOccurred())
		savepoint, err := transaction.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.WithTx(savepoint).CreateDocument(ctx, csfpg.CreateDocumentParams{ContentHash: strings.Repeat("c", 64), ByteSize: 1, ArtifactRef: "documents/c.txt"})
		Expect(err).NotTo(HaveOccurred())
		Expect(savepoint.Rollback(ctx)).To(Succeed())
		Expect(savepoint.Rollback(ctx)).To(MatchError(pgx.ErrTxClosed))
		Expect(transaction.Commit(ctx)).To(Succeed())

		_, err = queries.GetDocument(ctx, strings.Repeat("c", 64))
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue(), "the savepoint row rolled back")
		tag, err := database.Exec(ctx, "UPDATE csf_job_budgets SET limit_usd_micros = $1 WHERE account = $2", 2, "kept")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag.RowsAffected()).To(Equal(int64(1)), "the enclosing transaction committed")
	})
})
