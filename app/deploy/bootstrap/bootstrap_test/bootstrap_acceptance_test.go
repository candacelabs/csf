// Copyright 2026 Candace Labs

//go:build acceptance

// The acceptance tier: opt-in specs against a real, disposable PostgreSQL,
// opened only through csfpg.OpenPool. pgmem cannot yet run the deploy service
// schema (PL/pgSQL triggers, LATERAL, arrays, DISTINCT ON views); see
// go-rules.md CS-16.

package bootstrap_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/candacelabs/csf/app/deploy/bootstrap"
	"github.com/candacelabs/csf/ipc/db/csfpg"
	deployv1 "github.com/candacelabs/csf/proto/candace/deploy/v1"
	"github.com/candacelabs/csf/web/deploy/browserroutes"
)

const bootstrapTestDatabaseURLEnv = "DEPLOY_STORE_TEST_DATABASE_URL"
const wardenStatusPath = "/api/status"

func coreEnvironment(fieldName protoreflect.Name) string {
	field := (&deployv1.CoreConfig{}).ProtoReflect().Descriptor().Fields().ByName(fieldName)
	if field == nil {
		panic("unknown CoreConfig field " + fieldName)
	}
	return "DEPLOY_" + strings.ToUpper(string(field.Name()))
}

var _ = Describe("Core lifecycle on PostgreSQL", func() {
	It("cancels an active event stream and releases abandoned assemblies", func(ctx SpecContext) {
		databaseURL := strings.TrimSpace(os.Getenv(bootstrapTestDatabaseURLEnv))
		if databaseURL == "" {
			Skip("set " + bootstrapTestDatabaseURLEnv + " to run the Core lifecycle integration spec")
		}
		parsedDatabaseURL, err := url.Parse(databaseURL)
		Expect(err).NotTo(HaveOccurred())
		databaseName, err := url.PathUnescape(strings.TrimPrefix(parsedDatabaseURL.EscapedPath(), "/"))
		Expect(err).NotTo(HaveOccurred())
		Expect(databaseName).To(HaveSuffix("_test"), "the lifecycle spec refuses a non-test database")

		admin, err := csfpg.OpenPool(ctx, csfpg.Settings{URL: databaseURL})
		Expect(err).NotTo(HaveOccurred())
		Expect(admin.Ping(ctx)).To(Succeed())
		schemaName := fmt.Sprintf("deploy_bootstrap_test_%d_%d", os.Getpid(), time.Now().UnixNano())
		_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schemaName}.Sanitize())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(cleanupCtx SpecContext) {
			_, dropErr := admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" CASCADE")
			admin.Close()
			Expect(dropErr).NotTo(HaveOccurred())
		})
		query := parsedDatabaseURL.Query()
		query.Set("search_path", schemaName)
		parsedDatabaseURL.RawQuery = query.Encode()

		wardenWriteErrors := make(chan error, 1)
		wardenHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodGet || request.URL.Path != wardenStatusPath {
				http.NotFound(response, request)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(response, `{
  "view": {
    "self": "node-a",
    "role": "leader",
    "term": 1,
    "leader_id": "node-a",
    "authoritative": true,
    "updated_at": "2026-08-20T00:00:00Z",
    "membership": {
      "version": 1,
      "created_in_term": 1,
      "voters": [{"id": "node-a", "addr": "127.0.0.1:7717"}]
    },
    "peers": [{
      "node": {"id": "node-a", "addr": "127.0.0.1:7717"},
      "status": "alive",
      "last_seen": "2026-08-20T00:00:00Z",
      "member": "voter"
    }]
  }
}`); err != nil {
				select {
				case wardenWriteErrors <- err:
				default:
				}
			}
		})
		wardenServer := httptest.NewServer(wardenHandler)
		DeferCleanup(wardenServer.Close)

		reservation, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		bind := reservation.Addr().String()
		Expect(reservation.Close()).To(Succeed())

		GinkgoT().Setenv("DEPLOY_MODE", "")
		GinkgoT().Setenv(coreEnvironment("harness_backend"), "demo")
		GinkgoT().Setenv(coreEnvironment("database_url"), parsedDatabaseURL.String())
		GinkgoT().Setenv(coreEnvironment("data_dir"), GinkgoT().TempDir())
		GinkgoT().Setenv(coreEnvironment("workspace"), GinkgoT().TempDir())
		GinkgoT().Setenv(coreEnvironment("warden_url"), wardenServer.URL)
		GinkgoT().Setenv(coreEnvironment("nodeexec_url"), "")
		GinkgoT().Setenv(coreEnvironment("nodeexec_port"), "8094")
		GinkgoT().Setenv(coreEnvironment("nodeexec_token"), "")
		GinkgoT().Setenv(coreEnvironment("node_labels"), "")
		GinkgoT().Setenv(coreEnvironment("bind"), bind)

		abandoned, err := bootstrap.AssembleCore(ctx, "lifecycle-test", bootstrap.WithHTTPService(testHTTPService{}))
		Expect(err).NotTo(HaveOccurred())
		Expect(abandoned.Close()).To(Succeed())
		Expect(abandoned.Close()).To(Succeed(), "Close must be idempotent before Run")
		Expect(abandoned.Run(ctx)).To(MatchError("Deploy is closed"))

		core, err := bootstrap.AssembleCore(ctx, "lifecycle-test", bootstrap.WithHTTPService(testHTTPService{}))
		Expect(err).NotTo(HaveOccurred())
		lifecycle, cancelLifecycle := context.WithCancel(ctx)
		runDone := make(chan error, 1)
		go func() {
			defer close(runDone)
			runDone <- core.Run(lifecycle)
		}()
		DeferCleanup(func(cleanupCtx SpecContext) {
			cancelLifecycle()
			Eventually(runDone).WithContext(cleanupCtx).Should(BeClosed())
		})

		client := &http.Client{}
		Eventually(func(probeCtx context.Context) error {
			request, requestErr := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://"+bind+browserroutes.Health, nil)
			if requestErr != nil {
				return requestErr
			}
			response, requestErr := client.Do(request)
			if requestErr != nil {
				return requestErr
			}
			var statusErr error
			if response.StatusCode != http.StatusOK {
				statusErr = fmt.Errorf("Core health returned %s", response.Status)
			}
			return errors.Join(statusErr, response.Body.Close())
		}).WithContext(ctx).Should(Succeed())

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+bind+testHTTPServicePath, nil)
		Expect(err).NotTo(HaveOccurred())
		customResponse, err := client.Do(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(customResponse.StatusCode).To(Equal(http.StatusNoContent))
		Expect(customResponse.Body.Close()).To(Succeed())

		request, err = http.NewRequestWithContext(ctx, http.MethodGet, "http://"+bind+browserroutes.Events, nil)
		Expect(err).NotTo(HaveOccurred())
		response, err := client.Do(request)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(response.Body.Close)
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		reader := bufio.NewReader(response.Body)
		eventLine, err := reader.ReadString('\n')
		Expect(err).NotTo(HaveOccurred())
		Expect(eventLine).To(Equal("event: snapshot\n"))
		dataLine, err := reader.ReadString('\n')
		Expect(err).NotTo(HaveOccurred())
		Expect(dataLine).To(HavePrefix("data: "))
		Expect(core.Close()).To(HaveOccurred(), "Run owns resources until it returns")

		cancelLifecycle()
		var runErr error
		Eventually(runDone).WithContext(ctx).Should(Receive(&runErr))
		Expect(runErr).NotTo(HaveOccurred())
		Expect(core.Close()).To(Succeed())
		Expect(core.Close()).To(Succeed(), "Close must be idempotent after Run")
		Expect(wardenWriteErrors).NotTo(Receive())
	}, SpecTimeout(20*time.Second))
})
