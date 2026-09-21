package httpapi_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/app/candaceos-agent/internal/agent"
	"github.com/candacelabs/csf/app/candaceos-agent/internal/httpapi"
	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestHTTPAPI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "candaceos-agent HTTP API suite")
}

func removeSealedHTTPTestTree(root string) error {
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.RemoveAll(root)
}

var _ = Describe("API", func() {
	var handler http.Handler
	var sourceRevision, sourceDigest string

	BeforeEach(func() {
		workspace := GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(workspace, "notes"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workspace, "notes", "compose.yaml"), []byte("services: {}\n"), 0o600)).To(Succeed())
		runHTTPGit(workspace, "init", "-q", "-b", "main")
		runHTTPGit(workspace, "config", "user.name", "CandaceOS Test")
		runHTTPGit(workspace, "config", "user.email", "candaceos-test@example.invalid")
		runHTTPGit(workspace, "add", ".")
		runHTTPGit(workspace, "commit", "-q", "-m", "test: app source")
		sourceRevision = runHTTPGit(workspace, "rev-parse", "HEAD")
		var err error
		sourceDigest, err = candaceos.DigestAppSource(context.Background(), filepath.Join(workspace, "notes"))
		Expect(err).NotTo(HaveOccurred())
		dockerBin := filepath.Join(GinkgoT().TempDir(), "docker")
		Expect(os.WriteFile(dockerBin, []byte("#!/bin/sh\nexit 0\n"), 0o700)).To(Succeed())
		revisionRoot := GinkgoT().TempDir()
		DeferCleanup(removeSealedHTTPTestTree, revisionRoot)
		runner, err := agent.NewDockerComposeRunner(
			dockerBin, workspace, revisionRoot,
			&candaceosv1.RevisionLimits{MaxEntries: 16, MaxBytes: 1 << 20}, true,
		)
		Expect(err).NotTo(HaveOccurred())
		reconciler, err := agent.NewReconciler(&agent.MemoryStore{}, runner)
		Expect(err).NotTo(HaveOccurred())
		handler = httpapi.New("node-a", "test-token", reconciler)
	})

	It("authenticates and emits protobuf health and empty-status presence", func() {
		for _, path := range []string{"/healthz", "/v1/status"} {
			response := serve(handler, httptest.NewRequest(http.MethodGet, path, nil), false)
			Expect(response.Code).To(Equal(http.StatusUnauthorized))
		}

		healthResponse := serve(handler, httptest.NewRequest(http.MethodGet, "/healthz", nil), true)
		Expect(healthResponse.Code).To(Equal(http.StatusOK))
		Expect(healthResponse.Header().Get("Content-Type")).To(Equal("application/json"))
		Expect(healthResponse.Body.String()).To(ContainSubstring(`"node_id":"node-a"`))
		Expect(healthResponse.Body.String()).NotTo(ContainSubstring("nodeId"))
		var health candaceosv1.HealthResponse
		Expect(protojson.Unmarshal(healthResponse.Body.Bytes(), &health)).To(Succeed())
		Expect(health.GetStatus()).To(Equal("ok"))
		Expect(health.GetDryRun()).To(BeTrue())

		statusResponse := serve(handler, httptest.NewRequest(http.MethodGet, "/v1/status", nil), true)
		Expect(statusResponse.Code).To(Equal(http.StatusOK))
		var status candaceosv1.AgentStatus
		Expect(protojson.Unmarshal(statusResponse.Body.Bytes(), &status)).To(Succeed())
		Expect(status.GetNodeId()).To(Equal("node-a"))
		Expect(status.Fence).To(BeNil(), "fence presence must mean a fence was accepted")
		Expect(status.Assignment).To(BeNil(), "assignment presence must mean convergence succeeded")
		Expect(status.UpdatedAt).To(BeNil())
		Expect(statusResponse.Body.String()).NotTo(ContainSubstring(`"fence"`))
		Expect(statusResponse.Body.String()).NotTo(ContainSubstring(`"assignment"`))
	})

	It("consumes and returns the shared protobuf contract with enum names", func() {
		response := put(handler, &candaceosv1.ReconcileRequest{
			Fence: &candaceosv1.Fence{Term: 1, LeaderId: "warden-a"},
			Assignment: &candaceosv1.Assignment{
				App:            "notes",
				Project:        "candace-notes",
				Path:           "notes",
				DesiredState:   candaceosv1.DesiredState_DESIRED_STATE_RUNNING,
				SourceRevision: sourceRevision,
				ContentSha256:  sourceDigest,
			},
		})
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(response.Body.String()).To(ContainSubstring(`"term":"1"`), "uint64 must use canonical ProtoJSON")
		Expect(response.Body.String()).To(ContainSubstring(`"desired_state":"DESIRED_STATE_RUNNING"`))
		Expect(response.Body.String()).NotTo(ContainSubstring("desiredState"))

		var result candaceosv1.ReconcileResponse
		Expect(protojson.Unmarshal(response.Body.Bytes(), &result)).To(Succeed())
		Expect(result.GetFence()).NotTo(BeNil())
		Expect(result.GetAssignment()).NotTo(BeNil())
		Expect(result.GetAssignment().GetDesiredState()).To(Equal(candaceosv1.DesiredState_DESIRED_STATE_RUNNING))
		Expect(result.GetAssignment().GetSourceRevision()).To(Equal(sourceRevision))
		Expect(result.GetAssignment().GetContentSha256()).To(Equal(sourceDigest))
		Expect(result.GetDryRun()).To(BeTrue())
		Expect(result.GetCommands()).To(HaveLen(2))
		argv := result.GetCommands()[1].GetArgv()
		Expect(argv[len(argv)-4:]).To(Equal([]string{"up", "-d", "--remove-orphans", "notes"}))
		Expect(result.GetUpdatedAt()).NotTo(BeNil())
		Expect(result.GetUpdatedAt().CheckValid()).To(Succeed())

		statusResponse := serve(handler, httptest.NewRequest(http.MethodGet, "/v1/status", nil), true)
		var status candaceosv1.AgentStatus
		Expect(protojson.Unmarshal(statusResponse.Body.Bytes(), &status)).To(Succeed())
		Expect(status.Fence).NotTo(BeNil())
		Expect(status.Assignment).NotTo(BeNil())
		Expect(status.GetAssignment().GetDesiredState()).To(Equal(candaceosv1.DesiredState_DESIRED_STATE_RUNNING))
		Expect(status.UpdatedAt).NotTo(BeNil())
	})

	It("returns conflict for a stale protobuf fence", func() {
		request := func(term uint64) *candaceosv1.ReconcileRequest {
			return &candaceosv1.ReconcileRequest{
				Fence: &candaceosv1.Fence{Term: term, LeaderId: "warden-a"},
				Assignment: &candaceosv1.Assignment{
					App:            "notes",
					Project:        "notes",
					Path:           "notes",
					DesiredState:   candaceosv1.DesiredState_DESIRED_STATE_STOPPED,
					SourceRevision: sourceRevision,
					ContentSha256:  sourceDigest,
				},
			}
		}
		Expect(put(handler, request(2)).Code).To(Equal(http.StatusOK))
		Expect(put(handler, request(1)).Code).To(Equal(http.StatusConflict))
	})

	DescribeTable("rejects invalid protobuf JSON requests",
		func(body string, expectedStatus int) {
			request := httptest.NewRequest(http.MethodPut, "/v1/assignment", bytes.NewBufferString(body))
			response := serve(handler, request, true)
			Expect(response.Code).To(Equal(expectedStatus), response.Body.String())
		},
		Entry("missing fence message", `{"assignment":{"app":"notes","project":"notes","path":"notes","desired_state":"DESIRED_STATE_RUNNING"}}`, http.StatusUnprocessableEntity),
		Entry("missing assignment message", `{"fence":{"term":"1","leader_id":"warden-a"}}`, http.StatusUnprocessableEntity),
		Entry("unknown field", `{"fence":{"term":"1","leader_id":"warden-a"},"assignment":{"app":"notes","project":"notes","path":"notes","desired_state":"DESIRED_STATE_RUNNING"},"surprise":true}`, http.StatusBadRequest),
		Entry("legacy domain enum string", `{"fence":{"term":"1","leader_id":"warden-a"},"assignment":{"app":"notes","project":"notes","path":"notes","desired_state":"running"}}`, http.StatusBadRequest),
		Entry("trailing JSON value", `{"fence":{"term":"1","leader_id":"warden-a"},"assignment":{"app":"notes","project":"notes","path":"notes","desired_state":"DESIRED_STATE_RUNNING"}} {}`, http.StatusBadRequest),
	)

	DescribeTable("identifies the generated assignment refinement",
		func(body, field string) {
			request := httptest.NewRequest(http.MethodPut, "/v1/assignment", bytes.NewBufferString(body))
			response := serve(handler, request, true)
			Expect(response.Code).To(Equal(http.StatusUnprocessableEntity), response.Body.String())
			Expect(response.Body.String()).To(ContainSubstring("candace.candaceos.v1.Assignment." + field))
		},
		Entry("path traversal", `{"fence":{"term":"1","leader_id":"warden-a"},"assignment":{"app":"notes","project":"notes","path":"apps/../notes","desired_state":"DESIRED_STATE_RUNNING","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","content_sha256":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}`, "path"),
		Entry("unspecified enum", `{"fence":{"term":"1","leader_id":"warden-a"},"assignment":{"app":"notes","project":"notes","path":"notes","desired_state":"DESIRED_STATE_UNSPECIFIED","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","content_sha256":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}`, "desired_state"),
		Entry("unknown enum", `{"fence":{"term":"1","leader_id":"warden-a"},"assignment":{"app":"notes","project":"notes","path":"notes","desired_state":99,"source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","content_sha256":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}`, "desired_state"),
		Entry("invalid source revision", `{"fence":{"term":"1","leader_id":"warden-a"},"assignment":{"app":"notes","project":"notes","path":"notes","desired_state":"DESIRED_STATE_RUNNING","source_revision":"main","content_sha256":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}`, "source_revision"),
		Entry("invalid content digest", `{"fence":{"term":"1","leader_id":"warden-a"},"assignment":{"app":"notes","project":"notes","path":"notes","desired_state":"DESIRED_STATE_RUNNING","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","content_sha256":"bbbb"}}`, "content_sha256"),
	)
})

func runHTTPGit(workspace string, args ...string) string {
	output, err := exec.Command("git", append([]string{"-C", workspace}, args...)...).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git %s failed: %s", strings.Join(args, " "), output)
	return strings.TrimSpace(string(output))
}

func put(handler http.Handler, request *candaceosv1.ReconcileRequest) *httptest.ResponseRecorder {
	body, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(request)
	Expect(err).NotTo(HaveOccurred())
	return serve(handler, httptest.NewRequest(http.MethodPut, "/v1/assignment", bytes.NewReader(body)), true)
}

func serve(handler http.Handler, request *http.Request, authenticated bool) *httptest.ResponseRecorder {
	if authenticated {
		request.Header.Set("Authorization", "Bearer test-token")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
