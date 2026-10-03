// Copyright 2026 Candace Labs

package pgmem

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the pgx surface internals", func() {
	It("renders INSERT tags with the OID column and other commands without it", func() {
		Expect(commandTag(`insert into notes (id) values (1)`, 3).String()).To(Equal("INSERT 0 3"))
		Expect(commandTag(`UPDATE notes SET id = 2`, 4).String()).To(Equal("UPDATE 4"))
		Expect(commandTag(`SELECT 1`, 0).String()).To(Equal("SELECT 0"))
	})

	It("answers every batch call with ErrPGXUnsupported", func() {
		batch := unsupportedBatch{}
		_, err := batch.Exec()
		Expect(err).To(MatchError(ErrPGXUnsupported))
		_, err = batch.Query()
		Expect(err).To(MatchError(ErrPGXUnsupported))
		Expect(batch.QueryRow().Scan()).To(MatchError(ErrPGXUnsupported))
		Expect(batch.Close()).To(MatchError(ErrPGXUnsupported))
	})

	It("closes rows once and stops iterating after Close", func() {
		engine := MustNew()
		DeferCleanup(engine.Close)
		database := engine.Public().OpenPGX()
		DeferCleanup(database.Close)
		rows, err := database.Query(context.Background(), `SELECT 1 AS one`)
		Expect(err).NotTo(HaveOccurred())
		concrete, ok := rows.(*pgxRows)
		Expect(ok).To(BeTrue())
		concrete.Close()
		concrete.Close()
		Expect(concrete.closed).To(BeTrue())
		Expect(concrete.Next()).To(BeFalse())
		Expect(concrete.Conn()).To(BeNil())
		Expect(concrete.RawValues()).To(BeNil())
	})

	It("passes a query error through QueryRow's Scan and refuses a finished transaction", func() {
		failure := errors.New("query failed")
		Expect((&pgxRow{err: failure}).Scan()).To(MatchError(failure))
		Expect((&pgxTx{finished: true}).Commit(context.Background())).To(MatchError(pgx.ErrTxClosed))
	})
})
