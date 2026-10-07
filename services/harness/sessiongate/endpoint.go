// Copyright 2026 Candace Labs

package sessiongate

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/candacelabs/csf/services/harness/endpoint"
)

// What the endpoint gate recognises: the commands that stop serving an
// operator-facing endpoint, and the operator's own retirement verb.
const (
	commandCSF     = "csf"
	commandHarness = "harness"
	csfVerbStop    = "stop"
	csfVerbView    = "view"
	csfVerbServe   = "serve"
	csfVerbRetire  = "retire"
	// csfNounEndpoint is the csf verb that holds the operator's retire.
	csfNounEndpoint = "endpoint"
	csfFlagStop     = "stop"

	commandKill    = "kill"
	commandPkill   = "pkill"
	commandKillall = "killall"
	commandDocker  = "docker"
	dockerObject   = "container"
)

// dockerRemovals are the docker subcommands that end a container's listener.
var dockerRemovals = map[string]bool{"stop": true, "rm": true, "kill": true}

// EndpointFinding is one command the endpoint gate refuses.
type EndpointFinding struct {
	// Snippet is the offending command's source text.
	Snippet string
	// Endpoints are the registered endpoints it would stop serving; empty for
	// a retirement, which stops nothing itself.
	Endpoints []endpoint.Endpoint
	// Retire marks csf endpoint retire, which records the operator's
	// acknowledgement and so is the operator's to run.
	Retire bool
}

// Message is the refusal the agent reads: the command, every endpoint it would
// stop with its users, and the two ways forward.
func (finding EndpointFinding) Message() string {
	if finding.Retire {
		return fmt.Sprintf("%q records the operator's acknowledgement that an address is retired, so only the operator runs it; "+
			"tell the operator which address moves where and ask them to run it", finding.Snippet)
	}
	described := make([]string, 0, len(finding.Endpoints))
	for _, served := range finding.Endpoints {
		described = append(described, served.Describe())
	}
	return fmt.Sprintf("%q would stop serving %s, and no retirement record exists for these addresses. "+
		"Restart in the same command instead (csf stop && csf serve ... -detach): csf serve refuses to start without every registered endpoint. "+
		"To move an address, tell the operator and ask them to run: csf endpoint retire -address HOST:PORT -moved-to HOST:PORT -acknowledgement '<their words>'",
		finding.Snippet, strings.Join(described, "; "))
}

// FindEndpointStops parses command as bash and returns every command in it,
// or in a literal script handed to sh -c, that stops serving an active
// endpoint of the registry: csf stop or csf view -stop without a csf serve in
// the same command, kill of the registered host process, pkill or killall
// naming csf, and docker stop, rm or kill of an endpoint's container. csf
// endpoint retire is returned too: it is the operator's.
func FindEndpointStops(command string, registry endpoint.Registry) ([]EndpointFinding, error) {
	file, err := parse(command)
	if err != nil {
		return nil, err
	}
	scan := &endpointScan{source: command, active: registry.Active(), pid: registry.HostPID}
	scan.walk(file, command)
	if scan.restarts {
		scan.findings = slices.DeleteFunc(scan.findings, func(finding scannedStop) bool { return finding.hostStop })
	}
	findings := make([]EndpointFinding, 0, len(scan.findings))
	for _, found := range scan.findings {
		findings = append(findings, found.EndpointFinding)
	}
	return findings, nil
}

// endpointScan walks one command and the literal scripts it hands to a
// shell, recording whether any part of it starts csf serve again.
type endpointScan struct {
	source   string
	active   []endpoint.Endpoint
	pid      int
	restarts bool
	findings []scannedStop
}

type scannedStop struct {
	EndpointFinding
	// hostStop marks a stop of csf serve itself, which a restart in the same
	// command answers.
	hostStop bool
}

func (scan *endpointScan) walk(file *syntax.File, source string) {
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		name, arguments := commandWords(call)
		if shells[name] {
			if script, ok := shellScript(arguments); ok {
				if nested, err := parse(script); err == nil {
					scan.walk(nested, script)
				}
			}
			return true
		}
		scan.call(name, arguments, snippet(source, call))
		return true
	})
}

func (scan *endpointScan) call(name string, arguments []string, text string) {
	operands := slices.DeleteFunc(slices.Clone(arguments), func(argument string) bool { return !isOperand(argument) })
	verb := ""
	if len(operands) > 0 {
		verb = operands[0]
	}
	csf := name == commandCSF || name == commandHarness
	switch {
	case csf && verb == csfVerbServe:
		scan.restarts = true
	case csf && verb == csfNounEndpoint && slices.Contains(operands, csfVerbRetire):
		scan.findings = append(scan.findings, scannedStop{EndpointFinding: EndpointFinding{Snippet: text, Retire: true}})
	case csf && (verb == csfVerbStop || (verb == csfVerbView && slices.ContainsFunc(arguments, isStopFlag))),
		name == commandKill && scan.pid > 0 && slices.Contains(operands, strconv.Itoa(scan.pid)),
		(name == commandPkill || name == commandKillall) && slices.ContainsFunc(operands, namesHost):
		scan.stop(text, true, func(served endpoint.Endpoint) bool { return served.Owner == endpoint.OwnerServe })
	case name == commandDocker && dockerRemoves(operands):
		scan.stop(text, false, func(served endpoint.Endpoint) bool {
			return served.Container != "" && slices.Contains(operands, served.Container)
		})
	}
}

// stop records a finding when the command stops any active endpoint matches
// selects.
func (scan *endpointScan) stop(text string, hostStop bool, matches func(served endpoint.Endpoint) bool) {
	stopped := slices.DeleteFunc(slices.Clone(scan.active), func(served endpoint.Endpoint) bool { return !matches(served) })
	if len(stopped) > 0 {
		scan.findings = append(scan.findings, scannedStop{EndpointFinding: EndpointFinding{Snippet: text, Endpoints: stopped}, hostStop: hostStop})
	}
}

// isStopFlag is Go's flag package spelling of -stop: one or two dashes, with
// or without a value.
func isStopFlag(argument string) bool {
	name := strings.TrimPrefix(strings.TrimPrefix(argument, flagPrefix), flagPrefix)
	name, _, _ = strings.Cut(name, assignment)
	return argument != name && name == csfFlagStop
}

// namesHost reports a pkill or killall pattern that matches csf serve.
func namesHost(pattern string) bool {
	return strings.Contains(pattern, commandCSF) || path.Base(pattern) == commandHarness
}

// dockerRemoves reports docker stop, rm or kill, also spelled docker
// container stop.
func dockerRemoves(operands []string) bool {
	if len(operands) > 1 && operands[0] == dockerObject {
		operands = operands[1:]
	}
	return len(operands) > 0 && dockerRemovals[operands[0]]
}

func snippet(source string, node syntax.Node) string {
	start, end := int(node.Pos().Offset()), int(node.End().Offset())
	if start < 0 || end > len(source) || start > end {
		return ""
	}
	return strings.TrimSpace(source[start:end])
}
