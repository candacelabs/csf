// Copyright 2026 Candace Labs

package verbs

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
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/atomicfile"
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
	// hostAnswerTimeout bounds the one call that asks whether a recorded
	// host answers.
	hostAnswerTimeout = 2 * time.Second

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
	errNotRoot  = errors.New("run csf init at the root of a git repository, or give -repo")
	errNoBranch = errors.New("the repository is on no branch (detached HEAD); check out the branch the sample should start from")
	errQuoted   = errors.New("paths and branch names may not contain a quote or a backslash")
)

// initRepository is `csf init`: it writes the sample assignment into the
// repository, then registers with the running host, or starts one.
func initRepository(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbInit, flag.ContinueOnError)
	repoPath := flags.String(repoFlag, ".", "root of the git repository to initialize")
	stateDirectory := flags.String(stateFlag, "", "host state directory (default ~/"+defaultStateDirectory+")")
	listen := flags.String(listenFlag, defaultListen, "address a host started by init serves on")
	_ = flags.Bool(outputFlag, false, "render output as a markdown table instead of JSON (informational output only)")
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
	if err := recordLoopCheckout(ctx, launcher, state, repo, output); err != nil {
		return err
	}
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

// recordLoopCheckout records the repository as the mining loop's checkout
// when none is recorded yet and it has the origin the loop's tickets live
// on, so the host init starts mounts the loop with no flag.
func recordLoopCheckout(ctx context.Context, launcher *proc.HostLauncher, state string, repo string, output io.Writer) error {
	recorded, err := readLoopSettings(state)
	if err != nil || recorded.Repository != "" {
		return err
	}
	if _, err := gitOutput(ctx, launcher, repo, gitRemote, gitGetURL, gitOrigin); err != nil {
		fmt.Fprintf(output, "[SKIP] %s has no origin, so the mining loop has no tickets to work from; it is not recorded as the loop's checkout.\n", repo)
		return nil
	}
	if _, err := recordLoopSettings(state, loopSettings{Repository: repo}); err != nil {
		return err
	}
	fmt.Fprintf(output, "[PASS] Recorded %s as the mining loop's checkout.\n", repo)
	return nil
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
		if err := atomicfile.WriteFile(filepath.Join(directory, name), []byte(replacer.Replace(string(template))), sampleFileMode); err != nil {
			return "", err
		}
	}
	return assignment, nil
}

// ensureHost registers with the host the state directory records when it
// answers, and otherwise starts one in the background, which returns once
// the host is ready. It returns the host's endpoint.
func ensureHost(ctx context.Context, launcher *proc.HostLauncher, state string, listen string, output io.Writer) (string, error) {
	if endpoint, err := resolveEndpoint("", state); err == nil && hostAnswers(ctx, endpoint) {
		fmt.Fprintf(output, "[PASS] Registered with the running host at %s.\n", endpoint)
		return endpoint, nil
	}
	if err := detachHost(ctx, launcher, state, verbServe, hostLogFile, []string{"-" + stateFlag, state, "-" + listenFlag, listen}, lifecycleDeadline, output); err != nil {
		return "", err
	}
	endpoint, err := resolveEndpoint("", state)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(output, "[PASS] Started the host at %s.\n", endpoint)
	return endpoint, nil
}

// hostAnswers reports whether a host serves the session operations at
// endpoint.
func hostAnswers(ctx context.Context, endpoint string) bool {
	client, err := csf.NewClient(endpoint, &http.Client{Timeout: hostAnswerTimeout}, clientWarnings)
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
	_ = flags.Bool(outputFlag, false, "render output as a markdown table instead of JSON (URL output only)")
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
