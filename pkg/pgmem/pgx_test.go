// Copyright 2026 Candace Labs

package pgmem_test

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/pgmem"
)

var _ = Describe("the pgx surface", func() {
	var (
		ctx      context.Context
		database *pgmem.PGX
	)

	BeforeEach(func() {
		ctx = context.Background()
		engine := pgmem.MustNew()
		DeferCleanup(engine.Close)
		Expect(engine.Public().None(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)`)).To(Succeed())
		database = engine.Public().OpenPGX()
		DeferCleanup(database.Close)
	})

	It("reports PostgreSQL command tags", func() {
		tag, err := database.Exec(ctx, `INSERT INTO notes (id, body) VALUES ($1, $2), ($3, $4)`, 1, "one", 2, "two")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag.String()).To(Equal("INSERT 0 2"))
		tag, err = database.Exec(ctx, `DELETE FROM notes WHERE id = $1`, 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(tag.String()).To(Equal("DELETE 1"))
	})

	It("iterates rows with values and column names, and reports no row from QueryRow", func() {
		_, err := database.Exec(ctx, `INSERT INTO notes (id, body) VALUES ($1, $2)`, 1, "one")
		Expect(err).NotTo(HaveOccurred())
		rows, err := database.Query(ctx, `SELECT id, body FROM notes`)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows.FieldDescriptions()).To(HaveLen(2))
		Expect(rows.FieldDescriptions()[1].Name).To(Equal("body"))
		Expect(rows.Next()).To(BeTrue())
		values, err := rows.Values()
		Expect(err).NotTo(HaveOccurred())
		Expect(values).To(Equal([]any{int64(1), "one"}))
		Expect(rows.Next()).To(BeFalse())
		Expect(rows.Err()).NotTo(HaveOccurred())
		Expect(rows.CommandTag().String()).To(Equal("SELECT 1"))

		var body string
		err = database.QueryRow(ctx, `SELECT body FROM notes WHERE id = $1`, 9).Scan(&body)
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
	})

	It("refuses the pgx features it does not emulate", func() {
		transaction, err := database.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = transaction.Rollback(context.Background()) })
		_, err = transaction.CopyFrom(ctx, pgx.Identifier{"notes"}, []string{"id"}, pgx.CopyFromRows(nil))
		Expect(err).To(MatchError(pgmem.ErrPGXUnsupported))
		_, err = transaction.Prepare(ctx, "named", `SELECT 1`)
		Expect(err).To(MatchError(pgmem.ErrPGXUnsupported))
		Expect(transaction.SendBatch(ctx, &pgx.Batch{}).Close()).To(MatchError(pgmem.ErrPGXUnsupported))
		Expect(transaction.Commit(ctx)).To(Succeed())
		Expect(transaction.Commit(ctx)).To(MatchError(pgx.ErrTxClosed))
	})

	It("rejects a finished transaction and a closed surface with defined errors", func() {
		transaction, err := database.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(transaction.Commit(ctx)).To(Succeed())
		Expect(transaction.Rollback(ctx)).To(MatchError(pgx.ErrTxClosed))
		_, err = transaction.Begin(ctx)
		Expect(err).To(MatchError(pgx.ErrTxClosed))

		Expect(database.Close()).To(Succeed())
		Expect(database.Close()).To(Succeed(), "a second Close does nothing")
		_, err = database.Exec(ctx, `DELETE FROM notes`)
		Expect(err).To(MatchError(pgmem.ErrPGXClosed))
		_, err = database.Query(ctx, `SELECT id FROM notes`)
		Expect(err).To(MatchError(pgmem.ErrPGXClosed))
		var id int
		Expect(database.QueryRow(ctx, `SELECT id FROM notes`).Scan(&id)).To(MatchError(pgmem.ErrPGXClosed))
		_, err = database.Begin(ctx)
		Expect(err).To(MatchError(pgmem.ErrPGXClosed))
		Expect(database.Ping(ctx)).To(MatchError(pgmem.ErrPGXClosed))
	})
})
