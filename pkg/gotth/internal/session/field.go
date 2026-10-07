package session

// Field is one form value carried by an event.
type Field struct {
	// Key is the form field's name, as the browser sent it.
	Key string

	// Value is its value, already past the refinement boundary — length
	// bounded and valid UTF-8 — and past authorization, but otherwise
	// untrusted application input.
	Value string
}
