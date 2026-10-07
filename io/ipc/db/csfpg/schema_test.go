// Copyright 2026 Candace Labs

package csfpg_test

import (
	"context"
	"database/sql"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/examples/csfpg-consumer"
	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	"github.com/candacelabs/csf/pkg/pgmem"
	"github.com/candacelabs/csf/pkg/sqlmigrate"
)

// csfTables lists the tables csf_rows.sql writes one valid row into, so each
// one's CHECK constraints, foreign keys, enum columns and defaults evaluate at
// least once.
var csfTables = []string{
	"csf_documents", "csf_source_revisions", "csf_projection_tasks", "csf_nodes", "csf_edges",
	"csf_job_budgets", "csf_jobs", "csf_job_metric_definitions", "csf_job_measurements",
	"csf_job_trace_deliveries", "csf_agent_configurations",
	"csf_meter_measurements", "csf_meter_invariants",
}

const (
	countVersions       = "SELECT COUNT(*) FROM "
	initMigration       = "001_init.sql"
	evalSuiteMigration  = "002_eval_suite.sql"
	scoreboardMigration = "003_scoreboard.sql"
	consumerTable       = "example_notes"
	consumerColumns     = "INSERT INTO example_notes (note_id, body) VALUES ($1, $2)"
)

func fixture(name string) string {
	body, err := os.ReadFile("testdata/" + name)
	Expect(err).NotTo(HaveOccurred())
	return string(body)
}

// versions counts a table's rows: a version table's applied migrations, or a
// fixture table's rows.
func versions(ctx context.Context, handle *sql.DB, table string) int {
	var count int
	Expect(handle.QueryRowContext(ctx, countVersions+table).Scan(&count)).To(Succeed())
	return count
}

// The schema specs run CSF's real migration on pgmem: no PostgreSQL server,
// no socket, and no DDL in Go.
var _ = Describe("CSF's schema on pgmem", func() {
	var (
		ctx      context.Context
		database *pgmem.DB
		handle   *sql.DB
	)

	BeforeEach(func() {
		ctx = context.Background()
		database = pgmem.MustNew()
		DeferCleanup(database.Close)
		handle = database.Open()
		DeferCleanup(handle.Close)
	})

	It("ships its migrations in order, one file per change", func() {
		entries, err := csfpg.Schema.ReadDir(csfpg.SchemaDirectory)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(3))
		Expect(entries[0].Name()).To(Equal(initMigration))
		Expect(entries[1].Name()).To(Equal(evalSuiteMigration))
		Expect(entries[2].Name()).To(Equal(scoreboardMigration))
	})

	It("applies once under csf_schema_version and stacks a consumer's migrations under the consumer's own table", func() {
		Expect(csfpg.ApplySchema(ctx, handle)).To(Succeed())
		Expect(sqlmigrate.ApplyVersioned(ctx, handle, csfpgconsumer.Migrations, csfpgconsumer.MigrationDirectory, csfpgconsumer.VersionTable)).To(Succeed())
		Expect(csfpg.ApplySchema(ctx, handle)).To(Succeed(), "a second start applies nothing")
		Expect(sqlmigrate.ApplyVersioned(ctx, handle, csfpgconsumer.Migrations, csfpgconsumer.MigrationDirectory, csfpgconsumer.VersionTable)).To(Succeed())

		Expect(versions(ctx, handle, csfpg.SchemaVersionTable)).To(Equal(3))
		Expect(versions(ctx, handle, csfpgconsumer.VersionTable)).To(Equal(1), "both sets number from 001 without colliding")
		_, err := handle.ExecContext(ctx, consumerColumns, "note-1", "stacked on CSF")
		Expect(err).NotTo(HaveOccurred())
		Expect(database.Public().None(fixture("csf_rows.sql"))).To(Succeed(), "CSF's tables are intact beside "+consumerTable)
	})

	It("holds one valid row in every table, with defaults and enum labels", func() {
		Expect(csfpg.ApplySchema(ctx, handle)).To(Succeed())
		Expect(database.Public().None(fixture("csf_rows.sql"))).To(Succeed())

		for _, table := range csfTables {
			Expect(versions(ctx, handle, table)).To(BeNumerically(">=", 1), table)
		}
		defaults, err := database.Public().One(fixture("fixture_defaults.sql"))
		Expect(err).NotTo(HaveOccurred())
		Expect(defaults["stamped"]).To(BeNumerically("==", 1))
		Expect(defaults["task_status"]).To(Equal("failed"))
		Expect(defaults["job_state"]).To(Equal("cancelling"))
	})

	DescribeTable("rejects rows the PostgreSQL CHECK constraints reject",
		func(rejected string) {
			Expect(csfpg.ApplySchema(ctx, handle)).To(Succeed())
			Expect(database.Public().None(fixture("csf_rows.sql"))).To(Succeed())

			err := database.Public().None(fixture(rejected))

			var failure *pgmem.Error
			Expect(err).To(BeAssignableToTypeOf(failure))
			Expect(err.(*pgmem.Error).Code).To(Equal(checkViolation))
		},
		Entry("a path escaping the artifact root (regular expression)", "escaping_artifact_ref.sql"),
		Entry("a trailing separator (right)", "trailing_separator.sql"),
		Entry("a doubled separator (position)", "doubled_separator.sql"),
		Entry("an infinite measurement ('Infinity'::DOUBLE PRECISION)", "infinite_measurement.sql"),
		Entry("a job admitting no units of work", "job_without_units.sql"),
		Entry("job progress past its admitted units", "progress_past_total.sql"),
		Entry("a budget reservation past the account limit", "overdrawn_budget.sql"),
		Entry("a measurement day that is not an ISO date", "bad_measured_on.sql"),
		Entry("an invariant satisfied past the instances it was checked over", "invariant_past_checked.sql"),
	)
})

const checkViolation = "23514"
