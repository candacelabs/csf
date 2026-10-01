package yakshave

import "strconv"

// This file is the whole of the seam between the pipeline and the card, and it
// is deliberately the only file in the package that knows both halves exist.
//
// Nothing in stage.go, pipeline.go or view.go names a wire field, a region or an
// event; nothing in the generated widget knows there is a chain of goroutines
// behind it. Either could be replaced without the other noticing, which is the
// test of whether a seam is a seam. The field names come from the generated
// constants rather than from literals, so renaming one in the document is a
// compile error here rather than a card that silently stops updating.

// RunFields is one run view as the runAdvance event carries it.
func RunFields(view RunView) map[string]string {
	return map[string]string{
		YakshaveEventRunAdvanceFieldRunSequence:  strconv.FormatUint(view.Sequence, 10),
		YakshaveEventRunAdvanceFieldCurrentStage: view.Stage,
		YakshaveEventRunAdvanceFieldCheckoutOk:   strconv.FormatBool(view.Cleared[stageCheckout]),
		YakshaveEventRunAdvanceFieldBuildOk:      strconv.FormatBool(view.Cleared[stageBuild]),
		YakshaveEventRunAdvanceFieldTestOk:       strconv.FormatBool(view.Cleared[stageTest]),
		YakshaveEventRunAdvanceFieldDeployOk:     strconv.FormatBool(view.Cleared[stageDeploy]),
		YakshaveEventRunAdvanceFieldRetries:      strconv.Itoa(view.Retries),
	}
}

// QuotaFields is one quota view as the quotaUpdate event carries it.
//
// It is the second stream, and the two are separate all the way down: two
// tickers, two feeds, two events, two field maps. Only one of them can be the
// tick, because `restartOn` names a single counter.
func QuotaFields(view QuotaView) map[string]string {
	return map[string]string{
		YakshaveEventQuotaUpdateFieldQueueMinutes: strconv.Itoa(view.QueueMinutes),
		YakshaveEventQuotaUpdateFieldQuotaMinutes: strconv.Itoa(view.QuotaMinutes),
	}
}
