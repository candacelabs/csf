// Copyright 2026 Candace Labs

package sessiongate

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/candacelabs/csf/pkg/argv"
)

// Rule names a shell shape the wait gate rejects.
type Rule string

// The rules of the wait gate. Each is a way an agent waits that cannot end
// on its own: nothing joins a poll loop, a pgrep -f pattern matches the
// loop's own command line, and a foreground sleep holds the turn while
// waiting on nothing.
const (
	// RulePollLoop is a while, until or for loop whose body or condition
	// sleeps: a hand-rolled poll.
	RulePollLoop Rule = "poll_loop"
	// RuleSelfMatchingPgrep is pgrep -f (or --full): its pattern is matched
	// against whole command lines, including the shell that runs it.
	RuleSelfMatchingPgrep Rule = "pgrep_full"
	// RuleForegroundSleep is a sleep the turn waits for.
	RuleForegroundSleep Rule = "foreground_sleep"
)

// The replacement each rule's rejection names.
const (
	replacementBackground = "start the command with run_in_background and wait for its completion notification"
	replacementMonitor    = "wait for a condition with the Monitor tool"
	replacementOwnProcess = "track the process you started with run_in_background instead of searching command lines"
)

// ErrUnparsedCommand reports a command the shell parser could not read.
var ErrUnparsedCommand = errors.New("session gate: command is not valid shell")

// ShellFinding is one rejected shape in a command.
type ShellFinding struct {
	Rule Rule
	// Snippet is the offending source text.
	Snippet string
}

// Replacement is what to do instead, as the rejection message states it.
func (finding ShellFinding) Replacement() string {
	switch finding.Rule {
	case RuleSelfMatchingPgrep:
		return replacementOwnProcess
	default:
		return replacementBackground + ", or " + replacementMonitor
	}
}

// Message is the rejection text the agent reads.
func (finding ShellFinding) Message() string {
	return fmt.Sprintf("%s: %q is not allowed in a CSF session; %s", finding.Rule, finding.Snippet, finding.Replacement())
}

// Command names that run their argument as a shell script with -c.
var shells = map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true}

// Command names that run the rest of their arguments as a command.
var wrappers = map[string]bool{"command": true, "builtin": true, "exec": true, "nohup": true, "env": true, "time": true}

const (
	commandSleep = "sleep"
	commandPgrep = "pgrep"
	flagPrefix   = "-"
	assignment   = "="
)

var (
	// pgrepFull is pgrep's option to match whole command lines.
	pgrepFull = argv.Flag{Short: 'f', Long: "full"}
	// shellCommandString is a shell's option to run its first operand as a
	// script.
	shellCommandString = argv.Flag{Short: 'c'}
)

func parse(command string) (*syntax.File, error) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnparsedCommand, err)
	}
	return file, nil
}

// FindShellWaits parses command as bash and returns every shape the wait
// gate rejects, in source order. A literal script handed to sh -c or bash -c
// is parsed and checked the same way. Heredoc bodies and quoted text are
// data, not commands, and never match.
func FindShellWaits(command string) ([]ShellFinding, error) {
	file, err := parse(command)
	if err != nil {
		return nil, err
	}
	scan := &shellScan{source: command, loops: map[syntax.Node]bool{}}
	syntax.Walk(file, scan.visit)
	return scan.findings, nil
}

// shellScan walks one parsed script. The stack holds the nodes from the root
// to the one being visited, so a node's ancestors decide whether it is a
// background job or already part of a reported loop.
type shellScan struct {
	source   string
	stack    []syntax.Node
	loops    map[syntax.Node]bool
	findings []ShellFinding
}

func (scan *shellScan) visit(node syntax.Node) bool {
	if node == nil {
		scan.stack = scan.stack[:len(scan.stack)-1]
		return true
	}
	switch typed := node.(type) {
	case *syntax.WhileClause:
		scan.loop(typed, slices.Concat(typed.Cond, typed.Do))
	case *syntax.ForClause:
		scan.loop(typed, typed.Do)
	case *syntax.CallExpr:
		scan.call(typed)
	}
	scan.stack = append(scan.stack, node)
	return true
}

// loop reports a loop that sleeps in the foreground of its own body. A loop
// started as a job (&) is reported too: nothing ever joins it.
func (scan *shellScan) loop(node syntax.Node, body []*syntax.Stmt) {
	if !scan.inReportedLoop() && slices.ContainsFunc(body, sleeps) {
		scan.loops[node] = true
		scan.report(RulePollLoop, node)
	}
}

func (scan *shellScan) call(call *syntax.CallExpr) {
	name, arguments := commandWords(call)
	switch {
	case name == commandSleep:
		if !scan.inBackground() && !scan.inReportedLoop() {
			scan.report(RuleForegroundSleep, call)
		}
	case name == commandPgrep && argv.HasFlag(arguments, pgrepFull):
		scan.report(RuleSelfMatchingPgrep, call)
	case shells[name]:
		if script, ok := shellScript(arguments); ok {
			if nested, err := FindShellWaits(script); err == nil {
				scan.findings = append(scan.findings, nested...)
			}
		}
	}
}

// shellScript is the literal script of sh -c SCRIPT: the first operand.
func shellScript(arguments []string) (string, bool) {
	if !argv.HasFlag(arguments, shellCommandString) {
		return "", false
	}
	operand := slices.IndexFunc(arguments, isOperand)
	if operand < 0 {
		return "", false
	}
	return arguments[operand], true
}

func isOperand(argument string) bool { return !strings.HasPrefix(argument, flagPrefix) }

func (scan *shellScan) report(rule Rule, node syntax.Node) {
	start, end := int(node.Pos().Offset()), int(node.End().Offset())
	snippet := ""
	if start >= 0 && end <= len(scan.source) && start <= end {
		snippet = scan.source[start:end]
	}
	scan.findings = append(scan.findings, ShellFinding{Rule: rule, Snippet: strings.TrimSpace(snippet)})
}

// inBackground reports whether an enclosing statement runs as a job (&),
// which the turn does not wait for.
func (scan *shellScan) inBackground() bool {
	return slices.ContainsFunc(scan.stack, isBackgroundStatement)
}

func (scan *shellScan) inReportedLoop() bool {
	return slices.ContainsFunc(scan.stack, scan.isReportedLoop)
}

func (scan *shellScan) isReportedLoop(node syntax.Node) bool { return scan.loops[node] }

func isBackgroundStatement(node syntax.Node) bool {
	statement, ok := node.(*syntax.Stmt)
	return ok && (statement.Background || statement.Coprocess || statement.Disown)
}

// sleeps reports whether statement runs sleep in its foreground, anywhere
// inside it.
func sleeps(statement *syntax.Stmt) bool {
	found := false
	syntax.Walk(statement, func(node syntax.Node) bool {
		if found || isBackgroundStatement(node) {
			return false
		}
		if call, ok := node.(*syntax.CallExpr); ok {
			name, _ := commandWords(call)
			found = name == commandSleep
		}
		return !found
	})
	return found
}

// commandWords is the command a call runs and its literal arguments, past
// wrappers such as command, exec, env and nohup and their options and
// assignments. A word that is not literal text, such as "$x", is returned
// empty.
func commandWords(call *syntax.CallExpr) (string, []string) {
	words := make([]string, 0, len(call.Args))
	for _, word := range call.Args {
		words = append(words, literal(word))
	}
	for len(words) > 0 {
		if words[0] == "" {
			return "", words[1:]
		}
		name := path.Base(words[0])
		if !wrappers[name] {
			return name, words[1:]
		}
		next := slices.IndexFunc(words[1:], isWrappedCommand)
		if next < 0 {
			return "", nil
		}
		words = words[1+next:]
	}
	return "", nil
}

// isWrappedCommand reports a wrapper's argument that starts the wrapped
// command: neither one of the wrapper's options nor an assignment.
func isWrappedCommand(word string) bool {
	return isOperand(word) && !strings.Contains(word, assignment)
}

// literal is a word's text when it has no expansion: plain, single-quoted
// and double-quoted literal parts joined.
func literal(word *syntax.Word) string {
	var text strings.Builder
	for _, part := range word.Parts {
		switch typed := part.(type) {
		case *syntax.Lit:
			text.WriteString(typed.Value)
		case *syntax.SglQuoted:
			text.WriteString(typed.Value)
		case *syntax.DblQuoted:
			for _, inner := range typed.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return ""
				}
				text.WriteString(lit.Value)
			}
		default:
			return ""
		}
	}
	return text.String()
}

// Git's global options that take a separate value before the subcommand.
var gitValueOptions = map[string]bool{"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true}

const (
	commandGit       = "git"
	subcommandCommit = "commit"
	commandGh        = "gh"
	ghNounPR         = "pr"
	ghVerbReady      = "ready"
	ghUndo           = "--undo"
)

// RunsGitCommit reports whether command runs git commit anywhere in it,
// including inside sh -c. Whether the commit succeeded is for git to say.
func RunsGitCommit(command string) (bool, error) {
	return runsCommand(command, func(name string, arguments []string) bool {
		return name == commandGit && gitSubcommand(arguments) == subcommandCommit
	})
}

// RunsPullRequestReady reports whether command marks a pull request ready
// for review (gh pr ready, not gh pr ready --undo) anywhere in it, including
// inside sh -c.
func RunsPullRequestReady(command string) (bool, error) {
	return runsCommand(command, func(name string, arguments []string) bool {
		return name == commandGh && len(arguments) >= 2 && arguments[0] == ghNounPR &&
			arguments[1] == ghVerbReady && !slices.Contains(arguments, ghUndo)
	})
}

// runsCommand parses command as bash and reports whether any simple command
// in it, or in a literal script handed to sh -c, satisfies matches.
func runsCommand(command string, matches func(name string, arguments []string) bool) (bool, error) {
	file, err := parse(command)
	if err != nil {
		return false, err
	}
	found := false
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || found {
			return !found
		}
		name, arguments := commandWords(call)
		switch {
		case matches(name, arguments):
			found = true
		case shells[name]:
			if script, ok := shellScript(arguments); ok {
				found, _ = runsCommand(script, matches)
			}
		}
		return !found
	})
	return found, nil
}

// gitSubcommand is the first argument after git's global options, skipping
// the value each value-taking option consumes.
func gitSubcommand(arguments []string) string {
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch {
		case gitValueOptions[argument]:
			index++
		case !isOperand(argument):
		default:
			return argument
		}
	}
	return ""
}
