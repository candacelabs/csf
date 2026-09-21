package bootstrap

import (
	"context"
	"fmt"

	"github.com/candacelabs/csf/services/candaceos/component"
)

// WithComponent registers one component the embedding repository owns. Core
// assembles it after its own dependencies and before the agent harness, starts
// it immediately before the harness starts, and stops it after the harness
// closes, in the exact reverse of the resolved order. The option is repeatable;
// registered components resolve among themselves by their declared
// requirements, with registration order breaking ties.
func WithComponent(definition *component.Definition) Option {
	return func(settings *assemblyOptions) error {
		if definition == nil {
			return fmt.Errorf("%w: CandaceOS component", component.ErrNilDefinition)
		}
		settings.components = append(settings.components, definition)
		return nil
	}
}

func (assembly *coreAssembly) assemble(
	ctx context.Context,
	definition *component.Definition,
) error {
	capabilities := componentCapabilities{assembly: assembly, name: definition.Name()}
	if !assembly.registered(definition) {
		return definition.Assemble(ctx, capabilities)
	}
	if err := definition.Assemble(ctx, capabilities); err != nil {
		return assembly.core.failExtension(ctx, definition.Name(), err)
	}
	assembly.core.extensions = append(assembly.core.extensions, definition)
	return nil
}

func (assembly *coreAssembly) registered(definition *component.Definition) bool {
	for _, candidate := range assembly.settings.components {
		if candidate == definition {
			return true
		}
	}
	return false
}

// componentCapabilities is the entire Core surface a registered component
// receives. It namespaces every record under the component's own name and
// applies Core's configured redaction policy.
type componentCapabilities struct {
	assembly *coreAssembly
	name     string
}

func (capabilities componentCapabilities) Log(
	ctx context.Context,
	event string,
	message string,
) error {
	if len(event) == 0 || len(event) > component.MaxEventBytes {
		return fmt.Errorf(
			"logging CandaceOS component %q: event must contain 1 to %d bytes",
			capabilities.name, component.MaxEventBytes,
		)
	}
	return capabilities.assembly.reporter.componentEvent(ctx, capabilities.name, event, message)
}
