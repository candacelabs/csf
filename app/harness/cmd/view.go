// Copyright 2026 Candace Labs

package main

import (
	"errors"
	"log/slog"
	"os"

	"github.com/candacelabs/csf/csf"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/services/opsview"
)

// The view verb ran the ops view as a second process. The page is now the
// Workbench and serve mounts it on its own listeners, where its actions reach
// the session service in process; the verb says so rather than starting a
// second, read-only copy.
const (
	verbView           = "view"
	workbenchMountName = "workbench"
)

var errViewRetired = errors.New("csf view is retired: csf serve serves the Workbench at / on its own listen addresses")

// newWorkbench grants the Workbench the file and watch capabilities over the
// state directory, the CSF service's operations, the recipe templates when
// serve was given a directory of them, and extra, such as the installed
// widget definitions.
func newWorkbench(state string, recipes string, origins []string, logger *slog.Logger, operations *csf.Service, extra ...opsview.Option) (*opsview.OpsView, error) {
	if err := os.MkdirAll(state, 0o700); err != nil {
		return nil, err
	}
	files, err := iofs.NewHostFiles(state)
	if err != nil {
		return nil, err
	}
	watcher, err := iofs.NewHostWatcher(state)
	if err != nil {
		return nil, err
	}
	options := append([]opsview.Option{opsview.WithOperations(operations)}, extra...)
	if recipes != "" {
		templates, err := iofs.NewHostFiles(recipes)
		if err != nil {
			return nil, err
		}
		options = append(options, opsview.WithRecipeTemplates(templates))
	}
	return opsview.NewOpsView(files, watcher, origins, logger, options...)
}
