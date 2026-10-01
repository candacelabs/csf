package csf_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/candacelabs/csf/csf"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const themeCSS = ":root { --mantine-color-body: #fff4de; }"

var _ = Describe("host-owned Workbench theme file", func() {
	var directory, path string
	var service *csf.Service
	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		path = filepath.Join(directory, csf.WorkbenchThemeFileName)
		var err error
		service, err = csf.New(csf.WithWorkbenchThemeDirectory(directory))
		Expect(err).NotTo(HaveOccurred())
	})

	It("loads the fixed file on reconstruction without exposing mutable state", func() {
		Expect(readTheme(service)).To(BeEmpty())
		Expect(os.WriteFile(path, []byte(themeCSS), 0600)).To(Succeed())
		restored, err := csf.New(csf.WithWorkbenchThemeDirectory(directory))
		Expect(err).NotTo(HaveOccurred())
		result, err := restored.GetWorkbenchTheme(context.Background(), &pb.GetWorkbenchThemeRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Theme.FilePath).To(Equal(path))
		Expect(result.Theme.CustomCss).To(Equal(themeCSS))
		result.Theme.CustomCss = "caller mutation"
		Expect(readTheme(restored)).To(Equal(themeCSS))
	})

	It("requires explicit reload, then resets when the file is removed", func() {
		Expect(os.WriteFile(path, []byte(themeCSS), 0600)).To(Succeed())
		Expect(readTheme(service)).To(BeEmpty())
		reloadTheme(service)
		Expect(readTheme(service)).To(Equal(themeCSS))
		Expect(os.Remove(path)).To(Succeed())
		reloadTheme(service)
		Expect(readTheme(service)).To(BeEmpty())
	})

	It("resets when the canonical file becomes empty", func() {
		Expect(os.WriteFile(path, []byte(themeCSS), 0600)).To(Succeed())
		reloadTheme(service)
		Expect(os.WriteFile(path, nil, 0600)).To(Succeed())
		reloadTheme(service)
		Expect(readTheme(service)).To(BeEmpty())
	})

	DescribeTable("preserves the active value when a replacement is invalid", func(content []byte) {
		Expect(os.WriteFile(path, []byte(themeCSS), 0600)).To(Succeed())
		reloadTheme(service)
		Expect(os.WriteFile(path, content, 0600)).To(Succeed())
		_, err := service.ReloadWorkbenchTheme(context.Background(), &pb.ReloadWorkbenchThemeRequest{})
		Expect(err).To(HaveOccurred())
		Expect(readTheme(service)).To(Equal(themeCSS))
		_, err = csf.New(csf.WithWorkbenchThemeDirectory(directory))
		Expect(err).To(HaveOccurred())
	}, Entry("oversized CSS", []byte(strings.Repeat("x", 65537))),
		Entry("invalid UTF-8", []byte{0xff}))

	It("reports a filesystem read failure without clearing the active value", func() {
		Expect(os.WriteFile(path, []byte(themeCSS), 0600)).To(Succeed())
		reloadTheme(service)
		Expect(os.Remove(path)).To(Succeed())
		Expect(os.Mkdir(path, 0700)).To(Succeed())
		_, err := service.ReloadWorkbenchTheme(context.Background(), &pb.ReloadWorkbenchThemeRequest{})
		Expect(err).To(HaveOccurred())
		Expect(readTheme(service)).To(Equal(themeCSS))
	})

	It("requires host configuration rather than accepting an agent-selected path", func() {
		unconfigured, err := csf.New()
		Expect(err).NotTo(HaveOccurred())
		_, err = unconfigured.ReloadWorkbenchTheme(context.Background(), &pb.ReloadWorkbenchThemeRequest{})
		Expect(err).To(MatchError(ContainSubstring("directory is not configured")))
	})

	It("honors cancellation without publishing new CSS", func() {
		Expect(os.WriteFile(path, []byte(themeCSS), 0600)).To(Succeed())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := service.ReloadWorkbenchTheme(ctx, &pb.ReloadWorkbenchThemeRequest{})
		Expect(err).To(MatchError(context.Canceled))
		Expect(readTheme(service)).To(BeEmpty())
	})

	It("serves simultaneous readers and reloads of the shared file", func() {
		Expect(os.WriteFile(path, []byte(themeCSS), 0600)).To(Succeed())
		var workers sync.WaitGroup
		for range 8 {
			workers.Go(func() {
				defer GinkgoRecover()
				reloadTheme(service)
				Expect(readTheme(service)).To(Equal(themeCSS))
			})
		}
		workers.Wait()
	})
})

func readTheme(service *csf.Service) string {
	result, err := service.GetWorkbenchTheme(context.Background(), &pb.GetWorkbenchThemeRequest{})
	Expect(err).NotTo(HaveOccurred())
	return result.Theme.CustomCss
}

func reloadTheme(service *csf.Service) {
	_, err := service.ReloadWorkbenchTheme(context.Background(), &pb.ReloadWorkbenchThemeRequest{})
	Expect(err).NotTo(HaveOccurred())
}
