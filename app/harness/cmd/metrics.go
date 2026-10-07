// Copyright 2026 Candace Labs

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/ipc/docker"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	ionet "github.com/candacelabs/csf/io/net"
	"github.com/candacelabs/csf/io/ipc/proc"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	dispatchservice "github.com/candacelabs/csf/services/dispatch"
	"github.com/candacelabs/csf/services/harness"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/candacelabs/csf/services/harness/routing"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/sessiongate"
	"github.com/candacelabs/csf/services/opsview"
	"github.com/candacelabs/csf/services/ouroboros"
	"github.com/candacelabs/csf/services/ouroboros/codes"
	"github.com/candacelabs/csf/services/views"
)

const (
	// viewsRepositoryFlag names the repository, OWNER/NAME, whose merges and
	// ontology alignment score the views read; the mining loop's checkout's
	// origin when not given.
	viewsRepositoryFlag = "views-repository"
	viewsMountName      = "views"
	// phasePrefix is the proto enum prefix a session phase's label drops.
	phasePrefix = "AGENT_SESSION_PHASE_"
	// leadTimeWindow is how far back the lead time reads merged slices: a
	// day, the window throughput is judged per.
	leadTimeWindow = 24 * time.Hour
	// correctionUncoded is the class of an operator correction AFFECT reads:
	// it finds that the operator flagged a mistake, not which class it is.
	correctionUncoded = "uncoded"
	// correctionWhat names the turn an operator correction answers.
	correctionWhat = "%s turn %d"
	// codedWhat names the transcript entry a coded complaint is.
	codedWhat = "%s entry %s"
)

// loopbackAddress is where Prometheus and Grafana are always published.
var loopbackAddress = netip.MustParseAddr("127.0.0.1")

// newViews grants the views this host's capabilities: the state directory
// as the corpus, the session service, the launch check and the dispatcher as
// live sources, the cache and question collectors, main's merge history
// through gh, and, when csf serve listens on an address a container reaches,
// the Prometheus and Grafana it owns, which scrape it there.
func newViews(ctx context.Context, state string, listens []string, containers *docker.ContainerHost, launcher *proc.HostLauncher,
	logger *slog.Logger, sessions *harness.AgentSessionService, dispatcher *dispatchservice.DispatchService,
	repository string, loopCheckout string, collectors ...prometheus.Collector) (*views.Views, error) {
	corpus, err := iofs.NewHostFiles(state)
	if err != nil {
		return nil, err
	}
	processes, err := iofs.NewHostFiles(processTableDirectory)
	if err != nil {
		return nil, err
	}
	options := []views.Option{
		views.WithCorpus(corpus), views.WithLogger(logger),
		views.WithCollectors(append([]prometheus.Collector{routing.NewCollector(state), sessiongate.NewQuestionCollector(state), sessiongate.NewRulingCollector(state)}, collectors...)...),
		views.WithSessions(func(ctx context.Context) ([]views.SessionState, error) { return sessionStates(ctx, sessions) }),
		views.WithAdmission(func(ctx context.Context) (views.Admission, error) { return admission(ctx, sessions, processes, corpus) }),
		views.WithDispatch(func(ctx context.Context) (views.Dispatch, error) { return dispatchState(ctx, dispatcher) }),
		views.WithCorrections(func(ctx context.Context) ([]views.Correction, error) { return operatorCorrections(state) }),
	}
	if repository == "" && loopCheckout != "" {
		if origin, err := gitOutput(ctx, launcher, loopCheckout, gitRemote, gitGetURL, gitOrigin); err == nil {
			repository, _ = ouroboros.RepositorySlug(origin)
		}
	}
	if repository == "" {
		logger.Warn("harness: no -" + viewsRepositoryFlag + " and no mining loop checkout with an origin, so merges and the ontology alignment score are not measured")
	} else {
		pulls, err := views.NewGitHubPulls(launcher, repository)
		if err != nil {
			return nil, err
		}
		options = append(options, views.WithPulls(pulls.Merged))
	}
	address, found := mcpAddress(listens, true)
	if !found {
		logger.Warn("harness: no listen address a container reaches, so Prometheus could not scrape /metrics and no Prometheus or Grafana is provisioned; add a -" + listenFlag + " on a non-loopback address")
		return views.NewViews(options...)
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	reachable, err := netip.ParseAddr(host)
	if err != nil {
		return nil, err
	}
	stack, err := views.NewStack(views.WithStateDirectory(state), views.WithContainers(containers),
		views.WithListener(ionet.NewHostNetwork()), views.WithFreeBytes(harness.HostMeasures{}.FreeBytes),
		views.WithPublishedAddresses(loopbackAddress, reachable), views.WithScrapeTarget(address))
	if err != nil {
		return nil, err
	}
	return views.NewViews(append(options, views.WithStack(stack))...)
}

// operatorCorrections is every operator correction the runs under state
// recorded (operator_affect), uncoded, and every complaint the codes verb
// coded, classed by its failure code, as the views count them.
func operatorCorrections(state string) ([]views.Correction, error) {
	recorded, err := session.OperatorCorrections(state)
	if err != nil {
		return nil, err
	}
	corrections := make([]views.Correction, 0, len(recorded))
	for _, correction := range recorded {
		corrections = append(corrections, views.Correction{Class: correctionUncoded, What: fmt.Sprintf(correctionWhat, correction.Assignment, correction.TargetTurn), At: correction.At})
	}
	coded, err := codedComplaints(state)
	if err != nil {
		return nil, err
	}
	return append(corrections, coded...), nil
}

// codedComplaints is every complaint the state directory's failure codes
// file holds a catalogue code for, by its latest coding; none when the file
// does not exist.
func codedComplaints(state string) ([]views.Correction, error) {
	file, err := os.Open(filepath.Join(state, codes.RecordFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	records, err := codes.ReadRecords(file)
	if err != nil {
		return nil, err
	}
	corrections := []views.Correction{}
	for _, record := range records {
		if record.Code != "" {
			corrections = append(corrections, views.Correction{Class: record.Code, What: fmt.Sprintf(codedWhat, record.Source, record.Ref), At: record.At})
		}
	}
	return corrections, nil
}

// sessionStates lists the host's sessions as the views label them.
func sessionStates(ctx context.Context, sessions *harness.AgentSessionService) ([]views.SessionState, error) {
	listed, err := sessions.List(ctx, &harnessv1.ListAgentSessionsRequest{})
	if err != nil {
		return nil, err
	}
	states := make([]views.SessionState, 0, len(listed.GetSessions()))
	for _, state := range listed.GetSessions() {
		states = append(states, views.SessionState{Assignment: state.GetAssignmentId(), Agent: state.GetAgentId(),
			Phase: strings.ToLower(strings.TrimPrefix(state.GetPhase().String(), phasePrefix))})
	}
	return states, nil
}

// admission is the launch check a submit would get now, beside the host's
// pressure and the resumes the last restart holds; a held admission measures
// no launch check.
func admission(ctx context.Context, sessions *harness.AgentSessionService, processes iofs.IFiles, state iofs.IFiles) (views.Admission, error) {
	measured := views.Admission{Pressure: map[string]float64{}}
	for _, resource := range opsview.PressureResources {
		if percent := opsview.PressureSomeAvg10(processes, resource); percent >= 0 {
			measured.Pressure[resource] = percent
		}
	}
	if content, err := state.ReadFile(harness.ResumeQueueFile); err == nil {
		var queue harness.ResumeQueue
		if json.Unmarshal(content, &queue) == nil {
			measured.ResumesHeld = len(queue.Held)
		}
	}
	check, err := sessions.Check(ctx)
	if errors.Is(err, harness.ErrAdmissionHeld) {
		measured.Held = true
		return measured, nil
	}
	if err != nil {
		return views.Admission{}, err
	}
	measured.WorkerCap, measured.FreeBytes, measured.DiskFloorBytes = int64(check.GetWorkerCap()), check.GetFreeBytes(), check.GetDiskFloorBytes()
	return measured, nil
}

// dispatchState is the dispatcher's snapshot as the views count it: slices
// by state, the launches each bounded limit allows, and each launched
// slice's assignment.
func dispatchState(ctx context.Context, dispatcher *dispatchservice.DispatchService) (views.Dispatch, error) {
	snapshot, err := dispatcher.CurrentSnapshot(ctx, dispatchservice.SnapshotInput{})
	if err != nil {
		return views.Dispatch{}, err
	}
	state := views.Dispatch{Queued: len(snapshot.Queue), Held: len(snapshot.Held), Running: len(snapshot.Running),
		Remaining: map[string]int64{}, Slices: map[string]string{}}
	for _, limit := range snapshot.Limits {
		if limit.Bounded {
			state.Remaining[string(limit.Name)] = int64(limit.Launches)
		}
	}
	for _, slice := range slices.Concat(snapshot.Queue, snapshot.Running, snapshot.Held) {
		if slice.AssignmentID != "" {
			state.Slices[slice.AssignmentID] = slice.SliceID
		}
	}
	flow := snapshot.Flow(leadTimeWindow)
	state.Stages, state.StageAges = map[string]int{}, map[string]float64{}
	for stage, count := range flow.Slices {
		state.Stages[string(stage)] = count
		state.StageAges[string(stage)] = flow.Oldest[stage].Seconds()
	}
	if flow.LeadTime != nil {
		seconds := flow.LeadTime.Seconds()
		state.LeadTime = &seconds
	}
	return state, nil
}
