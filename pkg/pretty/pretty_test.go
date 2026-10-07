package pretty

import (
	"bytes"
	"testing"

	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPretty(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Pretty Suite")
}

var _ = Describe("Pretty", func() {
	Describe("Render", func() {
		It("renders a single message as a table", func() {
			msg := &harnessv1.AgentSessionState{
				AssignmentId: "test-id",
				AgentId:      "agent-1",
				Phase:        harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN,
				Turns:        5,
				Queued:       2,
			}
			var buf bytes.Buffer
			err := Render(&buf, msg)
			Expect(err).NotTo(HaveOccurred())
			output := buf.String()
			Expect(output).To(ContainSubstring("Assignment Id"))
			Expect(output).To(ContainSubstring("test-id"))
		})
	})

	Describe("RenderList", func() {
		It("renders multiple messages as a table with one row per message", func() {
			msgs := []proto.Message{
				&harnessv1.AgentSessionState{
					AssignmentId: "id-1",
					AgentId:      "agent-1",
					Phase:        harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN,
					Turns:        5,
				},
				&harnessv1.AgentSessionState{
					AssignmentId: "id-2",
					AgentId:      "agent-2",
					Phase:        harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING,
					Turns:        3,
				},
			}
			var buf bytes.Buffer
			err := RenderList(&buf, msgs)
			Expect(err).NotTo(HaveOccurred())
			output := buf.String()
			Expect(output).To(ContainSubstring("id-1"))
			Expect(output).To(ContainSubstring("id-2"))
		})
	})

	Describe("RenderList with timestamps", func() {
		It("handles timestamp fields properly", func() {
			ts := timestamppb.Now()
			msg := &harnessv1.AgentSessionState{
				AssignmentId: "test-id",
				StartedAt:    ts,
			}
			var buf bytes.Buffer
			err := Render(&buf, msg)
			Expect(err).NotTo(HaveOccurred())
			output := buf.String()
			Expect(output).NotTo(BeEmpty())
		})
	})
})
