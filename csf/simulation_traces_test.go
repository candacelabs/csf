package csf

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	db "github.com/candacelabs/csf/csf/internal/brainspinedb"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("native simulator trace projection", func() {
	var local *LocalSimulations
	var root string
	var row db.BrainspineSimulation
	BeforeEach(func() {
		root = GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(root, "native"), 0700)).To(Succeed())
		local = &LocalSimulations{config: &pb.LocalSimulationConfig{PublicTraces: true, Profiles: []*pb.LocalSimulationProfile{{Simulator: pb.Simulator_SIMULATOR_CARLA, ArtifactDirectory: root, ArtifactUrl: "http://example.invalid/ui/runs"}}}}
		row = db.BrainspineSimulation{RunID: "native", Simulator: 1, Steps: 1, CompletedSteps: 1, State: db.BrainspineSimulationStateSucceeded}
		events := []*pb.ResearchEvent{
			{SchemaVersion: 1, RecordedAt: "2026-01-01T12:00:00Z", Payload: &pb.ResearchEvent_Status{Status: &pb.RunStatus{RunId: "native", Phase: "started"}}},
			{SchemaVersion: 1, RecordedAt: "2026-01-01T12:00:03Z", Payload: &pb.ResearchEvent_Measurement{Measurement: &pb.Measurement{RunId: "native", Step: 1, Metric: "simulation_seconds", Value: 0.05}}},
			{SchemaVersion: 1, RecordedAt: "2026-01-01T12:00:04Z", Payload: &pb.ResearchEvent_Status{Status: &pb.RunStatus{RunId: "native", Phase: "completed"}}},
		}
		var lines []string
		for _, event := range events {
			content, err := protojson.Marshal(event)
			Expect(err).NotTo(HaveOccurred())
			lines = append(lines, string(content))
		}
		Expect(os.WriteFile(filepath.Join(root, "native", "events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "native", "trace.jsonl"), []byte("{\"step\":0,\"simulation_seconds\":0.05,\"position\":[1,2,3]}\n"), 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "native", "manifest.json"), []byte(`{"status":"completed"}`), 0600)).To(Succeed())
	})
	It("uses actual wall clocks, keeps simulator time in output and gives replay-stable identities", func() {
		payload, id, err := local.trace(row)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(HaveLen(32))
		spans := payload.ScopeSpans[0].Spans
		Expect(spans).To(HaveLen(2))
		Expect(spans[0].EndTimeUnixNano - spans[0].StartTimeUnixNano).To(Equal(uint64(4 * time.Second)))
		Expect(spans[1].EndTimeUnixNano).To(Equal(spans[1].StartTimeUnixNano))
		Expect(spans[1].Name).To(Equal("Simulation step 1"))
		Expect(spans[1].Attributes[1].Key).To(Equal("langfuse.observation.metadata.simulation_steps_completed"))
		Expect(spans[1].Attributes[1].Value.GetIntValue()).To(Equal(int64(1)))
		Expect(spans[1].Attributes[2].Value.GetStringValue()).To(ContainSubstring(`"simulation_seconds":0.05`))
		repeated, repeatedID, err := local.trace(row)
		Expect(err).NotTo(HaveOccurred())
		Expect(repeatedID).To(Equal(id))
		Expect(repeated.ScopeSpans[0].Spans[1].SpanId).To(Equal(spans[1].SpanId))
	})
	It("reconstructs identical spans from the typed source without local files", func() {
		source, err := local.traceSource(row)
		Expect(err).NotTo(HaveOccurred())
		original, identity, err := local.trace(row)
		Expect(err).NotTo(HaveOccurred())
		encoded, err := protojson.Marshal(source)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.RemoveAll(filepath.Join(root, "native"))).To(Succeed())
		retained := &pb.SimulationTraceSource{}
		Expect(protojson.Unmarshal(encoded, retained)).To(Succeed())
		rebuilt, repeatedID, err := local.projectTrace(retained)
		Expect(err).NotTo(HaveOccurred())
		Expect(repeatedID).To(Equal(identity))
		Expect(proto.Equal(original, rebuilt)).To(BeTrue())
	})
	It("rejects a mismatched camera hash instead of advertising unsupported provenance", func() {
		frame := &pb.SimulationFrames{Frames: []*pb.SimulationFrame{{Step: 1, Path: "camera-step-000001.png", Sha256: strings.Repeat("0", 64)}}}
		raw, err := protojson.Marshal(frame)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(root, "native", "frames.json"), raw, 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "native", "camera-step-000001.png"), []byte("test fixture bytes"), 0600)).To(Succeed())
		_, _, err = local.trace(row)
		Expect(err).To(MatchError(ContainSubstring("hash mismatch")))
	})
	It("does not synthesize missing step wall clocks from simulation seconds", func() {
		Expect(os.WriteFile(filepath.Join(root, "native", "trace.jsonl"), []byte("{}\n{}\n"), 0600)).To(Succeed())
		row.Steps = 2
		_, _, err := local.trace(row)
		Expect(err).To(MatchError(ContainSubstring("no observed wall-clock")))
	})
	It("exports observed startup failures even when no vendor manifest was written", func() {
		row.State = db.BrainspineSimulationStateFailed
		row.Reason = "world readiness timed out"
		Expect(os.Remove(filepath.Join(root, "native", "manifest.json"))).To(Succeed())
		payload, _, err := local.trace(row)
		Expect(err).NotTo(HaveOccurred())
		Expect(payload.ScopeSpans[0].Spans[0].Status.Message).To(Equal(row.Reason))
		Expect(payload.ScopeSpans[0].Spans[0].Attributes[2].Value.GetStringValue()).To(ContainSubstring(row.Reason))
		row.LogDocumentID, row.TraceUrl, row.LogProjectionError = "archived", "http://example.invalid/trace", "prior retry"
		repeated, _, err := local.trace(row)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(payload, repeated)).To(BeTrue())
	})
	It("only exposes bounded known artifacts from the admitted run directory", func() {
		Expect(os.WriteFile(filepath.Join(root, "native", "secret.txt"), []byte("not an artifact"), 0600)).To(Succeed())
		artifacts := local.artifactViews(row.RunID, pb.Simulator(row.Simulator))
		Expect(artifacts).To(HaveLen(3))
		for _, artifact := range artifacts {
			Expect(artifact.Sha256).To(HaveLen(64))
			Expect(artifact.Url).To(HavePrefix("http://example.invalid/ui/runs/native/"))
		}
		row.RunID = "../elsewhere"
		Expect(local.artifactViews(row.RunID, pb.Simulator(row.Simulator))).To(BeEmpty())
	})
})
