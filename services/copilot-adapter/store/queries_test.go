package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/pgmem"

	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

var _ = Describe("queries", func() {
	It("orders worktrees by their latest associated session activity with deterministic ties", func() {
		ctx := context.Background()
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		Expect(ApplyMigrations(ctx, db)).To(Succeed())
		queries := storedb.New(db)
		base := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
		activeID := uuid.MustParse("00000000-0000-4000-8000-000000000000")
		tiedSessionID := uuid.MustParse("00000000-0000-4000-8000-000000000001")
		tiedNoSessionID := uuid.MustParse("00000000-0000-4000-8000-000000000002")
		for _, parameters := range []storedb.CreateWorktreeParams{
			{ID: activeID, RepositoryID: "repo", RepositoryRoot: "/repo", Path: "/repo/active", BaseRef: "main", CreatedAt: base, UpdatedAt: base},
			{ID: tiedSessionID, RepositoryID: "repo", RepositoryRoot: "/repo", Path: "/repo/tied-session", BaseRef: "main", CreatedAt: base, UpdatedAt: base.Add(2 * time.Hour)},
			{ID: tiedNoSessionID, RepositoryID: "repo", RepositoryRoot: "/repo", Path: "/repo/tied-empty", BaseRef: "main", CreatedAt: base, UpdatedAt: base.Add(2 * time.Hour)},
		} {
			_, err := queries.CreateWorktree(ctx, parameters)
			Expect(err).NotTo(HaveOccurred())
		}
		sessionID := uuid.New()
		_, err := queries.CreateSession(ctx, storedb.CreateSessionParams{
			ID: sessionID, WorktreeID: activeID, DisplayName: "active chat", Model: "gpt-5",
			WorkingDirectory: "/repo/active", Status: "idle", CreatedAt: base, UpdatedAt: base,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.TouchSessionLastTurn(ctx, storedb.TouchSessionLastTurnParams{
			ID: sessionID, LastTurnAt: null.TimeFrom(base.Add(3 * time.Hour)), UpdatedAt: base.Add(3 * time.Hour),
		})
		Expect(err).NotTo(HaveOccurred())

		rows, err := queries.ListWorktrees(ctx)
		Expect(err).NotTo(HaveOccurred())
		identifiers := make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			identifiers = append(identifiers, row.ID)
		}
		Expect(identifiers).To(Equal([]uuid.UUID{activeID, tiedNoSessionID, tiedSessionID}))
	})
})
