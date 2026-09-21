// Package bazel holds the legacy WORKSPACE shim: deps.bzl, the macro a
// consuming repository's WORKSPACE file loads, and versions.bzl, the toolchain
// pins that macro needs. Neither is Go, and neither is read by anything in this
// module — this file is here only to gate them.
//
// versions.bzl is a copy. The pins it publishes are owned by MODULE.bazel and
// .bazelversion, which Starlark cannot read, so the copy exists for the one
// audience that has no other source: a WORKSPACE consumer. An ungated copy of a
// version pin goes stale silently and then ships a wrong answer to exactly that
// audience, so this test compares the two on every run of both batteries — the
// plain `go test ./...` one and `bazel test //...`.
//
// The same file is the home for the other copied version in this module:
// MODULE.bazel fetches github.com/pganalyze/pg_query_go/v6 by hand, so its
// version is written twice, here and in go.mod.
package bazel

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// moduleRoot walks up from the working directory to the directory holding
// MODULE.bazel. `go test` starts in the package directory and Bazel starts in
// its runfiles tree, so neither a relative path nor a runfiles helper is
// portable between them; the file being looked for is.
func moduleRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolving the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "MODULE.bazel")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("no MODULE.bazel above the working directory")
		}
		directory = parent
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(content)
}

// capture returns the single submatch of pattern in content, failing when the
// pattern does not match exactly once: a pin that is absent and a pin that is
// declared twice are both drift.
func capture(t *testing.T, content, description, pattern string) string {
	t.Helper()
	matches := regexp.MustCompile(pattern).FindAllStringSubmatch(content, -1)
	if len(matches) != 1 {
		t.Fatalf("expected exactly one %s, found %d", description, len(matches))
	}
	return matches[0][1]
}

func TestVersionsMirrorTheFilesThatOwnThem(t *testing.T) {
	root := moduleRoot(t)
	versions := read(t, filepath.Join(root, "bazel", "versions.bzl"))
	module := read(t, filepath.Join(root, "MODULE.bazel"))
	bazelVersion := strings.TrimSpace(read(t, filepath.Join(root, ".bazelversion")))

	for _, pin := range []struct {
		constant string
		owner    string
		want     string
	}{
		{
			constant: "BAZEL_VERSION",
			owner:    ".bazelversion",
			want:     bazelVersion,
		},
		{
			constant: "RULES_GO_VERSION",
			owner:    "the rules_go bazel_dep in MODULE.bazel",
			want: capture(t, module, "rules_go bazel_dep",
				`(?m)^bazel_dep\(name = "rules_go", version = "([^"]+)"\)`),
		},
		{
			constant: "GAZELLE_VERSION",
			owner:    "the gazelle bazel_dep in MODULE.bazel",
			want: capture(t, module, "gazelle bazel_dep",
				`(?m)^bazel_dep\(name = "gazelle", version = "([^"]+)"\)`),
		},
		{
			constant: "GO_SDK_VERSION",
			owner:    "the go_sdk.download version in MODULE.bazel",
			// The call is written on one line or several depending on how
			// many arguments it carries, so the version is captured from
			// inside the call rather than from a fixed line shape.
			want: capture(t,
				capture(t, module, "go_sdk.download call", `(?s)go_sdk\.download\((.*?)\)`),
				"go_sdk.download version", `version = "([^"]+)"`),
		},
	} {
		got := capture(t, versions, pin.constant+" in versions.bzl",
			`(?m)^`+pin.constant+` = "([^"]+)"$`)
		if got != pin.want {
			t.Errorf(
				"bazel/versions.bzl says %s = %q, but %s says %q; update the copy",
				pin.constant, got, pin.owner, pin.want,
			)
		}
	}
}

// TestPatchedGoDependencyMatchesGoMod holds the one Go dependency this module
// fetches for itself.
//
// MODULE.bazel cannot ask Gazelle to patch github.com/pganalyze/pg_query_go/v6
// — Gazelle forbids a non-root module from overriding a Go dependency, and a
// consumer's build makes this module non-root — so the archive is fetched
// directly from the Go module proxy with the packaging patched in. That writes
// the version down a second time, next to go.mod's, where a `go get` would
// leave the two disagreeing and the Bazel build compiling a different version
// of a SQL parser than the `go test` build.
func TestPatchedGoDependencyMatchesGoMod(t *testing.T) {
	const modulePath = "github.com/pganalyze/pg_query_go/v6"

	root := moduleRoot(t)
	module := read(t, filepath.Join(root, "MODULE.bazel"))
	goMod := read(t, filepath.Join(root, "go.mod"))

	required := capture(t, goMod, "pg_query_go requirement in go.mod",
		`(?m)^\s+`+regexp.QuoteMeta(modulePath)+` (v[^\s]+)$`)
	archive := capture(t, module, "pg_query_go http_archive",
		`(?s)http_archive\(\n\s+name = "pg_query_go",(.*?)\n\)`)

	// The proxy names the version in both the URL and the directory the zip
	// unpacks into, and getting either wrong is a different failure, so both
	// are checked rather than one.
	for _, field := range []struct {
		name string
		want string
	}{
		{
			name: "strip_prefix",
			want: modulePath + "@" + required,
		},
		{
			name: "urls",
			want: "https://proxy.golang.org/" + modulePath + "/@v/" + required + ".zip",
		},
	} {
		got := capture(t, archive, field.name+" in the pg_query_go http_archive",
			field.name+` = \[?"([^"]+)"`)
		if got != field.want {
			t.Errorf(
				"MODULE.bazel fetches pg_query_go with %s %q, but go.mod requires %s; "+
					"update MODULE.bazel and its integrity hash",
				field.name, got, required,
			)
		}
	}
}
