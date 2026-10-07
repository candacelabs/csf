package widget_test

import (
	"context"
	"strings"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/pkg/widget/widgettest"
)

// A widget definition as data: a dialect document, its binding to a source's
// fields, and fixtures, checked and drawn with no generated code.
const (
	spendName   = "spend-cap"
	spendSource = "ouroboros.json"

	spendDocument = `widget SpendCap
dialect 0
region "widget.spend-cap"
palette fieldStation

state
  field day type text
  field spent type text
  field cap type text
  field dollars type count
end

predicates
  predicate overCap
    requires dollars atLeast 100
  end
end

bindings
  binding statusText
    when overCap then "at the cap"
    otherwise "under the cap"
  end
end

labels
  label titleLabel text "Fixer spend"
  label spendLabel text "${spent} of ${cap} on {day}"
  label sceneLabel binds statusText
  label nodeLabel text "today"
  label statusLabel binds statusText
end

chrome
  title titleLabel
  stat spendLabel
end

roles
  role budgetRole
    token accent
    marker large
    emphasis forbidden
  end
end

placements
  placement centre left 50 top 50
end

scene spend
  description sceneLabel

  node todayNode
    role budgetRole
    at centre
    title nodeLabel
    caption statusLabel
  end
end

indicators
  indicator capIndicator
    label statusLabel
    positiveWhen overCap
  end
end

events
  event snapshot
    wire "widget.spend-cap.snapshot"
    field day writes day
    field spent writes spent
    field cap writes cap
    field dollars writes dollars
  end
end

data
  stream loopWatch
    source "ouroboros.json"
    delivers snapshot
  end
end
`
	spendBinding = `{"stream": "loopWatch", "fields": [
  {"wire": "day", "path": "day"},
  {"wire": "spent", "path": "fixers.spend_today_usd"},
  {"wire": "cap", "path": "fixers.budget_usd"},
  {"wire": "dollars", "path": "fixers.spend_today_usd"}
]}`
	spendFixtures = `[{"name": "under", "source": {"day": "2026-10-05", "fixers": {"spend_today_usd": 12.5, "budget_usd": 100}}, "expect": ["$12.50 of $100.00 on 2026-10-05", "under the cap"]}]`
)

// spendSnapshot is the shape of the source the specs bind: the part of the
// loop's snapshot the spend widget reads, plus kinds a binding cannot read.
type spendSnapshot struct {
	Day    string `json:"day"`
	Fixers struct {
		SpendToday float64  `json:"spend_today_usd"`
		Budget     float64  `json:"budget_usd"`
		Running    int      `json:"running"`
		Launch     bool     `json:"launch_enabled"`
		Yield      *float64 `json:"yield"`
	} `json:"fixers"`
	Days     []string `json:"days"`
	internal string
}

func spendDefinition(replacements map[string]string) fstest.MapFS {
	files := map[string]string{widget.DefinitionFile: spendDocument, widget.BindingFile: spendBinding, widget.FixturesFile: spendFixtures}
	for file, content := range replacements {
		files[file] = content
	}
	definitions := fstest.MapFS{}
	for file, content := range files {
		definitions[spendName+"/"+file] = &fstest.MapFile{Data: []byte(content)}
	}
	return definitions
}

var spendSources = widget.Sources{spendSource: widget.SchemaOf[spendSnapshot]()}

func check(definitions fstest.MapFS) (*widget.CheckedDefinition[live.AnonymousIdentity], []string) {
	return widget.CheckDefinition[live.AnonymousIdentity](definitions, spendName, spendSources)
}

// mounted is the checked widget alone in a registry, showing one source
// document as its stream would deliver it.
func drawn(checked *widget.CheckedDefinition[live.AnonymousIdentity], source string) widgettest.Rendered {
	GinkgoHelper()
	card, err := widgettest.Mount(context.Background(), checked.Widget)
	Expect(err).NotTo(HaveOccurred())
	event, err := checked.Event([]byte(source))
	Expect(err).NotTo(HaveOccurred())
	card.Apply(event)
	rendered, err := card.Render(context.Background())
	Expect(err).NotTo(HaveOccurred())
	return rendered
}

const (
	underCap = `{"day":"2026-10-05","fixers":{"spend_today_usd":12.5,"budget_usd":100}}`
	overCap  = `{"day":"2026-10-05","fixers":{"spend_today_usd":131.2,"budget_usd":100}}`
)

var _ = Describe("A widget definition as data", func() {
	It("checks clean and draws its bound source with no generated code", func() {
		checked, reasons := check(spendDefinition(nil))
		Expect(reasons).To(BeEmpty())
		Expect(checked.Source).To(Equal(spendSource))
		Expect(checked.Widget.Register().Internal).To(Equal([]string{"widget.spend-cap.snapshot"}), "a stream-delivered event is never browser-sendable")

		rendered := drawn(checked, underCap)
		Expect(rendered.Has("$12.50 of $100.00 on 2026-10-05")).To(BeTrue(), rendered.String())
		Expect(rendered.Has(`data-tone="warning"`)).To(BeTrue())
		landmark, found := rendered.Landmark()
		Expect(found).To(BeTrue())
		Expect(landmark).To(Equal(widgettest.Landmark{Element: "aside", LabelledBy: "widget.spend-cap.title", Named: true}))

		rendered = drawn(checked, overCap)
		Expect(rendered.Has("$131.20 of $100.00 on 2026-10-05")).To(BeTrue())
		Expect(rendered.Has(`aria-label="at the cap"`)).To(BeTrue(), "the predicate's bound reads the truncated dollars")
		Expect(rendered.Has(`data-tone="positive"`)).To(BeTrue())
	})

	It("reads a field the source omits or carries as null as its zero value", func() {
		checked, reasons := check(spendDefinition(nil))
		Expect(reasons).To(BeEmpty())
		rendered := drawn(checked, `{"fixers":{"spend_today_usd":null}}`)
		Expect(rendered.Has("$– of $– on –")).To(BeTrue(), rendered.String())
		Expect(rendered.Has("under the cap")).To(BeTrue())
	})

	It("derives a source's schema from its owner's type: scalars by JSON name, never lists or unexported fields", func() {
		Expect(spendSources[spendSource].Paths()).To(Equal([]string{
			"day", "fixers.budget_usd", "fixers.launch_enabled", "fixers.running", "fixers.spend_today_usd", "fixers.yield",
		}))
		kind, known := spendSources[spendSource].Kind("fixers.yield")
		Expect(known).To(BeTrue())
		Expect(kind).To(Equal(widget.SourceNumber))
	})

	It("refuses a binding to a field the source does not have, or of a kind its field cannot read, naming each", func() {
		_, reasons := check(spendDefinition(map[string]string{widget.BindingFile: `{"stream":"loopWatch","fields":[
			{"wire":"day","path":"days"},{"wire":"spent","path":"fixers.yield"},{"wire":"cap","path":"fixers.spend"},{"wire":"dollars","path":"fixers.launch_enabled"}]}`}))
		Expect(reasons).To(Equal([]string{
			`spend-cap/binding.json: field day: "days" is not a field of ouroboros.json; its fields are: day, fixers.budget_usd, fixers.launch_enabled, fixers.running, fixers.spend_today_usd, fixers.yield`,
			`spend-cap/binding.json: field cap: "fixers.spend" is not a field of ouroboros.json; its fields are: day, fixers.budget_usd, fixers.launch_enabled, fixers.running, fixers.spend_today_usd, fixers.yield`,
			"spend-cap/binding.json: field dollars: fixers.launch_enabled holds a bool, which a count field cannot read",
		}))
	})

	It("refuses an unbound wire field, a field bound twice and a wire field the event does not carry", func() {
		_, reasons := check(spendDefinition(map[string]string{widget.BindingFile: `{"stream":"loopWatch","fields":[
			{"wire":"day","path":"day"},{"wire":"day","path":"day"},{"wire":"spend","path":"day"},{"wire":"cap","path":"fixers.budget_usd"},{"wire":"dollars","path":"fixers.running"}]}`}))
		Expect(reasons).To(Equal([]string{
			"spend-cap/binding.json: wire field day is bound twice",
			`spend-cap/binding.json: wire field "spend" is not a field of event snapshot`,
			"spend-cap/binding.json: wire field spent of event snapshot is not bound; bind it to a field of ouroboros.json",
		}))
	})

	It("refuses a stream the document does not declare and a source the host does not have", func() {
		_, reasons := check(spendDefinition(map[string]string{widget.BindingFile: `{"stream":"spendWatch","fields":[]}`}))
		Expect(reasons).To(Equal([]string{`spend-cap/binding.json: stream "spendWatch" is not declared in definition.widget; declare it or bind one of: loopWatch`}))

		_, reasons = check(spendDefinition(map[string]string{widget.DefinitionFile: strings.Replace(spendDocument, `source "ouroboros.json"`, `source "ledger"`, 1)}))
		Expect(reasons).To(Equal([]string{`spend-cap/binding.json: stream loopWatch reads source "ledger", which this host does not have; use one of: ouroboros.json`}))
	})

	It("refuses a document with findings, with the dialect's own anchored reason and repair", func() {
		_, reasons := check(spendDefinition(map[string]string{widget.DefinitionFile: strings.Replace(spendDocument, "requires dollars atLeast 100", "requires dolars atLeast 100", 1)}))
		Expect(reasons).To(HaveLen(1))
		Expect(reasons[0]).To(HavePrefix(`spend-cap/definition.widget:15:14: W201: state field "dolars" is not declared.`))
		Expect(reasons[0]).To(ContainSubstring("fix: add `field dolars type <flag|counter|count|text>`"))
	})

	It("refuses a construct an interpreted widget does not draw", func() {
		document := strings.Replace(spendDocument, "  field dollars type count\n", "  field dollars type count\n  field slow type flag signal slowClient\n", 1)
		document = strings.Replace(document, "    requires dollars atLeast 100\n", "    requires dollars atLeast 100\n    forbids slow\n", 1)
		_, reasons := check(spendDefinition(map[string]string{widget.DefinitionFile: document}))
		Expect(reasons).To(Equal([]string{"spend-cap/definition.widget: field slow: signal fields are runtime-minted and an interpreted widget has no runtime signal; give it an event writer"}))
	})

	It("refuses a fixture the widget does not render, and a definition with no fixture or no binding", func() {
		_, reasons := check(spendDefinition(map[string]string{widget.FixturesFile: `[{"name":"under","source":{"fixers":{"spend_today_usd":1}},"expect":["$1.00 of","over the cap"]}]`}))
		Expect(reasons).To(Equal([]string{`spend-cap/fixtures.json: fixture "under": the widget does not show "over the cap"`}))

		_, reasons = check(spendDefinition(map[string]string{widget.FixturesFile: `[]`}))
		Expect(reasons).To(Equal([]string{"spend-cap/fixtures.json: no fixture; give at least one source document and the text it must show"}))

		definitions := spendDefinition(nil)
		delete(definitions, spendName+"/"+widget.BindingFile)
		_, reasons = check(definitions)
		Expect(reasons).To(HaveLen(1))
		Expect(reasons[0]).To(HavePrefix("spend-cap/binding.json: not read: "))
	})

	It("refuses to draw a document that came with findings", func() {
		document, findings := widget.Interpret("broken.widget", []byte("widget Broken\ndialect 0\n"))
		_, err := widget.NewInterpretedWidget[live.AnonymousIdentity](document, findings)
		Expect(err).To(MatchError(widget.ErrUnsound))
		_, err = widget.NewInterpretedWidget[live.AnonymousIdentity](nil, nil)
		Expect(err).To(MatchError(widget.ErrUnsound))
	})
})
