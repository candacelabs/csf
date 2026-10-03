# 041 — acceptance judgment

Tier 2. Judged against `rubric.md`.

---

The house rule CS-18 says unit tests live in the package, integration tests are
external `_test` packages that drive the exported API with gomock mocks of
exported interfaces, and tests make no real crossings. The new
`CS-18-CROSSING` locator is advisory. Someone has opened a change titled
"CS-18: zero the crossing report for package `ledger`", touching the three
files below. Say what you would do with each file, and why.

**File 1** — `candace/services/ledger/ledger_test.go`

```go
package ledger_test

import (
	"github.com/candacelabs/csf/pkg/pgmem"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/services/ledger"
)

var _ = Describe("the ledger", func() {
	It("rejects Record after Close with ErrClosed", func(ctx SpecContext) {
		notifier := NewMockINotifier(gomock.NewController(GinkgoT()))
		book := ledger.NewPostgresLedger(pgmem.MustNew(), notifier)
		Expect(book.Close()).To(Succeed())
		Expect(book.Record(ctx, ledger.Entry{Amount: 5})).To(MatchError(ledger.ErrClosed))
	})
})
```

The change replaces `pgmem.MustNew()` with a gomock `MockIDB`, "because pgmem
is a database and CS-18 says tests make no real database crossings".

**File 2** — `candace/services/ledger/migrations_postgres_test.go`

```go
//go:build integration

package ledger

// Applies every migration to a disposable PostgreSQL 17 started by the CI job,
// then checks the partial unique index rejects a duplicate open entry. pgmem
// does not implement partial indexes.
func TestMigrationsAgainstPostgres(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), os.Getenv("LEDGER_POSTGRES_URL"))
	// ... apply migrations, insert twice, expect a unique_violation ...
}
```

The change deletes this file, "because it opens a real pool".

**File 3** — `candace/services/ledger/webhook_test.go`

```go
package ledger_test

var _ = Describe("the webhook notifier", func() {
	It("posts the entry", func(ctx SpecContext) {
		received := make(chan ledger.Entry, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			received <- decode(r)
		}))
		DeferCleanup(server.Close)
		Expect(ledger.NewWebhookNotifier(server.URL).Notify(ctx, ledger.Entry{Amount: 5})).To(Succeed())
		Eventually(received).Should(Receive(Equal(ledger.Entry{Amount: 5})))
	})
})
```

The change adds `//go:build acceptance` to the top of this file, "so the
locator stops reporting it".
