// Copyright 2026 Candace Labs

package docker_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_container_api_test.go -package=docker_test github.com/candacelabs/csf/io/ipc/docker IContainerAPI
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_listener_test.go -package=docker_test github.com/candacelabs/csf/io/net IListener
