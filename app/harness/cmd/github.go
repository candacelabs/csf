// Copyright 2026 Candace Labs

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/candacelabs/csf/csf/githubtools"
	"github.com/candacelabs/csf/io/ipc/proc"
	ionet "github.com/candacelabs/csf/io/net"
	"github.com/candacelabs/csf/io/net/github"
	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/runtime/config"
	"github.com/candacelabs/csf/services/bootstrap"
)

const (
	// verbGitHub calls one GitHub tool in-process: csf github TOOL < input.json.
	verbGitHub = "github"
	// GitHubTokenVariable and GHTokenVariable name the token the GitHub
	// protocol client authenticates with, read through the config
	// capability, the first that is set.
	GitHubTokenVariable = "GITHUB_TOKEN"
	GHTokenVariable     = "GH_TOKEN"
	// GhConfigVariable and HomeVariable locate gh's config directory, whose
	// hosts.yml holds the token gh itself uses when neither variable is set.
	GhConfigVariable    = "GH_CONFIG_DIR"
	HomeVariable        = "HOME"
	githubClientTimeout = 2 * time.Minute
	listFlagName        = "list"
)

// newGitHubClient is the GitHub protocol client over the host network,
// authenticated with the token [githubToken] finds.
func newGitHubClient(environment config.Environment) (*github.GitHubClient, error) {
	token, err := githubToken(environment)
	if err != nil {
		return nil, err
	}
	httpClient, err := iohttp.NewHTTPClient(ionet.NewHostNetwork(), iohttp.WithClientTimeout(githubClientTimeout))
	if err != nil {
		return nil, err
	}
	return github.NewGitHubClient(httpClient, token)
}

// githubToken is GITHUB_TOKEN, else GH_TOKEN, else the token in gh's own
// hosts.yml: the credential the operator's gh already holds, which a session
// container sees through the gh config the harness mounts read-only.
func githubToken(environment config.Environment) (string, error) {
	if token := environment.First(GitHubTokenVariable, GHTokenVariable); token != "" {
		return token, nil
	}
	directory := environment.Raw(GhConfigVariable)
	if directory == "" {
		directory = filepath.Join(environment.Raw(HomeVariable), ghConfigHome)
	}
	content, err := os.ReadFile(filepath.Join(directory, github.GhHostsFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", github.ErrNoToken
	}
	if err != nil {
		return "", err
	}
	return github.TokenFromGhHosts(content)
}

// newGitHubTools are the GitHub tools recording under state. When the loop's
// checkout is known, the merge quality gate measures the revisions it compares
// through the golden measurer over that checkout.
func newGitHubTools(environment config.Environment, state string, logger *slog.Logger, launcher proc.ILauncher, repository string) (*githubtools.GitHubTools, error) {
	client, err := newGitHubClient(environment)
	if err != nil {
		return nil, err
	}
	options := []githubtools.GitHubToolsOption{
		githubtools.WithClient(client),
		githubtools.WithStateDirectory(state),
		githubtools.WithLogger(logger),
	}
	if repository != "" {
		options = append(options, githubtools.WithGoldenMetrics(newGoldenMeasurer(launcher, repository, bootstrap.Measure)))
	}
	return githubtools.NewGitHubTools(options...)
}

// githubVerb is csf github TOOL [-state DIR] < input.json: one GitHub tool,
// called in-process through the protocol client as the operator, recorded
// in the host's GitHub log like a call over MCP. csf github -list names the
// tools.
func githubVerb(ctx context.Context, launcher *proc.HostLauncher, arguments []string, input io.Reader, output io.Writer, diagnostics io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbGitHub, flag.ContinueOnError)
	stateDirectory := flags.String(stateFlag, "", "directory holding the host's GitHub log (default ~/"+defaultStateDirectory+")")
	list := flags.Bool(listFlagName, false, "list the GitHub tools")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	settings, err := readLoopSettings(state)
	if err != nil {
		return err
	}
	tools, err := newGitHubTools(config.OSEnvironment(), state, slog.New(slog.NewTextHandler(diagnostics, nil)), launcher, settings.Repository)
	if err != nil {
		return err
	}
	if *list {
		_, err := fmt.Fprintln(output, strings.Join(tools.Names(), "\n"))
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: %s %s TOOL < input.json, or -%s", commandName, verbGitHub, listFlagName)
	}
	request, err := io.ReadAll(input)
	if err != nil {
		return err
	}
	response, err := tools.Invoke(ctx, flags.Arg(0), request)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, string(response))
	return err
}
