// Copyright 2026 Candace Labs

package sqlmigrate_test

import (
	"context"
	"database/sql"
	"errors"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/pgmem"
	"github.com/candacelabs/csf/pkg/sqlmigrate"
)

const (
	componentVersionTable   = "component_schema_version"
	applicationVersionTable = "application_schema_version"
	componentDirectory      = "component"
	applicationDirectory    = "application"
	countReceipts           = "SELECT COUNT(*) FROM "
)

func receipts(ctx context.Context, db *sql.DB, table string) int {
	var count int
	Expect(db.QueryRowContext(ctx, countReceipts+table).Scan(&count)).To(Succeed())
	return count
}

var _ = Describe("ApplyVersioned", func() {
	var (
		ctx   context.Context
		db    *sql.DB
		files = os.DirFS("testdata")
	)

	BeforeEach(func() {
		ctx = context.Background()
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db = database.Open()
		DeferCleanup(db.Close)
	})

	It("applies every .sql file once, in name order, skipping .down.sql", func() {
		Expect(sqlmigrate.ApplyVersioned(ctx, db, files, componentDirectory, componentVersionTable)).To(Succeed())
		Expect(sqlmigrate.ApplyVersioned(ctx, db, files, componentDirectory, componentVersionTable)).To(Succeed())

		Expect(receipts(ctx, db, componentVersionTable)).To(Equal(2))
		_, err := db.ExecContext(ctx, "INSERT INTO component_items (id, label) VALUES (1, 'first')")
		Expect(err).NotTo(HaveOccurred(), "002 added the column and the .down.sql file never ran")
	})

	It("keeps two migration sets with the same numbering apart by their version tables", func() {
		Expect(sqlmigrate.ApplyVersioned(ctx, db, files, componentDirectory, componentVersionTable)).To(Succeed())
		Expect(sqlmigrate.ApplyVersioned(ctx, db, files, applicationDirectory, applicationVersionTable)).To(Succeed())

		Expect(receipts(ctx, db, componentVersionTable)).To(Equal(2))
		Expect(receipts(ctx, db, applicationVersionTable)).To(Equal(1))
		_, err := db.ExecContext(ctx, "INSERT INTO application_items (id) VALUES (1)")
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses a version table name that is not a plain identifier", func() {
		for _, name := range []string{"", "Upper", "drop table x", "quoted\"name", "9starts_with_digit"} {
			err := sqlmigrate.ApplyVersioned(ctx, db, files, componentDirectory, name)
			Expect(errors.Is(err, sqlmigrate.ErrInvalidVersionTable)).To(BeTrue(), name)
		}
	})

	It("refuses a nil handle", func() {
		Expect(sqlmigrate.ApplyVersioned(ctx, nil, files, componentDirectory, componentVersionTable)).To(MatchError(ContainSubstring("database handle")))
	})
})
