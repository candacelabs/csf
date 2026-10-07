// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/a-h/templ"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/pkg/gotth/live"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// The cost panel: the session operations' cost model, fed by the report the
// costs verb replaces under the state directory (services/harness/costs,
// ReportFile).
const (
	CostsRegion = "opsview.costs"
	// EventCosts carries the report, as its JSON, in the report field. It is
	// internal: the follow effect emits it, and a browser may not.
	EventCosts  = "opsview.costs"
	FieldReport = "report"

	costsTemplate   = "costs"
	operationPrefix = "HYPERVISOR_OPERATION_"
	decisionPrefix  = "HYPERVISOR_DECISION_"
	dollarsFormat   = "%.2f"
	intervalFormat  = "%.2f [%.2f, %.2f]"
	// otherColumn heads the operations the series has no column of their own for.
	otherColumn = "other"
)

// The operations the series shows a column for, in order; the rest are
// summed into the last column.
var costColumns = []harnessv1.HypervisorOperation{
	harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_TURN_WARM,
	harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_TURN_COLD,
	harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_BIND,
	harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_REOPEN,
}

// CostDayRow is one day of the series, every number rendered.
type CostDayRow struct {
	Day          string
	SessionHours string
	// ByOperation follows costColumns, then everything else.
	ByOperation [5]string
	HFleet      string
	Saving      string
}

// CostPolicyRow is one decision's chosen policy against today's.
type CostPolicyRow struct {
	Decision string
	Policy   string
	USD      string
	Saving   string
	Note     string
}

// CostParameterRow is one parameter the hypervisor reads.
type CostParameterRow struct {
	Name   string
	Value  string
	Chosen bool
}

// CostsPanel is what the view shows of the cost model: a pure projection of
// its report, every number already rendered.
type CostsPanel struct {
	Recorded   bool
	Updated    string
	Runs       string
	Days       []CostDayRow
	Policies   []CostPolicyRow
	Parameters []CostParameterRow
}

// equal compares two panels field by field.
func (panel CostsPanel) equal(other CostsPanel) bool {
	return panel.Recorded == other.Recorded && panel.Updated == other.Updated && panel.Runs == other.Runs &&
		slices.Equal(panel.Days, other.Days) && slices.Equal(panel.Policies, other.Policies) && slices.Equal(panel.Parameters, other.Parameters)
}

// costsPanelOf renders one report.
func costsPanelOf(report *harnessv1.CostReport) CostsPanel {
	panel := CostsPanel{Recorded: true, Updated: report.GetAt().AsTime().UTC().Format(time.DateTime),
		Runs: fmt.Sprintf("%d runs over %d days", report.GetRuns(), report.GetDays())}
	for _, day := range report.GetSeries() {
		row := CostDayRow{Day: day.GetDay(), SessionHours: fmt.Sprintf("%.0f", day.GetSessionHours()),
			HFleet: fmt.Sprintf("%.4f", day.GetHFleet()), Saving: fmt.Sprintf(dollarsFormat, day.GetSavingUsd())}
		for index := range row.ByOperation {
			row.ByOperation[index] = fmt.Sprintf("%.3f", 0.0)
		}
		other := 0.0
		for operation, dollars := range day.GetUsdPerSessionHour() {
			index := slices.IndexFunc(costColumns, func(column harnessv1.HypervisorOperation) bool { return column.String() == operation })
			if index < 0 {
				other += dollars
				continue
			}
			row.ByOperation[index] = fmt.Sprintf("%.3f", dollars)
		}
		row.ByOperation[len(costColumns)] = fmt.Sprintf("%.3f", other)
		panel.Days = append(panel.Days, row)
	}
	for _, policy := range report.GetPolicies() {
		if !policy.GetChosen() {
			continue
		}
		panel.Policies = append(panel.Policies, CostPolicyRow{
			Decision: strings.ToLower(strings.TrimPrefix(policy.GetDecision().String(), decisionPrefix)),
			Policy:   policy.GetPolicy(),
			USD:      fmt.Sprintf(intervalFormat, policy.GetUsdPerDay(), policy.GetUsdPerDayInterval().GetLow(), policy.GetUsdPerDayInterval().GetHigh()),
			Saving:   fmt.Sprintf(intervalFormat, policy.GetSavingUsdPerDay(), policy.GetSavingInterval().GetLow(), policy.GetSavingInterval().GetHigh()),
			Note:     policy.GetNote(),
		})
	}
	for _, parameter := range report.GetParameters() {
		panel.Parameters = append(panel.Parameters, CostParameterRow{Name: parameter.GetName(),
			Value: fmt.Sprintf("%.0f %s", parameter.GetValue(), parameter.GetUnit()), Chosen: parameter.GetChosen()})
	}
	return panel
}

// decodeCosts reads a report off the wire. One that does not decode leaves
// the panel as it was.
func decodeCosts(text string) (CostsPanel, bool) {
	var report harnessv1.CostReport
	if protojson.Unmarshal([]byte(text), &report) != nil {
		return CostsPanel{}, false
	}
	return costsPanelOf(&report), true
}

// CostsEvent is the event the follow effect emits when the report changed,
// addressed to the panel's own region.
func CostsEvent(report []byte) live.Event {
	return live.Event{Name: EventCosts, FragmentID: CostsRegion, Fields: live.NewFields(map[string]string{FieldReport: string(report)})}
}

// costsView is the panel region's data.
type costsView struct {
	CostsPanel
	Region  string
	Columns []string
}

// renderCosts draws the panel from its projection.
func renderCosts(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		columns := []string{}
		for _, column := range costColumns {
			columns = append(columns, strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(column.String(), operationPrefix), "_", " ")))
		}
		return views.ExecuteTemplate(writer, costsTemplate, costsView{CostsPanel: state.costs, Region: CostsRegion, Columns: append(columns, otherColumn)})
	})
}

// costsChanged reports whether the panel's rendered values moved.
func costsChanged(previous viewState, next viewState) bool {
	return !previous.costs.equal(next.costs)
}
