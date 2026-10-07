// Copyright 2026 Candace Labs

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/harness/progress"
)

// The observe verbs read the one session_progress projection
// services/harness/progress builds from the state directory: csf status
// renders each session as one row, and csf tail renders the lines its event
// log folds to, labelled by agent and never as JSON. Both take the agents to
// show as arguments — a name per session, or all — and the tail takes the
// kinds to show and how many lines.
const (
	verbObserveStatus = "status"
	verbTail          = "tail"

	kindsFlag = "kinds"
	linesFlag = "n"

	// allAgents is the argument that keeps every session.
	allAgents = "all"
	// defaultTailLines is how many lines csf tail shows without -n.
	defaultTailLines = 50
	// activityFormat is how a row's last activity renders in the table: the
	// date matters when a session has been quiet since an earlier day.
	activityFormat = "01-02 15:04:05"
)

// statusVerb prints every session as one session_progress row. The arguments
// name the agents to show; none, or all, shows every one.
func statusVerb(ctx context.Context, launcher proc.ILauncher, arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbObserveStatus, flag.ContinueOnError)
	stateDirectory := flags.String(stateFlag, "", "state directory (default ~/"+defaultStateDirectory+")")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	keep := agentFilter(flags.Args())
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	files, err := iofs.NewHostFiles(state)
	if err != nil {
		return err
	}
	rows, err := progress.ReadAll(files)
	if err != nil {
		return err
	}
	table := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "AGENT\tASSIGNMENT\tPHASE\tTURN\tQUEUED\tTOOLS\tAHEAD\tCHANGED\tLAST\tNOTE")
	for index := range rows {
		row := &rows[index]
		if !keep(row.Agent) {
			continue
		}
		progress.Work(ctx, launcher, row)
		fmt.Fprintf(table, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%s\t%s\n",
			row.Agent, row.Assignment, row.Phase, row.Turn, row.Queued, row.ToolCalls,
			row.CommitsAhead, row.ChangedFiles, formatActivity(row.LastActivity), row.LastNote)
	}
	return table.Flush()
}

// tailVerb prints the last lines of every session's event log, readable and
// labelled by agent. The arguments name the agents to show; none, or all,
// shows every one. -kinds keeps only the named line kinds and -n sets how many
// lines are shown.
func tailVerb(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbTail, flag.ContinueOnError)
	stateDirectory := flags.String(stateFlag, "", "state directory (default ~/"+defaultStateDirectory+")")
	kinds := flags.String(kindsFlag, "", "line kinds to show, comma separated ("+kindList()+"); default all")
	lines := flags.Int(linesFlag, defaultTailLines, "how many lines to show")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *lines < 0 {
		return fmt.Errorf("-%s must not be negative", linesFlag)
	}
	keepAgent := agentFilter(flags.Args())
	keepKind, err := progress.ParseKinds(*kinds)
	if err != nil {
		return err
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	files, err := iofs.NewHostFiles(state)
	if err != nil {
		return err
	}
	projection, err := progress.ReadAllLines(files)
	if err != nil {
		return err
	}
	shown := []progress.Line{}
	for _, line := range projection.Lines {
		if keepAgent(line.Agent) && keepKind[line.Kind] {
			shown = append(shown, line)
		}
	}
	if len(shown) > *lines {
		shown = shown[len(shown)-*lines:]
	}
	for _, line := range shown {
		fmt.Fprintln(output, line)
	}
	return nil
}

// agentFilter reads the agents a verb shows into a predicate. No agent, or the
// argument all, keeps every session.
func agentFilter(names []string) func(agent string) bool {
	keep := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || name == allAgents {
			return func(agent string) bool { return true }
		}
		keep[name] = true
	}
	if len(keep) == 0 {
		return func(agent string) bool { return true }
	}
	return func(agent string) bool { return keep[agent] }
}

// kindList is every line kind csf tail accepts, spelled for a flag's help.
func kindList() string {
	names := make([]string, 0, len(progress.Kinds()))
	for _, kind := range progress.Kinds() {
		names = append(names, string(kind))
	}
	return strings.Join(names, ", ")
}

// formatActivity renders a row's last activity, and a dash for a session that
// has not run yet.
func formatActivity(at time.Time) string {
	if at.IsZero() {
		return "-"
	}
	return at.Format(activityFormat)
}
