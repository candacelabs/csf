// Copyright 2026 Candace Labs

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	ipcfs "github.com/candacelabs/csf/ipc/fs"
	ipcnet "github.com/candacelabs/csf/ipc/net"
	ipchttp "github.com/candacelabs/csf/ipc/net/http"
	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/opsview"
)

// The ops view host: a second long-running process, separate from serve so
// the view starts, stops and restarts without touching running sessions. It
// reads the same state directory serve writes.
const (
	verbView          = "view"
	stopFlag          = "stop"
	defaultViewListen = "127.0.0.1:14121"
	// ViewRecordFile is where view records its endpoint and pid, for -stop
	// and for the operator.
	ViewRecordFile = "view.json"
	viewLogFile    = "view.log"
	viewService    = "ops-view"
	viewHostName   = "harness-view"
	viewMountName  = "ops view"
)

var (
	errNoView       = errors.New("no ops view is recorded as running; start one with harness view")
	errViewConflict = errors.New("give -detach or -stop, not both")
)

// view is the ops view host app: it grants the file and watch capabilities
// over the state directory, mounts the view and one HTTP listener per
// address into a host runtime, and records where it listens.
func view(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer, diagnostics io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbView, flag.ContinueOnError)
	var listens, origins listFlag
	flags.Var(&listens, listenFlag, "address to serve on; repeat for several (default "+defaultViewListen+")")
	flags.Var(&origins, originFlag, "extra browser Origin to accept; each listen address is accepted already")
	stateDirectory := flags.String(stateFlag, "", "directory holding each run as <assignment id>/ (default ~/"+defaultStateDirectory+")")
	detach := flags.Bool(detachFlag, false, "start the view in the background, logging to <state>/"+viewLogFile+", and return")
	stop := flags.Bool(stopFlag, false, "stop the view recorded in <state>/"+ViewRecordFile)
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errUsage
	}
	if *detach && *stop {
		return errViewConflict
	}
	if len(listens) == 0 {
		listens = listFlag{defaultViewListen}
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	if *stop {
		return stopView(state, output)
	}
	if *detach {
		return detachHost(launcher, state, verbView, viewLogFile, arguments, output)
	}
	logger := slog.New(slog.NewJSONHandler(diagnostics, nil))
	files, err := ipcfs.NewHostFiles(state)
	if err != nil {
		return err
	}
	watcher, err := ipcfs.NewHostWatcher(state)
	if err != nil {
		return err
	}
	for _, address := range listens {
		origins = append(origins, schemeHTTP+address)
	}
	page, err := opsview.NewOpsView(files, watcher, origins, logger)
	if err != nil {
		return err
	}
	engine := httpserver.NewEngine(viewService, httpserver.WithRequestLogging())
	page.Register(engine)

	host, err := runtime.NewHostRuntime(runtime.WithHostName(viewHostName), runtime.WithLogger(logger))
	if err != nil {
		return err
	}
	if err := host.Mount(viewMountName, page); err != nil {
		return err
	}
	network := ipcnet.NewHostNetwork()
	for _, address := range listens {
		listener, err := ipchttp.NewHTTPListener(network, address, engine)
		if err != nil {
			return err
		}
		if err := host.Mount("http "+address, listener); err != nil {
			return err
		}
	}
	// Mounted last, so it runs once every listener is bound and is removed
	// first on shutdown: the record is true exactly while the view answers.
	record := hostRecord{PID: os.Getpid(), Endpoint: schemeHTTP + listens[0], Listen: listens, StartedAt: time.Now().UTC()}
	if err := host.Mount("record", runtime.ServiceFunc(func(scope *runtime.Scope) error {
		if err := writeHostRecord(state, ViewRecordFile, record); err != nil {
			return err
		}
		fmt.Fprintf(output, "%s %s: pid %d serving %s on %s\n", commandName, verbView, record.PID, opsview.PageURL(record.Endpoint), strings.Join(listens, " "))
		return scope.GoOwner("record", func(ctx context.Context) error {
			<-ctx.Done()
			return os.Remove(filepath.Join(state, ViewRecordFile))
		})
	})); err != nil {
		return err
	}
	return host.Run(ctx)
}

// stopView asks the recorded view process to stop, the way an interrupt
// would; the process removes its own record as it shuts down.
func stopView(state string, output io.Writer) error {
	content, err := os.ReadFile(filepath.Join(state, ViewRecordFile))
	if errors.Is(err, os.ErrNotExist) {
		return errNoView
	}
	if err != nil {
		return err
	}
	var record hostRecord
	if err := json.Unmarshal(content, &record); err != nil {
		return fmt.Errorf("decode %s: %w", ViewRecordFile, err)
	}
	process, err := os.FindProcess(record.PID)
	if err != nil {
		return err
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop the ops view (pid %d): %w", record.PID, err)
	}
	fmt.Fprintf(output, "%s %s: asked pid %d to stop\n", commandName, verbView, record.PID)
	return nil
}
