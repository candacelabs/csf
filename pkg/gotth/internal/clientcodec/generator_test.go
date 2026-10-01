package clientcodec_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/candacelabs/csf/pkg/gotth/internal/clientcodec"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	liquidv1 "github.com/candacelabs/csf/pkg/liquidproto/v1"
)

// descriptorSet writes the same FileDescriptorSet protoc would, built from the
// descriptors already linked into this binary.
//
// Building it here rather than shelling out to protoc is what keeps `go test`
// on a clean clone free of a code-generation toolchain (PRD FR-7, G11). It is
// the same content: protodesc round-trips the descriptor the generated code
// carries, which is the descriptor protoc emitted.
func descriptorSet() string {
	GinkgoHelper()
	set := &descriptorpb.FileDescriptorSet{
		// Dependencies first: refine.proto extends FieldOptions, so
		// descriptor.proto has to be resolvable, exactly as it is under
		// protoc --include_imports.
		File: []*descriptorpb.FileDescriptorProto{
			protodesc.ToFileDescriptorProto(descriptorpb.File_google_protobuf_descriptor_proto),
			protodesc.ToFileDescriptorProto(liquidv1.File_liquidproto_v1_refinement_proto),
			protodesc.ToFileDescriptorProto(pb.File_gotthlive_v1_frame_proto),
		},
	}
	raw, err := proto.Marshal(set)
	Expect(err).NotTo(HaveOccurred())

	path := filepath.Join(GinkgoT().TempDir(), "frame.descset")
	Expect(os.WriteFile(path, raw, 0o644)).To(Succeed())
	return path
}

func generate() map[string]string {
	GinkgoHelper()
	arts, err := clientcodec.Generate(descriptorSet())
	Expect(err).NotTo(HaveOccurred())

	out := map[string]string{}
	for _, a := range arts {
		out[a.Path] = string(a.Data)
	}
	return out
}

var _ = Describe("The client codec generator", func() {

	// FR-7's byte-reproducibility check covers the client codec as well as the
	// Go code, and a check that cannot be trusted to produce the same bytes
	// twice cannot detect drift. Asserted rather than assumed, because the
	// usual way this breaks is a map range nobody noticed.
	It("is deterministic", func() {
		first, second := generate(), generate()
		Expect(second).To(Equal(first))
	})

	Describe("the emitted schema table", func() {
		var table map[string]map[int32]string

		BeforeEach(func() {
			table = parseTable(generate()["codec.gen.js"])
		})

		// This is the spec that makes "the client cannot disagree with the
		// server about the wire" a checked property rather than a design
		// intention: it walks the real descriptors and demands a table row for
		// every field of every message, at the right number and the right wire
		// kind. A field added to the schema and forgotten in the client fails
		// here, and no reviewer has to notice it.
		It("carries every field of every message, at the descriptor's number and kind", func() {
			msgs := pb.File_gotthlive_v1_frame_proto.Messages()
			Expect(table).To(HaveLen(msgs.Len()))

			for i := 0; i < msgs.Len(); i++ {
				md := msgs.Get(i)
				fields := table[string(md.Name())]
				Expect(fields).NotTo(BeNil(), "message %s is missing from the table", md.Name())

				fds := md.Fields()
				Expect(fields).To(HaveLen(fds.Len()), "message %s has the wrong field count", md.Name())

				for j := 0; j < fds.Len(); j++ {
					fd := fds.Get(j)
					Expect(fields).To(HaveKeyWithValue(int32(fd.Number()), expectedKind(fd)),
						"%s.%s is missing or has the wrong wire kind", md.Name(), fd.Name())
				}
			}
		})

		It("gives the envelope's session identifier its exact 16-byte bound", func() {
			Expect(generate()["codec.gen.js"]).To(ContainSubstring("2,y,session_id,16:16"))
		})

		It("derives an inclusive range from a strict length inequality", func() {
			// len(this) > 0 && len(this) <= 64 is a 1..64 bound, not 0..64.
			Expect(generate()["codec.gen.js"]).To(ContainSubstring("1,s,fragment_id,1:64"))
		})

		It("leaves a field with no length predicate unbounded", func() {
			// Origin.event_id and client_ref carry no predicate at all, so
			// they must not acquire an argument the decoder would check.
			Expect(generate()["codec.gen.js"]).To(ContainSubstring("2,v,event_id;3,v,client_ref"))
		})
	})

	Describe("the predicate manifest", func() {
		var manifest string

		BeforeEach(func() { manifest = generate()["predicates.manifest.txt"] })

		// docs/protocol.md §10.3 promises the manifest lists EVERY predicate
		// in the schema. A predicate that never reached the manifest would
		// leave a reviewer believing the asymmetry is smaller than it is,
		// which is the specific failure the manifest exists to prevent.
		It("names every refined field in the schema", func() {
			for _, name := range refinedFields() {
				Expect(manifest).To(ContainSubstring(name), "%s has a predicate but no manifest row", name)
			}
		})

		It("marks length terms on text fields as enforced and everything else as not", func() {
			Expect(manifest).To(MatchRegexp(`Frame\.session_id\s+len\(this\) == 16\s+length\s+yes \(decode\)`))
			Expect(manifest).To(MatchRegexp(`Frame\.protocol_version\s+this > 0\s+numeric\s+no`))
			Expect(manifest).To(MatchRegexp(`matches\(this,.*\)\s+matches\s+no`))
		})

		// The claim itself. protocol.md §10.3 is explicit that docs may say
		// every byte the SERVER accepts crosses a generated boundary, and may
		// not say the protocol is typed end to end in both runtimes.
		It("states the claim the project is allowed to make, and refuses the stronger one", func() {
			Expect(manifest).To(ContainSubstring("every byte the SERVER"))
			Expect(manifest).To(ContainSubstring("NOT the claim"))
		})
	})

	// A manifest that claims to list every predicate must not be able to
	// under-report one. The generator therefore refuses a term it cannot
	// classify rather than emitting an "unknown" row somebody would read past.
	It("refuses a predicate it cannot classify, rather than silently omitting it", func() {
		frame := protodesc.ToFileDescriptorProto(pb.File_gotthlive_v1_frame_proto)

		var patched bool
		for _, m := range frame.MessageType {
			if m.GetName() != "Ack" {
				continue
			}
			opts := m.Field[0].Options
			proto.SetExtension(opts, liquidv1.E_Field,
				&liquidv1.FieldRefinement{Expr: "this > 0 && somethingNew(this)"})
			patched = true
		}
		Expect(patched).To(BeTrue())

		set := &descriptorpb.FileDescriptorSet{
			File: []*descriptorpb.FileDescriptorProto{
				protodesc.ToFileDescriptorProto(descriptorpb.File_google_protobuf_descriptor_proto),
				protodesc.ToFileDescriptorProto(liquidv1.File_liquidproto_v1_refinement_proto),
				frame,
			},
		}
		raw, err := proto.Marshal(set)
		Expect(err).NotTo(HaveOccurred())
		path := filepath.Join(GinkgoT().TempDir(), "patched.descset")
		Expect(os.WriteFile(path, raw, 0o644)).To(Succeed())

		_, err = clientcodec.Generate(path)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("somethingNew(this)"))
	})

	// The Go half of the cross-runtime round-trip. The JavaScript half
	// (client/test/codec.test.mjs) needs the bench image; this one needs
	// nothing, so the fixture the browser suite trusts is itself validated on
	// a clean clone with no node installed.
	Describe("the golden vectors", func() {
		It("all parse and re-marshal byte-identically", func() {
			raw, err := os.ReadFile(filepath.Join("..", "..", "client", "test", "golden.json"))
			Expect(err).NotTo(HaveOccurred())

			var vectors []clientcodec.Vector
			Expect(json.Unmarshal(raw, &vectors)).To(Succeed())
			Expect(len(vectors)).To(BeNumerically(">=", 10))

			for _, v := range vectors {
				b := mustHex(v.Hex)

				var f pb.Frame
				Expect(proto.Unmarshal(b, &f)).To(Succeed(), "vector %s does not parse", v.Name)

				// Everything except the deliberate unknown-tag vector must
				// survive a Go round trip unchanged, which is what makes byte
				// equality a meaningful assertion for the JS encoder too.
				if v.Reencode {
					out, err := proto.Marshal(&f)
					Expect(err).NotTo(HaveOccurred())
					Expect(out).To(Equal(b), "vector %s does not re-marshal identically", v.Name)
				}
			}
		})

		It("keeps a frame carrying unknown fields parseable, which is what FR-10 asks of both runtimes", func() {
			raw, err := os.ReadFile(filepath.Join("..", "..", "client", "test", "golden.json"))
			Expect(err).NotTo(HaveOccurred())

			var vectors []clientcodec.Vector
			Expect(json.Unmarshal(raw, &vectors)).To(Succeed())

			var found bool
			for _, v := range vectors {
				if v.Reencode {
					continue
				}
				found = true
				var f pb.Frame
				Expect(proto.Unmarshal(mustHex(v.Hex), &f)).To(Succeed())
				Expect(f.ProtoReflect().GetUnknown()).NotTo(BeEmpty(),
					"vector %s is meant to carry unknown fields", v.Name)
			}
			Expect(found).To(BeTrue(), "the fixture has no unknown-field vector")
		})
	})
})

func mustHex(s string) []byte {
	GinkgoHelper()
	b, err := hex.DecodeString(s)
	Expect(err).NotTo(HaveOccurred())
	return b
}

// expectedKind is the wire kind the table must record, derived independently
// of the generator so the spec is a second opinion rather than an echo.
func expectedKind(fd protoreflect.FieldDescriptor) string {
	switch {
	case fd.Kind() == protoreflect.MessageKind && fd.IsList():
		return "r"
	case fd.Kind() == protoreflect.MessageKind:
		return "m"
	case fd.Kind() == protoreflect.BoolKind:
		return "b"
	case fd.Kind() == protoreflect.StringKind:
		return "s"
	case fd.Kind() == protoreflect.BytesKind:
		return "y"
	case fd.IsList():
		return "p"
	default:
		return "v"
	}
}

// refinedFields walks the descriptors for every field carrying extension
// 51234, so the manifest spec is driven by the schema rather than by a list
// somebody has to remember to extend.
func refinedFields() []string {
	GinkgoHelper()
	var out []string
	msgs := pb.File_gotthlive_v1_frame_proto.Messages()
	for i := 0; i < msgs.Len(); i++ {
		md := msgs.Get(i)
		fds := md.Fields()
		for j := 0; j < fds.Len(); j++ {
			fd := fds.Get(j)
			opts, ok := fd.Options().(*descriptorpb.FieldOptions)
			if ok && opts != nil && proto.HasExtension(opts, liquidv1.E_Field) {
				out = append(out, string(md.Name())+"."+string(fd.Name()))
			}
		}
	}
	Expect(out).NotTo(BeEmpty())
	return out
}

// parseTable reads the generated schema table back out of the emitted
// JavaScript: message name -> field number -> wire kind.
func parseTable(js string) map[string]map[int32]string {
	GinkgoHelper()
	start := strings.Index(js, "var S = [")
	Expect(start).To(BeNumerically(">=", 0))
	end := strings.Index(js[start:], "\n];")
	Expect(end).To(BeNumerically(">=", 0))

	out := map[string]map[int32]string{}
	for _, line := range strings.Split(js[start:start+end], "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `"`) {
			continue
		}
		line = strings.TrimSuffix(strings.Trim(line, ","), `"`)[1:]

		name, body, ok := strings.Cut(line, "=")
		Expect(ok).To(BeTrue(), "malformed table line %q", line)

		fields := map[int32]string{}
		for _, f := range strings.Split(body, ";") {
			parts := strings.Split(f, ",")
			Expect(len(parts)).To(BeNumerically(">=", 3), "malformed field %q", f)
			n, err := strconv.Atoi(parts[0])
			Expect(err).NotTo(HaveOccurred())
			fields[int32(n)] = parts[1]
		}
		out[name] = fields
	}
	return out
}
