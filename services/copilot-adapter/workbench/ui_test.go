package workbench

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("repository root resolution", func() {
	It("normalizes a nested configured directory to the git worktree top level", func() {
		repository := GinkgoT().TempDir()
		Expect(exec.Command("git", "init", "--quiet", repository).Run()).To(Succeed())
		nested := filepath.Join(repository, "go", "app")
		Expect(os.MkdirAll(nested, 0o750)).To(Succeed())

		root, err := CanonicalRepositoryRoot(context.Background(), nested)
		Expect(err).NotTo(HaveOccurred())
		Expect(root).To(Equal(repository))
	})
})

var _ = Describe("UI bundle mount", func() {
	var engine *gin.Engine
	var directory string

	BeforeEach(func() {
		gin.SetMode(gin.TestMode)
		directory = GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(directory, "assets"), 0o750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, uiDocument), []byte("workbench-index"), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, "assets", "app.js"), []byte("workbench-asset"), 0o600)).To(Succeed())
		engine = httpserver.NewEngine("workbench-test")
		MountUI(engine, directory)
	})

	It("serves the index from the canonical UI base", func() {
		response := request(engine, uiIndex)
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(Equal("workbench-index"))
	})

	It("falls back to the index for client-side history routes", func() {
		response := request(engine, "/ui/worktrees/abc123")
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(Equal("workbench-index"))
	})

	It("serves build assets at both canonical and legacy absolute paths", func() {
		canonical := request(engine, "/ui/assets/app.js")
		legacy := request(engine, "/assets/app.js")
		Expect(canonical.Code).To(Equal(http.StatusOK))
		Expect(canonical.Body.String()).To(Equal("workbench-asset"))
		Expect(legacy.Code).To(Equal(http.StatusOK))
		Expect(legacy.Body.String()).To(Equal("workbench-asset"))
	})

	It("reports missing camera artifacts and build assets as missing", func() {
		for _, path := range []string{
			"/ui/simulation-runs/isaac/run/camera-step-000020.png",
			"/ui/simulation-runs/carla/run/frames.json",
			"/ui/assets/missing.js",
		} {
			response := request(engine, path)
			Expect(response.Code).To(Equal(http.StatusNotFound), path)
			Expect(response.Body.String()).NotTo(ContainSubstring("workbench-index"))
		}
	})

	It("redirects the base path to its trailing-slash form", func() {
		response := request(engine, uiRoute)
		Expect(response.Code).To(Equal(http.StatusFound))
		Expect(response.Header().Get("Location")).To(Equal(uiIndex))
	})
})

func request(engine http.Handler, path string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(http.MethodGet, path, nil)
	engine.ServeHTTP(response, httpRequest)
	return response
}
