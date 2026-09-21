// Package widgettest renders a registered widget's own live region, so a
// specification can assert on what a viewer receives rather than on how the
// markup was produced.
//
// # Why it renders rather than reads source
//
// Two widgets that draw the same card share no file names, no function names
// and no formatting when one is generated from a document and the other is
// written by hand against the SDK. The one thing they can be held to
// identically is what comes out of Render, so everything here takes a widget
// and gives back markup: a card assertion written against this package holds
// of both, which is what makes it fair to compare them.
//
// # What it is not
//
// It is not an HTML parser and does not want to become one. [Rendered] answers
// the questions a card assertion actually asks — does this string appear, how
// many times, in what order, on how many elements of one class, and is the
// landmark named — and every one of them is a substring or a class-token
// question. A specification that needs a document tree has outgrown this
// package and should say so rather than grow it a DOM.
//
// # The mount is a real one
//
// [Mount] puts the widget in a registry of its own and takes the fragment the
// live path patches, rather than calling the widget's Render directly. The
// difference is not decorative: a region identity that does not match the
// fragment's, a mount that never returns state, and a dirty declaration that
// disagrees with the markup are all mistakes a direct call holds constant.
package widgettest
