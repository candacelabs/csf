package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const shutdownTimeout = 10 * time.Second

// Serve runs server until it fails or ctx is canceled, then performs a bounded
// graceful shutdown and waits for the listener goroutine to exit.
func Serve(ctx context.Context, server *http.Server) error {
	if ctx == nil {
		return errors.New("httpserver: context is nil")
	}
	if server == nil {
		return errors.New("httpserver: server is nil")
	}
	originalBaseContext := server.BaseContext
	baseContextCleanup := make(chan func(), 1)
	// Shutdown waits for active handlers but deliberately does not cancel
	// them. Merge the process lifecycle into the listener's base context so an
	// SSE or WebSocket handler observing request.Context exits before that
	// drain, while preserving any values or earlier cancellation the caller put
	// on BaseContext.
	server.BaseContext = func(listener net.Listener) context.Context {
		if originalBaseContext == nil {
			return ctx
		}
		base := originalBaseContext(listener)
		if base == nil {
			return nil
		}
		merged, cancelMerged := context.WithCancel(base)
		stopLifecycle := context.AfterFunc(ctx, cancelMerged)
		baseContextCleanup <- func() {
			stopLifecycle()
			cancelMerged()
		}
		return merged
	}
	defer func() {
		server.BaseContext = originalBaseContext
		select {
		case cleanup := <-baseContextCleanup:
			cleanup()
		default:
		}
	}()
	serverError := make(chan error, 1)
	go func() {
		serverError <- server.ListenAndServe()
	}()

	select {
	case err := <-serverError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			_ = server.Close()
			<-serverError
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		err := <-serverError
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP while shutting down: %w", err)
		}
		return nil
	}
}

// Probe requests a health endpoint and requires a 2xx response.
func Probe(ctx context.Context, target string) error {
	if ctx == nil {
		return errors.New("httpserver: context is nil")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("create health request: %w", err)
	}
	client := &http.Client{
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request health endpoint: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}
