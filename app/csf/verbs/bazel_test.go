// Copyright 2026 Candace Labs

package verbs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/runtime/config"
)

var _ = Describe("csf bazel", func() {
	var (
		controller  *gomock.Controller
		launcher    *MockILauncher
		root        string
		output      bytes.Buffer
		diagnostics bytes.Buffer
		variables   map[string]string
	)
	environment := func() config.Environment {
		return config.NewEnvironment(func(name string) (string, bool) {
			value, ok := variables[name]
			return value, ok
		})
	}
	run := func(arguments ...string) (int, error) {
		return bazelVerb(context.Background(), launcher, environment(), arguments, nil, &output, &diagnostics)
	}
	expectRoot := func() {
		launcher.EXPECT().Run(gomock.Any(), launched(gitExecutable, gitTopLevel...)).
			Return(proc.Result{Stdout: []byte(root + "\n")}, nil)
	}
	expectSwitch := func() {
		for variable, value := range map[string]string{opamRoot: "/opam root", opamSwitch: "dev", opamPrefix: "/opam root/dev"} {
			launcher.EXPECT().Run(gomock.Any(), launched(opamExecutable, opamVar, variable)).
				Return(proc.Result{Stdout: []byte(value + "\n")}, nil)
		}
		launcher.EXPECT().Run(gomock.Any(), launched(opamExecutable, opamList)).
			Return(proc.Result{Stdout: []byte("cmdliner 2.1.0\nyojson 3.0.0\n")}, nil)
	}

	BeforeEach(func() {
		controller = gomock.NewController(GinkgoT())
		launcher = NewMockILauncher(controller)
		root = GinkgoT().TempDir()
		output.Reset()
		diagnostics.Reset()
		variables = map[string]string{pathVariable: "/usr/bin", homeVariable: "/home/dev", "STRAY": "leaked"}
		Expect(os.MkdirAll(filepath.Join(root, "csf", "compiler"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, opamLockPath),
			[]byte(`{"packages": {"cmdliner": "2.1.1", "sha": "1.15.4", "yojson": "3.0.0"}}`), 0o644)).To(Succeed())
	})

	It("builds hard-pinned through tools/bazel.sh by default, passing the arguments unchanged", func() {
		expectRoot()
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, command proc.Command) (proc.Result, error) {
			Expect(command.Executable).To(Equal(bashExecutable))
			Expect(command.Arguments).To(Equal([]string{filepath.Join(root, hardPinLauncher), "build", "--lockfile_mode=error", "//x"}))
			Expect(command.Directory).To(Equal(root))
			Expect(command.Environment).To(BeNil())
			return proc.Result{}, nil
		})
		Expect(run("build", "--lockfile_mode=error", "//x")).To(Equal(0))
	})

	It("builds soft-pinned with only the declared environment and declared flags, and reports lock drift", func() {
		expectRoot()
		expectSwitch()
		launcher.EXPECT().Run(gomock.Any(), launched(bazelExecutable)).DoAndReturn(func(_ context.Context, command proc.Command) (proc.Result, error) {
			Expect(command.Environment).To(Equal([]string{"PATH=/usr/bin", "HOME=/home/dev", "USER=" + defaultUser}))
			Expect(command.Arguments).To(Equal([]string{
				"--batch", "test",
				"--lockfile_mode=off",
				"--repo_env=OBAZL_OPAM_ENV=1",
				"--repo_env=OBAZL_UPDATE_LOCK=1",
				"--repo_env=OPAMROOT=/opam root",
				"--repo_env=OPAMSWITCH=dev",
				"--repo_env=OPAM_SWITCH_PREFIX=/opam root/dev",
				"//x", "--", "--lockfile_mode=error",
			}))
			return proc.Result{}, &proc.ExitError{Executable: bazelExecutable, Code: 3}
		})
		code, err := run("--pin=soft", "--", "--batch", "test", "--lockfile_mode=update", "//x", "--", "--lockfile_mode=error")
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(3), "Bazel's own exit status passes through")
		Expect(diagnostics.String()).To(Equal(
			"csf bazel: soft pin: cmdliner is 2.1.0, the lock says 2.1.1\n" +
				"csf bazel: soft pin: sha is not installed, the lock says 1.15.4\n"))
	})

	It("refuses a soft pin in CI", func() {
		variables[ciVariable] = "true"
		expectRoot()
		_, err := run("--pin=soft", "build", "//x")
		Expect(err).To(MatchError(errSoftPinInCI))
	})

	It("refuses a soft pin with no active opam switch, before running Bazel", func() {
		expectRoot()
		launcher.EXPECT().Run(gomock.Any(), launched(opamExecutable, opamVar, opamRoot)).
			Return(proc.Result{}, &proc.ExitError{Executable: opamExecutable, Code: 5})
		_, err := run("--pin=soft", "build", "//x")
		Expect(err).To(MatchError(ContainSubstring("--pin=soft needs an active opam switch")))
	})

	It("refuses an unknown pin and an empty Bazel command", func() {
		expectRoot()
		_, err := run("--pin=loose", "build")
		Expect(err).To(MatchError(`--pin is hard or soft, not "loose"`))
		_, err = run("--pin=soft")
		Expect(err).To(MatchError(errNoBazelArgs))
	})
})
