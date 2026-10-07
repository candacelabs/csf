// Copyright 2026 Candace Labs

package harness_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

// jsonUnmarshal decodes one record of unknown shape.
func jsonUnmarshal(data []byte, value any) error { return json.Unmarshal(data, value) }

// handlerTransport answers an HTTP client's requests by calling handler in
// process: the MCP client speaks the real streamable HTTP protocol to the
// CSF service's handler with no listener between them.
type handlerTransport struct {
	handler http.Handler
}

func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}
