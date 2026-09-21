// This consumer owns the process, listener and custom routes. CSF supplies
// in-process capabilities and generated HTTP/MCP operations.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/httpserver"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/gin-gonic/gin"
)

const (
	applicationName = "csf-consumer"
	listenFlag      = "listen"
	themeFlag       = "theme-dir"
	eventsFlag      = "events"
	defaultListen   = "127.0.0.1:8089"
	defaultTheme    = "."
	mcpPath         = "/mcp"
	summaryPath     = "/consumer/snapshot"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s: %v\n", applicationName, err)
		os.Exit(1)
	}
}

func run() error {
	address := flag.String(listenFlag, defaultListen, "HTTP listen address")
	themeDirectory := flag.String(themeFlag, defaultTheme, "directory containing workbench-theme.css")
	eventPath := flag.String(eventsFlag, "", "optional append-only research event file")
	flag.Parse()

	router, err := newConsumerRouter(*themeDirectory, *eventPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return httpserver.Serve(ctx, httpserver.NewStreamingServer(*address, router))
}

func newConsumerRouter(themeDirectory string, eventPath string) (*gin.Engine, error) {
	options := []csf.Option{csf.WithWorkbenchThemeDirectory(themeDirectory)}
	if eventPath != "" {
		options = append(options, csf.WithDashboard(csf.NewDashboard(eventPath)))
	}
	service, err := csf.New(options...)
	if err != nil {
		return nil, fmt.Errorf("create CSF: %w", err)
	}
	router := httpserver.NewEngine(applicationName)
	service.Register(router)
	router.Any(mcpPath, gin.WrapH(service.MCPHandler()))
	registerConsumerSummary(router, service)
	return router, nil
}

// The consumer adds behavior without changing CSF or duplicating its wire types.
func registerConsumerSummary(router gin.IRouter, service *csf.Service) {
	router.GET(summaryPath, func(ctx *gin.Context) {
		response, err := service.GetSnapshot(ctx.Request.Context(), &pb.GetSnapshotRequest{})
		if err != nil {
			ctx.String(http.StatusInternalServerError, "read snapshot: %v", err)
			return
		}
		snapshot := response.GetSnapshot()
		ctx.String(http.StatusOK, "Events: %d\nTruncated: %t\nIssues: %s\n",
			len(snapshot.GetEvents()), snapshot.GetTruncated(), strings.Join(snapshot.GetIssues(), "; "))
	})
}
