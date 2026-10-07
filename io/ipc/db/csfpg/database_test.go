// Copyright 2026 Candace Labs

package csfpg_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	ionet "github.com/candacelabs/csf/io/net"
	"github.com/candacelabs/csf/runtime/config"
)

// Both the pool a binary opens and the upstream pool it wraps are the
// capability a service receives.
var (
	_ csfpg.IDB = (*csfpg.Pool)(nil)
	_ csfpg.IDB = (*pgxpool.Pool)(nil)
)

var _ = Describe("PostgreSQL capability", func() {
	const databaseVariable = "APP_DATABASE_URL"

	It("reads its settings from the config capability under the name the binary declares", func() {
		environment := config.NewEnvironment(func(name string) (string, bool) {
			if name == databaseVariable {
				return " postgres://app@db.example.invalid/app ", true
			}
			return "", false
		})

		settings, err := csfpg.SettingsFromEnvironment(environment, databaseVariable)

		Expect(err).NotTo(HaveOccurred())
		Expect(settings.URL).To(Equal("postgres://app@db.example.invalid/app"))
		_, err = csfpg.SettingsFromEnvironment(config.NewEnvironment(nil), databaseVariable)
		Expect(errors.Is(err, csfpg.ErrMissingURL)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring(databaseVariable)))
	})

	It("opens nothing without a URL", func() {
		_, err := csfpg.OpenPool(context.Background(), csfpg.Settings{})
		Expect(err).To(MatchError(csfpg.ErrMissingURL))
	})

	It("rejects a malformed URL without repeating it, since it may carry a password", func() {
		_, err := csfpg.OpenPool(context.Background(), csfpg.Settings{URL: "postgres://app:hunter2@db.example.invalid:port/app"})
		Expect(err).To(MatchError(ContainSubstring("invalid database URL")))
		Expect(err.Error()).NotTo(ContainSubstring("hunter2"))
	})

	It("returns no pool for a database that does not answer", func() {
		ctx := context.Background()
		// Reserve a loopback port through the socket capability, then free it
		// so nothing listens there.
		listener, err := ionet.NewHostNetwork().Listen(ctx, "tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		address := listener.Addr().String()
		Expect(listener.Close()).To(Succeed())

		pool, err := csfpg.OpenPool(ctx, csfpg.Settings{
			URL: fmt.Sprintf("postgres://app@%s/app?sslmode=disable&connect_timeout=5", address),
		})

		Expect(pool).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("database did not answer")))
	})
})
