// Copyright 2026 Candace Labs

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/io/net/hfjobs"
	ionet "github.com/candacelabs/csf/io/net"
	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/io/ipc/proc"
	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
	"github.com/candacelabs/csf/services/cloud"
	dispatchservice "github.com/candacelabs/csf/services/dispatch"
	"github.com/candacelabs/csf/services/email"
)

// The cloud service in serve, and the csf burst verb that drives it.
const (
	cloudProviderFlag    = "cloud-provider"
	burstImageFlag       = "burst-image"
	burstRepositoryFlag  = "burst-repository"
	operatorEmailFlag    = "operator-email-config"
	cloudProviderHF      = "huggingface"
	cloudProviderNone    = "none"
	huggingFaceTokenHome = ".cache/huggingface/token"
	cloudMountName       = "cloud"
	noticeSubjectFormat  = "CSF cloud %s: %s"

	verbBurst    = "burst"
	verbSessions = "sessions"
	verbProbe    = "probe"
	verbJobs     = "jobs"
	sessionsFlag = "n"
	flavorFlag   = "flavor"
	capFlag      = "cap"
	purposeFlag  = "purpose"
	jobFlag      = "job"
	// defaultBurstFlavor is cpu-xl: 16 vCPU, 124 GB and 1 TB of disk
	// (/api/jobs/hardware, 2026-10-05). Two sessions' Bazel output bases do
	// not fit the 50 GB the smaller CPU flavors carry.
	defaultBurstFlavor = "cpu-xl"
	defaultProbeFlavor = "cpu-basic"
	// defaultProbePurpose is what a probe is for when nobody says.
	defaultProbePurpose = "probe"

	ghExecutable = "gh"
	ghPR         = "pr"
	ghList       = "list"
	ghRepository = "--repo"
	ghHead       = "--head"
	ghState      = "--state"
	ghMerged     = "merged"
	ghJSON       = "--json"
	ghNumber     = "number"
	ghQuery      = "--jq"
	ghLength     = "length"
)

var errUsageBurst = errors.New("usage: csf burst sessions -n N [-flavor F] -cap USD [-dry-run] | probe -cap USD [-flavor F] [-purpose TEXT] | jobs | stop -job ID")

// cloudSettings are serve's cloud flags.
type cloudSettings struct {
	provider   string
	image      string
	repository string
	email      string
}

// addCloudFlags declares serve's cloud flags.
func addCloudFlags(flags *flag.FlagSet) *cloudSettings {
	settings := &cloudSettings{}
	flags.StringVar(&settings.provider, cloudProviderFlag, cloudProviderHF, "where cloud jobs run: "+cloudProviderHF+" (Hugging Face Jobs, with this user's Hugging Face token), "+
		cloud.ProviderDryRun+" (records that run nothing, for proving the Cloud panel) or "+cloudProviderNone)
	flags.StringVar(&settings.image, burstImageFlag, "", "the published session image burst jobs run (none: bursts are refused)")
	flags.StringVar(&settings.repository, burstRepositoryFlag, "", "owner/name of the repository burst jobs clone and open pull requests on (none: bursts are refused)")
	flags.StringVar(&settings.email, operatorEmailFlag, "", "private protobuf JSON operator email configuration: cloud notices reach the operator's phone through it")
	return settings
}

// newCloudJobs builds the cloud service serve mounts, or nil when no provider
// is selected or reachable; the reason is logged.
func newCloudJobs(ctx context.Context, settings *cloudSettings, state string, launcher proc.ILauncher, dispatcher *dispatchservice.DispatchService, logger *slog.Logger) (*cloud.CloudJobs, error) {
	var provider cloud.IJobProvider
	switch settings.provider {
	case cloudProviderNone:
		logger.Warn("harness: no cloud provider, by -" + cloudProviderFlag + " " + cloudProviderNone)
		return nil, nil
	case cloud.ProviderDryRun:
		provider = cloud.NewDryRunProvider(clock.NewSystemClock())
		logger.Warn("harness: cloud jobs are dry runs, by -" + cloudProviderFlag + " " + cloud.ProviderDryRun)
	case cloudProviderHF:
		jobs, err := huggingFaceJobs(ctx)
		if err != nil {
			logger.Warn("harness: the cloud service is not mounted: Hugging Face Jobs is not reachable", "error", err)
			return nil, nil
		}
		provider = jobs
	default:
		return nil, fmt.Errorf("-%s is %s, %s or %s, not %q", cloudProviderFlag, cloudProviderHF, cloud.ProviderDryRun, cloudProviderNone, settings.provider)
	}
	options := []cloud.Option{
		cloud.WithProvider(settings.provider, provider), cloud.WithStateDirectory(state), cloud.WithLogger(logger),
		cloud.WithBurstImage(settings.image), cloud.WithBurstRepository(settings.repository),
		cloud.WithSliceQueue(dispatcher.ReadySlices, func(ctx context.Context, slice string, reason string) error {
			_, err := dispatcher.Hold(ctx, dispatchservice.SliceControlInput{SliceID: slice, Reason: reason})
			return err
		}),
	}
	if settings.repository != "" {
		options = append(options, cloud.WithMergedCount(mergedCount(launcher, settings.repository)))
	}
	if settings.email != "" {
		mailer, err := email.NewOperatorMailer(settings.email, nil)
		if err != nil {
			return nil, err
		}
		options = append(options, cloud.WithNotifier(mailNotifier{mailer: mailer}))
	} else {
		logger.Warn("harness: cloud notices are on the Workbench only; give -" + operatorEmailFlag + " to send them to the operator's phone")
	}
	return cloud.NewCloudJobs(options...)
}

// cloudCollectors is the cloud record's series on /metrics, when the cloud
// service is mounted.
func cloudCollectors(jobs *cloud.CloudJobs, state string) []prometheus.Collector {
	if jobs == nil {
		return nil
	}
	return []prometheus.Collector{cloud.NewCloudCollector(state)}
}

// huggingFaceJobs is the Jobs client for this user's Hugging Face token, in
// the namespace the token belongs to.
func huggingFaceJobs(ctx context.Context) (*hfjobs.HFJobsClient, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(filepath.Join(home, huggingFaceTokenHome))
	if err != nil {
		return nil, fmt.Errorf("read the Hugging Face token: %w", err)
	}
	token := strings.TrimSpace(string(content))
	client, err := iohttp.NewHTTPClient(ionet.NewHostNetwork())
	if err != nil {
		return nil, err
	}
	namespace, err := hfjobs.WhoAmI(ctx, client, token)
	if err != nil {
		return nil, err
	}
	return hfjobs.NewHFJobsClient(client, namespace, token)
}

// mailNotifier sends each cloud notice to the operator by email.
type mailNotifier struct {
	mailer *email.Mailer
}

// Notify sends one notice.
func (notifier mailNotifier) Notify(ctx context.Context, notice cloud.Notice) error {
	_, err := notifier.mailer.Send(ctx, &emailv1.EmailMessage{Subject: fmt.Sprintf(noticeSubjectFormat, notice.Kind, notice.JobID), Text: notice.Text})
	return err
}

// mergedCount counts the merged pull requests of a burst's branches with gh.
func mergedCount(launcher proc.ILauncher, repository string) func(ctx context.Context, branches []string) (int, error) {
	return func(ctx context.Context, branches []string) (int, error) {
		total := 0
		for _, branch := range branches {
			result, err := launcher.Run(ctx, proc.Command{Executable: ghExecutable, Arguments: []string{
				ghPR, ghList, ghRepository, repository, ghHead, branch, ghState, ghMerged, ghJSON, ghNumber, ghQuery, ghLength}})
			if err != nil {
				return 0, fmt.Errorf("count merged pull requests of %s: %w", branch, err)
			}
			count, err := strconv.Atoi(strings.TrimSpace(string(result.Stdout)))
			if err != nil {
				return 0, fmt.Errorf("count merged pull requests of %s: %w", branch, err)
			}
			total += count
		}
		return total, nil
	}
}

// burstVerbs runs one burst verb against the running host.
func burstVerbs(ctx context.Context, arguments []string, output io.Writer) error {
	if len(arguments) == 0 {
		return errUsageBurst
	}
	verb := arguments[0]
	flags := flag.NewFlagSet(commandName+" "+verbBurst+" "+verb, flag.ContinueOnError)
	endpoint := flags.String(endpointFlag, "", "harness address (default: the one <state>/"+HostRecordFile+" records)")
	stateDirectory := flags.String(stateFlag, "", "state directory holding "+HostRecordFile+" (default ~/"+defaultStateDirectory+")")
	sessions := flags.Int(sessionsFlag, 0, "fixer sessions the burst runs, one ready slice each")
	flavor := flags.String(flavorFlag, "", "the provider's hardware flavor (default "+defaultBurstFlavor+" for sessions, "+defaultProbeFlavor+" for a probe)")
	capUSD := flags.Float64(capFlag, 0, "the job's spend cap in dollars")
	dryRun := flags.Bool(dryRunFlag, false, "show the job spec, secrets redacted, and launch nothing")
	purpose := flags.String(purposeFlag, defaultProbePurpose, "what a probe is for, as the Cloud panel shows it")
	job := flags.String(jobFlag, "", "the job to stop")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errUsageBurst
	}
	target, err := resolveEndpoint(*endpoint, *stateDirectory)
	if err != nil {
		return err
	}
	base := strings.TrimRight(target, "/")
	switch verb {
	case verbSessions:
		if *flavor == "" {
			*flavor = defaultBurstFlavor
		}
		var launched cloud.LaunchBurstOutput
		if err := requestCloud(ctx, http.MethodPost, base+cloud.BurstPath, cloud.LaunchBurstInput{Sessions: *sessions, Flavor: *flavor, CapUSD: *capUSD, DryRun: *dryRun}, &launched, output); err != nil {
			return err
		}
		if launched.Job == nil {
			return nil
		}
		return followJob(ctx, base, launched.Job.ID, output)
	case verbProbe:
		if *flavor == "" {
			*flavor = defaultProbeFlavor
		}
		var launched cloud.CloudJob
		if err := requestCloud(ctx, http.MethodPost, base+cloud.ProbePath, cloud.LaunchProbeInput{Flavor: *flavor, Purpose: *purpose, CapUSD: *capUSD}, &launched, output); err != nil {
			return err
		}
		return followJob(ctx, base, launched.ID, output)
	case verbJobs:
		return requestDispatcher(ctx, http.MethodGet, base+cloud.JobsPath, cloud.ListCloudJobsInput{}, output)
	case verbStop:
		return requestDispatcher(ctx, http.MethodPost, base+cloud.StopPath, cloud.StopCloudJobInput{ID: *job}, output)
	}
	return errUsageBurst
}

// requestCloud is one operation whose answer is printed and decoded.
func requestCloud[Input any, Output any](ctx context.Context, method string, url string, input Input, decoded *Output, output io.Writer) error {
	var printed strings.Builder
	if err := requestDispatcher(ctx, method, url, input, &printed); err != nil {
		return err
	}
	fmt.Fprint(output, printed.String())
	return json.Unmarshal([]byte(printed.String()), decoded)
}

// followJob prints the job's stage and spend each watch period until it
// ends. The host's watcher, not this verb, stops it at its cap, so leaving
// the verb leaves the job watched.
func followJob(ctx context.Context, base string, id string, output io.Writer) error {
	for {
		var ledger cloud.Ledger
		var discarded strings.Builder
		if err := requestCloud(ctx, http.MethodGet, base+cloud.JobsPath, cloud.ListCloudJobsInput{}, &ledger, &discarded); err != nil {
			return err
		}
		for _, job := range ledger.Jobs {
			if job.ID != id {
				continue
			}
			fmt.Fprintf(output, "%s %s %s: $%.4f of %s\n", time.Now().UTC().Format(time.TimeOnly), job.ID, job.Stage, job.SpendUSD, cloud.FormatUSD(job.CapUSD))
			if !job.Running() {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(cloud.WatchInterval):
		}
	}
}
