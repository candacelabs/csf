// Copyright 2026 Candace Labs

package ouroboros_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_launcher_test.go -package=ouroboros_test github.com/candacelabs/csf/io/ipc/proc ILauncher
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_sessions_test.go -package=ouroboros_test github.com/candacelabs/csf/csf IAgentSessions
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_watcher_test.go -package=ouroboros_test github.com/candacelabs/csf/io/kernel/fs IWatcher
