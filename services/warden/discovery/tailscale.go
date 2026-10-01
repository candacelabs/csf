package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/core"
	"github.com/candacelabs/csf/services/warden"
)

// Tailscale discovery defaults.
const (
	defaultTSSocket       = "/var/run/tailscale/tailscaled.sock"
	defaultTSPollInterval = 15 * time.Second
	defaultWardenPort     = 7717

	// tsStatusURL is the tailscaled LocalAPI status endpoint. The host is a
	// convention only: the HTTP transport dials the unix socket, so the URL
	// authority is never resolved on the network.
	tsStatusURL  = "http://local-tailscaled.sock/localapi/v0/status"
	tsStatusHost = "local-tailscaled.sock"
)

// TailscaleConfig configures a Tailscale discoverer.
type TailscaleConfig struct {
	// Socket is the tailscaled LocalAPI unix socket path
	// (default /var/run/tailscale/tailscaled.sock).
	Socket string
	// Tag selects peers advertising this ACL tag, e.g. "tag:candacenet".
	Tag string
	// HostPattern is an RE2 pattern matched, anchored to the whole string,
	// against a peer's HostName. Tag and HostPattern are OR'd: when both are
	// set, either match selects the peer.
	HostPattern string
	// Port is the warden port used to compose a peer's Addr from its tailscale
	// IPv4 (default 7717). cmd/main.go derives it from the node's bind port.
	Port int
	// PollInterval is how often tailscaled status is polled (default 15s).
	PollInterval time.Duration
	// IncludeSelf includes the local node (status.Self) in the roster. The
	// local node is, by definition, a warden of this cluster, so when set it is
	// included unconditionally — the Tag/HostPattern filter governs only remote
	// peers. The documented default is true; because a bool cannot distinguish
	// "unset" from an explicit false, cmd/main.go always passes true (there is
	// no config knob to disable it) and tests set it explicitly.
	IncludeSelf bool
}

// Tailscale is a PeerDiscoverer that polls the local tailscaled LocalAPI and
// reports peers selected by ACL tag and/or hostname pattern. Offline peers are
// retained in the roster: membership is a persisted, quorum-relevant fact, and
// liveness is the election manager's job — a rebooting voter must not fall out
// of the roster and shrink the cluster.
type Tailscale struct {
	socket      string
	tag         string
	hostRE      *regexp.Regexp
	port        int
	poll        time.Duration
	includeSelf bool
	client      *http.Client
}

var _ warden.IPeerDiscoverer = (*Tailscale)(nil)

// CompileHostPattern compiles an RE2 host pattern anchored to the whole string.
// services/warden/config validates operator patterns against the identical anchoring;
// keep the two in sync.
func CompileHostPattern(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(`\A(?:` + pattern + `)\z`)
}

// NewTailscale constructs a Tailscale discoverer, applying defaults for unset
// Socket/Port/PollInterval and compiling HostPattern. A malformed HostPattern
// (which config validation should already have rejected) is logged and ignored
// so the process still starts; that peer-selection path simply never matches.
func NewTailscale(cfg TailscaleConfig) *Tailscale {
	socket := cfg.Socket
	if strings.TrimSpace(socket) == "" {
		socket = defaultTSSocket
	}
	port := cfg.Port
	if port <= 0 {
		port = defaultWardenPort
	}
	poll := cfg.PollInterval
	if poll <= 0 {
		poll = defaultTSPollInterval
	}

	var re *regexp.Regexp
	if cfg.HostPattern != "" {
		compiled, err := CompileHostPattern(cfg.HostPattern)
		if err != nil {
			if core.Logger != nil {
				core.Logger.Warn().Err(err).Str("host_pattern", cfg.HostPattern).
					Msg("discovery(tailscale): invalid host_pattern; ignoring it")
			}
		} else {
			re = compiled
		}
	}

	return &Tailscale{
		socket:      socket,
		tag:         cfg.Tag,
		hostRE:      re,
		port:        port,
		poll:        poll,
		includeSelf: cfg.IncludeSelf,
		client:      unixSocketClient(socket),
	}
}

// Discover polls tailscaled and delivers change-only snapshots until ctx ends,
// then closes the channel.
func (t *Tailscale) Discover(ctx context.Context) (<-chan warden.Roster, error) {
	ch := make(chan warden.Roster, 1)
	logErr := func(err error) {
		if core.Logger != nil {
			core.Logger.Warn().Err(err).Str("socket", t.socket).
				Msg("discovery(tailscale): status poll failed; keeping last roster")
		}
	}
	go pollLoop(ctx, ch, t.poll, t.fetch, logErr)
	return ch, nil
}

// fetch queries the LocalAPI status endpoint and maps it to a sorted roster.
func (t *Tailscale) fetch(ctx context.Context) ([]warden.Node, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tsStatusURL, nil)
	if err != nil {
		return nil, fmt.Errorf("discovery(tailscale): building status request: %w", err)
	}
	req.Host = tsStatusHost

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discovery(tailscale): status request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("discovery(tailscale): status HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var status tsStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("discovery(tailscale): decoding status: %w", err)
	}
	return t.selectNodes(&status), nil
}

// selectNodes maps a tailscaled status to the roster: self (when IncludeSelf)
// plus every peer that matches the tag/hostname filter and yields a usable
// warden.Node. The result is de-duplicated by ID and sorted.
func (t *Tailscale) selectNodes(status *tsStatus) []warden.Node {
	out := make([]warden.Node, 0, len(status.Peer)+1)
	if t.includeSelf && status.Self != nil {
		if n, ok := t.nodeFor(status.Self); ok {
			out = append(out, n)
		}
	}
	for _, p := range status.Peer {
		if p == nil || !t.matches(p) {
			continue
		}
		if n, ok := t.nodeFor(p); ok {
			out = append(out, n)
		}
	}
	out = dedupByID(out)
	warden.SortNodes(out)
	return out
}

// matches reports whether a peer satisfies the tag OR hostname-pattern filter.
// A peer matching neither is never selected.
func (t *Tailscale) matches(p *tsPeer) bool {
	if t.tag != "" {
		for _, tag := range p.Tags {
			if tag == t.tag {
				return true
			}
		}
	}
	if t.hostRE != nil && t.hostRE.MatchString(p.HostName) {
		return true
	}
	return false
}

// nodeFor builds a warden.Node from a peer, taking its first IPv4 tailscale
// address and the configured port. A peer with no hostname or no IPv4 address
// cannot form a valid Node and is skipped.
func (t *Tailscale) nodeFor(p *tsPeer) (warden.Node, bool) {
	if p.HostName == "" {
		return warden.Node{}, false
	}
	ip, ok := firstIPv4(p.TailscaleIPs)
	if !ok {
		return warden.Node{}, false
	}
	return warden.Node{
		ID:   warden.NodeID(p.HostName),
		Addr: net.JoinHostPort(ip, strconv.Itoa(t.port)),
	}, true
}

// unixSocketClient returns an HTTP client whose transport dials a unix socket,
// ignoring the request URL's host/port (LocalAPI convention).
func unixSocketClient(socket string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
			MaxIdleConns:    2,
			IdleConnTimeout: 30 * time.Second,
		},
	}
}

// firstIPv4 returns the first parseable IPv4 address in ips.
func firstIPv4(ips []string) (string, bool) {
	for _, s := range ips {
		addr, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil {
			continue
		}
		if addr.Is4() {
			return addr.String(), true
		}
	}
	return "", false
}

// dedupByID keeps the first node seen per ID (self may also surface as a peer
// in unusual configurations). It filters in place.
func dedupByID(nodes []warden.Node) []warden.Node {
	seen := make(map[warden.NodeID]bool, len(nodes))
	out := nodes[:0]
	for _, n := range nodes {
		if seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		out = append(out, n)
	}
	return out
}

// tsStatus is the subset of tailscaled's LocalAPI /localapi/v0/status response
// that discovery needs.
type tsStatus struct {
	Self *tsPeer            `json:"Self"`
	Peer map[string]*tsPeer `json:"Peer"`
}

// tsPeer is the subset of a tailscaled peer status entry discovery reads. The
// Online field is decoded for completeness but intentionally NOT used to filter:
// offline peers stay in the roster (see the Tailscale type doc).
type tsPeer struct {
	HostName     string   `json:"HostName"`
	DNSName      string   `json:"DNSName"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Tags         []string `json:"Tags"`
	Online       bool     `json:"Online"`
}
