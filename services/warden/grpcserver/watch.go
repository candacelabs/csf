package grpcserver

import (
	"google.golang.org/grpc/status"

	wardenv1 "github.com/candacelabs/csf/services/warden/proto/warden/v1"
	"github.com/candacelabs/csf/services/warden/wireconv"
)

// watchSubscriberBuffer sizes the per-stream ViewSource subscription. A small
// buffer lets a brief stream.Send stall coexist with the election loop's
// non-blocking publish without the loop dropping every intermediate signal; the
// handler collapses whatever it finds to the latest snapshot anyway (see below),
// so the exact depth only trades wake-ups against memory, never correctness.
const watchSubscriberBuffer = 16

// WatchCluster streams full ClusterView snapshots to a client, one per observed
// cluster-state change, keyed by ClusterViewCursor for dedup and resume.
//
// The handler is a self-contained consumer goroutine (the one gRPC runs it in).
// It owns exactly one ViewSource subscription and tears it down on return. It
// never blocks the election loop: the subscription channel is best-effort
// (the loop drops when the buffer is full), and on every wake the handler
// re-reads ViewSource.View() for the LATEST snapshot rather than trusting the
// channel payload — a slow client therefore skips straight to current state
// (drop-to-latest) instead of applying a backlog, and the loop is never
// back-pressured.
//
// Dedup: two snapshots with an equal cursor denote the same observable state, so
// a repeat (e.g. the periodic publish tick re-emitting an unchanged follower
// view) is suppressed. The `since` cursor lets a resuming client suppress the
// redundant initial snapshot when its state already matches.
//
// Teardown: the select watches the stream context (client disconnect or RPC
// deadline) AND the server drain signal, so the handler always returns promptly
// and unsubscribes — no goroutine or subscription leak. Removing the stream-ctx
// case would leak the handler goroutine on client disconnect; that is the
// goroutine-leak counterfactual the contract suite exercises.
func (s *Server) WatchCluster(req *wardenv1.WatchClusterRequest, stream wardenv1.WardenService_WatchClusterServer) error {
	ctx := stream.Context()

	changes, cancel := s.views.Subscribe(watchSubscriberBuffer)
	defer cancel()

	// Initial snapshot, unless the client resumes from a cursor that already
	// matches the current state.
	current := wireconv.ClusterViewToProto(s.views.View())
	lastSent := wireconv.CursorOf(current)
	if since := req.GetSince(); since == nil || !wireconv.CursorEqual(since, lastSent) {
		if err := stream.Send(&wardenv1.ClusterViewUpdate{View: current, Cursor: lastSent}); err != nil {
			return err
		}
	}

	for {
		select {
		case <-ctx.Done():
			// Client disconnected or the RPC deadline elapsed.
			return status.FromContextError(ctx.Err()).Err()
		case <-s.drain.Done():
			// The node is draining; end the stream cleanly so the client
			// reopens the watch elsewhere.
			return errDraining
		case _, ok := <-changes:
			if !ok {
				return errViewSourceClosed
			}
			// Drop-to-latest: the receive is only a change signal; read the
			// current snapshot and dedup by cursor.
			view := wireconv.ClusterViewToProto(s.views.View())
			cursor := wireconv.CursorOf(view)
			if wireconv.CursorEqual(cursor, lastSent) {
				continue
			}
			if err := stream.Send(&wardenv1.ClusterViewUpdate{View: view, Cursor: cursor}); err != nil {
				return err
			}
			lastSent = cursor
		}
	}
}
