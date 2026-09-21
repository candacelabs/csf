package csf

import (
	"fmt"

	brainspinev1 "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"google.golang.org/protobuf/proto"
)

// Runtime has one owner. Compose it directly into a Go loop, or use the JSONL
// test adapter. No goroutine, socket, container or service registry is required.
type Runtime struct {
	program        *brainspinev1.Program
	epoch          uint64
	sequence       uint64
	lastTick       uint64
	hasObservation bool
}

func NewRuntime() *Runtime { return &Runtime{} }

// Activate is an episode-boundary operation in this prototype. It compiles the
// candidate completely before replacing the active immutable program.
func (runtime *Runtime) Activate(controller *brainspinev1.Controller, epoch uint64) (*brainspinev1.Program, error) {
	if epoch == 0 || epoch <= runtime.epoch {
		return nil, fmt.Errorf("activation epoch must increase")
	}
	program, err := Compile(controller)
	if err != nil {
		return nil, err
	}
	runtime.program = proto.CloneOf(program)
	runtime.epoch = epoch
	runtime.sequence = 0
	runtime.lastTick = 0
	runtime.hasObservation = false
	return program, nil
}

// Reset starts a new test episode while preserving the admitted controller.
func (runtime *Runtime) Reset(epoch uint64) error {
	if runtime.program == nil || epoch <= runtime.epoch {
		return fmt.Errorf("reset requires an active controller and increasing epoch")
	}
	runtime.epoch = epoch
	runtime.sequence = 0
	runtime.lastTick = 0
	runtime.hasObservation = false
	return nil
}

// Step rejects stale, replayed and wrong-epoch observations. Braking is a
// simulation fallback policy, not a theorem about a physical vehicle.
func (runtime *Runtime) Step(observation *brainspinev1.Observation, nowTick uint64) *brainspinev1.Action {
	fallback := func(reason string) *brainspinev1.Action {
		return &brainspinev1.Action{Acceleration: -Scale, Epoch: runtime.epoch, Fallback: true, Reason: reason}
	}
	if runtime.program == nil || observation == nil {
		return fallback("no_active_controller_or_observation")
	}
	if observation.Epoch != runtime.epoch {
		return fallback("wrong_epoch")
	}
	if observation.Tick > nowTick || nowTick-observation.Tick > MaxAgeTicks {
		return fallback("stale_observation")
	}
	if runtime.hasObservation && (observation.Sequence <= runtime.sequence || observation.Tick < runtime.lastTick) {
		return fallback("replayed_observation")
	}
	action, err := Evaluate(runtime.program, observation.Features)
	if err != nil {
		return fallback("invalid_observation")
	}
	runtime.hasObservation = true
	runtime.sequence = observation.Sequence
	runtime.lastTick = observation.Tick
	action.Epoch = runtime.epoch
	action.Sequence = observation.Sequence
	return action
}

func (runtime *Runtime) Handle(request *brainspinev1.RuntimeRequest) *brainspinev1.RuntimeResponse {
	response := &brainspinev1.RuntimeResponse{}
	if request == nil || len(request.ProtoReflect().GetUnknown()) != 0 {
		response.Error = "request is required and must have no unknown fields"
		response.Epoch = runtime.epoch
		return response
	}
	var err error
	switch request.Kind {
	case brainspinev1.RequestKind_REQUEST_KIND_COMPILE:
		response.Program, err = Compile(request.Controller)
	case brainspinev1.RequestKind_REQUEST_KIND_ACTIVATE:
		response.Program, err = runtime.Activate(request.Controller, request.Epoch)
	case brainspinev1.RequestKind_REQUEST_KIND_STEP:
		response.Action = runtime.Step(request.Observation, request.Tick)
	case brainspinev1.RequestKind_REQUEST_KIND_RESET:
		err = runtime.Reset(request.Epoch)
	case brainspinev1.RequestKind_REQUEST_KIND_EVALUATE:
		if request.Observation == nil {
			err = fmt.Errorf("observation is required")
		} else {
			response.Action, err = Evaluate(request.Program, request.Observation.Features)
		}
	default:
		err = fmt.Errorf("unsupported request kind")
	}
	if err != nil {
		response.Error = err.Error()
	}
	response.Epoch = runtime.epoch
	return response
}
