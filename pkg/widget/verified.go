package widget

import (
	"bytes"
	"context"
	"html/template"
	"io"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// VerifiedAction is a button whose operation carries its acceptance check: a
// dry run of the exact operation against the live system, and the result the
// operation is expected to produce. A host verifies its actions when it
// installs the buttons and again before each render, and draws a button whose
// check failed disabled with the reason, so a page never offers a button that
// might not work.
//
// Request is the operation's own typed input, so the page runs exactly what
// was checked.
type VerifiedAction[Request any] struct {
	// ID names the action on the page; the button sends it with its event.
	ID    string
	Label string
	// Request is the exact operation the button runs.
	Request Request
}

// Expectation is what a dry run says the operation will do.
type Expectation struct {
	// Result is the expected result, in words.
	Result string
	// Confirm names what a destructive operation changes; the page lists them
	// and asks for one confirm before it runs. Empty runs at once.
	Confirm []string
}

// Verified is one action after its check, as the data a render reads: enabled
// with what it will do, or disabled with why.
type Verified[Request any] struct {
	VerifiedAction[Request]
	Expectation
	// Refusal is why the check failed; empty when the button is enabled.
	Refusal string
}

// Enabled reports whether the check passed.
func (verified Verified[Request]) Enabled() bool { return verified.Refusal == "" }

// VerifyActions runs check, the dry run of each action's operation against
// the live system, and keeps each verdict in order.
func VerifyActions[Request any](ctx context.Context, actions []VerifiedAction[Request], check func(ctx context.Context, request Request) (Expectation, error)) []Verified[Request] {
	verified := make([]Verified[Request], 0, len(actions))
	for _, action := range actions {
		expectation, err := check(ctx, action.Request)
		result := Verified[Request]{VerifiedAction: action, Expectation: expectation}
		if err != nil {
			result.Refusal = err.Error()
		}
		verified = append(verified, result)
	}
	return verified
}

// FindVerified is the verified action named id.
func FindVerified[Request any](verified []Verified[Request], id string) (Verified[Request], bool) {
	for _, action := range verified {
		if action.ID == id {
			return action, true
		}
	}
	return Verified[Request]{}, false
}

// verifiedButtonSource draws an enabled button with its expected result as
// its title, or a disabled one followed by the reason.
const verifiedButtonSource = `{{if .Enabled}}<button type="button" class="{{.Class}}" data-verified="{{.ID}}" title="{{.Result}}" {{.On}}>{{.Label}}</button>` +
	`{{else}}<button type="button" class="{{.Class}}" data-verified="{{.ID}}" data-refusal="{{.Refusal}}" title="{{.Refusal}}" disabled>{{.Label}}</button><small class="refusal" data-verified-refusal="{{.ID}}">{{.Refusal}}</small>{{end}}`

// verifiedPress is the DOM event a verified button raises its event on.
const verifiedPress = "click"

var verifiedButton = template.Must(template.New("verified").Parse(verifiedButtonSource))

// VerifiedButton draws one verified action as a button that raises event
// with the action's ID in field when it is enabled, and as a disabled button
// with the reason when its check failed.
func VerifiedButton[Request any](verified Verified[Request], event string, field string, class string) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		var on bytes.Buffer
		if err := templ.RenderAttributes(ctx, &on, live.OnWith(verifiedPress, event, live.Bind{Fields: map[string]string{field: verified.ID}})); err != nil {
			return err
		}
		return verifiedButton.Execute(writer, struct {
			ID, Label, Result, Refusal, Class string
			Enabled                           bool
			On                                template.HTMLAttr
		}{verified.ID, verified.Label, verified.Result, verified.Refusal, class, verified.Enabled(), template.HTMLAttr(on.String())})
	})
}
