// Copyright 2026 Candace Labs

package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/ipc/proc"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

const (
	verbInit = "init"
	verbChat = "chat"
	repoFlag = "repo"

	// SampleDirectory is where init writes the sample assignment, relative
	// to the repository root.
	SampleDirectory = ".csf/assignments/sample"
	recipeFile      = "agent.json"
	briefFile       = "brief.md"
	sampleFileMode  = 0o644
	chatPath        = "/chat/"
	// hostStartLimit bounds how long init waits for a host it started to
	// record itself and answer.
	hostStartLimit = 60 * time.Second
	hostStartPoll  = 250 * time.Millisecond

	// recipeDirectory is where the templates sit inside the binary.
	recipeDirectory = "recipe/"
	// The placeholders the templates carry, each filled for the repository.
	placeholderAssignment = "@ASSIGNMENT_ID@"
	placeholderShortID    = "@SHORT_ID@"
	placeholderRepository = "@REPOSITORY_ID@"
	placeholderPath       = "@REPOSITORY_PATH@"
	placeholderBranch     = "@BASE_BRANCH@"
	// shortIDLength is the assignment-id prefix the sample branch carries.
	shortIDLength = 8

	gitExecutable = "git"
	// unsafeCharacters may not appear in the repository path or branch the
	// recipe's JSON strings carry.
	unsafeCharacters = `"\`
	// temporarySuffix names the file an atomic write renames into place.
	temporarySuffix = ".tmp"
)

// The git queries init makes: the repository's root, and its branch.
var (
	gitTopLevel = []string{"rev-parse", "--show-toplevel"}
	gitBranch   = []string{"symbolic-ref", "--quiet", "--short", "HEAD"}
)

// recipeTemplates are the sample assignment init fills for a repository.
//
//go:embed recipe/agent.json recipe/brief.md
var recipeTemplates embed.FS

var (
	errNotRoot   = errors.New("run csf init at the root of a git repository, or give -repo")
	errNoBranch  = errors.New("the repository is on no branch (detached HEAD); check out the branch the sample should start from")
	errQuoted    = errors.New("paths and branch names may not contain a quote or a backslash")
	errNoStarted = errors.New("the host was started but did not answer in time; read its log")
)

// initRepository is `csf init`: it writes the sample assignment into the
// repository, then registers with the running host, or starts one.
func initRepository(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbInit, flag.ContinueOnError)
	repoPath := flags.String(repoFlag, ".", "root of the git repository to initialize")
	stateDirectory := flags.String(stateFlag, "", "host state directory (default ~/"+defaultStateDirectory+")")
	listen := flags.String(listenFlag, defaultListen, "address a host started by init serves on")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errUsage
	}
	repo, branch, err := repositoryRoot(ctx, launcher, *repoPath)
	if err != nil {
		return err
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	assignment, err := writeSample(repo, branch)
	if err != nil {
		return err
	}
	recipe := filepath.Join(repo, SampleDirectory, recipeFile)
	fmt.Fprintf(output, "[PASS] Wrote %s and %s (assignment %s, branch csf/sample-%s from %s).\n",
		recipe, briefFile, assignment, assignment[:shortIDLength], branch)
	endpoint, err := ensureHost(ctx, launcher, state, *listen, output)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "\nNext:\n  %[1]s submit -recipe %[2]s\n  %[1]s events -assignment %[3]s\n  %[1]s chat -assignment %[3]s   (%[4]s%[5]s%[3]s)\n",
		commandName, recipe, assignment, endpoint, chatPath)
	return nil
}

// repositoryRoot is the repository's root and current branch, read with git.
func repositoryRoot(ctx context.Context, launcher *proc.HostLauncher, path string) (string, string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", "", err
	}
	toplevel, err := gitOutput(ctx, launcher, absolute, gitTopLevel...)
	if err != nil || toplevel != absolute {
		return "", "", errNotRoot
	}
	branch, err := gitOutput(ctx, launcher, absolute, gitBranch...)
	if err != nil {
		return "", "", errNoBranch
	}
	if strings.ContainsAny(absolute+branch, unsafeCharacters) {
		return "", "", errQuoted
	}
	return absolute, branch, nil
}

func gitOutput(ctx context.Context, launcher *proc.HostLauncher, directory string, arguments ...string) (string, error) {
	result, err := launcher.Run(ctx, proc.Command{Executable: gitExecutable, Arguments: arguments, Directory: directory})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// writeSample fills the embedded templates for the repository, each file
// written atomically, and returns the new assignment's identifier.
func writeSample(repo string, branch string) (string, error) {
	assignment := uuid.NewString()
	replacer := strings.NewReplacer(
		placeholderAssignment, assignment,
		placeholderShortID, assignment[:shortIDLength],
		placeholderRepository, filepath.Base(repo),
		placeholderPath, repo,
		placeholderBranch, branch,
	)
	directory := filepath.Join(repo, SampleDirectory)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	for _, name := range []string{recipeFile, briefFile} {
		template, err := recipeTemplates.ReadFile(recipeDirectory + name)
		if err != nil {
			return "", err
		}
		if err := writeAtomically(filepath.Join(directory, name), replacer.Replace(string(template)), sampleFileMode); err != nil {
			return "", err
		}
	}
	return assignment, nil
}

// ensureHost registers with the host the state directory records when it
// answers, and otherwise starts one in the background and waits until it
// records itself and answers. It returns the host's endpoint.
func ensureHost(ctx context.Context, launcher *proc.HostLauncher, state string, listen string, output io.Writer) (string, error) {
	if endpoint, err := resolveEndpoint("", state); err == nil && hostAnswers(ctx, endpoint) {
		fmt.Fprintf(output, "[PASS] Registered with the running host at %s.\n", endpoint)
		return endpoint, nil
	}
	if err := detachHost(launcher, state, verbServe, hostLogFile, []string{"-" + stateFlag, state, "-" + listenFlag, listen}, output); err != nil {
		return "", err
	}
	waiting, cancel := context.WithTimeout(ctx, hostStartLimit)
	defer cancel()
	ticker := time.NewTicker(hostStartPoll)
	defer ticker.Stop()
	for {
		if endpoint, err := resolveEndpoint("", state); err == nil && hostAnswers(waiting, endpoint) {
			fmt.Fprintf(output, "[PASS] Started the host at %s.\n", endpoint)
			return endpoint, nil
		}
		select {
		case <-waiting.Done():
			return "", fmt.Errorf("%w: %s", errNoStarted, filepath.Join(state, hostLogFile))
		case <-ticker.C:
		}
	}
}

// hostAnswers reports whether a host serves the session operations at
// endpoint.
func hostAnswers(ctx context.Context, endpoint string) bool {
	client, err := csf.NewClient(endpoint, &http.Client{Timeout: hostStartPoll * 8})
	if err != nil {
		return false
	}
	_, err = client.ListAgentSessions(ctx, &harnessv1.ListAgentSessionsRequest{})
	return err == nil
}

// chatAddress is `csf chat`: it prints the address of a session's chat page
// on the running host.
func chatAddress(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbChat, flag.ContinueOnError)
	endpoint := flags.String(endpointFlag, "", "host address (default: the one <state>/"+HostRecordFile+" records)")
	stateDirectory := flags.String(stateFlag, "", "state directory holding "+HostRecordFile+" (default ~/"+defaultStateDirectory+")")
	assignment := flags.String(assignmentFlag, "", "assignment identifier")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errUsage
	}
	if *assignment == "" {
		return errNoAssignment
	}
	target, err := resolveEndpoint(*endpoint, *stateDirectory)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, strings.TrimRight(target, "/")+chatPath+*assignment)
	return err
}

// writeAtomically replaces path with content, so a reader never sees a
// half-written file.
func writeAtomically(path string, content string, mode os.FileMode) error {
	temporary := path + temporarySuffix
	if err := os.WriteFile(temporary, []byte(content), mode); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
