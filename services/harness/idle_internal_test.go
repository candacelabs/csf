// Copyright 2026 Candace Labs

package harness

import (
	"os"
	"path/filepath"
	"testing/fstest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/session"
)

// Gap sets whose knee and quantiles were probed with the same chord method
// before the assertions were written: the first has its knee where the
// agent-paced seconds end and the human-paced hours begin.
var (
	probedGaps = []time.Duration{
		time.Second, 2 * time.Second, 3 * time.Second, 5 * time.Second, 8 * time.Second, 13 * time.Second,
		600 * time.Second, 1800 * time.Second, 7200 * time.Second, 86400 * time.Second,
	}
	minuteGaps = []time.Duration{
		30 * time.Second, 45 * time.Second, 60 * time.Second, 90 * time.Second, 120 * time.Second, 150 * time.Second,
		180 * time.Second, 240 * time.Second, 300 * time.Second, 1800 * time.Second, 3600 * time.Second, 7200 * time.Second,
	}
	fallbackBound = 50 * time.Minute
)

// The harness records a gap is measured between, with the keys the scan
// reads; every other line of a real log is an executor event.
const (
	finishedAt  = `{"time":"2026-10-04T10:00:00Z","level":"INFO","msg":"turn finished","turn":1,"event_type":"harness_run_finished"}` + "\n"
	requestedAt = `{"time":"2026-10-04T10:05:00Z","level":"INFO","msg":"turn requested","turn":2,"event_type":"harness_turn_requested"}` + "\n"
	executorAt  = `{"time":"2026-10-04T10:05:03Z","level":"INFO","msg":"turn executor event","turn":2,"direction":"out","event_type":"assistant","event":{"type":"assistant"}}` + "\n"
	finishedTwo = `{"time":"2026-10-04T11:00:00Z","level":"INFO","msg":"turn finished","turn":2,"event_type":"harness_run_finished"}` + "\n"
	requestedTw = `{"time":"2026-10-04T11:00:30Z","level":"INFO","msg":"turn requested","turn":3,"event_type":"harness_turn_requested"}` + "\n"
)

var _ = Describe("deriving the idle bound", func() {
	It("takes the knee of the gap distribution and reports where it sits", func() {
		report := IdleBound(probedGaps, fallbackBound)
		Expect(report.Fallback).To(BeFalse())
		Expect(report.Bound).To(Equal(13 * time.Second))
		Expect(report.Quantile).To(BeNumerically("~", 0.6, 1e-9))
		Expect(report.Gaps).To(Equal(10))
		Expect(report.Quantiles).To(HaveLen(5))
		Expect(report.Quantiles[0]).To(Equal(Quantile{P: 0.5, Value: 10500 * time.Millisecond}))
		Expect(report.Quantiles[1]).To(Equal(Quantile{P: 0.75, Value: 1500 * time.Second}))
		Expect(report.String()).To(HavePrefix("idle bound 13s at quantile 0.600 of 10 gaps (knee); q0.50=11s q0.75=25m0s"))

		minutes := IdleBound(minuteGaps, fallbackBound)
		Expect(minutes.Bound).To(Equal(300 * time.Second))
		Expect(minutes.Quantile).To(BeNumerically("~", 0.75, 1e-9))
	})

	It("falls back to the cache lifetime before two gaps exist, and says so", func() {
		Expect(IdleBound(nil, fallbackBound)).To(Equal(IdleBoundReport{Bound: fallbackBound, Fallback: true}))
		one := IdleBound([]time.Duration{time.Minute}, fallbackBound)
		Expect(one.Bound).To(Equal(fallbackBound))
		Expect(one.Fallback).To(BeTrue())
		Expect(one.Gaps).To(Equal(1))
		Expect(one.String()).To(Equal("idle bound 50m0s (fallback: 1 gaps measured)"))
	})

	It("measures a gap from each turn's end to the next turn's request, across every run", func() {
		state := GinkgoT().TempDir()
		run := filepath.Join(state, "0caf2d51-1ee7-468a-882f-ac478a437640")
		Expect(os.MkdirAll(run, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(run, session.EventsFile), []byte(finishedAt+requestedAt+executorAt+finishedTwo+requestedTw+finishedAt), 0o600)).To(Succeed())
		other := filepath.Join(state, "1d2e3f40-5161-4728-9a0b-c1d2e3f40516")
		Expect(os.MkdirAll(other, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(other, session.EventsFile), []byte("not json\n"+finishedAt+requestedAt), 0o600)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(state, BazelDiskCacheDirectory), 0o700)).To(Succeed())

		gaps, err := TurnGaps(state)
		Expect(err).NotTo(HaveOccurred())
		Expect(gaps).To(ConsistOf(5*time.Minute, 30*time.Second, 5*time.Minute), "a turn end with no next request is no gap")
	})
})

// A process table as /proc shows it: the harness, its executor working in the
// worktree, a background child in the executor's group, and an unrelated
// process. The stat line is the real shape, the command name in parentheses,
// the fields the table reads at their real positions.
const (
	tableHost     = 4242
	tableExecutor = 5000
	tableChild    = 5001
	tableOther    = 7000
	pageSize      = 4096
	worktree      = "/state/run/worktree"
	hostStat      = "4242 (csf) S 1 4242 4242 0 -1 4194304 0 0 0 0 0 0 0 0 20 0 1 0 0 0 100 0\n"
	executorStat  = "5000 (claude code) S 4242 5000 4242 0 -1 4194304 0 0 0 0 0 0 0 0 20 0 1 0 0 0 1000 0\n"
	childStat     = "5001 (bash) S 5000 5000 4242 0 -1 4194304 0 0 0 0 0 0 0 0 20 0 1 0 0 0 10 0\n"
	otherStat     = "7000 (sshd) S 1 7000 7000 0 -1 4194304 0 0 0 0 0 0 0 0 20 0 1 0 0 0 500 0\n"
)

func tableWith(entries map[string]string, links map[string]string) fstest.MapFS {
	table := fstest.MapFS{}
	for name, stat := range entries {
		table[name+"/stat"] = &fstest.MapFile{Data: []byte(stat)}
	}
	for name, target := range links {
		table[name+"/cwd"] = &fstest.MapFile{Mode: os.ModeSymlink, Data: []byte(target)}
	}
	return table
}

var _ = Describe("reading the process table", func() {
	It("sums the groups the harness's children lead and finds an executor by its worktree", func() {
		table := readProcessTable(tableWith(
			map[string]string{"4242": hostStat, "5000": executorStat, "5001": childStat, "7000": otherStat, "self": hostStat},
			map[string]string{"5000": worktree}), tableHost, pageSize)
		Expect(table.rss(tableHost)).To(Equal(uint64(100 * pageSize)))
		Expect(table.childGroups(tableHost)).To(Equal(uint64(1010*pageSize)), "the executor and its child, not the harness or sshd")
		executor, found := table.executorOf(tableHost, worktree)
		Expect(found).To(BeTrue())
		Expect(executor.pid).To(Equal(tableExecutor))
		Expect(table.liveChildren(executor)).To(Equal(1))
		_, found = table.executorOf(tableHost, "/elsewhere")
		Expect(found).To(BeFalse())
	})

	It("skips a stat line it cannot read", func() {
		_, ok := parseStat(1, "1 (init", pageSize)
		Expect(ok).To(BeFalse())
		_, ok = parseStat(1, "1 (init) S 0 1", pageSize)
		Expect(ok).To(BeFalse())
		entry, ok := parseStat(tableChild, childStat, pageSize)
		Expect(ok).To(BeTrue())
		Expect(entry).To(Equal(processEntry{pid: tableChild, parent: tableExecutor, group: tableExecutor, rssBytes: 10 * pageSize}))
	})
})

var _ = Describe("the resident series file", func() {
	It("reads back what it wrote and skips a line that is not a sample", func() {
		path := filepath.Join(GinkgoT().TempDir(), ResidentFile)
		at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
		window := []ResidentSample{{At: at, HarnessRSSBytes: 56 << 20, ExecutorsRSSBytes: 1200 << 20, OpenSessions: 5, ExecutorsAlive: 4, Resumes: 1, ResumeTimeToFirstTokenMs: 3497, IdleBoundSeconds: 661, IdleBoundQuantile: 0.829, Gaps: 486}}
		Expect(writeResidentSeries(path, window)).To(Succeed())
		content, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(ReadResidentSeries(append([]byte("not json\n"), content...))).To(Equal(window))
		Expect(ReadResidentSeries(nil)).To(BeEmpty())
	})
})
