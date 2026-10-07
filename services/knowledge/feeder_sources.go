// Copyright 2026 Candace Labs

package knowledge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/textbound"
	"github.com/candacelabs/csf/services/harness/session"
)

// The sources, as the measurements name them.
const (
	sourceRepository = "repository"
	sourceGitHub     = "github"
	sourceState      = "state"
	sourceRuns       = "runs"
)

const (
	// maxFileBytes skips generated bundles and data files a lookup never
	// wants; 3,530 of the repository's 3,535 text files are under it.
	maxFileBytes = 256 << 10
	// maxProseBytes bounds a ticket's body and a run's conversation, so a run
	// of many turns costs at most a few dozen chunks.
	maxProseBytes = 64 << 10
	// maxSourceID is the store's bound on a source id, less the prefix.
	maxSourceID = 240
	// binarySniff is how much of a file is read for a NUL byte.
	binarySniff = 8000

	programGit      = "git"
	programGitHub   = "gh"
	remoteOrigin    = "origin"
	branchMain      = "main"
	remoteMain      = remoteOrigin + "/" + branchMain
	objectBlob      = "blob"
	modeSymlink     = "120000"
	objectMissing   = "missing"
	githubBlobURL   = "https://github.com/%s/blob/%s/%s"
	repositoryURI   = "repository:"
	jsonSuffix      = ".json"
	stateTitle      = "harness/"
	runsTitleSuffix = "/" + session.EventsFile
	keyArray        = "[]"
	keySeparator    = "."
	roleUser        = "user: "
	roleAssistant   = "assistant: "
	issueKind       = "issue"
	pullKind        = "pull"
	githubLimit     = "1000"
	githubFields    = "number,title,body,updatedAt,url"
)

// Source id prefixes, one per source, so a lookup's path says where it came
// from.
const (
	idRepository = "repository/"
	idGitHub     = "github/"
	idState      = "harness/state/"
	idRun        = "harness/run/"
)

// repositorySource reads main of a checkout through git.
type repositorySource struct {
	directory string
	slug      string
	launcher  proc.ILauncher
	load      func() (string, error)
	save      func(commit string) error
	// pending is the commit a pass read, recorded once the pass ingested it.
	pending string
}

func (repository *repositorySource) git(ctx context.Context, stdin io.Reader, arguments ...string) ([]byte, error) {
	result, err := repository.launcher.Run(ctx, proc.Command{Executable: programGit, Arguments: append([]string{"-C", repository.directory}, arguments...), Stdin: stdin})
	if err != nil {
		return nil, fmt.Errorf("knowledge: git %s: %w", arguments[0], err)
	}
	return result.Stdout, nil
}

// read is every text file of main's head that changed since the commit last
// indexed, and the files deleted since it.
func (repository *repositorySource) read(ctx context.Context) ([]document, []string, error) {
	repository.pending = ""
	if _, err := repository.git(ctx, nil, "fetch", "--quiet", remoteOrigin, branchMain); err != nil {
		return nil, nil, err
	}
	output, err := repository.git(ctx, nil, "rev-parse", remoteMain)
	if err != nil {
		return nil, nil, err
	}
	head := strings.TrimSpace(string(output))
	last, err := repository.load()
	if err != nil {
		return nil, nil, fmt.Errorf("knowledge: read the indexed commit: %w", err)
	}
	if head == last {
		return nil, nil, nil
	}
	entries, err := repository.tree(ctx, head)
	if err != nil {
		return nil, nil, err
	}
	documents, err := repository.contents(ctx, head, entries)
	if err != nil {
		return nil, nil, err
	}
	var gone []string
	if last != "" {
		deleted, err := repository.git(ctx, nil, "diff", "--name-only", "-z", "--diff-filter=D", last, head)
		if err != nil {
			return nil, nil, err
		}
		for _, name := range strings.Split(strings.TrimRight(string(deleted), "\x00"), "\x00") {
			if name != "" {
				gone = append(gone, idRepository+name)
			}
		}
	}
	repository.pending = head
	return documents, gone, nil
}

func (repository *repositorySource) done() error {
	if repository.pending == "" {
		return nil
	}
	return repository.save(repository.pending)
}

// treeEntry is one blob of the tree the repository source indexes.
type treeEntry struct {
	object string
	name   string
}

// tree lists head's blobs small enough to index; links and submodules are
// not text of this repository.
func (repository *repositorySource) tree(ctx context.Context, head string) ([]treeEntry, error) {
	listing, err := repository.git(ctx, nil, "ls-tree", "-r", "-l", "-z", head)
	if err != nil {
		return nil, err
	}
	var entries []treeEntry
	for _, line := range strings.Split(strings.TrimRight(string(listing), "\x00"), "\x00") {
		meta, name, found := strings.Cut(line, "\t")
		fields := strings.Fields(meta)
		if !found || len(fields) != 4 || fields[0] == modeSymlink || fields[1] != objectBlob || len(name) > maxSourceID {
			continue
		}
		size, err := strconv.Atoi(fields[3])
		if err != nil || size == 0 || size > maxFileBytes {
			continue
		}
		entries = append(entries, treeEntry{object: fields[2], name: name})
	}
	return entries, nil
}

// contents reads every entry's blob in one git process and keeps the text
// files among them.
func (repository *repositorySource) contents(ctx context.Context, head string, entries []treeEntry) ([]document, error) {
	var request strings.Builder
	for _, entry := range entries {
		request.WriteString(entry.object + "\n")
	}
	output, err := repository.git(ctx, strings.NewReader(request.String()), "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReader(bytes.NewReader(output))
	documents := make([]document, 0, len(entries))
	for _, entry := range entries {
		content, err := readBatchObject(reader)
		if err != nil {
			return nil, fmt.Errorf("knowledge: read %s: %w", entry.name, err)
		}
		if content == nil || bytes.IndexByte(content[:min(len(content), binarySniff)], 0) >= 0 || !utf8.Valid(content) {
			continue
		}
		uri := repositoryURI + entry.name
		if repository.slug != "" {
			uri = fmt.Sprintf(githubBlobURL, repository.slug, head, entry.name)
		}
		documents = append(documents, document{sourceID: idRepository + entry.name, revision: entry.object, title: entry.name,
			uri: uri, license: licenseRepository, text: string(content)})
	}
	return documents, nil
}

// readBatchObject reads one answer of git cat-file --batch: a header line,
// then the object and a newline; a missing object is nil.
func readBatchObject(reader *bufio.Reader) ([]byte, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(header)
	if len(fields) == 2 && fields[1] == objectMissing {
		return nil, nil
	}
	if len(fields) != 3 {
		return nil, fmt.Errorf("unexpected cat-file header %q", header)
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, err
	}
	content := make([]byte, size+1)
	if _, err := io.ReadFull(reader, content); err != nil {
		return nil, err
	}
	return content[:size], nil
}

// githubItem is one ticket or pull request as gh lists it.
type githubItem struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	UpdatedAt string `json:"updatedAt"`
	URL       string `json:"url"`
}

// readGitHub is every ticket and pull request of the repository, each at the
// revision of its last update.
func (feeder *Feeder) readGitHub(ctx context.Context) ([]document, []string, error) {
	var documents []document
	for _, kind := range []string{issueKind, pullKind} {
		command := issueKind
		if kind == pullKind {
			command = "pr"
		}
		result, err := feeder.launcher.Run(ctx, proc.Command{Executable: programGitHub, Arguments: []string{
			command, "list", "--repo", feeder.github, "--state", "all", "--limit", githubLimit, "--json", githubFields}})
		if err != nil {
			return nil, nil, fmt.Errorf("knowledge: gh %s list: %w", command, err)
		}
		var items []githubItem
		if err := json.Unmarshal(result.Stdout, &items); err != nil {
			return nil, nil, fmt.Errorf("knowledge: decode gh %s list: %w", command, err)
		}
		for _, item := range items {
			title := "#" + strconv.Itoa(item.Number) + " " + item.Title
			documents = append(documents, document{sourceID: idGitHub + kind + "/" + strconv.Itoa(item.Number), revision: item.UpdatedAt,
				title: title, uri: item.URL, license: licenseUnknown, text: textbound.Prefix(title+"\n\n"+item.Body, maxProseBytes)})
		}
	}
	return documents, nil, nil
}

// readState is the harness state directory's JSON files as their key names:
// where a setting lives is findable, and no value, which may be a secret, is
// ever read into the index.
func (feeder *Feeder) readState(ctx context.Context) ([]document, []string, error) {
	entries, err := feeder.state.ReadDir(".")
	if err != nil {
		return nil, nil, err
	}
	var documents []document
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), jsonSuffix) {
			continue
		}
		content, err := feeder.state.ReadFile(entry.Name())
		if err != nil {
			return nil, nil, err
		}
		var decoded any // an arbitrary JSON document; only its shape is read
		if json.Unmarshal(content, &decoded) != nil {
			continue
		}
		keys := map[string]bool{}
		keyPaths("", decoded, keys)
		if len(keys) == 0 {
			continue
		}
		text := "Key names of " + entry.Name() + " in the harness state directory (values are not indexed):\n" +
			strings.Join(slices.Sorted(maps.Keys(keys)), "\n") + "\n"
		documents = append(documents, document{sourceID: idState + entry.Name(), revision: digest(text), title: stateTitle + entry.Name(),
			uri: "file://" + filepath.Join(feeder.stateRoot, entry.Name()), license: licenseUnknown, text: text})
	}
	return documents, nil, nil
}

// keyPaths adds every key path of value under prefix: objects by key, array
// elements as [].
func keyPaths(prefix string, value any, keys map[string]bool) {
	switch shaped := value.(type) {
	case map[string]any:
		for key, child := range shaped {
			name := key
			if prefix != "" {
				name = prefix + keySeparator + key
			}
			keys[name] = true
			keyPaths(name, child, keys)
		}
	case []any:
		for _, child := range shaped {
			keyPaths(prefix+keyArray, child, keys)
		}
	}
}

// readRuns is each run's conversation whose event log grew since the last
// pass: the user and assistant text, never a tool's input or output.
func (feeder *Feeder) readRuns(ctx context.Context) ([]document, []string, error) {
	entries, err := feeder.state.ReadDir(".")
	if err != nil {
		return nil, nil, err
	}
	var documents []document
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if !entry.IsDir() {
			continue
		}
		info, err := stdfs.Stat(feeder.state, path.Join(entry.Name(), session.EventsFile))
		if errors.Is(err, stdfs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if feeder.runs[entry.Name()] == info.Size() {
			continue
		}
		text, err := conversation(filepath.Join(feeder.stateRoot, entry.Name()))
		if err != nil {
			return nil, nil, err
		}
		feeder.runs[entry.Name()] = info.Size()
		if text == "" {
			continue
		}
		documents = append(documents, document{sourceID: idRun + entry.Name(), revision: digest(text), title: entry.Name() + runsTitleSuffix,
			uri: "file://" + filepath.Join(feeder.stateRoot, entry.Name(), session.EventsFile), license: licenseUnknown, text: text})
	}
	return documents, nil, nil
}

// conversation is a run's user and assistant text, in order, bounded.
func conversation(directory string) (string, error) {
	records, err := session.ReadRecords(directory, func(record *session.Record) bool {
		return record.EventType == session.EventTypeUser || record.EventType == session.EventTypeAssistant
	})
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for index := range records {
		said := strings.Join(records[index].Text(), "\n\n")
		if said == "" {
			continue
		}
		role := roleAssistant
		if records[index].EventType == session.EventTypeUser {
			role = roleUser
		}
		text.WriteString(role + said + "\n\n")
		if text.Len() > maxProseBytes {
			break
		}
	}
	return textbound.Prefix(text.String(), maxProseBytes), nil
}

// digest is a text's revision when its source has none of its own.
func digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
