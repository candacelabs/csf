// Copyright 2026 Candace Labs

// Package endpoint is the endpoint registry: the typed record, in the harness
// state directory, of every operator-facing endpoint csf serve has served (the
// Workbench, the API and MCP), each with its owner, its users and every
// address it has been served at, and of every address the operator retired,
// with the address it moved to, the day and the operator's acknowledgement.
//
// An address once registered stays registered. csf serve refuses to start
// without serving every registered address that has no retirement record,
// either by listening on it or by a redirect alias it serves itself, and the
// session gate refuses a command that would stop serving one. So an address
// the operator holds a link to cannot disappear without the operator's word.
//
// It is a pure library: the binary reads and writes the registry file.
package endpoint

import (
	"encoding/json"
	"errors"
	"fmt"
	stdfs "io/fs"
	"net"
	"net/http"
	"slices"
	"strings"
)

// Where the registry lives under the harness state directory: a directory of
// its own, so a session container can be granted it read-only and still see
// each atomic replacement of the file.
const (
	Directory    = "endpoints"
	RegistryFile = "registry.json"
)

// The endpoints csf serve serves on every address it listens on, their paths
// and their users.
const (
	NameWorkbench = "Workbench"
	NameAPI       = "API"
	NameMCP       = "MCP"

	PathWorkbench = "/"
	PathAPI       = "/api/"
	PathMCP       = "/mcp"

	UsersWorkbench = "the operator, in a browser"
	UsersAPI       = "the csf command line and the operator's scripts"
	UsersMCP       = "agent sessions and the orchestrator, as their csf MCP server"

	// OwnerServe is the owner of every endpoint csf serve serves.
	OwnerServe = "csf serve"
)

// How an address is served.
const (
	// ViaListen is an address csf serve listens on and answers itself.
	ViaListen = "listen"
	// ViaRedirect is an alias address csf serve listens on only to redirect
	// every request to the same path on its primary port.
	ViaRedirect = "redirect"

	scheme = "http://"
)

var (
	// ErrNotRegistered reports a retirement of an address no endpoint was
	// ever served at.
	ErrNotRegistered = errors.New("endpoint registry: no endpoint is registered at this address")
	// ErrAlreadyRetired reports a second retirement of one address.
	ErrAlreadyRetired = errors.New("endpoint registry: the address is already retired")
	// ErrIncompleteRetirement reports a retirement without the address it
	// moved to, its day or the operator's acknowledgement.
	ErrIncompleteRetirement = errors.New("endpoint registry: a retirement needs the address it moved to, the day and the operator's acknowledgement")
)

// Address is one address an endpoint has been served at.
type Address struct {
	// Address is host:port, as csf serve's -listen spells it.
	Address string `json:"address"`
	// Via is how the address was served when it was last registered.
	Via string `json:"via"`
	// RedirectsTo is the port a redirect alias sends its requests to.
	RedirectsTo string `json:"redirects_to,omitempty"`
	// RegisteredOn is the UTC day the address was first served.
	RegisteredOn string `json:"registered_on"`
}

// Endpoint is one operator-facing endpoint.
type Endpoint struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Owner string `json:"owner"`
	// Container is the container that serves the endpoint, when one does; the
	// session gate refuses stopping or removing it.
	Container string    `json:"container,omitempty"`
	Users     string    `json:"users"`
	Addresses []Address `json:"addresses"`
}

// URL is the endpoint's address on one of its addresses.
func (endpoint Endpoint) URL(address string) string { return scheme + address + endpoint.Path }

// Describe names the endpoint at its addresses and its users, as a refusal
// states them.
func (endpoint Endpoint) Describe() string {
	urls := make([]string, 0, len(endpoint.Addresses))
	for _, address := range endpoint.Addresses {
		urls = append(urls, endpoint.URL(address.Address))
	}
	return fmt.Sprintf("%s at %s (used by %s)", endpoint.Name, strings.Join(urls, " and "), endpoint.Users)
}

// Retirement is the operator's record that an address is no longer served.
type Retirement struct {
	Address string `json:"address"`
	// MovedTo is where the endpoints served there are now.
	MovedTo   string `json:"moved_to"`
	RetiredOn string `json:"retired_on"`
	// Acknowledgement is the operator's own words agreeing to the retirement.
	Acknowledgement string `json:"acknowledgement"`
}

// Registry is the whole registry file.
type Registry struct {
	// HostPID is the process of the csf serve that last registered, so the
	// session gate recognises a kill of it.
	HostPID     int          `json:"host_pid,omitempty"`
	Endpoints   []Endpoint   `json:"endpoints"`
	Retirements []Retirement `json:"retirements,omitempty"`
}

// Unserved is one registered address that is neither served nor retired.
type Unserved struct {
	Endpoint Endpoint
	Address  string
}

// String names the endpoint, the address and its users.
func (unserved Unserved) String() string {
	return fmt.Sprintf("%s at %s (used by %s)", unserved.Endpoint.Name, unserved.Endpoint.URL(unserved.Address), unserved.Endpoint.Users)
}

// ServeEndpoints is what one csf serve serves: the Workbench, the API and MCP
// on every listen address, and the same three, as redirects to the first
// listen address's port, on every alias address.
func ServeEndpoints(listens []string, redirects []string) []Endpoint {
	served := make([]Address, 0, len(listens)+len(redirects))
	for _, address := range listens {
		served = append(served, Address{Address: address, Via: ViaListen})
	}
	if len(listens) > 0 {
		_, port, _ := net.SplitHostPort(listens[0])
		for _, address := range redirects {
			served = append(served, Address{Address: address, Via: ViaRedirect, RedirectsTo: port})
		}
	}
	endpoints := []Endpoint{
		{Name: NameWorkbench, Path: PathWorkbench, Users: UsersWorkbench},
		{Name: NameAPI, Path: PathAPI, Users: UsersAPI},
		{Name: NameMCP, Path: PathMCP, Users: UsersMCP},
	}
	for index := range endpoints {
		endpoints[index].Owner = OwnerServe
		endpoints[index].Addresses = slices.Clone(served)
	}
	return endpoints
}

// Retired reports whether the operator retired address.
func (registry Registry) Retired(address string) bool {
	return slices.ContainsFunc(registry.Retirements, func(retirement Retirement) bool { return retirement.Address == address })
}

// Active is every endpoint with its unretired addresses; an endpoint whose
// every address is retired is left out.
func (registry Registry) Active() []Endpoint {
	var active []Endpoint
	for _, endpoint := range registry.Endpoints {
		endpoint.Addresses = slices.DeleteFunc(slices.Clone(endpoint.Addresses), func(address Address) bool { return registry.Retired(address.Address) })
		if len(endpoint.Addresses) > 0 {
			active = append(active, endpoint)
		}
	}
	return active
}

// Unserved is every registered, unretired address of an endpoint that served
// does not serve, in registry order: what a csf serve given served would
// silently retire.
func (registry Registry) Unserved(served []Endpoint) []Unserved {
	var missing []Unserved
	for _, endpoint := range registry.Active() {
		current := find(served, endpoint.Name)
		for _, address := range endpoint.Addresses {
			if current < 0 || !slices.ContainsFunc(served[current].Addresses, sameAddress(address.Address)) {
				missing = append(missing, Unserved{Endpoint: endpoint, Address: address.Address})
			}
		}
	}
	return missing
}

// Register records served on day: a new endpoint or address is added, and a
// known one takes its current owner, users and the way it is served now.
// Nothing is ever removed; only a retirement ends an address.
func (registry Registry) Register(served []Endpoint, day string) Registry {
	next := Registry{HostPID: registry.HostPID, Retirements: slices.Clone(registry.Retirements)}
	for _, endpoint := range registry.Endpoints {
		endpoint.Addresses = slices.Clone(endpoint.Addresses)
		next.Endpoints = append(next.Endpoints, endpoint)
	}
	for _, endpoint := range served {
		index := find(next.Endpoints, endpoint.Name)
		if index < 0 {
			next.Endpoints = append(next.Endpoints, Endpoint{Name: endpoint.Name})
			index = len(next.Endpoints) - 1
		}
		known := &next.Endpoints[index]
		known.Path, known.Owner, known.Container, known.Users = endpoint.Path, endpoint.Owner, endpoint.Container, endpoint.Users
		for _, address := range endpoint.Addresses {
			position := slices.IndexFunc(known.Addresses, sameAddress(address.Address))
			if position < 0 {
				address.RegisteredOn = day
				known.Addresses = append(known.Addresses, address)
				continue
			}
			known.Addresses[position].Via, known.Addresses[position].RedirectsTo = address.Via, address.RedirectsTo
		}
	}
	return next
}

// Retire records the operator's retirement of one registered address.
func (registry Registry) Retire(retirement Retirement) (Registry, error) {
	if retirement.MovedTo == "" || retirement.RetiredOn == "" || strings.TrimSpace(retirement.Acknowledgement) == "" {
		return registry, ErrIncompleteRetirement
	}
	if registry.Retired(retirement.Address) {
		return registry, fmt.Errorf("%w: %s", ErrAlreadyRetired, retirement.Address)
	}
	registered := slices.ContainsFunc(registry.Endpoints, func(endpoint Endpoint) bool {
		return slices.ContainsFunc(endpoint.Addresses, sameAddress(retirement.Address))
	})
	if !registered {
		return registry, fmt.Errorf("%w: %s", ErrNotRegistered, retirement.Address)
	}
	registry.Retirements = append(slices.Clone(registry.Retirements), retirement)
	return registry, nil
}

// Decode reads a registry file's content.
func Decode(content []byte) (Registry, error) {
	var registry Registry
	if err := json.Unmarshal(content, &registry); err != nil {
		return Registry{}, fmt.Errorf("endpoint registry: decode %s: %w", RegistryFile, err)
	}
	return registry, nil
}

// Encode is the registry file's content.
func (registry Registry) Encode() ([]byte, error) {
	content, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

// ReadRegistry reads the registry from its directory; a registry never
// written is empty.
func ReadRegistry(directory stdfs.FS) (Registry, error) {
	content, err := stdfs.ReadFile(directory, RegistryFile)
	if errors.Is(err, stdfs.ErrNotExist) {
		return Registry{}, nil
	}
	if err != nil {
		return Registry{}, fmt.Errorf("endpoint registry: read %s: %w", RegistryFile, err)
	}
	return Decode(content)
}

// AliasRedirect answers every request on an alias address with a permanent
// redirect to the same host name, path and query on the primary port, so a
// link the operator holds to the alias lands where the endpoints are served
// now, on whichever of the host's names the operator used.
type AliasRedirect struct {
	port string
}

// NewAliasRedirect redirects to port.
func NewAliasRedirect(port string) *AliasRedirect { return &AliasRedirect{port: port} }

// ServeHTTP redirects one request.
func (redirect *AliasRedirect) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	host := request.Host
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	http.Redirect(writer, request, scheme+net.JoinHostPort(host, redirect.port)+request.URL.RequestURI(), http.StatusPermanentRedirect)
}

func find(endpoints []Endpoint, name string) int {
	return slices.IndexFunc(endpoints, func(endpoint Endpoint) bool { return endpoint.Name == name })
}

func sameAddress(address string) func(candidate Address) bool {
	return func(candidate Address) bool { return candidate.Address == address }
}
