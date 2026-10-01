package clientcodec

import (
	"os"
	"path/filepath"
)

// Artifact is one generated file: a path relative to the output directory and
// its exact bytes.
type Artifact struct {
	// Path is relative to the output directory and is part of the generated
	// output: gen.sh compares it, so a renamed artifact is a diff rather than
	// a silently orphaned file.
	Path string

	// Data is the file's exact bytes. Generating into memory is what makes the
	// determinism spec cheap: it generates twice and compares these.
	Data []byte
}

// Generate produces every artifact from a descriptor set, without writing
// anything. Returning the bytes rather than writing them is what makes the
// determinism spec cheap — it can generate twice and compare, with no
// temporary directories and no filesystem in the assertion.
func Generate(descriptorSetPath string) ([]Artifact, error) {
	s, err := Load(descriptorSetPath)
	if err != nil {
		return nil, err
	}

	golden, err := EmitGolden()
	if err != nil {
		return nil, err
	}

	return []Artifact{
		{Path: "codec.gen.js", Data: EmitCodec(s)},
		{Path: "predicates.manifest.txt", Data: EmitManifest(s)},
		{Path: filepath.Join("test", "golden.json"), Data: golden},
	}, nil
}

// Write writes artifacts under dir, creating directories as needed.
func Write(dir string, artifacts []Artifact) error {
	for _, a := range artifacts {
		p := filepath.Join(dir, a.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, a.Data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
