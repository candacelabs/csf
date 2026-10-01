package postgres_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPostgresStore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cron PostgreSQL Store Integration Suite")
}
