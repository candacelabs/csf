package opencode

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=provider.go -destination=mock_provider_test.go -package=opencode -mock_names=iProvider=MockProvider
