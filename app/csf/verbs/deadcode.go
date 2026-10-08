// Copyright 2026 Candace Labs

package verbs

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/candacelabs/csf/pkg/deadcode"
)

// csf deadcode [-fix] [-dir DIR] [PATTERN...]: every source function no main
// package or test reaches. Exported API of a library package is reported and
// kept; everything else dead is removable, -fix removes it until nothing is
// left, and the verb fails while any remains, which makes it the gate.
const (
	verbDeadcode     = "deadcode"
	deadcodeFixFlag  = "fix"
	deadcodeDirFlag  = "dir"
	deadcodeAll      = "./..."
	deadcodeRemove   = "removable"
	deadcodeKept     = "kept: exported API of a library package"
	deadcodeConforms = "kept: its type satisfies an interface with it"
	deadcodeRemoved  = "removed"
	deadcodeExitNote = "%d removable dead functions remain; run csf deadcode -fix"
)

var errDeadcodeRemains = errors.New("removable dead code remains")

func deadcodeVerb(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbDeadcode, flag.ContinueOnError)
	fix := flags.Bool(deadcodeFixFlag, false, "remove every removable dead function, until a pass removes nothing")
	dir := flags.String(deadcodeDirFlag, ".", "module directory the patterns are relative to")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	patterns := flags.Args()
	if len(patterns) == 0 {
		patterns = []string{deadcodeAll}
	}
	if *fix {
		removed, err := deadcode.Fix(*dir, patterns...)
		for _, function := range removed {
			fmt.Fprintf(output, "%s (%s)\n", function, deadcodeRemoved)
		}
		if err != nil {
			return err
		}
	}
	found, err := deadcode.Find(*dir, patterns...)
	if err != nil {
		return err
	}
	remaining := 0
	for _, function := range found {
		note := deadcodeKept
		if function.Satisfies {
			note = deadcodeConforms
		}
		if function.Removable() {
			note = deadcodeRemove
			remaining++
		}
		fmt.Fprintf(output, "%s (%s)\n", function, note)
	}
	if remaining > 0 {
		return fmt.Errorf("%w: "+deadcodeExitNote, errDeadcodeRemains, remaining)
	}
	return nil
}
