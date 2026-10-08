// Copyright 2026 Candace Labs

package verbs

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/candacelabs/csf/io/ipc/proc"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/services/ouroboros"
)

// The ouroboros verbs: the mining loop's pre-check run once, by hand, over
// the open mining tickets or the ones named, printing one row per ticket.
// It is the same check the loop runs before it pays a model, so a ticket's
// verdict can be read, and replayed, without the loop.
const (
	verbOuroboros   = "ouroboros"
	verbPrecheck    = "precheck"
	repositoryFlag  = "repository"
	ticketFlag      = "ticket"
	precheckHeader  = "ticket\tminer\tcorpus\tverdict\treason"
	precheckFormat  = "%d\t%s\t%s\t%s\t%s\n"
	noProposal      = "-"
	noProposalText  = "no proposal"
	tabwriterIndent = 2
)

var errUsageOuroboros = errors.New("usage: csf ouroboros precheck -repository DIR [-state DIR] [-ticket N]... | csf ouroboros codes -transcripts GLOB [flags]")

// ouroborosVerbs runs one ouroboros verb.
func ouroborosVerbs(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer) error {
	if len(arguments) == 0 {
		return errUsageOuroboros
	}
	switch arguments[0] {
	case verbPrecheck:
		return precheck(ctx, launcher, arguments[1:], output)
	case verbCodes:
		return codesVerb(ctx, arguments[1:], output)
	}
	return errUsageOuroboros
}

// precheck prints the pre-check's verdict on the latest proposal of every
// open mining ticket, or of the tickets named with -ticket.
func precheck(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbOuroboros+" "+verbPrecheck, flag.ContinueOnError)
	var numbers listFlag
	repository := flags.String(repositoryFlag, "", "checkout whose origin holds the tickets and whose commits instances may name")
	stateDirectory := flags.String(stateFlag, "", "harness state directory, the corpus of runs (default ~/"+defaultStateDirectory+")")
	flags.Var(&numbers, ticketFlag, "ticket to check; repeat for several (default: every open mining ticket)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 || *repository == "" {
		return errUsageOuroboros
	}
	checker, tickets, err := newPrechecker(ctx, launcher, *stateDirectory, *repository)
	if err != nil {
		return err
	}
	selected, err := selectTickets(ctx, tickets, numbers)
	if err != nil {
		return err
	}
	table := tabwriter.NewWriter(output, 0, 0, tabwriterIndent, ' ', 0)
	fmt.Fprintln(table, precheckHeader)
	for _, ticket := range selected {
		comments, err := tickets.TicketComments(ctx, ticket.Number)
		if err != nil {
			return err
		}
		proposal, ok := ouroboros.LatestProposal(comments)
		if !ok {
			fmt.Fprintf(table, precheckFormat, ticket.Number, noProposal, noProposal, noProposal, noProposalText)
			continue
		}
		verdict, err := checker.Check(ctx, ticket.Number, proposal)
		if err != nil {
			return err
		}
		fmt.Fprintf(table, precheckFormat, ticket.Number, proposal.Miner, strings.Join(proposal.Corpus, ","), verdict.Verdict, verdict.Reason)
	}
	return table.Flush()
}

// newPrechecker grants the pre-check the corpus under the state directory
// and the tickets of the repository origin points at.
func newPrechecker(ctx context.Context, launcher *proc.HostLauncher, stateDirectory string, repository string) (*ouroboros.Prechecker, *ouroboros.GitHubTickets, error) {
	state, err := stateRoot(stateDirectory)
	if err != nil {
		return nil, nil, err
	}
	corpus, err := iofs.NewHostFiles(state)
	if err != nil {
		return nil, nil, err
	}
	repositoryFiles, err := iofs.NewHostFiles(repository)
	if err != nil {
		return nil, nil, err
	}
	origin, err := launcher.Run(ctx, proc.Command{Executable: gitExecutable, Arguments: []string{gitDirectory, repositoryFiles.Directory(), gitRemote, gitGetURL, gitOrigin}})
	if err != nil {
		return nil, nil, fmt.Errorf("read the repository's origin: %w", err)
	}
	slug, err := ouroboros.RepositorySlug(string(origin.Stdout))
	if err != nil {
		return nil, nil, err
	}
	tickets, err := ouroboros.NewGitHubTickets(launcher, slug)
	if err != nil {
		return nil, nil, err
	}
	checker, err := ouroboros.NewPrechecker(corpus, tickets, launcher, repositoryFiles.Directory())
	if err != nil {
		return nil, nil, err
	}
	return checker, tickets, nil
}

// selectTickets is every open mining ticket, or the named numbers.
func selectTickets(ctx context.Context, tickets *ouroboros.GitHubTickets, numbers listFlag) ([]ouroboros.Ticket, error) {
	if len(numbers) == 0 {
		return tickets.ListMiningTickets(ctx)
	}
	selected := make([]ouroboros.Ticket, 0, len(numbers))
	for _, text := range numbers {
		number, err := strconv.ParseInt(text, 10, 64)
		if err != nil || number <= 0 {
			return nil, fmt.Errorf("%w: -%s %q", errUsageOuroboros, ticketFlag, text)
		}
		selected = append(selected, ouroboros.Ticket{Number: number})
	}
	return selected, nil
}
