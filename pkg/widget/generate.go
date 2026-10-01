package widget

// This file carries only the code-generation directive for the SDK's one
// contract interface; it defines no types.
//
// The generated gomock double for IWidget lives in the unexported subpackage
// pkg/widget/internal/mocks, so the SDK itself stays free of any test-tooling
// dependency while its own suites and a host's can both drive a widget that
// records what the registry did to it. go.uber.org/mock is the maintained fork
// of the archived golang/mock. The generated code is committed.
//
// It is deliberately not one of gen.sh's outputs. gen.sh runs inside the
// toolchain container, which carries protoc and the pinned templ CLI and not
// mockgen; adding a fourth tool to that image to regenerate a file that changes
// only when IWidget changes would buy a check that fires roughly never. The
// same trade is already made by services/warden, which is the precedent this
// follows.
//
// Regenerate after any change to IWidget (run from candace/, with
// `go install go.uber.org/mock/mockgen@v0.6.0` on PATH):
//
//	go generate -run mockgen ./pkg/widget
//
// The -run filter is load-bearing rather than tidy. This package holds a second
// directive — doc.go's `bash gen.sh` — and go generate runs both in file order
// and stops at the first failure, so an unfiltered `go generate ./pkg/widget`
// reaches gen.sh first. That leaves the two directives with no environment in
// common: gen.sh needs the toolchain container's templ CLI, the container
// deliberately carries no mockgen, and outside it gen.sh fails before mockgen
// is ever reached. Naming the directive is what makes each runnable where its
// tool lives.
//
//go:generate mockgen -destination=internal/mocks/mocks.go -package=mocks github.com/candacelabs/csf/pkg/widget IWidget
