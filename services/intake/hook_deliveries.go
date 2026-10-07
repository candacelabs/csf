// Copyright 2026 Candace Labs

package intake

import (
	"context"
	"fmt"
	"strings"

	"github.com/candacelabs/csf/io/net/github"
)

// hookPageSize is GitHub's largest page.
const hookPageSize = 100

// GitHubHookDeliveries is recovery's [IHookDeliveries] over CSF's GitHub
// protocol client.
type GitHubHookDeliveries struct {
	client *github.GitHubClient
}

var _ IHookDeliveries = (*GitHubHookDeliveries)(nil)

// NewGitHubHookDeliveries builds recovery's view of client.
func NewGitHubHookDeliveries(client *github.GitHubClient) (*GitHubHookDeliveries, error) {
	if client == nil {
		return nil, github.ErrNoHTTPClient
	}
	return &GitHubHookDeliveries{client: client}, nil
}

// ListHooks lists the repository's webhooks, up to one page of 100.
func (hooks *GitHubHookDeliveries) ListHooks(ctx context.Context, repository string) ([]Hook, error) {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return nil, err
	}
	perPage := hookPageSize
	response, err := hooks.client.ReposlistWebhooksWithResponse(ctx, owner, name, &github.ReposlistWebhooksParams{PerPage: &perPage})
	if err != nil {
		return nil, fmt.Errorf("intake: list the hooks of %s: %w", repository, err)
	}
	if response.JSON200 == nil {
		return nil, &github.StatusError{Status: response.StatusCode(), Message: string(response.Body)}
	}
	listed := make([]Hook, 0, len(*response.JSON200))
	for _, hook := range *response.JSON200 {
		listed = append(listed, Hook{ID: int64(hook.Id)})
		if hook.Config.Url != nil {
			listed[len(listed)-1].URL = *hook.Config.Url
		}
	}
	return listed, nil
}

// ListHookDeliveries lists one page of a hook's deliveries, newest first.
func (hooks *GitHubHookDeliveries) ListHookDeliveries(ctx context.Context, repository string, hook int64, cursor string) ([]HookDelivery, string, error) {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return nil, "", err
	}
	perPage := hookPageSize
	params := &github.ReposlistWebhookDeliveriesParams{PerPage: &perPage}
	if cursor != "" {
		params.Cursor = &cursor
	}
	response, err := hooks.client.ReposlistWebhookDeliveriesWithResponse(ctx, owner, name, int(hook), params)
	if err != nil {
		return nil, "", fmt.Errorf("intake: list the deliveries of hook %d: %w", hook, err)
	}
	if response.JSON200 == nil {
		return nil, "", &github.StatusError{Status: response.StatusCode(), Message: string(response.Body)}
	}
	page := make([]HookDelivery, 0, len(*response.JSON200))
	for _, attempt := range *response.JSON200 {
		page = append(page, HookDelivery{
			ID: attempt.Id, GUID: attempt.Guid, Event: attempt.Event, DeliveredAt: attempt.DeliveredAt,
			StatusCode: attempt.StatusCode, Redelivery: attempt.Redelivery,
		})
	}
	return page, github.NextCursor(response.HTTPResponse), nil
}

// RedeliverHookDelivery asks GitHub to deliver one attempt again.
func (hooks *GitHubHookDeliveries) RedeliverHookDelivery(ctx context.Context, repository string, hook int64, delivery int64) error {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return err
	}
	_, err = hooks.client.RedeliverHookDelivery(ctx, owner, name, int(hook), delivery)
	return err
}

func splitRepository(repository string) (string, string, error) {
	if err := ValidateRepository(repository); err != nil {
		return "", "", err
	}
	owner, name, _ := strings.Cut(repository, "/")
	return owner, name, nil
}
