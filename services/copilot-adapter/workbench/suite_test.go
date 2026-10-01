package workbench

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestWorkbench(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Shared Workbench") }
