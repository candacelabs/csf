// Copyright 2026 Candace Labs

package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	redeliverPathFormat = "/repos/%s/%s/hooks/%d/deliveries/%d/attempts"
	headerLink          = "Link"
	linkNext            = `rel="next"`
	linkSeparator       = ","
	linkParameter       = ";"
	cursorParameter     = "cursor"
)

// RedeliverHookDelivery asks GitHub to deliver one attempt to a repository
// webhook again. GitHub answers 202 with no body the description names, so
// the generated client does not carry it.
func (client *GitHubClient) RedeliverHookDelivery(ctx context.Context, owner string, repo string, hook int, delivery int64) (RateLimit, error) {
	path := fmt.Sprintf(redeliverPathFormat, url.PathEscape(owner), url.PathEscape(repo), hook, delivery)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, Server+path, nil)
	if err != nil {
		return RateLimit{}, fmt.Errorf("github: redeliver %d: %w", delivery, err)
	}
	if err := client.authenticate(ctx, request); err != nil {
		return RateLimit{}, err
	}
	response, err := client.http.Do(request)
	if err != nil {
		return RateLimit{}, fmt.Errorf("github: redeliver %d: %w", delivery, err)
	}
	defer func() { _ = response.Body.Close() }()
	rate := RateLimitOf(response)
	if response.StatusCode != http.StatusAccepted {
		content, _ := io.ReadAll(io.LimitReader(response.Body, maxAnswerBytes))
		return rate, &StatusError{Status: response.StatusCode, Message: string(content)}
	}
	return rate, nil
}

// NextCursor is the cursor of the next page a cursor-paginated response's
// Link header points to: "" on the last page.
func NextCursor(response *http.Response) string {
	if response == nil {
		return ""
	}
	for link := range strings.SplitSeq(response.Header.Get(headerLink), linkSeparator) {
		target, parameters, found := strings.Cut(link, linkParameter)
		if !found || !strings.Contains(parameters, linkNext) {
			continue
		}
		parsed, err := url.Parse(strings.Trim(strings.TrimSpace(target), "<>"))
		if err != nil {
			return ""
		}
		return parsed.Query().Get(cursorParameter)
	}
	return ""
}
