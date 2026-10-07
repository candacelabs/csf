// Copyright 2026 Candace Labs

package sessiongate

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/candacelabs/csf/pkg/terms"
	"github.com/candacelabs/csf/services/harness/session"
)

// RuleMeasuredQuantity: a reply that reports a number to an operator who
// asked for a measurement names the quantity the operator asked for, what it
// measured, and whether the two are the same; a term it introduces for what
// it measured carries a research check.
const RuleMeasuredQuantity Rule = "measured_quantity"

// MeasurementFence tags the fenced block holding a reply's measurement as one
// JSON object.
const MeasurementFence = "measurement"

// Measurement is the typed record a reply carries for a number it reports to
// an operator who asked for a measurement.
type Measurement struct {
	// Quantity is the quantity the operator named, in the operator's words.
	Quantity string `json:"quantity"`
	// Measured is what the reply measured.
	Measured string `json:"measured"`
	// Same is whether Measured is Quantity.
	Same *bool `json:"same"`
	// Difference is how Measured differs from Quantity when they are not the
	// same.
	Difference string `json:"difference"`
}

// measurementAsked is an operator message that asks for a measurement.
var measurementAsked = regexp.MustCompile(`(?i)\b(measur\w*|metrics?|quantif\w*|how (many|much|often)|rates?|density|counts?|percent\w*|share of|mine|mining)\b`)

// numberReported is a number reported as a measurement: a percentage, a
// ratio, a decimal, a multiplier or a rate per unit.
var numberReported = regexp.MustCompile(`(?i)(\d+(\.\d+)?\s?%|\b\d[\d,]*(\.\d+)?\s*(of|/|out of)\s*\d[\d,]*\b|\b\d+\.\d+\b|\b\d+(\.\d+)?\s?[×x]\B|\bper\s+(1,?000|\d+)\b|\bn\s?=\s?\d+)`)

// measurementBlock is a fenced block tagged as a measurement.
var measurementBlock = regexp.MustCompile("(?s)```" + MeasurementFence + "[ \t]*\n(.*?)\n[ \t]*```")

// JudgeMeasurement applies the measured-quantity rule to one reply: operator
// is the turn's message when the operator wrote it, else empty. No finding
// means the reply passes.
func JudgeMeasurement(operator string, reply string) []ReplyFinding {
	if operator == "" || !measurementAsked.MatchString(terms.StripCode(operator)) || !numberReported.MatchString(terms.StripCode(reply)) {
		return []ReplyFinding{}
	}
	problems := []string{}
	measurements, malformed := parseMeasurements(reply)
	problems = append(problems, malformed...)
	if len(measurements) == 0 && len(malformed) == 0 {
		problems = append(problems, "the reply reports a number but carries no measurement block")
	}
	asked := stemmedWords(operator)
	checks, _ := ParseResearchChecks(reply)
	for _, measurement := range measurements {
		problems = append(problems, measurementProblems(measurement, asked, checks)...)
	}
	if len(problems) == 0 {
		return []ReplyFinding{}
	}
	skeleton, _ := json.Marshal(Measurement{Quantity: "<the operator's words>", Measured: "<what you measured>", Same: new(bool), Difference: "<how they differ, when not the same>"})
	return []ReplyFinding{{Rule: RuleMeasuredQuantity, Message: fmt.Sprintf(
		"%s: %s. The operator asked for a measurement: add a fenced block tagged %s holding %s, the quantity in the operator's own words",
		RuleMeasuredQuantity, strings.Join(problems, "; "), MeasurementFence, skeleton)}}
}

func parseMeasurements(reply string) ([]Measurement, []string) {
	measurements := []Measurement{}
	malformed := []string{}
	for index, match := range measurementBlock.FindAllStringSubmatch(reply, -1) {
		var measurement Measurement
		if err := json.Unmarshal([]byte(strings.TrimSpace(match[1])), &measurement); err != nil {
			malformed = append(malformed, fmt.Sprintf("measurement block %d is not a JSON object: %v", index+1, err))
			continue
		}
		measurements = append(measurements, measurement)
	}
	return measurements, malformed
}

// measurementProblems is what one measurement block lacks: asked is the
// operator's message as stemmed words, checks the reply's research checks.
func measurementProblems(measurement Measurement, asked []string, checks []session.ResearchCheck) []string {
	problems := []string{}
	quantity := stemmedWords(measurement.Quantity)
	switch {
	case len(quantity) == 0:
		problems = append(problems, "quantity is empty")
	case !containsRun(asked, quantity):
		problems = append(problems, fmt.Sprintf("quantity %q is not in the operator's words", measurement.Quantity))
	}
	if strings.TrimSpace(measurement.Measured) == "" {
		problems = append(problems, "measured is empty")
	}
	switch {
	case measurement.Same == nil:
		problems = append(problems, "same is missing")
	case !*measurement.Same && strings.TrimSpace(measurement.Difference) == "":
		problems = append(problems, "the measured quantity is not the one asked for and difference does not say how")
	}
	introduced := []terms.Term{}
	for _, term := range terms.Extract(measurement.Measured) {
		if !slices.Contains(asked, terms.Stem(string(term))) {
			introduced = append(introduced, term)
		}
	}
	if missing := MissingResearchChecks(introduced, checks); len(missing) > 0 {
		spelled := make([]string, 0, len(missing))
		for _, term := range missing {
			spelled = append(spelled, string(term))
		}
		problems = append(problems, fmt.Sprintf("measured introduces %s, which the operator did not use: add a research check for each", strings.Join(spelled, listSeparator)))
	}
	return problems
}
