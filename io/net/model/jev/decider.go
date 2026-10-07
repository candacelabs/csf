// Copyright 2026 Candace Labs

package jev

import (
	"context"
	"errors"
	"fmt"
	"strings"

	iohttp "github.com/candacelabs/csf/io/net/http"
)

// ErrInvalidModel reports a decider model the server cannot be asked for.
var ErrInvalidModel = errors.New("jev: invalid decider model")

// IDecider answers typed questions about state text. It is the one contract
// every transport satisfies, so a caller holding one answers the same way
// whether the model is reached locally or over the network.
type IDecider interface {
	// Decide answers every question about state, one distribution per
	// question in question order.
	Decide(ctx context.Context, state string, questions []Question) ([]*Distribution, error)
}

// DeciderModel is one declared decision model: the model the Ollama server is
// asked for, and the readout the answer is taken at. A declaration names a
// model; building one into a working decider is [NewDecider].
type DeciderModel struct {
	// Name is the declared identifier, such as "jevk5_9b_q4", used to select
	// the model and to label its row in the eval table.
	Name string
	// OllamaModel is the reference the Ollama server is asked for, a GGUF tag
	// pulled through the Ollama API.
	OllamaModel string
	// Temperature re-tempers the option letters' logprobs; one is the model's
	// own distribution, above one flattens it, below one sharpens it.
	Temperature float64
	// KnockoutTemperature is the final round's temperature when a choice is
	// wider than the model's 16 slots.
	KnockoutTemperature float64
}

// Validate reports whether the declaration names a model and a usable readout.
func (model DeciderModel) Validate() error {
	if strings.TrimSpace(model.Name) == "" {
		return fmt.Errorf("%w: the name is empty", ErrInvalidModel)
	}
	if strings.TrimSpace(model.OllamaModel) == "" {
		return fmt.Errorf("%w: %s names no Ollama model", ErrInvalidModel, model.Name)
	}
	if model.Temperature <= 0 {
		return fmt.Errorf("%w: %s has temperature %g, which must be positive", ErrInvalidModel, model.Name, model.Temperature)
	}
	if model.KnockoutTemperature <= 0 {
		return fmt.Errorf("%w: %s has knockout temperature %g, which must be positive",
			ErrInvalidModel, model.Name, model.KnockoutTemperature)
	}
	return nil
}

// DecisionCandidates are the declared decision models `csf decide` selects
// from. Both are JEV GGUF builds, pulled through the Ollama API only.
var DecisionCandidates = []DeciderModel{
	{
		Name:                "jevk5_9b_q4",
		OllamaModel:         "hf.co/mindchain/jevk5-9b-v0.3.3-GGUF:Q4_K_M",
		Temperature:         1,
		KnockoutTemperature: 1,
	},
	{
		Name:                "jev_omni_12b_q4",
		OllamaModel:         "hf.co/Reza2kn/Jev-Omni-Q4_K_M-GGUF:Q4_K_M",
		Temperature:         1,
		KnockoutTemperature: 1,
	},
}

// DefaultDeciderModel is the model `csf decide` asks when none is named: the
// candidate with the best held-out accuracy within the measured latency knee.
// The measured winner is jevk5_9b_q4; the omni candidate is a Gemma 4 build
// the server this was measured against cannot load. [ChosenModel] derives this
// same choice from the committed eval table, and a spec holds the two
// together.
var DefaultDeciderModel = DecisionCandidates[0]

// DeclaredModel finds the candidate the name declares, so a caller selects a
// decision model by its declared name and never by a hard-coded reference.
func DeclaredModel(name string) (DeciderModel, error) {
	for _, candidate := range DecisionCandidates {
		if candidate.Name == name {
			return candidate, nil
		}
	}
	return DeciderModel{}, fmt.Errorf("%w: %q is not a declared decision model", ErrInvalidModel, name)
}

// NewDecider builds the local decider for a declared model: a
// [OllamaDecider] carrying the model's own readout. It is the one place a
// declaration becomes a working decider. Further options, such as a server
// other than the local one, are applied after the declaration's own.
func NewDecider(client iohttp.IHTTPClient, model DeciderModel, options ...OllamaOption) (*OllamaDecider, error) {
	if err := model.Validate(); err != nil {
		return nil, err
	}
	readout := []OllamaOption{
		WithOllamaTemperature(model.Temperature),
		WithOllamaKnockoutTemperature(model.KnockoutTemperature),
	}
	return NewOllamaDecider(client, model.OllamaModel, append(readout, options...)...)
}
