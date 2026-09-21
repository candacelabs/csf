package config

import (
	"errors"
	"strings"
)

// ProviderModel identifies one model within one provider.
type ProviderModel struct {
	ProviderID string
	ModelID    string
}

// ParseProviderModel parses the provider/model reference used by agent APIs.
// Model IDs may contain further slashes; neither component may be empty.
func ParseProviderModel(value string) (ProviderModel, error) {
	providerID, modelID, found := strings.Cut(value, "/")
	if !found || providerID == "" || modelID == "" || strings.HasSuffix(modelID, "/") {
		return ProviderModel{}, errors.New("must be providerID/modelID")
	}
	return ProviderModel{ProviderID: providerID, ModelID: modelID}, nil
}
