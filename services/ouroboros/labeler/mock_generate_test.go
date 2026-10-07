// Copyright 2026 Candace Labs

package labeler_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_launcher_test.go -package=labeler_test github.com/candacelabs/csf/io/ipc/proc ILauncher
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_containers_test.go -package=labeler_test github.com/candacelabs/csf/services/ouroboros/labeler IContainers
