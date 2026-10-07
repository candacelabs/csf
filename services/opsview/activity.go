// Copyright 2026 Candace Labs

package opsview

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// toolInput is the part of a tool call's input an activity line reads. Every
// field is optional; each tool fills the few it has.
type toolInput struct {
	Description string `json:"description"`
	Command     string `json:"command"`
	FilePath    string `json:"file_path"`
	Path        string `json:"path"`
	Pattern     string `json:"pattern"`
	Query       string `json:"query"`
	URL         string `json:"url"`
	Skill       string `json:"skill"`
	Reason      string `json:"reason"`
}

// activityOf is one tool's sentence, from its parsed input and the worktree
// paths are shown relative to.
type activityOf func(input toolInput, worktree string) string

// The tools a session calls, by the name the executor reports, and the
// sentence each becomes. A tool not listed reads as its own name.
var toolActivities = map[string]activityOf{
	"Bash":         func(input toolInput, worktree string) string { return commandActivity(input, worktree) },
	"Monitor":      func(input toolInput, worktree string) string { return "Watching: " + lowerFirst(input.Description) },
	"Read":         func(input toolInput, worktree string) string { return "Reading " + relative(input.FilePath, worktree) },
	"Edit":         func(input toolInput, worktree string) string { return "Editing " + relative(input.FilePath, worktree) },
	"MultiEdit":    func(input toolInput, worktree string) string { return "Editing " + relative(input.FilePath, worktree) },
	"Write":        func(input toolInput, worktree string) string { return "Writing " + relative(input.FilePath, worktree) },
	"NotebookEdit": func(input toolInput, worktree string) string { return "Editing " + relative(input.FilePath, worktree) },
	"Grep":         func(input toolInput, worktree string) string { return searching(input.Pattern, input.Path, worktree) },
	"Glob":         func(input toolInput, worktree string) string { return "Finding files matching " + input.Pattern },
	"WebFetch":     func(input toolInput, worktree string) string { return "Reading " + hostOf(input.URL) },
	"WebSearch":    func(input toolInput, worktree string) string { return "Searching the web for " + quoted(input.Query) },
	"Agent":        func(input toolInput, worktree string) string { return "Delegating: " + lowerFirst(input.Description) },
	"Task":         func(input toolInput, worktree string) string { return "Delegating: " + lowerFirst(input.Description) },
	"TodoWrite":    func(input toolInput, worktree string) string { return "Updating its plan" },
	"ToolSearch":   func(input toolInput, worktree string) string { return "Loading tools" },
	"Skill":        func(input toolInput, worktree string) string { return "Reading the " + input.Skill + " guide" },
	"TaskStop":     func(input toolInput, worktree string) string { return "Stopping a background task" },
	"ScheduleWakeup": func(input toolInput, worktree string) string {
		return "Waiting: " + lowerFirst(input.Reason)
	},
}

// mcpPrefix starts every MCP tool's name: mcp__<server>__<operation>.
const mcpPrefix = "mcp__"

// Activity is the sentence a card shows for one tool call: what the session
// is doing, in words, never the raw command. Paths are relative to the
// worktree and a home directory is never shown.
func Activity(tool string, raw json.RawMessage, worktree string) string {
	var input toolInput
	_ = json.Unmarshal(raw, &input)
	if describe, known := toolActivities[tool]; known {
		return bounded(Scrub(strings.TrimSpace(describe(input, worktree)), worktree))
	}
	if server, operation, isMCP := strings.Cut(strings.TrimPrefix(tool, mcpPrefix), "__"); strings.HasPrefix(tool, mcpPrefix) && isMCP {
		return bounded(strings.ToUpper(server) + ": " + spaced(operation))
	}
	return tool
}

// targetKeys are the input field that names what each tool acts on, as a
// transcript's tool row shows it after the tool's name. A tool not listed is
// named by its gist.
var targetKeys = map[string]func(input toolInput) string{
	"Bash":         func(input toolInput) string { return input.Command },
	"Read":         func(input toolInput) string { return input.FilePath },
	"Edit":         func(input toolInput) string { return input.FilePath },
	"MultiEdit":    func(input toolInput) string { return input.FilePath },
	"Write":        func(input toolInput) string { return input.FilePath },
	"NotebookEdit": func(input toolInput) string { return input.FilePath },
	"Grep":         func(input toolInput) string { return input.Pattern },
	"Glob":         func(input toolInput) string { return input.Pattern },
	"WebFetch":     func(input toolInput) string { return input.URL },
	"WebSearch":    func(input toolInput) string { return input.Query },
	"Skill":        func(input toolInput) string { return input.Skill },
}

// Target is what one tool call acts on, for a transcript's tool row: the
// file, the command line's first line, the pattern or the address, relative
// to the worktree with home directories removed, on one bounded line.
func Target(tool string, raw json.RawMessage, worktree string) string {
	var input toolInput
	_ = json.Unmarshal(raw, &input)
	target := gist(raw)
	if read, known := targetKeys[tool]; known {
		target = firstLine(read(input))
	}
	if input.FilePath != "" && target == firstLine(input.FilePath) {
		target = relative(target, worktree)
	}
	return bounded(Scrub(target, worktree))
}

// RawActivity is the call as the executor sent it, for the raw view: the
// tool and the first line of its input's leading field, home paths removed.
func RawActivity(tool string, raw json.RawMessage, worktree string) string {
	return Scrub(tool+": "+gist(raw), worktree)
}

// commandActivity is a shell call's sentence: the description the session
// gave it when it gave one, else what the command line runs.
func commandActivity(input toolInput, worktree string) string {
	if description := strings.TrimSpace(input.Description); description != "" {
		return firstLine(description)
	}
	words := commandWords(input.Command)
	if len(words) == 0 {
		return "Running a command"
	}
	for _, command := range commandActivities {
		if sentence, matched := command(words, worktree); matched {
			return sentence
		}
	}
	return "Running " + programName(words[0])
}

// commandActivity entries name what a command line runs, or report no match.
// The first that matches wins, so the specific ones come first.
var commandActivities = []func(words []string, worktree string) (string, bool){
	func(words []string, worktree string) (string, bool) {
		script := scriptName(words)
		sentence, known := scriptActivities[script]
		if !known {
			return "", false
		}
		if script == mergeScriptName {
			return sentence + pullNumber(words), true
		}
		return sentence, true
	},
	func(words []string, worktree string) (string, bool) {
		if scriptName(words) != bazelScriptName || len(words) < 2 {
			return "", false
		}
		verb, targets := bazelVerb(words)
		return fmt.Sprintf("%s (%s)", verb, counted(targets, "target")), true
	},
	func(words []string, worktree string) (string, bool) {
		if words[0] != "go" || len(words) < 2 {
			return "", false
		}
		switch words[1] {
		case "test":
			return "Running Go tests in " + packagesOf(words[2:], worktree), true
		case "build":
			return "Building Go packages", true
		case "vet":
			return "Vetting Go packages", true
		}
		return "", false
	},
	func(words []string, worktree string) (string, bool) {
		if words[0] != "gh" || len(words) < 3 {
			return "", false
		}
		number := numberIn(words[3:])
		switch words[1] + " " + words[2] {
		case "pr view", "pr checks", "pr diff":
			return "Checking pull request" + number, true
		case "pr create":
			return "Opening a pull request", true
		case "pr ready":
			return "Marking pull request" + number + " ready", true
		case "pr merge":
			return "Merging pull request" + number, true
		case "pr edit", "api -X":
			return "Updating pull request" + number, true
		case "issue view":
			return "Reading ticket" + number, true
		case "run watch", "run view":
			return "Watching a CI run", true
		}
		return "Using GitHub (" + words[1] + " " + words[2] + ")", true
	},
	func(words []string, worktree string) (string, bool) {
		if words[0] != "git" || len(words) < 2 {
			return "", false
		}
		sentence, known := gitActivities[gitVerb(words)]
		return sentence, known
	},
	func(words []string, worktree string) (string, bool) {
		if words[0] != "grep" && words[0] != "rg" {
			return "", false
		}
		pattern, at := searchPattern(words[1:])
		return searching(pattern, at, worktree), true
	},
	func(words []string, worktree string) (string, bool) {
		sentence, known := plainActivities[words[0]]
		if !known {
			return "", false
		}
		if target := lastPath(words[1:]); target != "" {
			return sentence + " " + relative(target, worktree), true
		}
		return sentence, true
	},
	func(words []string, worktree string) (string, bool) {
		if words[0] != "docker" || len(words) < 2 {
			return "", false
		}
		switch words[1] {
		case "run":
			return "Running a container", true
		case "build":
			return "Building a container image", true
		}
		return "Using Docker (" + words[1] + ")", true
	},
}

// The repository's own scripts a session runs, by file name.
const (
	bazelScriptName = "bazel.sh"
	mergeScriptName = "merge-pr.sh"
)

var scriptActivities = map[string]string{
	"check-merge.sh":                "Running the merge checks",
	"check-house-lint.sh":           "Running the house lint",
	"ontology-score.sh":             "Scoring ontology alignment",
	"check_operator_identifiers.py": "Checking for operator identifiers",
	"check-generated.sh":            "Checking generated files",
	"check-bazel-metadata.sh":       "Checking Bazel metadata",
	"house-lint-ratchet.sh":         "Running the house lint ratchet",
	mergeScriptName:                 "Merging pull request",
}

var gitActivities = map[string]string{
	"status": "Checking the working tree", "diff": "Reviewing its changes", "log": "Reading history", "show": "Reading a commit",
	"add": "Staging changes", "commit": "Committing", "push": "Pushing the branch", "fetch": "Fetching from the remote",
	"pull": "Pulling from the remote", "rebase": "Rebasing", "merge": "Merging branches", "checkout": "Switching branches",
	"switch": "Switching branches", "branch": "Managing branches", "stash": "Stashing changes", "reset": "Resetting the branch",
	"worktree": "Managing worktrees", "ls-files": "Listing tracked files", "grep": "Searching tracked files",
	"cherry-pick": "Cherry-picking a commit", "remote": "Reading the remote", "ls-remote": "Reading the remote",
}

var plainActivities = map[string]string{
	"ls": "Listing", "cat": "Reading", "head": "Reading", "tail": "Reading", "sed": "Reading", "wc": "Measuring",
	"find": "Finding files in", "mkdir": "Creating", "rm": "Removing", "cp": "Copying", "mv": "Moving", "du": "Measuring",
	"python3": "Running", "bash": "Running", "make": "Building", "curl": "Calling", "jq": "Reading JSON", "diff": "Comparing",
	"chmod": "Changing permissions of", "touch": "Creating", "echo": "Printing", "printf": "Printing",
}

// commandWords splits the first simple command of a command line into words,
// past the wrappers that do not say what it does: an environment assignment,
// cd into a directory, a timeout.
func commandWords(command string) []string {
	line := strings.TrimSpace(strings.TrimLeft(firstLine(command), "\\ "))
	// Separators are spaced the way commands are written, so a | inside a
	// quoted pattern is not read as a pipe.
	for _, separator := range []string{" && ", " || ", " | ", "; "} {
		if before, after, found := strings.Cut(line, separator); found {
			if first := strings.Fields(before); len(first) > 0 && first[0] == "cd" {
				line = strings.TrimSpace(after)
				continue
			}
			line = before
		}
	}
	words := strings.Fields(line)
	for len(words) > 0 {
		switch {
		case strings.Contains(words[0], "=") && !strings.HasPrefix(words[0], "-"):
			words = words[1:]
		case words[0] == "timeout" && len(words) > 1:
			words = words[2:]
		case words[0] == "env" || words[0] == "exec" || words[0] == "time" || words[0] == "sudo":
			words = words[1:]
		default:
			return words
		}
	}
	return words
}

// scriptName is the script a command runs: the program itself, or the first
// argument of bash, sh or python3.
func scriptName(words []string) string {
	program := programName(words[0])
	if (program == "bash" || program == "sh" || program == "python3") && len(words) > 1 {
		return programName(words[1])
	}
	return program
}

func programName(word string) string {
	return word[strings.LastIndex(word, "/")+1:]
}

// bazelVerb reads tools/bazel.sh <verb> [--] <targets...>.
func bazelVerb(words []string) (string, int) {
	if scriptName(words) != programName(words[0]) {
		words = words[1:]
	}
	verb, targets := "Running Bazel", 0
	for index, word := range words[1:] {
		if index == 0 {
			switch word {
			case "test":
				verb = "Running Bazel tests"
			case "build":
				verb = "Building with Bazel"
			case "run":
				verb = "Running a Bazel target"
			}
			continue
		}
		if strings.HasPrefix(word, "//") || strings.HasPrefix(word, "@") || strings.HasPrefix(word, "-//") || strings.Contains(word, ":") && !strings.HasPrefix(word, "-") {
			targets++
		}
	}
	return verb, targets
}

// packagesOf names the packages a go test runs: one by its path, several by
// their count.
func packagesOf(arguments []string, worktree string) string {
	var packages []string
	for _, argument := range arguments {
		if argument == "-args" || argument == "--" {
			break
		}
		if strings.HasPrefix(argument, "./") || strings.HasPrefix(argument, "/") {
			packages = append(packages, strings.TrimRight(strings.TrimSuffix(strings.TrimPrefix(relative(argument, worktree), "./"), "/..."), "/"))
		}
	}
	switch len(packages) {
	case 0:
		return "the module"
	case 1:
		return packages[0]
	}
	return counted(len(packages), "package")
}

func gitVerb(words []string) string {
	for index := 1; index < len(words); index++ {
		switch {
		case words[index] == "-C" || words[index] == "-c":
			index++
		case !strings.HasPrefix(words[index], "-"):
			return words[index]
		}
	}
	return ""
}

// searchPattern is a grep's pattern and the path it searches, past its
// flags.
func searchPattern(arguments []string) (string, string) {
	var plain []string
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch {
		case argument == "-e" && index+1 < len(arguments):
			return unquote(arguments[index+1]), ""
		case argument == "--include" || argument == "-A" || argument == "-B" || argument == "-C" || argument == "-m":
			index++
		case strings.HasPrefix(argument, "-"):
		default:
			plain = append(plain, unquote(argument))
		}
	}
	switch len(plain) {
	case 0:
		return "", ""
	case 1:
		return plain[0], ""
	}
	return plain[0], plain[1]
}

// searching is a search's sentence: the pattern in words when it is words,
// quoted when it is an expression, and where.
func searching(pattern string, at string, worktree string) string {
	if pattern == "" {
		return "Searching"
	}
	sentence := "Searching for " + readablePattern(pattern)
	if place := relative(at, worktree); place != "" && place != "." {
		sentence += " in " + place
	}
	return sentence
}

var expressionAlternation = regexp.MustCompile(`\\\||\|`)

// readablePattern is a pattern as words: alternations become "or", escapes
// are dropped, and anything still not words is quoted.
func readablePattern(pattern string) string {
	alternatives := expressionAlternation.Split(pattern, -1)
	if len(alternatives) > 3 {
		alternatives = append(alternatives[:3], "…")
	}
	for index, alternative := range alternatives {
		words := strings.NewReplacer(`\s+`, " ", `\s*`, " ", `.*`, " ", `\b`, "", `\(`, "(", `\)`, ")", `\.`, ".", `\`, "").Replace(alternative)
		alternatives[index] = quoted(strings.Join(strings.Fields(words), " "))
	}
	return strings.Join(alternatives, " or ")
}

func quoted(text string) string {
	if text == "…" {
		return text
	}
	return "“" + text + "”"
}

func unquote(word string) string { return strings.Trim(word, `"'`) }

// lastPath is the last argument that is not a flag, which is the file most
// read, list and copy commands act on.
func lastPath(arguments []string) string {
	for index := len(arguments) - 1; index >= 0; index-- {
		if argument := unquote(arguments[index]); argument != "" && !strings.HasPrefix(argument, "-") && !strings.ContainsAny(argument, "<>&$'\"") {
			return argument
		}
	}
	return ""
}

var pullRequestNumber = regexp.MustCompile(`(?:^|/pull/)#?(\d+)$`)

func numberIn(words []string) string {
	for _, word := range words {
		if match := pullRequestNumber.FindStringSubmatch(word); match != nil {
			return " #" + match[1]
		}
	}
	return ""
}

func pullNumber(words []string) string { return numberIn(words[1:]) }

func hostOf(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" {
		return "a web page"
	}
	return parsed.Host
}

func counted(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// spaced turns an operation's name into words: ListAgentSessions → list agent
// sessions.
func spaced(name string) string {
	var builder strings.Builder
	for index, letter := range name {
		if unicode.IsUpper(letter) && index > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteRune(unicode.ToLower(letter))
	}
	return strings.ReplaceAll(builder.String(), "_", " ")
}

func lowerFirst(text string) string {
	text = firstLine(text)
	if text == "" {
		return text
	}
	runes := []rune(text)
	if len(runes) > 1 && unicode.IsUpper(runes[1]) {
		return text
	}
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// relative shows a path relative to the worktree, and a home directory as ~.
func relative(target string, worktree string) string {
	target = unquote(target)
	if worktree != "" {
		if target == worktree {
			return "."
		}
		if rest, inside := strings.CutPrefix(target, strings.TrimRight(worktree, "/")+"/"); inside {
			return rest
		}
	}
	return Scrub(target, worktree)
}

// A home directory, a session's scratch directory (whose name spells the
// worktree's path with dashes, home included), and a home directory spelled
// that way anywhere else.
var (
	homeDirectory    = regexp.MustCompile(`/home/[^/\s"']+`)
	scratchDirectory = regexp.MustCompile(`/tmp/claude-\d+/[^/\s"']+`)
	dashedHome       = regexp.MustCompile(`-home-[^-/\s"']+-`)
)

// Scrub removes the worktree, scratch directories and any home directory
// from text: what every line the Workbench shows of a session goes through.
func Scrub(text string, worktree string) string {
	if worktree != "" {
		text = strings.ReplaceAll(text, strings.TrimRight(worktree, "/")+"/", "")
	}
	text = scratchDirectory.ReplaceAllString(text, "scratch")
	text = dashedHome.ReplaceAllString(text, "-~-")
	return homeDirectory.ReplaceAllString(text, "~")
}

// bounded keeps an activity line to one short line.
func bounded(text string) string {
	if runes := []rune(text); len(runes) > activityLimit {
		return string(runes[:activityLimit-1]) + "…"
	}
	return text
}

// activityLimit is how long a sentence may be before it is cut: a phone row
// at 16 px holds about this many characters on two lines.
const activityLimit = 72

// errorWordings turn the harness's and the executor's failures into what
// happened. The first fragment found decides; anything else reads as its own
// innermost message.
var errorWordings = []struct {
	fragment string
	wording  string
}{
	{"terminated signal received", "Stopped: the session was canceled."},
	{"context canceled", "Stopped: the session was canceled."},
	{"context deadline exceeded", "Timed out."},
	{"executable file not found", "The model executable is not installed on this host."},
	{"No commits between", "No pull request yet: the branch has no commits."},
	{"could not open the draft pull request", "Could not open the draft pull request."},
	{"could not push the work branch", "Could not push the work branch."},
	{"the Claude Code process is closed", "The model process exited."},
}

var errorPrefixes = regexp.MustCompile(`^(?:[a-z-]+(?: [a-z-]+)?: )+`)

// HumanError is a failure as a card says it: what happened, in a sentence,
// with the turn it happened on. The raw text stays behind the card's details.
func HumanError(raw string, worktree string) string {
	if raw == "" {
		return ""
	}
	turn := ""
	if match := errorTurn.FindStringSubmatch(raw); match != nil {
		turn = "Turn " + match[1] + ": "
	}
	for _, wording := range errorWordings {
		if strings.Contains(raw, wording.fragment) {
			return turn + wording.wording
		}
	}
	message := errorPrefixes.ReplaceAllString(firstLine(Scrub(raw, worktree)), "")
	message = strings.TrimPrefix(message, errorTurn.FindString(message))
	if message == "" {
		return turn + "Failed."
	}
	runes := []rune(message)
	runes[0] = unicode.ToUpper(runes[0])
	return turn + string(runes)
}

var errorTurn = regexp.MustCompile(`turn (\d+): `)
