// Copyright 2026 Candace Labs

package cloud

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/io/net/hfjobs"
	"github.com/candacelabs/csf/pkg/privatefile"
	"github.com/candacelabs/csf/services/dispatch"
)

// A burst: N fixer sessions in one disposable cloud job, each a whole
// harness. The job runs burst.sh in the session image; this side picks the
// slices, builds the spec with the credentials as job secrets, launches it
// under its cap and holds the slices here so this host never runs them too.
const (
	// ProvidersFile holds the operator's provider credentials, under the
	// harness state directory, readable by its owner only.
	ProvidersFile = "providers.json"

	// The job's environment: what burst.sh reads.
	environmentName       = "BURST_NAME"
	environmentRecipes    = "BURST_RECIPES"
	environmentRepository = "BURST_REPOSITORY"
	environmentDataset    = "CORPUS_DATASET"
	secretGitHub          = "GH_TOKEN"
	secretHuggingFace     = "HF_TOKEN"
	// A suite shard's environment (#416): the mode burst.sh switches on, the
	// build to replay on, the suite and the replay jobs.
	environmentMode  = "BURST_MODE"
	environmentBuild = "BURST_BUILD"
	environmentSuite = "BURST_SUITE"
	environmentJobs  = "BURST_JOBS"
	modeSuite        = "suite"

	labelKind  = "csf"
	labelBurst = "burst"
	kindBurst  = "burst"
	kindSuite  = "suite"
	// A suite shard is named for its build and when it launched.
	suiteNamePrefix = "suite-"

	shell        = "bash"
	shellCommand = "-c"

	maxProvidersBytes = 1 << 20
	// maxSessions bounds one burst: the flavors measured on 2026-10-05 top
	// out at 32 vCPU, and a session's Bazel build wants several.
	maxSessions = 8
	// redacted stands in for a secret's value in a dry run's spec.
	redacted = "(secret)"
	// heldOutFormat stands in for a suite shard's held-out suite and jobs.
	heldOutFormat = "(held out, %d bytes)"
	// The providers.json keys a burst reads, as a dry run names a missing one.
	KeyGitHubToken         = "burst.github_token"
	KeyHuggingFaceToken    = "burst.hf_token"
	KeyCorpusDataset       = "burst.corpus_dataset"
	KeyExecutorEnvironment = "burst.executor_environment"
	// A burst is named for when it launched: burst-20261005-051053.
	burstNamePrefix = "burst-"
	burstNameTime   = "20060102-150405"
	// missingImage and missingRepository name what serve was not given.
	missingImage      = "serve -burst-image"
	missingRepository = "serve -burst-repository"
)

//go:embed burst.sh
var burstScript string

var (
	// ErrNoImage reports a burst on a service granted no session image.
	ErrNoImage = errors.New("cloud: no session image is published for bursts; give serve -burst-image")
	// ErrNoRepository reports a burst on a service granted no repository.
	ErrNoRepository = errors.New("cloud: no repository is named for bursts; give serve -burst-repository")
	// ErrNoQueue reports a burst on a service granted no dispatcher.
	ErrNoQueue = errors.New("cloud: bursts need the slice dispatcher")
	// ErrNoReadySlice reports a burst with nothing to run.
	ErrNoReadySlice = errors.New("cloud: no slice is ready and uncontended")
	// ErrMissingCredentials reports a launch the operator's credentials do
	// not cover yet.
	ErrMissingCredentials = errors.New("cloud: providers.json lacks credentials a burst needs")
	// ErrInvalidSessions reports a session count out of range.
	ErrInvalidSessions = fmt.Errorf("cloud: a burst runs between 1 and %d sessions", maxSessions)
)

// BurstCredentials are the "burst" object of providers.json: what a burst job
// receives as job secrets. None is ever written into the image or the record.
type BurstCredentials struct {
	// GitHubToken is a fine-grained token for the repository, with contents
	// and pull requests write.
	GitHubToken string `json:"github_token"`
	// HuggingFaceToken writes the run logs to the corpus dataset.
	HuggingFaceToken string `json:"hf_token"`
	// CorpusDataset is the private dataset the run logs go to, owner/name.
	CorpusDataset string `json:"corpus_dataset"`
	// ExecutorEnvironment is the executor's model credential, by the
	// environment variable it reads, such as ANTHROPIC_API_KEY.
	ExecutorEnvironment map[string]string `json:"executor_environment"`
}

// providers is providers.json; other keys belong to other readers.
type providers struct {
	Burst BurstCredentials `json:"burst"`
}

// missing names every key a launch needs that the credentials lack.
func (credentials BurstCredentials) missing() []string {
	var absent []string
	for _, required := range []struct{ key, value string }{
		{KeyGitHubToken, credentials.GitHubToken},
		{KeyHuggingFaceToken, credentials.HuggingFaceToken},
		{KeyCorpusDataset, credentials.CorpusDataset},
	} {
		if strings.TrimSpace(required.value) == "" {
			absent = append(absent, required.key)
		}
	}
	if len(credentials.ExecutorEnvironment) == 0 {
		absent = append(absent, KeyExecutorEnvironment)
	}
	return absent
}

// burstSettings is what the binary grants bursts.
type burstSettings struct {
	image      string
	repository string
	ready      func(ctx context.Context, limit int) ([]dispatch.ReadySlice, error)
	hold       func(ctx context.Context, slice string, reason string) error
}

// WithBurstImage grants the published session image burst jobs run.
func WithBurstImage(image string) Option {
	return func(jobs *CloudJobs) error {
		jobs.burst.image = image
		return nil
	}
}

// WithBurstRepository names the repository burst jobs clone and open their
// pull requests on, owner/name.
func WithBurstRepository(repository string) Option {
	return func(jobs *CloudJobs) error {
		jobs.burst.repository = repository
		return nil
	}
}

// WithSliceQueue grants the dispatcher: ready lists the slices that may run
// away from this host, hold holds one here with a reason.
func WithSliceQueue(ready func(ctx context.Context, limit int) ([]dispatch.ReadySlice, error), hold func(ctx context.Context, slice string, reason string) error) Option {
	return func(jobs *CloudJobs) error {
		jobs.burst.ready, jobs.burst.hold = ready, hold
		return nil
	}
}

// LaunchBurstInput is one burst.
type LaunchBurstInput struct {
	Sessions int     `json:"sessions" jsonschema:"how many fixer sessions the job runs, one ready slice each"`
	Flavor   string  `json:"flavor" jsonschema:"the provider's hardware flavor, such as cpu-xl"`
	CapUSD   float64 `json:"cap_usd" jsonschema:"the job's spend cap in dollars; the provider's timeout is the minutes it buys, and CSF cancels at it"`
	DryRun   bool    `json:"dry_run,omitempty" jsonschema:"build and show the job spec, secrets redacted, and launch nothing"`
}

// LaunchBurstOutput is the burst: its spec (secrets redacted), the slices it
// runs, what providers.json still lacks, and the job once launched.
type LaunchBurstOutput struct {
	DryRun  bool        `json:"dry_run"`
	Spec    hfjobs.Spec `json:"spec"`
	Slices  []string    `json:"slices"`
	Missing []string    `json:"missing"`
	Job     *CloudJob   `json:"job,omitempty"`
}

// LaunchBurst picks the ready slices, builds the job and, unless it is a dry
// run, launches it under its cap and holds its slices on this host.
func (jobs *CloudJobs) LaunchBurst(ctx context.Context, input LaunchBurstInput) (LaunchBurstOutput, error) {
	if input.Sessions < 1 || input.Sessions > maxSessions {
		return LaunchBurstOutput{}, ErrInvalidSessions
	}
	if jobs.burst.ready == nil || jobs.burst.hold == nil {
		return LaunchBurstOutput{}, ErrNoQueue
	}
	ready, err := jobs.burst.ready(ctx, input.Sessions)
	if err != nil {
		return LaunchBurstOutput{}, err
	}
	if len(ready) == 0 {
		return LaunchBurstOutput{}, ErrNoReadySlice
	}
	credentials, err := jobs.credentials()
	if err != nil {
		return LaunchBurstOutput{}, err
	}
	name := burstNamePrefix + jobs.clock.Now().UTC().Format(burstNameTime)
	spec, ids, branches, err := jobs.burstSpec(name, input.Flavor, ready, credentials)
	if err != nil {
		return LaunchBurstOutput{}, err
	}
	output, err := jobs.launchSpec(ctx, spec, name, input.CapUSD, input.DryRun, ids, branches, credentials)
	if err != nil || input.DryRun {
		return output, err
	}
	var unheld []error
	for _, id := range ids {
		if err := jobs.burst.hold(ctx, id, "running in cloud job "+output.Job.ID); err != nil {
			unheld = append(unheld, fmt.Errorf("hold %s: %w", id, err))
		}
	}
	return output, errors.Join(unheld...)
}

// LaunchSuiteShardInput is one shard of an evaluation suite's replays run
// as a burst job (#416): the replay jobs and the suite as the evaluate
// package encodes them, the build they replay on, and the job's cap.
type LaunchSuiteShardInput struct {
	Build   string          `json:"build"`
	Suite   json.RawMessage `json:"suite"`
	Jobs    json.RawMessage `json:"jobs"`
	Tickets []string        `json:"tickets"`
	Flavor  string          `json:"flavor"`
	CapUSD  float64         `json:"cap_usd"`
	DryRun  bool            `json:"dry_run,omitempty"`
}

// LaunchSuiteShard builds the job that replays a shard of the suite on a
// cloud node and, unless it is a dry run, launches it under its cap. The job
// runs burst.sh in suite mode: the build's released binary, replays at the
// suite's budget, pushes that never leave the job, no merge, and the replays
// uploaded beside the run logs for csf eval record.
func (jobs *CloudJobs) LaunchSuiteShard(ctx context.Context, input LaunchSuiteShardInput) (LaunchBurstOutput, error) {
	credentials, err := jobs.credentials()
	if err != nil {
		return LaunchBurstOutput{}, err
	}
	name := suiteNamePrefix + input.Build + "-" + jobs.clock.Now().UTC().Format(burstNameTime)
	spec := jobs.jobSpec(name, input.Flavor, credentials, map[string]string{
		environmentMode: modeSuite, environmentBuild: input.Build,
		environmentSuite: string(input.Suite), environmentJobs: string(input.Jobs),
	})
	spec.Labels[labelKind] = kindSuite
	return jobs.launchSpec(ctx, spec, name, input.CapUSD, input.DryRun, input.Tickets, nil, credentials)
}

// launchSpec sets the job's provider timeout from the cap at the flavor's
// price, names what the credentials and the binary's grants still lack, and,
// unless it is a dry run, launches the job under its cap.
func (jobs *CloudJobs) launchSpec(ctx context.Context, spec hfjobs.Spec, name string, capUSD float64, dryRun bool, ids []string, branches []string, credentials BurstCredentials) (LaunchBurstOutput, error) {
	// The provider's timeout is shown on a dry run too: the minutes the cap
	// buys at the flavor's price now. The price is the provider's, read
	// without the record, so a dry run needs no running service.
	price, err := jobs.price(ctx, spec.Flavor)
	if err != nil {
		return LaunchBurstOutput{}, err
	}
	if spec.TimeoutSeconds, err = timeoutFor(capUSD, price); err != nil {
		return LaunchBurstOutput{}, err
	}
	output := LaunchBurstOutput{DryRun: dryRun, Spec: redact(spec), Slices: ids, Missing: credentials.missing()}
	if jobs.burst.image == "" {
		output.Missing = append(output.Missing, missingImage)
	}
	if jobs.burst.repository == "" {
		output.Missing = append(output.Missing, missingRepository)
	}
	if output.Missing == nil {
		output.Missing = []string{}
	}
	if dryRun {
		return output, nil
	}
	switch {
	case jobs.burst.image == "":
		return output, ErrNoImage
	case jobs.burst.repository == "":
		return output, ErrNoRepository
	case len(output.Missing) > 0:
		return output, fmt.Errorf("%w: %s", ErrMissingCredentials, strings.Join(output.Missing, ", "))
	}
	purpose := fmt.Sprintf("%s: %s", name, strings.Join(ids, ", "))
	var job CloudJob
	err = jobs.do(ctx, func(ctx context.Context, ledger *Ledger) error {
		job, err = jobs.launch(ctx, ledger, spec, purpose, capUSD, ids, branches)
		return err
	})
	if err != nil {
		return output, err
	}
	output.Job = &job
	return output, nil
}

// jobSpec is a job of the session image running burst.sh with the names in
// environment beside the repository and corpus dataset, the credentials as
// its secrets.
func (jobs *CloudJobs) jobSpec(name string, flavor string, credentials BurstCredentials, environment map[string]string) hfjobs.Spec {
	secrets := map[string]string{secretGitHub: credentials.GitHubToken, secretHuggingFace: credentials.HuggingFaceToken}
	for key, value := range credentials.ExecutorEnvironment {
		secrets[key] = value
	}
	environment[environmentName] = name
	environment[environmentRepository] = jobs.burst.repository
	environment[environmentDataset] = credentials.CorpusDataset
	return hfjobs.Spec{
		DockerImage: jobs.burst.image,
		Command:     []string{shell, shellCommand, burstScript},
		Arguments:   []string{},
		Environment: environment,
		Secrets:     secrets,
		Flavor:      flavor,
		Labels:      map[string]string{labelKind: kindBurst, labelBurst: name},
	}
}

// burstSpec is the job: the session image running burst.sh, the recipes and
// names in its environment, the credentials as its secrets. It returns the
// slices and their work branches beside it.
func (jobs *CloudJobs) burstSpec(name string, flavor string, ready []dispatch.ReadySlice, credentials BurstCredentials) (hfjobs.Spec, []string, []string, error) {
	recipes := make([]json.RawMessage, 0, len(ready))
	ids := make([]string, 0, len(ready))
	var branches []string
	for _, slice := range ready {
		encoded, err := protojson.Marshal(slice.Recipe)
		if err != nil {
			return hfjobs.Spec{}, nil, nil, fmt.Errorf("cloud: encode %s's recipe: %w", slice.SliceID, err)
		}
		recipes = append(recipes, encoded)
		ids = append(ids, slice.SliceID)
		if branch := slice.Recipe.GetWorkspace().GetBranch(); branch != "" {
			branches = append(branches, branch)
		}
	}
	encoded, err := json.Marshal(recipes)
	if err != nil {
		return hfjobs.Spec{}, nil, nil, err
	}
	return jobs.jobSpec(name, flavor, credentials, map[string]string{environmentRecipes: string(encoded)}), ids, branches, nil
}

// credentials reads the burst credentials; a missing file is none at all.
func (jobs *CloudJobs) credentials() (BurstCredentials, error) {
	path := filepath.Join(jobs.directory, ProvidersFile)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return BurstCredentials{}, nil
	}
	content, err := privatefile.Read(path, maxProvidersBytes)
	if err != nil {
		return BurstCredentials{}, fmt.Errorf("cloud: read %s: %w", ProvidersFile, err)
	}
	var read providers
	if err := json.Unmarshal(content, &read); err != nil {
		return BurstCredentials{}, fmt.Errorf("cloud: decode %s: %w", ProvidersFile, err)
	}
	return read.Burst, nil
}

// redact is the spec with every secret's value replaced, for showing, and
// a suite shard's suite and jobs too: they name the held-out tickets, which
// must not land in a session's event log, where the miners read.
func redact(spec hfjobs.Spec) hfjobs.Spec {
	shown := spec
	shown.Environment = maps.Clone(spec.Environment)
	for _, held := range []string{environmentSuite, environmentJobs} {
		if value, set := shown.Environment[held]; set {
			shown.Environment[held] = fmt.Sprintf(heldOutFormat, len(value))
		}
	}
	shown.Secrets = map[string]string{}
	keys := make([]string, 0, len(spec.Secrets))
	for key := range spec.Secrets {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		shown.Secrets[key] = redacted
	}
	return shown
}
