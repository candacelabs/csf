// Copyright 2026 Candace Labs

package jev

import (
	"fmt"
	"io"
	stdhttp "net/http"
	"strings"

	iohttp "github.com/candacelabs/csf/io/net/http"
)

// exchange sends request through the http capability and returns the bounded
// response body, failing on any status other than 200. It is the one crossing
// both transports share: the Space and the local Ollama server are reached the
// same way, and only their paths, payloads and headers differ.
func exchange(request *stdhttp.Request, client iohttp.IHTTPClient) ([]byte, error) {
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("jev: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	content, err := io.ReadAll(io.LimitReader(response.Body, maxAnswerBytes))
	if err != nil {
		return nil, fmt.Errorf("jev: read answer: %w", err)
	}
	if response.StatusCode != stdhttp.StatusOK {
		return nil, fmt.Errorf("jev: HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(content)))
	}
	return content, nil
}
