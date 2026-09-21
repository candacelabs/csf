package operator

// controllerPhase is Core's in-process lifecycle. Strings are produced only
// when projecting the status into the existing UI and persistence contracts.
type controllerPhase uint8

const (
	controllerStarting controllerPhase = iota
	controllerIdle
	controllerRunning
	controllerAborting
	controllerPersisting
	controllerStopped
)

func (phase controllerPhase) String() string {
	switch phase {
	case controllerStarting:
		return "starting"
	case controllerIdle:
		return "idle"
	case controllerRunning:
		return "running"
	case controllerAborting:
		return "aborting"
	case controllerPersisting:
		return "persisting"
	case controllerStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// runPhase is the canonical in-process execution lifecycle. RunState.Status is
// its string projection for the existing WebUI protobuf and SQL schema.
type runPhase uint8

const (
	runUnset runPhase = iota
	runRunning
	runAborting
	runSucceeded
	runFailed
	runAborted
)

func (phase runPhase) String() string {
	switch phase {
	case runUnset:
		return ""
	case runRunning:
		return "running"
	case runAborting:
		return "aborting"
	case runSucceeded:
		return "succeeded"
	case runFailed:
		return "failed"
	case runAborted:
		return "aborted"
	default:
		return "unknown"
	}
}
