// Copyright 2026 Candace Labs

package opsview_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_watcher_test.go -package=opsview_test github.com/candacelabs/csf/io/kernel/fs IWatcher
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_launcher_test.go -package=opsview_test github.com/candacelabs/csf/io/ipc/proc ILauncher
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_client_test.go -package=opsview_test github.com/candacelabs/csf/io/net/http IHTTPClient
