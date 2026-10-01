package httpserver

import (
	"net/http"
	"time"
)

// NewStreamingServer returns the canonical server shape for long-lived SSE or
// WebSocket responses. It deliberately leaves ReadTimeout and WriteTimeout
// unset; ReadHeaderTimeout still bounds the unauthenticated request phase.
func NewStreamingServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}
