package warden

// This file carries only the code-generation directive for the contract
// interfaces; it defines no types and does not alter the frozen contract.
//
// The generated gomock doubles for the eight contract interfaces live in the
// unexported subpackage services/warden/internal/mocks so the contract package
// itself stays free of any test-tooling dependency. go.uber.org/mock is the maintained fork of
// the archived golang/mock. The generated code is committed.
//
// Regenerate after any interface change (run from candace/, with
// `go install go.uber.org/mock/mockgen@v0.6.0` on PATH):
//
//	go generate ./services/warden
//
//go:generate mockgen -destination=internal/mocks/mocks.go -package=mocks github.com/candacelabs/csf/services/warden ITransport,INotifier,IStore,IClock,IPeerDiscoverer,IViewSource,IIncidentLog,IRPCHandler
