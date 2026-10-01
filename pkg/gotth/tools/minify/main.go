// Command minify builds the files the library serves, and measures them.
//
// It does two jobs that belong together because the second is only meaningful
// against the artifact the first produced:
//
//  1. Bundle client/runtime.js and its generated codec into one self-contained
//     IIFE, minified, and write it to live/clientjs/gotth-live.min.js — the
//     directory the public package embeds it from by exact filename. That file
//     is committed. A clean clone therefore needs no minifier, no node and no
//     network to serve the runtime (PRD FR-7, G11, NFR-5).
//
//     The dev inspector (client/inspector.js) is bundled the same way to
//     live/clientjs/gotth-live-inspector.min.js, and held to its own ceiling
//     of 40,960 bytes gzipped. It is a SECOND artifact rather than a region of
//     the first because NFR-8 requires it to be a separate opt-in file that
//     does not count against NFR-2 — so the two ceilings here are two
//     requirements, and neither number may be spent on the other's code.
//
//     Dev reload (client/dev-reload.js) is a THIRD artifact on the same terms,
//     to live/clientjs/gotth-live-dev-reload.min.js, with a ceiling of 8,192
//     bytes gzipped. FR-57 has no byte budget of its own in the PRD; the
//     ceiling exists anyway, because the reason the inspector got one applies
//     here unchanged — a dev-only file with no gate is a file that grows until
//     somebody measures it, and the number that must not move is NFR-2's.
//
//     It is emitted THERE and nowhere else. Keeping a second copy beside the
//     sources would create a two-copy equality invariant, and an invariant you
//     do not create is better than one somebody has to check (L9-1 addendum to
//     docs/reviews/module-init.md, 2026-08-04). client/ stays the source of
//     truth for the runtime source, the generated codec and the node tests,
//     and stays a directory with no Go file in it.
//
//  2. Measure it: gzip -9 over the minified bundle is the PRD NFR-2 gate at
//     12,288 bytes, and each //#region in the source gets a measured marginal
//     cost so NFR-3's per-subsystem ledger is a measurement rather than an
//     estimate.
//
// # How a subsystem is measured, and what the numbers do not mean
//
// Two columns, because neither alone is honest.
//
// MINIFIED B is the region minified on its own. It is exact, it is additive,
// and it says where the source actually went. It slightly over-counts against
// the bundle, because bundling renames identifiers across the whole file.
//
// MARGINAL GZIP B is gzip(baseline) - gzip(baseline with that region deleted):
// the compressed bytes the file would lose if the subsystem were not there.
// That is the number that answers "what is this costing me".
//
// The baseline for the marginals is built with tree shaking DISABLED, and the
// shipped artifact is built with it enabled. Both numbers are reported. The
// reason is that deleting a region also deletes the last use of things it
// referenced, so a tree-shaken variant measures the dead-code cascade rather
// than the region — the first draft of this tool reported a subsystem as
// costing more than the whole file, which is how the problem announced itself.
//
// Marginals do not sum to the total, and no arithmetic here pretends they do.
// DEFLATE shares matches between regions — a string literal in the transport
// region compresses better because the event region already used it — so the
// shared saving belongs to no single subsystem. The residual is reported as
// its own line rather than distributed, because distributing it would be an
// invention.
//
// # Why esbuild, and why in its own module
//
// The minifier is a contributor tool. It lives in candace/pkg/gotth/tools/ with its
// own go.mod so esbuild can never reach a consumer's build graph, which is the
// arrangement docs/dependencies.md §3 already settled. Node is not used and is
// not available: .dis/Dockerfile deliberately has no node, and this runs there.
//
// Usage:
//
//	go run ./minify            # build and measure both artifacts
//	go run ./minify -check     # fail if either committed artifact is stale
package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

const (
	entry     = "runtime.js"
	codec     = "codec.gen.js"
	inspector = "inspector.js"
	devReload = "dev-reload.js"

	// codecStub replaces the generated codec when measuring its marginal cost.
	// It keeps the bundle resolvable and parseable while carrying none of the
	// schema table or the encode/decode paths.
	codecStub = "export function decodeFrame(){}\nexport function encodeFrame(){}\n" +
		"export var PatchOp={},OriginKind={},ResyncReason={},ErrorCode={};\n"
)

func main() {
	client := flag.String("client", "../client", "the directory holding the runtime source and the generated codec")
	out := flag.String("out", "../live/clientjs/gotth-live.min.js",
		"the shipped artifact, embedded by the live package by exact filename")
	ceiling := flag.Int("ceiling", 12288, "the PRD NFR-2 gzip ceiling, in bytes")
	inspectorOut := flag.String("inspector-out", "../live/clientjs/gotth-live-inspector.min.js",
		"the dev inspector artifact, embedded by the live package by exact filename")
	inspectorCeiling := flag.Int("inspector-ceiling", 40960, "the PRD NFR-8 gzip ceiling for the inspector, in bytes")
	devReloadOut := flag.String("dev-reload-out", "../live/clientjs/gotth-live-dev-reload.min.js",
		"the dev-reload artifact, embedded by the live package by exact filename")
	devReloadCeiling := flag.Int("dev-reload-ceiling", 8192, "the gzip ceiling for the FR-57 dev-reload client, in bytes")
	check := flag.Bool("check", false, "do not write; fail if a committed artifact is stale")
	flag.Parse()

	dir, err := filepath.Abs(*client)
	if err != nil {
		die(err)
	}
	target, err := filepath.Abs(*out)
	if err != nil {
		die(err)
	}
	artifact := filepath.Base(target)

	inspectorTarget, err := filepath.Abs(*inspectorOut)
	if err != nil {
		die(err)
	}

	devReloadTarget, err := filepath.Abs(*devReloadOut)
	if err != nil {
		die(err)
	}

	full, err := build(dir, entry, api.TreeShakingDefault)
	if err != nil {
		die(err)
	}
	total := gz(full)
	emit(target, *out, full, *check)

	// The inspector is built and gated here rather than in a second tool
	// because a second tool is a second thing to remember to run, and NFR-8's
	// ceiling is worth no more than the frequency of its measurement. Tree
	// shaking is on, as it is for the shipped runtime: the inspector imports
	// the whole generated codec and uses the decoder and the four enums, so
	// the encoder is dead code in this bundle and should not be in it.
	insp, err := build(dir, inspector, api.TreeShakingDefault)
	if err != nil {
		die(err)
	}
	inspectorTotal := gz(insp)
	emit(inspectorTarget, *inspectorOut, insp, *check)

	// Dev reload, on the same terms. It imports nothing — not even the codec —
	// so tree shaking has nothing to remove; the setting is passed anyway so
	// that all three artifacts are built by one function under one policy,
	// rather than by three call sites that can drift.
	reload, err := build(dir, devReload, api.TreeShakingDefault)
	if err != nil {
		die(err)
	}
	devReloadTotal := gz(reload)
	emit(devReloadTarget, *devReloadOut, reload, *check)

	src, err := os.ReadFile(filepath.Join(dir, entry))
	if err != nil {
		die(err)
	}
	regions, imports, err := regionsOf(string(src))
	if err != nil {
		die(err)
	}

	// The marginal baseline. Tree shaking off, so that deleting a region
	// measures the region.
	base, err := build(dir, entry, api.TreeShakingFalse)
	if err != nil {
		die(err)
	}
	baseline := gz(base)

	fmt.Println("| Subsystem | Source B | Minified B | Marginal gzip B |")
	fmt.Println("|---|---:|---:|---:|")

	var marginals, minSum int
	for _, r := range regions {
		variant, err := buildWithout(dir, r.name, imports, regions)
		if err != nil {
			die(err)
		}
		marginal := baseline - gz(variant)
		marginals += marginal
		m := minified(r.source)
		minSum += m
		fmt.Printf("| %s | %d | %d | %d |\n", r.name, len(r.source), m, marginal)
	}

	// The codec is a whole generated file rather than a region of the entry,
	// so it is measured by swapping it for a stub.
	codecSrc, err := os.ReadFile(filepath.Join(dir, codec))
	if err != nil {
		die(err)
	}
	stubbed, err := buildStubbedCodec(dir)
	if err != nil {
		die(err)
	}
	codecMarginal := baseline - gz(stubbed)
	marginals += codecMarginal
	m := minified(string(codecSrc))
	minSum += m
	fmt.Printf("| codec (generated) | %d | %d | %d |\n", len(codecSrc), m, codecMarginal)

	fmt.Printf("| **shared / residual** | | %d | %d |\n", len(base)-minSum, baseline-marginals)
	fmt.Printf("| **Baseline (no tree shaking)** | | %d | %d |\n", len(base), baseline)
	fmt.Printf("| **Shipped %s** | | **%d** | **%d** |\n", artifact, len(full), total)
	fmt.Printf("\nceiling %d, headroom %d (%.1f%%)\n", *ceiling, *ceiling-total,
		100*float64(*ceiling-total)/float64(*ceiling))

	inspectorSrc, err := os.ReadFile(filepath.Join(dir, inspector))
	if err != nil {
		die(err)
	}
	fmt.Printf("\n| Dev inspector (NFR-8, not part of NFR-2) | Source B | Minified B | gzip B |\n")
	fmt.Println("|---|---:|---:|---:|")
	fmt.Printf("| %s | %d | %d | %d |\n", filepath.Base(inspectorTarget), len(inspectorSrc), len(insp), inspectorTotal)
	fmt.Printf("\ninspector ceiling %d, headroom %d (%.1f%%)\n", *inspectorCeiling, *inspectorCeiling-inspectorTotal,
		100*float64(*inspectorCeiling-inspectorTotal)/float64(*inspectorCeiling))

	devReloadSrc, err := os.ReadFile(filepath.Join(dir, devReload))
	if err != nil {
		die(err)
	}
	fmt.Printf("\n| Dev reload (FR-57, not part of NFR-2) | Source B | Minified B | gzip B |\n")
	fmt.Println("|---|---:|---:|---:|")
	fmt.Printf("| %s | %d | %d | %d |\n",
		filepath.Base(devReloadTarget), len(devReloadSrc), len(reload), devReloadTotal)
	fmt.Printf("\ndev-reload ceiling %d, headroom %d (%.1f%%)\n", *devReloadCeiling, *devReloadCeiling-devReloadTotal,
		100*float64(*devReloadCeiling-devReloadTotal)/float64(*devReloadCeiling))

	// Every gate is reported before any of them fails, so one over-budget
	// artifact does not hide another's number from the person who has to fix
	// it.
	if total > *ceiling {
		die(fmt.Errorf("gzip -9 of %s is %d bytes, over the %d-byte NFR-2 ceiling", artifact, total, *ceiling))
	}
	if inspectorTotal > *inspectorCeiling {
		die(fmt.Errorf("gzip -9 of %s is %d bytes, over the %d-byte NFR-8 ceiling",
			filepath.Base(inspectorTarget), inspectorTotal, *inspectorCeiling))
	}
	if devReloadTotal > *devReloadCeiling {
		die(fmt.Errorf("gzip -9 of %s is %d bytes, over the %d-byte FR-57 ceiling",
			filepath.Base(devReloadTarget), devReloadTotal, *devReloadCeiling))
	}
}

// emit writes an artifact, or — under -check — demands the committed bytes
// already equal the ones just built.
//
// That is the FR-7 staleness check. It is one function over both artifacts
// because two copies of it is how the second one ends up checking the first
// one's path, which is a green CI over a stale file.
func emit(target, shown string, content []byte, check bool) {
	if check {
		have, err := os.ReadFile(target)
		if err != nil {
			die(err)
		}
		if !bytes.Equal(have, content) {
			die(fmt.Errorf("%s is stale: rebuild it with `go run ./minify` and commit the result", shown))
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		die(err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		die(err)
	}
}

// build bundles one entry point in dir into a self-contained minified IIFE.
func build(dir, from string, shake api.TreeShaking) ([]byte, error) {
	r := api.Build(api.BuildOptions{
		EntryPoints:       []string{filepath.Join(dir, from)},
		Bundle:            true,
		Format:            api.FormatIIFE,
		Target:            api.ES2020,
		Charset:           api.CharsetUTF8,
		TreeShaking:       shake,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		LegalComments:     api.LegalCommentsNone,
		Write:             false,
	})
	if len(r.Errors) > 0 {
		// With the location: a bundler error that names only its text is a
		// message you then have to go and find, and the sources this builds
		// are thousands of lines long.
		e := r.Errors[0]
		if e.Location != nil {
			return nil, fmt.Errorf("%s:%d:%d: %s", e.Location.File, e.Location.Line, e.Location.Column, e.Text)
		}
		return nil, fmt.Errorf("%s", e.Text)
	}
	return r.OutputFiles[0].Contents, nil
}

// buildWithout rebuilds the bundle with one region's source removed.
//
// The variant does not have to run — it has to compress. Deleting a region
// leaves its callers referring to identifiers that no longer exist, which is a
// runtime error and not a parse error, so the bundle still builds and its
// compressed size is still the honest counterfactual.
func buildWithout(dir, drop, imports string, regions []region) ([]byte, error) {
	tmp, err := stage(dir)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	var kept []string
	for _, r := range regions {
		if r.name != drop {
			kept = append(kept, r.source)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, entry), []byte(imports+strings.Join(kept, "\n")), 0o644); err != nil {
		return nil, err
	}
	return build(tmp, entry, api.TreeShakingFalse)
}

func buildStubbedCodec(dir string) ([]byte, error) {
	tmp, err := stage(dir)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	if err := os.WriteFile(filepath.Join(tmp, codec), []byte(codecStub), 0o644); err != nil {
		return nil, err
	}
	return build(tmp, entry, api.TreeShakingFalse)
}

// stage copies the two source files into a scratch directory.
func stage(dir string) (string, error) {
	tmp, err := os.MkdirTemp("", "gotthlive-size-")
	if err != nil {
		return "", err
	}
	for _, f := range []string{entry, codec} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			os.RemoveAll(tmp)
			return "", err
		}
		if err := os.WriteFile(filepath.Join(tmp, f), b, 0o644); err != nil {
			os.RemoveAll(tmp)
			return "", err
		}
	}
	return tmp, nil
}

type region struct {
	name   string
	source string
}

// regionsOf splits the entry on //#region markers, and returns the import
// preamble separately so every variant build keeps it.
//
// Every line of code must be inside a region or be an import: a line the
// ledger does not account for is exactly the silent growth NFR-3 exists to
// catch, so an unmarked line is an error rather than a rounding difference. A
// region name may appear more than once; the parts are joined.
func regionsOf(src string) ([]region, string, error) {
	var (
		out     []region
		index   = map[string]int{}
		current = ""
		buf     []string
		imports []string
	)
	flush := func() {
		if current == "" {
			return
		}
		body := strings.Join(buf, "\n")
		if i, ok := index[current]; ok {
			out[i].source += "\n" + body
		} else {
			index[current] = len(out)
			out = append(out, region{name: current, source: body})
		}
		buf = nil
	}

	for n, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "//#region "):
			if current != "" {
				return nil, "", fmt.Errorf("line %d: nested //#region", n+1)
			}
			current = strings.TrimSpace(strings.TrimPrefix(t, "//#region "))
		case t == "//#endregion":
			if current == "" {
				return nil, "", fmt.Errorf("line %d: //#endregion outside a region", n+1)
			}
			flush()
			current = ""
		case current != "":
			buf = append(buf, line)
		case strings.HasPrefix(t, "import "):
			imports = append(imports, line)
		case t != "" && !strings.HasPrefix(t, "//"):
			return nil, "", fmt.Errorf("line %d is outside every //#region: %q", n+1, t)
		}
	}
	if current != "" {
		return nil, "", fmt.Errorf("unterminated //#region %s", current)
	}
	return out, strings.Join(imports, "\n") + "\n", nil
}

// minified is a region's size when minified on its own. It is exact and
// additive, unlike the gzip marginals, and it is the number that says where
// the source actually went.
func minified(src string) int {
	r := api.Transform(src, api.TransformOptions{
		Loader:            api.LoaderJS,
		Format:            api.FormatESModule,
		Target:            api.ES2020,
		Charset:           api.CharsetUTF8,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		LegalComments:     api.LegalCommentsNone,
	})
	if len(r.Errors) > 0 {
		return -1
	}
	return len(r.Code)
}

func gz(b []byte) int {
	var out bytes.Buffer
	w, _ := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if _, err := w.Write(b); err != nil {
		die(err)
	}
	if err := w.Close(); err != nil {
		die(err)
	}
	return out.Len()
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "minify:", err)
	os.Exit(1)
}
