// Copyright 2026 Candace Labs

package await

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/ipc/proc"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// HostRecordFile is the host record csf serve writes under its state
// directory once it is ready, and removes as it stops.
const HostRecordFile = "harness.json"

// HostRecord is the host record: the host process, where it listens and
// when it started.
type HostRecord struct {
	PID       int       `json:"pid"`
	Endpoint  string    `json:"endpoint"`
	Listen    []string  `json:"listen"`
	StartedAt time.Time `json:"started_at"`
}

// ReadHostRecord reads the host record from the state directory, reporting
// whether one exists.
func ReadHostRecord(state iofs.IFiles) (HostRecord, bool, error) {
	var record HostRecord
	content, err := state.ReadFile(HostRecordFile)
	if errors.Is(err, stdfs.ErrNotExist) {
		return record, false, nil
	}
	if err != nil {
		return record, false, err
	}
	if err := json.Unmarshal(content, &record); err != nil {
		return record, false, fmt.Errorf("decode %s: %w", HostRecordFile, err)
	}
	return record, true, nil
}

const (
	processStatFile  = "stat"
	loadAverageFile  = "loadavg"
	phasePrefix      = "AGENT_SESSION_PHASE_"
	ghExecutable     = "gh"
	ghPullRequest    = "pr"
	ghView           = "view"
	ghRepository     = "--repo"
	ghJSON           = "--json"
	ghStateField     = "state"
	pullStateMerged  = "MERGED"
	pullStateClosed  = "CLOSED"
	defaultURLStatus = http.StatusOK
	// The process states /proc reports for a process that has exited and
	// not been reaped, or is being torn down.
	stateZombie = "Z"
	stateDead   = "X"
)

// processState is the state letter /proc/<pid>/stat reports, or false when
// the process does not exist. A zombie or dead process has exited.
func (awaiter *Awaiter) processState(pid int) (string, bool) {
	content, err := awaiter.processes.ReadFile(strconv.Itoa(pid) + "/" + processStatFile)
	if err != nil {
		return "", false
	}
	// The command name is parenthesized and may hold spaces; the state is
	// the first field after its closing parenthesis.
	fields := strings.Fields(string(content[bytes.LastIndexByte(content, ')')+1:]))
	if len(fields) == 0 {
		return "", false
	}
	state := fields[0]
	return state, state != stateZombie && state != stateDead
}

func (awaiter *Awaiter) harnessReady(request Request) (func(ctx context.Context) observation, error) {
	if awaiter.state == nil {
		return nil, fmt.Errorf("%w: %s reads the host state directory", ErrUnavailable, request.Condition)
	}
	return func(_ context.Context) observation {
		if request.PID != 0 && awaiter.processes != nil {
			if _, running := awaiter.processState(request.PID); !running {
				return observation{final: true, text: fmt.Sprintf("pid %d exited before it was ready", request.PID)}
			}
		}
		record, found, err := ReadHostRecord(awaiter.state)
		switch {
		case err != nil:
			return observation{text: "host record unreadable: " + err.Error()}
		case !found:
			return observation{text: "no host record yet"}
		case request.PID != 0 && record.PID != request.PID:
			return observation{text: fmt.Sprintf("the host record names pid %d, not %d", record.PID, request.PID)}
		}
		if awaiter.processes != nil {
			if _, running := awaiter.processState(record.PID); !running {
				return observation{text: fmt.Sprintf("the host record names pid %d, which is not running", record.PID)}
			}
		}
		return observation{met: true, text: fmt.Sprintf("pid %d ready on %s", record.PID, record.Endpoint)}
	}, nil
}

func (awaiter *Awaiter) harnessStopped(request Request) (func(ctx context.Context) observation, error) {
	if awaiter.processes == nil {
		return nil, fmt.Errorf("%w: %s reads the process table", ErrUnavailable, request.Condition)
	}
	pid := request.PID
	if pid == 0 {
		if awaiter.state == nil {
			return nil, fmt.Errorf("%w: %s without a pid reads the host state directory", ErrUnavailable, request.Condition)
		}
		record, found, err := ReadHostRecord(awaiter.state)
		if err != nil {
			return nil, err
		}
		if !found {
			return func(_ context.Context) observation {
				return observation{met: true, text: "no host is recorded as running"}
			}, nil
		}
		pid = record.PID
	}
	return func(_ context.Context) observation {
		state, running := awaiter.processState(pid)
		if !running {
			return observation{met: true, text: fmt.Sprintf("pid %d exited", pid)}
		}
		return observation{text: fmt.Sprintf("pid %d still running, state %s", pid, state)}
	}, nil
}

// phaseValue is the phase a request names, as OPEN or AGENT_SESSION_PHASE_OPEN
// in any case.
func phaseValue(name string) (harnessv1.AgentSessionPhase, bool) {
	upper := strings.ToUpper(name)
	if !strings.HasPrefix(upper, phasePrefix) {
		upper = phasePrefix + upper
	}
	value, known := harnessv1.AgentSessionPhase_value[upper]
	if !known || value == int32(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_UNSPECIFIED) {
		return 0, false
	}
	return harnessv1.AgentSessionPhase(value), true
}

func phaseNames() []string {
	var names []string
	for value, name := range harnessv1.AgentSessionPhase_name {
		if value != int32(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_UNSPECIFIED) {
			names = append(names, strings.TrimPrefix(name, phasePrefix))
		}
	}
	slices.Sort(names)
	return names
}

func phaseName(phase harnessv1.AgentSessionPhase) string {
	return strings.TrimPrefix(phase.String(), phasePrefix)
}

// ended is a phase no session leaves.
func ended(phase harnessv1.AgentSessionPhase) bool {
	switch phase {
	case harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED,
		harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED,
		harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CLOSED:
		return true
	}
	return false
}

// sessionText describes a session's state in one line.
func sessionText(state *harnessv1.AgentSessionState) string {
	text := fmt.Sprintf("session %s is %s after %d turns, %d queued", state.GetAssignmentId(), phaseName(state.GetPhase()), state.GetTurns(), state.GetQueued())
	if state.GetError() != "" {
		text += ": " + state.GetError()
	}
	return text
}

// sessionPoll reads the request's session and judges it.
func (awaiter *Awaiter) sessionPoll(request Request, judge func(state *harnessv1.AgentSessionState) observation) (func(ctx context.Context) observation, error) {
	if awaiter.sessions == nil {
		return nil, fmt.Errorf("%w: %s reads the session service", ErrUnavailable, request.Condition)
	}
	return func(ctx context.Context) observation {
		state, err := awaiter.sessions(ctx, request.Assignment)
		if err != nil {
			return observation{text: fmt.Sprintf("session %s unreadable: %v", request.Assignment, err)}
		}
		return judge(state)
	}, nil
}

func (awaiter *Awaiter) sessionPhase(request Request) (func(ctx context.Context) observation, error) {
	target, _ := phaseValue(request.Phase)
	return awaiter.sessionPoll(request, func(state *harnessv1.AgentSessionState) observation {
		return observation{
			met:   state.GetPhase() == target,
			final: state.GetPhase() != target && ended(state.GetPhase()),
			text:  sessionText(state),
		}
	})
}

func (awaiter *Awaiter) turnFinished(request Request) (func(ctx context.Context) observation, error) {
	return awaiter.sessionPoll(request, func(state *harnessv1.AgentSessionState) observation {
		finished := state.GetPhase() == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN &&
			state.GetQueued() == 0 && state.GetTurns() >= request.Turn
		return observation{met: finished, final: ended(state.GetPhase()), text: sessionText(state)}
	})
}

func (awaiter *Awaiter) pullRequestMerged(request Request) (func(ctx context.Context) observation, error) {
	if awaiter.launcher == nil {
		return nil, fmt.Errorf("%w: %s runs gh", ErrUnavailable, request.Condition)
	}
	arguments := []string{ghPullRequest, ghView, request.PullRequest, ghJSON, ghStateField}
	if request.Repository != "" {
		arguments = append(arguments, ghRepository, request.Repository)
	}
	return func(ctx context.Context) observation {
		result, err := awaiter.launcher.Run(ctx, proc.Command{Executable: ghExecutable, Arguments: arguments})
		if err != nil {
			return observation{text: fmt.Sprintf("pull request %s unreadable: %v", request.PullRequest, err)}
		}
		var view struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(result.Stdout, &view); err != nil {
			return observation{text: fmt.Sprintf("pull request %s unreadable: %v", request.PullRequest, err)}
		}
		return observation{
			met:   view.State == pullStateMerged,
			final: view.State == pullStateClosed,
			text:  fmt.Sprintf("pull request %s is %s", request.PullRequest, view.State),
		}
	}, nil
}

func (awaiter *Awaiter) urlStatus(request Request) (func(ctx context.Context) observation, error) {
	if awaiter.client == nil {
		return nil, fmt.Errorf("%w: %s sends an HTTP request", ErrUnavailable, request.Condition)
	}
	want := request.Status
	if want == 0 {
		want = defaultURLStatus
	}
	return func(ctx context.Context) observation {
		get, err := http.NewRequestWithContext(ctx, http.MethodGet, request.URL, nil)
		if err != nil {
			return observation{final: true, text: fmt.Sprintf("%s is not a request: %v", request.URL, err)}
		}
		response, err := awaiter.client.Do(get)
		if err != nil {
			return observation{text: fmt.Sprintf("%s unanswered: %v", request.URL, err)}
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
		_ = response.Body.Close()
		return observation{met: response.StatusCode == want, text: fmt.Sprintf("%s answered %d, awaiting %d", request.URL, response.StatusCode, want)}
	}, nil
}

func (awaiter *Awaiter) loadBelow(request Request) (func(ctx context.Context) observation, error) {
	if awaiter.processes == nil {
		return nil, fmt.Errorf("%w: %s reads the process table", ErrUnavailable, request.Condition)
	}
	return func(_ context.Context) observation {
		content, err := awaiter.processes.ReadFile(loadAverageFile)
		if err != nil {
			return observation{text: "load average unreadable: " + err.Error()}
		}
		fields := strings.Fields(string(content))
		if len(fields) < 3 {
			return observation{text: fmt.Sprintf("load average unreadable: %q", content)}
		}
		load, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			return observation{text: "load average unreadable: " + err.Error()}
		}
		return observation{
			met:  load < request.Load,
			text: fmt.Sprintf("load %s (5 min %s, 15 min %s), awaiting below %g", fields[0], fields[1], fields[2], request.Load),
		}
	}, nil
}
