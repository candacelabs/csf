package store

import (
	"context"
	"database/sql"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/pgmem"

	"github.com/candacelabs/csf/pkg/sqlmigrate"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

var migrationNames = []string{
	"000001_copilot_adapter.up.sql",
	"000002_transcript_tool_call_id.up.sql",
	"000003_workbench.up.sql",
	"000004_cron_ownership.up.sql",
	"000005_event_versions.up.sql",
	"000006_turn_delivery.up.sql",
	"000007_bridge_event_receipts.up.sql",
	"000008_prompt_idempotency.up.sql",
	"000009_session_and_abort_idempotency.up.sql",
	"000010_session_creation_state.up.sql",
	"000011_schedule_creation_idempotency.up.sql",
	"000012_usage_telemetry.up.sql",
	"000013_session_tasks.up.sql",
	"000014_agent_session_identity.up.sql",
}

var _ = Describe("schema upgrades", func() {
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

	It("deduplicates legacy sessions that shared one working directory", func() {
		applyMigrationRange(ctx, db, 0, 2)
		at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
		firstID, secondID := uuid.New(), uuid.New()
		insertLegacySession(ctx, db, firstID, "/workspace/shared", at)
		insertLegacySession(ctx, db, secondID, "/workspace/shared", at.Add(time.Minute))
		applyMigrationRange(ctx, db, 2, len(migrationNames))

		var firstWorktree, secondWorktree uuid.UUID
		Expect(db.QueryRowContext(ctx, "SELECT worktree_id FROM sessions WHERE id = $1", firstID).Scan(&firstWorktree)).To(Succeed())
		Expect(db.QueryRowContext(ctx, "SELECT worktree_id FROM sessions WHERE id = $1", secondID).Scan(&secondWorktree)).To(Succeed())
		Expect(firstWorktree).To(Equal(secondWorktree))
		var firstAgentID, secondAgentID string
		Expect(db.QueryRowContext(ctx, "SELECT agent_id FROM sessions WHERE id = $1", firstID).Scan(&firstAgentID)).To(Succeed())
		Expect(db.QueryRowContext(ctx, "SELECT agent_id FROM sessions WHERE id = $1", secondID).Scan(&secondAgentID)).To(Succeed())
		Expect(firstAgentID).To(BeEmpty())
		Expect(secondAgentID).To(BeEmpty())
		var worktreeCount int
		Expect(db.QueryRowContext(ctx, "SELECT COUNT(*) FROM worktrees").Scan(&worktreeCount)).To(Succeed())
		Expect(worktreeCount).To(Equal(1))
	})

	It("drops unreconstructable reference events while retaining the monotonic cursor", func() {
		applyMigrationRange(ctx, db, 0, 4)
		at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
		sessionID, worktreeID := uuid.New(), uuid.New()
		_, err := db.ExecContext(ctx, `
            INSERT INTO worktrees (id, repository_id, repository_root, path, base_ref, managed, created_at, updated_at)
            VALUES ($1, 'repo', '/workspace', '/workspace', 'HEAD', FALSE, $3, $3);
            INSERT INTO sessions (id, worktree_id, display_name, model, working_directory, system_instructions, status, created_at, updated_at)
            VALUES ($2, $1, 'chat', 'gpt-5', '/workspace', '', 'idle', $3, $3);
            INSERT INTO session_counters (session_id, last_transcript_seq, last_event_seq) VALUES ($2, 0, 8);
            INSERT INTO session_events (session_id, seq, kind, occurred_at, delta_text) VALUES ($2, 8, 'sessionUpdated', $3, '');
        `, worktreeID, sessionID, at)
		Expect(err).NotTo(HaveOccurred())
		applyMigrationRange(ctx, db, 4, len(migrationNames))

		var eventCount int
		Expect(db.QueryRowContext(ctx, "SELECT COUNT(*) FROM session_events WHERE session_id = $1", sessionID).Scan(&eventCount)).To(Succeed())
		Expect(eventCount).To(BeZero())
		var lastEventSeq int64
		Expect(db.QueryRowContext(ctx, "SELECT last_event_seq FROM session_counters WHERE session_id = $1", sessionID).Scan(&lastEventSeq)).To(Succeed())
		Expect(lastEventSeq).To(Equal(int64(8)))

		queries := storedb.New(db)
		seq, err := queries.AllocateSessionEventSeq(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(seq).To(Equal(int64(9)))
		_, err = queries.InsertSessionEvent(ctx, storedb.InsertSessionEventParams{
			SessionID: sessionID, Seq: seq, Kind: string(api.SessionEventKindSessionUpdated), OccurredAt: at,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.SnapshotSessionEvent(ctx, storedb.SnapshotSessionEventParams{SessionID: sessionID, EventSeq: seq})
		Expect(err).NotTo(HaveOccurred())
		version, err := queries.GetSessionEventVersion(ctx, storedb.GetSessionEventVersionParams{SessionID: sessionID, EventSeq: seq})
		Expect(err).NotTo(HaveOccurred())
		Expect(version.Status).To(Equal("idle"))
	})
})

func applyMigrationRange(ctx context.Context, db *sql.DB, start int, end int) {
	files := fstest.MapFS{}
	for _, name := range migrationNames[start:end] {
		body, err := migrationFiles.ReadFile("migrations/" + name)
		Expect(err).NotTo(HaveOccurred())
		files["migrations/"+name] = &fstest.MapFile{Data: body}
	}
	Expect(sqlmigrate.Apply(ctx, db, files, "migrations")).To(Succeed())
}

func insertLegacySession(ctx context.Context, db *sql.DB, identifier uuid.UUID, directory string, at time.Time) {
	_, err := db.ExecContext(ctx, `
        INSERT INTO sessions (
            id, display_name, model, working_directory, system_instructions, status, created_at, updated_at
        ) VALUES ($1, 'chat', 'gpt-5', $2, '', 'idle', $3, $3)
    `, identifier, directory, at)
	Expect(err).NotTo(HaveOccurred())
}
