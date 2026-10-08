// Copyright 2026 Candace Labs

package verbs

import (
	"log/slog"
	"os"

	"github.com/candacelabs/csf/csf"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/services/opsview"
)

// workbenchMountName names the Workbench serve mounts on its own listeners,
// where its actions reach the session service in process.
const workbenchMountName = "workbench"

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
