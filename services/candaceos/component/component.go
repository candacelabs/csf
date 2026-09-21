// Package component defines the public bring-up contract for services an
// embedding repository composes alongside CandaceOS Core. A component is a
// value the embedding repository already owns: Core never constructs it, never
// reads its configuration, and never hands it Core state. Core owns only
// ordering — every registered component is assembled before the agent harness
// is constructed, started before the harness starts, and stopped after the
// harness closes, in the exact reverse of the resolved order.
package component

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	// MaxNameBytes bounds a component name.
	MaxNameBytes = 64
	// MaxEventBytes bounds an event passed to ICapabilities.Log. Together with
	// MaxNameBytes it keeps the namespaced record "component.<name>.<event>"
	// inside the 128-byte telemetry event field.
	MaxEventBytes = 48
)

var (
	// ErrNilOption reports a nil functional option, named by its 1-based
	// position in the option list.
	ErrNilOption = errors.New("component: option is nil")
	// ErrNilDefinition reports a nil definition supplied as a requirement or
	// to Order.
	ErrNilDefinition = errors.New("component: definition is nil")
	// ErrName reports a name outside the accepted grammar or byte bound.
	ErrName = errors.New("component: name must match [a-z][a-z0-9-]* within 64 bytes")
	// ErrMissingAssemble reports a definition constructed without WithAssemble.
	ErrMissingAssemble = errors.New("component: assemble function is required")
	// ErrRequirement reports a self-referential or repeated requirement.
	ErrRequirement = errors.New("component: invalid requirement")
	// ErrDuplicateName reports two definitions sharing one name in one ordered
	// set.
	ErrDuplicateName = errors.New("component: duplicate name")
	// ErrMissingRequirement reports a requirement that is absent from the
	// ordered set, naming both components.
	ErrMissingRequirement = errors.New("component: requirement is not in the ordered set")
	// ErrDependencyCycle reports a cycle, naming the full path it traverses.
	ErrDependencyCycle = errors.New("component: dependency cycle")
)

// Definition is one immutable named unit of bring-up work. It holds no Core
// state, so a resolved order can be computed without any infrastructure.
type Definition struct {
	name         string
	assemble     func(ctx context.Context, capabilities ICapabilities) error
	start        func(ctx context.Context) error
	stop         func(ctx context.Context) error
	requirements []*Definition
}

// Name reports the registered component name.
func (definition *Definition) Name() string { return definition.name }

// Assemble runs the required assembly function.
func (definition *Definition) Assemble(ctx context.Context, capabilities ICapabilities) error {
	return definition.assemble(ctx, capabilities)
}

// Start runs the optional start hook, reporting nil when none was declared.
func (definition *Definition) Start(ctx context.Context) error {
	if definition.start == nil {
		return nil
	}
	return definition.start(ctx)
}

// Stop runs the optional stop hook, reporting nil when none was declared.
func (definition *Definition) Stop(ctx context.Context) error {
	if definition.stop == nil {
		return nil
	}
	return definition.stop(ctx)
}

// ICapabilities is the only typed Core surface a component receives.
type ICapabilities interface {
	// Log writes one INFO record through Core's reporter as event
	// "component.<name>.<event>", with message redacted of Core's configured
	// secrets unless the embedding binary opted into raw diagnostics.
	Log(ctx context.Context, event string, message string) error
}

// Option configures one Definition. New validates the complete option set
// before returning a definition.
type Option func(settings *settings) error

type settings struct {
	assemble     func(ctx context.Context, capabilities ICapabilities) error
	start        func(ctx context.Context) error
	stop         func(ctx context.Context) error
	requirements []*Definition
}

// New validates the complete option set and returns an immutable definition.
func New(name string, options ...Option) (*Definition, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	applied := settings{}
	for position, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: component %q option %d", ErrNilOption, name, position+1)
		}
		if err := option(&applied); err != nil {
			return nil, fmt.Errorf("component %q option %d: %w", name, position+1, err)
		}
	}
	if applied.assemble == nil {
		return nil, fmt.Errorf("%w: component %q", ErrMissingAssemble, name)
	}
	if err := validateRequirements(name, applied.requirements); err != nil {
		return nil, err
	}
	return &Definition{
		name:         name,
		assemble:     applied.assemble,
		start:        applied.start,
		stop:         applied.stop,
		requirements: applied.requirements,
	}, nil
}

// WithAssemble sets the required assembly function. It runs during
// bootstrap.AssembleCore, before the harness factory is invoked. The component
// stores what it builds in the embedding repository's own variables; Core
// injects nothing.
func WithAssemble(assemble func(ctx context.Context, capabilities ICapabilities) error) Option {
	return func(settings *settings) error {
		if assemble == nil {
			return ErrMissingAssemble
		}
		settings.assemble = assemble
		return nil
	}
}

// WithStart sets an optional start hook, run immediately before the harness
// starts. Its context is Core's lifecycle context, cancelled at shutdown, so
// goroutines started here are children of the Core lifecycle.
func WithStart(start func(ctx context.Context) error) Option {
	return func(settings *settings) error {
		if start == nil {
			return fmt.Errorf("%w: nil start hook", ErrRequirement)
		}
		settings.start = start
		return nil
	}
}

// WithStop sets an optional stop hook, run in reverse resolved order after the
// harness closes and before Core's store closes. Stop must be idempotent-safe,
// must tolerate a component that never started, and must not block
// indefinitely.
func WithStop(stop func(ctx context.Context) error) Option {
	return func(settings *settings) error {
		if stop == nil {
			return fmt.Errorf("%w: nil stop hook", ErrRequirement)
		}
		settings.stop = stop
		return nil
	}
}

// WithRequires declares dependencies by pointer identity; repeatable.
func WithRequires(requirements ...*Definition) Option {
	return func(settings *settings) error {
		settings.requirements = append(settings.requirements, requirements...)
		return nil
	}
}

// Order resolves definitions into a deterministic initialization order using
// Kahn's algorithm with the ready set processed in registration order, never
// map iteration order. It is pure: it performs no I/O and invokes no
// definition function.
func Order(definitions ...*Definition) ([]*Definition, error) {
	positions, err := positionsOf(definitions)
	if err != nil {
		return nil, err
	}
	for _, definition := range definitions {
		for _, requirement := range definition.requirements {
			if _, present := positions[requirement]; !present {
				return nil, fmt.Errorf(
					"%w: %q requires %q",
					ErrMissingRequirement, definition.name, requirement.name,
				)
			}
		}
	}
	resolved := make([]*Definition, 0, len(definitions))
	settled := make([]bool, len(definitions))
	for len(resolved) < len(definitions) {
		next := readyPosition(definitions, positions, settled)
		if next < 0 {
			return nil, fmt.Errorf(
				"%w: %s",
				ErrDependencyCycle, cyclePath(definitions, positions, settled),
			)
		}
		settled[next] = true
		resolved = append(resolved, definitions[next])
	}
	return resolved, nil
}

func positionsOf(definitions []*Definition) (map[*Definition]int, error) {
	positions := make(map[*Definition]int, len(definitions))
	names := make(map[string]int, len(definitions))
	for position, definition := range definitions {
		if definition == nil {
			return nil, fmt.Errorf("%w: definition %d", ErrNilDefinition, position+1)
		}
		if previous, exists := names[definition.name]; exists {
			return nil, fmt.Errorf(
				"%w: %q at positions %d and %d",
				ErrDuplicateName, definition.name, previous+1, position+1,
			)
		}
		names[definition.name] = position
		positions[definition] = position
	}
	return positions, nil
}

func readyPosition(definitions []*Definition, positions map[*Definition]int, settled []bool) int {
	for position, definition := range definitions {
		if settled[position] || unsettledRequirement(definition, positions, settled) >= 0 {
			continue
		}
		return position
	}
	return -1
}

func unsettledRequirement(
	definition *Definition,
	positions map[*Definition]int,
	settled []bool,
) int {
	for _, requirement := range definition.requirements {
		if position := positions[requirement]; !settled[position] {
			return position
		}
	}
	return -1
}

func cyclePath(definitions []*Definition, positions map[*Definition]int, settled []bool) string {
	current := -1
	for position, resolved := range settled {
		if !resolved {
			current = position
			break
		}
	}
	visited := make(map[int]int, len(definitions))
	path := make([]int, 0, len(definitions))
	for current >= 0 {
		if start, seen := visited[current]; seen {
			return joinNames(definitions, append(append([]int(nil), path[start:]...), current))
		}
		visited[current] = len(path)
		path = append(path, current)
		current = unsettledRequirement(definitions[current], positions, settled)
	}
	return joinNames(definitions, path)
}

func joinNames(definitions []*Definition, path []int) string {
	names := make([]string, 0, len(path))
	for _, position := range path {
		names = append(names, definitions[position].name)
	}
	return strings.Join(names, " -> ")
}

func validateName(name string) error {
	if len(name) == 0 || len(name) > MaxNameBytes || name[0] < 'a' || name[0] > 'z' {
		return fmt.Errorf("%w: %q", ErrName, name)
	}
	for position := 1; position < len(name); position++ {
		value := name[position]
		if (value < 'a' || value > 'z') && (value < '0' || value > '9') && value != '-' {
			return fmt.Errorf("%w: %q", ErrName, name)
		}
	}
	return nil
}

func validateRequirements(name string, requirements []*Definition) error {
	declared := make(map[string]struct{}, len(requirements))
	for position, requirement := range requirements {
		if requirement == nil {
			return fmt.Errorf("%w: component %q requirement %d", ErrNilDefinition, name, position+1)
		}
		if requirement.name == name {
			return fmt.Errorf("%w: component %q requires itself", ErrRequirement, name)
		}
		if _, exists := declared[requirement.name]; exists {
			return fmt.Errorf(
				"%w: component %q requires %q more than once",
				ErrRequirement, name, requirement.name,
			)
		}
		declared[requirement.name] = struct{}{}
	}
	return nil
}
