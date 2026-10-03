// Copyright 2026 Candace Labs

package harness_test

import "encoding/json"

// jsonUnmarshal decodes one record of unknown shape.
func jsonUnmarshal(data []byte, value any) error { return json.Unmarshal(data, value) }
