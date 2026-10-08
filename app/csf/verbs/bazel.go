// Copyright 2026 Candace Labs

package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/runtime/config"
)

// csf bazel [--pin=hard|soft] [--] BAZEL ARGUMENTS runs Bazel against this
// checkout under a declared pin.
//
// hard (the default) is tools/bazel.sh unchanged: the pinned image, the OCaml
// toolchain fetched at exactly the locked versions, and MODULE.bazel.lock as
// checked in. CI and every release build hard.
//
// soft is for a developer machine and is refused when CI is set. Bazel runs on
// the host with no inherited environment beyond PATH, HOME and USER; OCaml
// comes from the active opam switch, handed to tools_opam as declared
// --repo_env flags read from `opam var`; each package that differs from
// csf/compiler/opam.lock.json is printed rather than fatal; MODULE.bazel.lock
// is neither checked nor written. A soft build proves the code, not the pin.
const (
	verbBazel           = "bazel"
	bazelPinFlag        = "pin"
	pinHard             = "hard"
	pinSoft             = "soft"
	bazelExecutable     = "bazel"
	opamExecutable      = "opam"
	hardPinLauncher     = "tools/bazel.sh"
	opamLockPath        = "csf/compiler/opam.lock.json"
	bazelResidualMarker = "--"
	lockfileModeFlag    = "--lockfile_mode="
	lockfileModeOff     = "--lockfile_mode=off"
	repoEnvironmentFlag = "--repo_env="
	opamVar             = "var"
	opamRoot            = "root"
	opamSwitch          = "switch"
	opamPrefix          = "prefix"
	opamList            = "list"
	opamInstalled       = "--installed"
	opamColumns         = "--columns=name,version"
	opamShort           = "--short"

	// The process environment names this verb reads, through the config
	// capability, and the only ones a soft-pinned Bazel receives.
	ciVariable   = "CI"
	pathVariable = "PATH"
	homeVariable = "HOME"
	userVariable = "USER"
	defaultUser  = "bazel"
)

// tools_opam reads these from its environment: OBAZL_OPAM_ENV selects the
// switch the OPAM* values name, OBAZL_UPDATE_LOCK skips its lock-equality
// check. Both are already recorded inputs in MODULE.bazel.lock.
var softPinSwitches = []string{"OBAZL_OPAM_ENV=1", "OBAZL_UPDATE_LOCK=1"}

// The opam variables tools_opam reads, each paired with the `opam var` name
// that supplies it.
var opamRepositoryEnvironment = []struct{ name, variable string }{
	{"OPAMROOT", opamRoot},
	{"OPAMSWITCH", opamSwitch},
	{"OPAM_SWITCH_PREFIX", opamPrefix},
}

var (
	errSoftPinInCI = errors.New("--pin=soft is for a developer machine; CI and releases build hard-pinned")
	errNoBazelArgs = errors.New("usage: csf bazel [--pin=hard|soft] [--] BAZEL ARGUMENTS")
)

// bazelVerb is `csf bazel`. It returns Bazel's own exit status, so a failing
// test run reports the code Bazel chose.
func bazelVerb(ctx context.Context, launcher proc.ILauncher, environment config.Environment, arguments []string, input io.Reader, output io.Writer, diagnostics io.Writer) (int, error) {
	flags := flag.NewFlagSet(commandName+" "+verbBazel, flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	pin := flags.String(bazelPinFlag, pinHard, "hard: the pinned image and lock (CI, releases); soft: host Bazel and the active opam switch (a developer machine)")
	if err := flags.Parse(arguments); err != nil {
		return exitFailed, err
	}
	bazelArguments := flags.Args()
	if len(bazelArguments) == 0 {
		return exitFailed, errNoBazelArgs
	}
	root, err := checkoutRoot(ctx, launcher)
	if err != nil {
		return exitFailed, err
	}
	streams := proc.Command{Directory: root, Stdin: input, Stdout: output, Stderr: diagnostics}
	switch *pin {
	case pinHard:
		streams.Executable = bashExecutable
		streams.Arguments = append([]string{filepath.Join(root, hardPinLauncher)}, bazelArguments...)
		return exitStatus(launcher.Run(ctx, streams))
	case pinSoft:
		if environment.Raw(ciVariable) != "" {
			return exitFailed, errSoftPinInCI
		}
		declared, err := softPinFlags(ctx, launcher, environment)
		if err != nil {
			return exitFailed, err
		}
		reportLockDrift(ctx, launcher, environment, root, diagnostics)
		streams.Executable = bazelExecutable
		streams.Arguments = withDeclaredFlags(bazelArguments, declared)
		streams.Environment = declaredEnvironment(environment)
		return exitStatus(launcher.Run(ctx, streams))
	}
	return exitFailed, fmt.Errorf("--%s is %s or %s, not %q", bazelPinFlag, pinHard, pinSoft, *pin)
}

// checkoutRoot is the checkout this binary was asked to build.
func checkoutRoot(ctx context.Context, launcher proc.ILauncher) (string, error) {
	result, err := launcher.Run(ctx, proc.Command{Executable: gitExecutable, Arguments: gitTopLevel})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// declaredEnvironment is everything a soft-pinned Bazel inherits: PATH, HOME
// and USER, each named here, and nothing else.
func declaredEnvironment(environment config.Environment) []string {
	return []string{
		pathVariable + "=" + environment.Raw(pathVariable),
		homeVariable + "=" + environment.Raw(homeVariable),
		userVariable + "=" + environment.String(userVariable, defaultUser),
	}
}

// softPinFlags are the flags that turn the active opam switch into Bazel's
// OCaml toolchain and keep the lockfile untouched.
func softPinFlags(ctx context.Context, launcher proc.ILauncher, environment config.Environment) ([]string, error) {
	declared := []string{lockfileModeOff}
	for _, value := range softPinSwitches {
		declared = append(declared, repoEnvironmentFlag+value)
	}
	for _, pair := range opamRepositoryEnvironment {
		result, err := launcher.Run(ctx, proc.Command{
			Executable:  opamExecutable,
			Arguments:   []string{opamVar, pair.variable},
			Environment: declaredEnvironment(environment),
		})
		if err != nil {
			return nil, fmt.Errorf("--pin=soft needs an active opam switch: %w", err)
		}
		declared = append(declared, repoEnvironmentFlag+pair.name+"="+strings.TrimSpace(string(result.Stdout)))
	}
	return declared, nil
}

// withDeclaredFlags puts declared right after the Bazel command, dropping any
// --lockfile_mode the caller gave before a -- separator.
func withDeclaredFlags(arguments []string, declared []string) []string {
	rewritten := make([]string, 0, len(arguments)+len(declared))
	placed, passthrough := false, false
	for _, argument := range arguments {
		if !passthrough && strings.HasPrefix(argument, lockfileModeFlag) {
			continue
		}
		rewritten = append(rewritten, argument)
		switch {
		case !placed && !strings.HasPrefix(argument, "-"):
			rewritten = append(rewritten, declared...)
			placed = true
		case placed && argument == bazelResidualMarker:
			passthrough = true
		}
	}
	return rewritten
}

// reportLockDrift prints each locked package the active switch holds at
// another version, or lacks. Informational: Bazel fails on a package the
// build actually needs.
func reportLockDrift(ctx context.Context, launcher proc.ILauncher, environment config.Environment, root string, diagnostics io.Writer) {
	locked, err := lockedPackages(filepath.Join(root, opamLockPath))
	if err != nil {
		fmt.Fprintf(diagnostics, "%s %s: soft pin: cannot read %s: %v\n", commandName, verbBazel, opamLockPath, err)
		return
	}
	result, err := launcher.Run(ctx, proc.Command{
		Executable:  opamExecutable,
		Arguments:   []string{opamList, opamInstalled, opamColumns, opamShort},
		Environment: declaredEnvironment(environment),
	})
	if err != nil {
		fmt.Fprintf(diagnostics, "%s %s: soft pin: cannot list the switch: %v\n", commandName, verbBazel, err)
		return
	}
	for _, line := range lockDrift(locked, installedPackages(string(result.Stdout))) {
		fmt.Fprintf(diagnostics, "%s %s: soft pin: %s\n", commandName, verbBazel, line)
	}
}

func lockedPackages(path string) (map[string]string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lock struct {
		Packages map[string]string `json:"packages"`
	}
	if err := json.Unmarshal(contents, &lock); err != nil {
		return nil, err
	}
	return lock.Packages, nil
}

// installedPackages reads `opam list --columns=name,version --short`.
func installedPackages(listing string) map[string]string {
	installed := map[string]string{}
	for _, line := range strings.Split(listing, "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 {
			installed[fields[0]] = fields[1]
		}
	}
	return installed
}

// lockDrift names every locked package whose installed version differs, in
// name order.
func lockDrift(locked map[string]string, installed map[string]string) []string {
	names := make([]string, 0, len(locked))
	for name := range locked {
		names = append(names, name)
	}
	sort.Strings(names)
	var drift []string
	for _, name := range names {
		have, ok := installed[name]
		switch {
		case !ok:
			drift = append(drift, fmt.Sprintf("%s is not installed, the lock says %s", name, locked[name]))
		case have != locked[name]:
			drift = append(drift, fmt.Sprintf("%s is %s, the lock says %s", name, have, locked[name]))
		}
	}
	return drift
}

// exitStatus passes a child's own exit status through; only a failure to run
// it at all is an error.
func exitStatus(result proc.Result, err error) (int, error) {
	var exit *proc.ExitError
	if errors.As(err, &exit) {
		return exit.Code, nil
	}
	if err != nil {
		return exitFailed, err
	}
	return result.ExitCode, nil
}
