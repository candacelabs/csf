# 033 — keep-qualified judgment

Tier 2. Judged against `rubric.md`.

---

The house convention (CS-11) is that test files dot-import ginkgo/v2 and gomega,
so specs read `Describe`/`It`/`Expect`/`Eventually` unqualified. Someone is
rolling the convention out and has opened a change that dot-imports both
packages in **every** file that imports them, across the repository. Two of the
files it touches are below. Say what you would do with the assertion import in
each, and why.

**File 1** — `candace/pkg/eventually/eventually.go`

```go
package eventually

import (
	"time"

	"github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"
)

// Await polls until match accepts a value, and returns that value. It is the
// one typed shell this repository's tests wait through; gomega is its engine.
func Await[Value any](
	reporter IReporter,
	what string,
	budget Budget,
	poll func() Value,
	match func(value Value) bool,
) Value {
	assertion := gomega.NewWithT(reporter).Eventually(poll)
	// ... WithTimeout / WithPolling / Should(gomega.Satisfy(match)) ...
	return zero[Value]()
}

// Consistently is the negative-space twin, for asserting an absence.
func Consistently[Value any](reporter IReporter, what string, budget Budget, poll func() Value, match func(value Value) bool) Value {
	// ... gomega.NewWithT(reporter).Consistently(poll) ...
	return zero[Value]()
}
```

The change rewrites File 1 to `. "github.com/onsi/gomega"` and drops every
`gomega.` qualifier.

**File 2** — `candace/services/warden/watchdog/run_test.go`

```go
package watchdog

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

func TestWatchdog(t *testing.T) {
	gomega.RegisterFailHandler(Fail)
	RunSpecs(t, "watchdog suite")
}

var _ = Describe("the watchdog", func() {
	It("restarts a dead worker", func() {
		gomega.Expect(restarts()).To(gomega.Equal(1))
	})
})
```

The change rewrites File 2 to `. "github.com/onsi/gomega"` and drops every
`gomega.` qualifier.
