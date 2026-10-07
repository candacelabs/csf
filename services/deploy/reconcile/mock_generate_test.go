package reconcile

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_launcher_test.go -package=reconcile github.com/candacelabs/csf/io/ipc/proc ILauncher
