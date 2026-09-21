package pgmem_test

import (
	"context"
	"database/sql"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/pgmem"
)

var _ = Describe("database/sql NULL scanning", func() {
	It("scans a NULL timestamptz and a NULL uuid through database/sql", func() {
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		pool := database.Open()
		DeferCleanup(pool.Close)
		ctx := context.Background()
		_, err := pool.ExecContext(ctx, `CREATE TABLE events (id INTEGER PRIMARY KEY, seen_at TIMESTAMPTZ, owner UUID)`)
		Expect(err).NotTo(HaveOccurred())
		_, err = pool.ExecContext(ctx, `INSERT INTO events (id, seen_at, owner) VALUES (1, NULL, NULL)`)
		Expect(err).NotTo(HaveOccurred())
		var seenAt sql.NullTime
		var owner sql.NullString
		Expect(pool.QueryRowContext(ctx, `SELECT seen_at, owner FROM events WHERE id = 1`).Scan(&seenAt, &owner)).To(Succeed())
		Expect(seenAt.Valid).To(BeFalse())
		Expect(owner.Valid).To(BeFalse())
	})
})

var _ = Describe("typed parameters", func() {
	It("accepts a cast on a parameter the way sqlc emits nullable arguments", func() {
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		pool := database.Open()
		DeferCleanup(pool.Close)
		ctx := context.Background()
		_, err := pool.ExecContext(ctx, `CREATE TABLE sessions (id INTEGER PRIMARY KEY, status TEXT NOT NULL)`)
		Expect(err).NotTo(HaveOccurred())
		_, err = pool.ExecContext(ctx, `INSERT INTO sessions (id, status) VALUES (1, 'idle'), (2, 'ended')`)
		Expect(err).NotTo(HaveOccurred())
		var count int
		Expect(pool.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sessions WHERE ($1::text IS NULL OR status = $1::text)`, sql.NullString{}).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(2))
		Expect(pool.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sessions WHERE ($1::text IS NULL OR status = $1::text)`,
			sql.NullString{String: "idle", Valid: true}).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
	})
})
