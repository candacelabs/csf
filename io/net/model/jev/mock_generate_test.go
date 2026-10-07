// Copyright 2026 Candace Labs

package jev_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_client_test.go -package=jev_test github.com/candacelabs/csf/io/net/http IHTTPClient
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_decider_test.go -package=jev_test github.com/candacelabs/csf/io/net/model/jev IDecider
