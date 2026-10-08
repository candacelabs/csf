// Copyright 2026 Candace Labs

package verbs

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/atomicfile"
	"github.com/candacelabs/csf/services/harness/endpoint"
	"github.com/candacelabs/csf/services/harness/session"
)

// The endpoint verb holds the operator's retirement of an address in the
// endpoint registry; serve's -redirect serves an old address as an alias.
const (
	verbEndpoint        = "endpoint"
	endpointRetire      = "retire"
	redirectFlag        = "redirect"
	addressFlag         = "address"
	movedToFlag         = "moved-to"
	acknowledgementFlag = "acknowledgement"
	registryDirMode     = 0o700
)

var (
	errEndpointUsage = errors.New("usage: csf endpoint retire -address HOST:PORT -moved-to HOST:PORT -acknowledgement TEXT [-state DIR]")
	// errUnserved reports a serve that would silently retire a registered
	// address.
	errUnserved = errors.New("csf serve does not serve every registered endpoint")
)

// readEndpointRegistry reads the endpoint registry under the state directory;
// one never written is empty.
func readEndpointRegistry(state string) (endpoint.Registry, error) {
	return endpoint.ReadRegistry(os.DirFS(filepath.Join(state, endpoint.Directory)))
}

// writeEndpointRegistry replaces the registry atomically, so neither a gate
// nor the Workbench reads a half-written one.
func writeEndpointRegistry(state string, registry endpoint.Registry) error {
	directory := filepath.Join(state, endpoint.Directory)
	if err := os.MkdirAll(directory, registryDirMode); err != nil {
		return err
	}
	content, err := registry.Encode()
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(directory, endpoint.RegistryFile), content, hostFileMode)
}

// checkServed refuses a serve that would leave a registered, unretired
// address unserved, naming each endpoint there and its users.
func checkServed(registry endpoint.Registry, served []endpoint.Endpoint) error {
	unserved := registry.Unserved(served)
	if len(unserved) == 0 {
		return nil
	}
	lines := make([]string, 0, len(unserved))
	for _, missing := range unserved {
		lines = append(lines, "  "+missing.String())
	}
	return fmt.Errorf("%w; it would retire, without notice:\n%s\nserve each address with -%s or -%s, or the operator records its retirement with csf %s %s",
		errUnserved, strings.Join(lines, "\n"), listenFlag, redirectFlag, verbEndpoint, endpointRetire)
}

// today is the UTC day a registration or retirement is recorded on.
func today() string { return time.Now().UTC().Format(time.DateOnly) }

// endpointVerbs is csf endpoint retire: the operator records that an address
// is no longer served, where its endpoints moved and their own words.
func endpointVerbs(arguments []string, output io.Writer) error {
	if len(arguments) == 0 || arguments[0] != endpointRetire {
		return errEndpointUsage
	}
	flags := flag.NewFlagSet(commandName+" "+verbEndpoint+" "+endpointRetire, flag.ContinueOnError)
	address := flags.String(addressFlag, "", "the registered address, host:port, that is no longer served")
	movedTo := flags.String(movedToFlag, "", "the address its endpoints are served at now")
	acknowledgement := flags.String(acknowledgementFlag, "", "the operator's own words agreeing to the retirement")
	stateDirectory := flags.String(stateFlag, "", "state directory (default ~/"+defaultStateDirectory+")")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *address == "" {
		return errEndpointUsage
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	registry, err := readEndpointRegistry(state)
	if err != nil {
		return err
	}
	retirement := endpoint.Retirement{Address: *address, MovedTo: *movedTo, RetiredOn: today(), Acknowledgement: *acknowledgement}
	if registry, err = registry.Retire(retirement); err != nil {
		return err
	}
	if err := writeEndpointRegistry(state, registry); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "%s: %s retired on %s, moved to %s\n", commandName, retirement.Address, retirement.RetiredOn, retirement.MovedTo)
	return err
}

// gateRegistry is the registry a gate call protects: the one beside the run
// directory, or, in operator mode, under the directory given.
func gateRegistry(directory string) (endpoint.Registry, error) {
	state := directory
	if _, err := os.Stat(filepath.Join(directory, session.RunStateFile)); err == nil {
		state = filepath.Dir(directory)
	}
	return readEndpointRegistry(state)
}
