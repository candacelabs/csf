// Copyright 2026 Candace Labs

package intake

import (
	"context"
	"fmt"
	"io"
	stdhttp "net/http"
	"slices"
	"strconv"
	"time"

	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
)

// GitHub REST request and response headers the poller speaks.
const (
	headerAccept             = "Accept"
	headerAPIVersion         = "X-GitHub-Api-Version"
	headerAuthorization      = "Authorization"
	headerUserAgent          = "User-Agent"
	headerIfNoneMatch        = "If-None-Match"
	headerETag               = "ETag"
	headerPollInterval       = "X-Poll-Interval"
	headerRateLimitRemaining = "X-RateLimit-Remaining"
	headerRateLimitReset     = "X-RateLimit-Reset"
	headerRetryAfter         = "Retry-After"

	acceptGitHubJSON = "application/vnd.github+json"
	apiVersion       = "2022-11-28"
	userAgent        = "candace-csf-intake"
	bearerPrefix     = "Bearer "
	exhausted        = "0"
)

// maxResponseBytes bounds one feed page; GitHub's largest is far smaller.
const maxResponseBytes = 8 << 20

// defaultRateLimitWait is the backoff when GitHub reports a rate limit but no
// usable reset time.
const defaultRateLimitWait = time.Minute

// cursorKey identifies one feed of one repository.
type cursorKey struct {
	repository string
	feed       string
}

// outcome is what one poll asks of the round: how long to wait before the
// next round at least, and whether to stop this round now.
type outcome struct {
	wait time.Duration
	stop bool
}

// poll is the poller goroutine. It owns the ETag of every feed, so no lock
// guards them: one round polls every repository's every feed, then waits the
// longest interval any response asked for, or the rate-limit backoff.
func (intake *EventIntake) poll(ctx context.Context) error {
	etags := map[cursorKey]string{}
	for {
		wait := intake.round(ctx, etags)
		if ctx.Err() != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-intake.clock.After(min(wait, intake.maxBackoff)):
		}
	}
}

// round polls each feed of each repository once, stopping early when a
// response starts a rate-limit backoff.
func (intake *EventIntake) round(ctx context.Context, etags map[cursorKey]string) time.Duration {
	wait := intake.interval
	for _, repository := range intake.repositories {
		for _, source := range feeds {
			result := intake.pollFeed(ctx, etags, repository, source)
			wait = max(wait, result.wait)
			if result.stop || ctx.Err() != nil {
				return wait
			}
		}
	}
	return wait
}

// newPollRequest builds one GitHub REST request, conditional on etag when the
// feed has answered before.
func (intake *EventIntake) newPollRequest(ctx context.Context, target string, etag string) (*stdhttp.Request, error) {
	request, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(headerAccept, acceptGitHubJSON)
	request.Header.Set(headerAPIVersion, apiVersion)
	request.Header.Set(headerUserAgent, userAgent)
	if intake.token != "" {
		request.Header.Set(headerAuthorization, bearerPrefix+intake.token)
	}
	if etag != "" {
		request.Header.Set(headerIfNoneMatch, etag)
	}
	return request, nil
}

// pollFeed sends one conditional request and offers what it returns.
func (intake *EventIntake) pollFeed(ctx context.Context, etags map[cursorKey]string, repository string, source feed) outcome {
	key := cursorKey{repository: repository, feed: source.name}
	request, err := intake.newPollRequest(ctx, intake.baseURL+source.path(repository), etags[key])
	if err != nil {
		return intake.pollFailed(repository, source, err)
	}
	intake.counters.polls.Add(1)
	response, err := intake.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return outcome{stop: true}
		}
		return intake.pollFailed(repository, source, err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))

	result := outcome{wait: secondsHeader(response.Header, headerPollInterval)}
	if until, limited := intake.rateLimited(response); limited {
		intake.counters.rateLimited.Add(1)
		intake.counters.backoffUntil.Store(&until)
		backoff := until.Sub(intake.clock.Now())
		intake.logger.Warn("intake: rate limited; backing off", append(logAttributes(repository, source),
			logStatus, response.StatusCode, logUntil, until)...)
		result = outcome{wait: max(result.wait, backoff), stop: true}
		if response.StatusCode >= stdhttp.StatusBadRequest {
			return result
		}
		// A successful response that spent the window's last request still
		// carries its page; offer it, then stop the round.
	}
	switch response.StatusCode {
	case stdhttp.StatusNotModified:
		intake.counters.notModified.Add(1)
		return result
	case stdhttp.StatusOK:
	default:
		failed := intake.pollFailed(repository, source, fmt.Errorf("GitHub answered %s", response.Status))
		return outcome{wait: max(result.wait, failed.wait), stop: result.stop}
	}
	if readErr != nil {
		failed := intake.pollFailed(repository, source, fmt.Errorf("read response: %w", readErr))
		return outcome{wait: max(result.wait, failed.wait), stop: result.stop}
	}
	events, err := source.normalize(repository, body)
	if err != nil {
		failed := intake.pollFailed(repository, source, err)
		return outcome{wait: max(result.wait, failed.wait), stop: result.stop}
	}
	// Remember the ETag only once the page was understood, so a page that
	// failed to decode is fetched again rather than answered 304.
	if etag := response.Header.Get(headerETag); etag != "" {
		etags[key] = etag
	}
	slices.SortStableFunc(events, func(left *intakev1.Event, right *intakev1.Event) int {
		return left.GetOccurredAt().AsTime().Compare(right.GetOccurredAt().AsTime())
	})
	for _, event := range events {
		if err := intake.offer(ctx, event); err != nil {
			return outcome{stop: true}
		}
	}
	return result
}

// pollFailed counts and logs a failed poll; the round goes on.
func (intake *EventIntake) pollFailed(repository string, source feed, err error) outcome {
	intake.counters.pollFailures.Add(1)
	intake.logger.Warn("intake: poll failed", append(logAttributes(repository, source), logError, err)...)
	return outcome{}
}

// rateLimited reports whether response exhausted or exceeded a rate limit,
// and until when to back off. A secondary limit says how long in Retry-After;
// the primary limit says when its window resets in X-RateLimit-Reset.
func (intake *EventIntake) rateLimited(response *stdhttp.Response) (time.Time, bool) {
	now := intake.clock.Now()
	refused := response.StatusCode == stdhttp.StatusForbidden || response.StatusCode == stdhttp.StatusTooManyRequests
	if retry := secondsHeader(response.Header, headerRetryAfter); refused && retry > 0 {
		return now.Add(retry), true
	}
	if response.Header.Get(headerRateLimitRemaining) != exhausted {
		return time.Time{}, false
	}
	reset, err := strconv.ParseInt(response.Header.Get(headerRateLimitReset), 10, 64)
	if err != nil {
		return now.Add(defaultRateLimitWait), true
	}
	return time.Unix(reset, 0), true
}

// secondsHeader reads a header holding a whole number of seconds; anything
// else is zero.
func secondsHeader(header stdhttp.Header, name string) time.Duration {
	seconds, err := strconv.ParseInt(header.Get(name), 10, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
