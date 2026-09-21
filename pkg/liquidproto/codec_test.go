package liquidproto_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/candacelabs/csf/pkg/liquidproto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestLiquidProto(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Liquid Proto runtime")
}

func structCodec() *liquidproto.Codec[*structpb.Struct] {
	GinkgoHelper()
	codec, err := liquidproto.NewCodec(
		func() *structpb.Struct { return new(structpb.Struct) },
		func(message *structpb.Struct) error {
			if _, ok := message.GetFields()["required"]; !ok {
				return errors.New("required field is missing")
			}
			return nil
		},
	)
	Expect(err).NotTo(HaveOccurred())
	return codec
}

var _ = Describe("Codec", func() {
	It("validates both boundaries and encodes deterministically", func() {
		codec := structCodec()
		first, err := structpb.NewStruct(map[string]any{"required": true, "z": 1.0, "a": "x"})
		Expect(err).NotTo(HaveOccurred())
		second, err := structpb.NewStruct(map[string]any{"a": "x", "z": 1.0, "required": true})
		Expect(err).NotTo(HaveOccurred())

		firstWire, err := codec.Marshal(first)
		Expect(err).NotTo(HaveOccurred())
		secondWire, err := codec.Marshal(second)
		Expect(err).NotTo(HaveOccurred())
		Expect(secondWire).To(Equal(firstWire))

		decoded, err := codec.Unmarshal(firstWire)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(decoded, first)).To(BeTrue())
	})

	It("rejects invalid values before marshal and after unmarshal", func() {
		codec := structCodec()
		invalid := new(structpb.Struct)
		_, err := codec.Marshal(invalid)
		Expect(err).To(HaveOccurred())

		hostileWire, err := proto.Marshal(invalid)
		Expect(err).NotTo(HaveOccurred())
		_, err = codec.Unmarshal(hostileWire)
		Expect(err).To(HaveOccurred())
	})

	It("preserves unknown fields", func() {
		codec := structCodec()
		message, err := structpb.NewStruct(map[string]any{"required": true})
		Expect(err).NotTo(HaveOccurred())
		wire, err := proto.Marshal(message)
		Expect(err).NotTo(HaveOccurred())
		unknown := []byte{0x98, 0x06, 0x2a}
		wire = append(wire, unknown...)

		decoded, err := codec.Unmarshal(wire)
		Expect(err).NotTo(HaveOccurred())
		Expect(bytes.Equal(decoded.ProtoReflect().GetUnknown(), unknown)).To(BeTrue())
		reencoded, err := codec.Marshal(decoded)
		Expect(err).NotTo(HaveOccurred())
		var roundTrip structpb.Struct
		Expect(proto.Unmarshal(reencoded, &roundTrip)).To(Succeed())
		Expect(bytes.Equal(roundTrip.ProtoReflect().GetUnknown(), unknown)).To(BeTrue())
	})

	It("reports invalid configuration and typed nil messages", func() {
		_, err := liquidproto.NewCodec[*structpb.Struct](nil, func(message *structpb.Struct) error { return nil })
		Expect(errors.Is(err, liquidproto.ErrNilConstructor)).To(BeTrue(), "error: %v", err)

		_, err = liquidproto.NewCodec(func() *structpb.Struct { return new(structpb.Struct) }, nil)
		Expect(errors.Is(err, liquidproto.ErrNilValidator)).To(BeTrue(), "error: %v", err)

		_, err = liquidproto.NewCodec(
			func() *structpb.Struct { return nil },
			func(message *structpb.Struct) error { return nil },
		)
		Expect(errors.Is(err, liquidproto.ErrNilMessage)).To(BeTrue(), "error: %v", err)

		codec := structCodec()
		_, err = codec.Marshal(nil)
		Expect(errors.Is(err, liquidproto.ErrNilMessage)).To(BeTrue(), "error: %v", err)
	})

	It("binds broad codecs to their constructor's concrete protobuf type", func() {
		codec, err := liquidproto.NewCodec[proto.Message](
			func() proto.Message { return new(wrapperspb.StringValue) },
			func(message proto.Message) error { return nil },
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(codec.MessageType()).To(Equal("google.protobuf.StringValue"))

		_, err = codec.Marshal(wrapperspb.Int32(42))
		Expect(errors.Is(err, liquidproto.ErrWrongMessageType)).To(BeTrue(), "error: %v", err)

		incompatibleFile, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
			Name:    proto.String("incompatible_string_value.proto"),
			Package: proto.String("google.protobuf"),
			Syntax:  proto.String("proto3"),
			MessageType: []*descriptorpb.DescriptorProto{{
				Name: proto.String("StringValue"),
				Field: []*descriptorpb.FieldDescriptorProto{{
					Name:   proto.String("value"),
					Number: proto.Int32(1),
					Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					Type:   descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
				}},
			}},
		}, nil)
		Expect(err).NotTo(HaveOccurred())
		incompatible := dynamicpb.NewMessage(incompatibleFile.Messages().ByName("StringValue"))
		Expect(incompatible.Descriptor().FullName()).To(Equal(wrapperspb.String("").ProtoReflect().Descriptor().FullName()))

		_, err = codec.Marshal(incompatible)
		Expect(errors.Is(err, liquidproto.ErrWrongMessageType)).To(BeTrue(), "error: %v", err)
	})
})
