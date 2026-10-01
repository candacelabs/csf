package sqlmigrate_test

import (
	"context"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/pgmem"

	"github.com/candacelabs/csf/pkg/sqlmigrate"
)

var _ = Describe("Apply", func() {
	It("applies each file once, in name order, one statement at a time", func() {
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		files := fstest.MapFS{
			"migrations/000002_second.up.sql": {Data: []byte("-- second\nALTER TABLE things ADD COLUMN label TEXT;\n")},
			"migrations/000001_first.up.sql":  {Data: []byte("CREATE TABLE things (id INTEGER PRIMARY KEY);\n\nINSERT INTO things (id) VALUES (1);\n")},
			"migrations/README.md":            {Data: []byte("not a migration")},
		}
		ctx := context.Background()
		Expect(sqlmigrate.Apply(ctx, db, files, "migrations")).To(Succeed())
		Expect(sqlmigrate.Apply(ctx, db, files, "migrations")).To(Succeed(), "a second run is a no-op")
		var count int
		Expect(db.QueryRowContext(ctx, "SELECT COUNT(*) FROM things").Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
		var applied int
		Expect(db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&applied)).To(Succeed())
		Expect(applied).To(Equal(2))
	})

	It("refuses a nil handle", func() {
		Expect(sqlmigrate.Apply(context.Background(), nil, fstest.MapFS{}, "migrations")).To(MatchError(ContainSubstring("database handle")))
	})
})

var _ = Describe("ApplyPrefixed", func() {
	It("applies identical filenames once in independent component namespaces", func() {
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		files := fstest.MapFS{
			"adapter/000001_schema.up.sql": {Data: []byte("CREATE TABLE adapter_rows (id INTEGER PRIMARY KEY);")},
			"cron/000001_schema.up.sql":    {Data: []byte("CREATE TABLE cron_rows (id INTEGER PRIMARY KEY);")},
		}
		ctx := context.Background()
		Expect(sqlmigrate.ApplyPrefixed(ctx, db, files, "adapter", "adapter")).To(Succeed())
		Expect(sqlmigrate.ApplyPrefixed(ctx, db, files, "cron", "candace-cron")).To(Succeed())
		Expect(sqlmigrate.ApplyPrefixed(ctx, db, files, "adapter", "adapter")).To(Succeed())
		Expect(sqlmigrate.ApplyPrefixed(ctx, db, files, "cron", "candace-cron")).To(Succeed())

		var applied int
		Expect(db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&applied)).To(Succeed())
		Expect(applied).To(Equal(2))
		var adapterReceipts int
		Expect(db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE name = $1", "adapter:000001_schema.up.sql").Scan(&adapterReceipts)).To(Succeed())
		Expect(adapterReceipts).To(Equal(1))
		var cronReceipts int
		Expect(db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE name = $1", "candace-cron:000001_schema.up.sql").Scan(&cronReceipts)).To(Succeed())
		Expect(cronReceipts).To(Equal(1))
	})

	It("rolls back a failed component migration without recording a receipt", func() {
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		ctx := context.Background()
		failed := fstest.MapFS{
			"broken/000001_schema.up.sql": {Data: []byte("CREATE TABLE rollback_target (id INTEGER PRIMARY KEY); INSERT INTO missing_table (id) VALUES (1);")},
		}
		Expect(sqlmigrate.ApplyPrefixed(ctx, db, failed, "broken", "broken-component")).To(MatchError(ContainSubstring("apply 000001_schema.up.sql")))

		var receipts int
		Expect(db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE name = $1", "broken-component:000001_schema.up.sql").Scan(&receipts)).To(Succeed())
		Expect(receipts).To(Equal(0))

		recovered := fstest.MapFS{
			"broken/000001_schema.up.sql": {Data: []byte("CREATE TABLE rollback_target (id INTEGER PRIMARY KEY);")},
		}
		Expect(sqlmigrate.ApplyPrefixed(ctx, db, recovered, "broken", "broken-component")).To(Succeed())
	})
})

var _ = Describe("Statements", func() {
	It("drops comment-only and empty candidates", func() {
		Expect(sqlmigrate.Statements("-- only a comment;\n\nCREATE TABLE t (id INT);\n;\n")).To(Equal([]string{"CREATE TABLE t (id INT)"}))
	})

	It("removes full-line comment prose before splitting on semicolons", func() {
		body := "-- Durable state stays relational; no API wire payload is persisted here.\nCREATE TABLE t (id INT);\n"
		Expect(sqlmigrate.Statements(body)).To(Equal([]string{"CREATE TABLE t (id INT)"}))
	})
})
