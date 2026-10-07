// Copyright 2026 Candace Labs

package views

import (
	"regexp"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/candacelabs/csf/services/cloud"
	"github.com/candacelabs/csf/services/harness/routing"
	"github.com/candacelabs/csf/services/harness/sessiongate"
	"github.com/candacelabs/csf/services/intake"
	"github.com/candacelabs/csf/services/ouroboros/evaluate"
)

// describedExpression reads a descriptor's name and help from its String.
var describedExpression = regexp.MustCompile(`fqName: "([^"]+)", help: ("(?:[^"\\]|\\.)*")`)

var _ = Describe("the catalog", func() {
	It("is exactly what /metrics describes: every family, the pending ones included, with its definition as help", func() {
		described := map[string]string{}
		// The chat page observes its histogram with exactly these options; it
		// imports this package, so its collector cannot be built here.
		chatSettle := prometheus.NewHistogram(HistogramOpts(MetricChatSettle, nil))
		for _, exported := range []prometheus.Collector{newCollector(&Views{}), routing.NewCollector(GinkgoT().TempDir()),
			sessiongate.NewQuestionCollector(GinkgoT().TempDir()), sessiongate.NewRulingCollector(GinkgoT().TempDir()), cloud.NewCloudCollector(GinkgoT().TempDir()), chatSettle, intake.NewDeliveryCounts(),
			evaluate.NewSuiteCollector(nil)} {
			descriptors := make(chan *prometheus.Desc, 64)
			exported.Describe(descriptors)
			close(descriptors)
			for descriptor := range descriptors {
				match := describedExpression.FindStringSubmatch(descriptor.String())
				Expect(match).NotTo(BeNil(), descriptor.String())
				help, err := strconv.Unquote(match[2])
				Expect(err).NotTo(HaveOccurred())
				described[match[1]] = help
			}
		}
		definitions := map[string]string{}
		for _, metric := range Metrics() {
			definitions[metric.Name] = metric.Definition
		}
		Expect(described).To(Equal(definitions))
	})
})
