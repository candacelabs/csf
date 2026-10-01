package operator_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_runtime_test.go -package=operator_test github.com/candacelabs/csf/services/candaceos/harness IRuntime
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_reconciler_test.go -package=operator_test github.com/candacelabs/csf/services/candaceos/operator IReconciler
