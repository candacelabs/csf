// Copyright 2026 Candace Labs

package chat

import (
	"bytes"
	"context"
	_ "embed"
	"html/template"
	"io"

	"github.com/a-h/templ"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The page and its three live regions. Every render is a pure function of
// state: equal states render byte-identical markup.
//
//go:embed chat.html
var pageSource string

var views = template.Must(template.New("chat").Parse(pageSource))

const (
	pageTemplate       = "page"
	statusTemplate     = "status"
	transcriptTemplate = "transcript"
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

// pageView is the document's data: the state, the rendered regions and the
// live runtime tag.
type pageView struct {
	chatState
	Status     template.HTML
	Transcript template.HTML
	Composer   template.HTML
	Script     template.HTML
}

// regionView is one region's data: the state and the attributes gotth-live
// reads, rendered as a string so the template writes them verbatim.
type regionView struct {
	chatState
	Region    string
	OnSend    template.HTMLAttr
	OnCancel  template.HTMLAttr
	Preserved template.HTMLAttr
}

func renderPage(state chatState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		script, err := renderComponent(ctx, live.Script(state.Mount))
		if err != nil {
			return err
		}
		status, err := renderComponent(ctx, renderStatus(state))
		if err != nil {
			return err
		}
		transcript, err := renderComponent(ctx, renderTranscript(state))
		if err != nil {
			return err
		}
		composer, err := renderComponent(ctx, renderComposer(state))
		if err != nil {
			return err
		}
		return views.ExecuteTemplate(writer, pageTemplate, pageView{
			chatState: state, Script: template.HTML(script), Status: template.HTML(status),
			Transcript: template.HTML(transcript), Composer: template.HTML(composer),
		})
	})
}

func renderStatus(state chatState) templ.Component {
	return region(statusTemplate, fragmentStatus, state)
}

func renderTranscript(state chatState) templ.Component {
	return region(transcriptTemplate, fragmentTranscript, state)
}

func renderComposer(state chatState) templ.Component {
	return region(composerTemplate, fragmentComposer, state)
}

// region renders one live region from its template, with the gotth-live
// attributes spelled by the library rather than by hand.
func region(name string, id string, state chatState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		onSend, err := attributes(live.On("submit", eventSend))
		if err != nil {
			return err
		}
		onCancel, err := attributes(live.On("click", eventCancel))
		if err != nil {
			return err
		}
		preserved, err := attributes(live.Preserve())
		if err != nil {
			return err
		}
		return views.ExecuteTemplate(writer, name, regionView{
			chatState: state, Region: id,
			OnSend: template.HTMLAttr(onSend), OnCancel: template.HTMLAttr(onCancel), Preserved: template.HTMLAttr(preserved),
		})
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
