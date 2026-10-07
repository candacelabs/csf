// Package reconcile turns approved desired state into fenced node-agent calls.
//
// Service owns the write path. It resolves the requested source to an exact
// app revision, decides placement over the authoritative fleet snapshot,
// records desired state and its receipts in the relational store, and only
// then calls the selected node's agent. It implements operator.IReconciler, so
// Prepare and ReconcileApproved are always separated by Core's approval gate.
//
// Callers may rely on Prepare being read-only — it resolves and validates a
// revision without touching a node or desired state — on ReconcileApproved
// refusing to apply an input whose revision is no longer byte-for-byte
// identical to the approved one, on mutation being blocked unless Warden
// reports an authoritative view with a leader and quorum, and on every run
// reaching a durable terminal outcome before it is reported complete.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/io/ipc/proc"
	deployv1 "github.com/candacelabs/csf/proto/candace/deploy/v1"
	"github.com/candacelabs/csf/services/deploy"
	"github.com/candacelabs/csf/services/deploy/fleet"
	"github.com/candacelabs/csf/services/deploy/internal/storedb"
	"github.com/candacelabs/csf/services/deploy/operator"
	"github.com/candacelabs/csf/services/deploy/store"
	"github.com/candacelabs/csf/services/nodeexec/client"
)

const (
	runFinalizationTimeout = 5 * time.Second
	gitExecutable          = "git"
)

// Service owns placement, relational desired state, and node reconciliation.
type Service struct {
	workspace string
	labels    map[string]map[string]string
	fleet     *fleet.Client
	agents    *client.Client
	store     *store.Store
	now       func() time.Time
	launcher  proc.ILauncher
}

// NewService constructs a mutator. Dependencies are kept explicit so neither
// the web process nor the selected agent harness can reach the Docker socket.
// Git runs through launcher, the process-boundary capability.
func NewService(
	launcher proc.ILauncher,
	workspace string,
	labels map[string]map[string]string,
	fleetClient *fleet.Client,
	agents *client.Client,
	controlStore *store.Store,
) (*Service, error) {
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolving Deploy workspace: %w", err)
	}
	if launcher == nil || fleetClient == nil || agents == nil || controlStore == nil {
		return nil, errors.New("reconciler requires a process launcher, fleet, agent client, and store")
	}
	return &Service{
		workspace: resolved,
		labels:    cloneLabels(labels),
		fleet:     fleetClient,
		agents:    agents,
		store:     controlStore,
		now:       func() time.Time { return time.Now().UTC() },
		launcher:  launcher,
	}, nil
}

// Reconcile applies canonical desired state without a previously approved
// revision. Its caller owns the policy decision to permit that mutation.
func (s *Service) Reconcile(
	ctx context.Context,
	input *deployv1.ReconcileIntent,
) (*deployv1.ReconcileEvidence, error) {
	return s.reconcile(ctx, input, nil)
}

// ReconcileApproved applies an input only when the revision resolved at the
// dispatch boundary is byte-for-byte identical to the approved revision.
func (s *Service) ReconcileApproved(
	ctx context.Context,
	input *deployv1.ReconcileIntent,
	expected *deployv1.ReconcileRevision,
) (*deployv1.ReconcileEvidence, error) {
	var ownedExpected *deployv1.ReconcileRevision
	if expected != nil {
		ownedExpected = proto.Clone(expected).(*deployv1.ReconcileRevision)
	}
	if err := deployv1.ValidateReconcileRevision(ownedExpected); err != nil {
		return nil, fmt.Errorf("approved app revision: %w", err)
	}
	return s.reconcile(ctx, input, ownedExpected)
}

func (s *Service) reconcile(
	ctx context.Context,
	input *deployv1.ReconcileIntent,
	expected *deployv1.ReconcileRevision,
) (*deployv1.ReconcileEvidence, error) {
	input, err := ownedReconcileIntent(input)
	if err != nil {
		return nil, err
	}
	input, revision, composePath, err := s.resolveInput(ctx, input)
	if err != nil {
		return nil, err
	}
	if expected != nil && !proto.Equal(approvedRevision(revision, composePath), expected) {
		return nil, errors.New("app revision changed after approval")
	}
	assignment, desiredState, err := assignmentFrom(input, revision)
	if err != nil {
		return nil, err
	}
	placement, placementMode, exactNode, replicas, err := placementFrom(input)
	if err != nil {
		return nil, err
	}
	deployment := deploy.Deployment{
		ID: input.GetProject(), AppRevisionID: revision.ID, DesiredState: desiredState,
		Placement: placement, Stateful: input.GetStateful(),
	}
	if err := deployment.Validate(); err != nil {
		return nil, err
	}

	fleetSnapshot := s.fleet.Snapshot()
	if !fleetSnapshot.CanMutate() {
		return nil, fmt.Errorf("fleet mutation blocked: Warden view must be authoritative with a leader and quorum")
	}
	if fleetSnapshot.Term > math.MaxInt64 {
		return nil, fmt.Errorf("Warden term %d exceeds the database fence range", fleetSnapshot.Term)
	}
	cluster := deploy.ClusterSnapshot{
		LeaderNodeID:  fleetSnapshot.LeaderID,
		Authoritative: fleetSnapshot.Authoritative,
		HasQuorum:     fleetSnapshot.HasQuorum,
	}
	nodesByID := make(map[string]fleet.Node, len(fleetSnapshot.Nodes))
	for _, node := range fleetSnapshot.Nodes {
		nodesByID[node.ID] = node
		cluster.Nodes = append(cluster.Nodes, &deployv1.Node{
			Id: node.ID, Alive: node.Status == "alive", Labels: cloneStringMap(s.labels[node.ID]),
		})
	}
	possiblyActiveNodeIDs, err := s.store.Queries.ListPossiblyActiveDeploymentNodes(ctx, deployment.ID)
	if err != nil {
		return nil, fmt.Errorf("reading possibly active deployment targets: %w", err)
	}
	var targets []*deployv1.Node
	if desiredState == deploy.DesiredStateRunning {
		targets, err = deploy.ResolvePlacement(deployment, cluster)
		if err != nil {
			return nil, fmt.Errorf("resolving placement: %w", err)
		}
	}

	now := s.now()
	runs := planDeploymentRuns(possiblyActiveNodeIDs, targets)
	rolloutID := uuid.NewString()
	noOpReceiptID, err := s.persistIntent(ctx, input, revision, composePath, deployment, placementMode, exactNode, replicas, fleetSnapshot, rolloutID, runs, now)
	if err != nil {
		return nil, err
	}

	output := &deployv1.ReconcileEvidence{
		DeploymentId: deployment.ID,
		RevisionId:   revision.ID,
		DryRun:       len(runs) > 0,
	}
	if noOpReceiptID != 0 {
		output.ReceiptIds = append(output.ReceiptIds, noOpReceiptID)
	}
	var reconciliationErrors []error
	for _, run := range runs {
		node := nodesByID[run.NodeID]
		runAssignment := proto.Clone(assignment).(*deployv1.Assignment)
		if run.DesiredState == deploy.DesiredStateStopped {
			runAssignment.DesiredState = deployv1.DesiredState_DESIRED_STATE_STOPPED
		}
		request := &deployv1.ReconcileRequest{
			Fence:      &deployv1.Fence{Term: fleetSnapshot.Term, LeaderId: fleetSnapshot.LeaderID},
			Assignment: runAssignment,
		}
		result, callErr := s.agents.Reconcile(ctx, run.NodeID, node.Address, request)
		finishedAt := s.now()
		if callErr != nil {
			_, recordErr := s.finishRun(ctx, run.ID, false, callErr, finishedAt)
			reconciliationErrors = append(reconciliationErrors, errors.Join(callErr, recordErr))
			continue
		}
		receiptID, recordErr := s.finishRun(ctx, run.ID, result.GetDryRun(), nil, finishedAt)
		if recordErr != nil {
			reconciliationErrors = append(reconciliationErrors, recordErr)
			continue
		}
		output.RunIds = append(output.RunIds, run.ID)
		output.NodeIds = append(output.NodeIds, run.NodeID)
		output.ReceiptIds = append(output.ReceiptIds, receiptID)
		output.DryRun = output.DryRun && result.GetDryRun()
	}
	if err := deployv1.ValidateReconcileEvidence(output); err != nil {
		return nil, fmt.Errorf("reconcile evidence: %w", err)
	}
	if len(reconciliationErrors) > 0 {
		return output, fmt.Errorf("one or more node reconciliations failed: %w", errors.Join(reconciliationErrors...))
	}
	return output, nil
}

type deploymentRun struct {
	ID           string
	NodeID       string
	DesiredState deploy.DesiredState
}

func planDeploymentRuns(activeNodeIDs []string, targets []*deployv1.Node) []deploymentRun {
	runs := make([]deploymentRun, 0, len(targets)+len(activeNodeIDs))
	targetNodeIDs := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		targetNodeIDs[target.GetId()] = struct{}{}
		runs = append(runs, deploymentRun{
			ID: uuid.NewString(), NodeID: target.GetId(), DesiredState: deploy.DesiredStateRunning,
		})
	}
	for _, nodeID := range activeNodeIDs {
		if _, remainsTargeted := targetNodeIDs[nodeID]; remainsTargeted {
			continue
		}
		runs = append(runs, deploymentRun{
			ID: uuid.NewString(), NodeID: nodeID, DesiredState: deploy.DesiredStateStopped,
		})
	}
	return runs
}

func (s *Service) persistIntent(
	ctx context.Context,
	input *deployv1.ReconcileIntent,
	revision deploy.AppRevision,
	composePath string,
	deployment deploy.Deployment,
	placementMode string,
	exactNode string,
	replicas int,
	fleetSnapshot fleet.Snapshot,
	rolloutID string,
	runs []deploymentRun,
	at time.Time,
) (int64, error) {
	var noOpReceiptID int64
	err := s.store.WithTx(ctx, func(queries *storedb.Queries) error {
		stamp := pgtype.Timestamptz{Time: at, Valid: true}
		if err := queries.UpsertAppRevision(ctx, storedb.UpsertAppRevisionParams{
			AppRevisionID: revision.ID, AppName: revision.AppID,
			SourceRepository: revision.Source, SourceRevision: revision.Revision,
			SourceSha256: revision.Digest, ComposeFile: composePath, CreatedAt: stamp,
		}); err != nil {
			return fmt.Errorf("storing app revision: %w", err)
		}
		if err := queries.UpsertDeployment(ctx, storedb.UpsertDeploymentParams{
			DeploymentID: deployment.ID, AppRevisionID: revision.ID,
			ProjectName: input.GetProject(), WorkspacePath: input.GetPath(),
			DesiredState: string(deployment.DesiredState), PlacementMode: placementMode,
			ExactNodeID: optionalText(exactNode), Replicas: int32(replicas),
			Stateful: deployment.Stateful, CreatedAt: stamp, UpdatedAt: stamp,
		}); err != nil {
			return fmt.Errorf("storing deployment: %w", err)
		}
		if err := queries.CreateDeploymentRollout(ctx, storedb.CreateDeploymentRolloutParams{
			RolloutID: rolloutID, DeploymentID: deployment.ID,
			AppRevisionID: revision.ID, DesiredState: string(deployment.DesiredState),
			RequestedAt: stamp,
		}); err != nil {
			return fmt.Errorf("storing deployment rollout: %w", err)
		}
		if err := queries.DeleteDeploymentLabels(ctx, deployment.ID); err != nil {
			return fmt.Errorf("replacing deployment labels: %w", err)
		}
		keys := sortedKeys(input.GetLabels())
		for _, key := range keys {
			if err := queries.UpsertDeploymentLabel(ctx, storedb.UpsertDeploymentLabelParams{
				DeploymentID: deployment.ID, LabelKey: key, LabelValue: input.GetLabels()[key],
			}); err != nil {
				return fmt.Errorf("storing deployment label: %w", err)
			}
		}
		for _, run := range runs {
			if err := queries.CreateDeploymentRun(ctx, storedb.CreateDeploymentRunParams{
				RunID: run.ID, RolloutID: rolloutID,
				DeploymentID: deployment.ID, AppRevisionID: revision.ID,
				NodeID: run.NodeID, DesiredState: string(run.DesiredState), WardenTerm: int64(fleetSnapshot.Term),
				LeaderID: fleetSnapshot.LeaderID, RequestedAt: stamp,
			}); err != nil {
				return fmt.Errorf("storing deployment run: %w", err)
			}
		}
		if len(runs) == 0 {
			var err error
			noOpReceiptID, err = store.AppendReceipt(
				ctx, queries, "deployment_rollout", rolloutID, "deployment.noop",
				"Deployment already matched the approved desired state", revision.Digest, at,
			)
			if err != nil {
				return fmt.Errorf("recording no-op deployment receipt: %w", err)
			}
		}
		return nil
	})
	return noOpReceiptID, err
}

func (s *Service) finishRun(ctx context.Context, runID string, dryRun bool, runErr error, at time.Time) (int64, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runFinalizationTimeout)
	defer cancel()
	var receiptID int64
	err := s.store.WithTx(ctx, func(queries *storedb.Queries) error {
		status := "succeeded"
		kind := "deployment.succeeded"
		summary := "Node agent reconciled the approved assignment"
		if dryRun {
			kind = "deployment.dry_run"
			summary = "Node agent validated the approved assignment without mutation"
		}
		if runErr != nil {
			status = "failed"
			kind = "deployment.failed"
			summary = "Node agent rejected or failed the approved assignment"
		}
		rows, err := queries.FinishDeploymentRun(ctx, storedb.FinishDeploymentRunParams{
			Status: status, DryRun: pgtype.Bool{Bool: dryRun, Valid: true},
			ErrorMessage: optionalText(errorString(runErr)),
			FinishedAt:   pgtype.Timestamptz{Time: at, Valid: true}, RunID: runID,
		})
		if err != nil {
			return fmt.Errorf("finishing deployment run: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("finishing deployment run: expected one row, updated %d", rows)
		}
		receiptID, err = store.AppendReceipt(ctx, queries, "deployment_run", runID, kind, summary, "", at)
		if err != nil {
			return fmt.Errorf("recording deployment receipt: %w", err)
		}
		return nil
	})
	return receiptID, err
}

func (s *Service) persistedStopInput(
	ctx context.Context,
	request *deployv1.ReconcileIntent,
) (*deployv1.ReconcileIntent, deploy.AppRevision, string, error) {
	persisted, err := s.store.Queries.GetDeploymentForReconcile(ctx, request.GetProject())
	if err != nil {
		return nil, deploy.AppRevision{}, "", fmt.Errorf("loading deployment %q for stop: %w", request.GetProject(), err)
	}
	if request.GetApp() != persisted.AppName || request.GetProject() != persisted.ProjectName || request.GetPath() != persisted.WorkspacePath {
		return nil, deploy.AppRevision{}, "", fmt.Errorf(
			"stop request app/project/path does not match persisted deployment %q", persisted.DeploymentID,
		)
	}
	labels, err := s.store.Queries.ListDeploymentLabels(ctx, persisted.DeploymentID)
	if err != nil {
		return nil, deploy.AppRevision{}, "", fmt.Errorf("loading deployment labels for stop: %w", err)
	}
	effective := &deployv1.ReconcileIntent{
		App: persisted.AppName, Project: persisted.ProjectName, Path: persisted.WorkspacePath,
		DesiredState: deployv1.DesiredState_DESIRED_STATE_STOPPED, Replicas: persisted.Replicas,
		Stateful: persisted.Stateful,
	}
	switch persisted.PlacementMode {
	case "node":
		effective.PlacementMode = deployv1.PlacementMode_PLACEMENT_MODE_EXACT_NODE
		effective.NodeId = persisted.ExactNodeID.String
	case "leader":
		effective.PlacementMode = deployv1.PlacementMode_PLACEMENT_MODE_LEADER
	case "labels":
		effective.PlacementMode = deployv1.PlacementMode_PLACEMENT_MODE_LABELS
		effective.Labels = make(map[string]string, len(labels))
		for _, label := range labels {
			effective.Labels[label.LabelKey] = label.LabelValue
		}
	default:
		return nil, deploy.AppRevision{}, "", fmt.Errorf("persisted deployment %q has invalid placement mode %q", persisted.DeploymentID, persisted.PlacementMode)
	}
	if err := deployv1.ValidateReconcileIntent(effective); err != nil {
		return nil, deploy.AppRevision{}, "", fmt.Errorf("validating persisted reconcile intent: %w", err)
	}
	revision := deploy.AppRevision{
		ID: persisted.AppRevisionID, AppID: persisted.AppName,
		Source: persisted.SourceRepository, Revision: persisted.SourceRevision,
		Digest: persisted.SourceSha256, ComposePath: persisted.ComposeFile,
	}
	if err := revision.Validate(); err != nil {
		return nil, deploy.AppRevision{}, "", fmt.Errorf("validating persisted app revision: %w", err)
	}
	return effective, revision, persisted.ComposeFile, nil
}

func (s *Service) resolveInput(
	ctx context.Context,
	input *deployv1.ReconcileIntent,
) (*deployv1.ReconcileIntent, deploy.AppRevision, string, error) {
	desiredState, err := desiredStateFrom(input.GetDesiredState())
	if err != nil {
		return nil, deploy.AppRevision{}, "", err
	}
	if desiredState == deploy.DesiredStateStopped {
		return s.persistedStopInput(ctx, input)
	}
	revision, composePath, err := s.revision(ctx, input.GetApp(), input.GetPath())
	if err != nil {
		return nil, deploy.AppRevision{}, "", err
	}
	return input, revision, composePath, nil
}

func assignmentFrom(
	input *deployv1.ReconcileIntent,
	revision deploy.AppRevision,
) (*deployv1.Assignment, deploy.DesiredState, error) {
	domainState, err := desiredStateFrom(input.GetDesiredState())
	if err != nil {
		return nil, "", err
	}
	assignment := &deployv1.Assignment{
		App: input.GetApp(), Project: input.GetProject(), Path: input.GetPath(), DesiredState: input.GetDesiredState(),
		SourceRevision: revision.Revision, ContentSha256: revision.Digest,
	}
	if !utf8.ValidString(input.GetPath()) {
		return nil, "", fmt.Errorf("validating assignment: candace.deploy.v1.Assignment.path must be valid UTF-8")
	}
	if err := deployv1.ValidateAssignment(assignment); err != nil {
		return nil, "", fmt.Errorf("validating assignment: %w", err)
	}
	return assignment, domainState, nil
}

func desiredStateFrom(value deployv1.DesiredState) (deploy.DesiredState, error) {
	switch value {
	case deployv1.DesiredState_DESIRED_STATE_RUNNING:
		return deploy.DesiredStateRunning, nil
	case deployv1.DesiredState_DESIRED_STATE_STOPPED:
		return deploy.DesiredStateStopped, nil
	default:
		return "", fmt.Errorf("desired_state must be running or stopped")
	}
}

func placementFrom(input *deployv1.ReconcileIntent) (deploy.Placement, string, string, int, error) {
	switch input.GetPlacementMode() {
	case deployv1.PlacementMode_PLACEMENT_MODE_EXACT_NODE:
		return deploy.Placement{ExactNode: &deploy.ExactNodePlacement{NodeID: input.GetNodeId()}}, "node", input.GetNodeId(), 1, nil
	case deployv1.PlacementMode_PLACEMENT_MODE_LEADER:
		return deploy.Placement{Leader: &deploy.LeaderPlacement{}}, "leader", "", 1, nil
	case deployv1.PlacementMode_PLACEMENT_MODE_LABELS:
		replicas := int(input.GetReplicas())
		return deploy.Placement{MatchLabels: &deploy.MatchLabelsPlacement{Labels: cloneStringMap(input.GetLabels()), Replicas: replicas}}, "labels", "", replicas, nil
	default:
		return deploy.Placement{}, "", "", 0, fmt.Errorf("placement_mode must be exact_node, leader, or labels")
	}
}

func ownedReconcileIntent(input *deployv1.ReconcileIntent) (*deployv1.ReconcileIntent, error) {
	var owned *deployv1.ReconcileIntent
	if input != nil {
		owned = proto.Clone(input).(*deployv1.ReconcileIntent)
	}
	if err := deployv1.ValidateReconcileIntent(owned); err != nil {
		return nil, fmt.Errorf("reconcile intent: %w", err)
	}
	return owned, nil
}

func (s *Service) revision(ctx context.Context, app, relativePath string) (deploy.AppRevision, string, error) {
	_, err := resolveInside(s.workspace, relativePath)
	if err != nil {
		return deploy.AppRevision{}, "", err
	}
	status, err := s.git(ctx, "status", "--porcelain", "--untracked-files=all", "--", relativePath)
	if err != nil {
		return deploy.AppRevision{}, "", fmt.Errorf("checking app source status: %w", err)
	}
	if strings.TrimSpace(string(status)) != "" {
		return deploy.AppRevision{}, "", fmt.Errorf("app source %q has uncommitted changes; commit it before deployment", relativePath)
	}
	revisionBytes, err := s.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return deploy.AppRevision{}, "", fmt.Errorf("reading immutable Git revision: %w", err)
	}
	revisionSHA := strings.TrimSpace(string(revisionBytes))
	materialized, err := os.MkdirTemp("", "deploy-revision-*")
	if err != nil {
		return deploy.AppRevision{}, "", fmt.Errorf("creating app revision staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(materialized) }()
	digest, err := deploy.MaterializeGitAppSource(ctx, s.launcher, gitExecutable, s.workspace, revisionSHA, relativePath, materialized)
	if err != nil {
		return deploy.AppRevision{}, "", err
	}
	composePath, err := findCompose(materialized, relativePath)
	if err != nil {
		return deploy.AppRevision{}, "", err
	}
	sourceBytes, err := s.git(ctx, "config", "--get", "remote.origin.url")
	source := stripURLUserinfo(strings.TrimSpace(string(sourceBytes)))
	if err != nil || source == "" {
		source = "local://deploy-workspace"
	}
	revision := deploy.AppRevision{
		ID:    app + "-" + revisionSHA,
		AppID: app, Source: source, Revision: revisionSHA, Digest: digest, ComposePath: composePath,
	}
	if err := revision.Validate(); err != nil {
		return deploy.AppRevision{}, "", err
	}
	return revision, composePath, nil
}

// git runs one Git command in the workspace and returns its standard output.
func (s *Service) git(ctx context.Context, arguments ...string) ([]byte, error) {
	result, err := s.launcher.Run(ctx, proc.Command{
		Executable: gitExecutable, Arguments: append([]string{"-C", s.workspace}, arguments...),
	})
	return result.Stdout, err
}

func stripURLUserinfo(source string) string {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User == nil {
		return source
	}
	parsed.User = nil
	return parsed.String()
}

func resolveInside(root, relative string) (string, error) {
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return "", fmt.Errorf("resolving app path: %w", err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("app path escapes the configured workspace")
	}
	return resolved, nil
}

func findCompose(appDir, relative string) (string, error) {
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"} {
		candidate := filepath.Join(appDir, name)
		info, err := os.Stat(candidate)
		if os.IsNotExist(err) {
			continue
		}
		if err == nil && info.Mode().IsRegular() {
			return filepath.ToSlash(filepath.Join(relative, name)), nil
		}
	}
	return "", fmt.Errorf("app path %q has no standard Compose file", relative)
}

func cloneLabels(source map[string]map[string]string) map[string]map[string]string {
	result := make(map[string]map[string]string, len(source))
	for nodeID, labels := range source {
		result[nodeID] = cloneStringMap(labels)
	}
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func optionalText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

var _ operator.IReconciler = (*Service)(nil)
