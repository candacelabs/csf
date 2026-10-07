// Copyright 2026 Candace Labs

package chat

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"

	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/io/net/model/copilotcli"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/services/opsview"
)

// The page and its three live regions. Every render is a pure function of
// state: equal states render byte-identical markup.
//
//go:embed chat.html
var pageSource string

var pages = template.Must(template.New("chat").Funcs(template.FuncMap{"pullNumber": pullNumber}).Parse(pageSource))

// The DOM events and keys the page binds.
const (
	domSubmit  = "submit"
	domClick   = "click"
	domKeydown = "keydown"
	keyEnter   = "Enter"
	keyEscape  = "Escape"
)

// executorNames are the turn executors as the status bar names them, by the
// provider their records carry.
var executorNames = map[string]string{
	claudecode.ProviderName: "Claude Code",
	copilotcli.ProviderName: "Copilot",
}

// barView is the status bar's facts in words.
type barView struct {
	Executor string
	Model    string
	Context  string
	TurnCost string
	Session  string
	Elapsed  string
	// Since is when the running turn started, for the page's clock to count
	// from; empty between turns.
	Since string
	Phase string
}

// The status bar's formats.
const (
	usdFormat    = "$%.2f"
	unknownValue = "–"
)

// barOf is the status bar's facts in words. A cost the executor does not
// report, and a context no message has measured yet, read as unknown.
func barOf(state chatState) barView {
	status := state.Transcript.Status
	view := barView{Executor: executorNames[status.Executor], Model: status.Model, Context: unknownValue, TurnCost: unknownValue, Session: unknownValue, Phase: status.Phase}
	if view.Executor == "" {
		view.Executor = status.Executor
	}
	if status.Context > 0 {
		view.Context = tokensWords(status.Context)
	}
	if status.CostKnown {
		view.TurnCost, view.Session = fmt.Sprintf(usdFormat, status.TurnCost), fmt.Sprintf(usdFormat, status.SessionCost)
	}
	switch {
	case status.Phase != phaseIdle:
		// A render is a pure function of state, so the page's own clock
		// counts a running turn's time from Since.
		view.Since = status.TurnStarted.UTC().Format(time.RFC3339Nano)
	case !status.TurnStarted.IsZero():
		view.Elapsed = elapsedWords(status.TurnEnded.Sub(status.TurnStarted))
	}
	return view
}

// tokensWords is a token count in thousands.
func tokensWords(tokens int64) string {
	if tokens < 1000 {
		return fmt.Sprintf("%d tokens", tokens)
	}
	return fmt.Sprintf("%.1fk tokens", float64(tokens)/1000)
}

// pullNumber is #N for a pull request URL.
func pullNumber(url string) string {
	if _, number, found := strings.Cut(url, "/pull/"); found && number != "" {
		return "#" + strings.Trim(number, "/")
	}
	return "pull request"
}

const (
	pageTemplate       = "page"
	statusTemplate     = "status"
	tasksTemplate      = "tasks"
	transcriptTemplate = "transcript"
	rowTemplate        = "row"
	barTemplate        = "bar"
	composerTemplate   = "composer"
)

// markdown renders what the operator and the agent wrote. GitHub-flavoured,
// with a newline in a paragraph kept as a line break, because a message is
// written the way a chat is read. Raw HTML in the source is omitted and
// unsafe link targets are dropped, which is goldmark's default and the
// reason the renderer is not given the unsafe option: the text comes from an
// agent and from whoever reaches the page.
var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(goldmarkhtml.WithHardWraps()),
)

// renderMarkdown converts text to the markup a transcript entry shows. A
// source goldmark cannot render is shown escaped rather than lost.
func renderMarkdown(text string) template.HTML {
	var buffer bytes.Buffer
	if err := markdown.Convert([]byte(text), &buffer); err != nil {
		return template.HTML("<p>" + template.HTMLEscapeString(text) + "</p>")
	}
	return template.HTML(buffer.String())
}

// pageView is the document's data: the state, the rendered regions, the
// shared token stylesheet and the live runtime tag.
type pageView struct {
	chatState
	Style      template.CSS
	Status     template.HTML
	Tasks      template.HTML
	Transcript template.HTML
	Bar        template.HTML
	Composer   template.HTML
	Script     template.HTML
}

// regionView is one region's data: the state, the status bar in words, and
// the attributes gotth-live reads, rendered as strings so the template writes
// them verbatim.
type regionView struct {
	chatState
	Region      string
	Bar         barView
	Workbench   string
	OnSend      template.HTMLAttr
	OnEnter     template.HTMLAttr
	OnCancel    template.HTMLAttr
	OnAskCancel template.HTMLAttr
	OnKeep      template.HTMLAttr
	Preserved   template.HTMLAttr
}

// rowView is one row as its region renders it: the row, its region, whether
// this viewer opened it, and the binding that opens or closes it. The click
// is the server's to decide (PreventDefault), so the element's open state is
// always the one the server renders.
type rowView struct {
	row
	Region   string
	Open     bool
	OnToggle template.HTMLAttr
}

// rowViewOf is the view of one row in state.
func rowViewOf(state chatState, shown row) (rowView, error) {
	toggle, err := attributes(live.OnWith(domClick, eventToggle, live.Bind{Fields: map[string]string{fieldRow: strconv.Itoa(shown.Seq)}, PreventDefault: true}))
	if err != nil {
		return rowView{}, err
	}
	return rowView{row: shown, Region: rowRegion(shown.Seq), Open: slices.Contains(state.Open, shown.Seq), OnToggle: template.HTMLAttr(toggle)}, nil
}

// Rows are the transcript's rows as their regions render them.
func (view regionView) Rows() ([]rowView, error) {
	rows := make([]rowView, 0, len(view.Transcript.Rows))
	for _, shown := range view.Transcript.Rows {
		next, err := rowViewOf(view.chatState, shown)
		if err != nil {
			return nil, err
		}
		rows = append(rows, next)
	}
	return rows, nil
}

// renderRow renders the region of the row of seq; a row the transcript no
// longer holds renders nothing, and its region is gone from the next render.
func renderRow(seq int) func(state chatState) templ.Component {
	return func(state chatState) templ.Component {
		return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
			shown, held := state.Transcript.rowOf(seq)
			if !held {
				return nil
			}
			view, err := rowViewOf(state, shown)
			if err != nil {
				return err
			}
			return pages.ExecuteTemplate(writer, rowTemplate, view)
		})
	}
}

// TasksDone counts the task list's finished items.
func (view regionView) TasksDone() int {
	done := 0
	for _, each := range view.Transcript.Tasks {
		if each.State == taskDone {
			done++
		}
	}
	return done
}

func renderPage(state chatState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		script, err := renderComponent(ctx, live.Script(state.Mount))
		if err != nil {
			return err
		}
		view := pageView{chatState: state, Style: template.CSS(opsview.Stylesheet()), Script: template.HTML(script)}
		for _, part := range []struct {
			render func(state chatState) templ.Component
			into   *template.HTML
		}{
			{renderStatus, &view.Status}, {renderTasks, &view.Tasks}, {renderTranscript, &view.Transcript},
			{renderBar, &view.Bar}, {renderComposer, &view.Composer},
		} {
			rendered, err := renderComponent(ctx, part.render(state))
			if err != nil {
				return err
			}
			*part.into = template.HTML(rendered)
		}
		return pages.ExecuteTemplate(writer, pageTemplate, view)
	})
}

func renderStatus(state chatState) templ.Component {
	return region(statusTemplate, fragmentStatus, state)
}

func renderTasks(state chatState) templ.Component {
	return region(tasksTemplate, fragmentTasks, state)
}

func renderTranscript(state chatState) templ.Component {
	return region(transcriptTemplate, fragmentTranscript, state)
}

func renderBar(state chatState) templ.Component {
	return region(barTemplate, fragmentBar, state)
}

func renderComposer(state chatState) templ.Component {
	return region(composerTemplate, fragmentComposer, state)
}

// region renders one live region from its template, with the gotth-live
// attributes spelled by the library rather than by hand.
func region(name string, id string, state chatState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		view := regionView{chatState: state, Region: id, Workbench: opsview.PagePath}
		if name == barTemplate {
			view.Bar = barOf(state)
		}
		for _, binding := range []struct {
			attributes templ.Attributes
			into       *template.HTMLAttr
		}{
			{live.On(domSubmit, eventSend), &view.OnSend},
			// Enter sends and Shift+Enter breaks the line, as in every chat.
			// Esc interrupts: the harness stops a turn only by canceling the
			// session, so it asks the operator to confirm that.
			{live.OnAll(
				live.OnWith(domKeydown, eventSend, live.Bind{Keys: []string{keyEnter}, NoModifiers: true, PreventDefault: true}),
				live.OnWith(domKeydown, eventAskCancel, live.Bind{Keys: []string{keyEscape}, PreventDefault: true}),
			), &view.OnEnter},
			{live.On(domClick, eventCancel), &view.OnCancel},
			{live.On(domClick, eventAskCancel), &view.OnAskCancel},
			{live.On(domClick, eventKeep), &view.OnKeep},
			{live.Preserve(), &view.Preserved},
		} {
			rendered, err := attributes(binding.attributes)
			if err != nil {
				return err
			}
			*binding.into = template.HTMLAttr(rendered)
		}
		return pages.ExecuteTemplate(writer, name, view)
	})
}

// attributes renders templ attributes as the text an element carries.
func attributes(attributes templ.Attributes) (string, error) {
	var buffer bytes.Buffer
	if err := templ.RenderAttributes(context.Background(), &buffer, attributes); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func renderComponent(ctx context.Context, component templ.Component) (string, error) {
	var buffer bytes.Buffer
	if err := component.Render(ctx, &buffer); err != nil {
		return "", err
	}
	return buffer.String(), nil
}
