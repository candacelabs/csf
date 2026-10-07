// Copyright 2026 Candace Labs

package opsview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	stdfs "io/fs"
	"maps"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/a-h/templ"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/services/ouroboros"
)

// Installed widgets: every definition directory in the widgets directory of
// the checkout the Workbench runs from, checked and drawn the moment it lands
// and gone the moment it is removed, with no restart. A definition is data
// (pkg/widget's definition files), so installing one loads no code.
const (
	// WidgetsRegion is the installed widgets' panel.
	WidgetsRegion = "opsview.widgets"
	// EventWidgets carries every definition's files, as JSON in FieldWidgets.
	// It is internal: the widgets effect emits it and a browser may not, since
	// a browser posting one would be installing a widget.
	EventWidgets = "opsview.widgets"
	FieldWidgets = "widgets"

	sourceWidgets   = "opsview.widgets"
	widgetsTemplate = "widgets"
)

// ErrNoDefinitionWatcher reports widget definitions granted without their
// watch capability.
var ErrNoDefinitionWatcher = errors.New("ops view: widget definitions need both the file and the watch capability over the widgets directory")

// WidgetSources are the sources an installed widget can bind, by the name its
// stream declares: the mining loop's snapshot, whose schema is the loop's own
// Snapshot type.
func WidgetSources() widget.Sources {
	return widget.Sources{LoopFile: widget.SchemaOf[ouroboros.Snapshot]()}
}

// Option configures the view beyond its required capabilities.
type Option func(view *OpsView) error

// WithWidgetDefinitions grants the widgets directory: files reads its
// definitions and watcher reports their changes. Without it the panel stays
// empty.
func WithWidgetDefinitions(files iofs.IFiles, watcher iofs.IWatcher) Option {
	return func(view *OpsView) error {
		if files == nil || watcher == nil {
			return ErrNoDefinitionWatcher
		}
		view.definitions, view.definitionWatcher = files, watcher
		return nil
	}
}

// DefinitionFiles is one definition directory's files as the effect read
// them, by file name. A file that could not be read is absent, and the check
// says so.
type DefinitionFiles struct {
	Name  string            `json:"name"`
	Files map[string]string `json:"files"`
}

// InstalledWidget is one definition as the view holds it: the checked
// definition, or every reason it was refused.
type InstalledWidget struct {
	Name    string
	Reasons []string
	checked *widget.CheckedDefinition[live.AnonymousIdentity]
	files   DefinitionFiles
}

// Installed reports whether the widget is drawn.
func (installed InstalledWidget) Installed() bool { return installed.checked != nil }

// The reason a check gives for a definition another one already holds the
// identity of.
const reasonDuplicate = "%s: region %q or widget name %s is already installed by %s"

// InstallWidgets checks every definition against the sources and returns
// them in name order, reusing the previous check of a definition whose files
// did not change. Two definitions may not claim one region or one widget name;
// the second in name order is refused. It is pure.
func InstallWidgets(previous []InstalledWidget, definitions []DefinitionFiles, sources widget.Sources) []InstalledWidget {
	slices.SortFunc(definitions, func(a, b DefinitionFiles) int { return strings.Compare(a.Name, b.Name) })
	installed := make([]InstalledWidget, 0, len(definitions))
	claimed := map[string]string{}
	for _, definition := range definitions {
		widget := checkFiles(previous, definition, sources)
		if widget.checked != nil {
			document := widget.checked.Widget.Document()
			for _, identity := range []string{document.Region, document.Name} {
				if holder, taken := claimed[identity]; taken {
					widget.Reasons = append(widget.Reasons, fmt.Sprintf(reasonDuplicate, definition.Name, document.Region, document.Name, holder))
				}
			}
			if len(widget.Reasons) > 0 {
				widget.checked = nil
			} else {
				claimed[document.Region], claimed[document.Name] = definition.Name, definition.Name
			}
		}
		installed = append(installed, widget)
	}
	return installed
}

// checkFiles checks one definition, or reuses its previous check.
func checkFiles(previous []InstalledWidget, definition DefinitionFiles, sources widget.Sources) InstalledWidget {
	for _, earlier := range previous {
		if earlier.Name == definition.Name && maps.Equal(earlier.files.Files, definition.Files) && len(earlier.Reasons) == 0 {
			return earlier
		}
	}
	checked, reasons := widget.CheckDefinition[live.AnonymousIdentity](definitionFS(definition), definition.Name, sources)
	return InstalledWidget{Name: definition.Name, Reasons: reasons, checked: checked, files: definition}
}

// definitionFS lays one definition's files out as the directory they were
// read from, for the check, which reads a file system. The reducer holds the
// files' contents, never a file capability, so the file system is the
// standard library's in-memory one.
func definitionFS(definition DefinitionFiles) fstest.MapFS {
	files := fstest.MapFS{}
	for name, content := range definition.Files {
		files[definition.Name+"/"+name] = &fstest.MapFile{Data: []byte(content)}
	}
	return files
}

// ReadDefinitions reads every definition directory beneath files' root: its
// three definition files, whichever exist. A directory holding none of them
// is not a definition, which is also what a definition being created looks
// like before its first file lands.
func ReadDefinitions(files iofs.IFiles) ([]DefinitionFiles, error) {
	entries, err := files.ReadDir(rootName)
	if err != nil {
		return nil, err
	}
	var definitions []DefinitionFiles
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if definition := readDefinition(files, entry.Name()); len(definition.Files) > 0 {
			definitions = append(definitions, definition)
		}
	}
	return definitions, nil
}

func readDefinition(files iofs.IFiles, name string) DefinitionFiles {
	definition := DefinitionFiles{Name: name, Files: map[string]string{}}
	for _, file := range []string{widget.DefinitionFile, widget.BindingFile, widget.FixturesFile} {
		if content, err := files.ReadFile(name + "/" + file); err == nil {
			definition.Files[file] = string(content)
		}
	}
	return definition
}

// WidgetsEvent is the event the widgets effect emits when a definition
// changed, addressed to the panel.
func WidgetsEvent(definitions []DefinitionFiles) (live.Event, error) {
	encoded, err := json.Marshal(definitions)
	if err != nil {
		return live.Event{}, err
	}
	return live.Event{Name: EventWidgets, FragmentID: WidgetsRegion, Fields: live.NewFields(map[string]string{FieldWidgets: string(encoded)})}, nil
}

// followWidgets is the widgets directory's one I/O: it reads every
// definition, then reads them again whenever the kernel reports a change in
// the directory or in one of its definitions, and emits them when they
// changed. A definition directory is watched from the moment it is listed.
func (view *OpsView) followWidgets(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
	watch, err := view.definitionWatcher.Watch(ctx)
	if err != nil {
		return err
	}
	if err := watch.Add(rootName); err != nil {
		return err
	}
	watched := map[string]bool{}
	var last string
	deliver := func() error {
		definitions, err := ReadDefinitions(view.definitions)
		if err != nil {
			view.logger.Warn("ops view: widget definitions not read", "error", err)
			return nil
		}
		listed := map[string]bool{}
		for _, definition := range definitions {
			listed[definition.Name] = true
			if watched[definition.Name] {
				continue
			}
			if err := watch.Add(definition.Name); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
				return err
			}
			watched[definition.Name] = true
		}
		for name := range watched {
			if !listed[name] {
				delete(watched, name)
			}
		}
		event, err := WidgetsEvent(definitions)
		if err != nil {
			return err
		}
		if encoded := event.Fields.Get(FieldWidgets); encoded != last {
			last = encoded
			return emit(event)
		}
		return nil
	}
	if err := deliver(); err != nil {
		return err
	}
	for change := range watch.Changes {
		if change.Err != nil {
			return change.Err
		}
		if err := deliver(); err != nil {
			return err
		}
	}
	return nil
}

// widgetsView is the panel's data: each widget drawn, or refused with its
// reasons.
type widgetsView struct {
	Region  string
	Widgets []installedView
}

type installedView struct {
	Name    string
	Markup  template.HTML
	Reasons []string
}

// renderWidgets draws the panel: every installed widget over the loop's
// snapshot, which is the one source a widget binds, at its zero values until
// the loop has written one.
func renderWidgets(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		panel := widgetsView{Region: WidgetsRegion}
		var snapshot []byte
		if state.loop.Present {
			encoded, err := json.Marshal(state.loop.Snapshot)
			if err != nil {
				return err
			}
			snapshot = encoded
		}
		for _, installed := range state.widgets {
			entry := installedView{Name: installed.Name, Reasons: installed.Reasons}
			if installed.checked != nil {
				markup, err := drawInstalled(ctx, installed.checked, snapshot)
				if err != nil {
					entry.Reasons = []string{err.Error()}
				}
				// Only markup html/template already escaped crosses this boundary.
				entry.Markup = template.HTML(markup)
			}
			panel.Widgets = append(panel.Widgets, entry)
		}
		return views.ExecuteTemplate(writer, widgetsTemplate, panel)
	})
}

// drawInstalled draws one widget over a source document, or at its zero
// values when there is none yet.
func drawInstalled(ctx context.Context, checked *widget.CheckedDefinition[live.AnonymousIdentity], source []byte) (string, error) {
	if source != nil {
		return checked.Render(source)
	}
	var rendered bytes.Buffer
	err := checked.Widget.Render(checked.Widget.Initial()).Render(ctx, &rendered)
	return rendered.String(), err
}

// widgetsChanged reports whether the panel moved: a definition changed, or
// the source the widgets draw.
func widgetsChanged(previous viewState, next viewState) bool {
	if loopChanged(previous, next) {
		return true
	}
	if len(previous.widgets) != len(next.widgets) {
		return true
	}
	for index := range previous.widgets {
		before, after := previous.widgets[index], next.widgets[index]
		if before.Name != after.Name || before.checked != after.checked || !slices.Equal(before.Reasons, after.Reasons) {
			return true
		}
	}
	return false
}

// widgetsFragment is the panel as the live library mounts it.
func widgetsFragment() live.Fragment[viewState] {
	return live.Fragment[viewState]{ID: WidgetsRegion, Render: renderWidgets, Dirty: widgetsChanged}
}
