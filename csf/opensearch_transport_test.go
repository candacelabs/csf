package csf

import (
	"io"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	mocks "github.com/candacelabs/csf/csf/internal/mocks"
)

var _ = Describe("OpenSearch response budget", func() {
	It("bounds reads before SDK decoding and closes the original body", func() {
		closed := make(chan struct{}, 1)
		upstream := mocks.NewMockIHTTPDoer(gomock.NewController(GinkgoT()))
		request, err := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
		Expect(err).NotTo(HaveOccurred())
		upstream.EXPECT().Do(request).Return(&http.Response{StatusCode: http.StatusOK, Body: &observedResponseBody{Reader: strings.NewReader(strings.Repeat(" ", maxSearchResponseBytes+1)), closed: closed}}, nil)
		_, err = (openSearchTransport{http: upstream}).RoundTrip(request)
		Expect(err).To(MatchError(ContainSubstring("search response exceeds limit")))
		Expect(closed).To(Receive())
	})
})

type observedResponseBody struct {
	io.Reader
	closed chan struct{}
}

func (body *observedResponseBody) Close() error {
	body.closed <- struct{}{}
	return nil
}
