package validate_test

import (
	"fmt"
	"strings"

	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
	"github.com/candacelabs/csf/pkg/widget/internal/validate"
)

// The two base documents every class spec mutates. They are written here rather
// than read from docs/examples/ on purpose: an example is a document a human
// reads, and editing one to teach a reader something should not silently change
// what 68 error classes are tested against.

// minimalDocument is the smallest legal document: nine blocks, one node, one
// flag, one stream, no motion.
const minimalDocument = `widget NodeStatus
dialect 0
region "widget.node-status"
palette fieldStation

state
  field reachable type flag
end

bindings
  binding statusText
    when reachable then "reachable"
    otherwise "unreachable"
  end

  binding sceneDescriptionText
    when reachable then "One node; its health check is passing."
    otherwise "One node; its health check is failing."
  end
end

labels
  label titleLabel text "Node status"
  label sceneDescriptionLabel binds sceneDescriptionText
  label nodeALabel text "node-a"
  label statusLabel binds statusText
end

chrome
  title titleLabel
end

roles
  role node
    token accent
    marker large
    emphasis forbidden
  end
end

placements
  placement centre left 50 top 50
end

scene solo
  description sceneDescriptionLabel

  node nodeA
    role node
    at centre
    title nodeALabel
    caption statusLabel
  end
end

events
  event health
    wire "widget.node-status.health"
    field reachable writes reachable
  end
end

data
  stream healthWatch
    source "widget.node-status.watch"
    delivers health
  end
end
`

// fullDocument exercises all fourteen blocks: two nodes, one edge, one channel,
// motion with a pulse and an emphasis, an indicator, a control, two events and
// a stream. Every class that needs a construct the minimal document omits is
// mutated from this one.
const fullDocument = `widget FullWidget
dialect 0
region "widget.full"
palette fieldStation

state
  field sequence type counter
  field connected type flag
  field paused type flag
  field voters type count
  field degraded type flag signal slowClient
end

predicates
  predicate live
    requires connected
    requires voters atLeast 1
    forbids degraded
  end
end

bindings
  binding statusText
    when live then "live"
    whenNot connected then "offline"
    otherwise "degraded"
  end

  binding descriptionText
    when live then "Two nodes exchanging beats; {voters} voters."
    otherwise "Two nodes, stopped."
  end

  binding captionText
    when paused then "paused"
    otherwise "running"
  end
end

labels
  label titleLabel text "Full widget"
  label sourceLabel text "Read-only stream"
  label descriptionLabel binds descriptionText
  label statusLabel binds statusText
  label captionLabel binds captionText
  label beatLegendLabel text "beat"
  label nodeALabel text "node-a"
  label nodeBLabel text "node-b"
end

chrome
  title titleLabel
  source sourceLabel
  stat statusLabel
end

roles
  role peer
    token accent
    marker large
    emphasis allowed
  end
end

channels
  channel beat
    direction forward
    token accent
    legend beatLegendLabel
  end
end

placements
  placement west left 20 top 50
  placement east left 80 top 50
end

scene pair
  description descriptionLabel

  orbit ring token rule

  node nodeA
    role peer
    at west
    title nodeALabel
    caption captionLabel
  end

  node nodeB
    role peer
    at east
    title nodeBLabel
  end

  edge link
    from nodeA
    to nodeB
    carries beat
  end
end

motion
  requires live
  forbids paused
  restartOn sequence

  pulse beatPulse
    edge link
    channel beat
    duration 800 milliseconds
    delay 0 milliseconds
  end

  emphasis nodeRing
    node nodeA
    duration 700 milliseconds
    delay 0 milliseconds
  end
end

indicators
  indicator connection
    label statusLabel
    positiveWhen live
  end
end

controls
  control pauseToggle
    caption captionLabel
    trigger click
    emits togglePause
    pressedWhen paused
  end
end

events
  event togglePause
    wire "widget.full.toggle"
    toggles paused
  end

  event snapshot
    wire "widget.full.snapshot"
    field sequence writes sequence
    field connected writes connected
    field voters writes voters
  end
end

data
  stream watch
    source "widget.full.watch"
    delivers snapshot
    ordering sequence
  end
end
`

// deepPredicateDocument is the shape the P2 audit's H2 finding was measured on:
// a predicate graph with no cycle in it at all, where every composition names
// two predicates of the level below, so the number of simple paths through it
// doubles with each level.
//
// It is built rather than written out because the point of it is the depth, and
// 27 levels is 54 compositions nobody would keep in step by hand. The document
// reports findings — nothing names most of these predicates — which is
// deliberate: the cost being measured is the search for a cycle, and that runs
// on every document whatever else is wrong with it.
func deepPredicateDocument(levels int) string {
	compositions := &strings.Builder{}
	lower := [2]string{"reachable", "spare"}
	for level := 1; level <= levels; level++ {
		upper := [2]string{fmt.Sprintf("levelA%d", level), fmt.Sprintf("levelB%d", level)}
		for _, name := range upper {
			fmt.Fprintf(compositions, "  predicate %s\n    requires %s\n    requires %s\n  end\n\n", name, lower[0], lower[1])
		}
		lower = upper
	}
	return mutate(minimalDocument,
		edit{"  field reachable type flag\n", "  field reachable type flag\n  field spare type flag\n"},
		edit{"    field reachable writes reachable\n", "    field reachable writes reachable\n    field spare writes spare\n"},
		edit{"bindings\n", "predicates\n" + compositions.String() + "end\n\nbindings\n"},
		edit{"    when reachable then \"reachable\"", "    when " + lower[0] + " then \"reachable\""})
}

// edit is one textual change to a base document. Every edit asserts that the
// text it replaces is present, so a base document that drifts fails the spec
// that depended on it rather than passing vacuously.
type edit struct {
	from string
	to   string
}

func mutate(base string, edits ...edit) string {
	source := base
	for _, change := range edits {
		ExpectWithOffset(2, source).To(ContainSubstring(change.from), "the base document no longer contains the text this spec edits")
		source = strings.Replace(source, change.from, change.to, 1)
	}
	return source
}

// classesOf lists the classes of a run's findings, for an assertion that names
// what was reported when it does not match what was expected.
func classesOf(findings []diag.Finding) []string {
	classes := make([]string, 0, len(findings))
	for _, finding := range findings {
		classes = append(classes, string(finding.Class))
	}
	return classes
}

func findingsFor(source string) []diag.Finding {
	_, findings := validate.Document("fixture.widget", []byte(source))
	return findings
}

// firstOfClass returns the first finding of a class, and fails the spec when
// the class was not reported at all.
func firstOfClass(findings []diag.Finding, class diag.Class) diag.Finding {
	for _, finding := range findings {
		if finding.Class == class {
			return finding
		}
	}
	ExpectWithOffset(1, classesOf(findings)).To(ContainElement(string(class)))
	return diag.Finding{}
}
