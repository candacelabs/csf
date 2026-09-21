package gen

import (
	"fmt"
	"go/parser"
	"go/token"
	"testing"

	liquidv1 "github.com/candacelabs/csf/pkg/liquidproto/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestGenerator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Liquid Proto generator")
}

var _ = Describe("Liquid Proto generator", func() {
	It("emits a direct message validator", func() {
		digestOptions := new(descriptorpb.FieldOptions)
		proto.SetExtension(digestOptions, liquidv1.E_Field, &liquidv1.FieldRefinement{
			Expr: "len(this) == 32",
		})
		desiredStateOptions := new(descriptorpb.FieldOptions)
		proto.SetExtension(desiredStateOptions, liquidv1.E_Field, &liquidv1.FieldRefinement{
			Expr: "this == 1 || this == 2",
		})
		fixture := &descriptorpb.FileDescriptorProto{
			Name:       proto.String("liquid_bytes_fixture.proto"),
			Package:    proto.String("candace.liquid.fixture.v1"),
			Syntax:     proto.String("proto3"),
			Dependency: []string{"liquidproto/v1/refinement.proto"},
			Options: &descriptorpb.FileOptions{
				GoPackage: proto.String("example.com/liquidfixture;liquidfixture"),
			},
			EnumType: []*descriptorpb.EnumDescriptorProto{{
				Name: proto.String("DesiredState"),
				Value: []*descriptorpb.EnumValueDescriptorProto{
					{Name: proto.String("DESIRED_STATE_UNSPECIFIED"), Number: proto.Int32(0)},
					{Name: proto.String("DESIRED_STATE_RUNNING"), Number: proto.Int32(1)},
					{Name: proto.String("DESIRED_STATE_STOPPED"), Number: proto.Int32(2)},
				},
			}},
			MessageType: []*descriptorpb.DescriptorProto{{
				Name: proto.String("Blob"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:    proto.String("digest"),
						Number:  proto.Int32(1),
						Label:   descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:    descriptorpb.FieldDescriptorProto_TYPE_BYTES.Enum(),
						Options: digestOptions,
					},
					{
						Name:     proto.String("desired_state"),
						Number:   proto.Int32(2),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
						TypeName: proto.String(".candace.liquid.fixture.v1.DesiredState"),
						Options:  desiredStateOptions,
					},
				},
			}},
		}
		generated, err := generateFixture(fixture)
		Expect(err).NotTo(HaveOccurred())
		for _, want := range []string{
			"func ValidateBlob(message *Blob) error",
			"// ValidateBlob checks this message's annotated fields; it does not recurse.",
			"len(message.Digest) == 32",
			"message.DesiredState == 1 || message.DesiredState == 2",
			`Field:     "desired_state"`,
		} {
			Expect(generated).To(ContainSubstring(want))
		}
		Expect(generated).To(MatchRegexp(`Value:\s+message[.]Digest`))
		Expect(generated).To(MatchRegexp(`Value:\s+message[.]DesiredState`))
		for _, unwanted := range []string{"RefinedBlob", "MustBlob", "ToProto"} {
			Expect(generated).NotTo(ContainSubstring(unwanted))
		}

		_, err = parser.ParseFile(token.NewFileSet(), "liquid_bytes_fixture_liquid.pb.go", generated, parser.AllErrors)
		Expect(err).NotTo(HaveOccurred(), "generated source is not valid Go syntax:\n%s", generated)
	})

	It("emits deterministic string-map entry validation", func() {
		labelsOptions := new(descriptorpb.FieldOptions)
		proto.SetExtension(labelsOptions, liquidv1.E_Field, &liquidv1.FieldRefinement{
			MapKeyExpr:   "matches(this, `^[a-z]+$`)",
			MapValueExpr: "len(this) >= 1",
		})
		fixture := &descriptorpb.FileDescriptorProto{
			Name:       proto.String("liquid_map_fixture.proto"),
			Package:    proto.String("candace.liquid.fixture.v1"),
			Syntax:     proto.String("proto3"),
			Dependency: []string{"liquidproto/v1/refinement.proto"},
			Options: &descriptorpb.FileOptions{
				GoPackage: proto.String("example.com/liquidfixture;liquidfixture"),
			},
			MessageType: []*descriptorpb.DescriptorProto{{
				Name: proto.String("Node"),
				NestedType: []*descriptorpb.DescriptorProto{{
					Name:    proto.String("LabelsEntry"),
					Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
					Field: []*descriptorpb.FieldDescriptorProto{
						{
							Name: proto.String("key"), Number: proto.Int32(1),
							Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
							Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						},
						{
							Name: proto.String("value"), Number: proto.Int32(2),
							Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
							Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						},
					},
				}},
				Field: []*descriptorpb.FieldDescriptorProto{{
					Name: proto.String("labels"), Number: proto.Int32(1),
					Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
					Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
					TypeName: proto.String(".candace.liquid.fixture.v1.Node.LabelsEntry"),
					Options:  labelsOptions,
				}},
			}},
		}
		generated, err := generateFixture(fixture)
		Expect(err).NotTo(HaveOccurred())
		for _, want := range []string{
			"func ValidateNode(message *Node) error",
			"sort.Strings(_liquidNodeLabelsKeys)",
			"_liquidNodeLabelsKeyRe0.MatchString(_liquidNodeLabelsKey)",
			"len(_liquidNodeLabelsValue) >= 1",
			`Field:     "labels"`,
		} {
			Expect(generated).To(ContainSubstring(want))
		}
		_, err = parser.ParseFile(token.NewFileSet(), "liquid_map_fixture_liquid.pb.go", generated, parser.AllErrors)
		Expect(err).NotTo(HaveOccurred(), "generated source is not valid Go syntax:\n%s", generated)
	})

	It("emits deterministic opt-in nested validation", func() {
		endpointOptions := new(descriptorpb.FieldOptions)
		proto.SetExtension(endpointOptions, liquidv1.E_Field, &liquidv1.FieldRefinement{
			Expr: "len(this) >= 1",
		})
		configurationOptions := new(descriptorpb.FieldOptions)
		proto.SetExtension(configurationOptions, liquidv1.E_Field, &liquidv1.FieldRefinement{
			Recurse: true,
		})
		fixture := &descriptorpb.FileDescriptorProto{
			Name:       proto.String("liquid_nested_fixture.proto"),
			Package:    proto.String("candace.liquid.fixture.v1"),
			Syntax:     proto.String("proto3"),
			Dependency: []string{"liquidproto/v1/refinement.proto"},
			Options: &descriptorpb.FileOptions{
				GoPackage: proto.String("example.com/liquidfixture;liquidfixture"),
			},
			MessageType: []*descriptorpb.DescriptorProto{
				{
					Name: proto.String("Endpoint"),
					Field: []*descriptorpb.FieldDescriptorProto{{
						Name:    proto.String("url"),
						Number:  proto.Int32(1),
						Label:   descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:    descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						Options: endpointOptions,
					}},
				},
				{
					Name: proto.String("Configuration"),
					Field: []*descriptorpb.FieldDescriptorProto{{
						Name:     proto.String("endpoint"),
						Number:   proto.Int32(1),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
						TypeName: proto.String(".candace.liquid.fixture.v1.Endpoint"),
						Options:  configurationOptions,
					}},
				},
			},
		}

		first, err := generateFixture(fixture)
		Expect(err).NotTo(HaveOccurred())
		second, err := generateFixture(fixture)
		Expect(err).NotTo(HaveOccurred())
		Expect(first).To(Equal(second))
		for _, want := range []string{
			"func ValidateConfiguration(message *Configuration) error",
			"// ValidateConfiguration checks this message's annotated fields and opted-in nested messages.",
			"if message.Endpoint != nil {",
			"if err := ValidateEndpoint(message.Endpoint); err != nil {",
			"return err",
		} {
			Expect(first).To(ContainSubstring(want))
		}
		_, err = parser.ParseFile(token.NewFileSet(), "liquid_nested_fixture_liquid.pb.go", first, parser.AllErrors)
		Expect(err).NotTo(HaveOccurred(), "generated source is not valid Go syntax:\n%s", first)
	})

	It("rejects an opted-in nested-validation cycle", func() {
		leftOptions := new(descriptorpb.FieldOptions)
		proto.SetExtension(leftOptions, liquidv1.E_Field, &liquidv1.FieldRefinement{Recurse: true})
		rightOptions := new(descriptorpb.FieldOptions)
		proto.SetExtension(rightOptions, liquidv1.E_Field, &liquidv1.FieldRefinement{Recurse: true})
		fixture := &descriptorpb.FileDescriptorProto{
			Name:       proto.String("liquid_cycle_fixture.proto"),
			Package:    proto.String("candace.liquid.fixture.v1"),
			Syntax:     proto.String("proto3"),
			Dependency: []string{"liquidproto/v1/refinement.proto"},
			Options: &descriptorpb.FileOptions{
				GoPackage: proto.String("example.com/liquidfixture;liquidfixture"),
			},
			MessageType: []*descriptorpb.DescriptorProto{
				{
					Name: proto.String("Left"),
					Field: []*descriptorpb.FieldDescriptorProto{{
						Name:     proto.String("right"),
						Number:   proto.Int32(1),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
						TypeName: proto.String(".candace.liquid.fixture.v1.Right"),
						Options:  leftOptions,
					}},
				},
				{
					Name: proto.String("Right"),
					Field: []*descriptorpb.FieldDescriptorProto{{
						Name:     proto.String("left"),
						Number:   proto.Int32(1),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
						TypeName: proto.String(".candace.liquid.fixture.v1.Left"),
						Options:  rightOptions,
					}},
				},
			},
		}

		_, err := generateFixture(fixture)
		Expect(err).To(MatchError(ContainSubstring("nested validation forms a cycle")))
	})

	It("requires a nested-validation target to own a validator", func() {
		configurationOptions := new(descriptorpb.FieldOptions)
		proto.SetExtension(configurationOptions, liquidv1.E_Field, &liquidv1.FieldRefinement{Recurse: true})
		fixture := &descriptorpb.FileDescriptorProto{
			Name:       proto.String("liquid_missing_target_fixture.proto"),
			Package:    proto.String("candace.liquid.fixture.v1"),
			Syntax:     proto.String("proto3"),
			Dependency: []string{"liquidproto/v1/refinement.proto"},
			Options: &descriptorpb.FileOptions{
				GoPackage: proto.String("example.com/liquidfixture;liquidfixture"),
			},
			MessageType: []*descriptorpb.DescriptorProto{
				{Name: proto.String("Endpoint")},
				{
					Name: proto.String("Configuration"),
					Field: []*descriptorpb.FieldDescriptorProto{{
						Name:     proto.String("endpoint"),
						Number:   proto.Int32(1),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
						TypeName: proto.String(".candace.liquid.fixture.v1.Endpoint"),
						Options:  configurationOptions,
					}},
				},
			},
		}

		_, err := generateFixture(fixture)
		Expect(err).To(MatchError(ContainSubstring("has no Liquid Proto validator")))
	})
})

func generateFixture(fixture *descriptorpb.FileDescriptorProto) (string, error) {
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{fixture.GetName()},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			protodesc.ToFileDescriptorProto(descriptorpb.File_google_protobuf_descriptor_proto),
			protodesc.ToFileDescriptorProto(liquidv1.File_liquidproto_v1_refinement_proto),
			fixture,
		},
	}
	plugin, err := (protogen.Options{}).New(request)
	if err != nil {
		return "", err
	}
	if err := Run(plugin); err != nil {
		return "", err
	}
	files := plugin.Response().GetFile()
	if len(files) != 1 {
		return "", fmt.Errorf("expected one generated file, got %d", len(files))
	}
	return files[0].GetContent(), nil
}
