// Copyright 2026 Candace Labs

package cloud

import (
	"os"
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// The cloud series, measured from the record at each scrape; the Grafana
// panels in csf/observability/cloud-dashboard.json read them.
var (
	spendDescription = prometheus.NewDesc("csf_cloud_spend_usd_total",
		"Spend of every cloud job in the record, in dollars; its rate is spend per hour.", nil, nil)
	todayDescription = prometheus.NewDesc("csf_cloud_spend_today_usd",
		"Spend of the cloud jobs created today (UTC), in dollars.", nil, nil)
	dailyCapDescription = prometheus.NewDesc("csf_cloud_daily_cap_usd",
		"The day's cap on cloud spend, in dollars.", nil, nil)
	runningDescription = prometheus.NewDesc("csf_cloud_jobs_running",
		"Cloud jobs that may still be spending.", nil, nil)
	jobSpendDescription = prometheus.NewDesc("csf_cloud_job_spend_usd",
		"One cloud job's spend so far, in dollars.", []string{"job", "flavor", "purpose"}, nil)
	jobCapDescription = prometheus.NewDesc("csf_cloud_job_cap_usd",
		"One cloud job's cap, in dollars.", []string{"job", "flavor", "purpose"}, nil)
	sessionsDescription = prometheus.NewDesc("csf_cloud_burst_sessions_total",
		"Fixer sessions launched in burst jobs.", nil, nil)
	mergedDescription = prometheus.NewDesc("csf_cloud_burst_pull_requests_merged_total",
		"Pull requests merged from ended burst jobs.", nil, nil)
	burstSpendDescription = prometheus.NewDesc("csf_cloud_burst_spend_usd_total",
		"Spend of ended burst jobs whose merged pull requests are counted, in dollars: the denominator of pull requests per dollar.", nil, nil)
)

// Collector measures the cloud record at each scrape.
type Collector struct {
	directory string
}

var _ prometheus.Collector = (*Collector)(nil)

// NewCloudCollector measures the record under the state directory.
func NewCloudCollector(directory string) *Collector { return &Collector{directory: directory} }

// Describe sends every series' description.
func (collector *Collector) Describe(descriptions chan<- *prometheus.Desc) {
	for _, description := range []*prometheus.Desc{spendDescription, todayDescription, dailyCapDescription, runningDescription,
		jobSpendDescription, jobCapDescription, sessionsDescription, mergedDescription, burstSpendDescription} {
		descriptions <- description
	}
}

// Collect reads the record and sends every series.
func (collector *Collector) Collect(metrics chan<- prometheus.Metric) {
	content, err := os.ReadFile(filepath.Join(collector.directory, LedgerFile))
	if err != nil {
		return
	}
	ledger := ReadLedger(content)
	total, sessions, merged, burstSpend := 0.0, 0, 0, 0.0
	for _, job := range ledger.Jobs {
		total += job.SpendUSD
		sessions += len(job.Slices)
		if job.PullRequests >= 0 && len(job.Branches) > 0 {
			merged += job.PullRequests
			burstSpend += job.SpendUSD
		}
		if job.Running() {
			metrics <- prometheus.MustNewConstMetric(jobSpendDescription, prometheus.GaugeValue, job.SpendUSD, job.ID, job.Flavor, job.Purpose)
			metrics <- prometheus.MustNewConstMetric(jobCapDescription, prometheus.GaugeValue, job.CapUSD, job.ID, job.Flavor, job.Purpose)
		}
	}
	metrics <- prometheus.MustNewConstMetric(spendDescription, prometheus.CounterValue, total)
	metrics <- prometheus.MustNewConstMetric(todayDescription, prometheus.GaugeValue, ledger.SpentOn(time.Now()))
	metrics <- prometheus.MustNewConstMetric(dailyCapDescription, prometheus.GaugeValue, ledger.DailyCapUSD)
	metrics <- prometheus.MustNewConstMetric(runningDescription, prometheus.GaugeValue, float64(ledger.RunningJobs()))
	metrics <- prometheus.MustNewConstMetric(sessionsDescription, prometheus.CounterValue, float64(sessions))
	metrics <- prometheus.MustNewConstMetric(mergedDescription, prometheus.CounterValue, float64(merged))
	metrics <- prometheus.MustNewConstMetric(burstSpendDescription, prometheus.CounterValue, burstSpend)
}
