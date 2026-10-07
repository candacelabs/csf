// Copyright 2026 Candace Labs

package github

import _ "embed"

// Description is the filtered OpenAPI description the client was generated
// from: the source of each GitHub tool's input and output schema.
//
//go:embed api.github.com.json
var Description []byte
