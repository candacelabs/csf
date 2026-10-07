// Copyright 2026 Candace Labs

// Package certificate records why a bootstrap's choices are right. A bootstrap
// induces a language and a set of options for a repository, then picks among
// them; the certificate is the proof that both halves are the right ones, one
// Check per property, carried inside the archive the bootstrap emitted as
// certificate.json.
//
// Every check judges a measured value against a threshold read from the data
// by its knee (pkg/knee), never a number typed into the source: the caller
// hands in the reference sample the threshold is read from, the certificate
// derives the threshold and records the quantile it sits at. A check whose
// value falls on the wrong side of its data-derived threshold fails, so the
// certificate fails the moment the data no longer supports a choice.
package certificate

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/candacelabs/csf/pkg/atomicfile"
	"github.com/candacelabs/csf/pkg/knee"
)

// The check names, one per property the certificate proves. The options a
// bootstrap right are the ones that cover the held-out requests, that cost the
// fewest bits against their alternatives, that round-trip and hold under
// resampling, and that close under the proof checker; the picks it right are
// the ones whose confidence is calibrated and whose consequences are good.
const (
	CheckCoverage              = "coverage"
	CheckMDL                   = "mdl"
	CheckRoundTrip             = "round_trip"
	CheckStabilitySplitHalf    = "stability_split_half"
	CheckStabilityReorder      = "stability_reorder"
	CheckLean                  = "lean"
	CheckHeldoutAccuracy       = "heldout_accuracy"
	CheckHeldoutECE            = "heldout_ece"
	CheckConsequenceBuild      = "consequence_build"
	CheckConsequenceTest       = "consequence_test"
	CheckConsequenceScore      = "consequence_score"
	CheckOrderShuffleAgreement = "order_shuffle_agreement"
	CheckConfidenceKnee        = "confidence_knee"
	CheckOperatorSample        = "operator_sample"
)

// methodKnee is how a threshold that could be read is derived; methodNone
// marks a check whose sample held too few points to have a curve, which is a
// failure and not a pass.
const (
	methodKnee = "knee"
	methodNone = "none"
)

// definition is one required check: its name, whether a higher value is the
// accepting side, and what its reference sample measures.
type definition struct {
	name   string
	higher bool
	sample string
}

// required is every check a complete certificate carries, in certificate
// order. Verify emits one Check per entry, so a certificate is never silently
// short a property.
var required = []definition{
	{CheckCoverage, true, "option-set coverage per held-out fold"},
	{CheckMDL, false, "description length of the alternatives the set beat"},
	{CheckRoundTrip, true, "S1 equals S0 per resample"},
	{CheckStabilitySplitHalf, true, "agreement between split halves per resample"},
	{CheckStabilityReorder, true, "agreement under reorder per resample"},
	{CheckLean, true, "share of proofs closed without sorry per project"},
	{CheckHeldoutAccuracy, true, "accuracy per confidence level"},
	{CheckHeldoutECE, false, "calibration error per confidence level"},
	{CheckConsequenceBuild, true, "build outcome per pick"},
	{CheckConsequenceTest, true, "test outcome per pick"},
	{CheckConsequenceScore, true, "score delta per pick"},
	{CheckOrderShuffleAgreement, true, "pick agreement per order shuffle"},
	{CheckConfidenceKnee, true, "per-fold confidence knees"},
	{CheckOperatorSample, true, "operator accept rate per fold"},
}

// FileName is the name the certificate is carried under inside a bootstrap
// archive, and the name the ops view reads from the state directory.
const FileName = "certificate.json"

// Names returns every required check name, in certificate order.
func Names() []string {
	names := make([]string, 0, len(required))
	for _, def := range required {
		names = append(names, def.name)
	}
	return names
}

// Sample is one check's measurement: the value to judge, and the reference
// sample its threshold is read from. The reference is the data, never a typed
// constant, so it moves the threshold when the bootstrap changes.
type Sample struct {
	Value     float64   `json:"value"`
	Reference []float64 `json:"reference"`
}

// Choices is the measurement every required check judges, keyed by check name.
// A required check whose entry is absent or whose reference holds fewer than
// two points fails rather than passing by default.
type Choices map[string]Sample

// Derivation records how a check's threshold was read from the data, so the
// threshold can be recomputed and is never taken on trust.
type Derivation struct {
	// Method is how the threshold was derived: "knee", or "none" when the
	// sample was too small to have a curve.
	Method string `json:"method"`
	// Sample names what the reference points measure.
	Sample string `json:"sample"`
	// Samples is how many reference points the threshold was read from.
	Samples int `json:"samples"`
	// Quantile is where the knee fell in the reference sample.
	Quantile float64 `json:"quantile"`
	// Threshold is the value read from the reference sample.
	Threshold float64 `json:"threshold"`
}

// Check is one certificate entry: the value measured, the threshold read from
// the data, how it was read, and the verdict.
type Check struct {
	Name       string     `json:"name"`
	Value      float64    `json:"value"`
	Derivation Derivation `json:"threshold_derivation"`
	Pass       bool       `json:"pass"`
}

// Certificate is the record every bootstrap archive carries. Pass is true only
// when every check passed.
type Certificate struct {
	Repo     string  `json:"repo"`
	Revision string  `json:"revision"`
	Created  string  `json:"created"`
	PerCheck []Check `json:"per_check"`
	Pass     bool    `json:"pass"`
}

// Verify judges every required check against the choices and returns the
// certificate for them. A check whose reference sample is too small to derive
// a threshold fails, so an incomplete certificate is a failing one.
func Verify(repo string, revision string, choices Choices, created time.Time) Certificate {
	certificate := Certificate{
		Repo:     repo,
		Revision: revision,
		Created:  created.UTC().Format(time.RFC3339),
		PerCheck: make([]Check, 0, len(required)),
	}
	pass := true
	for _, def := range required {
		check := judge(def, choices[def.name])
		certificate.PerCheck = append(certificate.PerCheck, check)
		pass = pass && check.Pass
	}
	certificate.Pass = pass
	return certificate
}

// judge reads the threshold for one check from its reference sample and
// decides the verdict: the value must be at least the knee when a higher value
// is right, and at most it when a lower value is right. A sample with no curve
// derives no threshold and fails.
func judge(def definition, sample Sample) Check {
	result, ok := knee.Of(sample.Reference)
	check := Check{
		Name:  def.name,
		Value: sample.Value,
		Derivation: Derivation{
			Method:    methodKnee,
			Sample:    def.sample,
			Samples:   len(sample.Reference),
			Quantile:  result.Quantile,
			Threshold: result.Threshold,
		},
	}
	if !ok {
		check.Derivation.Method = methodNone
		return check
	}
	check.Pass = sample.Value >= result.Threshold
	if !def.higher {
		check.Pass = sample.Value <= result.Threshold
	}
	return check
}

// Write replaces path with the certificate encoded as indented JSON.
func Write(path string, certificate Certificate) error {
	encoded, err := json.MarshalIndent(certificate, "", "  ")
	if err != nil {
		return fmt.Errorf("certificate: encode %s: %w", path, err)
	}
	return atomicfile.WriteFile(path, append(encoded, '\n'), 0o644)
}

// Read decodes the certificate at path.
func Read(path string) (Certificate, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Certificate{}, fmt.Errorf("certificate: read %s: %w", path, err)
	}
	var certificate Certificate
	if err := json.Unmarshal(content, &certificate); err != nil {
		return Certificate{}, fmt.Errorf("certificate: decode %s: %w", path, err)
	}
	return certificate, nil
}
