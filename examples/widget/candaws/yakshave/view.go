package yakshave

// RunView is what the pipeline tells anybody about a run, and it is deliberately
// the shape the Yakshave card's `runAdvance` event carries.
//
// Not because the engine knows about a widget — it names no region, no wire name
// and no field spelling — but because a pipeline's observable state is a small
// closed set of facts and there is no honest disagreement about which they are.
type RunView struct {
	// Sequence is this view's position in the stream, from 1. It rises by one
	// per view and is never reused, which is what the card re-arms its motion
	// on.
	Sequence uint64

	// Run is the run identity the chain is currently working, from 1.
	Run uint64

	// Stage is the stage holding the artifact, or "idle" when none is.
	Stage string

	// Cleared is which stages this run has passed, in chain order.
	//
	// A stage cannot clear before its predecessor did, and this array is where
	// that is true: [advanceView] clears every downstream entry when a stage
	// fails, so no reader ever sees a deploy that passed over a build that did
	// not. The Yakshave document records that this fact lives here and not in
	// the document, because W415 cannot see an engine.
	Cleared [stageCount]bool

	// Retries is the current run's attempt count, from zero.
	Retries int
}

// QuotaView is the other stream: minutes, which Yakshave bills for whether or
// not anything was running.
type QuotaView struct {
	// Sequence is this view's position in the quota stream, from 1.
	Sequence uint64

	// QueueMinutes is how many billed minutes were spent waiting rather than
	// building. It only rises within a billing window.
	QueueMinutes int

	// QuotaMinutes is what is left of the window's budget. It only falls.
	QuotaMinutes int
}

// advanceView folds one stage report into the published view.
//
// It is a pure function of a view and a report, which is what lets the ordering
// invariant be specified without starting a goroutine: every rule about what a
// card may show is a rule about this function.
func advanceView(view RunView, incoming stageReport) RunView {
	next := view
	next.Retries = incoming.Attempt

	// A new run, or a retry of the current one, starts from nothing cleared.
	// A retry that kept the previous attempt's flags would show a run that had
	// passed stages it is about to run again.
	if incoming.Stage == stageCheckout && incoming.Busy {
		next.Run = incoming.Run
		next.Cleared = [stageCount]bool{}
	}

	if incoming.Busy {
		next.Stage = incoming.Stage.String()
		return next
	}

	next.Cleared[incoming.Stage] = incoming.Cleared
	// A stage that did not clear invalidates everything downstream of it. The
	// downstream stages have not run on this attempt, and a card that kept
	// their previous answers would be reporting a green deploy over a red test.
	if !incoming.Cleared {
		for downstream := int(incoming.Stage) + 1; downstream < stageCount; downstream++ {
			next.Cleared[downstream] = false
		}
	}
	if incoming.Stage == stageDeploy || !incoming.Cleared {
		next.Stage = idleStage
	}
	return next
}

// Green reports whether every stage of this view's run cleared.
func (view RunView) Green() bool {
	for _, cleared := range view.Cleared {
		if !cleared {
			return false
		}
	}
	return true
}

// Ordered reports whether the view's cleared flags respect the chain: no stage
// is marked cleared over a predecessor that is not.
//
// It is exported because it is the property, not an implementation detail: a
// specification asserts it of every view the engine ever publishes, and a host
// that wanted to could assert it of every view it forwards.
func (view RunView) Ordered() bool {
	for index := 1; index < stageCount; index++ {
		if view.Cleared[index] && !view.Cleared[index-1] {
			return false
		}
	}
	return true
}
