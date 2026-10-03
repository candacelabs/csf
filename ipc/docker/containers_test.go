// Copyright 2026 Candace Labs

package docker_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/ipc/docker"
)

const (
	sandboxImage = "example.invalid/evaluator:1"
	containerID  = "c0ffee"
)

// multiplexed is a Docker log stream carrying both outputs, framed the way the
// Engine frames a non-TTY container's logs: an eight-byte header naming the
// stream and the big-endian payload length, then the payload.
func multiplexed(stdout string, stderr string) io.ReadCloser {
	var stream bytes.Buffer
	for _, frame := range []struct {
		kind    stdcopy.StdType
		payload string
	}{{stdcopy.Stdout, stdout}, {stdcopy.Stderr, stderr}} {
		if frame.payload == "" {
			continue
		}
		header := make([]byte, 8)
		header[0] = byte(frame.kind)
		binary.BigEndian.PutUint32(header[4:], uint32(len(frame.payload)))
		stream.Write(header)
		stream.WriteString(frame.payload)
	}
	return io.NopCloser(&stream)
}

// exited is a wait result that has already reported status.
func exited(status int64) client.ContainerWaitResult {
	results := make(chan container.WaitResponse, 1)
	results <- container.WaitResponse{StatusCode: status}
	return client.ContainerWaitResult{Result: results, Error: make(chan error)}
}

func newHost(api docker.IContainerAPI, options ...docker.ContainerHostOption) *docker.ContainerHost {
	GinkgoHelper()
	host, err := docker.NewContainerHost(append([]docker.ContainerHostOption{docker.WithContainerAPI(api)}, options...)...)
	Expect(err).NotTo(HaveOccurred())
	return host
}

var _ = Describe("ContainerHost.RunSandboxed", func() {
	var (
		api      *MockIContainerAPI
		baseline goleak.Option
	)

	BeforeEach(func() {
		baseline = goleak.IgnoreCurrent()
		api = NewMockIContainerAPI(gomock.NewController(GinkgoT()))
	})
	AfterEach(func() { Expect(goleak.Find(baseline)).To(Succeed()) })

	It("creates an isolated container, runs it to completion, collects both outputs and removes it", func(ctx SpecContext) {
		var created client.ContainerCreateOptions
		gomock.InOrder(
			api.EXPECT().ContainerCreate(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
					created = options
					return client.ContainerCreateResult{ID: containerID}, nil
				}),
			api.EXPECT().ContainerWait(gomock.Any(), containerID, client.ContainerWaitOptions{Condition: container.WaitConditionNextExit}).Return(exited(0)),
			api.EXPECT().ContainerStart(gomock.Any(), containerID, gomock.Any()).Return(client.ContainerStartResult{}, nil),
			api.EXPECT().ContainerLogs(gomock.Any(), containerID, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true}).
				Return(multiplexed("verdict: pass\n", "2 warnings\n"), nil),
			api.EXPECT().ContainerRemove(gomock.Any(), containerID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}).
				Return(client.ContainerRemoveResult{}, nil),
		)

		result, err := newHost(api).RunSandboxed(ctx, docker.SandboxSpec{
			Image: sandboxImage, Command: []string{"evaluate"},
			Mounts: []docker.Mount{{Source: "/srv/candidate", Target: "/work", ReadOnly: true}},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(docker.SandboxResult{
			ContainerID: containerID, ExitCode: 0, Stdout: []byte("verdict: pass\n"), Stderr: []byte("2 warnings\n"),
		}))
		Expect(created.Name).To(HavePrefix("csf-sandbox-"))
		Expect(created.Config.Image).To(Equal(sandboxImage))
		Expect(created.Config.Cmd).To(Equal([]string{"evaluate"}))
		hostConfig := created.HostConfig
		Expect(string(hostConfig.NetworkMode)).To(Equal("none"), "a sandbox has no network unless asked")
		Expect(hostConfig.CapDrop).To(Equal([]string{"ALL"}))
		Expect(hostConfig.SecurityOpt).To(Equal([]string{"no-new-privileges"}))
		Expect(hostConfig.ReadonlyRootfs).To(BeTrue())
		Expect(hostConfig.Tmpfs).To(HaveKey("/tmp"))
		Expect(*hostConfig.Init).To(BeTrue())
		Expect(*hostConfig.PidsLimit).To(Equal(int64(docker.DefaultPidsLimit)))
		Expect(hostConfig.Mounts).To(HaveLen(1))
		Expect(hostConfig.Mounts[0].ReadOnly).To(BeTrue())
	})

	It("reports an unsuccessful exit with its output, and still removes the container", func(ctx SpecContext) {
		api.EXPECT().ContainerCreate(gomock.Any(), gomock.Any()).Return(client.ContainerCreateResult{ID: containerID}, nil)
		api.EXPECT().ContainerWait(gomock.Any(), containerID, gomock.Any()).Return(exited(3))
		api.EXPECT().ContainerStart(gomock.Any(), containerID, gomock.Any()).Return(client.ContainerStartResult{}, nil)
		api.EXPECT().ContainerLogs(gomock.Any(), containerID, gomock.Any()).Return(multiplexed("", "assertion failed\n"), nil)
		api.EXPECT().ContainerRemove(gomock.Any(), containerID, gomock.Any()).Return(client.ContainerRemoveResult{}, nil)

		result, err := newHost(api).RunSandboxed(ctx, docker.SandboxSpec{Image: sandboxImage, Network: "bridge", WritableRoot: true})

		var exitError *docker.ExitError
		Expect(errors.As(err, &exitError)).To(BeTrue(), "got %v", err)
		Expect(exitError.Code).To(Equal(int64(3)))
		Expect(string(result.Stderr)).To(Equal("assertion failed\n"))
	})

	It("removes a canceled sandbox with a context that outlives the cancellation", func(ctx SpecContext) {
		runContext, cancel := context.WithCancel(ctx)
		waitErrors := make(chan error, 1)
		api.EXPECT().ContainerCreate(gomock.Any(), gomock.Any()).Return(client.ContainerCreateResult{ID: containerID}, nil)
		api.EXPECT().ContainerWait(gomock.Any(), containerID, gomock.Any()).
			Return(client.ContainerWaitResult{Result: make(chan container.WaitResponse), Error: waitErrors})
		api.EXPECT().ContainerStart(gomock.Any(), containerID, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, _ client.ContainerStartOptions) (client.ContainerStartResult, error) {
				// The caller gives up while the sandbox runs; the Engine
				// client reports that on its wait error channel.
				cancel()
				waitErrors <- context.Canceled
				return client.ContainerStartResult{}, nil
			})
		api.EXPECT().ContainerRemove(gomock.Any(), containerID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}).DoAndReturn(
			func(cleanup context.Context, _ string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
				Expect(cleanup.Err()).NotTo(HaveOccurred(), "cleanup must not inherit the caller's cancellation")
				return client.ContainerRemoveResult{}, nil
			})

		_, err := newHost(api).RunSandboxed(runContext, docker.SandboxSpec{Image: sandboxImage})

		Expect(err).To(MatchError(context.Canceled))
	})

	It("removes a container that failed to start and joins both errors", func(ctx SpecContext) {
		refused, gone := errors.New("no such image"), errors.New("already gone")
		api.EXPECT().ContainerCreate(gomock.Any(), gomock.Any()).Return(client.ContainerCreateResult{ID: containerID}, nil)
		api.EXPECT().ContainerWait(gomock.Any(), containerID, gomock.Any()).Return(exited(0))
		api.EXPECT().ContainerStart(gomock.Any(), containerID, gomock.Any()).Return(client.ContainerStartResult{}, refused)
		api.EXPECT().ContainerRemove(gomock.Any(), containerID, gomock.Any()).Return(client.ContainerRemoveResult{}, gone)

		_, err := newHost(api).RunSandboxed(ctx, docker.SandboxSpec{Image: sandboxImage})

		Expect(err).To(MatchError(refused))
		Expect(err).To(MatchError(gone))
	})

	It("bounds each captured stream", func(ctx SpecContext) {
		api.EXPECT().ContainerCreate(gomock.Any(), gomock.Any()).Return(client.ContainerCreateResult{ID: containerID}, nil)
		api.EXPECT().ContainerWait(gomock.Any(), containerID, gomock.Any()).Return(exited(0))
		api.EXPECT().ContainerStart(gomock.Any(), containerID, gomock.Any()).Return(client.ContainerStartResult{}, nil)
		api.EXPECT().ContainerLogs(gomock.Any(), containerID, gomock.Any()).Return(multiplexed("abcdefgh", ""), nil)
		api.EXPECT().ContainerRemove(gomock.Any(), containerID, gomock.Any()).Return(client.ContainerRemoveResult{}, nil)

		result, err := newHost(api, docker.WithOutputBytes(4)).RunSandboxed(ctx, docker.SandboxSpec{Image: sandboxImage})

		Expect(err).NotTo(HaveOccurred())
		Expect(string(result.Stdout)).To(Equal("abcd"))
		Expect(result.StdoutTruncated).To(BeTrue())
	})

	It("refuses a spec without an image before touching the Engine", func(ctx SpecContext) {
		_, err := newHost(api).RunSandboxed(ctx, docker.SandboxSpec{})
		Expect(err).To(MatchError(docker.ErrImageRequired))
	})
})

var _ = Describe("NewContainerHost", func() {
	It("validates its options and connects lazily", func() {
		_, err := docker.NewContainerHost(nil)
		Expect(err).To(MatchError(docker.ErrInvalidOption))
		_, err = docker.NewContainerHost(docker.WithOutputBytes(0))
		Expect(err).To(MatchError(docker.ErrInvalidOption))
		_, err = docker.NewContainerHost(docker.WithContainerAPI(nil))
		Expect(err).To(MatchError(docker.ErrInvalidOption))
		api := NewMockIContainerAPI(gomock.NewController(GinkgoT()))
		_, err = docker.NewContainerHost(docker.WithContainerAPI(api), docker.WithDockerHost("unix:///run/none.sock"))
		Expect(err).To(MatchError(docker.ErrInvalidOption))

		host, err := docker.NewContainerHost(docker.WithDockerHost("unix:///run/csf-no-such-engine.sock"))
		Expect(err).NotTo(HaveOccurred(), "construction does not contact the Engine")
		Expect(host.Close()).To(Succeed())
	})
})
