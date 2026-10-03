// Copyright 2026 Candace Labs

package pgmem_test

import (
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/pgmem"
)

// One spec per PostgreSQL construct the CSF schema needed and the engine
// lacked. Each asserts PostgreSQL's observable behavior, not the rewrite.
var _ = Describe("PostgreSQL constructs the engine lacks", func() {
	var schema *pgmem.Schema

	BeforeEach(func() {
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		schema = database.Public()
	})

	It("evaluates now() and statement_timestamp() column defaults", func() {
		Expect(schema.None(`CREATE TABLE stamps (
			id INTEGER PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp()
		)`)).To(Succeed())
		Expect(schema.None(`INSERT INTO stamps (id) VALUES (1)`)).To(Succeed())

		row, err := schema.One(`SELECT created_at IS NOT NULL AND updated_at IS NOT NULL AS stamped, now() IS NOT NULL AS callable FROM stamps`)
		Expect(err).NotTo(HaveOccurred())
		Expect(row["stamped"]).To(BeNumerically("==", 1))
		Expect(row["callable"]).To(BeNumerically("==", 1))
	})

	It("matches the POSIX regular-expression operators, in CHECK constraints too", func() {
		Expect(schema.None(`CREATE TABLE hashes (value TEXT PRIMARY KEY CHECK (value ~ '^[0-9a-f]{4}$'))`)).To(Succeed())
		Expect(schema.None(`INSERT INTO hashes (value) VALUES ('00af')`)).To(Succeed())
		err := schema.None(`INSERT INTO hashes (value) VALUES ('00AF')`)
		Expect(err).To(MatchError(ContainSubstring("CHECK")))

		row, err := schema.One(`SELECT 'Abc' ~ '^a' AS sensitive, 'Abc' ~* '^a' AS insensitive,
			'Abc' !~ '^a' AS negated, 'Abc' !~* '^a' AS negated_insensitive, NULL ~ 'a' AS unknown`)
		Expect(err).NotTo(HaveOccurred())
		Expect(row["sensitive"]).To(BeNumerically("==", 0))
		Expect(row["insensitive"]).To(BeNumerically("==", 1))
		Expect(row["negated"]).To(BeNumerically("==", 1))
		Expect(row["negated_insensitive"]).To(BeNumerically("==", 0))
		Expect(row["unknown"]).To(BeNil())
	})

	It("casts with PostgreSQL's value semantics", func() {
		row, err := schema.One(`SELECT 'Infinity'::DOUBLE PRECISION AS positive, '-Infinity'::float8 AS negative,
			'2.5'::numeric AS fraction, 3.5::INTEGER AS rounded, '42'::BIGINT AS parsed,
			'yes'::BOOLEAN AS truthy, 'off'::bool AS falsy, 'kept'::TEXT AS text_value`)
		Expect(err).NotTo(HaveOccurred())
		Expect(row["positive"]).To(Equal(math.Inf(1)))
		Expect(row["negative"]).To(Equal(math.Inf(-1)))
		Expect(row["fraction"]).To(Equal(2.5))
		Expect(row["rounded"]).To(BeNumerically("==", 4), "PostgreSQL rounds half away from zero")
		Expect(row["parsed"]).To(BeNumerically("==", 42))
		Expect(row["truthy"]).To(BeNumerically("==", 1))
		Expect(row["falsy"]).To(BeNumerically("==", 0))
		Expect(row["text_value"]).To(Equal("kept"))

		_, err = schema.One(`SELECT 'many'::INTEGER AS invalid`)
		Expect(err).To(MatchError(ContainSubstring("invalid input syntax for type integer")))
	})

	It("rejects an infinite value through a CHECK against 'Infinity'::DOUBLE PRECISION", func() {
		Expect(schema.None(`CREATE TABLE measurements (value DOUBLE PRECISION NOT NULL
			CHECK (value > '-Infinity'::DOUBLE PRECISION AND value < 'Infinity'::DOUBLE PRECISION))`)).To(Succeed())
		Expect(schema.None(`INSERT INTO measurements (value) VALUES (-1.5)`)).To(Succeed())
		Expect(schema.None(`INSERT INTO measurements (value) VALUES (1e999)`)).To(MatchError(ContainSubstring("CHECK")))
	})

	It("accepts enum type DDL and stores the labels of an enum column", func() {
		Expect(schema.None(`CREATE TYPE task_status AS ENUM ('pending', 'running')`)).To(Succeed())
		Expect(schema.None(`CREATE TABLE tasks (id INTEGER PRIMARY KEY, status task_status NOT NULL DEFAULT 'pending')`)).To(Succeed())
		Expect(schema.None(`INSERT INTO tasks (id) VALUES (1)`)).To(Succeed())
		Expect(schema.None(`UPDATE tasks SET status = 'running'::task_status WHERE id = 1`)).To(Succeed())

		row, err := schema.One(`SELECT status FROM tasks WHERE id = 1`)
		Expect(err).NotTo(HaveOccurred())
		Expect(row["status"]).To(Equal("running"))
		Expect(schema.None(`DROP TYPE task_status`)).To(Succeed())
	})

	It("implements position(), right() and repeat()", func() {
		row, err := schema.One(`SELECT position('//' IN 'a//b') AS found, position('x' IN 'abc') AS missing,
			right('documents/', 1) AS last, right('abcdef', -2) AS without_first, repeat('ab', 3) AS repeated`)
		Expect(err).NotTo(HaveOccurred())
		Expect(row["found"]).To(BeNumerically("==", 2))
		Expect(row["missing"]).To(BeNumerically("==", 0))
		Expect(row["last"]).To(Equal("/"))
		Expect(row["without_first"]).To(Equal("cdef"))
		Expect(row["repeated"]).To(Equal("ababab"))
	})
})
