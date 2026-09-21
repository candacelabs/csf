package sqlmigrate_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSQLMigrate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "sqlmigrate suite")
}
