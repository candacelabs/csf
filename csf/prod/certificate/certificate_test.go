// Copyright 2026 Candace Labs

package certificate_test

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/csf/prod/certificate"
)

func TestCertificate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "csf/prod/certificate Suite")
}

// checkFixture is one required check's reference sample, with the direction
// that passes.
type checkFixture struct {
	name      string
	higher    bool
	reference []float64
}

// checkData is the reference sample each required check reads its threshold
// from, with the direction that passes. Every sample rises from low to high so
// the knee falls inside the curve and the threshold is always one of its
// points, never a number typed into the source.
var checkData = []checkFixture{
	{certificate.CheckCoverage, true, []float64{0.41, 0.55, 0.63, 0.71, 0.84, 0.93}},
	{certificate.CheckMDL, false, []float64{0.22, 0.34, 0.45, 0.58, 0.71, 0.89}},
	{certificate.CheckRoundTrip, true, []float64{0.50, 0.66, 0.74, 0.81, 0.88, 0.96}},
	{certificate.CheckStabilitySplitHalf, true, []float64{0.44, 0.57, 0.68, 0.76, 0.85, 0.94}},
	{certificate.CheckStabilityReorder, true, []float64{0.47, 0.60, 0.69, 0.78, 0.86, 0.95}},
	{certificate.CheckLean, true, []float64{0.35, 0.52, 0.64, 0.73, 0.82, 0.91}},
	{certificate.CheckHeldoutAccuracy, true, []float64{0.58, 0.67, 0.75, 0.82, 0.89, 0.95}},
	{certificate.CheckHeldoutECE, false, []float64{0.03, 0.05, 0.07, 0.10, 0.14, 0.19}},
	{certificate.CheckConsequenceBuild, true, []float64{0.62, 0.71, 0.78, 0.85, 0.92, 0.98}},
	{certificate.CheckConsequenceTest, true, []float64{0.55, 0.64, 0.73, 0.81, 0.90, 0.97}},
	{certificate.CheckConsequenceScore, true, []float64{0.24, 0.39, 0.51, 0.63, 0.77, 0.90}},
	{certificate.CheckOrderShuffleAgreement, true, []float64{0.60, 0.70, 0.79, 0.86, 0.93, 0.99}},
	{certificate.CheckConfidenceKnee, true, []float64{0.48, 0.61, 0.70, 0.79, 0.87, 0.94}},
	{certificate.CheckOperatorSample, true, []float64{0.66, 0.74, 0.82, 0.88, 0.94, 1.00}},
}

// sample is the measurement a check judges: its value sits at the accepting
// extreme when good, and beyond the reference at the rejecting extreme when
// not, so a bad sample fails whatever the knee turns out to be.
func sample(reference []float64, higher bool, good bool) certificate.Sample {
	low, high := slices.Min(reference), slices.Max(reference)
	switch {
	case higher && good:
		return certificate.Sample{Value: high, Reference: reference}
	case higher && !good:
		return certificate.Sample{Value: low - 1, Reference: reference}
	case !higher && good:
		return certificate.Sample{Value: low, Reference: reference}
	default:
		return certificate.Sample{Value: high + 1, Reference: reference}
	}
}

// fixtureChoices is a fixture repo whose choices are right: every check's value
// is at its accepting extreme.
func fixtureChoices() certificate.Choices {
	choices := certificate.Choices{}
	for _, data := range checkData {
		choices[data.name] = sample(data.reference, data.higher, true)
	}
	return choices
}

// badChoices is the fixture with one check's value moved to its rejecting
// extreme, so exactly that check fails.
func badChoices(name string) certificate.Choices {
	choices := fixtureChoices()
	for _, data := range checkData {
		if data.name == name {
			choices[data.name] = sample(data.reference, data.higher, false)
		}
	}
	return choices
}

// referenceOf returns the reference sample the fixture reads for one check.
func referenceOf(name string) []float64 {
	for _, data := range checkData {
		if data.name == name {
			return data.reference
		}
	}
	return nil
}

var _ = Describe("Verify", func() {
	It("passes every required check for a fixture repo whose choices are right", func() {
		produced := certificate.Verify("fixture/repo", "0000000", fixtureChoices(), time.Unix(0, 0))

		Expect(produced.Pass).To(BeTrue())
		Expect(produced.Repo).To(Equal("fixture/repo"))
		Expect(produced.Revision).To(Equal("0000000"))
		Expect(produced.PerCheck).To(HaveLen(len(certificate.Names())))
		for index, check := range produced.PerCheck {
			Expect(check.Name).To(Equal(certificate.Names()[index]))
			Expect(check.Pass).To(BeTrue(), check.Name)
		}
	})

	It("reads every threshold from its reference sample, never a typed number", func() {
		produced := certificate.Verify("fixture/repo", "0000000", fixtureChoices(), time.Unix(0, 0))

		for _, check := range produced.PerCheck {
			Expect(check.Derivation.Method).To(Equal("knee"), check.Name)
			Expect(check.Derivation.Samples).To(Equal(len(referenceOf(check.Name))), check.Name)
			Expect(check.Derivation.Quantile).To(BeNumerically(">", 0))
			Expect(check.Derivation.Quantile).To(BeNumerically("<=", 1))
			Expect(referenceOf(check.Name)).To(ContainElement(check.Derivation.Threshold), check.Name)
		}
	})

	It("fails the certificate and exactly one check for each check's bad option set", func() {
		for _, data := range checkData {
			produced := certificate.Verify("fixture/repo", "0000000", badChoices(data.name), time.Unix(0, 0))

			Expect(produced.Pass).To(BeFalse(), data.name)
			for _, check := range produced.PerCheck {
				if check.Name == data.name {
					Expect(check.Pass).To(BeFalse(), "the bad "+data.name+" passed")
					continue
				}
				Expect(check.Pass).To(BeTrue(), "the bad "+data.name+" also broke "+check.Name)
			}
		}
	})

	It("fails a check whose reference sample is too small to derive a threshold", func() {
		choices := fixtureChoices()
		choices[certificate.CheckCoverage] = certificate.Sample{Value: 1, Reference: []float64{0.5}}

		produced := certificate.Verify("fixture/repo", "0000000", choices, time.Unix(0, 0))

		Expect(produced.Pass).To(BeFalse())
		for _, check := range produced.PerCheck {
			if check.Name != certificate.CheckCoverage {
				continue
			}
			Expect(check.Pass).To(BeFalse())
			Expect(check.Derivation.Method).To(Equal("none"))
		}
	})

	It("fails a required check the choices do not carry at all", func() {
		choices := fixtureChoices()
		delete(choices, certificate.CheckLean)

		produced := certificate.Verify("fixture/repo", "0000000", choices, time.Unix(0, 0))

		Expect(produced.Pass).To(BeFalse())
		for _, check := range produced.PerCheck {
			if check.Name == certificate.CheckLean {
				Expect(check.Pass).To(BeFalse())
			}
		}
	})
})

var _ = Describe("Write and Read", func() {
	It("round-trips a certificate through its file", func() {
		path := filepath.Join(GinkgoT().TempDir(), "certificate.json")
		produced := certificate.Verify("fixture/repo", "0000000", fixtureChoices(), time.Unix(0, 0))

		Expect(certificate.Write(path, produced)).To(Succeed())
		read, err := certificate.Read(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(read).To(Equal(produced))
	})
})
