// Copyright 2026 Candace Labs

package verbs

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/net/github"
	"github.com/candacelabs/csf/services/cloud"
	dispatchservice "github.com/candacelabs/csf/services/dispatch"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/intake"
	"github.com/candacelabs/csf/services/ouroboros"
)

const githubWebhookMountName = "github webhook"

// newWebhookReceiver builds the GitHub webhook receiver: deliveries verified
// against github.webhook_secret in <state>/providers.json, read at each
// delivery; a comment, review or failed check queued in the session that owns
// the pull request; a merge into main marked on the dispatcher; and, with the
// GitHub protocol client and a repository, the deliveries missed while this
// host was down asked for again at start.
func newWebhookReceiver(state string, logger *slog.Logger, sessions *harness.AgentSessionService,
	dispatcher *dispatchservice.DispatchService, client *github.GitHubClient, repositories []string) (*intake.WebhookReceiver, error) {
	providers := filepath.Join(state, cloud.ProvidersFile)
	options := []intake.WebhookOption{
		intake.WithStateDirectory(state),
		intake.WithWebhookSecret(func() ([]byte, error) { return intake.ReadWebhookSecret(providers) }),
		intake.WithEventRoutes(intake.SessionRoute(sessions), intake.MergeRoute(dispatcher.MarkPullRequestMerged)),
		intake.WithWebhookLogger(logger),
	}
	switch {
	case client == nil:
		logger.Warn("harness: no GitHub protocol client, so missed webhook deliveries are not recovered")
	case len(repositories) == 0:
		logger.Warn("harness: no -" + viewsRepositoryFlag + " and no mining loop checkout with an origin, so missed webhook deliveries are not recovered")
	default:
		hooks, err := intake.NewGitHubHookDeliveries(client)
		if err != nil {
			return nil, err
		}
		options = append(options, intake.WithRecovery(hooks, repositories...))
	}
	return intake.NewWebhookReceiver(options...)
}

// webhookRepositories is the repository whose hooks deliver here: the one
// the views measure, given with -views-repository or read from the mining
// loop checkout's origin.
func webhookRepositories(ctx context.Context, launcher *proc.HostLauncher, repository string, loopCheckout string) []string {
	if repository == "" && loopCheckout != "" {
		if origin, err := gitOutput(ctx, launcher, loopCheckout, gitRemote, gitGetURL, gitOrigin); err == nil {
			repository, _ = ouroboros.RepositorySlug(origin)
		}
	}
	if repository == "" {
		return nil
	}
	return []string{repository}
}
