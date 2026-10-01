package copilotbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("copied Copilot history", func() {
	It("returns typed metadata and events without entering a prompt path", func() {
		metadata := copilot.SessionMetadata{SessionID: "fixture-session", ModifiedTime: time.Unix(10, 0)}
		events := []copilot.SessionEvent{
			{ID: "user-1", Data: &rpc.UserMessageData{Content: "synthetic request"}},
			{ID: "assistant-1", Data: &rpc.AssistantMessageData{Content: "synthetic answer"}},
		}
		var listCalls, readCalls int
		bridge := &CopilotBridge{
			historySnapshotDirectory: "/tmp/disposable-copilot-history",
			listSessions: func(_ context.Context, _ *copilot.SessionListFilter) ([]copilot.SessionMetadata, error) {
				listCalls++
				return []copilot.SessionMetadata{metadata}, nil
			},
			readHistory: func(_ context.Context, observed copilot.SessionMetadata) ([]copilot.SessionEvent, error) {
				readCalls++
				Expect(observed.SessionID).To(Equal(metadata.SessionID))
				return events, nil
			},
		}

		snapshots, err := bridge.ReadHistory(context.Background(), &copilot.SessionListFilter{Repository: "synthetic/example"})

		Expect(err).NotTo(HaveOccurred())
		Expect(listCalls).To(Equal(1))
		Expect(readCalls).To(Equal(1))
		Expect(snapshots).To(HaveLen(1))
		Expect(snapshots[0].Metadata).To(Equal(metadata))
		Expect(snapshots[0].Events).To(Equal(events))
		Expect(snapshots[0].Events[0].Data).To(BeAssignableToTypeOf(&rpc.UserMessageData{}))
		Expect(snapshots[0].Events[1].Data).To(BeAssignableToTypeOf(&rpc.AssistantMessageData{}))
	})

	It("requires an isolated native data root before discovering sessions", func() {
		var listCalls int
		bridge := &CopilotBridge{
			listSessions: func(_ context.Context, _ *copilot.SessionListFilter) ([]copilot.SessionMetadata, error) {
				listCalls++
				return nil, nil
			},
		}

		_, err := bridge.ReadHistory(context.Background(), nil)

		Expect(errors.Is(err, errHistorySnapshotRequired)).To(BeTrue())
		Expect(listCalls).To(BeZero())
	})

	It("lists typed session metadata without resuming sessions", func() {
		metadata := copilot.SessionMetadata{SessionID: "metadata-only"}
		bridge := &CopilotBridge{
			historySnapshotDirectory: "/tmp/disposable-copilot-history",
			listSessions: func(_ context.Context, filter *copilot.SessionListFilter) ([]copilot.SessionMetadata, error) {
				Expect(filter.Repository).To(Equal("synthetic/example"))
				return []copilot.SessionMetadata{metadata}, nil
			},
			readHistory: func(_ context.Context, _ copilot.SessionMetadata) ([]copilot.SessionEvent, error) {
				Fail("metadata listing must not resume or read session events")
				return nil, nil
			},
		}

		listed, err := bridge.ListHistorySessions(context.Background(), &copilot.SessionListFilter{Repository: "synthetic/example"})

		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(Equal([]copilot.SessionMetadata{metadata}))
	})

	It("configures copied-session reads without prompt or tool callbacks", func() {
		configuration := historyResumeConfig()

		Expect(configuration.SuppressResumeEvent).To(BeTrue())
		Expect(configuration.ContinuePendingWork).NotTo(BeNil())
		Expect(*configuration.ContinuePendingWork).To(BeFalse())
		Expect(configuration.AvailableTools).To(BeEmpty())
		Expect(configuration.Tools).To(BeEmpty())
		Expect(configuration.OnEvent).To(BeNil())
		Expect(configuration.OnPermissionRequest).To(BeNil())
		Expect(configuration.SkipCustomInstructions).NotTo(BeNil())
		Expect(*configuration.SkipCustomInstructions).To(BeTrue())
		Expect(configuration.EnableConfigDiscovery).NotTo(BeNil())
		Expect(*configuration.EnableConfigDiscovery).To(BeFalse())
		Expect(configuration.EnableFileHooks).NotTo(BeNil())
		Expect(*configuration.EnableFileHooks).To(BeFalse())
		Expect(configuration.EnableHostGitOperations).NotTo(BeNil())
		Expect(*configuration.EnableHostGitOperations).To(BeFalse())
		Expect(configuration.EnableSkills).NotTo(BeNil())
		Expect(*configuration.EnableSkills).To(BeFalse())
	})

	It("copies a source tree into a disposable home without mutating the source", func() {
		source := GinkgoT().TempDir()
		sourceFile := filepath.Join(source, "sessions", "fixture.jsonl")
		Expect(os.MkdirAll(filepath.Dir(sourceFile), 0o700)).To(Succeed())
		sourceContents := []byte("synthetic native history\n")
		Expect(os.WriteFile(sourceFile, sourceContents, 0o600)).To(Succeed())

		destination, cleanup, err := prepareHistoryHome(context.Background(), source)

		Expect(err).NotTo(HaveOccurred())
		Expect(destination).NotTo(Equal(source))
		copied, readErr := os.ReadFile(filepath.Join(destination, "sessions", "fixture.jsonl"))
		Expect(readErr).NotTo(HaveOccurred())
		Expect(copied).To(Equal(sourceContents))
		Expect(os.WriteFile(filepath.Join(destination, "sessions", "fixture.jsonl"), []byte("changed copy\n"), 0o600)).To(Succeed())
		unchanged, readErr := os.ReadFile(sourceFile)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(unchanged).To(Equal(sourceContents))
		Expect(cleanup()).To(Succeed())
		_, statErr := os.Stat(destination)
		Expect(os.IsNotExist(statErr)).To(BeTrue())
	})

	It("rejects a symlinked history source", func() {
		parent := GinkgoT().TempDir()
		source := filepath.Join(parent, "real-home")
		Expect(os.Mkdir(source, 0o700)).To(Succeed())
		link := filepath.Join(parent, "linked-home")
		Expect(os.Symlink(source, link)).To(Succeed())

		_, _, err := prepareHistoryHome(context.Background(), link)

		Expect(err).To(MatchError(ContainSubstring("not a symlink")))
	})

	It("rejects aliases inside the history source", func() {
		parent := GinkgoT().TempDir()
		source := filepath.Join(parent, "real-home")
		outside := filepath.Join(parent, "outside.jsonl")
		Expect(os.Mkdir(source, 0o700)).To(Succeed())
		Expect(os.WriteFile(outside, []byte("outside"), 0o600)).To(Succeed())
		Expect(os.Symlink(outside, filepath.Join(source, "session.jsonl"))).To(Succeed())

		_, _, err := prepareHistoryHome(context.Background(), source)

		Expect(err).To(MatchError(ContainSubstring("source contains symlink")))
	})

	It("honors cancellation before copying a history source", func() {
		source := GinkgoT().TempDir()
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()

		_, _, err := prepareHistoryHome(cancelled, source)

		Expect(err).To(MatchError(ContainSubstring("context canceled")))
	})

	It("removes the wrapper-owned home when the bridge closes", func() {
		home := GinkgoT().TempDir()
		marker := filepath.Join(home, "marker")
		Expect(os.WriteFile(marker, []byte("temporary"), 0o600)).To(Succeed())
		bridge := &CopilotBridge{
			forceStop:                func() {},
			shutdownTimeout:          time.Second,
			disconnects:              newDisconnectTracker(),
			historySnapshotDirectory: home,
			cleanupHistory:           func() error { return os.RemoveAll(home) },
		}

		Expect(bridge.Close()).To(Succeed())
		_, err := os.Stat(home)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("exercises the SDK resume, typed event read, and disconnect over a fixture runtime", func() {
		runtime := newHistoryFixtureRuntime()
		DeferCleanup(runtime.close)
		client := copilot.NewClient(&copilot.ClientOptions{
			Connection: copilot.URIConnection{URL: runtime.listener.Addr().String()},
			Mode:       copilot.ModeEmpty,
		})
		DeferCleanup(func() {
			client.ForceStop()
			_ = client.Stop()
		})
		Expect(client.Start(context.Background())).To(Succeed())
		bridge := &CopilotBridge{
			resumeSession:   client.ResumeSession,
			disconnects:     newDisconnectTracker(),
			shutdownTimeout: time.Second,
		}

		events, err := bridge.readHistoryFromSDK(context.Background(), copilot.SessionMetadata{SessionID: "fixture-session"})

		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(2))
		Expect(events[0].Data).To(BeAssignableToTypeOf(&rpc.UserMessageData{}))
		Expect(events[1].Data).To(BeAssignableToTypeOf(&rpc.AssistantMessageData{}))
		requests := runtime.recordedRequests()
		Expect(requests).To(ContainElement("connect"))
		Expect(requests).To(ContainElement("session.resume"))
		Expect(requests).To(ContainElement("session.getMessages"))
		Expect(requests).To(ContainElement("session.destroy"))
		Expect(requests).NotTo(ContainElement("session.send"))
		Expect(requests).NotTo(ContainElement("tool"))
		resume := runtime.resumeParams()
		Expect(resume["disableResume"]).To(BeTrue())
		Expect(resume["continuePendingWork"]).To(BeFalse())
		Expect(resume["availableTools"]).To(Equal([]any{}))
		Expect(resume["skipCustomInstructions"]).To(BeTrue())
	})

	It("reads one explicitly selected session through typed metadata", func() {
		metadata := copilot.SessionMetadata{SessionID: "selected-session"}
		bridge := &CopilotBridge{
			historySnapshotDirectory: "/tmp/disposable-copilot-history",
			getSessionMetadata: func(_ context.Context, sessionID string) (*copilot.SessionMetadata, error) {
				Expect(sessionID).To(Equal(metadata.SessionID))
				return &metadata, nil
			},
			readHistory: func(_ context.Context, observed copilot.SessionMetadata) ([]copilot.SessionEvent, error) {
				Expect(observed).To(Equal(metadata))
				return []copilot.SessionEvent{{ID: "event-1"}}, nil
			},
		}

		snapshot, err := bridge.ReadSessionHistory(context.Background(), metadata.SessionID)

		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Metadata).To(Equal(metadata))
		Expect(snapshot.Events).To(HaveLen(1))
	})
})

type historyFixtureRequest struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

type historyFixtureRuntime struct {
	listener net.Listener
	done     chan struct{}
	mu       sync.Mutex
	requests []historyFixtureRequest
	serveErr error
}

func newHistoryFixtureRuntime() *historyFixtureRuntime {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	runtime := &historyFixtureRuntime{listener: listener, done: make(chan struct{})}
	go runtime.serve()
	return runtime
}

func (runtime *historyFixtureRuntime) serve() {
	defer close(runtime.done)
	connection, err := runtime.listener.Accept()
	if err != nil {
		runtime.serveErr = err
		return
	}
	defer connection.Close()
	reader := bufio.NewReader(connection)
	for {
		request, readErr := readHistoryFixtureRequest(reader)
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				runtime.serveErr = readErr
			}
			return
		}
		runtime.mu.Lock()
		runtime.requests = append(runtime.requests, request)
		runtime.mu.Unlock()
		result := historyFixtureResult(request.Method)
		if writeHistoryFixtureResponse(connection, request.ID, result) != nil {
			return
		}
	}
}

func (runtime *historyFixtureRuntime) close() {
	_ = runtime.listener.Close()
	<-runtime.done
	Expect(runtime.serveErr).NotTo(HaveOccurred())
}

func (runtime *historyFixtureRuntime) recordedRequests() []string {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	methods := make([]string, 0, len(runtime.requests))
	for _, request := range runtime.requests {
		methods = append(methods, request.Method)
	}
	return methods
}

func (runtime *historyFixtureRuntime) resumeParams() map[string]any {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	for _, request := range runtime.requests {
		if request.Method != "session.resume" {
			continue
		}
		var params map[string]any
		Expect(json.Unmarshal(request.Params, &params)).To(Succeed())
		return params
	}
	return nil
}

func readHistoryFixtureRequest(reader *bufio.Reader) (historyFixtureRequest, error) {
	var length int
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return historyFixtureRequest{}, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if _, err := fmt.Sscanf(line, "Content-Length: %d", &length); err != nil {
			return historyFixtureRequest{}, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return historyFixtureRequest{}, err
	}
	var request historyFixtureRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return historyFixtureRequest{}, err
	}
	return request, nil
}

func historyFixtureResult(method string) map[string]any {
	switch method {
	case "connect":
		return map[string]any{"protocolVersion": 3}
	case "session.resume":
		return map[string]any{"sessionId": "fixture-session", "workspacePath": "/tmp/fixture"}
	case "session.getMessages":
		return map[string]any{"events": []map[string]any{
			{"id": "user-1", "type": "user.message", "data": map[string]any{"content": "synthetic request"}},
			{"id": "assistant-1", "type": "assistant.message", "data": map[string]any{"content": "synthetic answer"}},
		}}
	default:
		return map[string]any{}
	}
}

func writeHistoryFixtureResponse(writer io.Writer, id json.RawMessage, result map[string]any) error {
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
	return err
}
