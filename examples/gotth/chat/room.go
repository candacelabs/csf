package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The effect sources. They name the effect in provenance and in metrics, so
// they are the strings an operator greps for — and, because a failure event
// reports the source of the effect that failed, the strings the reducer
// matches on. Every server-initiated patch in this application carries one of
// them as "effect:<source>", which is FR-42's requirement in practice.
const (
	SourceSubscribe = "chat.subscribe"
	SourcePost      = "chat.post"
	SourcePurge     = "chat.purge"
	SourcePanic     = "chat.panic"
)

// retryDelay is how long the subscription pump waits before re-offering an
// update the session could not accept. Long enough that a saturated mailbox
// has drained, short enough that a person does not notice.
const retryDelay = 50 * time.Millisecond

// maxRefusals is how many refusals in a row the pump absorbs before it hands
// the decision to the reducer.
//
// Retrying forever inside the effect is the shape that hides a stuck
// subscription: the pump keeps offering, the session keeps not accepting, and
// nothing above the effect ever learns. A bound turns that into a retryable
// failure event, which is something a reducer can act on.
const maxRefusals = 20

// backlogDepth is how many room updates one session may be behind before the
// room stops keeping them.
//
// A chat cannot use the counter's latest-value-wins slot. A counter's snapshot
// is absolute, so an undelivered one carries nothing the next one lacks; a
// message is not, and collapsing two of them loses a sentence. So this is a
// real queue, and the interesting decision is what happens when it fills:
// the room drops, marks the subscriber, and the pump reports a TERMINAL
// failure. Not retryable, deliberately — re-subscribing does not refill a gap,
// and a session that silently missed a message looks right while being wrong.
// The reducer renders a notice and the person reloads, which mounts a fresh
// session with the room's own log.
const backlogDepth = 128

// Post is one message on its way into the room: the body, and the event that
// asked for it.
//
// It carries the body and nothing else about who sent it. The author is read
// from the live.Session[Member] the effect's Run is handed, which is the identity
// Authorize permitted the event under. A reducer cannot name a different one,
// so a message cannot be attributed to somebody who did not send it even if
// the reducer is wrong.
type Post struct {
	Body string
	// Cause is the identifier of the event that asked for this message. It
	// rides through the room so that the push which finally shows the message
	// can name the submission that caused it: the subscription delivering that
	// push was scheduled at mount, so without carrying the cause the only
	// provenance edge left is "some effect did it".
	Cause uint64
}

// Purge is a request to clear the room's log. Like [Post] it names no actor:
// who cleared the room is the session's identity, read at the boundary.
type Purge struct{ Cause uint64 }

// SubscribeEffect is the effect that pushes this session every room update
// until the session ends.
//
// It exists because an effect's Run is the only place an application is handed
// a live.Emitter, so a subscription that wants to inject events has to be
// expressed as a long-running effect. Config.Init registers the session; this
// pumps what the registration collects.
//
// It captures nothing, and that is the point of the live.Session[Member] parameter Run
// takes. A subscription's address is the session it belongs to, which the
// library already knows. Every patch another member's message causes in this
// session carries "effect:chat.subscribe" as its origin.
func (r *Room) SubscribeEffect() live.Effect[Member] {
	return live.Effect[Member]{
		Source: SourceSubscribe,
		Run: func(ctx context.Context, sess live.Session[Member], emit live.Emitter) error {
			return r.pump(ctx, sess.ID(), emit)
		},
	}
}

// PostEffect is the effect that records one message.
//
// The author is resolved inside Run, from the session, for the reason [Post]
// gives: an effect's identity is an input to what the effect does, and it is
// the one the handshake bound rather than the one a reducer named.
func (r *Room) PostEffect(post Post) live.Effect[Member] {
	return live.Effect[Member]{
		Source: SourcePost,
		Run: func(ctx context.Context, sess live.Session[Member], emit live.Emitter) error {
			// No lookup and no assertion: the session is typed by the identity
			// the handshake bound, so the author is read straight off it.
			r.Post(sess.Identity().Name, post, sess.ID())
			return nil
		},
	}
}

// PurgeEffect is the effect that clears the room's log, attributed to the
// session that asked.
func (r *Room) PurgeEffect(purge Purge) live.Effect[Member] {
	return live.Effect[Member]{
		Source: SourcePurge,
		Run: func(ctx context.Context, sess live.Session[Member], emit live.Emitter) error {
			r.Purge(sess.Identity().Name, purge, sess.ID())
			return nil
		},
	}
}

// PanicEffect is FR-23's second site: an effect that panics on purpose.
//
// The library turns it into a gotth.effect_failed event rather than an Error
// frame, and the asymmetry is the requirement rather than an implementation
// detail. A panicking effect leaves state consistent — the reducer never ran
// on a bad value — so the only party who can say whether the failure is
// user-visible is this application, and the reducer is where it says so. The
// patch the failure event produces carries "effect:chat.panic".
func (r *Room) PanicEffect() live.Effect[Member] {
	return live.Effect[Member]{
		Source: SourcePanic,
		Run: func(ctx context.Context, sess live.Session[Member], emit live.Emitter) error {
			// The library recovers it, contains it to this session, counts it
			// against gotthlive_panics_total{site}, logs it with the causal
			// identifiers — and does NOT send an Error frame. It arrives at
			// the reducer as gotth.effect_failed with retryable="false",
			// because re-running a panicking effect re-runs the bug.
			panic("chat: the injected effect panic (" + CmdPanicEffect + ")")
		},
	}
}

// update is one thing that happened to the room, on its way to one session.
//
// It is queued rather than pre-rendered as a live.Event because one of the
// event's fields depends on who is receiving it: the contributing edge back to
// the submission belongs to the session that submitted and to nobody else.
type update struct {
	// kind is the event name this becomes: EventPosted, EventPresence or
	// EventPurged.
	kind    string
	version uint64
	members []string
	msg     Message
	by      string

	// causeFor and cause are the contributing edge. It is a claim about one
	// recipient's own event, so it is only attached when the update reaches
	// that recipient — identifiers are session-scoped and naming another
	// session's event is not a thing that can be true.
	causeFor live.ID
	cause    uint64
}

// event renders the update as the live.Event the pump emits into one session.
func (u update) event(to live.ID) live.Event {
	fields := map[string]string{
		fieldVersion: strconv.FormatUint(u.version, 10),
		fieldMembers: strings.Join(u.members, ","),
	}
	switch u.kind {
	case EventPosted:
		fields[fieldSeq] = strconv.FormatUint(u.msg.Seq, 10)
		fields[fieldAuthor] = u.msg.Author
		fields[fieldBody] = u.msg.Body
		fields[fieldAtMilli] = strconv.FormatInt(u.msg.AtUnixMilli, 10)
	case EventPurged:
		fields[fieldBy] = u.by
	}

	var contributing []uint64
	if u.cause != 0 && u.causeFor == to {
		contributing = []uint64{u.cause}
	}
	return live.Event{Name: u.kind, Contributing: contributing, Fields: live.NewFields(fields)}
}

// subscriber is one session's slot in the room: a bounded backlog and a mark
// saying the room gave up keeping it.
type subscriber struct {
	name  string
	queue chan update
	// behind is set when the room dropped an update for this session. It is
	// read by the pump on its own goroutine and written by whichever session
	// happened to be posting, so it is atomic rather than under the room's
	// lock: the room must not hold its lock while touching a subscriber, or it
	// establishes a lock order nothing else in this file needs.
	behind atomic.Bool
}

func (s *subscriber) offer(u update) {
	select {
	case s.queue <- u:
	default:
		s.behind.Store(true)
	}
}

// Room is the chat itself: the shared log every session reads, and the
// subscription list that pushes changes to them.
//
// It is the application's own state, not the library's. gotth-live gives each
// session a goroutine that owns that session's state and nothing else — there
// is deliberately no cross-session write API — so anything genuinely shared
// lives out here, behind an ordinary mutex, and reaches sessions through
// effects and emitted events. A Redis stream or a Postgres table would take
// the same shape; this one is a struct because the example should run with
// nothing installed.
type Room struct {
	mu       sync.Mutex
	version  uint64
	seq      uint64
	messages []Message
	subs     map[live.ID]*subscriber

	// now is the clock, injectable so a spec can drive the timestamps without
	// waiting for one.
	now func() time.Time
}

// NewRoom returns an empty room.
func NewRoom() *Room {
	return &Room{subs: make(map[live.ID]*subscriber), now: time.Now}
}

// Log returns the room as it stands, for the first HTTP paint.
func (r *Room) Log() *Log {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.logLocked()
}

// Occupants is how many sessions are subscribed. The leak test reads it: a
// teardown that did not unsubscribe leaves this above zero with every
// connection closed.
func (r *Room) Occupants() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.subs)
}

func (r *Room) logLocked() *Log {
	return &Log{
		Version:  r.version,
		Messages: slices.Clone(r.messages),
		Members:  r.membersLocked(),
	}
}

func (r *Room) membersLocked() []string {
	if len(r.subs) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.subs))
	for _, sub := range r.subs {
		out = append(out, sub.name)
	}
	// Sorted, because a map has no iteration order and this list is rendered.
	// The same roster must produce the same markup or every transition would
	// look like a change to it.
	slices.Sort(out)
	return out
}

// Join registers a session for pushes and returns the room as of that moment.
//
// Registering and reading happen under one lock, and that is the whole reason
// this is one method rather than a Subscribe and a Log. Split in two, a
// message landing between them is either shown twice or missed entirely
// depending on the order — and the window is exactly as wide as a page load,
// which is when a second tab opens.
func (r *Room) Join(id live.ID, name string) *Log {
	r.mu.Lock()
	r.subs[id] = &subscriber{name: name, queue: make(chan update, backlogDepth)}
	r.version++
	log := r.logLocked()
	others := r.subscribersLocked(&id)
	r.mu.Unlock()

	// The joiner changed the roster every other session is showing.
	broadcast(others, update{kind: EventPresence, version: log.Version, members: log.Members})
	return log
}

// Leave unregisters a session. Config.Teardown calls it, after the session's
// goroutine has exited, so a session that dropped its connection does not
// leave a subscription behind.
func (r *Room) Leave(id live.ID) {
	r.mu.Lock()
	if _, ok := r.subs[id]; !ok {
		r.mu.Unlock()
		return
	}
	delete(r.subs, id)
	r.version++
	// The queue is dropped, not closed. Closing it would be a nicer way to
	// stop a pump that is still running, and it is unsafe: a post that took
	// the subscriber list under the lock a moment ago is about to offer into
	// that channel from another goroutine, and a send on a closed channel is a
	// panic in the room rather than a contained one in a session. The pump
	// exits on the session's context instead, which is cancelled when the
	// session ends — and Teardown, which calls this, runs after that.
	version, members := r.version, r.membersLocked()
	others := r.subscribersLocked(nil)
	r.mu.Unlock()

	broadcast(others, update{kind: EventPresence, version: version, members: members})
}

// Post records one message and pushes it to every session, the sender
// included. The author is a parameter rather than a field of the effect
// because the executor reads it from the session; see PostEffect.
func (r *Room) Post(author string, e Post, from live.ID) Message {
	r.mu.Lock()
	r.seq++
	r.version++
	msg := Message{
		Seq:         r.seq,
		Author:      author,
		Body:        e.Body,
		AtUnixMilli: r.now().UnixMilli(),
	}
	r.messages = append(r.messages, msg)
	if len(r.messages) > MaxHistory {
		// Re-slice into a fresh backing array rather than sliding the window
		// over the old one, so the *Log values handed out earlier — which are
		// promised to be immutable — cannot be written through.
		r.messages = slices.Clone(r.messages[len(r.messages)-MaxHistory:])
	}
	version, members := r.version, r.membersLocked()
	all := r.subscribersLocked(nil)
	r.mu.Unlock()

	broadcast(all, update{
		kind: EventPosted, version: version, members: members, msg: msg,
		causeFor: from, cause: e.Cause,
	})
	return msg
}

// Purge clears the log and tells everybody who did it.
func (r *Room) Purge(by string, e Purge, from live.ID) {
	r.mu.Lock()
	r.messages = nil
	r.version++
	version, members := r.version, r.membersLocked()
	all := r.subscribersLocked(nil)
	r.mu.Unlock()

	broadcast(all, update{
		kind: EventPurged, version: version, members: members, by: by,
		causeFor: from, cause: e.Cause,
	})
}

// subscribersLocked copies the subscriber list, optionally skipping one.
//
// The copy is taken under the room's lock and delivered outside it. A
// subscriber's offer touches a channel and an atomic, and doing that while
// holding this lock would let a slow send block every other session's post.
func (r *Room) subscribersLocked(except *live.ID) []*subscriber {
	out := make([]*subscriber, 0, len(r.subs))
	for id, sub := range r.subs {
		if except != nil && id == *except {
			continue
		}
		out = append(out, sub)
	}
	return out
}

func broadcast(subs []*subscriber, u update) {
	for _, sub := range subs {
		sub.offer(u)
	}
}

// pump delivers room updates to one session until its context is cancelled.
//
// It runs for the session's whole life, on a goroutine the library owns and
// waits for at shutdown, which is why it returns promptly on cancellation
// rather than on a room signal it might never get. That is also the property
// the leak test measures: no goroutine of this application outlives the
// session it belongs to.
//
// The queue policy above is genuinely this application's — a counter snapshot
// collapses and a message does not — but everything below the queue read is
// the same code examples/counter/store.go's pump has: a refusal budget, a
// fixed delay, a live.Retryable wrap when the budget runs out, and a
// ctx.Done() arm in two places. Two examples out of two have now written it.
// FRICTION.md item F-5.
func (r *Room) pump(ctx context.Context, id live.ID, emit live.Emitter) error {
	r.mu.Lock()
	sub := r.subs[id]
	r.mu.Unlock()
	if sub == nil {
		return fmt.Errorf("chat: session %s is not in the room: Config.Init must Join before it returns a SubscribeEffect", id)
	}

	var (
		pending  update
		holding  bool
		refusals int
	)
	for {
		if !holding {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case u := <-sub.queue:
				pending, holding = u, true
			}
		}

		if sub.behind.Load() {
			// Terminal, and not merely unclassified. Re-subscribing cannot
			// refill a gap, so a retry would restore the appearance of a
			// working subscription over a log that is missing a sentence.
			return fmt.Errorf("chat: this session fell more than %d updates behind the room: reload to catch up", backlogDepth)
		}

		if err := emit(pending.event(id)); err == nil {
			holding, refusals = false, 0
			continue
		}

		// The mailbox was full, or the session is closing.
		refusals++
		if refusals >= maxRefusals {
			// Transient by construction. Neither a full mailbox nor a session
			// mid-shutdown is a property of this effect, so a fresh
			// subscription has every chance of working — which is the claim
			// live.Retryable makes, and the reason it is this code making it
			// rather than the reducer guessing.
			return live.Retryable(fmt.Errorf(
				"chat: the session refused %d updates in a row", refusals))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}
}
