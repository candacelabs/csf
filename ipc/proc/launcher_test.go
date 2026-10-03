// Copyright 2026 Candace Labs

package proc_test

import (
	"context"
	"io"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/ipc/proc"
)

// The gateway as a client sees it: only the exported API, and every misuse
// answered with a defined error before any process exists. Specs that start
// real children are the package's own unit tests.
var _ = Describe("HostLauncher misuse", func() {
	const program = "/bin/true"
	var launcher *proc.HostLauncher

	BeforeEach(func() {
		var err error
		launcher, err = proc.NewHostLauncher()
		Expect(err).NotTo(HaveOccurred())
		var _ proc.ILauncher = launcher
	})

	DescribeTable("rejects a contradictory command without starting anything",
		func(run func(ctx context.Context) error, defined error) {
			Expect(run(context.Background())).To(MatchError(defined))
		},
		Entry("a terminal through Run", func(ctx context.Context) error {
			_, err := launcher.Run(ctx, proc.Command{Executable: program, Terminal: &proc.TerminalSize{Rows: 1, Columns: 1}})
			return err
		}, proc.ErrTerminalRun),
		Entry("a piped stdout through Run", func(ctx context.Context) error {
			_, err := launcher.Run(ctx, proc.Command{Executable: program, StdoutPipe: true})
			return err
		}, proc.ErrStreamConflict),
		Entry("both a stdout writer and a stdout pipe", func(ctx context.Context) error {
			_, err := launcher.Start(ctx, proc.Command{Executable: program, StdoutPipe: true, Stdout: io.Discard})
			return err
		}, proc.ErrStreamConflict),
		Entry("a terminal with its own stdin", func(ctx context.Context) error {
			_, err := launcher.Start(ctx, proc.Command{
				Executable: program, Stdin: strings.NewReader(""), Terminal: &proc.TerminalSize{Rows: 1, Columns: 1},
			})
			return err
		}, proc.ErrStreamConflict),
		Entry("no executable through Run", func(ctx context.Context) error {
			_, err := launcher.Run(ctx, proc.Command{})
			return err
		}, proc.ErrExecutableRequired),
		Entry("no executable through Start", func(ctx context.Context) error {
			_, err := launcher.Start(ctx, proc.Command{Arguments: []string{"x"}})
			return err
		}, proc.ErrExecutableRequired),
	)

	It("does not start a program for a context that has already ended", func() {
		ended, cancel := context.WithCancel(context.Background())
		cancel()

		result, err := launcher.Run(ended, proc.Command{Executable: program})

		Expect(err).To(MatchError(context.Canceled))
		Expect(result.ExitCode).To(Equal(-1), "nothing ran, so there is no exit status")
	})

	DescribeTable("rejects invalid options with the defined error",
		func(option proc.HostLauncherOption) {
			_, err := proc.NewHostLauncher(option)
			Expect(err).To(MatchError(proc.ErrInvalidOption))
		},
		Entry("a nil option", nil),
		Entry("a zero wait delay", proc.WithWaitDelay(0)),
		Entry("a negative wait delay", proc.WithWaitDelay(-time.Second)),
		Entry("a zero diagnostic limit", proc.WithDiagnosticBytes(0)),
	)

	It("accepts valid options", func() {
		_, err := proc.NewHostLauncher(proc.WithWaitDelay(time.Second), proc.WithDiagnosticBytes(1))
		Expect(err).NotTo(HaveOccurred())
	})
})
