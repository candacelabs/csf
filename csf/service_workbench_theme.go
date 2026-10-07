package csf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

// WorkbenchThemeFileName is the only stylesheet the theme capability reads.
const WorkbenchThemeFileName = "workbench-theme.css"

// A leaf lock serializes reloads with snapshot reads. No worker is created.
type workbenchTheme struct {
	mu   sync.RWMutex
	path string
	css  string
}

// WithWorkbenchThemeDirectory selects where the host reads workbench-theme.css.
// Missing or empty files use the default theme. Agents edit the file, then call
// ReloadWorkbenchTheme; they cannot choose a file path through an operation.
func WithWorkbenchThemeDirectory(directory string) Option {
	return func(service *Service) { service.theme.path = filepath.Join(directory, WorkbenchThemeFileName) }
}

func (theme *workbenchTheme) load() error {
	if theme.path == "" {
		return nil
	}
	path, err := filepath.Abs(theme.path)
	if err != nil {
		return err
	}
	theme.path = path
	css, err := readWorkbenchTheme(path)
	if err != nil {
		return err
	}
	theme.css = css
	return nil
}

func readWorkbenchTheme(path string) (string, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// Closing a read-only descriptor cannot publish a partially written theme.
	// Its cleanup error does not replace a read or validation failure.
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, maxAPIBytes+1))
	if err != nil {
		return "", err
	}
	css := string(content)
	if !utf8.ValidString(css) {
		return "", fmt.Errorf("theme CSS must be UTF-8")
	}
	if err := pb.ValidateWorkbenchTheme(&pb.WorkbenchTheme{CustomCss: css}); err != nil {
		return "", err
	}
	return css, nil
}

func (service *Service) GetWorkbenchTheme(ctx context.Context, request *pb.GetWorkbenchThemeRequest) (*pb.GetWorkbenchThemeResponse, error) {
	service.theme.mu.RLock()
	defer service.theme.mu.RUnlock()
	return &pb.GetWorkbenchThemeResponse{Theme: service.theme.snapshot()}, nil
}

func (service *Service) ReloadWorkbenchTheme(ctx context.Context, request *pb.ReloadWorkbenchThemeRequest) (*pb.ReloadWorkbenchThemeResponse, error) {
	service.theme.mu.Lock()
	defer service.theme.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if service.theme.path == "" {
		return nil, fmt.Errorf("Workbench theme directory is not configured")
	}
	css, err := readWorkbenchTheme(service.theme.path)
	if err != nil {
		return nil, fmt.Errorf("reload Workbench theme: %w", err)
	}
	service.theme.css = css
	return &pb.ReloadWorkbenchThemeResponse{Theme: service.theme.snapshot()}, nil
}

// The caller holds the theme lock; each response owns its protobuf value.
func (theme *workbenchTheme) snapshot() *pb.WorkbenchTheme {
	return &pb.WorkbenchTheme{CustomCss: theme.css, FilePath: theme.path}
}
