package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/pgmem"
	"github.com/candacelabs/csf/pkg/sqlmigrate"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

var migrationNames = []string{"000001_copilot_adapter.up.sql"}

// openSchema applies the embedded schema to a new pgmem database and returns
// it with the csfpg capability over it, both closed when the spec ends.
func openSchema(ctx context.Context) (*pgmem.DB, *pgmem.PGX) {
	GinkgoHelper()
	database, err := pgmem.NewContext(ctx)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(database.Close)
	handle := database.Open()
	DeferCleanup(handle.Close)
	Expect(sqlmigrate.Apply(ctx, handle, Migrations, MigrationsDirectory)).To(Succeed())
	capability := database.Public().OpenPGX()
	DeferCleanup(capability.Close)
	return database, capability
}

func fixtureWorktree(id uuid.UUID, at time.Time) storedb.CreateWorktreeParams {
	return storedb.CreateWorktreeParams{
		ID: id, RepositoryID: "fixture", RepositoryRoot: "/fixture",
		Path: "/fixture/" + id.String(), BaseRef: "HEAD", CreatedAt: at, UpdatedAt: at,
	}
}

var _ = Describe("schema baseline", func() {
	It("applies one complete schema migration idempotently", func(ctx SpecContext) {
		entries, err := Migrations.ReadDir(MigrationsDirectory)
		Expect(err).NotTo(HaveOccurred())
		var names []string
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
				continue
			}
			names = append(names, entry.Name())
		}
		sort.Strings(names)
		Expect(names).To(Equal(migrationNames))

		database, capability := openSchema(ctx)
		handle := database.Open()
		DeferCleanup(handle.Close)
		Expect(sqlmigrate.Apply(ctx, handle, Migrations, MigrationsDirectory)).To(Succeed())
		var applied int
		Expect(handle.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE name = $1", migrationNames[0]).Scan(&applied)).To(Succeed())
		Expect(applied).To(Equal(1))

		queries := storedb.New(capability)
		at := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
		worktreeID, sessionID := uuid.New(), uuid.New()
		_, err = queries.CreateWorktree(ctx, fixtureWorktree(worktreeID, at))
		Expect(err).NotTo(HaveOccurred())
		created, err := queries.CreateSession(ctx, storedb.CreateSessionParams{
			ID: sessionID, WorktreeID: worktreeID, DisplayName: "fixture", Model: "fixture-model",
			WorkingDirectory: "/fixture/worktree", SystemInstructions: "", PermissionMode: "ask",
			Status: "idle", CreatedAt: at, UpdatedAt: at,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(created.ID).To(Equal(sessionID))
		Expect(created.WorktreeID).To(Equal(worktreeID))
		Expect(created.PermissionMode).To(Equal("ask"))
	})
})

var _ = Describe("PostgresStore", func() {
	var (
		persistence *PostgresStore
		at          time.Time
	)

	BeforeEach(func(ctx SpecContext) {
		_, capability := openSchema(ctx)
		var err error
		persistence, err = NewPostgresStore(capability)
		Expect(err).NotTo(HaveOccurred())
		at = time.Date(2026, 10, 2, 6, 0, 0, 0, time.UTC)
	})

	It("refuses a nil database capability", func() {
		_, err := NewPostgresStore(nil)
		Expect(err).To(MatchError(ContainSubstring("nil database")))
	})

	It("refuses a nil transaction operation", func(ctx SpecContext) {
		Expect(persistence.Transact(ctx, nil)).To(MatchError(ContainSubstring("nil transaction operation")))
	})

	It("commits every write a successful transaction made", func(ctx SpecContext) {
		worktreeID := uuid.New()
		Expect(persistence.Transact(ctx, func(queries storedb.Querier) error {
			_, err := queries.CreateWorktree(ctx, fixtureWorktree(worktreeID, at))
			return err
		})).To(Succeed())
		stored, err := persistence.GetWorktree(ctx, worktreeID)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.ID).To(Equal(worktreeID))
	})

	It("rolls back every write when the operation fails, and reports a missing row as pgx.ErrNoRows", func(ctx SpecContext) {
		worktreeID := uuid.New()
		refused := errors.New("operation refused")
		Expect(persistence.Transact(ctx, func(queries storedb.Querier) error {
			if _, err := queries.CreateWorktree(ctx, fixtureWorktree(worktreeID, at)); err != nil {
				return err
			}
			return refused
		})).To(MatchError(refused))
		_, err := persistence.GetWorktree(ctx, worktreeID)
		Expect(err).To(MatchError(pgx.ErrNoRows))
	})
})
