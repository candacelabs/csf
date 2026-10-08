// Copyright 2026 Candace Labs

// Command csf is the one application (#600). This file is the process and
// nothing else: its signals, streams and exit status. Every verb, and every
// spec of one, lives in app/csf/verbs.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/candacelabs/csf/app/csf/verbs"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := verbs.Main(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
