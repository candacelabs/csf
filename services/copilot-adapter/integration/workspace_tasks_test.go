package integration_test

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
	"github.com/candacelabs/csf/pkg/telemetry"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/candacelabs/csf/pkg/workcontinuity"
	workv1 "github.com/candacelabs/csf/proto/candace/work/v1"
	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

type taskAuthorityFixture struct {
	mu         sync.Mutex
	snapshot   *workv1.SourceSnapshot
	continuity *workcontinuity.Continuity
}

func newTaskAuthority() *taskAuthorityFixture {
	GinkgoHelper()
	authority := &taskAuthorityFixture{snapshot: &workv1.SourceSnapshot{Issue: &workv1.SourceIssue{
		Number: 207, Title: "Shared widget board", HtmlUrl: workspaceTaskURL, State: workcontinuity.IssueOpen, Body: "Show committed task progress.",
	}}}
	source := NewMockISource(gomock.NewController(GinkgoT()))
	source.EXPECT().Load(gomock.Any(), workspaceTaskURL).DoAndReturn(func(ctx context.Context, taskURL string) (*workv1.SourceSnapshot, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		authority.mu.Lock()
		defer authority.mu.Unlock()
		return proto.Clone(authority.snapshot).(*workv1.SourceSnapshot), nil
	}).AnyTimes()
	source.EXPECT().Append(gomock.Any(), workspaceTaskURL, gomock.Any()).DoAndReturn(func(ctx context.Context, taskURL, body string) (*workv1.SourceComment, error) {
		authority.mu.Lock()
		defer authority.mu.Unlock()
		return authority.append(body), nil
	}).AnyTimes()
	logger, err := telemetry.NewJSONLLogger(io.Discard, "workspace-test", "continuity")
	Expect(err).NotTo(HaveOccurred())
	authority.continuity, err = workcontinuity.NewContinuity(workcontinuity.WithSource(source), workcontinuity.WithLogger(logger))
	Expect(err).NotTo(HaveOccurred())
	return authority
}

func (authority *taskAuthorityFixture) append(body string) *workv1.SourceComment {
	identifier := int64(len(authority.snapshot.Comments) + 1)
	comment := &workv1.SourceComment{Id: identifier, HtmlUrl: fmt.Sprintf("%s#issuecomment-%d", workspaceTaskURL, identifier),
		Body: body, User: &workv1.SourceUser{Login: "maintainer"}, AuthorAssociation: "OWNER"}
	authority.snapshot.Comments = append(authority.snapshot.Comments, comment)
	return proto.Clone(comment).(*workv1.SourceComment)
}

func (authority *taskAuthorityFixture) initialCheckpoint() *workv1.ResumeRecord {
	GinkgoHelper()
	record, err := authority.continuity.Checkpoint(context.Background(), workspaceTaskURL, &workv1.Checkpoint{
		Id: uuid.NewString(), Revision: strings.Repeat("a", 40), Owner: "implementer",
		Status: workv1.WorkStatus_WORK_STATUS_ACTIVE, NextAction: "Verify the next transition.",
	})
	Expect(err).NotTo(HaveOccurred())
	return record
}

var renderedCardRegion = regexp.MustCompile(`<article[^>]*data-gotth-region="([^"]+)"`)

func workspaceCardRegion(browser *livetest.Client) string {
	GinkgoHelper()
	for _, update := range browser.Snapshot().Patch.Updates {
		if match := renderedCardRegion.FindStringSubmatch(update.HTML); len(match) == 2 {
			return match[1]
		}
	}
	Fail("initial snapshot contains no rendered card region")
	return ""
}

func awaitWorkspaceText(browser *livetest.Client, text string) *livetest.Frame {
	GinkgoHelper()
	frame := browser.Await(text, 10*time.Second, func(frame *livetest.Frame) bool {
		if frame.Patch == nil {
			return false
		}
		for _, update := range frame.Patch.Updates {
			if strings.Contains(update.HTML, text) {
				return true
			}
		}
		return false
	})
	browser.Ack(frame.Patch.ServerSeq)
	return frame
}

var _ = Describe("authoritative workspace task transitions", func() {
	It("confirms a browser move through a saved checkpoint and updates both clients", func() {
		authority := newTaskAuthority()
		initial := authority.initialCheckpoint()
		fixture := newWorkspaceFixture(copilotadapter.WithTaskContinuity(authority.continuity))
		_, err := fixture.adapter.LinkWorkspaceTask(context.Background(), fixture.sessionID, workspaceTaskURL, 0)
		Expect(err).NotTo(HaveOccurred())
		first, second := workspaceBrowser(fixture), workspaceBrowser(fixture)
		first.Send("kanban.move", workspaceCardRegion(first), map[string]string{
			"status": workv1.WorkStatus_WORK_STATUS_BLOCKED.String(), "expected_checkpoint": initial.Checkpoint.Id,
			"next_action": "Retry when the dependency is ready.", "reason": "Waiting for the declared dependency.",
		})
		for _, browser := range []*livetest.Client{first, second} {
			awaitWorkspaceText(browser, "Retry when the dependency is ready.")
		}
		record, err := authority.continuity.Resume(context.Background(), workspaceTaskURL, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(record.Checkpoint.PredecessorId).To(Equal(initial.Checkpoint.Id))
		Expect(record.Checkpoint.Status).To(Equal(workv1.WorkStatus_WORK_STATUS_BLOCKED))
		Expect(record.CheckpointUrl).NotTo(BeEmpty())
	})

	It("refuses completion without evidence and refuses an outdated predecessor", func() {
		authority := newTaskAuthority()
		initial := authority.initialCheckpoint()
		fixture := newWorkspaceFixture(copilotadapter.WithTaskContinuity(authority.continuity))
		link, err := fixture.adapter.LinkWorkspaceTask(context.Background(), fixture.sessionID, workspaceTaskURL, 0)
		Expect(err).NotTo(HaveOccurred())
		_, err = fixture.adapter.MoveWorkspaceTask(context.Background(), link, initial.Checkpoint.Id, workv1.WorkStatus_WORK_STATUS_DONE, "Finished", "")
		Expect(err).To(HaveOccurred())
		moved, err := fixture.adapter.MoveWorkspaceTask(context.Background(), link, initial.Checkpoint.Id, workv1.WorkStatus_WORK_STATUS_QUEUED, "Run the next check", "")
		Expect(err).NotTo(HaveOccurred())
		_, err = fixture.adapter.MoveWorkspaceTask(context.Background(), link, initial.Checkpoint.Id, workv1.WorkStatus_WORK_STATUS_ACTIVE, "Run a conflicting check", "")
		Expect(err).To(MatchError(ContainSubstring(workcontinuity.ErrStale.Error())))
		record, err := authority.continuity.Resume(context.Background(), workspaceTaskURL, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(record.Checkpoint.Id).To(Equal(moved.Checkpoint.Id))
	})

	It("rejects a move against the previous task after the session is relinked", func() {
		authority := newTaskAuthority()
		initial := authority.initialCheckpoint()
		fixture := newWorkspaceFixture(copilotadapter.WithTaskContinuity(authority.continuity))
		link, err := fixture.adapter.LinkWorkspaceTask(context.Background(), fixture.sessionID, workspaceTaskURL, 0)
		Expect(err).NotTo(HaveOccurred())
		_, err = fixture.adapter.LinkWorkspaceTask(context.Background(), fixture.sessionID, "https://github.com/example/project/issues/208", link.Generation)
		Expect(err).NotTo(HaveOccurred())
		_, err = fixture.adapter.MoveWorkspaceTask(context.Background(), link, initial.Checkpoint.Id, workv1.WorkStatus_WORK_STATUS_QUEUED, "Do not modify the old task", "")
		Expect(err).To(MatchError(ContainSubstring(workcontinuity.ErrStale.Error())))
		record, err := authority.continuity.Resume(context.Background(), workspaceTaskURL, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(record.Checkpoint.Id).To(Equal(initial.Checkpoint.Id))
	})

	It("does not retain a canceled observer's failure for the next browser", func() {
		authority := newTaskAuthority()
		initial := authority.initialCheckpoint()
		fixture := newWorkspaceFixture(copilotadapter.WithTaskContinuity(authority.continuity))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		canceled := fixture.adapter.WorkspaceTask(ctx, workspaceTaskURL, false)
		Expect(canceled.Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_SOURCE_ERROR))
		retried := fixture.adapter.WorkspaceTask(context.Background(), workspaceTaskURL, false)
		Expect(retried.Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_READY))
		Expect(retried.Checkpoint.Id).To(Equal(initial.Checkpoint.Id))
	})

	It("ingests an external checkpoint explicitly and broadcasts its observed result", func() {
		authority := newTaskAuthority()
		initial := authority.initialCheckpoint()
		fixture := newWorkspaceFixture(copilotadapter.WithTaskContinuity(authority.continuity))
		_, err := fixture.adapter.LinkWorkspaceTask(context.Background(), fixture.sessionID, workspaceTaskURL, 0)
		Expect(err).NotTo(HaveOccurred())
		browser := workspaceBrowser(fixture)
		external := proto.Clone(initial.Checkpoint).(*workv1.Checkpoint)
		external.Id, external.PredecessorId, external.RecordedAt = uuid.NewString(), initial.Checkpoint.Id, timestamppb.Now()
		external.NextAction = "Inspect the external evidence."
		_, err = authority.continuity.Checkpoint(context.Background(), workspaceTaskURL, external)
		Expect(err).NotTo(HaveOccurred())
		_, err = fixture.adapter.RefreshWorkspace(context.Background(), api.RefreshWorkspaceRequestObject{})
		Expect(err).NotTo(HaveOccurred())
		awaitWorkspaceText(browser, external.NextAction)
	})
})
