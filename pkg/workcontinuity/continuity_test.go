package workcontinuity_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/candacelabs/csf/pkg/telemetry"
	"github.com/candacelabs/csf/pkg/workcontinuity"
	telemetryv1 "github.com/candacelabs/csf/proto/candace/telemetry/v1"
	workv1 "github.com/candacelabs/csf/proto/candace/work/v1"
)

const taskURL = "https://github.com/example/project/issues/1"

func sourceFixture() *workv1.SourceSnapshot {
	return &workv1.SourceSnapshot{Issue: &workv1.SourceIssue{Number: 1, Title: "Continue a bounded task", HtmlUrl: taskURL, State: workcontinuity.IssueOpen, Body: "Acceptance: reproduce the saved check."}}
}

func checkpointFixture(snapshot *workv1.SourceSnapshot) *workv1.Checkpoint {
	GinkgoHelper()
	trace, err := telemetry.NewTraceContext(telemetry.TraceFlagsSampled)
	Expect(err).NotTo(HaveOccurred())
	return &workv1.Checkpoint{SchemaVersion: 1, Id: "first", TaskUrl: taskURL, Revision: strings.Repeat("a", 40), Owner: "implementer", Status: workv1.WorkStatus_WORK_STATUS_ACTIVE, NextAction: "Run the saved integration check and append its receipt.", TaskFingerprint: workcontinuity.Fingerprint(snapshot.Issue), RecordedAt: timestamppb.Now(), TraceContext: trace}
}

func appendFixture(snapshot *workv1.SourceSnapshot, checkpoint *workv1.Checkpoint) {
	GinkgoHelper()
	body, err := workcontinuity.EncodeCheckpoint(checkpoint)
	Expect(err).NotTo(HaveOccurred())
	snapshot.Comments = append(snapshot.Comments, &workv1.SourceComment{Id: int64(len(snapshot.Comments) + 1), HtmlUrl: fmt.Sprintf("%s#issuecomment-%d", taskURL, len(snapshot.Comments)+1), Body: body, User: &workv1.SourceUser{Login: "maintainer"}, AuthorAssociation: "OWNER"})
}

var _ = Describe("handoff replay", func() {
	var snapshot *workv1.SourceSnapshot
	var checkpoint *workv1.Checkpoint
	BeforeEach(func() { snapshot = sourceFixture(); checkpoint = checkpointFixture(snapshot) })
	resume := func() *workv1.ResumeRecord {
		return workcontinuity.Resume(snapshot, checkpoint.Revision, time.Now(), workcontinuity.DefaultMaxAge)
	}

	It("identifies absent handoffs without guessing a next action", func() {
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_MISSING))
		Expect(resume().Checkpoint).To(BeNil())
	})
	It("round-trips instructions as data, including shell-looking content", func() {
		checkpoint.NextAction = "Inspect $(never-execute-this) and `literal instructions`; ask before expanding authority."
		appendFixture(snapshot, checkpoint)
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_READY))
		Expect(proto.Equal(resume().Checkpoint, checkpoint)).To(BeTrue())
	})
	It("ignores ordinary discussion but not malformed marked receipts", func() {
		snapshot.Comments = append(snapshot.Comments, &workv1.SourceComment{Body: "ordinary discussion"})
		appendFixture(snapshot, checkpoint)
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_READY))
		snapshot.Comments = append(snapshot.Comments, &workv1.SourceComment{Body: workcontinuity.CheckpointMarker + " truncated", User: &workv1.SourceUser{Login: "maintainer"}, AuthorAssociation: "OWNER"})
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_INVALID))
	})
	It("rejects incomplete or unknown checkpoint fields", func() {
		snapshot.Comments = []*workv1.SourceComment{{Body: workcontinuity.CheckpointMarker + "\n```json\n{\"id\":\"x\",\"imaginary\":true}\n```", User: &workv1.SourceUser{Login: "maintainer"}, AuthorAssociation: "OWNER"}}
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_INVALID))
	})
	It("accepts identical retry receipts but rejects ID reuse", func() {
		appendFixture(snapshot, checkpoint)
		appendFixture(snapshot, checkpoint)
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_READY))
		checkpoint.NextAction = "A conflicting next action"
		appendFixture(snapshot, checkpoint)
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_CONFLICT))
	})
	DescribeTable("ignores fabricated and malformed handoffs from untrusted publishers", func(association, login string) {
		appendFixture(snapshot, checkpoint)
		forged := proto.Clone(checkpoint).(*workv1.Checkpoint)
		forged.Id = "forged"
		forged.PredecessorId = checkpoint.Id
		forged.Owner = "attacker"
		appendFixture(snapshot, forged)
		comment := snapshot.Comments[1]
		comment.AuthorAssociation = association
		comment.User.Login = login
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_READY))
		Expect(resume().Checkpoint.Id).To(Equal(checkpoint.Id))
		comment.Body = workcontinuity.CheckpointMarker + " malformed"
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_READY))
		snapshot.Comments = snapshot.Comments[1:]
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_MISSING))
	}, Entry("outsider", "NONE", "visitor"), Entry("prior contributor", "CONTRIBUTOR", "visitor"), Entry("organization member", "MEMBER", "visitor"), Entry("unknown association", "NEW_ROLE", "visitor"), Entry("missing association", "", "visitor"), Entry("missing identity", "OWNER", ""))
	It("accepts GitHub-attested invited collaborators", func() {
		appendFixture(snapshot, checkpoint)
		snapshot.Comments[0].AuthorAssociation = "COLLABORATOR"
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_READY))
	})
	DescribeTable("rejects evidence URLs the board cannot render", func(address string) {
		checkpoint.Evidence = []*workv1.Evidence{{Description: "receipt", Url: address, Result: "checked"}}
		_, err := workcontinuity.EncodeCheckpoint(checkpoint)
		Expect(err).To(MatchError(workcontinuity.ErrInvalid))
		// A receipt persisted by an older publisher becomes a visible INVALID
		// record; its unsafe URL must not take down the whole board projection.
		body, err := protojson.Marshal(checkpoint)
		Expect(err).NotTo(HaveOccurred())
		snapshot.Comments = []*workv1.SourceComment{{Body: workcontinuity.CheckpointMarker + "\n```json\n" + string(body) + "\n```", User: &workv1.SourceUser{Login: "maintainer"}, AuthorAssociation: "OWNER"}}
		record := resume()
		Expect(record.Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_INVALID))
	}, Entry("invalid host escape", "http://%zz"), Entry("invalid path escape", "https://example.invalid/%zz"), Entry("newline", "https://example.invalid/\nreceipt"), Entry("no host", "https:///receipt"))

	It("reconstructs predecessor order rather than trusting comment order", func() {
		second := proto.Clone(checkpoint).(*workv1.Checkpoint)
		second.Id = "second"
		second.PredecessorId = checkpoint.Id
		appendFixture(snapshot, second)
		appendFixture(snapshot, checkpoint)
		Expect(resume().Checkpoint.Id).To(Equal("second"))
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_READY))
	})
	It("rejects competing tips", func() {
		appendFixture(snapshot, checkpoint)
		checkpoint.PredecessorId = checkpoint.Id
		checkpoint.Id = "branch-a"
		appendFixture(snapshot, checkpoint)
		checkpoint.Id = "branch-b"
		appendFixture(snapshot, checkpoint)
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_CONFLICT))
	})
	It("rejects missing predecessors and cycles", func() {
		checkpoint.PredecessorId = "missing"
		appendFixture(snapshot, checkpoint)
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_CONFLICT))
		snapshot.Comments = nil
		checkpoint.PredecessorId = checkpoint.Id
		appendFixture(snapshot, checkpoint)
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_CONFLICT))
	})
	It("rejects receipts for another task", func() {
		checkpoint.TaskUrl = "https://github.com/example/project/issues/2"
		appendFixture(snapshot, checkpoint)
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_CONFLICT))
	})
	DescribeTable("detects stale evidence",
		func(change func(snapshot *workv1.SourceSnapshot, checkpoint *workv1.Checkpoint)) {
			change(snapshot, checkpoint)
			appendFixture(snapshot, checkpoint)
			Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_STALE))
		},
		Entry("changed scope", func(snapshot *workv1.SourceSnapshot, checkpoint *workv1.Checkpoint) {
			snapshot.Issue.Body += " new acceptance"
		}),
		Entry("changed issue state", func(snapshot *workv1.SourceSnapshot, checkpoint *workv1.Checkpoint) {
			snapshot.Issue.State = workcontinuity.IssueClosed
		}),
		Entry("old receipt", func(snapshot *workv1.SourceSnapshot, checkpoint *workv1.Checkpoint) {
			checkpoint.RecordedAt = timestamppb.New(time.Now().Add(-48 * time.Hour))
		}),
		Entry("future receipt", func(snapshot *workv1.SourceSnapshot, checkpoint *workv1.Checkpoint) {
			checkpoint.RecordedAt = timestamppb.New(time.Now().Add(time.Hour))
		}),
	)
	It("reports a moved checkout explicitly", func() {
		appendFixture(snapshot, checkpoint)
		record := workcontinuity.Resume(snapshot, strings.Repeat("b", 40), time.Now(), time.Hour)
		Expect(record.Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_STALE))
	})
	It("does not confuse GitHub's comment-updated timestamp with changed scope", func() {
		appendFixture(snapshot, checkpoint)
		snapshot.Issue.UpdatedAt = timestamppb.Now()
		Expect(resume().Condition).To(Equal(workv1.ResumeCondition_RESUME_CONDITION_READY))
	})
	It("refuses unsupported status and unsupported completion claims", func() {
		checkpoint.Status = workv1.WorkStatus(99)
		_, err := workcontinuity.EncodeCheckpoint(checkpoint)
		Expect(err).To(MatchError(ContainSubstring("known status")))
		checkpoint.Status = workv1.WorkStatus_WORK_STATUS_DONE
		_, err = workcontinuity.EncodeCheckpoint(checkpoint)
		Expect(err).To(MatchError(ContainSubstring("requires evidence")))
		checkpoint.Status = workv1.WorkStatus_WORK_STATUS_BLOCKED
		_, err = workcontinuity.EncodeCheckpoint(checkpoint)
		Expect(err).To(MatchError(ContainSubstring("requires a blocker")))
	})
})

var _ = Describe("composed checkpoint service", func() {
	It("publishes, resumes in a fresh service, retries idempotently, and emits linked telemetry", func() {
		ctx := context.Background()
		snapshot := sourceFixture()
		checkpoint := checkpointFixture(snapshot)
		var trace bytes.Buffer
		logger, err := telemetry.NewJSONLLogger(&trace, "work-test", "harness")
		Expect(err).NotTo(HaveOccurred())
		source := NewMockISource(gomock.NewController(GinkgoT()))
		source.EXPECT().Load(gomock.Any(), taskURL).Return(snapshot, nil).Times(4)
		source.EXPECT().Append(gomock.Any(), taskURL, gomock.Any()).DoAndReturn(func(ctx context.Context, task, body string) (*workv1.SourceComment, error) {
			comment := &workv1.SourceComment{Id: 1, HtmlUrl: task + "#issuecomment-1", Body: body, User: &workv1.SourceUser{Login: "maintainer"}, AuthorAssociation: "OWNER"}
			snapshot.Comments = append(snapshot.Comments, comment)
			return comment, nil
		}).Times(1)
		service, err := workcontinuity.NewContinuity(workcontinuity.WithSource(source), workcontinuity.WithLogger(logger))
		Expect(err).NotTo(HaveOccurred())
		published, err := service.Checkpoint(ctx, taskURL, checkpoint)
		Expect(err).NotTo(HaveOccurred())
		fresh, err := workcontinuity.NewContinuity(workcontinuity.WithSource(source), workcontinuity.WithLogger(logger))
		Expect(err).NotTo(HaveOccurred())
		resumed, err := fresh.Resume(ctx, taskURL, checkpoint.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(published.Checkpoint, resumed.Checkpoint)).To(BeTrue())
		_, err = fresh.Checkpoint(ctx, taskURL, checkpoint)
		Expect(err).NotTo(HaveOccurred())
		lines := strings.Split(strings.TrimSpace(trace.String()), "\n")
		Expect(lines).To(HaveLen(3))
		for _, line := range lines {
			log := &telemetryv1.LogRecord{}
			Expect(protojson.Unmarshal([]byte(line), log)).To(Succeed())
			Expect(telemetry.ValidateLogRecord(log)).To(Succeed())
			Expect(log.TraceContext.TraceId).To(Equal(checkpoint.TraceContext.TraceId))
			Expect(log.Attributes["checkpoint_id"]).To(Equal(checkpoint.Id))
		}
	})
	It("does not append if writing the intent trace fails", func() {
		file, err := os.Create(filepath.Join(GinkgoT().TempDir(), "closed-trace"))
		Expect(err).NotTo(HaveOccurred())
		Expect(file.Close()).To(Succeed())
		logger, err := telemetry.NewJSONLLogger(file, "work-test", "harness")
		Expect(err).NotTo(HaveOccurred())
		snapshot := sourceFixture()
		source := NewMockISource(gomock.NewController(GinkgoT()))
		source.EXPECT().Load(gomock.Any(), taskURL).Return(snapshot, nil)
		service, err := workcontinuity.NewContinuity(workcontinuity.WithSource(source), workcontinuity.WithLogger(logger))
		Expect(err).NotTo(HaveOccurred())
		_, err = service.Checkpoint(context.Background(), taskURL, checkpointFixture(snapshot))
		Expect(err).To(HaveOccurred())
	})
	It("returns persistence failure rather than claiming success", func() {
		logger, err := telemetry.NewJSONLLogger(io.Discard, "work-test", "harness")
		Expect(err).NotTo(HaveOccurred())
		snapshot := sourceFixture()
		source := NewMockISource(gomock.NewController(GinkgoT()))
		source.EXPECT().Load(gomock.Any(), taskURL).Return(snapshot, nil)
		source.EXPECT().Append(gomock.Any(), taskURL, gomock.Any()).Return(nil, errors.New("unavailable"))
		service, err := workcontinuity.NewContinuity(workcontinuity.WithSource(source), workcontinuity.WithLogger(logger))
		Expect(err).NotTo(HaveOccurred())
		_, err = service.Checkpoint(context.Background(), taskURL, checkpointFixture(snapshot))
		Expect(err).To(MatchError(ContainSubstring("retry the same ID")))
	})
	DescribeTable("rejects invalid publication timestamps without appending or logging intent", func(offset time.Duration) {
		var trace bytes.Buffer
		logger, err := telemetry.NewJSONLLogger(&trace, "work-test", "harness")
		Expect(err).NotTo(HaveOccurred())
		snapshot := sourceFixture()
		checkpoint := checkpointFixture(snapshot)
		checkpoint.RecordedAt = timestamppb.New(time.Now().Add(offset))
		source := NewMockISource(gomock.NewController(GinkgoT()))
		source.EXPECT().Load(gomock.Any(), taskURL).Return(snapshot, nil)
		service, err := workcontinuity.NewContinuity(workcontinuity.WithSource(source), workcontinuity.WithLogger(logger))
		Expect(err).NotTo(HaveOccurred())
		_, err = service.Checkpoint(context.Background(), taskURL, checkpoint)
		Expect(err).To(MatchError(workcontinuity.ErrStale))
		Expect(trace.Len()).To(BeZero())
	}, Entry("expired", -48*time.Hour), Entry("future", time.Hour))
})
