package copilotadapter_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=seams.go -aux_files=github.com/candacelabs/csf/services/copilot-adapter/storedb=storedb/querier.go -destination=mock_seams_test.go -package=copilotadapter_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_trace_client_test.go -package=copilotadapter_test go.opentelemetry.io/otel/exporters/otlp/otlptrace Client
