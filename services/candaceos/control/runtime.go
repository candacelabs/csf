// Package control is CandaceOS Core's control-plane composition root beneath
// main.
//
// The package owns the wiring between the operator controller, the durable
// store, and the Warden fleet view: the lifecycle hooks that turn controller
// events into durable receipts, the persistence-fault gate that refuses
// operator work while PostgreSQL is unreachable, and the projection of
// controller and fleet state into the WebUI snapshot contract.
//
// Core produces every snapshot the browser renders, so the operator-visible
// identity is a Runtime setting rather than something the presentation layer
// discovers: WithBrand supplies the product and agent names this package
// stamps into each snapshot.
//
// Callers may rely on Runtime satisfying the operator transport's backend
// contract, including webui.ISnapshotProvider, and on every operator-visible
// mutation being proven durable before it is acknowledged. Runtime owns no
// HTTP surface, no agent harness, and no schema; those belong to httpapi,
// operator, and store respectively.
package control

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/fleet"
	"github.com/candacelabs/csf/services/candaceos/internal/storedb"
	"github.com/candacelabs/csf/services/candaceos/operator"
	"github.com/candacelabs/csf/services/candaceos/store"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

const (
	persistenceWindowPolls   = 15
	commitProbeIntervalParts = 20
	faultFleet               = "fleet_observation"
	faultRunStarted          = "run_started:"
	faultRunStatus           = "run_status:"
	faultApprovalRequested   = "approval_requested:"
	faultApprovalResolution  = "approval_resolution:"
)

type fleetTransaction func(
	transactionContext context.Context,
	apply func(queries *storedb.Queries) error,
) error

// Runtime is the user-facing control-plane composition root below main.
type Runtime struct {
	store       *store.Store
	fleet       *fleet.Client
	controller  *operator.Controller
	labels      map[string]map[string]string
	persistence *candaceosv1.PersistenceTiming
	version     string
	brand       webui.Brand

	mu     sync.RWMutex
	faults map[string]string

	fleetWriteMu     sync.Mutex
	lastFleetKey     string
	lastFleetWriteAt time.Time
	lastFleetFailure time.Time
}

// Option changes one public Runtime policy.
type Option func(settings *runtimeOptions) error

type runtimeOptions struct {
	brand webui.Brand
}

// WithBrand replaces the stock CandaceOS identity in the snapshots this
// runtime produces. Unset Brand fields keep their stock values.
func WithBrand(brand webui.Brand) Option {
	return func(settings *runtimeOptions) error {
		if err := brand.Validate(); err != nil {
			return err
		}
		settings.brand = brand.Resolved()
		return nil
	}
}

// NewRuntime installs durable lifecycle hooks on the controller.
func NewRuntime(
	controlStore *store.Store,
	fleetClient *fleet.Client,
	controller *operator.Controller,
	labels map[string]map[string]string,
	persistence *candaceosv1.PersistenceTiming,
	version string,
	functionalOptions ...Option,
) (*Runtime, error) {
	if controlStore == nil || fleetClient == nil || controller == nil {
		return nil, fmt.Errorf("control runtime requires store, fleet, and controller")
	}
	if err := candaceosv1.ValidatePersistenceTiming(persistence); err != nil {
		return nil, fmt.Errorf("control runtime persistence timing: %w", err)
	}
	settings := runtimeOptions{brand: webui.DefaultBrand()}
	for index, option := range functionalOptions {
		if option == nil {
			return nil, fmt.Errorf("control runtime option %d is nil", index+1)
		}
		if err := option(&settings); err != nil {
			return nil, fmt.Errorf("applying control runtime option %d: %w", index+1, err)
		}
	}
	runtime := &Runtime{
		store: controlStore, fleet: fleetClient, controller: controller,
		labels: labels, persistence: proto.Clone(persistence).(*candaceosv1.PersistenceTiming),
		version: version, brand: settings.brand, faults: make(map[string]string),
	}
	controller.OnRunStarted = runtime.recordRunStarted
	controller.OnRunStatus = runtime.recordRunStatus
	controller.OnApprovalRequested = runtime.recordApprovalRequested
	controller.OnApprovalResolved = runtime.recordApprovalResolved
	return runtime, nil
}

// Send starts or queues one operator request after proving the database is up.
func (r *Runtime) Send(ctx context.Context, prompt string) (string, error) {
	if err := r.requireDurable(ctx); err != nil {
		return "", err
	}
	return r.controller.Send(
		ctx,
		prompt,
		candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE,
	)
}

// SendToClaw fences a browser message to the rendered harness session and run.
// An active run is steered in place; an idle completed run becomes a new turn
// in the same harness session.
func (r *Runtime) SendToClaw(
	ctx context.Context,
	sessionID, expectedRunID, prompt string,
	delivery candaceosv1.HarnessDelivery,
) (string, error) {
	if err := r.requireDurable(ctx); err != nil {
		return "", err
	}
	return r.controller.SendToSession(ctx, sessionID, expectedRunID, prompt, delivery)
}

func (r *Runtime) requireDurable(ctx context.Context) error {
	if err := r.durableFault(); err != nil {
		return err
	}
	if err := r.store.Ping(ctx); err != nil {
		return err
	}
	return r.durableFault()
}

// Abort stops the current agent turn without discarding its persisted session.
func (r *Runtime) Abort(ctx context.Context) error { return r.controller.Abort(ctx) }

// AbortRun stops only the execution identified by the browser snapshot.
func (r *Runtime) AbortRun(ctx context.Context, sessionID, runID string) error {
	return r.controller.AbortRun(ctx, sessionID, runID)
}

// ResolveApproval records one first-answer-wins operator decision. Approving a
// mutation fails closed without Warden quorum; rejection is always possible.
func (r *Runtime) ResolveApproval(id, decision string) error {
	parsed := operator.ApprovalDecision(decision)
	if parsed == operator.DecisionApprove {
		approval, ok := r.controller.ApprovalQueue().Get(id)
		if ok && approval.RequiresFleetQuorum && !r.fleet.Snapshot().CanMutate() {
			return fmt.Errorf("approval blocked while the fleet lacks an authoritative leader and quorum")
		}
	}
	_, err := r.controller.ApprovalQueue().Resolve(id, parsed, operator.ApprovalActorState().WebOperator)
	var notPending *operator.ApprovalNotPendingError
	if !errors.As(err, &notPending) {
		return err
	}
	return r.describeNonPendingApproval(notPending, err)
}

func (r *Runtime) describeNonPendingApproval(
	notPending *operator.ApprovalNotPendingError,
	queueErr error,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.persistenceWindow())
	defer cancel()
	record, err := r.store.Queries.GetApproval(ctx, notPending.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: no durable approval record exists", queueErr)
	}
	if err != nil {
		return errors.Join(queueErr, fmt.Errorf("reading durable approval state: %w", err))
	}
	if record.ResolvedAt.Valid {
		actor := "an unknown actor"
		if record.ResolvedBy.Valid {
			actor = fmt.Sprintf("%q", record.ResolvedBy.String)
		}
		return fmt.Errorf(
			"%w: already resolved as %q by %s at %s",
			queueErr, record.Status, actor, record.ResolvedAt.Time.Format(time.RFC3339Nano),
		)
	}
	return fmt.Errorf(
		"%w: durable status is %q but this runtime no longer owns its waiter",
		queueErr, record.Status,
	)
}

// Subscribe reports transient run/approval changes. The HTTP event stream also
// polls snapshots so fleet changes cannot be missed.
func (r *Runtime) Subscribe() (<-chan struct{}, func()) { return r.controller.Subscribe() }

// Health checks the durable dependency and any sticky write fault that must be
// repaired before accepting more work.
func (r *Runtime) Health(ctx context.Context) error {
	if err := r.store.Ping(ctx); err != nil {
		return err
	}
	return r.durableFault()
}

// RecordFleetContext persists one coalesced Warden observation within the
// owning fleet worker's lifecycle so shutdown cannot leave a database callback
// running after the store closes.
func (r *Runtime) RecordFleetContext(parent context.Context, snapshot fleet.Snapshot) {
	r.recordFleetContext(parent, snapshot, r.store.WithTx)
}

func (r *Runtime) recordFleetContext(
	parent context.Context,
	snapshot fleet.Snapshot,
	withTransaction fleetTransaction,
) {
	if snapshot.Term > math.MaxInt64 {
		r.rememberError(faultFleet, fmt.Errorf("Warden term %d exceeds the database fence range", snapshot.Term))
		return
	}
	configured := fleet.WithConfiguration(snapshot, r.labels)
	key := fleetPersistenceKey(configured)
	r.fleetWriteMu.Lock()
	defer r.fleetWriteMu.Unlock()
	now := time.Now().UTC()
	if key == r.lastFleetKey && now.Sub(r.lastFleetWriteAt) < r.persistenceWindow() {
		r.lastFleetFailure = time.Time{}
		r.rememberError(faultFleet, nil)
		return
	}
	if !r.lastFleetFailure.IsZero() && now.Sub(r.lastFleetFailure) < r.persistenceWindow() {
		return
	}

	observed := postgresTimestamp(snapshot.UpdatedAt)
	if !observed.Valid {
		observed = postgresTimestamp(time.Now())
	}
	ctx, cancel := context.WithTimeout(parent, r.persistenceWindow())
	defer cancel()
	err := withTransaction(ctx, func(queries *storedb.Queries) error {
		for _, node := range configured.Nodes {
			lastSeen := postgresTimestamp(node.LastSeen)
			if err := queries.UpsertNode(ctx, storedb.UpsertNodeParams{
				NodeID: node.ID, Address: node.Address, Role: node.Role, Status: node.Status,
				WardenTerm: int64(snapshot.Term), LastSeenAt: lastSeen, ObservedAt: observed,
			}); err != nil {
				return fmt.Errorf("upserting fleet node %q: %w", node.ID, err)
			}
			if err := queries.DeleteNodeLabels(ctx, node.ID); err != nil {
				return fmt.Errorf("replacing labels for %q: %w", node.ID, err)
			}
			for key, value := range node.Labels {
				if err := queries.UpsertNodeLabel(ctx, storedb.UpsertNodeLabelParams{
					NodeID: node.ID, LabelKey: key, LabelValue: value,
				}); err != nil {
					return fmt.Errorf("upserting label for %q: %w", node.ID, err)
				}
			}
		}
		return nil
	})
	err = r.reconcileCommitError(err, func(readContext context.Context) (bool, error) {
		return r.fleetObservationPersisted(readContext, configured, observed)
	})
	if err == nil {
		r.lastFleetKey = key
		r.lastFleetWriteAt = time.Now().UTC()
		r.lastFleetFailure = time.Time{}
	} else {
		r.lastFleetFailure = time.Now().UTC()
	}
	r.rememberError(faultFleet, err)
}

// fleetObservationPersisted runs while fleetWriteMu is held, so this runtime
// cannot interleave another node or label write between these reads.
func (r *Runtime) fleetObservationPersisted(
	ctx context.Context,
	snapshot fleet.ConfiguredSnapshot,
	observed pgtype.Timestamptz,
) (bool, error) {
	if len(snapshot.Nodes) == 0 {
		return false, nil
	}
	rows, err := r.store.Queries.ListNodes(ctx)
	if err != nil {
		return false, fmt.Errorf("listing fleet nodes after an ambiguous commit: %w", err)
	}
	rowsByID := make(map[string]storedb.CandaceosNode, len(rows))
	for _, row := range rows {
		rowsByID[row.NodeID] = row
	}
	for _, node := range snapshot.Nodes {
		row, ok := rowsByID[node.ID]
		if !ok || row.Address != node.Address || row.Role != node.Role ||
			row.Status != node.Status || row.WardenTerm != int64(snapshot.Term) ||
			!postgresTimestampsEqual(row.LastSeenAt, postgresTimestamp(node.LastSeen)) ||
			!postgresTimestampsEqual(row.ObservedAt, observed) {
			return false, nil
		}
		labels, err := r.store.Queries.ListNodeLabels(ctx, node.ID)
		if err != nil {
			return false, fmt.Errorf("listing labels for fleet node %q after an ambiguous commit: %w", node.ID, err)
		}
		if len(labels) != len(node.Labels) {
			return false, nil
		}
		for _, label := range labels {
			expected, exists := node.Labels[label.LabelKey]
			if !exists || label.LabelValue != expected {
				return false, nil
			}
		}
	}
	return true, nil
}

func postgresTimestamp(value time.Time) pgtype.Timestamptz {
	if value.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{
		Time: value.UTC().Truncate(time.Microsecond), Valid: true,
	}
}

func postgresTimestampsEqual(actual, expected pgtype.Timestamptz) bool {
	if actual.Valid != expected.Valid || actual.InfinityModifier != expected.InfinityModifier {
		return false
	}
	return !actual.Valid || actual.Time.Equal(expected.Time)
}

// fleetPersistenceKey excludes observation timestamps, which change on every
// Warden poll but do not represent a durable topology or placement change.
func fleetPersistenceKey(snapshot fleet.ConfiguredSnapshot) string {
	nodes := append([]fleet.ConfiguredNode(nil), snapshot.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	var key strings.Builder
	writeFleetKeyPart(&key, strconv.FormatUint(snapshot.Term, 10))
	for _, node := range nodes {
		writeFleetKeyPart(&key, node.ID)
		writeFleetKeyPart(&key, node.Address)
		writeFleetKeyPart(&key, node.Role)
		writeFleetKeyPart(&key, node.Status)
		labelKeys := make([]string, 0, len(node.Labels))
		for labelKey := range node.Labels {
			labelKeys = append(labelKeys, labelKey)
		}
		sort.Strings(labelKeys)
		writeFleetKeyPart(&key, strconv.Itoa(len(labelKeys)))
		for _, labelKey := range labelKeys {
			writeFleetKeyPart(&key, labelKey)
			writeFleetKeyPart(&key, node.Labels[labelKey])
		}
	}
	return key.String()
}

func writeFleetKeyPart(key *strings.Builder, value string) {
	key.WriteString(strconv.Itoa(len(value)))
	key.WriteByte(':')
	key.WriteString(value)
}

// Snapshot implements webui.ISnapshotProvider.
func (r *Runtime) Snapshot(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
	deploymentRows, err := r.store.Queries.ListDeploymentRolloutRows(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing deployments: %w", err)
	}
	deployments := aggregateDeploymentRollouts(deploymentRows)
	receipts, err := r.store.Queries.ListRecentReceipts(ctx, 30)
	if err != nil {
		return nil, fmt.Errorf("listing receipts: %w", err)
	}
	fleetSnapshot := r.fleet.Snapshot()
	configuredFleet := fleet.WithConfiguration(fleetSnapshot, r.labels)
	result := &candaceosv1.WebUISnapshot{
		GeneratedAt: timestamppb.New(time.Now().UTC()),
		System:      r.systemView(fleetSnapshot),
		Fleet: &candaceosv1.WebUIFleet{
			LeaderId: fleetSnapshot.LeaderID,
			Term:     fleetSnapshot.Term,
			Quorum: &candaceosv1.WebUIQuorum{
				Healthy: fleetSnapshot.HasQuorum, Online: uint32(fleetSnapshot.Online), Required: uint32(fleetSnapshot.Required),
			},
		},
	}

	appsPerNode := make(map[string]int)
	for _, deployment := range deployments {
		status := deployment.latestStatus
		if deployment.latestDryRun && status == "succeeded" {
			status = "planned"
		} else if status == "succeeded" {
			status = deployment.row.DesiredState
		}
		updatedAt := deployment.row.UpdatedAt.Time
		if deployment.latestFinishedAt.Valid {
			updatedAt = deployment.latestFinishedAt.Time
		}
		nodeIDs := append([]string(nil), deployment.nodeIDs...)
		result.Apps = append(result.Apps, &candaceosv1.WebUIApp{
			Id: deployment.row.DeploymentID, Name: deployment.row.AppName,
			Summary: "Compose app from " + deployment.row.WorkspacePath,
			Status:  status, NodeId: strings.Join(nodeIDs, ", "), NodeIds: nodeIDs,
			Revision: shortRevision(deployment.row.SourceRevision), UpdatedAt: timestamppb.New(updatedAt),
		})
		if status != "stopped" {
			for _, nodeID := range nodeIDs {
				appsPerNode[nodeID]++
			}
		}
	}
	for _, node := range configuredFleet.Nodes {
		result.Fleet.Nodes = append(result.Fleet.Nodes, &candaceosv1.WebUINode{
			Id: node.ID, Name: node.Name, Role: node.Role, Status: node.Status,
			Labels:  node.Labels,
			Address: node.Address, Apps: uint32(appsPerNode[node.ID]), LastSeen: timestamppb.New(node.LastSeen),
		})
	}
	for _, approval := range r.controller.ApprovalQueue().Pending() {
		result.Attention = append(result.Attention, &candaceosv1.WebUIAttention{
			Id: approval.ID, Title: approval.Title, Detail: approval.Detail,
			Risk: approval.Risk, RequestedAt: timestamppb.New(approval.RequestedAt),
		})
	}
	run := r.controller.Run()
	if run.ID != "" {
		view := &candaceosv1.WebUIRun{
			Id: run.ID, SessionId: run.SessionID, Title: run.Title, Status: run.Status,
			StartedAt: timestamppb.New(run.StartedAt), CanAbort: run.CanAbort,
		}
		for _, entry := range run.Entries {
			view.Entries = append(view.Entries, &candaceosv1.WebUIRunEntry{
				Id: entry.ID, Kind: entry.Kind, Role: entry.Role, Name: entry.Name,
				Text: entry.Text, Detail: entry.Detail, Status: entry.Status, At: timestamppb.New(entry.At),
			})
		}
		result.Run = view
	}
	for _, receipt := range receipts {
		result.Activity = append(result.Activity, &candaceosv1.WebUIActivity{
			Id: strconv.FormatInt(receipt.ReceiptID, 10), Kind: activityKind(receipt.Kind),
			Title: receipt.Summary, Detail: receipt.Kind, Status: activityStatus(receipt.Kind),
			At: timestamppb.New(receipt.OccurredAt.Time), ReceiptId: "receipt-" + strconv.FormatInt(receipt.ReceiptID, 10),
		})
	}
	return result, nil
}

type deploymentRollout struct {
	row              storedb.ListDeploymentRolloutRowsRow
	nodeIDs          []string
	latestStatus     string
	latestDryRun     bool
	dryRunSet        bool
	latestFinishedAt pgtype.Timestamptz
}

func aggregateDeploymentRollouts(rows []storedb.ListDeploymentRolloutRowsRow) []deploymentRollout {
	rollouts := make([]deploymentRollout, 0, len(rows))
	indexByDeployment := make(map[string]int, len(rows))
	for _, row := range rows {
		index, ok := indexByDeployment[row.DeploymentID]
		if !ok {
			index = len(rollouts)
			indexByDeployment[row.DeploymentID] = index
			rollouts = append(rollouts, deploymentRollout{
				row: row, nodeIDs: append([]string(nil), row.PossiblyActiveNodeIds...),
				latestStatus: "pending",
			})
		}
		rollout := &rollouts[index]
		if row.LatestRolloutID == "" {
			continue
		}
		rollout.latestStatus = aggregateRolloutStatus(rollout.latestStatus, row.LatestStatus)
		if !rollout.dryRunSet {
			rollout.latestDryRun = row.LatestDryRun
			rollout.dryRunSet = true
		} else {
			rollout.latestDryRun = rollout.latestDryRun && row.LatestDryRun
		}
		if row.LatestFinishedAt.Valid && (!rollout.latestFinishedAt.Valid || row.LatestFinishedAt.Time.After(rollout.latestFinishedAt.Time)) {
			rollout.latestFinishedAt = row.LatestFinishedAt
		}
	}
	return rollouts
}

func aggregateRolloutStatus(current, candidate string) string {
	rank := map[string]int{"pending": 0, "succeeded": 1, "running": 2, "failed": 3}
	if rank[candidate] > rank[current] {
		return candidate
	}
	return current
}

func (r *Runtime) systemView(snapshot fleet.Snapshot) *candaceosv1.WebUISystem {
	status := "healthy"
	summary := fmt.Sprintf("%d nodes · quorum healthy", len(snapshot.Nodes))
	if snapshot.Error != "" || !snapshot.HasQuorum || !snapshot.Authoritative || snapshot.LeaderID == "" {
		status = "warning"
		summary = "Read-only until Warden has an authoritative leader and quorum"
	}
	if controllerStatus := r.controller.Status(); controllerStatus == "starting" || controllerStatus == "stopped" {
		status = "unavailable"
		summary = "Agent runtime is " + controllerStatus
	}
	if r.durableFault() != nil {
		status = "warning"
		summary = "A durable control-plane write needs attention"
	}
	identity := r.controller.HarnessIdentity()
	return &candaceosv1.WebUISystem{
		Name: r.brand.ProductName, AgentName: r.brand.AgentName,
		Status: status, Summary: summary, Version: r.version,
		HarnessBackend: identity.GetBackend(), HarnessModel: identity.GetModel(),
		HarnessImplementation: identity.GetImplementation(),
		HarnessCapabilities:   append([]candaceosv1.HarnessCapability(nil), identity.GetCapabilities()...),
	}
}

func (r *Runtime) recordRunStarted(event operator.RunStarted) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.persistenceWindow())
	defer cancel()
	identity := r.controller.HarnessIdentity()
	err := r.store.WithTx(ctx, func(queries *storedb.Queries) error {
		at := pgtype.Timestamptz{Time: event.At, Valid: true}
		if err := queries.CreateRun(ctx, storedb.CreateRunParams{
			RunID: event.RunID, SessionID: event.SessionID, Title: event.Title,
			Prompt: event.Prompt, Status: "running", StartedAt: at,
		}); err != nil {
			return err
		}
		_, err := store.AppendReceipt(
			ctx, queries, "operator_run", event.RunID, "run.started", r.runStartedSummary(identity), identity.GetModelDigest(), event.At,
		)
		return err
	})
	err = r.reconcileCommitError(err, func(readContext context.Context) (bool, error) {
		row, readErr := r.store.Queries.GetRun(readContext, event.RunID)
		if readErr != nil {
			return false, readErr
		}
		return row.SessionID == event.SessionID && row.Title == event.Title &&
			row.Prompt == event.Prompt && row.Status == "running", nil
	})
	r.rememberRecordError(faultRunStarted+event.RunID, err)
	return err
}

func (r *Runtime) recordRunStatus(runID, status string, at time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), r.persistenceWindow())
	defer cancel()
	err := r.store.WithTx(ctx, func(queries *storedb.Queries) error {
		rows, err := queries.UpdateRunStatus(ctx, storedb.UpdateRunStatusParams{
			RunID: runID, Status: status, FinishedAt: pgtype.Timestamptz{Time: at, Valid: true},
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return fmt.Errorf("expected one operator run, updated %d", rows)
		}
		_, err = store.AppendReceipt(ctx, queries, "operator_run", runID, "run."+status, "Agent run "+status, "", at)
		return err
	})
	err = r.reconcileCommitError(err, func(readContext context.Context) (bool, error) {
		row, readErr := r.store.Queries.GetRun(readContext, runID)
		if readErr != nil {
			return false, readErr
		}
		return row.Status == status && row.FinishedAt.Valid, nil
	})
	r.rememberRecordError(faultRunStatus+runID, err)
}

func (r *Runtime) runStartedSummary(identity *candaceosv1.HarnessRuntimeIdentity) string {
	summary := "Agent run started with " + identity.GetImplementation()
	if model := strings.TrimSpace(identity.GetModel()); model != "" {
		if digest := identity.GetModelDigest(); digest != "" {
			model += "@sha256:" + digest
		}
		summary += " (" + model + ")"
	}
	return summary
}

func (r *Runtime) recordApprovalRequested(approval operator.Approval) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.persistenceWindow())
	defer cancel()
	err := r.store.WithTx(ctx, func(queries *storedb.Queries) error {
		if err := queries.CreateApproval(ctx, storedb.CreateApprovalParams{
			ApprovalID: approval.ID, RunID: optionalText(approval.RunID),
			ToolCallID: optionalText(approval.ToolCallID), RequestKind: approval.Kind,
			Title: approval.Title, Detail: approval.Detail, Risk: approval.Risk,
			PayloadSha256: approval.PayloadSHA256,
			RequestedAt:   pgtype.Timestamptz{Time: approval.RequestedAt, Valid: true},
			ExpiresAt:     pgtype.Timestamptz{Time: approval.ExpiresAt, Valid: true},
		}); err != nil {
			return err
		}
		_, err := store.AppendReceipt(ctx, queries, "approval", approval.ID, "approval.requested", approval.Title, approval.PayloadSHA256, approval.RequestedAt)
		return err
	})
	err = r.reconcileCommitError(err, func(readContext context.Context) (bool, error) {
		row, readErr := r.store.Queries.GetApproval(readContext, approval.ID)
		if readErr != nil {
			return false, readErr
		}
		return row.RunID == optionalText(approval.RunID) &&
			row.ToolCallID == optionalText(approval.ToolCallID) &&
			row.RequestKind == approval.Kind && row.Title == approval.Title &&
			row.Detail == approval.Detail && row.Risk == approval.Risk &&
			row.PayloadSha256 == approval.PayloadSHA256 && row.Status == "pending", nil
	})
	r.rememberRecordError(faultApprovalRequested+approval.ID, err)
	return err
}

func (r *Runtime) recordApprovalResolved(resolution operator.ApprovalResolution) error {
	status := string(resolution.Decision)
	if resolution.Decision == operator.DecisionApprove {
		status = "approved"
	} else if resolution.Decision == operator.DecisionReject {
		status = "rejected"
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.persistenceWindow())
	defer cancel()
	err := r.store.WithTx(ctx, func(queries *storedb.Queries) error {
		rows, err := queries.ResolveApproval(ctx, storedb.ResolveApprovalParams{
			ApprovalID: resolution.Approval.ID, Status: status,
			ResolvedAt: pgtype.Timestamptz{Time: resolution.At, Valid: true},
			ResolvedBy: optionalText(resolution.Actor),
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return fmt.Errorf("expected one pending approval, updated %d", rows)
		}
		_, err = store.AppendReceipt(ctx, queries, "approval", resolution.Approval.ID, "approval."+status, resolution.Approval.Title, resolution.Approval.PayloadSHA256, resolution.At)
		return err
	})
	err = r.reconcileCommitError(err, func(readContext context.Context) (bool, error) {
		row, readErr := r.store.Queries.GetApproval(readContext, resolution.Approval.ID)
		if readErr != nil {
			return false, readErr
		}
		return row.Status == status && row.ResolvedAt.Valid &&
			row.ResolvedBy == optionalText(resolution.Actor), nil
	})
	r.rememberRecordError(faultApprovalResolution+resolution.Approval.ID, err)
	return err
}

func (r *Runtime) reconcileCommitError(writeErr error, verify func(readContext context.Context) (bool, error)) error {
	if writeErr == nil || !store.IsTransactionCommitError(writeErr) {
		return writeErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.persistenceWindow()/2)
	defer cancel()
	ticker := time.NewTicker(r.persistencePollInterval() / commitProbeIntervalParts)
	defer ticker.Stop()
	for {
		applied, _ := verify(ctx)
		if applied {
			return nil
		}
		select {
		case <-ctx.Done():
			return writeErr
		case <-ticker.C:
		}
	}
}

func (r *Runtime) persistencePollInterval() time.Duration {
	return time.Duration(r.persistence.FleetPollIntervalNanoseconds)
}

func (r *Runtime) persistenceWindow() time.Duration {
	return persistenceWindowPolls * r.persistencePollInterval()
}

func (r *Runtime) rememberRecordError(key string, err error) {
	if err != nil {
		r.rememberError(key, err)
	}
}

func (r *Runtime) rememberError(key string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.faults == nil {
		r.faults = make(map[string]string)
	}
	if err != nil {
		r.faults[key] = err.Error()
		return
	}
	delete(r.faults, key)
}

func (r *Runtime) durableFault() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.faults) == 0 {
		return nil
	}
	keys := make([]string, 0, len(r.faults))
	for key := range r.faults {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return fmt.Errorf("unrepaired durable control-plane write fault: %s", strings.Join(keys, ", "))
}

func optionalText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func shortRevision(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func activityKind(kind string) string {
	switch {
	case strings.HasPrefix(kind, "deployment."):
		return "deploy"
	case strings.HasPrefix(kind, "approval."):
		return "approval"
	default:
		return "run"
	}
}

func activityStatus(kind string) string {
	switch {
	case strings.HasSuffix(kind, ".failed"), strings.HasSuffix(kind, ".rejected"), strings.HasSuffix(kind, ".expired"), strings.HasSuffix(kind, ".interrupted"):
		return "failed"
	case strings.HasSuffix(kind, ".requested"), strings.HasSuffix(kind, ".started"):
		return "pending"
	case strings.HasSuffix(kind, ".dry_run"):
		return "planned"
	default:
		return "succeeded"
	}
}

var _ webui.ISnapshotProvider = (*Runtime)(nil)
