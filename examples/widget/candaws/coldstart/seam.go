package coldstart

import "strconv"

// This file is the whole of the seam between the runtime and the card. Nothing
// in runtime.go, instance.go or view.go names a wire field, a region or an
// event, and nothing in the generated widget knows that an instance is a
// goroutine.

// ReportFields is one pool view as the poolReport event carries it.
//
// The live count, the warm floor and the totals do not cross. The card draws
// what the pool is, and a viewer who can see that two instances are warm has
// been told everything the picture uses.
func ReportFields(view PoolView) map[string]string {
	return map[string]string{
		ColdstartEventPoolReportFieldInvocationSequence: strconv.FormatUint(view.Sequence, 10),
		ColdstartEventPoolReportFieldRuntimeName:        view.RuntimeName,
		ColdstartEventPoolReportFieldWarmInstances:      strconv.Itoa(view.WarmInstances),
		ColdstartEventPoolReportFieldQueuedInvocations:  strconv.Itoa(view.Queued),
		ColdstartEventPoolReportFieldColdStartMillis:    strconv.Itoa(view.ColdStartMillis),
		ColdstartEventPoolReportFieldDispatcherUp:       strconv.FormatBool(view.DispatcherUp),
		ColdstartEventPoolReportFieldDraining:           strconv.FormatBool(view.Draining),
	}
}
