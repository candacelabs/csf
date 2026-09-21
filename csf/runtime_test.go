package csf

import (
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func fixtureController() *pb.Controller {
	return &pb.Controller{SchemaVersion: 1, Name: "fixture", Steering: &pb.Expression{Opcode: pb.Opcode_OPCODE_SCALE, Value: -500, Arguments: []*pb.Expression{{Opcode: pb.Opcode_OPCODE_INPUT}}}, Acceleration: &pb.Expression{Opcode: pb.Opcode_OPCODE_CONSTANT, Value: 250}}
}

var _ = Describe("controller admission and execution", func() {
	It("uses truncation toward zero and clamps both actuator outputs", func() {
		source := fixtureController()
		program, err := Compile(source)
		Expect(err).NotTo(HaveOccurred())
		result, err := Evaluate(program, []int64{3, 0, 0, 0})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Steering).To(Equal(int64(-1)))
		source.Steering.Value = CoefficientLimit
		source.Acceleration.Value = -ValueLimit
		program, err = Compile(source)
		Expect(err).NotTo(HaveOccurred())
		result, err = Evaluate(program, []int64{FeatureLimit, 0, 0, 0})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Steering).To(Equal(Scale))
		Expect(result.Acceleration).To(Equal(-Scale))
	})
	It("rejects malformed source and externally supplied programs", func() {
		source := fixtureController()
		source.Steering.Arguments = nil
		_, err := Compile(source)
		Expect(err).To(HaveOccurred())
		source = fixtureController()
		source.ProtoReflect().SetUnknown([]byte{0x78, 1})
		_, err = Compile(source)
		Expect(err).To(HaveOccurred())
		program, err := Compile(fixtureController())
		Expect(err).NotTo(HaveOccurred())
		program.Steering = []*pb.Instruction{{Opcode: pb.Opcode_OPCODE_ADD}}
		_, err = Evaluate(program, []int64{0, 0, 0, 0})
		Expect(err).To(HaveOccurred())
		program.Steering = []*pb.Instruction{{Opcode: pb.Opcode_OPCODE_CONSTANT}}
		for range MaxDepth {
			program.Steering = append(program.Steering, &pb.Instruction{Opcode: pb.Opcode_OPCODE_SCALE, Value: Scale})
		}
		_, err = Evaluate(program, []int64{0, 0, 0, 0})
		Expect(err).To(HaveOccurred())
	})
	It("does not expose mutable active program storage to the author", func() {
		runtime := NewRuntime()
		program, err := runtime.Activate(fixtureController(), 1)
		Expect(err).NotTo(HaveOccurred())
		program.Acceleration[0].Value = -Scale
		action := runtime.Step(&pb.Observation{Epoch: 1, Sequence: 1, Tick: 10, Features: []int64{0, 0, 0, 0}}, 10)
		Expect(action.Fallback).To(BeFalse())
		Expect(action.Acceleration).To(Equal(int64(250)))
	})
	It("retains the incumbent after rejection and rejects stale, replayed and wrong-epoch input", func() {
		runtime := NewRuntime()
		_, err := runtime.Activate(fixtureController(), 4)
		Expect(err).NotTo(HaveOccurred())
		invalid := fixtureController()
		invalid.SchemaVersion = 2
		_, err = runtime.Activate(invalid, 5)
		Expect(err).To(HaveOccurred())
		obs := &pb.Observation{Epoch: 4, Sequence: 2, Tick: 10, Features: []int64{0, 0, 0, 0}}
		Expect(runtime.Step(obs, 10).Fallback).To(BeFalse())
		Expect(runtime.Step(obs, 10).Reason).To(Equal("replayed_observation"))
		obs.Sequence = 3
		Expect(runtime.Step(obs, 14).Reason).To(Equal("stale_observation"))
		obs.Epoch = 5
		Expect(runtime.Step(obs, 10).Reason).To(Equal("wrong_epoch"))
		Expect(runtime.Reset(5)).To(Succeed())
		obs.Epoch = 5
		obs.Sequence = 1
		obs.Tick = 0
		Expect(runtime.Step(obs, 0).Fallback).To(BeFalse())
	})
	It("returns a typed error for a missing request", func() { Expect(NewRuntime().Handle(nil).Error).NotTo(BeEmpty()) })
})

var _ = Describe("immutable artifacts", func() {
	It("verifies content on read and never replaces an existing corrupt blob", func() {
		artifacts, err := NewArtifacts(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(artifacts.Close)
		hash, ref, err := artifacts.Put([]byte("retained evidence"))
		Expect(err).NotTo(HaveOccurred())
		_, _, err = artifacts.Put([]byte("retained evidence"))
		Expect(err).NotTo(HaveOccurred())
		Expect(artifacts.root.WriteFile(ref, []byte("tampered"), 0600)).To(Succeed())
		_, err = artifacts.Get(hash)
		Expect(err).To(HaveOccurred())
		_, _, err = artifacts.Put([]byte("retained evidence"))
		Expect(err).To(HaveOccurred())
		_, err = artifacts.Get("../outside")
		Expect(err).To(HaveOccurred())
	})
})
