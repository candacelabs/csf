// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/services/harness"
)

// The resident panel: what the harness and its executors hold in memory,
// how many sessions are open and how many executors are alive, the executors
// resumed after a suspend and the latest resumed turn's time to first
// token. The harness samples the series into one file at the root of the
// state directory (harness.ResidentFile) and the panel follows it the way the
// miners panel follows the mutation series.
const (
	// ResidentRegion is the panel's region.
	ResidentRegion = "opsview.resident"
	// EventResident carries the whole series, as JSON in the series field. It
	// is internal: the follow effect emits it and a browser may not.
	EventResident = "opsview.resident"

	residentTemplate = "resident"
	// residentShown is how many samples the panel lists, newest last.
	residentShown      = 24
	bytesFormat        = "%.0f MB"
	secondsFormat      = "%.1fs"
	boundFormat        = "%s (q%.2f of %d gaps)"
	residentNoBoundYet = "no bound"
)

// ResidentEvent is the event the follow effect emits when the series file
// changed, addressed to the panel.
func ResidentEvent(samples []harness.ResidentSample) (live.Event, error) {
	encoded, err := json.Marshal(samples)
	if err != nil {
		return live.Event{}, err
	}
	return live.Event{Name: EventResident, FragmentID: ResidentRegion, Fields: live.NewFields(map[string]string{FieldSeries: string(encoded)})}, nil
}

// residentEqual compares two series by their wire form; no series at all and
// an empty one are the same series.
func residentEqual(previous []harness.ResidentSample, next []harness.ResidentSample) bool {
	if len(previous) == 0 && len(next) == 0 {
		return true
	}
	before, _ := json.Marshal(previous)
	after, _ := json.Marshal(next)
	return string(before) == string(after)
}

// residentView is the panel's data: the latest sample rendered and the recent
// samples as a series.
type residentView struct {
	Region         string
	Latest         residentPoint
	Points         []residentPoint
	OpenSessions   int
	ExecutorsAlive int
	Resumes        int
	ResumeTTFT     string
	IdleBound      string
}

// residentPoint is one sample as the panel shows it.
type residentPoint struct {
	Time      string
	Harness   string
	Executors string
	Alive     int
}

func residentPointOf(sample harness.ResidentSample) residentPoint {
	return residentPoint{
		Time:      sample.At.Format(seriesTimeFormat),
		Harness:   megabytes(sample.HarnessRSSBytes),
		Executors: megabytes(sample.ExecutorsRSSBytes),
		Alive:     sample.ExecutorsAlive,
	}
}

func megabytes(bytes uint64) string { return fmt.Sprintf(bytesFormat, float64(bytes)/(1<<20)) }

// residentViewOf is the panel's data for the series, latest sample last.
func residentViewOf(samples []harness.ResidentSample) residentView {
	view := residentView{Region: ResidentRegion, IdleBound: residentNoBoundYet}
	if len(samples) == 0 {
		return view
	}
	shown := samples
	if len(shown) > residentShown {
		shown = shown[len(shown)-residentShown:]
	}
	for _, sample := range shown {
		view.Points = append(view.Points, residentPointOf(sample))
	}
	latest := samples[len(samples)-1]
	view.Latest = residentPointOf(latest)
	view.OpenSessions, view.ExecutorsAlive, view.Resumes = latest.OpenSessions, latest.ExecutorsAlive, latest.Resumes
	view.ResumeTTFT = fmt.Sprintf(secondsFormat, latest.ResumeTimeToFirstTokenMs/1000)
	if latest.IdleBoundSeconds > 0 {
		bound := time.Duration(latest.IdleBoundSeconds * float64(time.Second)).Round(time.Second)
		view.IdleBound = fmt.Sprintf(boundFormat, bound, latest.IdleBoundQuantile, latest.Gaps)
	}
	return view
}

// renderResident draws the panel: nothing inside it until a sample exists.
func renderResident(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		return views.ExecuteTemplate(writer, residentTemplate, residentViewOf(state.resident))
	})
}

// residentChanged reports whether the panel's markup moved.
func residentChanged(previous viewState, next viewState) bool {
	return !residentEqual(previous.resident, next.resident)
}

// residentFragment is the panel as the live library mounts it.
func residentFragment() live.Fragment[viewState] {
	return live.Fragment[viewState]{ID: ResidentRegion, Render: renderResident, Dirty: residentChanged}
}
