package webui

import (
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"strings"
)

// The overlay tree's two subtrees and the pattern that selects a template file
// in either the embedded or the overlay tree.
const (
	overlayTemplatesDir = "templates"
	overlayAssetsDir    = "assets"
	templateGlob        = overlayTemplatesDir + "/*.html"
)

// ErrInvalidUIOverlay reports an overlay that cannot supply presentation:
// a missing filesystem, a second overlay, or a template file that does not
// parse.
var ErrInvalidUIOverlay = errors.New("webui: invalid UI overlay")

// WithUIOverlay resolves presentation against a caller-supplied filesystem
// before the embedded one. The overlay is a single tree with the same shape as
// this package's own:
//
//	templates/*.html   named-block definitions that redefine the shipped ones
//	assets/*           files served in place of the embedded asset of that name
//
// Both subtrees are optional and both are overlay-first with embedded
// fallback: a name the overlay does not carry keeps shipping from the embedded
// filesystem, so an overlay replaces only what it names.
//
// An overlay template file contributes {{define}} blocks; its text outside a
// define is ignored. Replacing a whole page therefore means defining that
// page's block, exactly as the embedded files do. The supported block names,
// and the data each receives, are listed in this package's documentation.
//
// Overlay assets are served by the same handler as the embedded ones and keep
// its behavior: the same asset URL space, the same cache and content-type
// headers, and the same generated brand stylesheet layered on top. An overlay
// file outside assets/ is never reachable through the asset route.
//
// Only one overlay may be supplied. Overlay templates are operator-trusted
// markup on the same footing as Brand.Wordmark: they are page source, not
// escaped content, so never assemble one from a browser request, a fleet node,
// or an agent. The page's Content-Security-Policy is unchanged and still
// permits only same-origin styles and scripts, so an overlay that inlines
// either will simply not run.
func WithUIOverlay(overlay fs.FS) Option {
	return func(settings *presentationOptions) error {
		if overlay == nil {
			return fmt.Errorf("%w: overlay filesystem is required", ErrInvalidUIOverlay)
		}
		if settings.overlay != nil {
			return fmt.Errorf("%w: only one overlay may be supplied", ErrInvalidUIOverlay)
		}
		settings.overlay = overlay
		return nil
	}
}

// parseTemplates parses the embedded pages and then lets the overlay redefine
// whichever named blocks it carries. Redefinition happens before the first
// render, which is the only point at which html/template accepts it.
func parseTemplates(overlay fs.FS) (*template.Template, error) {
	parsed, err := template.New("candaceos").Funcs(templateFuncs()).ParseFS(templatesFS, templateGlob)
	if err != nil {
		return nil, fmt.Errorf("parse CandaceOS web templates: %w", err)
	}
	if overlay == nil {
		return parsed, nil
	}

	names, err := fs.Glob(overlay, templateGlob)
	if err != nil {
		return nil, fmt.Errorf("%w: reading overlay templates: %w", ErrInvalidUIOverlay, err)
	}
	for _, name := range names {
		contents, err := fs.ReadFile(overlay, name)
		if err != nil {
			return nil, fmt.Errorf("%w: reading overlay template %s: %w", ErrInvalidUIOverlay, name, err)
		}
		// The file is associated under a name of its own so that parsing it can
		// only add or redefine blocks, never displace the embedded page that
		// happens to share its filename.
		if _, err := parsed.New("overlay:" + name).Parse(string(contents)); err != nil {
			return nil, fmt.Errorf("%w: parsing overlay template %s: %w", ErrInvalidUIOverlay, name, err)
		}
	}
	return parsed, nil
}

// assetTree returns the filesystem behind the asset route: the overlay's
// assets/ subtree first, then the embedded assets. The overlay is scoped to
// that subtree, so no cleaned request path can reach its templates.
func assetTree(overlay fs.FS) fs.FS {
	if overlay == nil {
		return assetsFS
	}
	return layeredFS{layers: []fs.FS{
		scopedFS{prefix: overlayAssetsDir, fsys: overlay},
		assetsFS,
	}}
}

// scopedFS exposes only one subtree of the filesystem it wraps. A name outside
// that subtree reports fs.ErrNotExist, which lets a layered lookup continue to
// the next layer rather than failing outright.
type scopedFS struct {
	prefix string
	fsys   fs.FS
}

func (s scopedFS) Open(name string) (fs.File, error) {
	if name != s.prefix && !strings.HasPrefix(name, s.prefix+"/") {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return s.fsys.Open(name)
}

// layeredFS resolves each name against its layers in order and returns the
// first that carries it. Only absence advances to the next layer: a layer that
// fails for any other reason fails the lookup, so a broken overlay is visible
// rather than silently serving the shipped file.
type layeredFS struct {
	layers []fs.FS
}

func (l layeredFS) Open(name string) (fs.File, error) {
	var absent error
	for _, layer := range l.layers {
		file, err := layer.Open(name)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if absent == nil {
			absent = err
		}
	}
	if absent == nil {
		absent = &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return nil, absent
}
