// Copyright 2026 Candace Labs

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/pkg/atomicfile"
	"github.com/candacelabs/csf/services/harness/costs"
)

// The costs verb: the cost model of the session operations over every
// recorded run, written whole to <state>/costs.json for the hypervisor's
// parameters and the Workbench's series.
const (
	verbCosts    = "costs"
	ontologyFlag = "ontology"
	// procRoot is where the turn executors' memory is read.
	procRoot = "/proc"
)

// costsVerb builds the cost model and prints each chosen parameter.
func costsVerb(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbCosts, flag.ContinueOnError)
	stateDirectory := flags.String(stateFlag, "", "state directory (default ~/"+defaultStateDirectory+")")
	ontology := flags.String(ontologyFlag, "", "architecture.csf whose terms join the area signatures; none when empty")
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
	files, err := iofs.NewHostFiles(state)
	if err != nil {
		return err
	}
	runs, err := costs.ReadRuns(files)
	if err != nil {
		return err
	}
	var terms []costs.Term
	if *ontology != "" {
		source, err := os.Open(*ontology)
		if err != nil {
			return err
		}
		terms, err = costs.ReadTerms(source)
		_ = source.Close()
		if err != nil {
			return err
		}
	}
	proc, err := iofs.NewHostFiles(procRoot)
	if err != nil {
		return err
	}
	report := costs.Build(runs, costs.SampleResident(proc, costs.ExecutorCommand), terms, time.Now())
	content, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(report)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(filepath.Join(state, costs.ReportFile), content, hostFileMode); err != nil {
		return err
	}
	fmt.Fprintf(output, "%d runs over %d days; %s written\n", report.GetRuns(), report.GetDays(), costs.ReportFile)
	for _, parameter := range report.GetParameters() {
		fmt.Fprintf(output, "%s = %.0f %s (chosen %t): %s\n", parameter.GetName(), parameter.GetValue(), parameter.GetUnit(), parameter.GetChosen(), parameter.GetDerivation())
	}
	return nil
}
