// Copyright 2026 Candace Labs

package ouroboros

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdfs "io/fs"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/harness/session"
)

// A miner's files under its directory and the verbs of its executable, as
// the contract's main defines them.
const (
	minerExecutable   = "miner.exe"
	minerRules        = "rules.dl"
	minerLabels       = "fixtures/labels.tsv"
	minerFixturesGlob = "fixtures/*/events.jsonl"
	verbBacktest      = "backtest"
	verbFindings      = "findings"
	verbScope         = "scope"
	flagJSON          = "--json"
	flagKnee          = "--knee"
	// ScopeGeneric and ScopeTenant are the proto names of a miner's scope
	// (#120 amendment C): generic holds for any harness user and may be
	// shared from an opted-in tenant; tenant never leaves the tenant.
	ScopeGeneric = "SCOPE_GENERIC"
	ScopeTenant  = "SCOPE_TENANT"
	// maxFindingLineBytes bounds one finding's JSON line: a proof carries a
	// fact per premise, each with its source line, never the log itself.
	maxFindingLineBytes = 8 << 20
)

// Miner is one accepted miner: a directory under the repository's miners
// directory with its rules, and its built executable.
type Miner struct {
	Name       string
	Directory  string
	Executable string
	Built      bool
	// Scope is what the miner's findings encode, as its executable reports
	// it; empty for a miner that is not built.
	Scope string
}

// MinerStatus is what the snapshot shows about one miner.
type MinerStatus struct {
	Name     string `json:"name"`
	Built    bool   `json:"built"`
	Scope    string `json:"scope"`
	Knee     int64  `json:"knee"`
	HasKnee  bool   `json:"has_knee"`
	Findings int    `json:"findings"`
}

// minerFinding is the contract's Finding record as the executable prints it.
type minerFinding struct {
	Miner   string `json:"miner"`
	Rule    string `json:"rule"`
	Subject []struct {
		Text   string `json:"text"`
		Number *int64 `json:"number"`
	} `json:"subject"`
	Severity string `json:"severity"`
	Scope    string `json:"scope"`
}

// minerBacktest is the part of the contract's Backtest record the detector
// reads: the fitted knee.
type minerBacktest struct {
	Knee *int64 `json:"knee"`
}

// Miners lists the miners under the repository: every directory with a
// rules file, built or not, with the scope a built one reports.
func (loop *Loop) Miners(ctx context.Context) ([]Miner, error) {
	entries, err := loop.repository.files.ReadDir(MinersDirectory)
	if err != nil {
		return nil, fmt.Errorf("ouroboros: list miners: %w", err)
	}
	var miners []Miner
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := stdfs.Stat(loop.repository.files, path.Join(MinersDirectory, entry.Name(), minerRules)); err != nil {
			continue
		}
		executable := path.Join(MinerExecutables, entry.Name(), minerExecutable)
		_, err := stdfs.Stat(loop.repository.files, executable)
		miner := Miner{
			Name:       entry.Name(),
			Directory:  filepath.Join(loop.repository.directory, MinersDirectory, entry.Name()),
			Executable: filepath.Join(loop.repository.directory, executable),
			Built:      err == nil,
		}
		if miner.Built {
			result, err := loop.launcher.Run(ctx, proc.Command{Executable: miner.Executable, Arguments: []string{verbScope}, Directory: loop.repository.directory})
			if err != nil {
				return nil, fmt.Errorf("ouroboros: scope of %s: %w", miner.Name, err)
			}
			miner.Scope = strings.TrimSpace(string(result.Stdout))
		}
		miners = append(miners, miner)
	}
	slices.SortFunc(miners, func(a, b Miner) int { return strings.Compare(a.Name, b.Name) })
	return miners, nil
}

// knee fits the miner's threshold on its own labeled fixtures, the way its
// acceptance test does, so the live detector fires at the accepted knee.
func (loop *Loop) knee(ctx context.Context, miner Miner) (int64, bool, error) {
	result, err := loop.launcher.Run(ctx, proc.Command{
		Executable: miner.Executable,
		Arguments:  []string{verbBacktest, flagJSON, filepath.Join(miner.Directory, minerLabels), filepath.Join(miner.Directory, minerFixturesGlob)},
		Directory:  loop.repository.directory,
	})
	if err != nil {
		return 0, false, fmt.Errorf("ouroboros: backtest %s: %w", miner.Name, err)
	}
	var backtest minerBacktest
	if err := json.Unmarshal(bytes.TrimSpace(result.Stdout), &backtest); err != nil {
		return 0, false, fmt.Errorf("ouroboros: decode the backtest of %s: %w", miner.Name, err)
	}
	if backtest.Knee == nil {
		return 0, false, nil
	}
	return *backtest.Knee, true, nil
}

// mine runs one miner over items and returns its findings, each named by
// the item it was read from.
func (loop *Loop) mine(ctx context.Context, miner Miner, knee int64, hasKnee bool, items []string) ([]Finding, error) {
	arguments := []string{verbFindings}
	if hasKnee {
		arguments = append(arguments, flagKnee, strconv.FormatInt(knee, 10))
	}
	for _, item := range items {
		arguments = append(arguments, filepath.Join(loop.corpus.directory, filepath.FromSlash(item)))
	}
	result, err := loop.launcher.Run(ctx, proc.Command{Executable: miner.Executable, Arguments: arguments, Directory: loop.repository.directory})
	if err != nil {
		return nil, fmt.Errorf("ouroboros: findings of %s: %w", miner.Name, err)
	}
	return ParseFindings(miner.Name, items, result.Stdout, loop.clock.Now().UTC())
}

// ParseFindings reads the JSON lines a miner printed. A finding's subject is
// its first argument; it is attributed to the item whose run directory the
// subject names, or to the first item when the subject names none.
func ParseFindings(miner string, items []string, output []byte, at time.Time) ([]Finding, error) {
	var findings []Finding
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64<<10), maxFindingLineBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var record minerFinding
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, fmt.Errorf("ouroboros: decode a finding of %s: %w", miner, err)
		}
		subject := ""
		if len(record.Subject) > 0 {
			subject = record.Subject[0].Text
			if subject == "" && record.Subject[0].Number != nil {
				subject = strconv.FormatInt(*record.Subject[0].Number, 10)
			}
		}
		if subject == "" {
			continue
		}
		item := ""
		for _, candidate := range items {
			if strings.HasPrefix(candidate, subject+"/") || candidate == subject {
				item = candidate
				break
			}
		}
		if item == "" && len(items) > 0 {
			item = items[0]
		}
		findings = append(findings, Finding{
			Miner: miner, Rule: record.Rule, Subject: subject, Item: item, Severity: record.Severity, Scope: record.Scope,
			Record: json.RawMessage(slices.Clone(line)), FoundAt: at,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ouroboros: read the findings of %s: %w", miner, err)
	}
	return findings, nil
}

// corpusItems lists the run logs under the corpus, as item names relative
// to it, with their sizes: every <assignment>/events.jsonl.
func (loop *Loop) corpusItems(ctx context.Context, only []string) (map[string]int64, error) {
	names := only
	if names == nil {
		entries, err := loop.corpus.files.ReadDir(rootName)
		if err != nil {
			return nil, fmt.Errorf("ouroboros: list the corpus: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				names = append(names, entry.Name())
			}
		}
	}
	hidden, err := loop.hiddenRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("ouroboros: list the held-out runs: %w", err)
	}
	items := map[string]int64{}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if hidden[name] {
			continue
		}
		item := path.Join(name, session.EventsFile)
		info, err := stdfs.Stat(loop.corpus.files, item)
		if errors.Is(err, stdfs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		items[item] = info.Size()
	}
	return items, nil
}

// Detect runs every built miner over the corpus items that grew since the
// miner last read them: every item when only is nil, the named run
// directories otherwise. It is deterministic and calls no model; the new
// findings go to the ledger and each item's size is recorded as read.
func (loop *Loop) Detect(ctx context.Context, only []string) error {
	miners, err := loop.Miners(ctx)
	if err != nil {
		return err
	}
	items, err := loop.corpusItems(ctx, only)
	if err != nil {
		return err
	}
	var failures []error
	for _, miner := range miners {
		if !miner.Built {
			continue
		}
		if err := loop.detectWith(ctx, miner, items); err != nil {
			if ctx.Err() != nil {
				return err
			}
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (loop *Loop) detectWith(ctx context.Context, miner Miner, items map[string]int64) error {
	var grown []string
	for item, size := range items {
		read, known, err := loop.store.Item(ctx, miner.Name, item)
		if err != nil {
			return err
		}
		if !known || read.ByteSize != size {
			grown = append(grown, item)
		}
	}
	if len(grown) == 0 {
		return nil
	}
	slices.Sort(grown)
	knee, hasKnee, err := loop.knee(ctx, miner)
	if err != nil {
		return err
	}
	findings, err := loop.mine(ctx, miner, knee, hasKnee, grown)
	if err != nil {
		return err
	}
	inserted, err := loop.store.RecordFindings(ctx, findings)
	if err != nil {
		return err
	}
	now := loop.clock.Now().UTC()
	for _, item := range grown {
		if err := loop.store.RecordItem(ctx, Item{Miner: miner.Name, Item: item, ByteSize: items[item], MinedAt: now}); err != nil {
			return err
		}
	}
	loop.logger.Info("ouroboros: mined", "miner", miner.Name, "items", len(grown), "findings", len(findings), "new", inserted)
	return nil
}
