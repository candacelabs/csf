// Copyright 2026 Candace Labs

package costs

import (
	"bufio"
	"cmp"
	"encoding/json"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/candacelabs/csf/services/harness/routing"
	"github.com/candacelabs/csf/services/harness/session"
)

// Area affinity: a real session's area signature is the exact set of
// repository paths it read, edited or searched, and the ontology terms that
// name a directory on them, all read from its events. A request's area is
// the paths and terms its prompt names. Routing a request to the warm real
// session whose signature covers it best carries that session's context
// into every call the request makes, at the cache-read price, and avoids
// the exploration the signature already holds.

// AccessKind is how a tool touched a path.
type AccessKind int

const (
	AccessRead AccessKind = iota
	AccessEdit
	AccessSearch
)

// The tools that name a repository path, by kind.
var accessTools = map[string]AccessKind{
	"Read": AccessRead, "Edit": AccessEdit, "Write": AccessEdit, "NotebookEdit": AccessEdit, "Grep": AccessSearch, "Glob": AccessSearch,
}

// Access is one repository path a real session touched.
type Access struct {
	At   time.Time
	Real string
	// Path is relative to the run's worktree.
	Path string
	Kind AccessKind
	// Bytes is the tool result's size.
	Bytes int64
}

// Prompt is one message sent to the session.
type Prompt struct {
	At   time.Time
	Text string
}

// ByteSample pairs a read result's size with the cache write of the call
// that next carried it: alone in its step, the result is most of that write.
type ByteSample struct {
	Bytes int64
	Write int64
}

// RunArea is everything a run read, edited and was asked.
type RunArea struct {
	Accesses []Access
	Prompts  []Prompt
	// Calls is the run's top-level model calls.
	Calls   int
	Samples []ByteSample
}

const (
	blockToolUse    = "tool_use"
	blockToolResult = "tool_result"
	blockText       = "text"
	directionIn     = "in"
	// sampleBytes is the least read result a byte sample takes, so the
	// result and not the model's own output dominates the next write.
	sampleBytes = 8 << 10
)

// block is one content block of a message.
type block struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Text  string `json:"text"`
	Input struct {
		FilePath     string `json:"file_path"`
		Path         string `json:"path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"input"`
	ToolUseID string `json:"tool_use_id"`
	// Content is a string or a list of text blocks.
	Content json.RawMessage `json:"content"`
}

// areaReader follows one run's tool uses to their results.
type areaReader struct {
	pending  map[string]int
	lastCall string
	reads    []int64
	others   int
}

func newAreaReader() *areaReader { return &areaReader{pending: map[string]int{}} }

// blocks decodes a message's content; a plain string is one text block.
func blocks(content json.RawMessage) []block {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return []block{{Type: blockText, Text: text}}
	}
	var decoded []block
	_ = json.Unmarshal(content, &decoded)
	return decoded
}

// size is a tool result's length in bytes.
func size(content json.RawMessage) int64 {
	var total int64
	for _, each := range blocks(content) {
		total += int64(len(each.Text))
	}
	return total
}

// observe takes what one record says about the run's area. Only the
// top-level conversation counts: a subagent's reads never enter it.
func (reader *areaReader) observe(run *Run, line record) {
	if line.Event.ParentToolUseID != nil {
		return
	}
	switch line.EventType {
	case session.EventTypeAssistant:
		message := line.Event.Message
		if message.ID != "" && message.ID != reader.lastCall {
			run.Area.Calls++
			if len(reader.reads) == 1 && reader.others == 0 && reader.reads[0] >= sampleBytes && message.Usage != nil {
				run.Area.Samples = append(run.Area.Samples, ByteSample{Bytes: reader.reads[0], Write: message.Usage.Creation})
			}
			reader.lastCall, reader.reads, reader.others = message.ID, nil, 0
		}
		for _, use := range blocks(message.Content) {
			kind, known := accessTools[use.Name]
			if use.Type != blockToolUse || !known {
				continue
			}
			path, inside := relative(run.Worktree, cmp.Or(use.Input.FilePath, use.Input.Path, use.Input.NotebookPath))
			if !inside {
				continue
			}
			reader.pending[use.ID] = len(run.Area.Accesses)
			run.Area.Accesses = append(run.Area.Accesses, Access{At: line.Time, Real: line.Event.SessionID, Path: path, Kind: kind})
		}
	case session.EventTypeUser:
		if len(line.Event.ToolUseResult) == 0 {
			if line.Direction == directionIn {
				texts := []string{}
				for _, each := range blocks(line.Event.Message.Content) {
					texts = append(texts, each.Text)
				}
				run.Area.Prompts = append(run.Area.Prompts, Prompt{At: line.Time, Text: strings.Join(texts, "\n")})
			}
			return
		}
		for _, result := range blocks(line.Event.Message.Content) {
			if result.Type != blockToolResult {
				continue
			}
			index, tracked := reader.pending[result.ToolUseID]
			if !tracked || run.Area.Accesses[index].Kind != AccessRead {
				reader.others++
			}
			if !tracked {
				continue
			}
			delete(reader.pending, result.ToolUseID)
			run.Area.Accesses[index].Bytes = size(result.Content)
			if run.Area.Accesses[index].Kind == AccessRead {
				reader.reads = append(reader.reads, run.Area.Accesses[index].Bytes)
			}
		}
	}
}

// relative is a path relative to the worktree, when it lies inside it.
func relative(worktree string, path string) (string, bool) {
	if worktree == "" {
		return "", false
	}
	rest, inside := strings.CutPrefix(path, strings.TrimSuffix(worktree, "/")+"/")
	return rest, inside && rest != ""
}

// Term is one ontology term: its identifier and the spellings that name it.
type Term struct {
	ID    string
	Forms []string
}

// The two line shapes of architecture.csf a term list needs; the language
// compiler owns the grammar.
var (
	termLine  = regexp.MustCompile(`^term ([a-z0-9_]+) "([^"]*)"`)
	formsLine = regexp.MustCompile(`^\s+forms\s+(.*);\s*$`)
	quoted    = regexp.MustCompile(`"([^"]*)"`)
)

// ReadTerms reads every term of an architecture.csf with its name and forms.
func ReadTerms(source io.Reader) ([]Term, error) {
	terms := []Term{}
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 0, 1<<16), maxRecordBytes)
	for scanner.Scan() {
		if match := termLine.FindStringSubmatch(scanner.Text()); match != nil {
			terms = append(terms, Term{ID: match[1], Forms: []string{strings.ReplaceAll(match[1], "_", " "), strings.ToLower(match[2])}})
			continue
		}
		if match := formsLine.FindStringSubmatch(scanner.Text()); match != nil && len(terms) > 0 {
			for _, form := range quoted.FindAllStringSubmatch(match[1], -1) {
				terms[len(terms)-1].Forms = append(terms[len(terms)-1].Forms, strings.ToLower(form[1]))
			}
		}
	}
	return terms, scanner.Err()
}

// Signature is a real session's area at one moment.
type Signature struct {
	Paths map[string]bool
	Terms map[string]bool
}

// covers reports whether the signature holds an area item: a path it
// touched or a directory above one, or a term.
func (signature Signature) covers(item string) bool {
	if term, isTerm := strings.CutPrefix(item, termPrefix); isTerm {
		return signature.Terms[term]
	}
	if signature.Paths[item] {
		return true
	}
	for path := range signature.Paths {
		if strings.HasPrefix(path, item+"/") {
			return true
		}
	}
	return false
}

// Coverage is the share of the area's items the signature holds.
func (signature Signature) Coverage(area []string) float64 {
	if len(area) == 0 {
		return 0
	}
	covered := 0
	for _, item := range area {
		if signature.covers(item) {
			covered++
		}
	}
	return float64(covered) / float64(len(area))
}

// termPrefix marks a term among an area's items.
const termPrefix = "term:"

// signatureOf is what a real session of the run had touched before the
// moment: its paths, and every term whose identifier names a directory on
// one of them.
func signatureOf(run Run, real string, before time.Time, terms []Term) Signature {
	signature := Signature{Paths: map[string]bool{}, Terms: map[string]bool{}}
	segments := map[string]bool{}
	for _, access := range run.Area.Accesses {
		if access.Real != real || !access.At.Before(before) {
			continue
		}
		signature.Paths[access.Path] = true
		for segment := range strings.SplitSeq(access.Path, "/") {
			segments[segment] = true
		}
	}
	for _, term := range terms {
		if segments[term.ID] || segments[strings.ReplaceAll(term.ID, "_", "")] {
			signature.Terms[term.ID] = true
		}
	}
	return signature
}

// currentDirectory is how prose may start a relative path.
const currentDirectory = "./"

// pathToken is a slash-separated token of prose that may name a path.
var pathToken = regexp.MustCompile(`[A-Za-z0-9_.\-]+(?:/[A-Za-z0-9_.\-]+)+`)

// RequestArea is what a prompt names: the paths among the known ones (a
// file or a directory above one), and the terms it spells.
func RequestArea(text string, worktree string, known map[string]bool, terms []Term) []string {
	area := map[string]bool{}
	for _, token := range pathToken.FindAllString(text, -1) {
		if path, inside := relative(worktree, "/"+strings.TrimPrefix(token, "/")); inside {
			token = path
		}
		token = strings.TrimSuffix(strings.TrimPrefix(token, currentDirectory), "/")
		if known[token] {
			area[token] = true
		}
	}
	lowered := " " + strings.ToLower(text) + " "
	for _, term := range terms {
		for _, form := range term.Forms {
			if form != "" && containsWord(lowered, form) {
				area[termPrefix+term.ID] = true
				break
			}
		}
	}
	return slices.Sorted(maps.Keys(area))
}

// containsWord reports whether text holds the phrase between non-letters.
func containsWord(text string, phrase string) bool {
	for offset := 0; ; {
		index := strings.Index(text[offset:], phrase)
		if index < 0 {
			return false
		}
		start, end := offset+index, offset+index+len(phrase)
		if !isWordByte(text[start-1]) && (end >= len(text) || !isWordByte(text[end])) {
			return true
		}
		offset = start + 1
	}
}

func isWordByte(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_'
}

// KnownPaths is every path any run touched and every directory above one.
func KnownPaths(runs []Run) map[string]bool {
	known := map[string]bool{}
	for _, run := range runs {
		for _, access := range run.Area.Accesses {
			for path := access.Path; path != "." && path != ""; {
				if known[path] {
					break
				}
				known[path] = true
				index := strings.LastIndex(path, "/")
				if index < 0 {
					break
				}
				path = path[:index]
			}
		}
	}
	return known
}

// Reread is one read of a path that another real session, live at the
// time, had already read.
type Reread struct {
	Run   int
	Day   string
	Model string
	Bytes int64
}

// liveSession reports whether the run was open at the moment with real as
// its current conversation: the last one a turn had started on.
func liveSession(run Run, real string, at time.Time) bool {
	open := false
	for _, span := range run.Open {
		if !at.Before(span.From) && !at.After(span.To) {
			open = true
			break
		}
	}
	if !open {
		return false
	}
	current := ""
	for _, turn := range run.Turns {
		if turn.Start.After(at) {
			break
		}
		current = turn.Real
	}
	return current == real
}

// Rereads is every read in the record of a path another then-live real
// session had already read, and the bytes of every read.
func Rereads(runs []Run) ([]Reread, int64) {
	type reading struct {
		run  int
		real string
		at   time.Time
	}
	byPath := map[string][]reading{}
	for index, run := range runs {
		for _, access := range run.Area.Accesses {
			if access.Kind == AccessRead {
				byPath[access.Path] = append(byPath[access.Path], reading{index, access.Real, access.At})
			}
		}
	}
	rereads := []Reread{}
	var total int64
	for index, run := range runs {
		for _, access := range run.Area.Accesses {
			if access.Kind != AccessRead {
				continue
			}
			total += access.Bytes
			for _, earlier := range byPath[access.Path] {
				if earlier.real != access.Real && earlier.at.Before(access.At) && liveSession(runs[earlier.run], earlier.real, access.At) {
					rereads = append(rereads, Reread{Run: index, Day: access.At.UTC().Format(dayFormat), Model: modelAt(run, access.At), Bytes: access.Bytes})
					break
				}
			}
		}
	}
	return rereads, total
}

// modelAt is the model of the run's turn running at the moment.
func modelAt(run Run, at time.Time) string {
	model := run.Model
	for _, turn := range run.Turns {
		if turn.Start.After(at) {
			break
		}
		if turn.Model != "" {
			model = turn.Model
		}
	}
	return model
}

// TokensPerByte is the median of the byte samples' write over size: the
// tokens one byte of a read result costs.
func TokensPerByte(runs []Run) []float64 {
	ratios := []float64{}
	for _, run := range runs {
		for _, sample := range run.Area.Samples {
			ratios = append(ratios, float64(sample.Write)/float64(sample.Bytes))
		}
	}
	return ratios
}

// AreaRoute is one bind replayed under a routing policy: the real session
// it attaches to, if any, and what that attach costs against a new one.
type AreaRoute struct {
	Day string
	// Attached is false when the policy opens a new real session.
	Attached bool
	Coverage float64
	// Carried is the attached conversation's context, in tokens.
	Carried int64
	// Credit is the exploration the attach avoids, in tokens: the request's
	// reads of paths the signature already holds.
	Credit float64
	// Delta is the attach's dollars against a new real session.
	Delta float64
	// BreakEven is the carried context, in tokens, at which this attach
	// would cost what it saves: credit * (write - read) / (read * calls).
	BreakEven float64
}

// AreaReplay replays the binds of the record under routing policies.
type AreaReplay struct {
	Runs          []Run
	Terms         []Term
	Lifetime      time.Duration
	Prices        map[string]Price
	TokensPerByte float64
	known         map[string]bool
}

// candidate is one real session a bind could attach to at its moment.
type candidate struct {
	run     int
	real    string
	end     time.Time
	carried int64
	model   string
}

// candidates is every real session of another run, on the model, idle at
// the moment: its last turn ended before it and none is running.
func (replay *AreaReplay) candidates(self int, model string, at time.Time) []candidate {
	found := []candidate{}
	for index, run := range replay.Runs {
		if index == self {
			continue
		}
		last := map[string]Turn{}
		running := false
		for _, turn := range run.Turns {
			if turn.Start.After(at) {
				break
			}
			if turn.End.After(at) {
				running = true
				break
			}
			last[turn.Real] = turn
		}
		if running {
			continue
		}
		for real, turn := range last {
			// A turn with no recorded call has no known context to carry.
			if turn.Model == model && turn.Last.Input() > 0 {
				found = append(found, candidate{index, real, turn.End, turn.Last.Input() + turn.Last.Output, turn.Model})
			}
		}
	}
	slices.SortFunc(found, func(left candidate, right candidate) int { return strings.Compare(left.real, right.real) })
	return found
}

// attach is the route of the bind onto the candidate: the carried context
// read by every call the request makes, the context written again when the
// cache expired, and the credited exploration.
func (replay *AreaReplay) attach(bind int, at time.Time, chosen candidate, coverage float64) AreaRoute {
	run := replay.Runs[bind]
	price := replay.Prices[chosen.model]
	signature := signatureOf(replay.Runs[chosen.run], chosen.real, at, replay.Terms)
	var credit float64
	seen := map[string]bool{}
	for _, access := range run.Area.Accesses {
		if access.Kind == AccessRead && !seen[access.Path] && signature.Paths[access.Path] {
			seen[access.Path] = true
			credit += float64(access.Bytes) * replay.TokensPerByte
		}
	}
	delta := float64(chosen.carried)*price.Read*float64(run.Area.Calls) - credit*(price.Write-price.Read)
	if at.Sub(chosen.end) > replay.Lifetime {
		delta += float64(chosen.carried) * (price.Write - price.Read)
	}
	route := AreaRoute{Day: at.UTC().Format(dayFormat), Attached: true, Coverage: coverage, Carried: chosen.carried, Credit: credit, Delta: delta}
	if run.Area.Calls > 0 && price.Read > 0 {
		route.BreakEven = credit * (price.Write - price.Read) / (price.Read * float64(run.Area.Calls))
	}
	return route
}

// bindsOf is every run's first turn with a model: the request a router
// places.
func (replay *AreaReplay) bindsOf() []int {
	binds := []int{}
	for index, run := range replay.Runs {
		if len(run.Turns) > 0 && run.Turns[0].Model != "" {
			binds = append(binds, index)
		}
	}
	return binds
}

// Area is a bind's request area: what its first prompt names.
func (replay *AreaReplay) Area(bind int) []string {
	if replay.known == nil {
		replay.known = KnownPaths(replay.Runs)
	}
	run := replay.Runs[bind]
	if len(run.Area.Prompts) == 0 {
		return nil
	}
	return RequestArea(run.Area.Prompts[0].Text, run.Worktree, replay.known, replay.Terms)
}

// Affinity routes every bind to the warm candidate whose signature covers
// its area best, when one covers any of it and its carried context is at
// most limit tokens (no limit when limit is negative).
func (replay *AreaReplay) Affinity(limit int64) []AreaRoute {
	routes := []AreaRoute{}
	for _, bind := range replay.bindsOf() {
		run := replay.Runs[bind]
		at := run.Turns[0].Start
		route := AreaRoute{Day: at.UTC().Format(dayFormat)}
		area := replay.Area(bind)
		best, bestCoverage := candidate{}, 0.0
		for _, each := range replay.candidates(bind, run.Turns[0].Model, at) {
			if at.Sub(each.end) > replay.Lifetime || (limit >= 0 && each.carried > limit) {
				continue
			}
			coverage := signatureOf(replay.Runs[each.run], each.real, at, replay.Terms).Coverage(area)
			if coverage > bestCoverage || coverage == bestCoverage && coverage > 0 && each.carried < best.carried {
				best, bestCoverage = each, coverage
			}
		}
		if bestCoverage > 0 {
			route = replay.attach(bind, at, best, bestCoverage)
		}
		routes = append(routes, route)
	}
	return routes
}

// Router replays today's router: a closed real session of the same model
// and agent on the same ticket, or whose title overlaps enough; warm first,
// then the better fit, then the most recent.
func (replay *AreaReplay) Router() []AreaRoute {
	routes := []AreaRoute{}
	for _, bind := range replay.bindsOf() {
		run := replay.Runs[bind]
		at := run.Turns[0].Start
		route := AreaRoute{Day: at.UTC().Format(dayFormat)}
		var best *candidate
		var bestScore float64
		bestWarm := false
		for _, each := range replay.candidates(bind, run.Turns[0].Model, at) {
			holder := replay.Runs[each.run]
			if holder.Agent != run.Agent || liveSession(holder, each.real, at) {
				continue
			}
			score := 1.0
			if run.Ticket == "" || holder.Ticket != run.Ticket {
				score = routing.TitleOverlap(run.Title, holder.Title)
			}
			if score < routing.DefaultThreshold {
				continue
			}
			warm := at.Sub(each.end) <= routing.DefaultTTL
			if best == nil || warm && !bestWarm || warm == bestWarm && (score > bestScore || score == bestScore && each.end.After(best.end)) {
				chosen := each
				best, bestScore, bestWarm = &chosen, score, warm
			}
		}
		if best != nil {
			route = replay.attach(bind, at, *best, 0)
		}
		routes = append(routes, route)
	}
	return routes
}
