package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The rooms: the shared message logs, presence and typing sets every session
// looks at, and the fixture replay that drives the peer traffic §2.3 specifies.
//
// This is the application's own state, not the library's. gotth-live gives each
// session a goroutine that owns that session's state and nothing else — there
// is deliberately no cross-session write API — so anything genuinely shared
// lives out here behind an ordinary mutex and reaches sessions through effects
// and emitted events. A Redis stream or a Postgres table would take the same
// shape.

// The effect sources. They name the effect in provenance and in metrics, so
// they are the strings an operator greps for — and, because a failure event
// reports the source of the effect that failed, the strings the reducer matches
// on.
const (
	SourceSubscribe = "chat.subscribe"
	SourceSend      = "chat.send"
	SourceSwitch    = "chat.switch"
	SourceTyping    = "chat.typing"
)

// retryDelay and maxRefusals bound the subscription pump's patience before it
// hands the decision to the reducer. Retrying forever inside the effect is the
// shape that hides a stuck subscription.
const (
	retryDelay  = 20 * time.Millisecond
	maxRefusals = 50
)

// backlogDepth is how many updates one session may be behind before the rooms
// stop keeping them. It is generous on purpose: it must not be the thing that
// bounds a slow session, because the library's outbound window is what FR-51
// makes that claim about and a spec cannot tell the two apart if both are
// tight.
const backlogDepth = 4096

// Send is one message on its way into a room (F-CHT-3, CHT-2).
//
// Room is carried because an effect's Run has the session's IDENTITY but not
// its STATE — which room a tab is looking at is one of the things the reducer
// decides — and what a reducer hands an effect should be what the reducer
// decided.
type Send struct {
	Room string
	Body string
	// Cause is the identifier of the event that asked. It rides through the
	// rooms so the broadcast that finally shows the message can name the click
	// that sent it, and it comes back as the message's ClientID so the sender
	// recognises its own confirmation.
	Cause uint64
}

// Switch is a request to move this session to another room (F-CHT-7, CHT-4).
type Switch struct {
	Room  string
	Cause uint64
}

// SubscribeEffect is the effect that pushes this session every change until
// the session ends. It captures nothing: a subscription's address is the
// session it belongs to, which the library already knows and hands to Run.
func (r *Rooms) SubscribeEffect() live.Effect[Member] {
	return live.Effect[Member]{
		Source: SourceSubscribe,
		Run: func(ctx context.Context, sess live.Session[Member], emit live.Emitter) error {
			return r.pump(ctx, sess.ID(), emit)
		},
	}
}

// SendEffect is the effect that accepts one message into a room.
func (r *Rooms) SendEffect(send Send) live.Effect[Member] {
	return live.Effect[Member]{
		Source: SourceSend,
		Run: func(ctx context.Context, sess live.Session[Member], emit live.Emitter) error {
			member := sess.Identity()
			// F-CHT-9 again, and not redundantly. The reducer's refusal is
			// what a reader SEES; this one is what a reader cannot get past.
			// They are two halves of one rule and the second is here because
			// an effect is reachable from anywhere a reducer can be wrong.
			if member.Readonly {
				return fmt.Errorf("chat-gotth: %s is a read-only participant and may not post", member.Name)
			}
			r.Post(send.Room, member.Name, send.Body, strconv.FormatUint(send.Cause, 10), sess.ID(), send.Cause)
			return nil
		},
	}
}

// SwitchEffect is the effect that moves this session to another room.
func (r *Rooms) SwitchEffect(change Switch) live.Effect[Member] {
	return live.Effect[Member]{
		Source: SourceSwitch,
		Run: func(ctx context.Context, sess live.Session[Member], emit live.Emitter) error {
			r.Switch(sess.ID(), sess.Identity().Name, change.Room)
			// The answer to the switch, emitted straight into the asking
			// session rather than queued: it is a fact about one session and
			// nobody else's render changes because of it. The contributing
			// edge names the click, so the patch that finally shows the other
			// room can be traced back to it (FR-42).
			return emit(live.Event{
				Name:         EventEntered,
				Contributing: []uint64{change.Cause},
				Fields:       live.NewFields(map[string]string{fieldRoom: change.Room}),
			})
		},
	}
}

// TypingEffect is the effect that marks this session as typing (F-CHT-6). The
// mark decays on its own after TypingDecay, swept by the replay.
func (r *Rooms) TypingEffect(roomID string) live.Effect[Member] {
	return live.Effect[Member]{
		Source: SourceTyping,
		Run: func(ctx context.Context, sess live.Session[Member], emit live.Emitter) error {
			r.Typing(roomID, sess.Identity().Name)
			return nil
		},
	}
}

// room is one of §2.3's three, as the server owns it.
type room struct {
	id  string
	log *Log
	seq int
	// presence and typing are maps because they are looked up by name; nothing
	// renders from them directly. The lists that DO render are built sorted, so
	// no render depends on Go's map iteration order.
	presence map[string]bool
	typing   map[string]time.Time
}

// update is one thing that happened, on its way to every subscriber.
type update struct {
	kind    string
	roomIdx int
	message Message
	// presence and typing are the already-sorted lists a roster update carries.
	presence []string
	typing   []string
	// causeFor and cause are the contributing edge. It is a claim about one
	// recipient's own event, so it is attached only when the update reaches
	// that recipient: identifiers are session-scoped, and naming another
	// session's event is not a thing that can be true.
	causeFor live.ID
	cause    uint64
}

func (u update) event(to live.ID) live.Event {
	fields := map[string]string{fieldRoom: RoomIDs[u.roomIdx]}
	switch u.kind {
	case EventPosted:
		fields[fieldSeq] = strconv.Itoa(u.message.Seq)
		fields[fieldAuthor] = u.message.Author
		fields[fieldBody] = u.message.Body
		fields[fieldAtMs] = strconv.FormatInt(u.message.AtMs, 10)
		fields[fieldClientID] = u.message.ClientID
	case EventRoster:
		fields[fieldPresence] = strings.Join(u.presence, ",")
		fields[fieldTyping] = strings.Join(u.typing, ",")
	}

	var contributing []uint64
	if u.cause != 0 && u.causeFor == to {
		contributing = []uint64{u.cause}
	}
	return live.Event{Name: u.kind, Contributing: contributing, Fields: live.NewFields(fields)}
}

// subscriber is one session's slot: a bounded backlog and a mark saying the
// rooms gave up keeping it.
type subscriber struct {
	me     string
	queue  chan update
	behind atomic.Bool
}

func (s *subscriber) offer(u update) {
	select {
	case s.queue <- u:
	default:
		s.behind.Store(true)
	}
}

// Rooms is the whole shared surface.
type Rooms struct {
	mu     sync.Mutex
	rooms  [3]*room
	subs   map[live.ID]*subscriber
	replay *Replay
	sha    string

	// now is the clock, a field so a spec can drive presence decay without
	// waiting for it. Nothing in a reducer reads it: every non-deterministic
	// thing this application does is behind the effect boundary, which is what
	// makes the reducer replayable.
	now func() time.Time
}

// Snapshot is what a joining session is handed: every room's log, roster and
// high-water mark, as of the instant it registered.
type Snapshot struct {
	Logs    [3]*Log
	Rosters [3]*Roster
	LastSeq [3]int
}

// NewRooms builds the three rooms from the committed fixture's base record and
// prepares the replay. Start begins it.
func NewRooms(fixture *Fixture) *Rooms {
	r := &Rooms{subs: make(map[live.ID]*subscriber), sha: fixture.SHA256, now: time.Now}
	for i, id := range RoomIDs {
		presence := make(map[string]bool, len(fixture.Base.Presence))
		for _, name := range fixture.Base.Presence {
			presence[name] = true
		}
		r.rooms[i] = &room{id: id, presence: presence, typing: make(map[string]time.Time)}
	}
	r.replay = NewReplay(fixture.Ticks, TickMs*time.Millisecond, r.applyTick)
	return r
}

// SetInterval changes the replay interval before Start.
//
// §2.3 asks for peer traffic at 2 msg/s for latency runs and 20 msg/s for the
// stress row, and §2.5 requires both servers to read the same BYTES — not that
// a rate be baked into them. So the stress row is the same corpus with the tick
// interval divided by ten, which is BENCH-1's reading R-7, and the interval in
// force is recorded in the run manifest.
func (r *Rooms) SetInterval(d time.Duration) {
	if d > 0 {
		r.replay.interval = d
	}
}

// Start begins the fixture replay. Stop ends it and waits.
func (r *Rooms) Start() { r.replay.Start() }

// Stop ends the fixture replay.
func (r *Rooms) Stop() { r.replay.Stop() }

// FixtureSHA256 is the digest of the bytes this process read, for the run
// manifest (§6).
func (r *Rooms) FixtureSHA256() string { return r.sha }

// Clock is §3.2's control channel content: T0 and the replay position.
func (r *Rooms) Clock() (t0 time.Time, tick int) { return r.replay.T0(), r.replay.TickNow() }

/* -------------------------------------------------------------- replay ---- */

// applyTick folds one fixture tick into the rooms and pushes what changed.
//
// Neither this process nor the Next.js one generates the data: both read the
// same committed bytes and apply them on the same monotonic schedule (§2.5), so
// tick N is the same information on both stacks at the same instant.
func (r *Rooms) applyTick(tick Tick) {
	now := r.now()
	nowMs := now.UnixMilli()

	r.mu.Lock()
	var posted []update
	rosterDirty := [3]bool{}

	for _, ev := range tick.E {
		i := RoomIndex(ev.Room)
		if i < 0 {
			continue
		}
		rm := r.rooms[i]
		switch ev.Kind {
		case FixtureMsg:
			m := rm.append(Message{Author: ev.Author, Body: ev.Body, AtMs: nowMs})
			rm.presence[ev.Author] = true
			delete(rm.typing, ev.Author)
			posted = append(posted, update{kind: EventPosted, roomIdx: i, message: m})
			rosterDirty[i] = true
		case FixtureTyping:
			rm.typing[ev.Author] = now.Add(TypingDecay)
			rosterDirty[i] = true
		case FixtureJoin:
			rm.presence[ev.Author] = true
			rosterDirty[i] = true
		case FixtureLeave:
			delete(rm.presence, ev.Author)
			delete(rm.typing, ev.Author)
			rosterDirty[i] = true
		}
	}

	// F-CHT-6's 3 s decay, swept here rather than on a timer of its own. A name
	// whose window has closed changes what every viewer of that room renders,
	// so the room is dirty and gets a push — otherwise "2 people are typing"
	// would stay on screen until the next unrelated event, which is the visible
	// bug the decay exists to prevent.
	for i, rm := range r.rooms {
		for name, until := range rm.typing {
			if !until.After(now) {
				delete(rm.typing, name)
				rosterDirty[i] = true
			}
		}
	}

	var rosters []update
	for i, dirty := range rosterDirty {
		if dirty {
			rosters = append(rosters, r.rooms[i].rosterUpdate(i, now))
		}
	}
	all := r.subscribersLocked()
	r.mu.Unlock()

	// Delivered outside the lock: a subscriber's offer touches a channel and an
	// atomic, and doing that while holding this lock would let one slow send
	// block every other session's tick.
	for _, u := range posted {
		broadcast(all, u)
	}
	for _, u := range rosters {
		broadcast(all, u)
	}
}

// append adds one message and applies F-CHT-1's cap on the SERVER, so both
// stacks render the same number of nodes by construction.
func (rm *room) append(m Message) Message {
	rm.seq++
	m.Seq = rm.seq
	rm.log = rm.log.with(m)
	return m
}

// rosterUpdate builds the already-sorted, already-decayed lists a roster event
// carries. Sorting here rather than at render is what keeps the render a pure
// function of state without every viewer re-sorting the same eight names.
func (rm *room) rosterUpdate(i int, now time.Time) update {
	presence := make([]string, 0, len(rm.presence))
	for name := range rm.presence {
		presence = append(presence, name)
	}
	sort.Strings(presence)

	typing := make([]string, 0, len(rm.typing))
	for name, until := range rm.typing {
		if until.After(now) {
			typing = append(typing, name)
		}
	}
	sort.Strings(typing)

	return update{kind: EventRoster, roomIdx: i, presence: presence, typing: typing}
}

func broadcast(subs []*subscriber, u update) {
	for _, sub := range subs {
		sub.offer(u)
	}
}

func (r *Rooms) subscribersLocked() []*subscriber {
	out := make([]*subscriber, 0, len(r.subs))
	for _, sub := range r.subs {
		out = append(out, sub)
	}
	return out
}

/* ----------------------------------------------------------- lifecycle ---- */

// Join registers a session for pushes and returns every room as of that moment.
//
// Registering and reading happen under one lock, and that is the whole reason
// this is one method rather than a Subscribe and a Snapshot: a message landing
// between them is either shown twice or missed entirely, and the window is
// exactly as wide as a page load.
func (r *Rooms) Join(id live.ID, me, current string) Snapshot {
	r.mu.Lock()
	r.subs[id] = &subscriber{me: me, queue: make(chan update, backlogDepth)}
	if i := RoomIndex(current); i >= 0 {
		r.rooms[i].presence[me] = true
	}
	snap := r.snapshotLocked()
	rosters := r.allRosterUpdatesLocked()
	all := r.subscribersLocked()
	r.mu.Unlock()

	// The joiner changed the roster every other tab in that room is showing.
	for _, u := range rosters {
		broadcast(all, u)
	}
	return snap
}

// Leave unregisters a session. Config.Teardown calls it, after the session's
// goroutine has exited, so a session that dropped its connection does not leave
// a subscription or a presence entry behind.
//
// The queue is dropped, not closed. Closing it would be a nicer way to stop a
// pump that is still running and it is unsafe: a tick that took the subscriber
// list under the lock a moment ago is about to offer into that channel from
// another goroutine, and a send on a closed channel is a panic in the rooms
// rather than a contained one in a session.
func (r *Rooms) Leave(id live.ID) {
	r.mu.Lock()
	sub := r.subs[id]
	delete(r.subs, id)
	if sub != nil {
		for _, rm := range r.rooms {
			delete(rm.presence, sub.me)
			delete(rm.typing, sub.me)
		}
	}
	rosters := r.allRosterUpdatesLocked()
	all := r.subscribersLocked()
	r.mu.Unlock()

	for _, u := range rosters {
		broadcast(all, u)
	}
}

func (r *Rooms) snapshotLocked() Snapshot {
	now := r.now()
	var snap Snapshot
	for i, rm := range r.rooms {
		snap.Logs[i] = rm.log
		snap.LastSeq[i] = rm.seq
		u := rm.rosterUpdate(i, now)
		snap.Rosters[i] = &Roster{Presence: u.presence, Typing: u.typing}
	}
	return snap
}

func (r *Rooms) allRosterUpdatesLocked() []update {
	now := r.now()
	out := make([]update, 0, len(r.rooms))
	for i, rm := range r.rooms {
		out = append(out, rm.rosterUpdate(i, now))
	}
	return out
}

// Post accepts one message into a room and tells every session.
func (r *Rooms) Post(roomID, author, body, clientID string, by live.ID, cause uint64) {
	i := RoomIndex(roomID)
	if i < 0 {
		return
	}
	now := r.now()

	r.mu.Lock()
	rm := r.rooms[i]
	m := rm.append(Message{Author: author, Body: body, AtMs: now.UnixMilli(), ClientID: clientID})
	rm.presence[author] = true
	delete(rm.typing, author)
	roster := rm.rosterUpdate(i, now)
	all := r.subscribersLocked()
	r.mu.Unlock()

	broadcast(all, update{kind: EventPosted, roomIdx: i, message: m, causeFor: by, cause: cause})
	broadcast(all, roster)
}

// Switch moves a session's presence from one room to another. The session's own
// idea of which room it is in is state, and it changes when EventEntered
// arrives — not here.
func (r *Rooms) Switch(id live.ID, me, to string) {
	j := RoomIndex(to)
	if j < 0 {
		return
	}
	r.mu.Lock()
	for _, rm := range r.rooms {
		delete(rm.presence, me)
		delete(rm.typing, me)
	}
	r.rooms[j].presence[me] = true
	rosters := r.allRosterUpdatesLocked()
	all := r.subscribersLocked()
	r.mu.Unlock()

	for _, u := range rosters {
		broadcast(all, u)
	}
}

// Typing marks a name as typing in a room, for TypingDecay. The replay's sweep
// removes it again.
func (r *Rooms) Typing(roomID, me string) {
	i := RoomIndex(roomID)
	if i < 0 {
		return
	}
	now := r.now()

	r.mu.Lock()
	rm := r.rooms[i]
	_, already := rm.typing[me]
	rm.typing[me] = now.Add(TypingDecay)
	roster := rm.rosterUpdate(i, now)
	all := r.subscribersLocked()
	r.mu.Unlock()

	// A name that was already typing has not changed what anybody renders, so
	// the re-mark is silent. That is what keeps a debounced draft binding from
	// being a broadcast per keystroke.
	if already {
		return
	}
	broadcast(all, roster)
}

// pump delivers updates to one session until its context is cancelled.
//
// It runs for the session's whole life, on a goroutine the library owns and
// waits for at shutdown, which is why it returns promptly on cancellation
// rather than on a signal it might never get. That is also the property a leak
// spec measures: no goroutine of this application outlives its session.
func (r *Rooms) pump(ctx context.Context, id live.ID, emit live.Emitter) error {
	r.mu.Lock()
	sub := r.subs[id]
	r.mu.Unlock()
	if sub == nil {
		return fmt.Errorf("chat-gotth: session %s is not subscribed: Config.Init must Join before it returns a SubscribeEffect", id)
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
			// refill a gap, and a chat that silently missed a message looks
			// right while being wrong.
			return fmt.Errorf(
				"chat-gotth: this session fell more than %d updates behind: reload to catch up",
				backlogDepth)
		}

		if err := emit(pending.event(id)); err == nil {
			holding, refusals = false, 0
			continue
		}

		refusals++
		if refusals >= maxRefusals {
			// Transient by construction. Neither a full mailbox nor a session
			// mid-shutdown is a property of this effect, so a fresh
			// subscription has every chance of working — which is the claim
			// live.Retryable makes, and the reason it is this code making it
			// rather than the reducer guessing.
			return live.Retryable(fmt.Errorf(
				"chat-gotth: the session refused %d updates in a row", refusals))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}
}

// Subscribers is how many sessions are subscribed. A leak spec reads it: a
// teardown that did not unsubscribe leaves this above zero with every
// connection closed.
func (r *Rooms) Subscribers() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.subs)
}

// LogOf returns one room's log, for the first HTTP paint and for specs.
func (r *Rooms) LogOf(roomID string) *Log {
	i := RoomIndex(roomID)
	if i < 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rooms[i].log
}

// PageSnapshot is what the page handler renders before any connection exists.
func (r *Rooms) PageSnapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked()
}
