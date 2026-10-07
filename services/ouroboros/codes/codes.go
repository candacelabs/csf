// Copyright 2026 Candace Labs

// Package codes holds CSF's failure codes and codes complaints with them.
//
// A failure code is one recurring way the agent system fails. Each is declared
// once, in csf/compiler/language/architecture.csf, with the edge between the
// two components where it starts and the fault side that owns its repair;
// the language generator projects the declarations to [Catalogue]. MAST's 14
// modes are the seed (Cemri et al., arXiv:2503.13657); a code CSF induces
// from complaints the seed does not fit names the mode it refines, as
// AdaMAST induces its codes from traces (arXiv:2607.16387). The edge and side
// follow the interaction-centric taxonomy (arXiv:2607.28802).
//
// A complaint is an operator correction: an operator-authored message that
// pkg/affect reads as flagging the agent's mistake. [ComplaintCoder] asks a
// local model behind the brain contract to code each one, and keeps a code
// only when the model's quote from the complaint is in it verbatim; a
// complaint no code fits comes back with the model's proposal for a new code.
// [Tally] counts the coded complaints per code, edge and side. Nothing here
// reaches a session: the codes are structure, read by the views and by
// whoever decides which gate or miner to build next.
package codes

import (
	"slices"
	"strings"

	"github.com/candacelabs/csf/pkg/collections"
)

// Side is a failure code's fault side: who owns its repair.
type Side string

// The fault sides, spelled as architecture.csf spells them.
const (
	SideHarness            Side = "harness"
	SideHarnessEnvironment Side = "harness_environment"
	SideEnvironment        Side = "environment"
	SideProvider           Side = "provider"
	SideModel              Side = "model"
)

// Sides is every fault side, in the order a tally reports them.
var Sides = []Side{SideHarness, SideHarnessEnvironment, SideEnvironment, SideProvider, SideModel}

// routes is the repair each side's codes are routed to (#417).
var routes = map[Side]string{
	SideHarness:            "a session gate or a miner",
	SideHarnessEnvironment: "a migration or an infrastructure slice",
	SideEnvironment:        "capacity, not a gate",
	SideProvider:           "capacity, not a gate",
	SideModel:              "the turn executor's model choice",
}

// Route is the repair a code on this side is routed to; empty for a side
// that is not one of [Sides].
func (side Side) Route() string { return routes[side] }

// Edge is the pair of components, two ontology terms, between which a
// failure starts.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// String is the edge as a tally labels it: from→to.
func (edge Edge) String() string { return edge.From + "→" + edge.To }

// Code is one failure code as architecture.csf declares it.
type Code struct {
	ID         string
	Name       string
	Definition string
	Edge       Edge
	Side       Side
	// MAST is the MAST mode the code refines, FM-<category>.<mode>; empty for
	// an induced code that refines none.
	MAST string
	// Miner is the directory of the miner whose rule detects the code; empty
	// until one is accepted.
	Miner string
}

// seedPrefix starts the identifier of every seed code, one of MAST's modes
// as architecture.csf declares it.
const seedPrefix = "mast_"

// Seed is the catalogue's seed codes, MAST's 14 modes, without the codes
// induced since: the hand-crafted reference vocabulary an induced catalogue
// is measured against.
func Seed() []Code {
	seed := []Code{}
	for _, code := range Catalogue {
		if strings.HasPrefix(code.ID, seedPrefix) {
			seed = append(seed, code)
		}
	}
	return seed
}

// Lookup is the catalogue's code with id.
func Lookup(id string) (Code, bool) {
	return collections.NewKeyedList(func(code Code) string { return code.ID }, Catalogue).Get(id)
}

// Components is every ontology term a code's edge may name, with the meaning
// a coder is told: the endpoints the catalogue already uses and the parts of
// the system a complaint can be about.
var Components = map[string]string{
	"human":         "the operator",
	"agent":         "the agent doing the work in a session",
	"model":         "the large model behind the agent",
	"harness":       "the CSF harness that runs sessions, its verbs and its recipes",
	"session_gate":  "a structural check the harness installs into every session",
	"assignment":    "the work an agent was given: ticket, recipe and instructions",
	"context":       "what the agent knows in a session: conversation, memory, rulings",
	"session":       "one agent conversation and its lifecycle",
	"relay":         "messages between agents and sessions",
	"turn_executor": "the CLI that runs the agent's turns (Claude Code, Copilot)",
	"host":          "the machine and its processes, files and caches",
	"infra":         "deployment and infrastructure outside the harness",
	"worktree":      "the session's git checkout and branch",
	"miner":         "an ouroboros rule that detects a failure in records",
	"receipt":       "the links a run returns: branch, pull request, trace",
	"csfpg":         "CSF's PostgreSQL state",
	"vendor":        "an external provider or service CSF depends on",
}

// componentNames is [Components]' keys, sorted, as a schema enumerates them.
func componentNames() []string {
	names := make([]string, 0, len(Components))
	for name := range Components {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// normalName folds a proposed code name so proposals that differ only in
// case, spacing or punctuation count together.
func normalName(name string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(name), func(character rune) bool {
		return !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9')
	}), "-")
}
