package httpapi_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_backend_test.go -package=httpapi_test github.com/candacelabs/csf/services/candaceos/httpapi IBackend
