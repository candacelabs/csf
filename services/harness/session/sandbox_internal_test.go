// Copyright 2026 Candace Labs

package session

import (
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("readWritePaths", func() {
	It("grants the worktree, run directory, private tmp and the cache paths", func() {
		paths := readWritePaths("/run/s1/worktree", "/run/s1", []string{
			"CANDACE_BAZEL_DISK_CACHE=/state/bazel-disk-cache",
			"CANDACE_BAZEL_CACHE=/run/s1/bazel",
			"NOT_A_PATH=relative",
			"EMPTY=",
		})
		Expect(paths).To(Equal([]string{
			"/run/s1/worktree", "/run/s1",
			"/state/bazel-disk-cache", "/run/s1/bazel",
		}))
	})
})

var _ = Describe("proxyDiagnostics", func() {
	It("logs each non-empty line and reports the full write length", func() {
		var records []string
		logger := slog.New(slog.NewTextHandler(writerFunc(func(line []byte) (int, error) {
			records = append(records, string(line))
			return len(line), nil
		}), nil))
		diagnostics := proxyDiagnostics{logger: logger, assignment: "s1"}
		chunk := []byte("first line\n\nsecond line\n")
		written, err := diagnostics.Write(chunk)
		Expect(err).NotTo(HaveOccurred())
		Expect(written).To(Equal(len(chunk)))
		Expect(records).To(HaveLen(2))
	})
})

// writerFunc adapts a function to io.Writer for the diagnostics spec.
type writerFunc func(line []byte) (int, error)

func (write writerFunc) Write(line []byte) (int, error) { return write(line) }
