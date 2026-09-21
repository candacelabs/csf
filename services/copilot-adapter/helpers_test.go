package copilotadapter_test

import (
	"io"
	"strings"
)

func bytesReader(body string) io.Reader {
	return strings.NewReader(body)
}
