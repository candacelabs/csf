package workcontinuity_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=source.go -destination=mock_source_test.go -package=workcontinuity_test
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_launcher_test.go -package=workcontinuity_test github.com/candacelabs/csf/ipc/proc ILauncher
