// Copyright 2026 Candace Labs

package pgmem_test

import (
	"context"
	"database/sql"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/pgmem"
)

var (
	errRefusedSchema = errors.New("schema refused")
	errRefusedStore  = errors.New("store refused")
)

func createNotes(ctx context.Context, database *sql.DB) error {
	_, err := database.ExecContext(ctx, `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)`)
	return err
}

var _ = Describe("a store opened on pgmem for a spec", func() {
	It("builds the store on the schema, over the one capability the wrapper releases on Close", func(ctx SpecContext) {
		var granted *pgmem.PGX
		wrapper, err := pgmem.OpenPostgresStoreOnPgmem(ctx, createNotes,
			func(capability *pgmem.PGX) (string, error) {
				granted = capability
				_, err := capability.Exec(ctx, `INSERT INTO notes (id, body) VALUES ($1, $2)`, 1, "one")
				return "store", err
			})
		Expect(err).NotTo(HaveOccurred())
		Expect(wrapper.Store).To(Equal("store"))
		Expect(wrapper.Database()).To(BeIdenticalTo(granted),
			"the store and the spec's verification queries share one capability, so one Close releases it")

		var body string
		Expect(wrapper.Database().QueryRow(ctx, `SELECT body FROM notes WHERE id = $1`, 1).Scan(&body)).To(Succeed())
		Expect(body).To(Equal("one"))

		Expect(wrapper.Close()).To(Succeed())
		_, err = granted.Exec(ctx, `INSERT INTO notes (id, body) VALUES ($1, $2)`, 2, "two")
		Expect(err).To(MatchError(pgmem.ErrPGXClosed), "Close releases the capability the store was built on")
	})

	It("releases the capability when the store cannot be built, and reports the failure", func(ctx SpecContext) {
		var granted *pgmem.PGX
		wrapper, err := pgmem.OpenPostgresStoreOnPgmem(ctx, createNotes,
			func(capability *pgmem.PGX) (string, error) {
				granted = capability
				return "", errRefusedStore
			})
		Expect(wrapper).To(BeNil())
		Expect(err).To(MatchError(errRefusedStore))
		_, err = granted.Exec(ctx, `INSERT INTO notes (id, body) VALUES ($1, $2)`, 1, "one")
		Expect(err).To(MatchError(pgmem.ErrPGXClosed))
	})

	It("builds no store when the schema cannot be applied", func(ctx SpecContext) {
		built := false
		wrapper, err := pgmem.OpenPostgresStoreOnPgmem(ctx,
			func(_ context.Context, _ *sql.DB) error { return errRefusedSchema },
			func(_ *pgmem.PGX) (string, error) {
				built = true
				return "store", nil
			})
		Expect(wrapper).To(BeNil())
		Expect(err).To(MatchError(errRefusedSchema))
		Expect(built).To(BeFalse())
	})
})
