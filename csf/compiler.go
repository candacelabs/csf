// Package csf composes the research controller compiler and runtime in
// one Go process. It makes no hard-real-time or physical-safety guarantee.
package csf

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	brainspinev1 "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"google.golang.org/protobuf/proto"
)

const (
	SchemaVersion    = 1
	Scale            = int64(1000)
	ValueLimit       = int64(1000000000)
	CoefficientLimit = int64(1000000)
	FeatureLimit     = int64(10000)
	FeatureCount     = 4
	MaxInstructions  = 128
	MaxDepth         = 16
	MaxAgeTicks      = uint64(3)
)

// Compile rejects unsupported expressions before creating a bounded program.
// Deterministic protobuf is used only for this map-free, unknown-field-free
// versioned contract. This is not a universal canonical protobuf encoding.
func Compile(controller *brainspinev1.Controller) (*brainspinev1.Program, error) {
	if controller == nil {
		return nil, fmt.Errorf("controller is required")
	}
	if err := brainspinev1.ValidateController(controller); err != nil {
		return nil, err
	}
	if len(controller.ProtoReflect().GetUnknown()) != 0 {
		return nil, fmt.Errorf("unknown controller fields")
	}
	steering, err := compileExpression(controller.Steering, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("steering: %w", err)
	}
	acceleration, err := compileExpression(controller.Acceleration, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("acceleration: %w", err)
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(controller)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	return &brainspinev1.Program{
		SchemaVersion:  SchemaVersion,
		ControllerHash: hex.EncodeToString(digest[:]),
		Steering:       steering,
		Acceleration:   acceleration,
	}, nil
}

func compileExpression(expression *brainspinev1.Expression, instructions []*brainspinev1.Instruction, depth int) ([]*brainspinev1.Instruction, error) {
	if expression == nil || depth >= MaxDepth || len(instructions) >= MaxInstructions {
		return nil, fmt.Errorf("missing expression or expression budget exceeded")
	}
	if err := brainspinev1.ValidateExpression(expression); err != nil {
		return nil, err
	}
	if len(expression.ProtoReflect().GetUnknown()) != 0 {
		return nil, fmt.Errorf("unknown expression fields")
	}
	instruction := &brainspinev1.Instruction{
		Opcode: expression.Opcode, Value: expression.Value,
		InputIndex: expression.InputIndex, Lower: expression.Lower, Upper: expression.Upper,
	}
	arity, err := instructionArity(instruction)
	if err != nil {
		return nil, err
	}
	if len(expression.Arguments) != arity {
		return nil, fmt.Errorf("opcode %s needs %d arguments", expression.Opcode, arity)
	}
	for _, argument := range expression.Arguments {
		instructions, err = compileExpression(argument, instructions, depth+1)
		if err != nil {
			return nil, err
		}
	}
	if len(instructions) >= MaxInstructions {
		return nil, fmt.Errorf("instruction budget exceeded")
	}
	return append(instructions, instruction), nil
}

func instructionArity(instruction *brainspinev1.Instruction) (int, error) {
	if instruction == nil || len(instruction.ProtoReflect().GetUnknown()) != 0 {
		return 0, fmt.Errorf("missing instruction or unknown fields")
	}
	if instruction.Value < -ValueLimit || instruction.Value > ValueLimit || instruction.Lower < -ValueLimit || instruction.Lower > ValueLimit || instruction.Upper < -ValueLimit || instruction.Upper > ValueLimit {
		return 0, fmt.Errorf("operand outside numeric profile")
	}
	if instruction.Opcode != brainspinev1.Opcode_OPCODE_INPUT && instruction.InputIndex != 0 {
		return 0, fmt.Errorf("unexpected input index")
	}
	if instruction.Opcode != brainspinev1.Opcode_OPCODE_CONSTANT && instruction.Opcode != brainspinev1.Opcode_OPCODE_SCALE && instruction.Value != 0 {
		return 0, fmt.Errorf("unexpected value")
	}
	if instruction.Opcode != brainspinev1.Opcode_OPCODE_CLAMP && (instruction.Lower != 0 || instruction.Upper != 0) {
		return 0, fmt.Errorf("unexpected clamp bounds")
	}
	switch instruction.Opcode {
	case brainspinev1.Opcode_OPCODE_CONSTANT:
		return 0, nil
	case brainspinev1.Opcode_OPCODE_INPUT:
		if instruction.InputIndex >= FeatureCount {
			return 0, fmt.Errorf("input index outside numeric profile")
		}
		return 0, nil
	case brainspinev1.Opcode_OPCODE_ADD:
		return 2, nil
	case brainspinev1.Opcode_OPCODE_SCALE:
		if instruction.Value < -CoefficientLimit || instruction.Value > CoefficientLimit {
			return 0, fmt.Errorf("coefficient outside numeric profile")
		}
		return 1, nil
	case brainspinev1.Opcode_OPCODE_CLAMP:
		if instruction.Lower > instruction.Upper {
			return 0, fmt.Errorf("inverted clamp bounds")
		}
		return 1, nil
	default:
		return 0, fmt.Errorf("unsupported opcode %d", instruction.Opcode)
	}
}

// Evaluate interprets the compiled program with bounded integer arithmetic.
// Multiplication cannot overflow int64: |stack value| <= 1e9 and |gain| <= 1e6.
func Evaluate(program *brainspinev1.Program, features []int64) (*brainspinev1.Action, error) {
	if program == nil || program.SchemaVersion != SchemaVersion || len(program.ProtoReflect().GetUnknown()) != 0 {
		return nil, fmt.Errorf("unsupported program")
	}
	if len(features) != FeatureCount {
		return nil, fmt.Errorf("exactly four normalized features required")
	}
	for _, value := range features {
		if value < -FeatureLimit || value > FeatureLimit {
			return nil, fmt.Errorf("feature outside numeric profile")
		}
	}
	steering, err := evaluateInstructions(program.Steering, features)
	if err != nil {
		return nil, err
	}
	acceleration, err := evaluateInstructions(program.Acceleration, features)
	if err != nil {
		return nil, err
	}
	return &brainspinev1.Action{Steering: clamp(steering, -Scale, Scale), Acceleration: clamp(acceleration, -Scale, Scale)}, nil
}

func evaluateInstructions(instructions []*brainspinev1.Instruction, features []int64) (int64, error) {
	if len(instructions) == 0 || len(instructions) > MaxInstructions {
		return 0, fmt.Errorf("instruction budget exceeded")
	}
	var stack [MaxInstructions]int64
	var depths [MaxInstructions]int
	count := 0
	for _, instruction := range instructions {
		arity, err := instructionArity(instruction)
		if err != nil {
			return 0, err
		}
		if count < arity {
			return 0, fmt.Errorf("stack underflow")
		}
		var result int64
		depth := 1
		for position := count - arity; position < count; position++ {
			depth = max(depth, depths[position]+1)
		}
		if depth > MaxDepth {
			return 0, fmt.Errorf("expression depth exceeded")
		}
		switch instruction.Opcode {
		case brainspinev1.Opcode_OPCODE_CONSTANT:
			result = instruction.Value
		case brainspinev1.Opcode_OPCODE_INPUT:
			result = features[instruction.InputIndex]
		case brainspinev1.Opcode_OPCODE_ADD:
			result = stack[count-2] + stack[count-1]
		case brainspinev1.Opcode_OPCODE_SCALE:
			result = stack[count-1] * instruction.Value / Scale
		case brainspinev1.Opcode_OPCODE_CLAMP:
			result = clamp(stack[count-1], instruction.Lower, instruction.Upper)
		}
		count -= arity
		stack[count] = clamp(result, -ValueLimit, ValueLimit)
		depths[count] = depth
		count++
	}
	if count != 1 {
		return 0, fmt.Errorf("program must leave exactly one value")
	}
	return stack[0], nil
}

func clamp(value int64, lower int64, upper int64) int64 {
	return min(upper, max(lower, value))
}
