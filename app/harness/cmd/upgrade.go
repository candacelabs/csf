// Copyright 2026 Candace Labs

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/candacelabs/csf/csf"
	ionet "github.com/candacelabs/csf/io/net"
	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/io/ipc/proc"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime/config"
	"github.com/candacelabs/csf/services/harness/upgrade"
)

const (
	verbUpgrade  = "upgrade"
	commitFlag   = "commit"
	rollbackFlag = "rollback"
	binaryFlag   = "binary"

	// releaseRepository is where CI publishes the csf binary on every merge.
	releaseRepository  = "candacelabs/csf_staging"
	githubAPI          = "https://api.github.com/repos/"
	releaseByTag       = "/releases/tags/"
	headerAccept       = "Accept"
	headerAuthorize    = "Authorization"
	bearerPrefix       = "Bearer "
	acceptReleaseJSON  = "application/vnd.github+json"
	acceptReleaseAsset = "application/octet-stream"
	// downloadTimeout bounds one download: the binary is about 65 MB.
	downloadTimeout = 10 * time.Minute
	// probeTimeout bounds one request to the restarted Workbench.
	probeTimeout = 5 * time.Second

	processExecutable = "exe"
	processArguments  = "cmdline"
	processDirectory  = "cwd"
	processStat       = "stat"
	argumentSeparator = "\x00"
	// The /proc states of a process that has exited but is not yet reaped.
	stateZombie = "Z"
	stateDead   = "X"
)

// progressTime stamps each progress line with the wall clock.
const progressTime = "15:04:05"

var errUpgradeBoth = errors.New("give -" + commitFlag + " or -" + rollbackFlag + ", not both")

// clientWarnings prints a client's warnings, such as a newer host's
// response carrying fields this binary does not know, on standard error.
var clientWarnings = csf.WithClientWarnings(func(warning string) {
	fmt.Fprintf(os.Stderr, "%s: warning: %s\n", commandName, warning)
})

// upgradeVerb installs a released binary, or rolls back to the kept one, and
// restarts the running host on it, printing one line per step as it happens.
func upgradeVerb(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbUpgrade, flag.ContinueOnError)
	commit := flags.String(commitFlag, "", "commit whose release to install (default: latest main)")
	rollback := flags.Bool(rollbackFlag, false, "restore the binary the last upgrade replaced")
	binaryPath := flags.String(binaryFlag, "", "the csf binary to replace (default: the running host's, else this one)")
	stateDirectory := flags.String(stateFlag, "", "state directory holding "+HostRecordFile+" (default ~/"+defaultStateDirectory+")")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errUsage
	}
	if *commit != "" && *rollback {
		return errUpgradeBoth
	}
	progress := func(line string) { fmt.Fprintf(output, "%s %s\n", time.Now().Format(progressTime), line) }
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	record, running, err := runningHost(state)
	if err != nil {
		return err
	}
	flagBinary := ""
	if *binaryPath != "" {
		if flagBinary, err = filepath.Abs(*binaryPath); err != nil {
			return err
		}
	}
	binary, source, err := upgrade.ChooseBinary(upgrade.BinaryCandidates{
		Flag:     flagBinary,
		Recorded: record.Binary,
		Running:  running,
		ProcessExecutable: func() (string, error) {
			return os.Readlink(processFile(record.PID, processExecutable))
		},
		Self: os.Executable,
	})
	if err != nil {
		return err
	}
	if running {
		progress(fmt.Sprintf("host pid %d on %s", record.PID, record.Endpoint))
	} else {
		progress(fmt.Sprintf("no host is recorded as running in %s", state))
	}
	progress(fmt.Sprintf("binary: %s (%s)", binary, source))
	network := ionet.NewHostNetwork()
	restart := func(ctx context.Context) error {
		progress("no host to restart")
		return nil
	}
	if running {
		probe, err := iohttp.NewHTTPClient(network, iohttp.WithClientTimeout(probeTimeout))
		if err != nil {
			return err
		}
		control, err := hostControl(launcher, probe, state, binary, record)
		if err != nil {
			return err
		}
		restarter, err := upgrade.NewHostRestarter(control, upgrade.WithRestartProgress(progress))
		if err != nil {
			return err
		}
		restart = restarter.Restart
	}
	options := []upgrade.Option{upgrade.WithBinary(binary), upgrade.WithRestart(restart), upgrade.WithProgress(progress)}
	if !*rollback {
		token, err := githubToken(config.OSEnvironment())
		if err != nil {
			return err
		}
		download, err := iohttp.NewHTTPClient(network, iohttp.WithClientTimeout(downloadTimeout))
		if err != nil {
			return err
		}
		options = append(options, upgrade.WithFetch(releaseFetch(download, releaseRepository, token, progress)))
		options = append(options, upgrade.WithScoreCheck(scoreBeforeLive(state, progress)))
	}
	upgrader, err := upgrade.NewUpgrader(options...)
	if err != nil {
		return err
	}
	started := time.Now()
	var result upgrade.Result
	if *rollback {
		result, err = upgrader.Rollback(ctx)
	} else {
		result, err = upgrader.Upgrade(ctx, *commit)
	}
	if err != nil {
		return err
	}
	reportVersions(launcher, state, binary, progress)
	content, err := json.Marshal(struct {
		upgrade.Result
		ElapsedMS int64 `json:"elapsed_ms"`
	}{result, time.Since(started).Milliseconds()})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, string(content))
	return err
}

// reportVersions says which binary the serving host runs and at which
// version, and which csf is first on PATH and at which version; when the two
// differ, it says so on one line naming the path to fix, since a csf older
// than its host cannot read every field of the host's answers.
func reportVersions(launcher *proc.HostLauncher, state string, binary string, progress upgrade.Progress) {
	hostBinary, hostVersion := binary, csf.UnknownVersion
	if record, running, err := runningHost(state); err == nil && running {
		if executable, err := os.Readlink(processFile(record.PID, processExecutable)); err == nil {
			hostBinary = upgrade.ExecutablePath(executable)
		}
		hostVersion, _ = csf.BinaryVersion(hostBinary)
		progress(fmt.Sprintf("serving host pid %d runs %s at version %s", record.PID, hostBinary, hostVersion))
	} else {
		hostVersion, _ = csf.BinaryVersion(binary)
		progress(fmt.Sprintf("no host is serving; %s is at version %s", binary, hostVersion))
	}
	onPath, err := launcher.LookPath(commandName)
	if err != nil {
		progress(fmt.Sprintf("no %s on PATH", commandName))
		return
	}
	if resolved, err := filepath.EvalSymlinks(onPath); err == nil {
		onPath = resolved
	}
	pathVersion, _ := csf.BinaryVersion(onPath)
	progress(fmt.Sprintf("%s on PATH is %s at version %s", commandName, onPath, pathVersion))
	if pathVersion != hostVersion {
		progress(fmt.Sprintf("VERSION MISMATCH: %s on PATH (%s) is %s but %s is %s; copy %s over %s", commandName, onPath, pathVersion, hostBinary, hostVersion, hostBinary, onPath))
	}
}

// runningHost reads the host record, reporting whether its process runs.
func runningHost(state string) (hostRecord, bool, error) {
	var record hostRecord
	content, err := os.ReadFile(filepath.Join(state, HostRecordFile))
	if errors.Is(err, os.ErrNotExist) {
		return record, false, nil
	}
	if err != nil {
		return record, false, err
	}
	if err := json.Unmarshal(content, &record); err != nil {
		return record, false, fmt.Errorf("decode %s: %w", HostRecordFile, err)
	}
	return record, processRunning(record.PID), nil
}

// hostControl grants a restart this host: the record, StopHarness, the
// process table, and a start of binary with the running host's argument
// vector in its directory, logging to <state>/harness.log. A host recorded
// before serve recorded its argument vector is read from the process table.
// The new host is this command's own child, in its own process group, so its
// exit status arrives the moment it exits, and it outlives this command.
func hostControl(launcher *proc.HostLauncher, probe iohttp.IHTTPClient, state string, binary string, record hostRecord) (upgrade.HostControl, error) {
	argv, directory := record.Argv, record.Directory
	if len(argv) == 0 {
		content, err := os.ReadFile(processFile(record.PID, processArguments))
		if err != nil {
			return upgrade.HostControl{}, fmt.Errorf("read pid %d's argument vector: %w", record.PID, err)
		}
		argv = strings.Split(strings.TrimRight(string(content), argumentSeparator), argumentSeparator)
		if directory, err = os.Readlink(processFile(record.PID, processDirectory)); err != nil {
			return upgrade.HostControl{}, fmt.Errorf("read pid %d's working directory: %w", record.PID, err)
		}
	}
	if len(argv) < 2 || argv[1] != verbServe {
		return upgrade.HostControl{}, fmt.Errorf("pid %d was not started as %s %s: %q", record.PID, commandName, verbServe, argv)
	}
	logPath := filepath.Join(state, hostLogFile)
	var logStart int64
	return upgrade.HostControl{
		Current: func() (int, bool, error) {
			current, running, err := runningHost(state)
			return current.PID, running, err
		},
		Stop: func(ctx context.Context, pid int) error {
			current, _, err := runningHost(state)
			if err != nil {
				return err
			}
			client, err := csf.NewClient(current.Endpoint, &http.Client{Timeout: clientTimeout}, clientWarnings)
			if err != nil {
				return err
			}
			_, err = client.StopHarness(ctx, &harnessv1.StopHarnessRequest{})
			return err
		},
		Alive: processRunning,
		Start: func(ctx context.Context) (upgrade.StartedHost, error) {
			log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, hostFileMode)
			if err != nil {
				return upgrade.StartedHost{}, err
			}
			defer func() { _ = log.Close() }()
			if info, err := log.Stat(); err == nil {
				logStart = info.Size()
			}
			// Started on its own context, not ctx: the host outlives this command.
			process, err := launcher.Start(context.Background(), proc.Command{Executable: binary, Arguments: argv[1:], Directory: directory, Stdout: log, Stderr: log})
			if err != nil {
				return upgrade.StartedHost{}, err
			}
			exited := make(chan upgrade.ExitStatus, 1)
			// Joined by the process's exit; when this command returns first,
			// the host lives on and the goroutine ends with this process.
			go func() {
				result, err := process.Wait()
				exited <- upgrade.ExitStatus{Code: result.ExitCode, Err: err}
			}()
			return upgrade.StartedHost{PID: process.Pid(), Log: logPath, Exited: exited}, nil
		},
		Ready: func(ctx context.Context, pid int) (bool, string) {
			return probeHost(ctx, probe, state, pid)
		},
		LogTail: func(lines int) []string { return logTail(logPath, logStart, lines) },
	}, nil
}

// probeHost reports whether the record names pid and the Workbench it names
// answers its page.
func probeHost(ctx context.Context, client iohttp.IHTTPClient, state string, pid int) (bool, string) {
	record, running, err := runningHost(state)
	switch {
	case err != nil:
		return false, err.Error()
	case !running || record.PID != pid:
		return false, fmt.Sprintf("pid %d has not written its record yet", pid)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(record.Endpoint, "/")+"/", nil)
	if err != nil {
		return false, err.Error()
	}
	response, err := client.Do(request)
	if err != nil {
		return false, err.Error()
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	_ = response.Body.Close()
	return response.StatusCode == http.StatusOK, "the Workbench at " + record.Endpoint + " answered " + response.Status
}

// logTail is the last lines of the log written since offset.
func logTail(path string, offset int64, lines int) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.NewSectionReader(file, offset, 1<<62))
	if err != nil {
		return nil
	}
	all := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if len(all) == 1 && all[0] == "" {
		return nil
	}
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return all
}

// processRunning is whether pid exists and has not exited.
func processRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	content, err := os.ReadFile(processFile(pid, processStat))
	if err != nil {
		return false
	}
	// The command name is parenthesized and may hold spaces; the state is
	// the first field after its closing parenthesis.
	fields := strings.Fields(string(content[bytes.LastIndexByte(content, ')')+1:]))
	return len(fields) > 0 && fields[0] != stateZombie && fields[0] != stateDead
}

func processFile(pid int, name string) string {
	return filepath.Join(processTableDirectory, strconv.Itoa(pid), name)
}

// releaseFetch downloads release assets of repository through GitHub's REST
// API, authenticated with token. GitHub answers an asset with a redirect to
// its storage, which the client follows without the token.
func releaseFetch(client iohttp.IHTTPClient, repository string, token string, progress upgrade.Progress) upgrade.Fetch {
	return func(ctx context.Context, tag string, asset string) ([]byte, error) {
		content, err := githubGet(ctx, client, token, githubAPI+repository+releaseByTag+tag, acceptReleaseJSON)
		if err != nil {
			return nil, err
		}
		var release struct {
			Target string `json:"target_commitish"`
			Assets []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"assets"`
		}
		if err := json.Unmarshal(content, &release); err != nil {
			return nil, fmt.Errorf("decode release %s: %w", tag, err)
		}
		for _, published := range release.Assets {
			if published.Name == asset {
				if asset == upgrade.Asset {
					progress(fmt.Sprintf("release %s of %s is commit %s", tag, repository, release.Target))
				}
				return githubGet(ctx, client, token, published.URL, acceptReleaseAsset)
			}
		}
		return nil, fmt.Errorf("release %s of %s has no asset %s", tag, repository, asset)
	}
}

func githubGet(ctx context.Context, client iohttp.IHTTPClient, token string, url string, accept string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(headerAccept, accept)
	request.Header.Set(headerAuthorize, bearerPrefix+token)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("GET %s: HTTP %d: %s", url, response.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(response.Body)
}
