package email

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=contracts.go -destination=mock_contracts_test.go -package=email
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=smtp.go -destination=mock_smtp_client_test.go -package=email -mock_names=iSMTPClient=MockSMTPClient
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_write_closer_test.go -package=email -mock_names=WriteCloser=MockWriteCloser io WriteCloser
