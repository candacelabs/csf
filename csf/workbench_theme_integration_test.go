package csf_test

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/candacelabs/csf/csf"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Workbench theme in a consumer's CSF host", func() {
	var consumer *csfConsumer
	var path string
	BeforeEach(func() {
		directory := GinkgoT().TempDir()
		path = filepath.Join(directory, csf.WorkbenchThemeFileName)
		consumer = newCSFConsumer(csf.WithWorkbenchThemeDirectory(directory))
	})

	It("mounts with existing CSF capabilities and reports the canonical file", func() {
		snapshot, err := consumer.client.GetSnapshot(consumer.context, &pb.GetSnapshotRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Snapshot).NotTo(BeNil())
		theme, err := consumer.client.GetWorkbenchTheme(consumer.context, &pb.GetWorkbenchThemeRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(theme.Theme.FilePath).To(Equal(path))
	})

	It("reloads a file through MCP and exposes its contents through HTTP", func() {
		Expect(os.WriteFile(path, []byte(themeCSS), 0600)).To(Succeed())
		result, err := consumer.agent.CallTool(consumer.context, &mcp.CallToolParams{Name: "ReloadWorkbenchTheme", Arguments: map[string]string{}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeFalse())
		theme, err := consumer.client.GetWorkbenchTheme(consumer.context, &pb.GetWorkbenchThemeRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(theme.Theme.CustomCss).To(Equal(themeCSS))
	})

	It("rejects CSS or file paths supplied as tool arguments", func() {
		result, err := consumer.agent.CallTool(consumer.context, &mcp.CallToolParams{Name: "ReloadWorkbenchTheme", Arguments: map[string]string{"filePath": path, "customCss": themeCSS}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeTrue())
		_, err = os.Stat(path)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("reports a bad file without replacing the active theme", func() {
		Expect(os.WriteFile(path, []byte(themeCSS), 0600)).To(Succeed())
		_, err := consumer.client.ReloadWorkbenchTheme(consumer.context, &pb.ReloadWorkbenchThemeRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, []byte(strings.Repeat("x", 65537)), 0600)).To(Succeed())
		result, err := consumer.agent.CallTool(consumer.context, &mcp.CallToolParams{Name: "ReloadWorkbenchTheme", Arguments: map[string]string{}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeTrue())
		theme, err := consumer.client.GetWorkbenchTheme(consumer.context, &pb.GetWorkbenchThemeRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(theme.Theme.CustomCss).To(Equal(themeCSS))
	})

	It("resets after removing the file and reloading through HTTP", func() {
		Expect(os.WriteFile(path, []byte(themeCSS), 0600)).To(Succeed())
		_, err := consumer.client.ReloadWorkbenchTheme(consumer.context, &pb.ReloadWorkbenchThemeRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Remove(path)).To(Succeed())
		reset, err := consumer.client.ReloadWorkbenchTheme(consumer.context, &pb.ReloadWorkbenchThemeRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(reset.Theme.CustomCss).To(BeEmpty())
	})
})
