package widgettest_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWidgetTest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/widget/widgettest")
}
