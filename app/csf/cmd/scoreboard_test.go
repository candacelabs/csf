package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// scoreboardSource is the scoreboard's one declaration: the CSF artifact the
// verb, the csfpg rows and the viewer panel all mirror. Its kinds are declared
// there — never as Grafana JSON and never as an external page.
const scoreboardSource = "../../../csf/observability/scoreboard.csf"

var _ = Describe("Scoreboard", func() {
	It("prints every program meter as one table row", func() {
		var output bytes.Buffer
		Expect(scoreboard(nil, &output)).To(Succeed())
		lines := strings.Split(strings.TrimRight(output.String(), "\n"), "\n")
		Expect(lines).To(HaveLen(20)) // one header row, the consistency headline, then the 18 meters.
		Expect(lines[0]).To(ContainSubstring("METER"))
		rows, err := meters()
		Expect(err).NotTo(HaveOccurred())
		for _, row := range rows {
			// Every meter's measures string is unique, so it names exactly one row.
			Expect(strings.Count(output.String(), row.measures)).To(Equal(1), row.name)
		}
	})

	It("heads the table with consistency, the mean satisfied fraction across the invariants", func() {
		rows, err := meters()
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).NotTo(BeEmpty())
		headline := rows[0]
		Expect(headline.name).To(Equal("consistency"))
		// Consistency is the same distance the ruling expressed, as a percentage
		// that rises toward 100: main is not there yet, so it never gates.
		Expect(headline.target).To(Equal("percent(100)"))
		Expect(headline.now).To(Equal("percent(59.0)"))
		Expect(headline.source).To(Equal("consistency_score"))
		// The ported query reproduces the measurement of csf_staging main at
		// 369d12d: the ruling pins consistency(0.590) n(14), mean(s(I)) over the
		// fourteen invariants.
		report, err := decodeConsistency()
		Expect(err).NotTo(HaveOccurred())
		Expect(report.MeasuredOn).To(Equal("2026-10-06"))
		Expect(report.Invariants).To(HaveLen(14))
		Expect(report.Commit).To(Equal("369d12d"))
		for _, invariant := range report.Invariants {
			Expect(invariant.RaisedBy).NotTo(BeEmpty(), invariant.Name)
			Expect(headline.movedBy).To(ContainElement(invariant.RaisedBy), invariant.Name)
		}
	})

	It("computes the demo meters from the committed evidence, reproducibly", func() {
		rows, err := meters()
		Expect(err).NotTo(HaveOccurred())
		byName := make(map[string]meter, len(rows))
		for _, row := range rows {
			byName[row.name] = row
		}
		Expect(byName["placement"].now).To(Equal("percent(61.9) top3(80.7)"))
		Expect(byName["decision_latency"].now).To(Equal("seconds(0.50)"))
		Expect(byName["csf_decides"].now).To(Equal("ratio(4, 5)"))
		Expect(byName["ambiguous"].now).To(Equal("ratio(5, 22) below(0.6, provisional_until_knee_fit)"))
	})

	It("names a source query for every meter", func() {
		rows, err := meters()
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(19))
		for _, row := range rows {
			Expect(row.source).NotTo(BeEmpty(), row.name)
			Expect(row.source).NotTo(ContainSubstring(" "), row.name)
		}
	})

	It("declares the scoreboard as a CSF artifact, shown by the CSF viewer", func() {
		declaration, err := os.ReadFile(scoreboardSource)
		Expect(err).NotTo(HaveOccurred())
		source := string(declaration)
		// The two kinds #517 declares: meter and invariant. Nothing here is
		// Grafana JSON, and nothing here is an external page.
		Expect(source).To(ContainSubstring("kind meter"))
		Expect(source).To(ContainSubstring("kind invariant"))
		Expect(strings.ToLower(source)).NotTo(ContainSubstring("grafana"))
		// The artifact: rows in csfpg, migrations only and sqlc only, written by
		// the verb.
		Expect(source).To(ContainSubstring("artifact scoreboard"))
		Expect(source).To(ContainSubstring("meter_measurement"))
		Expect(source).To(ContainSubstring("meter_invariant"))
		Expect(source).To(ContainSubstring("csfpg"))
		Expect(source).To(ContainSubstring("csf scoreboard"))
		// The viewer: the consistency panel in services/opsview, mounted by csf
		// serve at "/", like the cloud panel and phone first, and showing the
		// headline, the invariant rows, the chief meters and the slices.
		Expect(source).To(ContainSubstring(`panel "consistency"`))
		Expect(source).To(ContainSubstring(`in "services/opsview"`))
		Expect(source).To(ContainSubstring(`mounted_by "csf serve"`))
		Expect(source).To(ContainSubstring(`path "/"`))
		Expect(source).To(ContainSubstring("cloud_panel"))
		Expect(source).To(ContainSubstring("phone_first"))
		for _, shown := range []string{"c_headline", "invariant_rows", "chief_meters", "slices"} {
			Expect(source).To(ContainSubstring(shown), shown)
		}
		// Every meter the verb prints and every invariant row the headline
		// averages is declared here, so the verb, the rows and the panel cannot
		// drift from the one artifact.
		rows, err := meters()
		Expect(err).NotTo(HaveOccurred())
		for _, row := range rows {
			Expect(source).To(ContainSubstring("meter "+row.name), row.name)
		}
		report, err := decodeConsistency()
		Expect(err).NotTo(HaveOccurred())
		for _, invariant := range report.Invariants {
			Expect(source).To(ContainSubstring(invariant.Name), invariant.Name)
			Expect(invariant.Mode).To(BeElementOf("ratchet", "hard_gate", "advisory_until_zero"), invariant.Name)
			// The instance declares the same counts, mode and owner slice the
			// committed measurement holds, so the artifact is the one record.
			declared := fmt.Sprintf(`(?m)^invariant\s+%s\s+\{ satisfied %d; checked %d; mode %s; owner_slice %s; \}$`,
				regexp.QuoteMeta(invariant.Name), invariant.Satisfied, invariant.Checked, invariant.Mode, regexp.QuoteMeta(invariant.RaisedBy))
			Expect(source).To(MatchRegexp(declared), invariant.Name)
		}
	})
})
