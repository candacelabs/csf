// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	stdfs "io/fs"
	"slices"
	"time"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/services/cloud"
)

// The Cloud panel: every paid cloud job CSF launched, phone-first. Totals at
// the top (the day's spend against the day's cap, jobs running), one row per
// job with its Stop, and the latest notices. It follows the record the cloud
// service replaces under the state directory (cloud.LedgerFile), and its Stop
// is the service's own: cancel at the provider, then the provider's job list
// read back.
const (
	CloudRegion = "opsview.cloud"
	// EventCloud carries the record, as JSON in FieldLedger. It is internal:
	// the follow effect emits it, and a browser may not.
	EventCloud = "opsview.cloud"
	// EventCloudStop is a row's Stop: the job in FieldJob.
	EventCloudStop = "opsview.cloud.stop"
	// EventCloudStopped carries a Stop's outcome back, in FieldNotice.
	EventCloudStopped = "opsview.cloud.stopped"
	FieldLedger       = "ledger"
	FieldJob          = "job"

	cloudTemplate    = "cloud"
	cloudTitleNone   = "Cloud: no cloud record"
	cloudTitleFormat = "Cloud: $%.4f today of %s cap, %d running"
	cloudShown       = 5
	cloudTimeFormat  = "15:04:05"
	cloudSpendFormat = "$%.4f"
)

// CloudConstraint is said on the panel, beside the totals: where the limit a
// cloud session runs into comes from.
const CloudConstraint = "The usage limit follows the model credential, not the machine: a cloud session on the same Claude subscription hits the same limit, sooner with more sessions. It escapes only with a pay-per-token credential in burst.executor_environment, or with Copilot as the executor."

// WithCloudStop grants the cloud service's Stop, which the panel's buttons
// call. Without it the panel shows the jobs and offers no Stop.
func WithCloudStop(stop func(ctx context.Context, input cloud.StopCloudJobInput) (cloud.CloudJob, error)) Option {
	return func(view *OpsView) error {
		if stop == nil {
			return ErrNoOperations
		}
		view.cloudStop = stop
		return nil
	}
}

// cloudState is the panel's part of a connection's view.
type cloudState struct {
	Ledger   cloud.Ledger
	Recorded bool
	Notice   string
}

// CloudEvent is the event the follow effect emits when the record changed.
func CloudEvent(record []byte) live.Event {
	return live.Event{Name: EventCloud, FragmentID: CloudRegion, Fields: live.NewFields(map[string]string{FieldLedger: string(record)})}
}

// reduceCloud is the panel's transition.
func (view *OpsView) reduceCloud(state cloudState, event live.Event) (cloudState, []live.Effect[ViewerIdentity]) {
	switch event.Name {
	case EventCloud:
		state.Ledger, state.Recorded = cloud.ReadLedger([]byte(event.Fields.Get(FieldLedger))), true
	case EventCloudStopped:
		state.Notice = event.Fields.Get(FieldNotice)
	case EventCloudStop:
		id := event.Fields.Get(FieldJob)
		if _, err := view.cloudExpectation(state.Ledger, id); err != nil {
			state.Notice = "Stop refused: " + err.Error()
			return state, nil
		}
		state.Notice = "Stopping " + id + ": cancelling at the provider, then reading its job list back…"
		stop := view.cloudStop
		return state, []live.Effect[ViewerIdentity]{{Source: EventCloudStop, Run: func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
			job, err := stop(ctx, cloud.StopCloudJobInput{ID: id})
			notice := fmt.Sprintf("Stopped %s: the provider's job list shows it %s.", id, job.Stage)
			if err != nil {
				notice = "Stop " + id + " failed: " + firstLine(err.Error())
			}
			return emit(live.Event{Name: EventCloudStopped, FragmentID: CloudRegion, Fields: live.NewFields(map[string]string{FieldNotice: notice})})
		}}}
	}
	return state, nil
}

// cloudExpectation is a Stop's check against the record: what it will do, or
// why it is refused.
func (view *OpsView) cloudExpectation(ledger cloud.Ledger, id string) (widget.Expectation, error) {
	if view.cloudStop == nil {
		return widget.Expectation{}, errors.New("read-only: the page was mounted without the cloud service")
	}
	index := slices.IndexFunc(ledger.Jobs, func(job cloud.CloudJob) bool { return job.ID == id })
	if index < 0 {
		return widget.Expectation{}, fmt.Errorf("no job %s in the record", id)
	}
	job := ledger.Jobs[index]
	if !job.Running() {
		return widget.Expectation{}, fmt.Errorf("%s is %s already", id, job.Stage)
	}
	return widget.Expectation{Result: fmt.Sprintf("Cancels %s at %s, then checks the provider's job list shows it stopped.", id, job.Provider)}, nil
}

// cloudView is the panel's data, every value already rendered.
type cloudView struct {
	Region      string
	Recorded    bool
	SpentToday  string
	DailyCap    string
	OverDay     bool
	Running     int
	Constraint  string
	Rows        []cloudRow
	Notices     []cloud.Notice
	Notice      string
	UpdatedTime string
}

// cloudRow is one job as the panel shows it.
type cloudRow struct {
	cloud.CloudJob
	Started string
	Elapsed string
	Spend   string
	Cap     string
	Over    bool
	Stop    template.HTML
}

// renderCloud draws the panel. Elapsed is measured to the record's own last
// update, so equal records render equal markup.
func (view *OpsView) renderCloud(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		ledger := state.cloud.Ledger
		now := ledger.UpdatedAt
		data := cloudView{
			Region: CloudRegion, Recorded: state.cloud.Recorded, SpentToday: fmt.Sprintf(cloudSpendFormat, ledger.SpentOn(now)),
			DailyCap: cloud.FormatUSD(ledger.DailyCapUSD), OverDay: ledger.DailyCapUSD > 0 && ledger.SpentOn(now) >= ledger.DailyCapUSD,
			Running: ledger.RunningJobs(), Constraint: CloudConstraint, Notice: state.cloud.Notice, UpdatedTime: now.UTC().Format(cloudTimeFormat),
		}
		jobs := slices.Clone(ledger.Jobs)
		slices.SortStableFunc(jobs, func(a, b cloud.CloudJob) int {
			if a.Running() != b.Running() {
				if a.Running() {
					return -1
				}
				return 1
			}
			return b.CreatedAt.Compare(a.CreatedAt)
		})
		for _, job := range jobs {
			end := now
			if job.FinishedAt != nil {
				end = *job.FinishedAt
			}
			row := cloudRow{
				CloudJob: job, Started: job.CreatedAt.UTC().Format(cloudTimeFormat), Elapsed: end.Sub(job.CreatedAt).Round(time.Second).String(),
				Spend: fmt.Sprintf(cloudSpendFormat, job.SpendUSD), Cap: cloud.FormatUSD(job.CapUSD), Over: job.SpendUSD >= job.CapUSD,
			}
			if job.Running() {
				verified := widget.VerifyActions(ctx, []widget.VerifiedAction[string]{{ID: job.ID, Label: "Stop", Request: job.ID}},
					func(_ context.Context, id string) (widget.Expectation, error) {
						return view.cloudExpectation(ledger, id)
					})
				button, err := renderComponent(ctx, widget.VerifiedButton(verified[0], EventCloudStop, FieldJob, "act danger"))
				if err != nil {
					return err
				}
				// Only markup html/template already escaped crosses this boundary.
				row.Stop = template.HTML(button)
			}
			data.Rows = append(data.Rows, row)
		}
		notices := slices.Clone(ledger.Notices)
		slices.Reverse(notices)
		data.Notices = notices[:min(cloudShown, len(notices))]
		return views.ExecuteTemplate(writer, cloudTemplate, data)
	})
}

// cloudChanged reports whether the panel's data moved.
func cloudChanged(previous viewState, next viewState) bool {
	before, _ := json.Marshal(previous.cloud)
	after, _ := json.Marshal(next.cloud)
	return string(before) != string(after)
}

// cloudTitle is the folded panel's one line: the totals, so the day's cloud
// spend against its cap and the jobs running show without unfolding it.
func cloudTitle(state viewState) string {
	ledger := state.cloud.Ledger
	if !state.cloud.Recorded {
		return cloudTitleNone
	}
	return fmt.Sprintf(cloudTitleFormat, ledger.SpentOn(ledger.UpdatedAt), cloud.FormatUSD(ledger.DailyCapUSD), ledger.RunningJobs())
}

// cloudPanel is the panel folded under its totals until the viewer unfolds it.
func (view *OpsView) cloudPanel(state viewState) templ.Component {
	return foldedPanel(CloudRegion, sectionCloud, cloudTitle(state), view.renderCloud)(state)
}

// cloudFragment is the panel as the live library mounts it.
func (view *OpsView) cloudFragment() live.Fragment[viewState] {
	return live.Fragment[viewState]{ID: CloudRegion, Render: view.cloudPanel, Dirty: foldedDirty(sectionCloud, cloudChanged)}
}

// deliverCloud emits the record when there is one. A record mid-replacement
// reads again on its next change; one that cannot be read is logged.
func (view *OpsView) deliverCloud(emit live.Emitter) error {
	content, err := view.files.ReadFile(cloud.LedgerFile)
	if errors.Is(err, stdfs.ErrNotExist) {
		return nil
	}
	if err != nil {
		view.logger.Warn("ops view: cloud record not read", "error", err)
		return nil
	}
	return emit(CloudEvent(content))
}
