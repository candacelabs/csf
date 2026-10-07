// Copyright 2026 Candace Labs

package evaluate

import (
	"encoding/json"
	"errors"
	stdfs "io/fs"
	"path"
	"regexp"
	"strconv"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/services/harness/session"
)

// rootName is the corpus directory itself, as io/fs names it.
const rootName = "."

// ticketNumber reads the issue number off a recipe's ticket address.
var ticketNumber = regexp.MustCompile(`/issues/([0-9]+)$`)

// recipeTicket is the part of a run's recipe.json the hiding reads.
type recipeTicket struct {
	TicketURL string `json:"ticketUrl"`
}

// TicketOf reads the ticket a run worked from its recipe, 0 when the run
// names none.
func TicketOf(corpus iofs.IFiles, assignment string) (int64, error) {
	content, err := corpus.ReadFile(path.Join(assignment, session.RecipeFile))
	if errors.Is(err, stdfs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var recipe recipeTicket
	if json.Unmarshal(content, &recipe) != nil {
		return 0, nil
	}
	match := ticketNumber.FindStringSubmatch(recipe.TicketURL)
	if match == nil {
		return 0, nil
	}
	return strconv.ParseInt(match[1], 10, 64)
}

// HiddenAssignments lists the run directories under corpus that worked a
// hidden ticket: the original runs of suite tickets and every replay of
// them. The corpus readers skip these, so a miner run never receives a
// suite ticket's event log.
func HiddenAssignments(corpus iofs.IFiles, hidden map[int64]bool) (map[string]bool, error) {
	entries, err := corpus.ReadDir(rootName)
	if err != nil {
		return nil, err
	}
	assignments := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ticket, err := TicketOf(corpus, entry.Name())
		if err != nil {
			return nil, err
		}
		if hidden[ticket] {
			assignments[entry.Name()] = true
		}
	}
	return assignments, nil
}
