// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"errors"
	stdfs "io/fs"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/candacelabs/csf/csf/prod"
	"github.com/candacelabs/csf/csf/prod/certificate"
	"github.com/candacelabs/csf/io/net/model/jev"
	"github.com/candacelabs/csf/pkg/gotth/live"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/services/cloud"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/harness/costs"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/intake"
	"github.com/candacelabs/csf/services/ouroboros/labeler"
)

// sourceFollow names the follow effect in provenance: every patch a card
// takes carries "effect:opsview.follow".
const sourceFollow = "opsview.follow"

// rootName is the state directory itself in the capabilities' naming.
const rootName = "."

// initialize opens one connection: no cards yet, and the follow effect that
// delivers them. The first paint shows an empty board for the instant it
// takes the effect to read the run directories, and the snapshot that follows
// morphs the cards in.
func (view *OpsView) initialize(ctx context.Context, session live.Session[ViewerIdentity]) (viewState, []live.Effect[ViewerIdentity], error) {
	effects := []live.Effect[ViewerIdentity]{{Source: sourceFollow, Run: view.follow}}
	if view.definitions != nil {
		effects = append(effects, live.Effect[ViewerIdentity]{Source: sourceWidgets, Run: view.followWidgets})
	}
	if view.host != nil {
		effects = append(effects, live.Effect[ViewerIdentity]{Source: sourceHost, Run: view.followHost})
	}
	state := viewState{prefs: view.recalled(session.Identity())}
	if view.operations != nil {
		// A theme that cannot be read leaves the default tokens in force.
		if response, err := view.operations.GetWorkbenchTheme(ctx, &pb.GetWorkbenchThemeRequest{}); err == nil {
			state.theme = response.GetTheme().GetCustomCss()
		}
	}
	return state, effects, nil
}

// follow is the one I/O of the view: it reads every run directory once, then
// re-reads a session's files whenever the kernel reports a change to them,
// and emits the session's card each time it changed. It owns the tails for
// the life of the connection; nothing else reads them.
func (view *OpsView) follow(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
	emit = view.patient(ctx, emit)
	watch, err := view.watcher.Watch(ctx)
	if err != nil {
		return err
	}
	// The root is watched before the directories are listed, so a run
	// directory created during the listing is seen either way.
	if err := watch.Add(rootName); err != nil {
		return err
	}
	tails := map[string]*tail{}
	entries, err := view.files.ReadDir(rootName)
	if err != nil {
		return err
	}
	today := view.today()
	if err := emit(TodayEvent(today)); err != nil {
		return err
	}
	// Two passes, so the first useful paint does not wait for the long logs:
	// every session from the end of its log, then the whole log of each one
	// that is running or moved today, which is what its counts and today's
	// spend are read from.
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := view.adopt(watch.Add, tails, entry.Name(), emit); err != nil {
			return err
		}
	}
	var whole []string
	for assignment, position := range tails {
		if needsWhole(position.card, today) {
			whole = append(whole, assignment)
		}
	}
	slices.Sort(whole)
	view.readWhole(tails, whole)
	for _, assignment := range whole {
		if err := view.emitCard(tails[assignment], assignment, emit); err != nil {
			return err
		}
	}
	if err := emit(live.Event{Name: EventRefreshMerges, FragmentID: SummaryRegion}); err != nil {
		return err
	}
	if err := emit(live.Event{Name: EventRefreshSessions, FragmentID: SummaryRegion}); err != nil {
		return err
	}
	if err := emit(live.Event{Name: EventLoaded, FragmentID: BoardRegion}); err != nil {
		return err
	}
	if err := view.deliverLoop(emit); err != nil {
		return err
	}
	if err := view.deliverQueue(emit); err != nil {
		return err
	}
	if err := view.deliverDreamer(emit); err != nil {
		return err
	}
	if err := view.deliverTemplates(emit); err != nil {
		return err
	}
	var series []MinerSeries
	if series, err = view.deliverSeries(series, emit); err != nil {
		return err
	}
	if err := view.deliverLabeler(emit); err != nil {
		return err
	}
	if err := view.deliverCloud(emit); err != nil {
		return err
	}
	if err := view.deliverGitHub(emit); err != nil {
		return err
	}
	var resident []harness.ResidentSample
	if resident, err = view.deliverResident(resident, emit); err != nil {
		return err
	}
	if err := view.deliverCosts(emit); err != nil {
		return err
	}
	if err := view.deliverCertificate(emit); err != nil {
		return err
	}
	if err := view.deliverGolden(emit); err != nil {
		return err
	}
	if err := view.deliverRulings(emit); err != nil {
		return err
	}
	if err := view.deliverJev(emit); err != nil {
		return err
	}
	for change := range watch.Changes {
		if change.Err != nil {
			return change.Err
		}
		assignment, file, followed := splitChange(change.Name)
		switch {
		case !followed:
		case file == "" && assignment == LoopFile:
			if err := view.deliverLoop(emit); err != nil {
				return err
			}
		case file == "" && assignment == QueueFile:
			if err := view.deliverQueue(emit); err != nil {
				return err
			}
		case file == "" && assignment == DreamerFile:
			if err := view.deliverDreamer(emit); err != nil {
				return err
			}
		case file == "" && assignment == MutationFile:
			if series, err = view.deliverSeries(series, emit); err != nil {
				return err
			}
		case file == "" && assignment == labeler.RunFile:
			if err := view.deliverLabeler(emit); err != nil {
				return err
			}
		case file == "" && assignment == cloud.LedgerFile:
			if err := view.deliverCloud(emit); err != nil {
				return err
			}
		case file == "" && assignment == harness.ResidentFile:
			if resident, err = view.deliverResident(resident, emit); err != nil {
				return err
			}
		case file == "" && assignment == costs.ReportFile:
			if err := view.deliverCosts(emit); err != nil {
				return err
			}
		case file == "" && assignment == certificate.FileName:
			if err := view.deliverCertificate(emit); err != nil {
				return err
			}
		case file == "" && assignment == prod.FileName:
			if err := view.deliverGolden(emit); err != nil {
				return err
			}
		case file == "" && assignment == session.RulingsFile:
			if err := view.deliverRulings(emit); err != nil {
				return err
			}
		case file == "" && assignment == jev.DecideLedgerFile:
			if err := view.deliverJev(emit); err != nil {
				return err
			}
		case file == "" && view.isDirectory(assignment):
			if _, known := tails[assignment]; !known {
				if err := view.adopt(watch.Add, tails, assignment, emit); err != nil {
					return err
				}
			}
		case assignment == intake.StreamAssignment && file == session.EventsFile:
			if err := view.deliverGitHub(emit); err != nil {
				return err
			}
		case file == session.RunStateFile || file == session.EventsFile:
			position, known := tails[assignment]
			if !known {
				continue
			}
			if next := view.today(); next != today {
				today = next
				if err := emit(TodayEvent(today)); err != nil {
					return err
				}
			}
			moved, err := view.deliver(position, assignment, true, emit)
			if err != nil {
				return err
			}
			// A session whose status moved changed the session counts; one
			// that ended a turn or merged may have merged a pull request.
			if moved.status {
				if err := emit(live.Event{Name: EventRefreshSessions, FragmentID: SummaryRegion}); err != nil {
					return err
				}
			}
			if moved.settled {
				if err := emit(live.Event{Name: EventRefreshMerges, FragmentID: SummaryRegion}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// adopt starts following one run directory: watches it, reads the end of
// its log and emits its card if it is a session already.
func (view *OpsView) adopt(add func(name string) error, tails map[string]*tail, assignment string, emit live.Emitter) error {
	if err := add(assignment); err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return nil
		}
		return err
	}
	position := &tail{}
	tails[assignment] = position
	_, err := view.deliver(position, assignment, false, emit)
	return err
}

// needsWhole is whether a card read from the end of its log is read whole:
// when it is running, moved today, or its window held no status.
func needsWhole(card SessionCard, today string) bool {
	return card.Partial && (card.Status == "" || card.Status == StatusRunning || card.LastDay == today)
}

// movement is how a read moved a session: its status changed, or it just
// settled — ended a turn, closed, or had its pull request merged.
type movement struct {
	status  bool
	settled bool
}

// deliver reads what the session's files hold past the tail and emits the
// card when it changed, reporting how the session moved. A file the harness
// is mid-way through replacing reads again on its next change; a record the
// view cannot decode is logged and skipped rather than ending the follow.
func (view *OpsView) deliver(position *tail, assignment string, whole bool, emit live.Emitter) (movement, error) {
	before := position.card
	changed, err := position.read(view.files, assignment, whole)
	if err != nil {
		view.logger.Warn("ops view: session not read", "assignment", assignment, "error", err)
		return movement{}, nil
	}
	if !changed {
		return movement{}, nil
	}
	if err := view.emitCard(position, assignment, emit); err != nil {
		return movement{}, err
	}
	after := position.card
	known := !before.Partial && before.Assignment != ""
	moved := movement{
		status: known && before.Status != after.Status,
		settled: known && (before.Status != after.Status && after.Status != StatusRunning ||
			before.PullRequest != after.PullRequest && after.PullRequest == PullRequestMerged),
	}
	return moved, nil
}

// emitCard emits the session's card as the page shows it: a session still
// running by its log but silent since before this host started is stalled.
func (view *OpsView) emitCard(position *tail, assignment string, emit live.Emitter) error {
	region, err := view.cards.Region(assignment)
	if err != nil {
		view.logger.Warn("ops view: run directory name cannot be a region", "assignment", assignment, "error", err)
		return nil
	}
	shown := position.card
	if shown.Status == StatusRunning && shown.LastAt.Before(view.hostStart) {
		shown.Status = StatusStalled
	}
	event, err := CardEvent(region, shown)
	if err != nil {
		return err
	}
	return emit(event)
}

// readWhole reads the whole logs of the named sessions at once, half the
// processors' worth at a time, and returns when every read is done. Each
// read owns its session's tail and touches no other; one that fails is
// logged and leaves its tail as it was. It is the first pass's long part:
// on a host measured on 2026-10-05, 59 sessions that moved that day held
// 190 MB of log between them.
func (view *OpsView) readWhole(tails map[string]*tail, assignments []string) {
	slots := make(chan struct{}, max(1, goruntime.GOMAXPROCS(0)/2))
	var reads sync.WaitGroup
	for _, assignment := range assignments {
		position := tails[assignment]
		reads.Add(1)
		slots <- struct{}{}
		go func() {
			defer reads.Done()
			defer func() { <-slots }()
			if _, err := position.read(view.files, assignment, true); err != nil {
				view.logger.Warn("ops view: session not read", "assignment", assignment, "error", err)
			}
		}()
	}
	reads.Wait()
}

// openWhole is the effect an opened card asks for when it was read from the
// end of its log only: the whole log, folded once, as the card's event.
func (view *OpsView) openWhole(assignment string) func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
	return func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
		_, err := view.deliver(&tail{}, assignment, true, emit)
		return err
	}
}

// today is the UTC day by the view's clock.
func (view *OpsView) today() string {
	return view.clock.Now().UTC().Format(time.DateOnly)
}

// deliverSeries re-reads the whole series file, which is small, and emits
// the series when it differs from the one last emitted. A missing file is an
// empty series; a file that cannot be read is logged and left as it was.
func (view *OpsView) deliverSeries(previous []MinerSeries, emit live.Emitter) ([]MinerSeries, error) {
	content, err := view.files.ReadFile(MutationFile)
	if err != nil && !errors.Is(err, stdfs.ErrNotExist) {
		view.logger.Warn("ops view: mutation series not read", "file", MutationFile, "error", err)
		return previous, nil
	}
	next := ReadSeries(content)
	if seriesEqual(previous, next) {
		return previous, nil
	}
	event, err := SeriesEvent(next)
	if err != nil {
		return previous, err
	}
	return next, emit(event)
}

// deliverTemplates lists the launch form's recipe templates once, when the
// page has the template capability; one that cannot be listed is logged and
// offers none.
func (view *OpsView) deliverTemplates(emit live.Emitter) error {
	if view.templates == nil {
		return nil
	}
	names, err := ListTemplates(view.templates)
	if err != nil {
		view.logger.Warn("ops view: recipe templates not listed", "error", err)
		return nil
	}
	return emit(live.Event{Name: EventTemplates, FragmentID: LaunchRegion, Fields: live.NewFields(map[string]string{FieldTemplates: strings.Join(names, templateSeparator)})})
}

// deliverLabeler emits the labeler's run record when there is one. A record
// mid-replacement reads again on its next change; one the view cannot read
// is logged rather than ending the follow.
func (view *OpsView) deliverLabeler(emit live.Emitter) error {
	content, err := view.files.ReadFile(labeler.RunFile)
	if errors.Is(err, stdfs.ErrNotExist) {
		return nil
	}
	if err != nil {
		view.logger.Warn("ops view: labeler run not read", "error", err)
		return nil
	}
	return emit(LabelerEvent(content))
}

// deliverResident re-reads the whole resident series, which the harness keeps
// to a bounded window, and emits it when it differs from the one last
// emitted. A missing file is an empty series; a file that cannot be read is
// logged and left as it was.
func (view *OpsView) deliverResident(previous []harness.ResidentSample, emit live.Emitter) ([]harness.ResidentSample, error) {
	content, err := view.files.ReadFile(harness.ResidentFile)
	if err != nil && !errors.Is(err, stdfs.ErrNotExist) {
		view.logger.Warn("ops view: resident series not read", "file", harness.ResidentFile, "error", err)
		return previous, nil
	}
	next := harness.ReadResidentSeries(content)
	if residentEqual(previous, next) {
		return previous, nil
	}
	event, err := ResidentEvent(next)
	if err != nil {
		return previous, err
	}
	return next, emit(event)
}

// deliverRulings emits the ruling records when there are any, as
// deliverLabeler does the labeler's run.
func (view *OpsView) deliverRulings(emit live.Emitter) error {
	content, err := view.files.ReadFile(session.RulingsFile)
	if errors.Is(err, stdfs.ErrNotExist) {
		return nil
	}
	if err != nil {
		view.logger.Warn("ops view: ruling records not read", "file", session.RulingsFile, "error", err)
		return nil
	}
	return emit(RulingsEvent(content))
}

// deliverCosts emits the cost model's report when there is one, as
// deliverLabeler does the labeler's run.
func (view *OpsView) deliverCosts(emit live.Emitter) error {
	content, err := view.files.ReadFile(costs.ReportFile)
	if errors.Is(err, stdfs.ErrNotExist) {
		return nil
	}
	if err != nil {
		view.logger.Warn("ops view: cost report not read", "error", err)
		return nil
	}
	return emit(CostsEvent(content))
}

// deliverJev emits the decision record when there is one, as deliverLabeler
// does the labeler's run.
func (view *OpsView) deliverJev(emit live.Emitter) error {
	content, err := view.files.ReadFile(jev.DecideLedgerFile)
	if errors.Is(err, stdfs.ErrNotExist) {
		return nil
	}
	if err != nil {
		view.logger.Warn("ops view: decision record not read", "error", err)
		return nil
	}
	return emit(JevEvent(content))
}

// deliverCertificate emits the choice certificate when there is one, as
// deliverCosts does the cost report.
func (view *OpsView) deliverCertificate(emit live.Emitter) error {
	content, err := view.files.ReadFile(certificate.FileName)
	if errors.Is(err, stdfs.ErrNotExist) {
		return nil
	}
	if err != nil {
		view.logger.Warn("ops view: certificate not read", "error", err)
		return nil
	}
	return emit(CertificateEvent(content))
}

// deliverGolden emits the merge quality gate's verdict when there is one, as
// deliverCertificate does the choice certificate.
func (view *OpsView) deliverGolden(emit live.Emitter) error {
	content, err := view.files.ReadFile(prod.FileName)
	if errors.Is(err, stdfs.ErrNotExist) {
		return nil
	}
	if err != nil {
		view.logger.Warn("ops view: golden metrics not read", "error", err)
		return nil
	}
	return emit(GoldenEvent(content))
}

// splitChange reads a change's name as <assignment>/<file>. A name with no
// slash is an entry of the state directory itself and reads as an empty
// file; a deeper name is nothing the view follows.
func splitChange(name string) (assignment string, file string, followed bool) {
	assignment, file, _ = strings.Cut(name, "/")
	return assignment, file, !strings.Contains(file, "/")
}

func (view *OpsView) isDirectory(name string) bool {
	info, err := stdfs.Stat(view.files, name)
	return err == nil && info.IsDir()
}

// The emitter's back-off: from the first retry's wait, doubling to the cap.
const (
	emitRetryFirst = time.Millisecond
	emitRetryCap   = 100 * time.Millisecond
)

// patient wraps the follow's emitter so a refused emission waits and is sent
// again rather than ending the follow. The session's mailbox holds 64 events,
// and the first pass emits one card per session — 150 on a host measured on
// 2026-10-05 — faster than the session drains them; the library's answer to a
// full mailbox is to back off and emit again. A session that is closing
// cancels ctx, which ends the wait and returns the refusal.
func (view *OpsView) patient(ctx context.Context, emit live.Emitter) live.Emitter {
	return func(event live.Event) error {
		wait := emitRetryFirst
		for {
			err := emit(event)
			if err == nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return err
			case <-view.clock.After(wait):
			}
			wait = min(2*wait, emitRetryCap)
		}
	}
}
