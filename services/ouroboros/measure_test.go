// Copyright 2026 Candace Labs

package ouroboros_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/services/ouroboros"
)

const (
	measuredCall   = `{"time":"2026-10-03T19:03:10Z","event_type":"assistant","direction":"out","event":{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}}`
	measuredError  = `{"time":"2026-10-03T19:03:11Z","event_type":"user","direction":"out","event":{"type":"user","message":{"content":[{"type":"tool_result","content":"no","is_error":true}]}}}`
	measuredDay    = "2026-10-03"
	ratePerKFormat = `"per_1k":1000`
)

var _ = Describe("The measure", func() {
	It("records the struggle rate as series, publishes the snapshot and serves it", func() {
		ctx := context.Background()
		spec := newFixture()
		spec.corpus[runLog] = &fstest.MapFile{Data: []byte(strings.Join([]string{logLine, measuredCall, measuredError}, "\n"))}
		spec.corpus[otherLog] = &fstest.MapFile{Data: []byte(logLine + measuredCall + "\n")}
		delete(spec.repository, minerExecutable)
		spec.expectStructure()
		Expect(spec.ledger.RecordFindings(ctx, []ouroboros.Finding{{Miner: minerName, Rule: "invisible", Subject: runAssignment, Item: runLog, Severity: "SEVERITY_S3", Record: json.RawMessage(`{}`), FoundAt: specStart}})).Error().NotTo(HaveOccurred())

		snapshot, err := spec.loop.Measure(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Day).To(Equal("2026-10-04"))
		Expect(snapshot.Days).To(HaveLen(1))
		Expect(snapshot.Days[0].Day).To(Equal(measuredDay))
		Expect(snapshot.Days[0].ToolCalls).To(Equal(int64(2)))
		Expect(snapshot.Days[0].Struggles).To(Equal(int64(1)))
		Expect(*snapshot.Days[0].PerK).To(Equal(500.0))
		Expect(snapshot.Weeks).To(HaveLen(1))
		Expect(snapshot.Weeks[0].Day).To(Equal("2026-09-28"))
		Expect(snapshot.Week.ToolCalls).To(Equal(int64(2)), "today is in the measured week")
		Expect(snapshot.Compounding.Factor).To(BeNil(), "one week cannot compound")
		Expect(snapshot.CompoundingDaily.Factor).To(BeNil(), "one day cannot compound")
		Expect(snapshot.Baseline.Factor).To(Equal(1.156))
		Expect(snapshot.Structure).To(HaveLen(1))
		Expect(snapshot.Structure[0].Week).To(Equal("2026-09-28"))
		Expect(snapshot.Structure[0].Counts).To(Equal(ouroboros.StructureCounts{Gates: 3, Miners: 1, Terms: 2}))
		Expect(snapshot.Structure[0].Interventions).To(BeZero())
		Expect(snapshot.StructureExponent.Slope).To(BeNil())
		Expect(snapshot.Findings).To(Equal(1))
		Expect(snapshot.FindingsToday).To(Equal(1))
		Expect(snapshot.Miners).To(Equal([]ouroboros.MinerStatus{{Name: minerName, Built: false, Findings: 1}}))
		Expect(snapshot.Fixers.BudgetUSD).To(Equal(100.0))

		points, err := spec.ledger.Series(ctx, ouroboros.SeriesStruggleRate)
		Expect(err).NotTo(HaveOccurred())
		Expect(points).To(HaveLen(1))
		Expect(points[0].Day).To(Equal(measuredDay))
		Expect(points[0].Numerator).To(Equal(int64(1)))
		Expect(points[0].Denominator).To(Equal(int64(2)))
		compounding, err := spec.ledger.Series(ctx, ouroboros.SeriesCompounding)
		Expect(err).NotTo(HaveOccurred())
		Expect(compounding).To(HaveLen(1))
		Expect(compounding[0].Value).To(BeNil())
		weekly, err := spec.ledger.Series(ctx, ouroboros.SeriesStruggleRateWeekly)
		Expect(err).NotTo(HaveOccurred())
		Expect(weekly).To(HaveLen(1))
		Expect(weekly[0].Denominator).To(Equal(int64(2)))
		structure, err := spec.ledger.Series(ctx, ouroboros.SeriesStructure)
		Expect(err).NotTo(HaveOccurred())
		Expect(structure).To(HaveLen(1))
		Expect(structure[0].Numerator).To(Equal(int64(0)), "one commit all week gains nothing")

		var written ouroboros.Snapshot
		Expect(json.Unmarshal(spec.readSnapshot(), &written)).To(Succeed())
		Expect(written.Findings).To(Equal(1))

		engine := httpserver.NewEngine("ouroboros-spec")
		spec.loop.Register(engine)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, ouroboros.APIPath, nil))
		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(recorder.Body.String()).To(ContainSubstring(`"findings":1`))
	})
})
