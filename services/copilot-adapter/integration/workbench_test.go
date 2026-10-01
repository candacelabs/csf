package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/pkg/pgmem"
	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	"github.com/candacelabs/csf/services/copilot-adapter/store"
	"github.com/candacelabs/csf/services/copilot-adapter/workbench"
	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = Describe("Workbench composition in another host", func() {
	It("keeps sibling routes available but rejects adapter requests until durable restoration", func() {
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		Expect(store.ApplyMigrations(context.Background(), db)).To(Succeed())
		controller := gomock.NewController(GinkgoT())
		bridge := NewMockICopilotBridge(controller)
		terminal := NewMockITerminalManager(controller)
		terminal.EXPECT().Close().Return(nil)
		persistence, err := store.NewPostgresStore(db)
		Expect(err).NotTo(HaveOccurred())
		adapter, err := copilotadapter.NewCopilotAdapter(copilotadapter.WithBridge(bridge), copilotadapter.WithStore(persistence),
			copilotadapter.WithWorktreeManager(NewMockIWorktreeManager(controller)), copilotadapter.WithTerminalManager(terminal),
			copilotadapter.WithScheduleStore(cron.NewMemoryStore()))
		composed := &workbench.Workbench{Adapter: adapter, Store: persistence}
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(composed.Adapter.Close)
		router := httpserver.NewEngine("shared-host-test")
		router.GET("/mcp", func(ctx *gin.Context) { ctx.Status(http.StatusOK) })
		Expect(composed.Register(router)).To(Succeed())
		request := func(path string) *httptest.ResponseRecorder {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			return response
		}
		Expect(request("/mcp").Code).To(Equal(http.StatusOK))
		blocked := request("/v1/sessions")
		Expect(blocked.Code).To(Equal(http.StatusServiceUnavailable))
		Expect(blocked.Body.String()).To(ContainSubstring("workbench_restoring"))
		Expect(composed.Restore(context.Background())).To(Succeed())
		Expect(request("/v1/sessions").Code).To(Equal(http.StatusOK))
	})
})
