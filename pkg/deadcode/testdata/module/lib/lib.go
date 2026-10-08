package lib

import "strings"

// Used is reached from main.
func Used() {}

// helper is unexported and called by nothing.
func helper() string { return strings.ToUpper("dead") }

// Exported is API nothing in the module calls yet; it is kept.
func Exported() { onlyFromExported() }

// onlyFromExported is live because kept API calls it.
func onlyFromExported() {}

// Namer is satisfied by named, though nothing calls Name.
type Namer interface{ Name() string }

type named struct{}

// Name is unexported in effect and unreachable, but the build needs it.
func (named) Name() string { return "named" }

var _ Namer = named{}
