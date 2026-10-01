package store

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/pgmem"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

var migrationNames = []string{"000001_copilot_adapter.up.sql"}

var _ = Describe("schema baseline", func() {
	var (
		ctx context.Context
		db  *sql.DB
	)

	BeforeEach(func() {
		ctx = context.Background()
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db = database.Open()
		DeferCleanup(db.Close)
	})

	It("applies one complete schema migration idempotently", func() {
		entries, err := migrationFiles.ReadDir(migrationsDirectory)
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

		Expect(ApplyMigrations(ctx, db)).To(Succeed())
		Expect(ApplyMigrations(ctx, db)).To(Succeed())
		var applied int
		Expect(db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE name = $1", migrationNames[0]).Scan(&applied)).To(Succeed())
		Expect(applied).To(Equal(1))

		queries := storedb.New(db)
		at := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
		worktreeID, sessionID := uuid.New(), uuid.New()
		_, err = queries.CreateWorktree(ctx, storedb.CreateWorktreeParams{
			ID: worktreeID, RepositoryID: "fixture", RepositoryRoot: "/fixture",
			Path: "/fixture/worktree", BaseRef: "HEAD", CreatedAt: at, UpdatedAt: at,
		})
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
