//go:build !goverter

package copilotadapter

// The generator sets the goverter build tag while it reloads the package
// without its own previous output, so the one reference to the generated type
// lives here, excluded under that tag; every regeneration would otherwise fail
// on the type it is about to write.
func init() {
	views = &iViewConverterImpl{}
}
