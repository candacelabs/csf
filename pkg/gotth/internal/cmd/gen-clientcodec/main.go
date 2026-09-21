// Command gen-clientcodec generates the browser runtime's protobuf codec.
//
// The client encodes and decodes the same gotthlive.v1.Frame the server does,
// but it cannot afford a general protobuf runtime: the entire client budget is
// 12,288 bytes gzipped, and a general runtime would consume most of it on its
// own. So the codec is generated for this one fixed schema — a tag table and
// generic encode and decode paths that interpret it, plus skipping unknown
// tags by wire type so a newer server cannot break an older client.
//
// It is generated rather than hand-written because the bugs this code attracts
// are a wrong field number, a wrong wire type, or a field somebody forgot.
// This command reads the same FileDescriptorSet that drives the Go refinement
// generator, so the two sides cannot disagree about the wire.
//
// Alongside the codec it emits client/predicates.manifest.txt, which lists
// every predicate in the schema and states whether the client enforces it.
// Length bounds are enforced there, because the decoder already reads a length
// prefix and the check costs two comparisons; numeric ranges and regular
// expressions are not, because a regexp engine is not in the budget. That
// asymmetry is a generated artifact a reviewer can read rather than an
// unwritten assumption, and CI fails if it drifts from the descriptors.
//
// It also emits client/test/golden.json: frames encoded by the Go protobuf
// runtime, each with the value it must decode to. The JavaScript suite decodes
// them and re-encodes them, which is the only check that the two independent
// codecs agree about actual bytes rather than about intentions.
//
// This command is contributor tooling. Its output is committed, so consumers
// of this module never run it.
//
// Usage:
//
//	gen-clientcodec -descriptor_set <path> -out <dir>
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/candacelabs/csf/pkg/gotth/internal/clientcodec"
)

func main() {
	descriptorSet := flag.String("descriptor_set", "",
		"path to a FileDescriptorSet holding gotthlive/v1/frame.proto and its imports")
	out := flag.String("out", "", "directory to write the client artifacts into")
	flag.Parse()

	if *descriptorSet == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}

	artifacts, err := clientcodec.Generate(*descriptorSet)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen-clientcodec:", err)
		os.Exit(1)
	}
	if err := clientcodec.Write(*out, artifacts); err != nil {
		fmt.Fprintln(os.Stderr, "gen-clientcodec:", err)
		os.Exit(1)
	}

	for _, a := range artifacts {
		fmt.Println(filepath.Join(*out, a.Path))
	}
}
