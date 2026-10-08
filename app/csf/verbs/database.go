// Copyright 2026 Candace Labs

package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/candacelabs/csf/io/ipc/docker"
	ionet "github.com/candacelabs/csf/io/net"
	"github.com/candacelabs/csf/pkg/atomicfile"
	"github.com/candacelabs/csf/services/database"
)

// The db verbs: CSF's own database, which serve provisions when it is given
// no -database-config and leaves running when it stops, because it holds
// state.
const (
	verbDatabase = "db"
	verbStart    = "start"
	// loopSettingsFile records the mining loop's checkout and corpus once, by
	// csf init or serve's flags, so a later serve mounts the loop with none.
	loopSettingsFile = "ouroboros-settings.json"
	backupUserFormat = "%d:%d"
)

var errUsageDatabase = errors.New("usage: csf db status|start|stop [-state DIR]")

// databaseVerbs runs one db verb against the database the state directory
// owns, through the Docker Engine directly: it needs no running host.
func databaseVerbs(ctx context.Context, arguments []string, output io.Writer) error {
	return ownedServiceVerbs(ctx, verbDatabase, errUsageDatabase, arguments, output,
		func(state string, containers *docker.ContainerHost) (*docker.OwnedService[database.Access], error) {
			return ownedDatabase(state, containers)
		})
}

// ownedServiceVerbs runs status, start or stop against one container a state
// directory owns and prints its status, which never holds its settings. A
// verb for another owned container passes its own constructor.
func ownedServiceVerbs[Settings any](ctx context.Context, name string, usage error, arguments []string, output io.Writer,
	newOwned func(state string, containers *docker.ContainerHost) (*docker.OwnedService[Settings], error)) error {
	if len(arguments) == 0 {
		return usage
	}
	verb := arguments[0]
	flags := flag.NewFlagSet(commandName+" "+name+" "+verb, flag.ContinueOnError)
	stateDirectory := flags.String(stateFlag, "", "state directory that owns the container (default ~/"+defaultStateDirectory+")")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usage
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	containers, err := docker.NewContainerHost()
	if err != nil {
		return err
	}
	defer func() { _ = containers.Close() }()
	owned, err := newOwned(state, containers)
	if err != nil {
		return err
	}
	switch verb {
	case verbStatus:
	case verbStart:
		if _, err := owned.Ensure(ctx); err != nil {
			return err
		}
	case verbStop:
		if err := owned.Stop(ctx); err != nil {
			return err
		}
	default:
		return usage
	}
	status, err := owned.Status(ctx)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(status)
}

// ownedDatabase grants the state directory's database this host's Engine,
// its network for the one port probe, and the user backups are written as.
func ownedDatabase(state string, containers *docker.ContainerHost) (*docker.OwnedService[database.Access], error) {
	return database.NewOwnedDatabase(state, fmt.Sprintf(backupUserFormat, os.Getuid(), os.Getgid()),
		docker.WithServices(containers), docker.WithListener(ionet.NewHostNetwork()))
}

// loopSettings is <state>/ouroboros-settings.json.
type loopSettings struct {
	Repository string `json:"repository"`
	Corpus     string `json:"corpus,omitempty"`
}

// readLoopSettings is the recorded settings, or none.
func readLoopSettings(state string) (loopSettings, error) {
	var settings loopSettings
	content, err := os.ReadFile(filepath.Join(state, loopSettingsFile))
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal(content, &settings); err != nil {
		return settings, fmt.Errorf("decode %s: %w", loopSettingsFile, err)
	}
	return settings, nil
}

// recordLoopSettings records each setting given over the recorded one and
// returns the result.
func recordLoopSettings(state string, update loopSettings) (loopSettings, error) {
	settings, err := readLoopSettings(state)
	if err != nil || update == (loopSettings{}) {
		return settings, err
	}
	if update.Repository != "" {
		settings.Repository = update.Repository
	}
	if update.Corpus != "" {
		settings.Corpus = update.Corpus
	}
	content, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return settings, err
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		return settings, err
	}
	return settings, atomicfile.WriteFile(filepath.Join(state, loopSettingsFile), content, hostFileMode)
}
