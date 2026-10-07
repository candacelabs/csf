// Copyright 2026 Candace Labs

package evaluate

import (
	"errors"
	stdfs "io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/services/harness/session"
)

// titleTicket reads the ticket a merged pull request names in its title,
// "SLICE (#402): ...", the form every slice title takes.
var titleTicket = regexp.MustCompile(`\(#([0-9]+)\)`)

// MergedPull is one merged pull request as the pool reads it: its title,
// merge commit, the commit it started from (the merge commit's first
// parent) and the files it changed.
type MergedPull struct {
	Number      int64
	Title       string
	MergeCommit string
	BaseCommit  string
	Files       []string
}

// TicketOfTitle reads the ticket a pull request title names, 0 for none.
func TicketOfTitle(title string) int64 {
	match := titleTicket.FindStringSubmatch(title)
	if match == nil {
		return 0
	}
	number, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0
	}
	return number
}

// Pool is every past ticket a suite may hold: a merged pull request that
// names its ticket, and a run under corpus that worked the ticket and left
// its recipe, the one with the most tool calls being the original. A
// ticket closed by several pull requests is held once, by its last.
func Pool(corpus iofs.IFiles, pulls []MergedPull) ([]Ticket, error) {
	byTicket := map[int64]MergedPull{}
	for _, pull := range pulls {
		ticket := TicketOfTitle(pull.Title)
		if ticket == 0 || pull.BaseCommit == "" {
			continue
		}
		if held, seen := byTicket[ticket]; !seen || pull.Number > held.Number {
			byTicket[ticket] = pull
		}
	}
	entries, err := corpus.ReadDir(rootName)
	if err != nil {
		return nil, err
	}
	originals := map[int64]Ticket{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ticket, err := TicketOf(corpus, entry.Name())
		if err != nil {
			return nil, err
		}
		if _, merged := byTicket[ticket]; !merged {
			continue
		}
		events, err := corpus.ReadFile(path.Join(entry.Name(), session.EventsFile))
		if errors.Is(err, stdfs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		calls, episodes := Read(events, 0)
		if held, seen := originals[ticket]; seen && held.ToolCalls >= calls {
			continue
		}
		originals[ticket] = Ticket{Number: ticket, Assignment: entry.Name(), ToolCalls: calls, Episodes: episodes}
	}
	pool := make([]Ticket, 0, len(originals))
	for number, original := range originals {
		pull := byTicket[number]
		original.PullRequest, original.MergeCommit, original.BaseCommit = pull.Number, pull.MergeCommit, pull.BaseCommit
		original.Files = slices.Sorted(slices.Values(pull.Files))
		pool = append(pool, original)
	}
	slices.SortFunc(pool, func(a, b Ticket) int { return int(a.Number - b.Number) })
	return pool, nil
}
