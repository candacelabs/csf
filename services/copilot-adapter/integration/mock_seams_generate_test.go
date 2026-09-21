package integration_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=../seams.go -aux_files=github.com/candacelabs/csf/services/copilot-adapter/storedb=../storedb/querier.go -destination=mock_seams_test.go -package=integration_test
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_continuity_test.go -package=integration_test github.com/candacelabs/csf/pkg/workcontinuity ISource
