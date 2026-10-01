package contract_test

import (
	"testing"

	// A spec here loads America/Chicago by name. Go finds a named location in
	// the system time zone database, and the pinned Bazel container image has
	// none — it ships no /usr/share/zoneinfo at all. Importing time/tzdata
	// embeds the IANA database in this test binary as a fallback that is
	// consulted only when the system database is unavailable, so a developer's
	// machine still answers from the system copy and the container answers at
	// all.
	//
	// The previous arrangement copied lib/time/zoneinfo.zip out of the Go SDK
	// repository with a genrule and pointed $ZONEINFO at it. That required
	// MODULE.bazel to name the SDK repository, which rules_go permits only in
	// the root module — and this module is not the root module in the one place
	// it matters most, a consumer's build.
	_ "time/tzdata"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCronContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cron Contract Suite")
}
