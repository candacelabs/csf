// Command widgetc validates widget documents and prints their findings.
//
// Go's internal import rule allows packages below pkg/widget to import
// pkg/widget/internal/uigen. This command is inside that tree, so the import
// is permitted even though widgetc and uigen are different packages. Packages
// outside pkg/widget cannot import uigen, including others in the same module.
//
// It is contributor tooling for this repository, not a product surface: a
// consumer of the widget package never runs it, and nothing outside this module
// depends on its flags, its output or its exit status. The library entry point
// is widget.Interpret, and a build system that needs validation should call
// that rather than shell out to this.
//
// Usage:
//
//	widgetc validate [-quiet] <file...>
//	widgetc generate -package <name> -out <dir> <file>
//
// Exit status is 0 when every document is clean, 1 when any finding was
// reported or a document could not be generated, and 2 when a document could
// not be read at all or the command line was wrong. The third is a failure too:
// an unrun check must never read as a pass.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/pkg/widget/internal/uigen"
)

const (
	exitClean       = 0
	exitFindings    = 1
	exitUnreadable  = 2
	commandValidate = "validate"
	commandGenerate = "generate"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the whole command, with its streams and exit status passed in so the
// suite drives it exactly as a shell does.
func run(arguments []string, output io.Writer, errorOutput io.Writer) int {
	if len(arguments) == 0 {
		writeUsage(errorOutput)
		return exitUnreadable
	}
	switch arguments[0] {
	case commandValidate:
		return runValidate(arguments[1:], output, errorOutput)
	case commandGenerate:
		return runGenerate(arguments[1:], output, errorOutput)
	default:
		writeUsage(errorOutput)
		return exitUnreadable
	}
}

func runValidate(arguments []string, output io.Writer, errorOutput io.Writer) int {
	flags := flag.NewFlagSet(commandValidate, flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	quiet := flags.Bool("quiet", false, "report findings through the exit status only, printing nothing")
	flags.Usage = func() { writeUsage(errorOutput) }
	if parseError := flags.Parse(arguments); parseError != nil {
		return exitUnreadable
	}
	paths := flags.Args()
	if len(paths) == 0 {
		writeUsage(errorOutput)
		return exitUnreadable
	}
	return validatePaths(paths, *quiet, output, errorOutput)
}

// runGenerate interprets one document and writes its widget.
//
// It refuses to generate from a document that reported anything, and prints the
// findings rather than only the refusal: generating from an unsound document is
// generating from a guess, and an author told "it did not generate" without
// being told why would go looking in the generator.
func runGenerate(arguments []string, output io.Writer, errorOutput io.Writer) int {
	flags := flag.NewFlagSet(commandGenerate, flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	packageName := flags.String("package", "", "the Go package name both emitted files declare")
	outputDirectory := flags.String("out", "", "the directory the emitted files are written to")
	flags.Usage = func() { writeUsage(errorOutput) }
	if parseError := flags.Parse(arguments); parseError != nil {
		return exitUnreadable
	}
	paths := flags.Args()
	if len(paths) != 1 || *packageName == "" || *outputDirectory == "" {
		writeUsage(errorOutput)
		return exitUnreadable
	}

	document, findings, readError := widget.InterpretFile(paths[0])
	if readError != nil {
		fmt.Fprintf(errorOutput, "widgetc: %v\n", readError)
		return exitUnreadable
	}
	if len(findings) > 0 {
		writeFindings(output, filepath.Base(paths[0]), findings)
		fmt.Fprintf(errorOutput, "widgetc: nothing generates before it validates\n")
		return exitFindings
	}

	artifacts, generateError := uigen.Generate(document, uigen.Options{Package: *packageName})
	if generateError != nil {
		fmt.Fprintf(errorOutput, "widgetc: %v\n", generateError)
		return exitFindings
	}
	if writeError := uigen.Write(*outputDirectory, artifacts); writeError != nil {
		fmt.Fprintf(errorOutput, "widgetc: %v\n", writeError)
		return exitUnreadable
	}
	for _, artifact := range artifacts {
		fmt.Fprintf(output, "%s\n", filepath.Join(*outputDirectory, artifact.Path))
	}
	return exitClean
}

func validatePaths(paths []string, quiet bool, output io.Writer, errorOutput io.Writer) int {
	status := exitClean
	for _, path := range paths {
		_, findings, readError := widget.InterpretFile(path)
		if readError != nil {
			fmt.Fprintf(errorOutput, "widgetc: %v\n", readError)
			status = exitUnreadable
			continue
		}
		if len(findings) == 0 {
			continue
		}
		if status != exitUnreadable {
			status = exitFindings
		}
		if quiet {
			continue
		}
		writeFindings(output, filepath.Base(path), findings)
	}
	return status
}

func writeFindings(output io.Writer, name string, findings []widget.Finding) {
	for _, finding := range findings {
		fmt.Fprint(output, finding.String())
	}
	fmt.Fprintf(output, "%s: %d %s\n", name, len(findings), plural("finding", len(findings)))
}

func plural(word string, count int) string {
	if count == 1 {
		return word
	}
	return word + "s"
}

func writeUsage(errorOutput io.Writer) {
	fmt.Fprint(errorOutput, `widgetc validates widget documents against the dialect this repository ships,
and generates the widget one document declares.

usage: widgetc validate [-quiet] <file...>
       widgetc generate -package <name> -out <dir> <file>

  -quiet     report findings through the exit status only, printing nothing
  -package   the Go package name both emitted files declare
  -out       the directory the emitted files are written to

exit status: 0 clean, 1 findings or a document that cannot be generated,
             2 a document could not be read, or this command line was wrong
`)
}
