package noteboard_test

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/candaceos/component"
	"github.com/candacelabs/csf/services/candaceos/webui"

	"example.com/candace-external-consumer/noteboard"
)

// The ledger's rules are this repository's, so they are tested here rather than
// through the page: nothing in this file needs Core, a database, a network, or
// the composition root.

func TestNoteboard(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Noteboard Suite")
}

// steeringStub is the steering source the board reads. The board declares that
// interface itself, which is what lets the ledger be exercised without the
// component that satisfies it in the shipped composition.
type steeringStub struct{ observed []string }

func (stub *steeringStub) Observed() []string { return append([]string(nil), stub.observed...) }

type noCapabilities struct{}

func (noCapabilities) Log(ctx context.Context, event string, message string) error { return nil }

// upstream is the definition the board declares its requirement on. Its
// identity is all the board uses; the resolver reads the edge, not the value.
func upstream() *component.Definition {
	GinkgoHelper()
	definition, err := component.New("steering-service", component.WithAssemble(
		func(ctx context.Context, capabilities component.ICapabilities) error { return nil },
	))
	Expect(err).NotTo(HaveOccurred())
	return definition
}

// runningBoard returns a board Core would have assembled and started, together
// with the steering source behind it.
func runningBoard() (*noteboard.Board, *steeringStub, *component.Definition) {
	GinkgoHelper()
	source := &steeringStub{}
	board, err := noteboard.New(source, webui.Brand{ProductName: "Quillfern", AgentName: "Bramble"})
	Expect(err).NotTo(HaveOccurred())
	definition, err := board.Component(upstream())
	Expect(err).NotTo(HaveOccurred())
	Expect(definition.Assemble(context.Background(), noCapabilities{})).To(Succeed())
	Expect(definition.Start(context.Background())).To(Succeed())
	return board, source, definition
}

func texts(notes []noteboard.Note) []string {
	rendered := make([]string, 0, len(notes))
	for _, note := range notes {
		rendered = append(rendered, note.Text)
	}
	return rendered
}

var _ = Describe("constructing the board", func() {
	It("refuses a board with no steering source", func() {
		_, err := noteboard.New(nil, webui.Brand{})
		Expect(err).To(MatchError(noteboard.ErrNoSteering))
	})

	It("refuses a brand the operator UI would reject", func() {
		_, err := noteboard.New(&steeringStub{}, webui.Brand{
			Palette: webui.Palette{Canvas: "#fff; position: fixed"},
		})
		Expect(err).To(MatchError(webui.ErrInvalidPaletteValue))
	})

	It("resolves the brand once, so the page cannot render an unnamed product", func() {
		board, err := noteboard.New(&steeringStub{}, webui.Brand{})
		Expect(err).NotTo(HaveOccurred())
		Expect(board).NotTo(BeNil())
	})
})

var _ = Describe("the board's lifecycle", func() {
	It("records nothing until Core has started it", func() {
		source := &steeringStub{observed: []string{"restart the ingest worker"}}
		board, err := noteboard.New(source, webui.Brand{})
		Expect(err).NotTo(HaveOccurred())
		definition, err := board.Component(upstream())
		Expect(err).NotTo(HaveOccurred())

		Expect(board.Read().Running).To(BeFalse())
		Expect(board.Read().Notes).To(BeEmpty())

		Expect(definition.Assemble(context.Background(), noCapabilities{})).To(Succeed())
		Expect(board.Read().Running).To(BeFalse(), "assembly is not a start")
		Expect(board.Read().Notes).To(BeEmpty())

		Expect(definition.Start(context.Background())).To(Succeed())
		Expect(board.Read().Running).To(BeTrue())
		Expect(texts(board.Read().Notes)).To(Equal([]string{"restart the ingest worker"}))
	})

	It("refuses to start before it is assembled", func() {
		board, err := noteboard.New(&steeringStub{}, webui.Brand{})
		Expect(err).NotTo(HaveOccurred())
		definition, err := board.Component(upstream())
		Expect(err).NotTo(HaveOccurred())

		Expect(definition.Start(context.Background())).To(MatchError(noteboard.ErrUnassembled))
	})

	It("stops recording when Core stops it", func() {
		board, source, definition := runningBoard()
		source.observed = append(source.observed, "roll the edge certificate")
		Expect(board.Read().Notes).To(HaveLen(1))

		Expect(definition.Stop(context.Background())).To(Succeed())
		Expect(board.Read().Running).To(BeFalse())
		Expect(board.Read().Notes).To(BeEmpty())
	})

	It("requires the component it was handed", func() {
		board, err := noteboard.New(&steeringStub{}, webui.Brand{})
		Expect(err).NotTo(HaveOccurred())
		steeringService := upstream()
		definition, err := board.Component(steeringService)
		Expect(err).NotTo(HaveOccurred())

		Expect(definition.Name()).To(Equal(noteboard.ComponentName))
		resolved, err := component.Order(definition, steeringService)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved[0]).To(BeIdenticalTo(steeringService))
		Expect(resolved[1]).To(BeIdenticalTo(definition))
	})
})

var _ = Describe("the ledger's rules", func() {
	It("records each steering input once, however often the page is read", func() {
		board, source, _ := runningBoard()
		source.observed = []string{"drain node-a", "drain node-b"}

		Expect(texts(board.Read().Notes)).To(Equal([]string{"drain node-a", "drain node-b"}))
		Expect(texts(board.Read().Notes)).To(Equal([]string{"drain node-a", "drain node-b"}))

		source.observed = append(source.observed, "drain node-c")
		Expect(texts(board.Read().Notes)).To(Equal([]string{"drain node-a", "drain node-b", "drain node-c"}))
	})

	It("treats a repeat of the newest note as a retry", func() {
		board, source, _ := runningBoard()
		source.observed = []string{"retry the failed deployment", "retry the failed deployment"}

		Expect(texts(board.Read().Notes)).To(Equal([]string{"retry the failed deployment"}))

		// The same text after something else is a new note: only a consecutive
		// repeat is a retry.
		source.observed = append(source.observed, "check quorum", "retry the failed deployment")
		Expect(texts(board.Read().Notes)).To(Equal([]string{
			"retry the failed deployment", "check quorum", "retry the failed deployment",
		}))
	})

	It("ignores an input that is only whitespace", func() {
		board, source, _ := runningBoard()
		source.observed = []string{"  ", "\t\n", "  pin the release  "}

		Expect(texts(board.Read().Notes)).To(Equal([]string{"pin the release"}))
	})

	It("discards the oldest note past capacity and keeps counting", func() {
		board, source, _ := runningBoard()
		for index := 1; index <= noteboard.Capacity+6; index++ {
			source.observed = append(source.observed, fmt.Sprintf("step %d", index))
		}

		notes := board.Read().Notes
		Expect(notes).To(HaveLen(noteboard.Capacity))
		Expect(notes[0]).To(Equal(noteboard.Note{Sequence: 7, Text: "step 7"}))
		Expect(notes[len(notes)-1]).To(Equal(noteboard.Note{
			Sequence: noteboard.Capacity + 6,
			Text:     fmt.Sprintf("step %d", noteboard.Capacity+6),
		}))
	})

	It("carries on when the steering window it reads has shrunk", func() {
		board, source, _ := runningBoard()
		source.observed = []string{"first", "second", "third"}
		Expect(board.Read().Notes).To(HaveLen(3))

		// The steering store is bounded too, so its window can drop entries a
		// board already folded. Re-reading the whole window would duplicate
		// every note; the board clamps instead.
		source.observed = []string{"third"}
		Expect(texts(board.Read().Notes)).To(Equal([]string{"first", "second", "third"}))

		source.observed = []string{"third", "fourth"}
		Expect(texts(board.Read().Notes)).To(Equal([]string{"first", "second", "third", "fourth"}))
	})
})
