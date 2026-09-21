package queuecumber

import "strconv"

// This file is the whole of the seam between the broker and the card. Nothing in
// broker.go, worker.go or message.go names a wire field, a region or an event,
// and nothing in the generated widget knows there is a queue behind it. The
// field names come from the generated constants, so renaming one in the document
// is a compile error here rather than a card that silently stops updating.

// ReportFields is one broker view as the brokerReport event carries it.
//
// Six of the view's fields cross; the totals do not, because the card renders
// what the queue is rather than what it has been. Redelivered is the one that
// would be rendered largest if it crossed, and the document's own joke is that
// it is the redrive count in the stat line instead.
func ReportFields(view BrokerView) map[string]string {
	return map[string]string{
		QueuecumberEventBrokerReportFieldSequence:     strconv.FormatUint(view.Sequence, 10),
		QueuecumberEventBrokerReportFieldAccepting:    strconv.FormatBool(view.Accepting),
		QueuecumberEventBrokerReportFieldDepth:        strconv.Itoa(view.Depth),
		QueuecumberEventBrokerReportFieldInFlight:     strconv.Itoa(view.InFlight),
		QueuecumberEventBrokerReportFieldDeadLettered: strconv.Itoa(view.DeadLettered),
		QueuecumberEventBrokerReportFieldWorkersUp:    strconv.Itoa(view.WorkersUp),
	}
}
