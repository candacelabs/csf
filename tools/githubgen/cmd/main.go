// Copyright 2026 Candace Labs

// Command githubgen projects GitHub's OpenAPI description for CSF:
//
//	githubgen filter -allowlist FILE -description FILE -out FILE
//	githubgen tools  -description FILE -out FILE
//	githubgen verify -allowlist FILE -description FILE
//
// filter cuts the full description down to the allowlisted operations; tools
// writes the MCP tool registrations for the filtered description; verify
// reports a filtered description whose operations are not the allowlist.
package main

import (
	"flag"
	"fmt"
	"os"
	"slices"

	"github.com/candacelabs/csf/tools/githubgen"
)

const filePermissions = 0o644

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "githubgen:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return fmt.Errorf("usage: githubgen filter|tools|verify ...")
	}
	flags := flag.NewFlagSet(arguments[0], flag.ContinueOnError)
	allowlist := flags.String("allowlist", "", "operationIds, one per line")
	description := flags.String("description", "", "the OpenAPI description to read")
	out := flags.String("out", "", "the file to write")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	source, err := os.ReadFile(*description)
	if err != nil {
		return err
	}
	switch arguments[0] {
	case "filter":
		operations, err := readAllowlist(*allowlist)
		if err != nil {
			return err
		}
		filtered, err := githubgen.Filter(source, operations)
		if err != nil {
			return err
		}
		return os.WriteFile(*out, append(filtered, '\n'), filePermissions)
	case "tools":
		generated, err := githubgen.Tools(source)
		if err != nil {
			return err
		}
		return os.WriteFile(*out, generated, filePermissions)
	case "verify":
		operations, err := readAllowlist(*allowlist)
		if err != nil {
			return err
		}
		present, err := githubgen.Operations(source)
		if err != nil {
			return err
		}
		slices.Sort(operations)
		if !slices.Equal(operations, present) {
			return fmt.Errorf("the filtered description holds %v but the allowlist names %v; run generate.sh write", present, operations)
		}
		return nil
	}
	return fmt.Errorf("unknown verb %q", arguments[0])
}

func readAllowlist(path string) ([]string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return githubgen.ReadAllowlist(content), nil
}
