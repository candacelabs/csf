// Copyright 2026 Candace Labs

package store_test

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/db/csfpg/mocks"
	"github.com/candacelabs/csf/services/deploy/store"
)

var _ = Describe("the database capability", func() {
	var (
		ctx      context.Context
		database *mocks.MockIDB
	)

	BeforeEach(func() {
		ctx = context.Background()
		database = mocks.NewMockIDB(gomock.NewController(GinkgoT()))
	})

	It("requires a capability", func() {
		_, err := store.NewControlStore(ctx, nil)
		Expect(err).To(MatchError(ContainSubstring("a database capability is required")))
	})

	It("starts nothing on a database that does not answer", func() {
		unreachable := errors.New("connection refused")
		database.EXPECT().Ping(ctx).Return(unreachable)

		_, err := store.NewControlStore(ctx, database)

		Expect(err).To(MatchError(unreachable))
		Expect(err).To(MatchError(ContainSubstring("pinging Deploy database")))
	})

	It("migrates inside one transaction it opens on the borrowed database", func() {
		refused := errors.New("too many connections")
		database.EXPECT().Ping(ctx).Return(nil)
		database.EXPECT().Begin(ctx).Return(nil, refused)

		_, err := store.NewControlStore(ctx, database)

		Expect(err).To(MatchError(refused))
		Expect(err).To(MatchError(ContainSubstring("starting Deploy migrations")))
	})

	It("rolls the migration transaction back when the migration lock is refused", func() {
		refused := errors.New("lock timeout")
		upgrade := mocks.NewMockTx(gomock.NewController(GinkgoT()))
		database.EXPECT().Ping(ctx).Return(nil)
		database.EXPECT().Begin(ctx).Return(upgrade, nil)
		upgrade.EXPECT().Exec(ctx, gomock.Any()).Return(pgconn.CommandTag{}, refused)
		upgrade.EXPECT().Rollback(gomock.Any()).Return(nil)

		_, err := store.NewControlStore(ctx, database)

		Expect(err).To(MatchError(refused))
		Expect(err).To(MatchError(ContainSubstring("acquiring Deploy migration lock")))
	})

	It("reports a store without a database as closed", func() {
		var closed *store.Store
		Expect(closed.Ping(ctx)).To(MatchError(ContainSubstring("store is closed")))
		Expect((&store.Store{}).WithTx(ctx, nil)).To(MatchError(ContainSubstring("store is closed")))
	})
})
