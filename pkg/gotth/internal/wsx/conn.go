package wsx

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"go.opentelemetry.io/otel/attribute"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
)

// conn is one connection: two goroutines, both owned and both waited for.
type conn[I session.IIdentity] struct {
	h     *Handler[I]
	ws    *websocket.Conn
	peer  session.Peer[I]
	actor *session.Actor[I]
	fr    *protocol.Framer
	done  chan struct{}

	// idStr and idAttr are this session's identifier rendered once, for the
	// same reason session.Actor holds them: session.Peer.ID.String() hex-encodes into a
	// fresh string, and the read pump reaches it on EVERY inbound frame. It
	// does not change for the life of the connection.
	idStr  string
	idAttr attribute.KeyValue

	// closeOnce makes the close idempotent, and closeCode is atomic because
	// the drain path closes a session from a goroutine that has no other
	// synchronisation with the one serving it.
	closeOnce sync.Once
	closeCode atomic.Int64
}

// newConn builds the connection's own state, and nothing else: no goroutine, no
// actor, no ticker, no registration.
//
// It is separate from serve because the handler must be able to REGISTER a
// connection before any goroutine exists for it — see the C-34 comment in
// ServeHTTP. A conn that is built and registered but whose serve has not been
// entered is already closeable and already waited for: `ws` is set, so
// `Close`'s `c.close(...)` reaches the socket, and `done` is created here, so
// `Close`'s `<-c.done` has something to wait on.
func (h *Handler[I]) newConn(ws *websocket.Conn, peer session.Peer[I]) *conn[I] {
	id := peer.ID.String()
	return &conn[I]{
		h:      h,
		ws:     ws,
		peer:   peer,
		done:   make(chan struct{}),
		idStr:  id,
		idAttr: attribute.String(obs.AttrSessionID, id),
	}
}

// serve runs one connection to completion.
//
// Exactly two goroutines exist for it, and this function is one of them. The
// read pump is here; the session actor is the other, started below and waited
// for before this returns. Writes are performed by whichever goroutine has
// something to say, serialized by the framer, so there is no third goroutine
// and no write-side queue.
//
// Since ServeHTTP returns at the upgrade (handler.go says why), this runs on a
// goroutine the handler started rather than on net/http's connection goroutine.
// The count in RFC-0001 §3.4 is unchanged — this goroutine IS the read pump,
// and net/http's has gone home — but net/http's recover went home with it, so
// the teardown below carries a guard of its own.
func (h *Handler[I]) serve(ctx context.Context, c *conn[I], app session.IApp[I]) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ws, peer := c.ws, c.peer

	var actorDone sync.WaitGroup
	var ticker *time.Ticker

	// The whole teardown, and the panic guard, in one deferred function
	// registered before anything that can panic.
	//
	// RFC-0001 §9 makes recovery mandatory because "a panic in any goroutine
	// kills the process unless recovered", and until ServeHTTP began returning
	// at the upgrade this goroutine was net/http's, which recovers. It is no
	// longer. §9's table names three guarded sites — reduce, render, effect —
	// and this is not a fourth kind of failure but the same one at the only
	// site whose guard used to belong to somebody else; it emits no new metric
	// and no new label, so nothing docs/instrumentation.md specifies moves.
	//
	// It runs the teardown as well as the recovery, and that ordering is the
	// point: a session that panicked and then failed to cancel its actor, name
	// a close code, or release Close's wait would be a worse failure than the
	// panic it contained.
	defer func() {
		if r := recover(); r != nil {
			h.opts.Logger.Error(ctx, "gotth-live: the connection goroutine panicked",
				obs.Str("session_id", c.idStr),
				obs.Str("panic", fmt.Sprint(r)),
				obs.Str("stack", string(debug.Stack())))
			c.close(protocol.CloseInternalError, "the connection goroutine panicked")
		}

		cancel()
		actorDone.Wait()
		if ticker != nil {
			ticker.Stop()
		}

		code := c.finalCode()
		h.opts.Metrics.ConnectionClosed(ctx, code.Label())
		h.opts.Logger.Info(ctx, "gotth-live: session closed",
			obs.Str("session_id", c.idStr),
			obs.Str("close_code", code.Label()))

		// A close that names no code is a defect, so the fallback is a code
		// rather than silence: the ordinary end of a connection is a normal
		// close.
		_ = ws.Close(websocket.StatusCode(code), "")

		// deregister BEFORE releasing Close's wait, and the order is C-34's
		// second half. `Close` waits on `c.done` and then returns; if the
		// registry entry were removed after `done` closed, `Sessions()` could
		// still report a live session at the instant `Close` returned nil —
		// which is one of the two things L9-1's probe caught. The original
		// order was the other way round and had the same lag.
		h.deregister(ctx, c)
		close(c.done)
	}()

	// The inbound cap is applied to the connection before anything is read, so
	// an oversize frame is refused by the transport rather than buffered and
	// then rejected. That is what makes it the authoritative limit.
	ws.SetReadLimit(int64(h.opts.Limits.MaxInboundFrameBytes))

	c.fr = protocol.NewFramer(func(ctx context.Context, b []byte) error {
		wctx, cancelWrite := context.WithTimeout(ctx, h.opts.Limits.WriteDeadline)
		defer cancelWrite()
		return ws.Write(wctx, websocket.MessageBinary, b)
	})
	c.fr.OnSent = func(kind protocol.Kind, n int) {
		h.opts.Metrics.FrameSent(ctx, kind.String(), n)
	}
	c.fr.OnInvalid = func(kind protocol.Kind, err error) {
		h.opts.Metrics.OutboundInvalid(ctx, kind.String())
	}

	ticker = time.NewTicker(h.opts.Limits.HeartbeatInterval)

	c.actor = session.New(session.Options[I]{
		Peer:    peer,
		App:     app,
		Limits:  h.opts.Limits,
		Framer:  c.fr,
		Close:   c.close,
		Metrics: h.opts.Metrics,
		Tracer:  h.opts.Tracer,
		Logger:  h.opts.Logger,
		Dev:     h.opts.Dev,
		Ticks:   ticker.C,
	})

	// Registration already happened, on the handler's goroutine, before this
	// one existed (C-34). It is deliberately NOT repeated here: the whole point
	// is that a session is in the registry from before `Close` could have
	// snapshotted without it.

	actorDone.Add(1)
	go func() {
		defer actorDone.Done()
		c.actor.Run(ctx)
	}()

	if err := c.actor.Ready(ctx); err == nil {
		c.readPump(ctx)
	}
}

// readPump reads until the connection ends.
//
// It never blocks on a full channel. A flood is dropped with a typed error
// answered on this goroutine, because blocking here would stall the
// connection's own liveness handling — the failure the bounds exist to
// prevent, reached by a different route.
func (c *conn[I]) readPump(ctx context.Context) {
	limits := protocol.Limits{MaxInboundFrameBytes: c.h.opts.Limits.MaxInboundFrameBytes}

	for {
		typ, data, err := c.ws.Read(ctx)
		if err != nil {
			c.noteReadError(ctx, err)
			return
		}

		if typ != websocket.MessageBinary {
			// Every payload in both directions is one encoded frame. A text
			// frame is a protocol error, not a debug convenience.
			c.h.opts.Metrics.FrameRejected(ctx, protocol.ReasonTextFrame)
			c.close(protocol.CloseProtocolViolation, "text frames are not part of this protocol")
			return
		}

		// gotthlive.parse is the first span on FR-36's event path — "receive →
		// refine/parse → authorize → …" — and after clause 4 it is the whole
		// server-side path's ROOT, which is the property that matters more
		// than the span itself. A sampler decides here, once, and authorize,
		// the transition and everything under it inherit that decision through
		// the context and the SpanRef the ingress carries. Before this the path
		// had three roots and three independent decisions (C-30).
		//
		// The span covers the unmarshal, the refinement boundary and the §6
		// session-identity invariant, because those are one act of deciding
		// whether these bytes are a frame for this session.
		//
		// It is opened for every inbound frame, not only events: an ack and a
		// heartbeat are parsed too, and a parse span that appeared only for the
		// frames that turned out to be events would be a measurement of parsing
		// that excludes most of it.
		parseCtx := ctx
		var parseSpan obs.Span
		if c.h.opts.Tracer.Enabled() {
			parseCtx, parseSpan = c.h.opts.Tracer.Start(ctx, obs.SpanParse,
				c.idAttr,
				attribute.Int(obs.AttrFrameBytes, len(data)))
		}

		in, err := protocol.ParseInbound(data, limits)
		if err == nil {
			err = protocol.CheckSessionID(in, c.peer.ID)
		}
		if err != nil {
			parseSpan.RecordError(err)
			parseSpan.End()
			if c.rejected(parseCtx, err) {
				return
			}
			continue
		}

		kind := in.Kind().String()
		parseSpan.SetAttributes(attribute.String(obs.AttrFrameKind, kind))
		parseSpan.End()

		c.h.opts.Metrics.FrameReceived(parseCtx, kind, len(data))
		c.actor.Ingress(parseCtx, in)
	}
}

// rejected answers a refused frame and reports whether the connection is over.
func (c *conn[I]) rejected(ctx context.Context, err error) bool {
	var rej *protocol.RejectError
	if !errors.As(err, &rej) {
		// Unreachable while ParseInbound and CheckSessionID return only
		// *RejectError, and a conformance spec holds them to it. It is kept
		// because the alternative to this arm is a type assertion that panics,
		// and it now LOGS rather than closing in silence: an error the library
		// produced and told nobody about is the failure FR-58 exists to
		// prevent, and this was the one path in the read pump that did it.
		c.h.opts.Logger.Error(ctx, "gotth-live: refused an inbound frame with an error that is not a *protocol.RejectError: this is a library bug, not a client problem",
			obs.Str("session_id", c.idStr),
			obs.Err(err))
		c.close(protocol.CloseProtocolViolation, "unparseable frame")
		return true
	}

	c.h.opts.Metrics.FrameRejected(ctx, rej.Reason)
	c.h.opts.Logger.Warn(ctx, "gotth-live: refused an inbound frame",
		obs.Str("session_id", c.idStr),
		obs.Str("reason", rej.Reason),
		obs.Err(err))

	// The error frame is written from this goroutine rather than routed through
	// the mailbox, because a rejection must be deliverable exactly when the
	// mailbox cannot accept anything. The framer serializes it against the
	// actor's own writes.
	if _, sendErr := c.fr.Send(ctx, protocol.NewError(
		c.peer.ID, rej.Code, rej.Detail, 0, 0, rej.Fatal(),
	)); sendErr != nil {
		c.h.opts.Logger.Warn(ctx, "gotth-live: could not deliver a rejection to the client",
			obs.Str("session_id", c.idStr), obs.Err(sendErr))
	}

	if rej.Fatal() {
		c.close(rej.Close, rej.Reason)
		return true
	}
	return false
}

func (c *conn[I]) noteReadError(ctx context.Context, err error) {
	if errors.Is(err, context.Canceled) || websocket.CloseStatus(err) != -1 {
		return
	}
	c.h.opts.Logger.Debug(ctx, "gotth-live: the connection ended",
		obs.Str("session_id", c.idStr), obs.Err(err))
}

// close ends the connection with an enumerated code. It is idempotent and safe
// from any goroutine; the first code named is the one recorded, because it is
// the one that describes why.
func (c *conn[I]) close(code protocol.CloseCode, reason string) {
	c.closeOnce.Do(func() {
		c.closeCode.Store(int64(code))
		if len(reason) > 120 {
			reason = reason[:120]
		}
		_ = c.ws.Close(websocket.StatusCode(code), reason)
	})
}

func (c *conn[I]) finalCode() protocol.CloseCode {
	if code := protocol.CloseCode(c.closeCode.Load()); code.Valid() {
		return code
	}
	return protocol.CloseNormal
}
