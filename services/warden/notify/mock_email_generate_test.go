package notify

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_email_test.go -package=notify github.com/candacelabs/csf/services/email ITransport,IProvenanceSource,IReceiptSink
