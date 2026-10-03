// Copyright 2026 Candace Labs

package intake

import (
	"context"
	stdhttp "net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
)

// Unit specs of the package's internals: the feed normalizers, the queue's
// deduplication memory, and the rate-limit arithmetic.

const (
	unitRepository = "example/widgets"
	issueComment   = `[{"id":"9","type":"IssueCommentEvent","actor":{"login":"octo"},"created_at":"2026-10-01T12:00:00Z",
	  "payload":{"action":"created","issue":{"number":3},
	   "comment":{"id":77,"html_url":"https://github.com/example/widgets/issues/3#issuecomment-77","body":"ping","user":{"login":"octo"}}}},
	 {"id":"10","type":"IssueCommentEvent","actor":{"login":"octo"},"created_at":"2026-10-01T12:00:01Z",
	  "payload":{"action":"edited","issue":{"number":3},"comment":{"id":77}}},
	 {"id":"11","type":"PullRequestReviewCommentEvent","actor":{"login":"octo"},"created_at":"2026-10-01T12:00:02Z",
	  "payload":{"action":"created","pull_request":{"number":4},
	   "comment":{"id":78,"html_url":"https://github.com/example/widgets/pull/4#discussion_r78","body":"nit","user":{"login":"octo"}}}}]`
	mixedRuns = `{"workflow_runs":[
	 {"id":1,"name":"CI","run_attempt":2,"conclusion":"failure","head_branch":"main","updated_at":"2026-10-01T12:00:00Z","pull_requests":[]},
	 {"id":2,"name":"CI","run_attempt":1,"conclusion":"success","head_branch":"main","updated_at":"2026-10-01T12:00:00Z","pull_requests":[]}]}`
)

var _ = Describe("feed normalizers", func() {
	It("keeps created comments on issues and pull request diffs and drops edits", func() {
		events, err := normalizeRepositoryEvents(unitRepository, []byte(issueComment))
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(2))
		Expect(events[0].GetId()).To(Equal("github/comment/77"))
		Expect(events[0].GetSubject().GetKind()).To(Equal(intakev1.SubjectKind_SUBJECT_KIND_ISSUE))
		Expect(events[0].GetSubject().GetNumber()).To(BeEquivalentTo(3))
		Expect(events[1].GetId()).To(Equal("github/review_comment/78"))
		Expect(events[1].GetKind()).To(Equal(intakev1.EventKind_EVENT_KIND_REVIEW_COMMENT))
		Expect(events[1].GetSubject().GetKind()).To(Equal(intakev1.SubjectKind_SUBJECT_KIND_PULL_REQUEST))
		for _, event := range events {
			Expect(intakev1.ValidateEvent(event)).To(Succeed())
			Expect(intakev1.ValidateSubject(event.GetSubject())).To(Succeed())
		}
	})

	It("keeps only failed runs, one identifier per attempt, on the repository when no PR is linked", func() {
		events, err := normalizeFailedRuns(unitRepository, []byte(mixedRuns))
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
		Expect(events[0].GetId()).To(Equal("github/run/1/attempt/2"))
		Expect(events[0].GetSubject().GetKind()).To(Equal(intakev1.SubjectKind_SUBJECT_KIND_REPOSITORY))
		Expect(events[0].GetSummary()).To(Equal("CI failed on main (attempt 2)"))
	})

	It("reports a page it cannot decode", func() {
		_, err := normalizeRepositoryEvents(unitRepository, []byte(`{"message":"Not Found"}`))
		Expect(err).To(MatchError(ContainSubstring("decode repository events")))
		_, err = normalizeFailedRuns(unitRepository, []byte(`[`))
		Expect(err).To(MatchError(ContainSubstring("decode workflow runs")))
	})

	It("cuts a long excerpt on a rune boundary", func() {
		long := strings.Repeat("é", maxSummaryBytes)
		cut := excerpt(long)
		Expect(len(cut)).To(BeNumerically("<=", maxSummaryBytes))
		Expect(strings.ToValidUTF8(cut, "?")).To(Equal(cut))
	})

	It("registers every feed with a name, a path and a normalizer", func() {
		names := map[string]bool{}
		for _, registered := range feeds {
			Expect(registered.name).NotTo(BeEmpty())
			Expect(registered.path(unitRepository)).To(HavePrefix("/repos/" + unitRepository + "/"))
			Expect(registered.normalize).NotTo(BeNil())
			Expect(names).NotTo(HaveKey(registered.name))
			names[registered.name] = true
		}
	})
})

var _ = Describe("MemoryEventQueue", func() {
	event := func(identifier string) *intakev1.Event { return &intakev1.Event{Id: identifier} }

	It("accepts an identifier once and hands events out oldest first", func(ctx SpecContext) {
		queue, err := NewMemoryEventQueue()
		Expect(err).NotTo(HaveOccurred())
		for _, identifier := range []string{"a", "b", "a"} {
			_, err := queue.Offer(ctx, event(identifier))
			Expect(err).NotTo(HaveOccurred())
		}
		first, err := queue.Take(ctx)
		Expect(err).NotTo(HaveOccurred())
		second, err := queue.Take(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect([]string{first.GetId(), second.GetId()}).To(Equal([]string{"a", "b"}))
		Expect(queue.order).To(Equal([]string{"a", "b"}))
	})

	It("rejects an event without an identifier", func(ctx SpecContext) {
		queue, err := NewMemoryEventQueue()
		Expect(err).NotTo(HaveOccurred())
		_, err = queue.Offer(ctx, event(""))
		Expect(err).To(MatchError(ErrInvalidEvent))
	})

	It("forgets an event whose offer was abandoned, and the oldest past its memory", func(ctx SpecContext) {
		queue, err := NewMemoryEventQueue(WithQueueCapacity(1), WithDeduplicationMemory(2))
		Expect(err).NotTo(HaveOccurred())
		accepted, err := queue.Offer(ctx, event("a"))
		Expect(err).NotTo(HaveOccurred())
		Expect(accepted).To(BeTrue())
		full, cancel := context.WithCancel(ctx)
		cancel()
		_, err = queue.Offer(full, event("b"))
		Expect(err).To(MatchError(context.Canceled))
		Expect(queue.seen).NotTo(HaveKey("b"))

		_, err = queue.Take(ctx)
		Expect(err).NotTo(HaveOccurred())
		for _, identifier := range []string{"b", "c"} {
			accepted, err := queue.Offer(ctx, event(identifier))
			Expect(err).NotTo(HaveOccurred())
			Expect(accepted).To(BeTrue())
			_, err = queue.Take(ctx)
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(queue.seen).NotTo(HaveKey("a"), "the oldest identifier is forgotten past the memory")
	})

	It("refuses a memory smaller than its capacity", func() {
		_, err := NewMemoryEventQueue(WithQueueCapacity(8), WithDeduplicationMemory(4))
		Expect(err).To(MatchError(ContainSubstring("must cover the queue capacity")))
		_, err = NewMemoryEventQueue(WithQueueCapacity(0))
		Expect(err).To(MatchError(ContainSubstring("capacity must be positive")))
	})
})

var _ = Describe("rate-limit arithmetic", func() {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	intake := &EventIntake{configuration: configuration{clock: Clock{Now: func() time.Time { return now }}}}
	response := func(status int, header map[string]string) *stdhttp.Response {
		headers := stdhttp.Header{}
		for name, value := range header {
			headers.Set(name, value)
		}
		return &stdhttp.Response{StatusCode: status, Header: headers}
	}

	It("is not limited while requests remain", func() {
		_, limited := intake.rateLimited(response(stdhttp.StatusOK, map[string]string{headerRateLimitRemaining: "12"}))
		Expect(limited).To(BeFalse())
	})

	It("waits a minute when the reset time is unusable", func() {
		until, limited := intake.rateLimited(response(stdhttp.StatusForbidden, map[string]string{headerRateLimitRemaining: exhausted}))
		Expect(limited).To(BeTrue())
		Expect(until).To(Equal(now.Add(defaultRateLimitWait)))
	})

	It("ignores Retry-After on a response that was not refused", func() {
		_, limited := intake.rateLimited(response(stdhttp.StatusOK, map[string]string{headerRetryAfter: "30"}))
		Expect(limited).To(BeFalse())
	})

	It("reads whole seconds and nothing else", func() {
		Expect(secondsHeader(stdhttp.Header{headerPollInterval: {"60"}}, headerPollInterval)).To(Equal(time.Minute))
		Expect(secondsHeader(stdhttp.Header{headerPollInterval: {"soon"}}, headerPollInterval)).To(BeZero())
		Expect(secondsHeader(stdhttp.Header{}, headerPollInterval)).To(BeZero())
	})
})
