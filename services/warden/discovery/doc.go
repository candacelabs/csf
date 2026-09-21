// Package discovery implements warden.IPeerDiscoverer: the sources that report
// which nodes are candidate members of the cluster. Discovery is advisory. A
// Discover channel delivers roster snapshots; the election manager verifies
// each candidate with an identify handshake and only the leader turns stable,
// verified candidates into one-at-a-time voting-membership changes. Quorum is
// always computed over the persisted voter set, never over a roster, so a
// discovery source that goes quiet or unavailable can never shrink the quorum
// denominator.
//
// Three sources are provided:
//
//   - Static  — reports one fixed roster (the configured peer seed) once and
//     then stays quiet. This is the explicit "static" mode and the deterministic
//     stand-in used by tests.
//   - Tailscale — polls the local tailscaled LocalAPI over its unix socket and
//     reports peers selected by ACL tag and/or a hostname pattern.
//   - File — polls a JSON roster file (warden.Roster's shape). Doubles as a
//     manual dynamic mode: an operator edits the file and warden picks it up.
//
// Contract shared by the polling sources (Tailscale, File):
//
//   - The first successful snapshot is always sent; afterwards a snapshot is
//     sent only when the node set actually changes (change-only delivery).
//   - On any read/parse error the source logs a rate-limited warning (once per
//     distinct error transition) and sends NOTHING. Silence means consumers keep
//     the last roster and the persisted membership — a source never emits an
//     empty or partial roster on error. (A syntactically valid but empty roster,
//     e.g. a file with "nodes": [], is a real empty roster and is sent.)
//   - Rosters are sorted by node ID.
//   - The channel is closed when the context ends.
//
// The package is IO-bound by nature and uses the real clock (short poll
// intervals); it deliberately does not depend on services/warden/testclock.
package discovery
