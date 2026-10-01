// Package workbench composes the existing Copilot service for caller-owned hosts.
package workbench

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/candacelabs/csf/pkg/boundedbuffer"
	boundedbufferv1 "github.com/candacelabs/csf/pkg/boundedbuffer/v1"
	"github.com/gin-gonic/gin"
)

const (
	repositoryProbeBytes = 8 << 10
	assetsRoute          = "/assets"
	uiRoute              = "/ui"
	uiIndex              = "/ui/"
	uiDocument           = "index.html"
)

func CanonicalRepositoryRoot(ctx context.Context, path string) (string, error) {
	configured, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("copilot-adapter: resolve repository root: %w", err)
	}
	configured, err = filepath.Abs(configured)
	if err != nil {
		return "", fmt.Errorf("copilot-adapter: make repository root absolute: %w", err)
	}
	retention := &boundedbufferv1.Retention{MaxBytes: repositoryProbeBytes}
	stdout, err := boundedbuffer.New(retention)
	if err != nil {
		return "", err
	}
	stderr, err := boundedbuffer.New(retention)
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, "git", "-C", configured, "rev-parse", "--show-toplevel")
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("copilot-adapter: find repository root: %w: %s", err, stderr.String())
	}
	root := strings.TrimSpace(stdout.String())
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("copilot-adapter: canonicalize repository root: %w", err)
	}
	return root, nil
}

func MountUI(engine gin.IRouter, directory string) {
	// Keep the root route for compatibility with older Vite bundles while the
	// canonical build uses /ui/ as its base.
	engine.Static(assetsRoute, filepath.Join(directory, strings.TrimPrefix(assetsRoute, "/")))
	engine.GET(uiRoute, func(context *gin.Context) { context.Redirect(http.StatusFound, uiIndex) })
	engine.GET(uiRoute+"/*path", func(context *gin.Context) {
		if candidate, found := existingUIFile(directory, context.Param("path")); found {
			context.File(candidate)
			return
		}
		// A missing artifact or build asset must not look like a successful
		// download of the SPA document. Extensionless history routes still work.
		if filepath.Ext(context.Param("path")) != "" {
			context.Status(http.StatusNotFound)
			return
		}
		context.File(filepath.Join(directory, uiDocument))
	})
}

func existingUIFile(directory string, requestPath string) (string, bool) {
	relative := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(requestPath, "/")))
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	candidate := filepath.Join(directory, relative)
	info, err := os.Stat(candidate)
	return candidate, err == nil && !info.IsDir()
}
