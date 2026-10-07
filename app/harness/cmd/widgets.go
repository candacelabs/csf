// Copyright 2026 Candace Labs

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/candacelabs/csf/csf"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/net/github"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/opsview"
)

// widgetsFlag names the widgets directory of the checkout serve runs from,
// which the widget operations read and the Workbench installs; its default
// is the working directory's.
const widgetsFlag = "widgets"

// The three widget operations' descriptions, which are what an agent reads
// when it lists the tools.
const (
	listWidgetsDescription = "List the Workbench's widget definitions (widgets/<name>/ in the checkout it runs from) and the result of the install check on each, with every reason a refused one was refused, and the sources a widget may bind."
	checkWidgetDescription = "Run the Workbench's install check on one widget definition: the dialect parses, its refinements hold, every field the binding reads exists in its source's schema, and every fixture renders. Name assignment_id to check widgets/<name> in that harness session's worktree."
	proposeDescription     = "Propose a widget definition from a harness session's worktree: run the install check and, when it is clean, commit widgets/<name> there and publish the session's branch through the harness, which opens its pull request. Once the pull request merges the running Workbench shows the widget, with no restart."
)

// widgetsDirectory resolves -widgets: the directory given, or widgets under
// the working directory; ok is false when it does not exist, and then no
// widget is installed and no widget operation is served.
func widgetsDirectory(flagValue string) (directory string, ok bool, err error) {
	if flagValue == "" {
		flagValue = opsview.WidgetsDirectory
	}
	directory, err = filepath.Abs(flagValue)
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return directory, false, nil
	}
	if err != nil {
		return "", false, err
	}
	return directory, info.IsDir(), nil
}

// widgetOperations grants the widget operations the widgets directory, the
// state directory whose run directories hold each session's worktree, and the
// launcher, and returns them with the MCP tools that serve them.
func widgetOperations(directory string, state string, launcher proc.ILauncher, client *github.GitHubClient) (*opsview.WidgetOperations, []csf.Option, error) {
	definitions, err := iofs.NewHostFiles(directory)
	if err != nil {
		return nil, nil, err
	}
	stateFiles, err := iofs.NewHostFiles(state)
	if err != nil {
		return nil, nil, err
	}
	operations, err := opsview.NewWidgetOperations(opsview.WithInstalledDefinitions(definitions), opsview.WithSessionWorktrees(stateFiles, launcher), opsview.WithGitHubClient(client))
	if err != nil {
		return nil, nil, err
	}
	readOnly, closedWorld := true, false
	return operations, []csf.Option{
		csf.WithMCPTool(mcp.Tool{Name: opsview.ListWidgetsOperation, Description: listWidgetsDescription, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, OpenWorldHint: &closedWorld}}, tool(operations.List)),
		csf.WithMCPTool(mcp.Tool{Name: opsview.CheckWidgetOperation, Description: checkWidgetDescription, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, OpenWorldHint: &closedWorld}}, tool(operations.Check)),
		csf.WithMCPTool(mcp.Tool{Name: opsview.ProposeWidgetOperation, Description: proposeDescription}, tool(operations.Propose)),
	}, nil
}

// tool adapts one typed operation to an MCP tool handler; the SDK derives
// both schemas from In and Out.
func tool[In any, Out any](operation func(ctx context.Context, input In) (Out, error)) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		output, err := operation(ctx, input)
		return nil, output, err
	}
}

// installedWidgets grants the ops view the widgets directory and its watch.
func installedWidgets(directory string) (opsview.Option, error) {
	files, err := iofs.NewHostFiles(directory)
	if err != nil {
		return nil, err
	}
	watcher, err := iofs.NewHostWatcher(directory)
	if err != nil {
		return nil, err
	}
	return opsview.WithWidgetDefinitions(files, watcher), nil
}
