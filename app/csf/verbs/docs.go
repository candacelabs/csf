// Copyright 2026 Candace Labs

package verbs

import (
	"context"
	"errors"
	"flag"
	"io"
	"path/filepath"

	"github.com/candacelabs/csf/io/ipc/proc"
)

// The docs verbs: link rewrites the ontology terms the README lint reports
// as unlinked into links to their definitions, with the language generator's
// link verb, in a checkout. With no paths it links every tracked README, the
// files the ontology alignment score measures.
const (
	verbDocs = "docs"
	verbLink = "link"

	bazelScript       = "tools/bazel.sh"
	generatorTarget   = "//csf/compiler/language:generate"
	generatorBinary   = "bazel-bin/csf/compiler/language/generate.exe"
	generatorRootFlag = "--root"
	bashExecutable    = "bash"
	bazelBuild        = "build"
)

var errUsageDocs = errors.New("usage: csf docs link [-repo DIR] [PATH...]")

// docsVerbs runs one docs verb.
func docsVerbs(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer, diagnostics io.Writer) error {
	if len(arguments) == 0 || arguments[0] != verbLink {
		return errUsageDocs
	}
	flags := flag.NewFlagSet(commandName+" "+verbDocs+" "+verbLink, flag.ContinueOnError)
	repository := flags.String(repoFlag, ".", "checkout whose READMEs are linked")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	root, err := filepath.Abs(*repository)
	if err != nil {
		return err
	}
	linkArguments := []string{generatorRootFlag, root, verbLink}
	for _, path := range flags.Args() {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		linkArguments = append(linkArguments, absolute)
	}
	// The generator is built by the checkout's own pinned Bazel, so the fixer
	// is the lint the checkout's gates run.
	if _, err := launcher.Run(ctx, proc.Command{Executable: bashExecutable, Arguments: []string{bazelScript, bazelBuild, generatorTarget},
		Directory: root, Stdout: diagnostics, Stderr: diagnostics}); err != nil {
		return err
	}
	_, err = launcher.Run(ctx, proc.Command{Executable: filepath.Join(root, generatorBinary), Arguments: linkArguments,
		Directory: root, Stdout: output, Stderr: diagnostics})
	return err
}
