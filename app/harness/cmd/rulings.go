// Copyright 2026 Candace Labs

package main

import (
	"flag"
	"io"

	"github.com/candacelabs/csf/services/harness/session"
)

// verbRulings prints the human-readable view of the operator rulings in
// force, generated from the ruling records under the state directory.
const verbRulings = "rulings"

// rulingsVerb writes the rulings view.
func rulingsVerb(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbRulings, flag.ContinueOnError)
	stateDirectory := flags.String(stateFlag, "", "state directory (default ~/"+defaultStateDirectory+")")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errUsage
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	rulings, err := session.RulingsInForce(state)
	if err != nil {
		return err
	}
	return session.WriteRulingsView(output, rulings)
}
