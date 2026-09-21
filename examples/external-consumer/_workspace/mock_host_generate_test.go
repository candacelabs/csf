package externalconsumer_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_host_test.go -package=externalconsumer_test github.com/candacelabs/csf/services/candaceos/harness IHost
