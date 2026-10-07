// Copyright 2026 Candace Labs

package prod_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/csf/prod"
)

var _ = Describe("checker-output parsers", func() {
	It("counts csfc diagnostics and ignores summaries", func() {
		// tools/house_lint/alignment_test.ml's vectors, ported verbatim.
		output := "csf/architecture/architecture.csf:12:3: lifecycle: Services require a scoped lifecycle.\n" +
			"go/x/manager.go:122:13: CSF_PROCESS_BOUNDARY: os/exec.Command requires a declared gateway source\n" +
			"architecture=csf mode=check declarations=checked source=checked obligations=3\n" +
			""
		Expect(prod.Findings(output)).To(Equal(2))
		Expect(prod.Findings("")).To(Equal(0))
		Expect(prod.Findings("file.go:12: CODE: text")).To(Equal(0))
	})

	It("counts only generated-drift diagnostics", func() {
		output := "csf/architecture/generated/review_cgen.md:1:1: CSF_GENERATED_DRIFT: run csfc emit\n" +
			"csf/architecture/generated/ocaml_cgen.ml:1:1: CSF_GENERATED_DRIFT: run csfc emit\n" +
			"go/x/manager.go:122:13: CSF_PROCESS_BOUNDARY: not drift\n"
		Expect(prod.Drift(output)).To(Equal(2))
	})

	It("counts documentation drift and refuses a message that is not drift", func() {
		count, present := prod.DocumentationDrift("CSF generator: generated documentation differs: a.md, b/c.md\n")
		Expect(present).To(BeTrue())
		Expect(count).To(Equal(2))

		_, present = prod.DocumentationDrift("CSF generator: vocabulary findings:\nREADME.md:3: retired word 'x': y")
		Expect(present).To(BeFalse())
	})

	It("separates retired vocabulary from unlinked terms", func() {
		lint := "README.md:3: retired word 'CandaceOS': name each part by its function\n" +
			"README.md:7: unlinked ontology term 'service' (term service)\n" +
			"pkg/README.md:2: unlinked ontology term 'widgets' (term widget)\n" +
			"pkg/README.md:9: unlinked literature term 'RRSI' (literature rrsi)\n"
		retired, unlinked := prod.VocabularyCounts(lint)
		Expect(retired).To(Equal(1))
		Expect(unlinked).To(Equal(2))
	})

	It("detects a generator that predates the lint verb", func() {
		Expect(prod.LintUnsupported("CSF generator: usage: generate --root ROOT write|check|metrics [--manifest PATH]\n")).To(BeTrue())
		Expect(prod.LintUnsupported("CSF generator: usage: generate --root ROOT write|check|metrics [--manifest PATH] | lint FILE...\n")).To(BeFalse())
	})
})
