package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/gin-gonic/gin"
)

const (
	applicationName       = "csf-theme-example"
	defaultListenAddress  = "127.0.0.1:8089"
	defaultThemeDirectory = "."
	mcpPath               = "/mcp"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s: %v\n", applicationName, err)
		os.Exit(1)
	}
}

func run() error {
	listenAddress := flag.String("listen", defaultListenAddress, "HTTP listen address")
	themeDirectory := flag.String("theme-dir", defaultThemeDirectory, "directory containing the host-owned Workbench CSS file")
	flag.Parse()

	service, err := csf.New(csf.WithWorkbenchThemeDirectory(*themeDirectory))
	if err != nil {
		return fmt.Errorf("create CSF service: %w", err)
	}
	router := httpserver.NewEngine(applicationName, httpserver.WithRequestLogging())
	service.Register(router)
	router.Any(mcpPath, gin.WrapH(service.MCPHandler()))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := httpserver.NewStreamingServer(*listenAddress, router)
	return httpserver.Serve(ctx, server)
}
