// Copyright 2026 Candace Labs

package prod_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/csf/prod"
)

var _ = Describe("the derived share, measured by reproduction", func() {
	It("classifies a file by the program that reproduces it, not by its header text", func() {
		files := []string{
			"csf/observability/scoreboard.csf",             // source: .csf
			"csf/compiler/rules.dl",                        // source: .dl
			"pkg/gotth/docs/bench/data/g2/step01/r01.json", // derived, and it carries no generated header
			"services/opsview/view.html",                   // authored
			"docs/paper/notes.md",                          // authored, even were it to quote "Code generated"
		}
		classified := prod.Classify(files)
		Expect(classified.Source).To(ConsistOf("csf/observability/scoreboard.csf", "csf/compiler/rules.dl"))
		Expect(classified.Derived).To(ConsistOf("pkg/gotth/docs/bench/data/g2/step01/r01.json"))
		Expect(classified.Authored).To(ConsistOf("services/opsview/view.html", "docs/paper/notes.md"))
	})

	It("counts the derived share as source plus derived over every file", func() {
		classified := prod.Classify([]string{"csf/a.csf", "pkg/BUILD.bazel", "services/hand.go"})
		Expect(classified.DerivedShare()).To(Equal(2.0 / 3.0))
		// No files is wholly derived, vacuously, so a reading with nothing to
		// measure never falls and never refuses a merge.
		Expect(prod.Classify(nil).DerivedShare()).To(Equal(1.0))
	})

	It("reproduces the generated tree by its declared generator, not its text", func() {
		// Every path below is an output of the named generator, and the
		// generated-drift gate holds each byte-equal to its source. None needs a
		// header for the measurer to see it, and a matching authored exception
		// would move it out of the derived set.
		byGenerator := map[string]string{
			"gazelle":             "pkg/widget/BUILD.bazel",
			"protoc":              "proto/candace/brainspine/v1/api.pb.go",
			"sqlc":                "io/ipc/db/csfpg/jobs.sql.go",
			"mockgen":             "csf/internal/mocks/store.gen.go",
			"templ":               "pkg/widget/page_templ.go",
			"csfc":                "csf/architecture/generated/csf_architecture_cgen.ml",
			"language_generator":  "csf/docs/generated/ontology_cgen.md",
			"api_codegen":         "csf/simulation_views_gen.go",
		}
		for generator, file := range byGenerator {
			classified := prod.Classify([]string{file})
			Expect(classified.Derived).To(ConsistOf(file), generator)
			Expect(classified.Authored).To(BeEmpty(), generator)
		}
	})

	It("matches ** across any number of segments and * within one", func() {
		// The gazelle declaration reproduces a BUILD.bazel at the root and at
		// any depth (reproduces_or_listed_as_authored).
		Expect(prod.Classify([]string{"BUILD.bazel"}).Derived).To(ConsistOf("BUILD.bazel"))
		Expect(prod.Classify([]string{"a/b/BUILD.bazel"}).Derived).To(ConsistOf("a/b/BUILD.bazel"))
		Expect(prod.Classify([]string{"a/BUILD.md"}).Authored).To(ConsistOf("a/BUILD.md"))
		// The bench driver's output is everything under its data directory, and
		// only that: a file one segment up is not reproduced.
		Expect(prod.Classify([]string{"pkg/gotth/docs/bench/data/g2/r01.json"}).Derived).
			To(ConsistOf("pkg/gotth/docs/bench/data/g2/r01.json"))
		Expect(prod.Classify([]string{"pkg/gotth/docs/bench/report.md"}).Authored).
			To(ConsistOf("pkg/gotth/docs/bench/report.md"))
	})

	It("declares every machine-written family, so none leaks into the authored set", func() {
		// One representative file per machine-written family — BUILD files, the
		// protobuf, sqlc and mockgen projections, the templ pages, the generated
		// documentation, the language projections and the bench data — beside two
		// hand-written ones. The measurer lists no machine-written family among
		// its top authored families, which is the evidence that each is declared.
		classified := prod.Classify([]string{
			"BUILD.bazel",
			"pkg/widget/BUILD.bazel",
			"proto/candace/brainspine/v1/api.pb.go",
			"io/ipc/db/csfpg/jobs.sql.go",
			"csf/internal/mocks/store.gen.go",
			"pkg/widget/page_templ.go",
			"csf/architecture/generated/csf_architecture_cgen.ml",
			"csf/docs/generated/ontology_cgen.md",
			"services/ouroboros/codes/catalogue_cgen.go",
			"pkg/gotth/docs/bench/data/g2/step01/r01.json",
			"services/opsview/view.html",
			"services/opsview/ops_view_golden.go",
		})
		Expect(classified.Authored).To(ConsistOf(
			"services/opsview/view.html", "services/opsview/ops_view_golden.go"))
		Expect(classified.TopFamilies(3)).To(Equal([]prod.Family{
			{Directory: "services/opsview", Files: 2},
		}))
	})

	It("reports the authored set by family, most files first", func() {
		classified := prod.Classify([]string{
			"services/deploy/a.go", "services/deploy/b.go", "services/deploy/c.go",
			"pkg/core/d.go", "pkg/core/e.go",
			"docs/f.md",
		})
		Expect(classified.AuthoredFamilies()).To(Equal([]prod.Family{
			{Directory: "services/deploy", Files: 3},
			{Directory: "pkg/core", Files: 2},
			{Directory: "docs", Files: 1},
		}))
		Expect(classified.TopFamilies(2)).To(Equal([]prod.Family{
			{Directory: "services/deploy", Files: 3},
			{Directory: "pkg/core", Files: 2},
		}))
	})
})
