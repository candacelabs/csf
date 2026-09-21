// Package events is the compiled source for docs/guide/events-and-forms.md.
//
// It is a composer: a form that submits, a field that reports every keystroke,
// a checkbox whose absence means something, and a key that clears the draft.
package events

import (
	"strings"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The event names. Every one of them is in Config.Events; a name that is not
// there is refused with UNKNOWN_EVENT and counted, and never reaches Reduce.
const (
	EventSubmit = "compose.submit"
	EventDraft  = "compose.draft"
	EventClear  = "compose.clear"
)

// The form field names. They are the name attributes in view.templ and the
// keys Fields is read with, so they are constants rather than two spellings of
// one string.
const (
	FieldBody   = "body"
	FieldUrgent = "urgent"
)

// MaxBody is the length the reducer enforces. The client enforces nothing:
// validation is state the server computes, and the browser is not asked to
// agree.
const MaxBody = 140

// State is one session's view of the composer.
//
// Every field is comparable, which is a requirement rather than a preference —
// see guide/fragments-and-dirty-tracking.md.
type State struct {
	Draft      string
	DraftError string
	Posted     int
	LastBody   string
	LastUrgent bool
}

// Reduce is the pure state transition.
func Reduce(s State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
	switch ev.Name {
	case EventDraft:
		// One field, sent as the user types. The client sends the control's
		// own name and value, so this is the same Fields a submit carries.
		s.Draft = ev.Fields.Get(FieldBody)
		s.DraftError = validate(s.Draft)

	case EventSubmit:
		body := strings.TrimSpace(ev.Fields.Get(FieldBody))
		if msg := validate(body); msg != "" {
			// Validation feedback is reducer output. There is no validation
			// vocabulary and no error type: the message is state, and the
			// fragment renders it.
			s.DraftError = msg
			return s, nil
		}
		// Lookup, not Get. An unchecked checkbox is ABSENT from the form data,
		// not present-and-empty, and Get cannot tell the two apart. Every
		// boolean field read with Get is a bug waiting for its first false.
		_, urgent := ev.Fields.Lookup(FieldUrgent)
		s.Posted++
		s.LastBody = body
		s.LastUrgent = urgent
		s.Draft = ""
		s.DraftError = ""

	case EventClear:
		s.Draft = ""
		s.DraftError = ""
	}
	return s, nil
}

func validate(body string) string {
	switch {
	case strings.TrimSpace(body) == "":
		return "say something first"
	case len(body) > MaxBody:
		return "that is too long"
	default:
		return ""
	}
}

// Remaining is derived state the fragment renders. It is a method rather than
// a template expression so the fragment's Dirty function can compare exactly
// what the markup shows.
func (s State) Remaining() int { return MaxBody - len(s.Draft) }
