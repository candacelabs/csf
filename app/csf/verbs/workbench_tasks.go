package verbs

import (
	"context"
	"io"
	"time"

	ionet "github.com/candacelabs/csf/io/net"
	"github.com/candacelabs/csf/io/net/github"
	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/pkg/telemetry"

	"github.com/candacelabs/csf/services/workcontinuity"
)

const (
	continuityLogService   = "csf"
	continuityLogComponent = "task-continuity"
	continuityTimeout      = 30 * time.Second
)

// The binary supplies credentials and the existing retained log stream; the
// task source reaches GitHub through the GitHub protocol client in this
// process, never a gh subprocess. With no token there is no task continuity:
// the Workbench runs without it.
func workbenchTasks(_ context.Context, token string, output io.Writer) (*workcontinuity.Continuity, error) {
	if token == "" {
		return nil, nil
	}
	httpClient, err := iohttp.NewHTTPClient(ionet.NewHostNetwork(), iohttp.WithClientTimeout(continuityTimeout))
	if err != nil {
		return nil, err
	}
	client, err := github.NewGitHubClient(httpClient, token)
	if err != nil {
		return nil, err
	}
	logger, err := telemetry.NewJSONLLogger(output, continuityLogService, continuityLogComponent)
	if err != nil {
		return nil, err
	}
	return workcontinuity.NewContinuity(workcontinuity.WithSource(workcontinuity.NewGitHubSource(client)), workcontinuity.WithLogger(logger))
}
