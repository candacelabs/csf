package dashboard

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/candacelabs/csf/services/warden"
)

// warden.css pill and badge color classes. helpers.go maps domain enums to
// these class names; the class rules themselves live in the embedded
// assets/warden.css, and the dashboard specs pin the rendered class strings as
// literal goldens. Keeping the names here in one place means a color rename is
// a single edit against the stylesheet.
const (
	pillGreen   = "pill-green"
	pillAmber   = "pill-amber"
	pillRed     = "pill-red"
	pillSlate   = "pill-slate"
	pillSky     = "pill-sky"
	pillOutline = "pill-outline"

	badgeGreen = "badge-green"
	badgeAmber = "badge-amber"
	badgeSlate = "badge-slate"
	badgeRed   = "badge-red"
)

// Presentation helpers. Every function here is a pure function of its inputs
// (no clock reads, no shared state), which keeps them trivially unit-testable
// and safe to expose to templates. Callers pass an explicit "now" for age
// calculations so rendering stays deterministic.

// humanizeAge renders the elapsed time between now and t as a compact,
// human-friendly age such as "3s ago", "5m ago", "2h ago" or "4d ago". A
// zero t (never observed) renders as an em dash. Negative durations (clock
// skew) are clamped to zero.
func humanizeAge(now, t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}

// formatLatency renders a heartbeat round-trip time in milliseconds to one
// decimal place (e.g. "1.2 ms"). An unknown/zero latency renders as an em
// dash.
func formatLatency(ms float64) string {
	if ms <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f ms", ms)
}

// statusPillClass maps a peer liveness status to its warden.css pill color
// class: alive=green, suspect=amber, dead=red, unknown=slate. The classes are
// defined in the embedded assets/warden.css.
func statusPillClass(s warden.PeerStatus) string {
	switch s {
	case warden.StatusAlive:
		return pillGreen
	case warden.StatusSuspect:
		return pillAmber
	case warden.StatusDead:
		return pillRed
	default: // StatusUnknown and any unexpected value
		return pillSlate
	}
}

// roleBadgeClass maps an election role to its warden.css badge color class:
// leader=green, candidate=amber, follower=slate.
func roleBadgeClass(r warden.Role) string {
	switch r {
	case warden.RoleLeader:
		return badgeGreen
	case warden.RoleCandidate:
		return badgeAmber
	default: // RoleFollower and any unexpected value
		return badgeSlate
	}
}

// incidentBadgeClass maps an incident type to its warden.css badge color
// class: recovered=green, everything else (peer dead) = red.
func incidentBadgeClass(t warden.IncidentType) string {
	switch t {
	case warden.IncidentPeerRecovered:
		return badgeGreen
	default: // IncidentPeerDead and any unexpected value
		return badgeRed
	}
}

// incidentBadgeLabel returns the short uppercase label shown inside an
// incident's type badge.
func incidentBadgeLabel(t warden.IncidentType) string {
	switch t {
	case warden.IncidentPeerRecovered:
		return "RECOVERED"
	case warden.IncidentPeerDead:
		return "DEAD"
	default:
		return strings.ToUpper(string(t))
	}
}

// memberPillClass maps a node's membership kind to its warden.css pill color
// class: voter=slate (neutral), observer=sky (awaiting admission), discovered=
// outline (advisory/unverified). An empty Member is treated as a voter for
// backward compatibility with pre-membership views.
func memberPillClass(m warden.MemberKind) string {
	switch m {
	case warden.MemberObserver:
		return pillSky
	case warden.MemberDiscovered:
		return pillOutline
	default: // MemberVoter, "" (back-compat), and any unexpected value
		return pillSlate
	}
}

// memberLabel returns the short uppercase label shown inside a node's
// membership badge. An empty Member renders as VOTER (back-compat).
func memberLabel(m warden.MemberKind) string {
	if m == "" {
		return "VOTER"
	}
	return strings.ToUpper(string(m))
}

// membershipSummary renders the effective voting configuration for the summary
// strip: "v<Version> · <N> voters". A view with no voters (static or
// pre-membership) renders as "static".
func membershipSummary(m warden.Membership) string {
	if len(m.Voters) == 0 {
		return "static"
	}
	return fmt.Sprintf("v%d · %d voters", m.Version, len(m.Voters))
}

// funcMap builds the template function map. upper accepts any value so the
// caller can pass defined string types (warden.Role, warden.PeerStatus, ...)
// without an explicit conversion, which html/template's reflection-based call
// path would otherwise reject.
func funcMap() template.FuncMap {
	return template.FuncMap{
		"age":                humanizeAge,
		"latency":            formatLatency,
		"statusPillClass":    statusPillClass,
		"roleBadgeClass":     roleBadgeClass,
		"incidentBadgeClass": incidentBadgeClass,
		"incidentLabel":      incidentBadgeLabel,
		"memberPillClass":    memberPillClass,
		"memberLabel":        memberLabel,
		"membershipSummary":  membershipSummary,
		"upper":              func(v any) string { return strings.ToUpper(fmt.Sprint(v)) },
	}
}
