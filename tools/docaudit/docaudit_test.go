package main

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDocaudit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "docaudit suite")
}

var _ = Describe("categorizeBySet", func() {
	DescribeTable("categorizes files by set",
		func(path, expectedSet string) {
			Expect(categorizeBySet(path)).To(Equal(expectedSet))
		},
		Entry("root README", "README.md", "reference"),
		Entry("generated ontology", "csf/docs/generated/ontology_cgen.md", "reference"),
		Entry("pkg/gotth docs", "pkg/gotth/docs/README.md", "pkg/gotth/docs"),
		Entry("csf file", "csf/README.md", "csf"),
		Entry("pkg file", "pkg/core/README.md", "pkg"),
		Entry("services file", "services/deploy/README.md", "services"),
		Entry("examples file", "examples/csf-consumer/README.md", "examples"),
		Entry("infra file", "infra/deploy-kit/README.md", "infra"),
		Entry("docs file", "docs/audit/README.md", "docs"),
		Entry("web file", "web/deploy/README.md", "web"),
		Entry("extensions file", "extensions/copilot-pair/README.md", "extensions"),
		Entry("other file", "LICENSE", "other"),
	)
})

var _ = Describe("analyzeMarkdown", func() {
	It("extracts basic metrics from markdown", func() {
		content := `# My Title

This is a summary section.

## Status

Active

| Column1 | Column2 |
|---------|---------|
| Value1  | Value2  |

` + "```go\nfunc main() {}\n```" + `

![Image](path/to/image.png)

[Link to docs](./docs/README.md)
`
		tempFile, err := os.CreateTemp("", "test_*.md")
		Expect(err).NotTo(HaveOccurred())
		defer os.Remove(tempFile.Name())

		_, err = tempFile.WriteString(content)
		Expect(err).NotTo(HaveOccurred())
		tempFile.Close()

		metrics, err := analyzeMarkdown(tempFile.Name())
		Expect(err).NotTo(HaveOccurred())

		Expect(metrics.HasTitle).To(BeTrue())
		Expect(metrics.HasSummary).To(BeTrue())
		Expect(metrics.HasStatus).To(BeTrue())
		Expect(metrics.TableCount).To(BeNumerically(">", 0))
		Expect(metrics.CodeFences).To(HaveKey("go"))
		Expect(metrics.ImageCount).To(Equal(1))
		Expect(metrics.RelativeLinks).To(Equal(1))
	})
})

var _ = Describe("getReferenceProfile", func() {
	It("calculates reference profile from metrics", func() {
		metrics := []*DocMetrics{
			{HasTitle: true, HasSummary: false, HasStatus: true, TableCount: 2},
			{HasTitle: false, HasSummary: false, HasStatus: true, TableCount: 0},
		}

		profile := getReferenceProfile(metrics)

		Expect(profile["count"]).To(Equal(2))
		Expect(profile["has_title"].(float64)).To(BeNumerically("~", 0.5, 0.01))
		Expect(profile["has_summary"].(float64)).To(BeNumerically("~", 0.0, 0.01))
		Expect(profile["has_status"].(float64)).To(BeNumerically("~", 1.0, 0.01))
		Expect(profile["avg_tables"].(float64)).To(BeNumerically("~", 1.0, 0.01))
	})
})

var _ = Describe("calculateDeviation", func() {
	It("calculates deviation from reference profile", func() {
		refProfile := map[string]interface{}{
			"has_title":   0.5,
			"has_summary": 0.0,
			"has_status":  1.0,
			"avg_tables":  1.0,
		}

		// Half of the set has a title, no document has a summary, every document
		// has a status and the tables average one: exactly the reference profile.
		metrics := []*DocMetrics{
			{HasTitle: true, HasSummary: false, HasStatus: true, TableCount: 1},
			{HasTitle: false, HasSummary: false, HasStatus: true, TableCount: 1},
		}

		deviation := calculateDeviation(metrics, refProfile)
		Expect(deviation).To(BeNumerically("~", 0.0, 0.01))
	})

	It("reports the root mean square of the four feature gaps as a percentage", func() {
		refProfile := map[string]interface{}{
			"has_title":   0.5,
			"has_summary": 0.0,
			"has_status":  1.0,
			"avg_tables":  1.0,
		}

		// Every document has a title where the profile has half: one gap of 0.5,
		// so sqrt(0.5^2 / 4) = 0.25, which is 25%.
		metrics := []*DocMetrics{
			{HasTitle: true, HasSummary: false, HasStatus: true, TableCount: 1},
			{HasTitle: true, HasSummary: false, HasStatus: true, TableCount: 1},
		}

		deviation := calculateDeviation(metrics, refProfile)
		Expect(deviation).To(BeNumerically("~", 25.0, 0.01))
	})

	It("reports no deviation for an empty set", func() {
		refProfile := map[string]interface{}{"has_title": 0.5}

		Expect(calculateDeviation(nil, refProfile)).To(BeZero())
	})
})
