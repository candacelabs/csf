// Copyright 2026 Candace Labs

package verbs

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/io/ipc/docker"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	dispatchservice "github.com/candacelabs/csf/services/dispatch"
	"github.com/candacelabs/csf/services/opsview"
)

// processTable is the kernel's process table, which the host gauges read.
const processTable = "/proc"

// The host operations' descriptions, which are what an agent reads when it
// lists the tools.
const (
	getHostDescription           = "Read the machine: load against cores, memory and disk, and every container with its image, Compose project, state, cpu and memory, grouped as CSF work (the harness's sessions and their build containers) or everything else, with the operator's host profiles, protected containers and the applied profile an undo would reverse."
	controlContainersDescription = "Start, stop, pause or unpause containers by name, or every container of a group (csf or other) the action applies to. Run it with dry_run first: a stop, a pause, or a change to more than one container must confirm by naming exactly the containers the dry run listed, and is refused if the host moved since. It never removes a container or touches a volume, and refuses to stop or pause a container the operator protected."
	applyHostProfileDescription  = "Apply one of the operator's host profiles, such as \"only CSF work\". dry_run shows the diff (what stops, what starts); applying confirms by naming exactly those containers. The applied change set is recorded so undo_host_profile reverses it."
	undoHostProfileDescription   = "Undo the last applied host profile not yet undone: start what it stopped and stop what it started. dry_run shows the diff; applying confirms by naming exactly those containers."
	putHostProfileDescription    = "Add, replace or remove one host profile: the containers it keeps running, whether it keeps CSF work, and whether it stops everything else."
	protectContainersDescription = "Mark containers protected, which no host operation will stop or pause, or clear the mark."
)

// hostOperations grants the host operations the Engine, the process table
// and the state directory, and the dispatcher's pause as the load guard's
// session hold; it returns them with their MCP tools and the Engine client's
// close.
func hostOperations(state string, dispatcher *dispatchservice.DispatchService) (*opsview.HostOperations, []csf.Option, func(), error) {
	engine, err := docker.NewContainerHost()
	if err != nil {
		return nil, nil, nil, err
	}
	closeEngine := func() { _ = engine.Close() }
	processes, err := iofs.NewHostFiles(processTable)
	if err != nil {
		closeEngine()
		return nil, nil, nil, err
	}
	stateFiles, err := iofs.NewHostFiles(state)
	if err != nil {
		closeEngine()
		return nil, nil, nil, err
	}
	operations, err := opsview.NewHostOperations(opsview.WithHostContainers(engine), opsview.WithProcessTable(processes), opsview.WithHostState(stateFiles),
		opsview.WithSessionHold(func(ctx context.Context, reason string) error {
			_, err := dispatcher.Pause(ctx, dispatchservice.DispatcherControlInput{Reason: reason})
			return err
		}))
	if err != nil {
		closeEngine()
		return nil, nil, nil, err
	}
	readOnly, closedWorld := true, false
	return operations, []csf.Option{
		csf.WithMCPTool(mcp.Tool{Name: opsview.GetHostOperation, Description: getHostDescription, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, OpenWorldHint: &closedWorld}}, tool(operations.Host)),
		csf.WithMCPTool(mcp.Tool{Name: opsview.ControlContainersOperation, Description: controlContainersDescription}, tool(operations.Control)),
		csf.WithMCPTool(mcp.Tool{Name: opsview.ApplyHostProfileOperation, Description: applyHostProfileDescription}, tool(operations.ApplyProfile)),
		csf.WithMCPTool(mcp.Tool{Name: opsview.UndoHostProfileOperation, Description: undoHostProfileDescription}, tool(operations.Undo)),
		csf.WithMCPTool(mcp.Tool{Name: opsview.PutHostProfileOperation, Description: putHostProfileDescription}, tool(operations.PutProfile)),
		csf.WithMCPTool(mcp.Tool{Name: opsview.ProtectContainersOperation, Description: protectContainersDescription}, tool(operations.Protect)),
	}, closeEngine, nil
}
