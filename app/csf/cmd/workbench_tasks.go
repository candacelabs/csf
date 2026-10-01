package main

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/candacelabs/csf/pkg/telemetry"
	"golang.org/x/oauth2"

	"github.com/candacelabs/csf/pkg/workcontinuity"
)

const (
	continuityLogService   = "csf"
	continuityLogComponent = "task-continuity"
)

// The binary supplies credentials and the existing retained log stream; the
// task source performs HTTP requests in this process, never a gh subprocess.
func workbenchTasks(ctx context.Context, token string, output io.Writer) (*workcontinuity.Continuity, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	if token != "" {
		client = oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token}))
		client.Timeout = 30 * time.Second
	}
	source, err := workcontinuity.NewHTTPGitHubSource(client)
	if err != nil {
		return nil, err
	}
	logger, err := telemetry.NewJSONLLogger(output, continuityLogService, continuityLogComponent)
	if err != nil {
		return nil, err
	}
	return workcontinuity.NewContinuity(workcontinuity.WithSource(source), workcontinuity.WithLogger(logger))
}
