// Copyright 2026 Candace Labs

package await_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_launcher_test.go -package=await_test github.com/candacelabs/csf/io/ipc/proc ILauncher
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_client_test.go -package=await_test github.com/candacelabs/csf/io/net/http IHTTPClient
