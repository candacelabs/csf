# gotth-live wire protocol — liquid proto mapping spec

| | |
|---|---|
| **Status** | Current protocol-v1 mapping; Phase 0 review closed, implementation synced 2026-08-11 |
| **Date** | 2026-08-04; last synced 2026-08-11 |
| **Author** | DEV-1 (Server Core / Go) |
| **Protocol version** | 1 |
| **Transport** | WebSocket, binary frames — [ADR-001](adr/001-transport.md) |
| **Satisfies** | PRD FR-3, FR-4, FR-5, FR-6, FR-8, FR-9, FR-10, FR-11, FR-13, FR-39, FR-40, FR-41, FR-42; review checklist §3 |
| **Resolves** | PRD §7.2 Q3, Q4, Q8, Q14; PRD R-1, R-3, R-13 |

---

## 1. Scope and the framing rule

Every WebSocket **message payload**, in both directions, is exactly one encoded
`gotthlive.v1.Frame`, sent as a binary (opcode `0x2`) message. There is no
JSON, no text framing, no side channel, and no debug escape hatch
(PRD FR-3, checklist §3.1, §3.2).

**Two things on the wire are not frames, and cannot be on any transport:** the
RFC 6455 *opening handshake* (an HTTP request/response) and the RFC 6455 *close
frame* (a numeric code plus a reason string). Review-checklist **§3.1 was amended
on 2026-08-04 (L9-1)** to carve out transport *establishment* and *closure*
exactly, and the carve-out carries **two obligations**. Both are discharged here:

**Obligation 1 — anything application-meaningful the handshake negotiates must be
re-asserted in-band.** The handshake carries one such thing: the subprotocol
token `gotth-live.v1` (§8.1). It is **not** the source of truth. The negotiated
version is re-asserted as `Frame.protocol_version` in the first `Snapshot` and
validated by the client (§8.2), so a mismatch is caught by the protocol even if
the token were forged or omitted. Nothing else application-meaningful crosses the
handshake.

**Obligation 2 — the wire audit must state how establishment and close bytes are
accounted for**, rather than letting "zero non-`Frame` bytes" quietly mean "zero
except the ones we didn't count". The audit is defined **by WebSocket opcode**:

| Opcode | Audit assertion |
|---|---|
| `0x2` binary | **every** payload parses as a `Frame` and re-encodes byte-identically. This is the "100 % of bytes" claim in PRD G5, RFC E5, and ADR X4, and it is now scoped to this row. |
| `0x1` text | **count must be zero.** A received text frame is a protocol error (§8.3, `4002`). |
| `0x8` close | counted separately, **not** parsed as a `Frame`; the audit asserts every close code is a member of §8.3's enumeration and that the count equals `gotthlive_connections_closed_total`. |
| `0x9`/`0xA` ping/pong | the library **never initiates** these — liveness is the `Heartbeat` *frame*, not RFC 6455 ping (§3.4). The transport library may auto-respond to a client-initiated ping; those are counted and excluded by opcode, and a non-zero count is reported, not hidden. |
| HTTP upgrade bytes | outside the WebSocket message stream entirely; asserted separately to carry only the headers §8.1 names. |

So the honest form of the claim, and the one the audit implements, is: **zero
non-`Frame` application bytes; transport control accounted for by opcode with
its counts published.**

---

## 2. Design constraints imposed by Liquid Proto

The canonical
[`protoc-gen-liquidproto`](../../liquidproto/cmd/protoc-gen-liquidproto)
generator and [`liquidproto/v1`](../../liquidproto/v1) annotation schema —
packages of this same module, at `pkg/liquidproto` — are authoritative. Their limits are **verified from source**, not
assumed, and they shape this schema:

| Limit | Source | Consequence for this schema |
|---|---|---|
| Supported singular scalars are `bool`, signed/unsigned integer and fixed-width integer kinds, `string`, and `bytes`; floats, doubles, enums, and messages are rejected | `internal/expr/types.go` `FromProtoKind` | every generated predicate is carried on a supported plain scalar field |
| **`repeated` fields cannot be refined** | `internal/gen/gen.go` `compileField` | list cardinality is a hand-checked invariant (§6, H-4) |
| **`map` fields cannot be refined** | `internal/gen/gen.go` `compileField` | **no `map` appears anywhere in this schema** |
| **`enum` fields cannot be refined** | `FromProtoKind` default case | enum domain is a hand-checked invariant (§6, H-1), enforced generically via protoreflect |
| **Fields inside a `oneof` cannot be refined** | `internal/gen/gen.go` `compileField` | the `Frame` oneof members are messages (nothing to refine there anyway); no scalar ever sits directly in a oneof |
| **Fields with explicit presence (`optional`) cannot be refined** | `internal/gen/gen.go` `compileField` | **no `optional` appears anywhere in this schema**; absence is encoded as the zero value with a documented meaning |
| **Nested messages are not recursively validated** | generated `Validate<Message>` functions check only that message's annotated scalar fields | staged validation, §5 |
| Predicate grammar: literals, `this`, `! && \|\| == != < <= > >= ( )`, `len(this)` (string/bytes), `matches(this, "RE2")` (string only). Arithmetic, remainder, division, and cross-field references are rejected. | `internal/expr/compile.go` | every cross-field invariant is named in §6 |

**Consequence to state plainly:** the schema below is shaped by the generator's
supported field model, not merely annotated after the fact. No `map`, no
`optional`, no scalar in a oneof, and identifiers sized so `len()` and
`matches()` do real work.

---

## 3. Schema

Committed at `pkg/gotth/proto/gotthlive/v1/frame.proto`. Predicates use the
canonical option form,
`[(candace.liquid.v1.field) = {expr: "…"}]`. The schema imports
`liquidproto/v1/refinement.proto` and compiles with stock `protoc`; no forked
protobuf frontend or research-tree dependency is in the generation path.

Predicates are shown below in `where`-style shorthand for readability; the
committed `.proto` uses the option form.

### 3.1 Envelope

```proto
syntax = "proto3";
package gotthlive.v1;

message Frame {
  uint32 protocol_version = 1;  // where this > 0
  bytes  session_id       = 2;  // where len(this) == 16

  oneof payload {                       // field numbers 3..10: 1-byte tags
    Event           event            = 3;
    Ack             ack              = 4;
    Heartbeat       heartbeat        = 5;
    ClientTelemetry client_telemetry = 6;
    ResyncRequest   resync_request   = 7;
    Patch           patch            = 8;
    Snapshot        snapshot         = 9;
    Error           error            = 10;
  }
}
```

Field numbers 1–10 are deliberate: all fit a 1-byte tag, and the envelope
overhead is therefore **22–23 bytes** on every frame (`session_id` 2+16,
`protocol_version` 2, payload tag+len 2–3). §9 measures it.

*Cycle-2 correction (review condition C-17): this line and the normative
`.proto` header both said **21**, which is not what the components sum to —
18 + 2 + 2 = 22 at the low end, 23 when the payload length needs a second
varint byte. §9's table was right and these two were the stale statements. The
figure is a range because the payload length is, and rounding a range down to
its floor is how a number stops being checkable.*

**`protocol_version` carries no upper bound, and that is the point.** The
predicate rejects only the *structurally* impossible value — `0`, which in proto3
is indistinguishable from "unset". Every *semantic* version judgement belongs to
H-2 (§6), which compares majors against the server's own and answers an
unsupported one with `Error{UNSUPPORTED_VERSION}` and close `4003`. An upper
bound here would put the graceful path out of reach for any client above it: a
version-16 client would be rejected as a malformed frame rather than as an
unsupported version, and the field whose whole job is negotiating versions would
acquire a ceiling it could not cross without a breaking change to itself. The
clean division is: **schema predicates reject values that are structurally
invalid for protocol v1; H-2 rejects structurally valid versions the server
cannot serve, with a reason.**

`session_id` is 16 bytes because PRD FR-41 requires a patch frame captured *in
isolation* to be resolvable, and because it is the same width as an OTel trace
ID (`docs/instrumentation.md` §4). Trimming it is an FR-43 event requiring an
ADR with measurements; §9 exists so that ADR would be cheap to write.

### 3.2 Client → server

```proto
message Event {
  uint64 client_ref      = 1;  // where this > 0
  string name            = 2;  // where len(this) > 0 && len(this) <= 64
                               //    && matches(this, "^[a-z][a-z0-9_.:-]*$")
  string fragment_id     = 3;  // where len(this) > 0 && len(this) <= 64
                               //    && matches(this, "^[A-Za-z0-9_:.-]+$")
  uint64 seen_server_seq = 4;  // where this > 0
  repeated EventField fields = 5;
}

message EventField {
  string key   = 1;  // where len(this) > 0 && len(this) <= 128
                     //    && matches(this, "^[A-Za-z0-9_.\\[\\]-]+$")
  string value = 2;  // where len(this) <= 8192
}

message Ack {
  uint64 server_seq = 1;  // where this > 0   — highest *contiguous* seq applied
}

message ResyncRequest {
  uint64      last_applied_seq = 1;  // where this > 0
  ResyncReason reason          = 2;
}

message ClientTelemetry {
  uint64 patch_id     = 1;  // where this > 0
  uint32 morph_micros = 2;  // where this <= 60000000
  uint32 apply_micros = 3;  // where this <= 60000000
}
```

- `client_ref` is the client's own monotonic correlation handle — **not** the
  authoritative causal ID (§4). A `uint64` varint costs 1–3 bytes where a
  128-bit UUID would cost 18, and the client runtime needs no ID generator,
  which matters against NFR-2.
- `seen_server_seq` is **the causation edge**: the sequence number of the last
  patch the user was looking at when they acted. It is what makes stale-view
  conflicts detectable rather than silent (teardown §6.6). It is `> 0` because
  the first `Snapshot` establishes `server_seq = 1` and the client may not send
  a frame before receiving it (§8.2).
- `EventField` is the form-data carrier. It is a `repeated` **message**, so it is
  neither validated by `ValidateEvent` nor cardinality-bounded by the generator — both
  are handled in §5 and §6.

**Which limit is authoritative (resolves the former Q-P2).** Three bounds could
constrain an inbound `Event`: `EventField.value <= 8192`, `len(fields) <= 64`
(H-4, reduced from 256), and `max_inbound_frame_bytes` (H-5, default 65,536).
**H-5 is the single authoritative limit** — it is enforced by
`Conn.SetReadLimit` *before any payload is allocated*, so it is the only one that
bounds memory rather than merely rejecting after the fact. The other two are
deliberately *not* required to multiply up to it: they are defence in depth that
bound one field's and one list's contribution, and they exist to fail early with
a field-specific error (FR-58) rather than a generic "frame too large". Stated
here so nobody later removes the smaller bounds as redundant — they are
subordinate, not superfluous.

### 3.3 Server → client

```proto
message Patch {
  uint64 server_seq    = 1;  // where this > 0
  uint64 patch_id      = 2;  // where this > 0
  uint64 transition_id = 3;  // where this > 0
  uint64 state_version = 4;  // where this > 0
  Origin origin        = 5;
  repeated FragmentUpdate updates = 6;
}

message Snapshot {
  uint64 server_seq    = 1;  // where this > 0
  uint64 patch_id      = 2;  // where this > 0
  uint64 transition_id = 3;  // where this > 0
  uint64 state_version = 4;  // where this > 0
  Origin origin        = 5;
  uint32 heartbeat_interval_ms   = 6;  // where this >= 1000 && this <= 300000
  uint32 max_inbound_frame_bytes = 7;  // where this >= 1024 && this <= 1048576
  uint32 ack_window              = 8;  // where this >= 1 && this <= 256
  repeated FragmentUpdate updates = 9;

  // The supersession edge. Both are 0 on a session's first Snapshot and both
  // are non-zero on a resync Snapshot (H-13). See §4.3.
  uint64 superseded_from_seq     = 10;
  uint64 superseded_through_seq  = 11;
}

message FragmentUpdate {
  string  fragment_id = 1;  // where len(this) > 0 && len(this) <= 64
                            //    && matches(this, "^[A-Za-z0-9_:.-]+$")
  PatchOp op          = 2;
  string  html        = 3;  // where len(this) <= 1048576
}

message Origin {
  OriginKind kind       = 1;
  uint64     event_id   = 2;
  uint64     client_ref = 3;
  string     source     = 4;  // where len(this) > 0 && len(this) <= 64
                              //    && matches(this, "^[a-z][a-z0-9_.:/-]*$")
  repeated uint64 contributing_event_ids = 5;
}

message Error {
  ErrorCode code       = 1;
  string    message    = 2;  // where len(this) <= 512
  uint64    event_id   = 3;
  uint64    client_ref = 4;
  bool      fatal      = 5;
}
```

**`Origin.source` prefix vocabulary (convention, not schema).** The regex admits
`:` and `/` so the value can be namespaced. The library uses, and the docs
require: `event:<name>` for `CLIENT_EVENT`, `effect:<EffectSource()>` for
`EFFECT`, `timer:<name>`, `pubsub:<topic>`, and the bare literals `mount` and
`resync`. There is **no registration step** — a source is whatever
`Effect.EffectSource()` returns — so cardinality is bounded at the *metric*
rather than at the schema: instrumentation §2.1 caps the `source` label at 64
distinct values per process, after which further values collapse to `other` and
`gotthlive_source_label_overflow_total` increments. Traces and the provenance log
carry the full value; only the metric label is capped.

**`Origin.source` is where a refinement does load-bearing product work.** PRD
FR-42 says `unknown` is not a permitted origin value; the predicate
`len(this) > 0` makes an origin-less patch **rejectable at both mandatory
boundaries**: it can be represented by an ordinary generated protobuf struct
or decoded from bytes, but it cannot reach application code inbound or the
socket outbound. That is the difference between an unenforced rule and a
checked serialization boundary.

`html` is `string`, not `bytes`, so protobuf's own UTF-8 validation runs during
`Unmarshal` — a free correctness check *before* the generated `Validate*` boundary is even
reached.

### 3.4 Bidirectional

```proto
message Heartbeat {
  uint64 nonce       = 1;  // where this > 0
  uint32 interval_ms = 2;  // where this >= 1000 && this <= 300000
}
```

Server→client sets `interval_ms` (FR-6's heartbeat-interval bound); the client
echoes `nonce` and sends `interval_ms = 0`… which would violate the predicate.
**Resolved:** the client echoes the server's `interval_ms` verbatim. The
predicate is total in both directions and the echo doubles as an
acknowledgement that the client honoured the interval.

### 3.5 Enumerations

```proto
enum PatchOp      { PATCH_OP_UNSPECIFIED = 0; MORPH = 1; APPEND = 2; PREPEND = 3; REMOVE = 4; }
enum OriginKind   { ORIGIN_KIND_UNSPECIFIED = 0; CLIENT_EVENT = 1; EFFECT = 2; TIMER = 3;
                    PUBSUB = 4; MOUNT = 5; RESYNC = 6; }
enum ResyncReason { RESYNC_REASON_UNSPECIFIED = 0; GAP = 1; RECONNECT = 2; CLIENT_REQUEST = 3; }
enum ErrorCode    { ERROR_CODE_UNSPECIFIED = 0; UNSUPPORTED_VERSION = 1; UNAUTHORIZED = 2;
                    INVALID_FRAME = 3; UNKNOWN_EVENT = 4; UNKNOWN_FRAGMENT = 5;
                    RATE_LIMITED = 6; INTERNAL = 7; RESYNC_FAILED = 8; }
```

The `_UNSPECIFIED = 0` members exist because proto3 requires a zero value. They
are **never valid on the wire** — rejected by H-1 (§6).

---

## 4. Causal identity (FR-39, FR-40, FR-41; PRD Q3, Q14)

### 4.1 Who mints what

| ID | Minted by | Type | Why |
|---|---|---|---|
| `session_id` | **server**, at handshake, 16 random bytes | `bytes` | untrusted input can never name another session |
| `client_ref` | **client**, monotonic from 1 per connection | `uint64` | lets the client correlate its own pending event *before* the first patch, and costs 1–3 bytes |
| `event_id` | **server**, monotonic from 1 per session | `uint64` | the authoritative causal root; unforgeable |
| `transition_id` | **server**, monotonic from 1 per session | `uint64` | one per reducer invocation, including no-op transitions |
| `state_version` | **server**, monotonic from 1 per session | `uint64` | increments **iff** the transition changed state |
| `patch_id` | **server**, monotonic from 1 per session | `uint64` | one per emitted `Patch`/`Snapshot` |
| `server_seq` | **server**, monotonic from 1 per session | `uint64` | FR-11 ordering/gap detection |

**This answers PRD Q3 without compromise.** The client mints only a *local
correlation handle*; the authoritative chain is entirely server-minted, so no
untrusted value ever enters provenance. The server echoes `client_ref` back in
`Origin`, which is what gives the client its correlation.

**This answers PRD Q14.** `state_version` is a **monotonic `uint64` counter**,
not a hash and not a vector: v1 is single-node (PRD R-14), the session actor is
the sole writer, so there is exactly one total order and nothing to reconcile. A
hash would cost CPU per transition and buy comparison we never make; a vector
clock would encode concurrency that cannot exist.

Global uniqueness of a session-scoped `uint64` comes from the pair
`(session_id, id)`. This is why `session_id` rides the envelope (§3.1).

### 4.2 The chain

```
Event{client_ref, seen_server_seq}
   │  server mints event_id
   ▼
event_id ──▶ transition_id ──▶ state_version ──▶ patch_id ──▶ server_seq
                                                    │
                                              Origin{kind: CLIENT_EVENT,
                                                     event_id, client_ref,
                                                     source: "event:<name>"}
```

Server-initiated patches (FR-42) carry `kind ∈ {EFFECT, TIMER, PUBSUB, MOUNT}`,
`event_id = 0`, and a `source` naming the effect (`"effect:chat.broadcast"`,
`"timer:dashboard.tick"`). Where the effect was *scheduled by* an earlier event,
that event's id appears in `contributing_event_ids`.

**`RESYNC` is not in that list, and that is a correction.** A GAP resync is
caused by a specific client frame — `ResyncRequest` — which reaches the actor and
is authorized as a distinguished event kind (RFC §11.3.1). It is event-shaped, so
it gets an `event_id` like any other inbound event, and the resulting `Snapshot`
carries `Origin{kind: RESYNC, event_id: <that id>, client_ref: <the request's>}`.
A resync is the one server-initiated frame with a specific, nameable client
cause; leaving `event_id = 0` there would have discarded provenance we already
had.

### 4.3 The supersession edge — provenance across a resync boundary

A resync `Snapshot` replaces some range of patches the client never applied.
Without an explicit edge, an analyst holding a wire capture cannot answer *"which
events produced the DOM the user is now looking at?"* across that boundary: the
superseded patches were emitted and counted, then dropped, and the `Snapshot`
that replaced them named none of them. Supersession would be bookkeeping, not
causality.

`Snapshot.superseded_from_seq` / `superseded_through_seq` close it. On a resync
`Snapshot` they carry the inclusive `server_seq` range this snapshot replaces —
every sequence number the server emitted after the client's
`ResyncRequest.last_applied_seq` and before this snapshot. On a session's first
`Snapshot` both are `0`.

**Why a range and not the union of contributing event IDs.** The union is
unbounded — at the dashboard workload a long gap can accumulate thousands of
events, which would collide with H-4's list cardinality bound and reintroduce
exactly the truncation-is-provenance-loss problem H-4 exists to prevent (see
also RFC §7.4's flush trigger). The range is two varints, is exact, and is
sufficient: the superseded patches are themselves in the capture — P8 guarantees
the framer emitted and counted every one — so an analyst walks
`[from, through]`, reads each patch's `Origin`, and recovers the event set. The
range converts P7 from a reconciliation category into a checkable causal
property.

---

## 5. Staged validation — how deep the boundary reaches (PRD Q8 / R-3)

**Answer: gotth-live uses `pkg/liquidproto`'s canonical Liquid Proto toolchain
and works within its non-recursive validation model structurally.** There is no
remaining research-tree implementation or vendored runtime.

### 5.1 The mechanism

`ValidateFrame` validates the envelope scalars only (`protocol_version`,
`session_id`) — the oneof members are messages and pass through verbatim. The
library therefore provides **one** ingress function and no other path from
wire bytes to a dispatchable payload:

```go
// package gotthlive/internal/protocol
//
// ParseInbound is the sole entry point for bytes arriving from a client.
// There is no exported way to obtain an inbound payload that has not passed
// through it.
func ParseInbound(b []byte, limits Limits) (Inbound, error)

// Inbound is a closed sum type. Every variant holds immutable scalar snapshots
// copied only after validation. Envelope returns a deep clone.
type Inbound interface {
    isInbound()
    Kind() Kind
    Envelope() *pb.Frame
}

type InboundEvent struct {
    inboundBase
    clientRef, seenServerSeq uint64
    name, fragmentID string
    fields []EventField
}
func (e InboundEvent) ClientRef() uint64
func (e InboundEvent) Fields() []EventField // returns a copy
```

`ParseInbound` runs, in order:

1. `proto.Unmarshal` into the raw generated `Frame` (UTF-8 validation for
   `string` fields happens here, free).
2. `ValidateFrame` — envelope predicates.
3. Version compatibility check (H-2).
4. Direction check: a client cannot send a server-only payload.
5. Descriptor walk for enum domains (H-1) and repeated-field bounds (H-4).
6. Switch on the oneof; for the matched kind call its generated `Validate*`
   function **explicitly**.
7. For `Event`, call `ValidateEventField` on **every** repeated element.
8. Only after those validators succeed, copy payload scalars and repeated event
   fields into an immutable closed `Inbound` value; `Envelope` exposes only a
   deep clone.
9. The connection and session boundaries enforce the stateful and cross-frame
   invariants from §6 that need session state (H-3, H-7, H-8, H-10, H-11,
   and H-14).

### 5.2 Why a new event kind cannot skip a step

The switch is backed by a **conformance test that walks the `Frame` descriptor
via protoreflect**. It enumerates every member of the `payload` oneof, requires
every client-sendable member to have a valid corpus entry, and sends an
all-zero payload through `ParseInbound`; acceptance proves its `Validate*`
boundary was skipped and fails the test. Adding a oneof member without wiring
it therefore fails the suite. This is the structural answer checklist §3.4
asks for, and it is the same shape as checklist §5.3's "adding-an-event thought
experiment".

### 5.3 Outbound: the same re-checking boundary, in reverse

**Server→client frames also cross a validation boundary, immediately before
marshal.** This is not symmetry for its own sake. Generated protobuf structs
have constructible zero values, and Liquid Proto validators deliberately check
messages in place rather than wrapping them in opaque types. Inbound frames
cross the boundary because they are parsed. Outbound frames are **constructed**,
so without an explicit step they are protected only by construction discipline —
and an `Origin` assembled with an empty source anywhere in the emit path would
produce an orphan patch that nothing catches, because the client codec does not
enforce `matches` predicates (§10.3) and therefore neither does the independent
decode in `livetest.Audit`.

```go
// package gotthlive/internal/protocol
//
// ValidateOutbound re-checks a constructed frame against every predicate in the
// schema, plus the §6 invariants, through the generated Validate* boundary. It
// is called by the framer immediately before
// proto.Marshal, on the single write path, and it is not optional.
func ValidateOutbound(f *pb.Frame) error
```

Mechanically it is the ingress pipeline minus the unmarshal: `ValidateFrame` →
H-1/H-4 descriptor walk → switch on the oneof → the payload's `Validate*` →
per-element `Validate*` for repeated messages → the cross-field checks of §6.
Failure is an internal error, not a client-visible one: the frame is dropped,
`gotthlive_outbound_validation_failed_total{kind}` increments, and an `Error`
frame is emitted in its place with the causal chain intact (RFC §9's panic-guard
path, since a frame we cannot construct correctly is a library bug).

Cost is one extra pass over a small message on the emit path. It is measured in
Phase 1 against NFR-1's budget and reported; **the default is the boundary**, and
removing it would require an ADR with the measurement, not a judgement call. It
converts P1 from a discipline into a property.

### 5.4 Canonical toolchain and consumer dependency

The full Liquid Proto toolchain has one owner: `pkg/liquidproto`, a package of
this same Go 1.26 module rather than a module of its own.

- `pkg/liquidproto/v1/refinement.proto` owns the canonical field option,
  `(candace.liquid.v1.field)`.
- `pkg/liquidproto/cmd/protoc-gen-liquidproto` compiles those predicates into
  the committed `frame_liquid.pb.go` validators.
- `pkg/liquidproto` owns the small runtime imported by generated validators,
  including inspectable `*liquidproto.Error` values and redacted production
  formatting.
- `pkg/gotth/gen.sh --check` builds the generator from that canonical source,
  includes its schema root, and proves both protobuf outputs are
  byte-reproducible.

Consumers link only the `pkg/liquidproto` runtime; they do **not** need
`protoc`, the annotation schema, or the generator to build gotth-live because
generated output is committed (FR-7). The two live in one module,
`github.com/candacelabs/csf`, so during the unpublished bootstrap a consumer
checkout needs ONE local replacement rather than two, and that one goes away
with the first published version.

---

## 6. Hand-checked invariants (PRD R-13) — the named list

R-13 requires that invariants the predicate grammar cannot express be **named**,
not silently assumed covered. This is that list. It is exhaustive for protocol
version 1. Each has an ID, an enforcement site, and a test.

| ID | Invariant | Why the grammar cannot express it | Enforcement | Test |
|---|---|---|---|---|
| **H-1** | Every `enum` field holds a value declared in its descriptor, and never `*_UNSPECIFIED = 0` | enums are not a refinable kind (`FromProtoKind`) | **One** generic checker walks the message via protoreflect, finds every enum field, and validates against `EnumDescriptor.Values()`. Not per-field code — a new enum field is covered automatically | table test enumerating every enum in the schema × {valid, 0, out-of-range} |
| **H-2** | `protocol_version`'s **major** matches the server's | cross-value comparison against server config; grammar has no free variables | `ParseInbound` step 3 → `Error{UNSUPPORTED_VERSION, fatal}` + close `4003` | version-skew test, both directions |
| **H-3** | `Frame.session_id` equals the session bound to the connection | cross-frame/connection state | transport ingress, before dispatch → close `4002` | session-confusion attack test |
| **H-4** | `len(Event.fields) <= 64`; `len(Patch.updates) <= 64`; `len(Origin.contributing_event_ids) <= 1024`, with the last acting as a **coalescing flush trigger**, never a truncation (RFC §7.4) | `repeated` is not refinable | a limits table in `protocol`; a test asserts **every** `repeated` field in the schema has an entry, so a new list field cannot be added without a bound | descriptor-walk test + oversize-list rejection + a coalescing test that drives the union to the bound and asserts a flush, not a drop |
| **H-5** | Total decoded frame size ≤ `max_inbound_frame_bytes` | the predicate is per-field, not per-message | `Conn.SetReadLimit` **before** allocation (FR-13), belt-and-braces re-check after decode | hostile oversize-frame test |
| **H-6** | `Origin.event_id != 0` **iff** `Origin.kind` is **event-bearing** — that is, `CLIENT_EVENT` **or** `RESYNC`; same for `client_ref`. *(Amended 2026-08-04, L9-1 — see the note below the table.)* | cross-field | `protocol.validateOrigin`, on the outbound boundary (§5.3) and nowhere else | table test over all `OriginKind`, with both `RESYNC` arms present |
| **H-7** | `Ack.server_seq` ≤ the highest `server_seq` the server has emitted, and never decreases | cross-frame session state | session actor, on ack ingress → close `4002` on violation | replay/forged-ack test |
| **H-8** | `Event.seen_server_seq` ≤ highest emitted `server_seq` | cross-frame session state | session actor → `Error{INVALID_FRAME}` | forged-causation test |
| **H-9** | `server_seq` increases by exactly 1 per emitted sequenced frame | cross-frame | framer (single writer, §7 P3) | wire-capture conformance property P3 |
| **H-10** | `Snapshot` is the first frame on a connection, and the client sends nothing before it | ordering, not a value | server framer; client decoder | handshake-ordering test |
| **H-11** | `ClientTelemetry.patch_id` names a patch actually sent to this session | cross-frame; and it is **untrusted input**, so it is *rejected*, not trusted, if unknown | session actor; unknown → counted and dropped, never used to fabricate a span | telemetry-forgery test |
| **H-12** | `Error.event_id`/`client_ref` are 0 unless the error is event-scoped | cross-field | `protocol.validateError` | table test |
| **H-14** | `ResyncRequest` obeys its **own** rate budget, independent of the event bucket: minimum interval 1 s, burst 3 (RFC §7.6). A resync whose `last_applied_seq` already equals the current `server_seq` is answered with an `Ack`, not a `Snapshot` | rate is cross-frame state; no predicate can express it | session actor, before the re-render is scheduled → `Error{RATE_LIMITED}`, then close `4008` on sustained abuse | amplification test: 50 `ResyncRequest`/s from one authenticated client must not produce 50 full renders |
| **H-13** | Two clauses, enforced in different places — see the note below the table. **(a) the range clauses:** `Snapshot.superseded_from_seq` and `superseded_through_seq` are **both** 0 (first snapshot of a session) or **both** non-zero with `from <= through < server_seq` (resync snapshot). **(b) the kind clause:** a resync snapshot has `Origin.kind == RESYNC` iff they are non-zero | cross-field, and the zero case is legitimate so no predicate can express it | **(a)** `protocol.validateSnapshot` on the outbound boundary (§5.3) **and** the client, in `runtime.js`'s `applied()`, which additionally pins `from === seq + 1` against the sequence the client actually holds → close `4002`. **(b)** the outbound boundary only | table test over {first, resync, forged} snapshots; client side in `client/test/supersession.test.mjs` |

**H-1 and H-4 are the two that would otherwise rot**, because they are the ones a
new field silently escapes. Both are therefore implemented as *descriptor
walks*, not as per-field code, and both have a meta-test that fails when the
schema grows a field the mechanism does not cover.

**H-6's amendment — "event-bearing", not "`CLIENT_EVENT`"** (2026-08-04, L9-1,
[checkpoint-2 ruling batch §2](reviews/checkpoint-2-batch.md)). Cycle 2's B-7
removed `RESYNC` from §4.2's `event_id = 0` list and made a resync `Snapshot`
carry the identifiers of the `ResyncRequest` that caused it. H-6's own sentence
was not updated with it, so this table said `CLIENT_EVENT` while
`protocol.validateOrigin` — the enforcement this row names — accepted
`CLIENT_EVENT` **or** `RESYNC`, and the only place the two were reconciled was a
comment in `internal/protocol/invariants.go`. The wider rule is the correct one
and always was: a resync is caused by a specific, nameable client frame, and
zeroing its identifiers would discard provenance the server already holds
(§4.2). The table now states what the code enforces. **No implementation
changes**; §7's P2 and P6 are corrected in the same edit, because a stale twin
is how a corrected sentence gets un-corrected.

The vocabulary is now one word in three places: an origin kind is
**event-bearing** when it names an inbound frame of this session. Exactly two
kinds are — `CLIENT_EVENT` and `RESYNC` — and adding a third is a protocol
change that must move H-6, P2, P6 and `eventBearing` together.

**H-6's enforcement column — "and at parse" struck** (2026-08-04, L9-1,
[review-wave ruling 5.3](reviews/rulings-review-wave.md); REV-INV U-9). The
invariant holds; the claim about *where* did not. `validateOrigin` is called
only from `refineOriginAndUpdates`, which is reached only from
`ValidateOutbound`, and `Origin` appears only in `Patch` and `Snapshot` — both
server→client — so there is no parse path it could ever run on. The second half
of the enforcement claim described nothing. **No implementation changes**; a
normative table that names an enforcement site which does not exist is the same
defect as one that names the wrong rule, and it is worse in the direction that
matters, because it reads as coverage.

**H-13's enforcement is split by clause, deliberately** (2026-08-04; REV-INV
U-1/U-2 landed client-side in `79403c6a`, scoped here by
[review-wave ruling 5.2](reviews/rulings-review-wave.md)). This row used to say
`protocol.validateSnapshot` runs *"on both the outbound boundary and the client
decoder"*, and until that commit the client half was fiction: the generated
codec had decoded fields 10 and 11 since they were added and `runtime.js` read
neither. The client now enforces **the range clauses** — both zero, or both
non-zero with `from === seq + 1 <= through < server_seq`, `|| 0` normalization
pinned by spec — and closes `4002` naming what disagreed.

It deliberately does **not** enforce the `Origin.kind == RESYNC` iff-range
clause, and that asymmetry is the reason this row is split rather than left
whole. Comparing one `OriginKind` member from the client requires importing the
generated enum, which is a single object, so it ships all six members —
measured at **126 gzipped bytes** for the identically-shaped `ErrorCode` import
(`client/SIZE.md` §1.1.3), against a whole landing of 72 (§1.1.4). The two
clauses also do different work: the range clause constrains **what the client
does next** — it is the only side that knows where its own DOM stopped — while
the kind clause only labels the frame, and a mislabelled frame is already
refused by the outbound boundary before it is written. So the split is a
statement about which side can act, not a budget concession dressed up as one.
**The table now matches the code in both directions**, which is the property
§12 already records this table losing once, at H-6 in checkpoint 2 — that time
by stating a rule narrower than the code enforced, this time by stating an
enforcement site broader than the code had.

---

## 7. Provenance conformance properties (for QA-1)

Phrased as machine-checkable properties over a wire capture plus the server's
provenance log. These are the tests behind PRD G4, FR-41, and checklist §4.4.

| ID | Property | How QA-1 checks it |
|---|---|---|
| **P1** | **No orphan patches.** Every `Patch`/`Snapshot` has `origin.source != ""` and `origin.kind != ORIGIN_KIND_UNSPECIFIED` | `source != ""` is enforced **at the outbound `ValidateOutbound` boundary** (§5.3), which calls the generated `ValidateOrigin` on every constructed origin — *not* by construction discipline, because generated protobuf structs have constructible zero values. `kind` by H-1. Assert over every frame in a 30-min soak capture |
| **P2** | **Chain closure.** For every frame whose `origin.kind` is **event-bearing** (H-6 — `CLIENT_EVENT` or `RESYNC`), `origin.event_id` names an inbound frame this session received — an `Event` for `CLIENT_EVENT`, the `ResyncRequest` for `RESYNC` — and `origin.client_ref` equals that frame's `client_ref` | join capture ↔ provenance log on `(session_id, event_id)`; 0 unresolved. **Both arms must be exercised**, so a run that produced no resync snapshot fails the property rather than passing it vacuously |
| **P3** | **Sequence integrity.** Per session, `server_seq` starts at 1 and increases by exactly 1 across every sequenced frame | scan the capture per `session_id` |
| **P4** | **Transition semantics.** `transition_id` is unique per session; `state_version` is non-decreasing and increases **iff** state changed | provenance log; cross-check against the reducer determinism harness (FR-15) |
| **P5** | **Coalescing preserves provenance.** For any patch with `contributing_event_ids` non-empty, the union of those ids over a run equals the set of events that produced a state change and were not individually patched | dashboard soak (FR-62); set equality, not sampling |
| **P6** | **Standalone resolvability (FR-41).** Given only the bytes of one patch or snapshot frame, `(session_id, patch_id)` resolves to its transition, **either its originating event or its named server-effect source** (PRD G4's disjunction — a server-initiated patch that no client frame caused carries `event_id = 0` by design; a resync `Snapshot` is the one server-sent frame that *does* name one, §4.2 and H-6), and the render that produced each fragment | the automated test PRD FR-41 mandates: capture a random frame, discard all other context, resolve. Both arms of the disjunction are exercised: a `CLIENT_EVENT` patch and an `EFFECT`/`TIMER`/`PUBSUB` patch. A `RESYNC` snapshot resolves through the **first** arm and must be in the capture, or the resync half of P6 is untested |
| **P7** | **Ack closure, with a causal edge.** Every emitted `server_seq` is (a) acked, (b) **inside the `[superseded_from_seq, superseded_through_seq]` range of a subsequent resync `Snapshot`** (§4.3), or (c) accounted for by a close frame. Case (b) is now a stated causal edge on the wire, not a reconciliation category | reconcile framer counters against acks and supersession ranges in the capture; 0 unaccounted. Additionally assert the ranges are contiguous and non-overlapping per session |
| **P8** | **Single write path (checklist §4.4).** The number of frames in the wire capture equals the framer's emitted counter, exactly | wire capture vs `gotthlive_frames_sent_total`; any drift means a second write path exists |

P6 is the one that cannot be satisfied by a design argument, and P8 is the one
that catches the failure mode checklist §4.4 calls an automatic return.

---

## 8. Lifecycle, versioning, and close codes

### 8.1 Establishment (FR-8, FR-45, FR-46, FR-48)

Ordered, and the order is the security property (checklist §5.2 — authenticate
**before** allocating per-session memory):

1. HTTP `GET` with `Upgrade: websocket`, `Sec-WebSocket-Protocol: gotth-live.v1`.
2. **Origin check** against the allowlist → `403`, no state allocated (FR-45).
3. **Authenticate** via the consumer's hook on the *HTTP request* → `401`, no
   state allocated (FR-46).
4. **CSRF token check** — a token bound to the authenticated application session
   → `403` (FR-48).
5. Subprotocol match → `426` on mismatch (fast reject; **not** the source of
   truth, §1).
6. Accept `101`. **Only now** is `session_id` minted and the actor spawned.
7. Server sends `Snapshot` (`server_seq = 1`) carrying the session parameters.
8. Client may now send frames (H-10).

### 8.2 Version negotiation (FR-9)

Three layers, deliberately redundant: subprotocol token (fast reject) →
envelope `protocol_version` refined to `this > 0` (parse-boundary reject of the
structurally impossible value only, §3.1) → major-version equality in Go (H-2,
semantic reject with a human-readable reason). A version mismatch is **never** resolved by silently reinterpreting
fields.

### 8.3 Close codes (FR-8 — "closed for unknown reason is a bug")

Private range 4000–4999. Closed enumeration; a test enumerates every `Close(`
call site in the library and asserts each names a constant from this table.

Each code has exactly one **metric label value**, given here so dashboards, the
close-code enumeration, and the §5 audit cannot drift (instrumentation A-11).

| Code | Name | `code` label | Meaning |
|---|---|---|---|
| 4000 | `NORMAL` | `normal` | client or server closed cleanly |
| 4001 | `GOING_AWAY` | `going_away` | server shutting down / draining |
| 4002 | `PROTOCOL_VIOLATION` | `protocol_violation` | text frame, non-`Frame` bytes, H-3, H-7 |
| 4003 | `UNSUPPORTED_VERSION` | `unsupported_version` | H-2 |
| 4004 | `UNAUTHENTICATED` | `unauthenticated` | identity hook failed post-upgrade |
| 4005 | `FORBIDDEN_ORIGIN` | `forbidden_origin` | origin allowlist |
| 4006 | `UNAUTHORIZED` | `unauthorized` | authorization hook returned a fatal denial |
| 4007 | `FRAME_TOO_LARGE` | `frame_too_large` | H-5 |
| 4008 | `RATE_LIMITED` | `rate_limited` | FR-51 inbound limits, including the §6 H-14 resync bucket |
| 4009 | `SLOW_CLIENT` | `slow_client` | outbound window exhausted (RFC §7.4) |
| 4010 | `HEARTBEAT_TIMEOUT` | `heartbeat_timeout` | FR-12 |
| 4011 | `SESSION_EVICTED` | `session_evicted` | idle timeout (FR-22) |
| 4012 | `INTERNAL_ERROR` | `internal_error` | contained panic that could not be recovered into the session |
| 4013 | `RESYNC_FAILED` | `resync_failed` | resync could not produce a consistent snapshot |

### 8.4 Schema evolution (checklist §3.7)

- Field numbers are **never** reused or renumbered; deleted fields become
  `reserved` with a comment naming the version that removed them.
- Every PR touching `proto/` states its compatibility class in the description:
  **additive** / **wire-compatible** / **breaking**.
- A breaking change bumps `protocol_version`'s major and adds a migration note
  to this document.
- **Forward compatibility (FR-10):** the server preserves unknown fields because
  Liquid Proto's `Validate*` functions inspect the original generated message
  in place; library code must not re-marshal through an intermediate struct,
  and a round-trip test with a future-schema frame asserts it. **The client
  decoder must skip unknown tags by wire type, never error.**

---

## 9. Provenance byte cost (PRD R-9, feeding any future FR-43 ADR)

Stated now so the future decision is arithmetic, not argument. Per-frame fixed
provenance overhead, protocol version 1:

| Component | Bytes | Note |
|---|---|---|
| `session_id` (tag + len + 16) | 18 | the dominant fixed cost |
| `protocol_version` (tag + varint) | 2 | |
| payload tag + length | 2–3 | |
| `server_seq`, `patch_id`, `transition_id`, `state_version` | 8–20 | 4 × (1 tag + 1–4 varint) |
| `Origin` (kind, event_id, client_ref, source ≈ 20 chars) | 28–32 | `source` dominates |
| **Total per `Patch`** | **58–75** | before any HTML |

**Why the 18-byte `session_id` also rides client→server frames**, when the
connection already determines the session and H-3 exists only to reconcile a
frame that could disagree with it. Two reasons, recorded now while the arithmetic
is fresh so a future FR-43 ADR inherits them rather than re-deriving them. First,
one `Frame` message means one schema, one generated codec, one refinement
boundary, and one audit path on both sides; a direction-split envelope would
double all four for 18 bytes on the cheaper direction. Second, **the alternative
is not free**: the predicate grammar has no free variables (§2), so a
direction-dependent predicate is inexpressible — `session_id` would have to
become unrefined or move into each payload, and either way `len(this) == 16`
stops being a total guarantee. Inbound frames are also the low-volume direction:
at the dashboard workload the client sends single-digit frames per second against
53 patches.

At the dashboard workload (53 updates/s per session, equivalence spec §3.4) that
is **≈3.1–4.0 KB/s per session** of provenance, against HTML fragment payloads
that are typically an order of magnitude larger. Phase 5 measures the real
ratio (PRD §6 Phase 5, "wire bytes per interaction, with the provenance overhead
broken out as its own line"). **Nothing is trimmed now**; FR-43 governs.

---

## 10. Client codec contract (PRD Q4 / R-1)

### 10.1 What the client must do

- **Decode**: `Patch`, `Snapshot`, `Error`, `Heartbeat`.
- **Encode**: `Event`, `Ack`, `ResyncRequest`, `ClientTelemetry`, `Heartbeat`.
- **Skip unknown tags by wire type** (FR-10). Never throw on an unknown field.
- No generic protobuf runtime. Evidence: the entire Datastar framework is
  13,277 B gzip (teardown §5.2); a general protobuf-JS runtime would consume the
  majority of a 12,288 B budget on its own.

### 10.2 The codec is **generated**, not hand-written

This is the half of R-1 that actually causes bugs — a wrong field number, a
wrong wire type, a field someone forgot. A Go generator,
`pkg/gotth/internal/cmd/gen-clientcodec`, reads the **same
`FileDescriptorSet`** produced from the same canonical frame and
`pkg/liquidproto` annotation schemas that drive `protoc-gen-liquidproto`, and emits the client's
tag tables and encode/decode paths. Two properties follow:

- The client and server cannot disagree about field numbers or wire types,
  because both are generated from one descriptor set.
- Adding a field regenerates the client; CI's byte-reproducibility check (FR-7)
  covers the client codec as well as the Go code.

### 10.3 Predicate enforcement is **directional**, and here is exactly how much

| Predicate class | Server (inbound) | Client (inbound) |
|---|---|---|
| `len(this) <= N` on `string`/`bytes` | generated `Validate*` | **enforced** — the decoder already reads a length prefix, so the bound is two extra comparisons; emitted from the descriptor at ~0 marginal bytes |
| `len(this) == N` | generated `Validate*` | **enforced** — same mechanism |
| `this > 0`, numeric ranges | generated `Validate*` | **not enforced** |
| `matches(this, …)` | generated `Validate*` | **not enforced** — an RE2-compatible engine is not in budget |

**What the generated codec does *not* make independent.** instrumentation.md §5.2
uses this codec to decode wire captures "so a bug in the server's framing cannot
hide itself". That independence is real **as to framing** — field numbers, wire
types, and message structure are decoded by code that shares nothing with the
server's encoder. It is only **partial as to field validity**: the codec enforces
length predicates and nothing else, so a `matches` or numeric-range violation in
an outbound frame would pass the audit's decode. §5.3's `ValidateOutbound`
boundary is what actually covers that gap on the server; the audit's own limit is
stated here so no reader infers a stronger check than exists.

The generator emits, alongside the codec, a **manifest**
(`client/predicates.manifest.txt`) listing every predicate in the schema and
whether the client enforces it. The manifest is committed, and a CI check fails
if it drifts from the descriptors. So the asymmetry PRD R-1 warns about is not
an unwritten assumption — it is a generated artifact a reviewer can read.

**Why this is the right v1 line, stated as an argument and not an excuse.**
Downstream frames are produced by our own server and pass the generated
`Validate*` boundary immediately before encoding, so client-side re-checking defends only against a
server bug or version skew — not against an attacker, because the attacker is on
the *other* side, where enforcement is total. Meanwhile the budget it would
consume (an RE2 subset plus a predicate evaluator, est. 600–1,200 B gzip) is
being spent on morph correctness (FR-25/FR-26), which is a user-visible
correctness surface. **Full JS predicate codegen remains PRD BL-26**, and §10.2's
generator is the place it plugs in when BL-26 is scheduled — the descriptor
plumbing will already exist.

**The claim we may therefore make, and the one we may not.** We may say: *every
byte the server accepts crosses a generated Liquid Proto validation boundary before any
application code sees it.* We may **not** say the protocol is "typed end to end
in both runtimes". `docs/` must use the former phrasing.

---

## 11. Open questions

| # | Question | Owner | Needed by |
|---|---|---|---|
| Q-P3 | Whether `Origin.source` should be a bounded interned enum rather than a string, to cut the 20–24 dominant bytes of §9 | QA-2 measurement → FR-43 ADR if it changes | Phase 5 |
| Q-P4 | *Closed.* `pkg/liquidproto` owns the runtime, annotation schema, and `protoc-gen-liquidproto`; generated validators import that runtime directly (§5.4), so no configurable vendored-runtime path is needed | — | done |

*Q-P2 closed in cycle 2 (advisory A-8): H-5 is the single authoritative limit;
the field and list bounds are subordinate defence in depth. See §3.2.*

***Q-P1 closed 2026-08-04 by PM-1** (PRD §9 v0.6 row 1;
[`docs/pm/checkpoint-3-scope.md`](pm/checkpoint-3-scope.md) §1). It asked
whether `Event` needs a fragment-scoped nonce for idempotent replay, and it was
still open two phases past its own "before Phase 2 examples are written" date.
**It does not, and `Event` gains no field.** QA-2's checkpoint-3 chaos suite
§4.8 measured the consequence — one `Event` frame's bytes sent twice moves
`state_version` 2 → 3 and runs the effect twice — and escalated it rather than
filing it, because PRD Phase 3's case 8 asked for "no double state transition"
and this design cannot give that. **PM-1 struck the PRD clause instead of
re-opening this question.** The reasoning is in the PRD row; the part that binds
this schema is that the library never emits a duplicate — `client/runtime.js`
drops an event whose socket is not `OPEN`, with no queue and no resend — so a
second identical frame is always sender-originated, and a nonce would either
collapse two genuine user intents into one or be minted by the attacker it was
supposed to stop. **The obligation this leaves on applications is now a
requirement rather than a remark: PRD FR-77** puts the at-most-once contract,
both double-execution paths, and a money-moving worked example on the docs page
where effects are introduced. If at-least-once is ever wanted, this is still
where the field would go — but it is a v0.2+ protocol change with an ADR, not an
open question.*

---

## 12. Changelog

### Review wave — 2026-08-04, L9-1 [rulings](reviews/rulings-review-wave.md) §5.2 and §5.3

**Two enforcement columns scoped to what the code does.** No implementation
changed in this edit; the client half of H-13 landed separately in `79403c6a`
and this is the table catching up to it, in the same edit that removes a claim
nothing ever backed.

| Site | Change |
|---|---|
| **§6, H-13** | Split into clause **(a)**, the range, and clause **(b)**, the `Origin.kind == RESYNC` iff. (a) is enforced on the outbound boundary **and** in the client's `applied()`, which pins `from === seq + 1` against the sequence the client holds and closes `4002` naming what disagreed; (b) is outbound only. The row previously claimed `validateSnapshot` ran *"on both the outbound boundary and the client decoder"* for the whole invariant, and until `79403c6a` the client read neither field (REV-INV **U-1**). Splitting rather than re-widening is the point: the client deliberately does not import `OriginKind` to check (b), measured at **126 gzipped bytes** for one comparison (`client/SIZE.md` §1.1.3, §1.1.4), and the clauses do different work — the range constrains what the client does next, the kind only labels a frame the outbound boundary already refuses if it is wrong. |
| **§6, H-6** | *"at construction and at parse"* → *"on the outbound boundary (§5.3) and nowhere else"*. `validateOrigin` is reachable only from `ValidateOutbound`, and `Origin` occurs only in `Patch` and `Snapshot`, both server→client, so no parse path exists for it to run on. The invariant was never in doubt; the coverage claim was (REV-INV **U-9**). |
| **§6, notes** | Two paragraphs under the table record both, with the measurement behind H-13's asymmetry, so the next reader finds the argument rather than re-deriving whether the client is missing a check. |

### Checkpoint 2 — 2026-08-04, L9-1 [ruling batch](reviews/checkpoint-2-batch.md) item 2

**Amended in the open by L9-1, in the document rather than in a footnote.** No
implementation changed; three sentences did, and they were wrong together.

| Site | Change |
|---|---|
| **§6, H-6** | `iff kind == CLIENT_EVENT` → `iff kind` is **event-bearing** (`CLIENT_EVENT` **or** `RESYNC`). Cycle 2's B-7 made a resync `Snapshot` carry the identifiers of the `ResyncRequest` that caused it and removed `RESYNC` from §4.2's `event_id = 0` list, but left H-6's sentence behind. `protocol.validateOrigin` has enforced the wider rule since it was written; the reconciliation lived only in a code comment, which means the normative table and the normative implementation disagreed and a reader had to open the Go file to find out which one was binding. The code was right. |
| **§6, note** | New paragraph under the table records the amendment, names *event-bearing* as the vocabulary, and states that adding a third event-bearing kind must move H-6, P2, P6 and `eventBearing` together. |
| **§7, P2** | Restated over the event-bearing kinds rather than `CLIENT_EVENT` alone, naming what each resolves to (`Event` / `ResyncRequest`). Left as it was, the RESYNC arm's `event_id` — the provenance B-7 added the field *to preserve* — would have been the one causal identifier no conformance property checked. The check column now fails a run containing no resync snapshot, rather than passing it vacuously. |
| **§7, P6** | The parenthetical said *"server-initiated patches carry `event_id = 0` by design, §4.2"* and cited §4.2, which says the opposite for exactly one kind. Corrected to distinguish a server-initiated patch no client frame caused from a resync snapshot, and the test column now names the `RESYNC` snapshot as resolving through the first arm. |

`internal/protocol/invariants.go`'s `eventBearing` comment still explains that
H-6's sentence predates §4.2's correction. That sentence is now stale in the
other direction and comes out with **C-22**; the code itself is unchanged and
correct.

### Cycle 2 — 2026-08-04, in response to [L9-1 cycle-1 review](rfc/001-review-cycle-1.md)

**Verdict addressed: RETURN, 5 blocking + 3 advisory.** All five blockers fixed;
all three advisories applied; none declined.

| Objection | Change |
|---|---|
| **B-6** — `protocol_version <= 15` defeats the negotiation it serves | Upper bound **removed**. §3.1 is now `where this > 0`, and a new paragraph states the layering L9-1 asked me to confirm: *schema predicates reject values that are structurally invalid for protocol v1; H-2 rejects structurally valid versions the server cannot serve, with a reason.* There was no reason for 15; L9-1 was right that it was arbitrary, and it would have put the graceful `UNSUPPORTED_VERSION` path out of reach above version 15. |
| **B-7** — P6 false for server-initiated frames; resync has no origin | Three changes. (i) **P6 restated** with PRD G4's disjunction — "either its originating event **or** its named server-effect source" — and its test now exercises both arms. (ii) **`ResyncRequest` mints an `event_id`**; the resulting `Snapshot` carries `Origin{kind: RESYNC, event_id, client_ref}`, and `RESYNC` is removed from §4.2's `event_id = 0` list. (iii) **Schema addition:** `Snapshot.superseded_from_seq` / `superseded_through_seq` (new fields 10, 11) carry the `server_seq` range a resync replaces, with the reasoning in new §4.3 and the cross-field invariant as **H-13**. **P7 is restated as a causal property** rather than a reconciliation category. Range chosen over the event-ID union because the union is unbounded and would collide with H-4 — the range is two varints, exact, and sufficient because P8 guarantees the superseded patches are in the capture. |
| **B-8** — close frame is also not a `Frame` | §1 rewritten around L9-1's amended checklist §3.1 carve-out and its **two obligations**, both discharged explicitly. The audit is now defined **by WebSocket opcode** in a table covering `0x2`/`0x1`/`0x8`/`0x9`/`0xA` and the HTTP upgrade, so "zero non-`Frame` bytes" has a precise scope instead of a literal reading that fails on every clean disconnect. Also records that the library never initiates ping/pong — liveness is the `Heartbeat` frame. |
| **B-9** — outbound frames never cross a `Refine*` boundary | **Accepted without contest** — this was the objection most likely to be argued and I do not argue it. New **§5.3 `ValidateOutbound`**: the framer re-checks every constructed frame through the generated boundary immediately before `proto.Marshal`, on the single write path, not optional, with a failure metric. **P1's justification is restated** in terms of that boundary rather than "unparseable otherwise", which was wrong for server→client frames. Removing it later requires an ADR with a measurement. (Renumbers the old §5.3 to §5.4; four cross-references updated.) |
| **B-10** — audit independence overclaimed | New paragraph in §10.3: the generated codec makes the audit independent **as to framing** and only **partially as to field validity**, because it enforces length predicates and nothing else. Names §5.3 as what actually covers the gap. |
| **A-8** — Q-P2's two limits | Applied and **closed**. `len(Event.fields)` reduced 256 → 64, and §3.2 now names **H-5 as the single authoritative limit** (it is the only one enforced before allocation), with the field and list bounds explicitly subordinate defence-in-depth so nobody removes them as redundant later. |
| **A-9** — why `session_id` rides inbound frames | Applied. §9 records both reasons: one envelope means one schema/codec/boundary/audit path on both sides, and a direction-dependent predicate is **inexpressible** in the grammar (§2, no free variables), so the alternative costs the totality of `len(this) == 16`. |
| **A-10** — `Origin.source` vocabulary | Applied. §3.3 states the prefix vocabulary as a documented convention and resolves the registration gap L9-1 spotted: there **is** no registration step, so cardinality is bounded at the metric (64 distinct values, then `other`, with an overflow counter) rather than at the schema. |

**Also landed here, from the RFC's blockers:** **H-14** carries B-5's
`ResyncRequest` rate budget as a named invariant, and **H-4** now describes the
`contributing_event_ids` bound as a coalescing **flush trigger** rather than a
truncation (B-4). Close codes gained a `code` **metric label column** (A-11) so
protocol.md, RFC §7.4, and instrumentation §2.2 cannot drift.

**Governance incorporated:** **D6** — §1 now cites L9-1's amended checklist §3.1
carve-out and discharges its two obligations, rather than naming a conflict.
