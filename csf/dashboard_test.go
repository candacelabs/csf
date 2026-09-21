package csf

import (
	"os"
	"path/filepath"
	"strings"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
)

var _ = Describe("bounded dashboard history", func() {
	var file *os.File
	var path string
	var appendEvent func(event *pb.ResearchEvent)

	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "events.jsonl")
		var err error
		file, err = os.Create(path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(file.Close)
		appendEvent = func(event *pb.ResearchEvent) {
			event.SchemaVersion = SchemaVersion
			event.RecordedAt = "2026-09-17T00:00:00Z"
			content, err := protojson.Marshal(event)
			Expect(err).NotTo(HaveOccurred())
			_, err = file.Write(append(content, '\n'))
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It("keeps the latest measurements past the byte limit and preserves old and late definitions", func() {
		definition := &pb.MetricDefinition{Name: "reward", Unit: "points", Description: "Episode reward"}
		appendEvent(&pb.ResearchEvent{Payload: &pb.ResearchEvent_Definition{Definition: definition}})
		for range 300 {
			appendEvent(&pb.ResearchEvent{Payload: &pb.ResearchEvent_Status{Status: &pb.RunStatus{RunId: "run", Phase: "training", Message: strings.Repeat("x", 64*1024)}}})
		}
		late := &pb.MetricDefinition{Name: "distance", Unit: "m", Description: "Distance travelled"}
		appendEvent(&pb.ResearchEvent{Payload: &pb.ResearchEvent_Definition{Definition: late}})
		measurement := &pb.Measurement{Metric: late.Name, RunId: "run", Value: 42, Split: "held_out"}
		appendEvent(&pb.ResearchEvent{Payload: &pb.ResearchEvent_Measurement{Measurement: measurement}})
		_, err := file.WriteString(`{"schema_version":1,"measurement":`)
		Expect(err).NotTo(HaveOccurred())
		snapshot := NewDashboard(path).Snapshot()
		Expect(snapshot.Truncated).To(BeTrue())
		Expect(snapshot.BytesRead).To(BeNumerically(">", maxEventBytes))
		Expect(snapshot.Issues).To(BeEmpty())
		Expect(snapshot.Events[0].GetDefinition().Name).To(Equal(definition.Name))
		Expect(snapshot.Events[1].GetDefinition().Name).To(Equal(late.Name))
		latest := snapshot.Events[len(snapshot.Events)-1].GetMeasurement()
		Expect(latest.GetValue()).To(Equal(measurement.Value))
		Expect(latest.GetSplit()).To(Equal(measurement.Split))
		Expect(len(snapshot.Events)).To(BeNumerically("<", 303))
	})

	It("evicts early observations at the event limit while keeping the definition and final status", func() {
		definition := &pb.MetricDefinition{Name: "reward", Unit: "points", Description: "Episode reward"}
		appendEvent(&pb.ResearchEvent{Payload: &pb.ResearchEvent_Definition{Definition: definition}})
		for step := range maxViewEvents + 2 {
			appendEvent(&pb.ResearchEvent{Payload: &pb.ResearchEvent_Measurement{Measurement: &pb.Measurement{Metric: definition.Name, RunId: "run", Step: uint64(step)}}})
		}
		appendEvent(&pb.ResearchEvent{Payload: &pb.ResearchEvent_Status{Status: &pb.RunStatus{RunId: "run", Phase: "completed"}}})
		snapshot := NewDashboard(path).Snapshot()
		Expect(snapshot.Truncated).To(BeTrue())
		Expect(len(snapshot.Events)).To(BeNumerically("<=", maxViewEvents))
		Expect(snapshot.Events[0].GetDefinition().Name).To(Equal(definition.Name))
		Expect(snapshot.Events[1].GetMeasurement().Step).To(BeNumerically(">", 0))
		Expect(snapshot.Events[len(snapshot.Events)-1].GetStatus().Phase).To(Equal("completed"))
	})

	It("bounds malformed-record diagnostics while continuing to current observations", func() {
		for range maxViewIssues + 2 {
			_, err := file.WriteString("invalid\n")
			Expect(err).NotTo(HaveOccurred())
		}
		appendEvent(&pb.ResearchEvent{Payload: &pb.ResearchEvent_Status{Status: &pb.RunStatus{RunId: "run", Phase: "completed"}}})
		snapshot := NewDashboard(path).Snapshot()
		Expect(snapshot.Issues).To(HaveLen(maxViewIssues))
		Expect(snapshot.Truncated).To(BeTrue())
		Expect(snapshot.Events).To(HaveLen(1))
		Expect(snapshot.Events[0].GetStatus().Phase).To(Equal("completed"))
	})
})
