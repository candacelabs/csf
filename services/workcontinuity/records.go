// Package workcontinuity validates and records task checkpoints so work can
// resume across agents and sessions. Callers own its source and logger; calls
// run synchronously under the supplied context, with no background workers or
// service lifecycle. It owns no listener, credentials, or agent execution.
package workcontinuity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/telemetry"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	workv1 "github.com/candacelabs/csf/proto/candace/work/v1"
)

const (
	CheckpointMarker                     = "<!-- candace-work-checkpoint:v1 -->"
	checkpointPrefix                     = CheckpointMarker + "\n```json\n"
	checkpointSuffix                     = "\n```"
	maxCheckpointBytes                   = 60000
	SchemaVersion                 uint32 = 1
	DefaultMaxAge                        = 24 * time.Hour
	IssueOpen                            = "open"
	IssueClosed                          = "closed"
	fingerprintFieldSeparator            = "\x00"
	httpScheme                           = "http"
	githubOwnerAssociation               = "OWNER"
	githubCollaboratorAssociation        = "COLLABORATOR"
)

var (
	ErrInvalid  = errors.New("invalid checkpoint")
	ErrConflict = errors.New("checkpoint history conflict")
	ErrStale    = errors.New("checkpoint precondition changed")
)

type receipt struct {
	checkpoint *workv1.Checkpoint
	url        string
}

// Fingerprint excludes updated_at: posting a checkpoint itself updates an issue.
func Fingerprint(issue *workv1.SourceIssue) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{issue.GetHtmlUrl(), issue.GetTitle(), issue.GetBody(), issue.GetState()}, fingerprintFieldSeparator)))
	return hex.EncodeToString(digest[:])
}

// ValidateCheckpoint combines generated field invariants with lifecycle rules.
func ValidateCheckpoint(checkpoint *workv1.Checkpoint) error {
	if err := workv1.ValidateCheckpoint(checkpoint); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if checkpoint.RecordedAt == nil {
		return fmt.Errorf("%w: recorded_at is required", ErrInvalid)
	}
	if err := checkpoint.RecordedAt.CheckValid(); err != nil {
		return fmt.Errorf("%w: recorded_at: %w", ErrInvalid, err)
	}
	if err := telemetry.ValidateTraceContext(checkpoint.TraceContext); err != nil {
		return fmt.Errorf("%w: trace_context: %w", ErrInvalid, err)
	}
	if checkpoint.Status <= workv1.WorkStatus_WORK_STATUS_UNSPECIFIED || checkpoint.Status > workv1.WorkStatus_WORK_STATUS_DONE {
		return fmt.Errorf("%w: a known status is required", ErrInvalid)
	}
	if strings.TrimSpace(checkpoint.Owner) == "" || strings.TrimSpace(checkpoint.NextAction) == "" {
		return fmt.Errorf("%w: owner and next_action must not be whitespace", ErrInvalid)
	}
	if checkpoint.Status == workv1.WorkStatus_WORK_STATUS_DONE && len(checkpoint.Evidence) == 0 {
		return fmt.Errorf("%w: done requires evidence", ErrInvalid)
	}
	if (checkpoint.Status == workv1.WorkStatus_WORK_STATUS_BLOCKED || checkpoint.Status == workv1.WorkStatus_WORK_STATUS_OPERATOR) && len(checkpoint.Blockers) == 0 {
		return fmt.Errorf("%w: blocked/operator status requires a blocker", ErrInvalid)
	}
	for _, evidence := range checkpoint.Evidence {
		if err := workv1.ValidateEvidence(evidence); err != nil {
			return fmt.Errorf("%w: evidence: %w", ErrInvalid, err)
		}
		parsed, err := url.Parse(evidence.Url)
		if err != nil || parsed.Host == "" || (parsed.Scheme != httpScheme && parsed.Scheme != githubScheme) {
			return fmt.Errorf("%w: evidence URL must be an absolute HTTP(S) URL", ErrInvalid)
		}
	}
	return nil
}

// EncodeCheckpoint produces the one marked comment format consumed on resume.
func EncodeCheckpoint(checkpoint *workv1.Checkpoint) (string, error) {
	if err := ValidateCheckpoint(checkpoint); err != nil {
		return "", err
	}
	data, err := (protojson.MarshalOptions{UseProtoNames: true, Indent: "  "}).Marshal(checkpoint)
	if err != nil {
		return "", fmt.Errorf("encode checkpoint: %w", err)
	}
	body := checkpointPrefix + string(data) + checkpointSuffix
	if len(body) > maxCheckpointBytes {
		return "", fmt.Errorf("%w: checkpoint exceeds comment limit", ErrInvalid)
	}
	return body, nil
}

func decodeComment(comment *workv1.SourceComment) (*workv1.Checkpoint, error) {
	body := strings.TrimSpace(comment.GetBody())
	if !strings.HasPrefix(body, checkpointPrefix) || !strings.HasSuffix(body, checkpointSuffix) || len(body) > maxCheckpointBytes {
		return nil, fmt.Errorf("%w: malformed checkpoint comment %s", ErrInvalid, comment.GetHtmlUrl())
	}
	checkpoint := &workv1.Checkpoint{}
	data := strings.TrimSuffix(strings.TrimPrefix(body, checkpointPrefix), checkpointSuffix)
	if err := protojson.Unmarshal([]byte(data), checkpoint); err != nil {
		return nil, fmt.Errorf("%w: decode comment: %w", ErrInvalid, err)
	}
	if err := ValidateCheckpoint(checkpoint); err != nil {
		return nil, err
	}
	return checkpoint, nil
}

// history requires one complete linear chain. Comment arrival order is not a
// lock; competing descendants remain conflicts instead of last-writer-wins.
func history(snapshot *workv1.SourceSnapshot) ([]receipt, error) {
	byID := make(map[string]receipt)
	for _, comment := range snapshot.GetComments() {
		if !trustedPublisher(comment) || !strings.Contains(comment.GetBody(), CheckpointMarker) {
			continue
		}
		checkpoint, err := decodeComment(comment)
		if err != nil {
			return nil, err
		}
		if checkpoint.TaskUrl != snapshot.GetIssue().GetHtmlUrl() {
			return nil, fmt.Errorf("%w: receipt belongs to another task", ErrConflict)
		}
		if previous, exists := byID[checkpoint.Id]; exists {
			if !proto.Equal(previous.checkpoint, checkpoint) {
				return nil, fmt.Errorf("%w: ID %s has different payloads", ErrConflict, checkpoint.Id)
			}
			continue
		}
		byID[checkpoint.Id] = receipt{checkpoint: checkpoint, url: comment.GetHtmlUrl()}
	}
	children := make(map[string]receipt)
	for _, item := range byID {
		parent := item.checkpoint.PredecessorId
		if _, exists := children[parent]; exists {
			return nil, fmt.Errorf("%w: competing descendants of %q", ErrConflict, parent)
		}
		children[parent] = item
	}
	var chain []receipt
	parent := ""
	for len(chain) < len(byID) {
		next, exists := children[parent]
		if !exists {
			return nil, fmt.Errorf("%w: missing predecessor or cycle", ErrConflict)
		}
		chain = append(chain, next)
		parent = next.checkpoint.Id
	}
	return chain, nil
}

// Only GitHub-attested repository owners and invited collaborators publish
// handoffs. Organization membership or contribution alone is not authority.
// Other comments are discussion, even when their body imitates our marker.
func trustedPublisher(comment *workv1.SourceComment) bool {
	if strings.TrimSpace(comment.GetUser().GetLogin()) == "" {
		return false
	}
	switch comment.GetAuthorAssociation() {
	case githubOwnerAssociation, githubCollaboratorAssociation:
		return true
	default:
		return false
	}
}

func checkpointFresh(checkpoint *workv1.Checkpoint, now time.Time, maxAge time.Duration) bool {
	age := now.Sub(checkpoint.RecordedAt.AsTime())
	return age >= 0 && age <= maxAge
}

// Resume evaluates a source snapshot without executing any saved instructions.
// READY means current handoff data, not authorization or a live-worker claim.
func Resume(snapshot *workv1.SourceSnapshot, revision string, now time.Time, maxAge time.Duration) *workv1.ResumeRecord {
	record := &workv1.ResumeRecord{Issue: snapshot.GetIssue(), ExpectedRevision: revision, CheckedAt: timestamppb.New(now), Condition: workv1.ResumeCondition_RESUME_CONDITION_MISSING}
	chain, err := history(snapshot)
	if err != nil {
		record.Condition = workv1.ResumeCondition_RESUME_CONDITION_INVALID
		if errors.Is(err, ErrConflict) {
			record.Condition = workv1.ResumeCondition_RESUME_CONDITION_CONFLICT
		}
		record.Findings = []string{err.Error()}
		return record
	}
	if len(chain) == 0 {
		record.Findings = []string{"No checkpoint exists; scope and next action have not been handed off."}
		return record
	}
	tip := chain[len(chain)-1]
	record.Checkpoint, record.CheckpointUrl = tip.checkpoint, tip.url
	record.Condition = workv1.ResumeCondition_RESUME_CONDITION_READY
	if revision != "" && tip.checkpoint.Revision != revision {
		record.Findings = append(record.Findings, "Checkout revision differs from the checkpoint; inspect the intervening commits.")
	}
	if tip.checkpoint.TaskFingerprint != Fingerprint(snapshot.Issue) {
		record.Findings = append(record.Findings, "Issue scope, title or state changed since the checkpoint.")
	}
	if !checkpointFresh(tip.checkpoint, now, maxAge) {
		record.Findings = append(record.Findings, "Checkpoint timestamp is in the future or outside the freshness window.")
	}
	if len(record.Findings) > 0 {
		record.Condition = workv1.ResumeCondition_RESUME_CONDITION_STALE
	}
	return record
}
