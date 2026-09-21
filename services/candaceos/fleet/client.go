// Package fleet is CandaceOS Core's read-only view of Warden's cluster
// membership.
//
// The package owns polling one Warden status endpoint, normalizing the reply
// into an immutable Snapshot, and projecting configured node labels onto that
// snapshot for placement and the operator UI. Callers may rely on Client being
// safe for concurrent use, on Snapshot returning a defensive copy rather than
// shared state, and on a failed refresh preserving the last observed
// membership while clearing authority and quorum and recording the error —
// never on it reporting an empty fleet.
//
// Leadership, term, and quorum are Warden's observations and are never
// inferred here; node Role and Labels are fixed Core configuration. The
// package performs no writes and makes no placement decision.
package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Node is one member of the cluster as Warden currently observes it. Every
// field is an observation; none of it is Core configuration.
type Node struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	Status   string    `json:"status"`
	Address  string    `json:"address"`
	LastSeen time.Time `json:"last_seen"`
}

// Snapshot is one immutable observation of the cluster. Error is set when the
// most recent refresh failed, in which case the membership is the last good
// one and Authoritative and HasQuorum are cleared.
type Snapshot struct {
	Self          string    `json:"self"`
	LeaderID      string    `json:"leader_id"`
	Term          uint64    `json:"term"`
	Authoritative bool      `json:"authoritative"`
	HasQuorum     bool      `json:"has_quorum"`
	Online        int       `json:"online"`
	Required      int       `json:"required"`
	Nodes         []Node    `json:"nodes"`
	UpdatedAt     time.Time `json:"updated_at"`
	Error         string    `json:"error,omitempty"`
}

// CanMutate reports whether this observation is safe to act on: Warden must be
// authoritative, name a leader, and hold quorum. It is the single gate in
// front of every fleet mutation.
func (s Snapshot) CanMutate() bool {
	return s.Authoritative && s.LeaderID != "" && s.HasQuorum
}

// Client polls one Warden status endpoint and caches the latest observation.
// It is safe for concurrent use.
type Client struct {
	url        string
	httpClient *http.Client

	mu       sync.RWMutex
	snapshot Snapshot
}

// NewWardenClient constructs the fleet observer for one Warden endpoint.
func NewWardenClient(rawURL string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parsing warden URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("warden URL scheme must be http or https")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/status"
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 3 * time.Second}
	}
	return &Client{url: parsed.String(), httpClient: httpClient}, nil
}

// Refresh polls Warden once and stores the result. On failure it returns both
// the recorded error snapshot and the error, so a caller that only wants
// display state can ignore the second value.
func (c *Client) Refresh(ctx context.Context) (Snapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("building warden status request: %w", err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return c.recordError(fmt.Errorf("reading warden status: %w", err)), err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		err = fmt.Errorf("warden status returned %s", response.Status)
		return c.recordError(err), err
	}

	var payload statusResponse
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&payload); err != nil {
		return c.recordError(fmt.Errorf("decoding warden status: %w", err)), err
	}

	snapshot := normalize(payload.View)
	c.mu.Lock()
	c.snapshot = snapshot
	c.mu.Unlock()
	return snapshot, nil
}

// Snapshot returns a copy of the most recent observation without polling.
func (c *Client) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return clone(c.snapshot)
}

// Run refreshes on interval until ctx is done, invoking changed with each new
// observation. Delivery is latest-wins on a single-slot channel, so a slow
// callback drops intermediate snapshots rather than delaying polling.
func (c *Client) Run(ctx context.Context, interval time.Duration, changed func(snapshot Snapshot)) {
	var updates chan Snapshot
	var worker sync.WaitGroup
	if changed != nil {
		updates = make(chan Snapshot, 1)
		worker.Add(1)
		go func() {
			defer worker.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case snapshot := <-updates:
					changed(snapshot)
				}
			}
		}()
	}
	defer worker.Wait()
	refresh := func() {
		snapshot, _ := c.Refresh(ctx)
		publishLatest(updates, snapshot)
	}
	refresh()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

func publishLatest(updates chan Snapshot, snapshot Snapshot) {
	if updates == nil {
		return
	}
	select {
	case updates <- snapshot:
		return
	default:
	}
	select {
	case <-updates:
	default:
	}
	select {
	case updates <- snapshot:
	default:
	}
}

func (c *Client) recordError(err error) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot := clone(c.snapshot)
	snapshot.Authoritative = false
	snapshot.HasQuorum = false
	snapshot.Error = err.Error()
	snapshot.UpdatedAt = time.Now().UTC()
	c.snapshot = snapshot
	return clone(snapshot)
}

type statusResponse struct {
	View clusterView `json:"view"`
}

type clusterView struct {
	Self          string     `json:"self"`
	Role          string     `json:"role"`
	Term          uint64     `json:"term"`
	LeaderID      string     `json:"leader_id"`
	Authoritative bool       `json:"authoritative"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Peers         []peerView `json:"peers"`
	Membership    membership `json:"membership"`
}

type membership struct {
	Version       uint64       `json:"version"`
	CreatedInTerm uint64       `json:"created_in_term"`
	Voters        []wardenNode `json:"voters"`
}

type wardenNode struct {
	ID   string `json:"id"`
	Addr string `json:"addr"`
}

type peerView struct {
	Node     wardenNode `json:"node"`
	Status   string     `json:"status"`
	LastSeen time.Time  `json:"last_seen"`
	Member   string     `json:"member"`
}

func normalize(view clusterView) Snapshot {
	voters := make(map[string]struct{}, len(view.Membership.Voters))
	for _, voter := range view.Membership.Voters {
		voters[voter.ID] = struct{}{}
	}
	required := len(voters)/2 + 1
	online := 0
	nodes := make([]Node, 0, len(view.Peers))
	for _, peer := range view.Peers {
		if _, voter := voters[peer.Node.ID]; voter && peer.Status == "alive" {
			online++
		}
		role := "worker"
		if peer.Node.ID == view.LeaderID {
			role = "leader"
		}
		nodes = append(nodes, Node{
			ID: peer.Node.ID, Name: peer.Node.ID, Role: role,
			Status: peer.Status, Address: peer.Node.Addr, LastSeen: peer.LastSeen,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	return Snapshot{
		Self: view.Self, LeaderID: view.LeaderID, Term: view.Term,
		Authoritative: view.Authoritative, HasQuorum: required > 0 && online >= required,
		Online: online, Required: required, Nodes: nodes, UpdatedAt: view.UpdatedAt,
	}
}

func clone(source Snapshot) Snapshot {
	result := source
	result.Nodes = append([]Node(nil), source.Nodes...)
	return result
}
