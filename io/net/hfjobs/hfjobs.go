// Copyright 2026 Candace Labs

// Package hfjobs is the Hugging Face Jobs API: run a container on Hugging
// Face's machines, list and inspect the namespace's jobs, cancel one, and
// read each flavor's price. The paths and fields are huggingface_hub's own
// (hf_api.py run_job, list_jobs, inspect_job, cancel_job, list_jobs_hardware),
// and the response shapes in the specs are what the API returned on
// 2026-10-05.
package hfjobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"regexp"
	"strings"
	"time"

	iohttp "github.com/candacelabs/csf/io/net/http"
)

const (
	// DefaultEndpoint is the Hub.
	DefaultEndpoint = "https://huggingface.co"

	jobsPath     = "/api/jobs/"
	hardwarePath = "/api/jobs/hardware"
	cancelSuffix = "/cancel"
	whoamiPath   = "/api/whoami-v2"
	// whoamiNamespace stands in for the namespace WhoAmI is about to learn.
	whoamiNamespace = "whoami"
	jobPagePath     = "/jobs/"

	headerAuthorization = "Authorization"
	headerContentType   = "Content-Type"
	headerLink          = "Link"
	bearerPrefix        = "Bearer "
	contentTypeJSON     = "application/json"
	nextRelation        = `rel="next"`

	maxAnswerBytes = 16 << 20
	// maxPages bounds a listing, so a server that always answers with a next
	// page cannot hold the caller forever.
	maxPages = 100
)

// Stage is where a job is; the values are the API's JobStage.
type Stage string

const (
	StageScheduling Stage = "SCHEDULING"
	StageRunning    Stage = "RUNNING"
	StageCompleted  Stage = "COMPLETED"
	StageCanceled   Stage = "CANCELED"
	StageError      Stage = "ERROR"
	StageDeleted    Stage = "DELETED"
)

// Terminal reports whether a job in this stage will not run further.
func (stage Stage) Terminal() bool {
	switch stage {
	case StageCompleted, StageCanceled, StageError, StageDeleted:
		return true
	}
	return false
}

var (
	// ErrNoClient reports a client built without the http capability.
	ErrNoClient = errors.New("hf jobs: the http capability is required")
	// ErrNoToken reports a client built without a token.
	ErrNoToken = errors.New("hf jobs: a Hugging Face token is required")
	// ErrNoNamespace reports a client built without the namespace jobs run in.
	ErrNoNamespace = errors.New("hf jobs: the namespace is required")
	// ErrInvalidJobID reports a job identifier that is not one.
	ErrInvalidJobID = errors.New("hf jobs: not a job identifier")
	// ErrInvalidSpec reports a job spec the API would refuse.
	ErrInvalidSpec = errors.New("hf jobs: a job needs an image, a command and a flavor")
)

var jobID = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

// Spec is one job as the API takes it. Secrets reach the container as
// environment variables the API stores encrypted and never returns.
type Spec struct {
	DockerImage    string            `json:"dockerImage"`
	Command        []string          `json:"command"`
	Arguments      []string          `json:"arguments"`
	Environment    map[string]string `json:"environment"`
	Secrets        map[string]string `json:"secrets,omitempty"`
	Flavor         string            `json:"flavor"`
	TimeoutSeconds int64             `json:"timeoutSeconds,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
}

// Status is a job's stage and the API's message about it.
type Status struct {
	Stage   Stage  `json:"stage"`
	Message string `json:"message,omitempty"`
}

// Owner is the account a job runs under.
type Owner struct {
	Name string `json:"name"`
}

// Job is one job as the API reports it.
type Job struct {
	ID          string            `json:"id"`
	CreatedAt   time.Time         `json:"createdAt"`
	StartedAt   *time.Time        `json:"startedAt,omitempty"`
	FinishedAt  *time.Time        `json:"finishedAt,omitempty"`
	DockerImage string            `json:"dockerImage,omitempty"`
	Flavor      string            `json:"flavor"`
	Labels      map[string]string `json:"labels,omitempty"`
	Status      Status            `json:"status"`
	Owner       Owner             `json:"owner"`
	// Timeout is the provider's own stop, in seconds, as it echoes the
	// spec's timeoutSeconds back; zero when it holds none.
	Timeout int64 `json:"timeout,omitempty"`
}

// Hardware is one flavor and its price.
type Hardware struct {
	Name             string `json:"name"`
	PrettyName       string `json:"prettyName"`
	CPU              string `json:"cpu"`
	RAM              string `json:"ram"`
	EphemeralStorage string `json:"ephemeralStorage"`
	// UnitCostMicroUSD is the price of one UnitLabel, in millionths of a
	// dollar.
	UnitCostMicroUSD int64  `json:"unitCostMicroUSD"`
	UnitLabel        string `json:"unitLabel"`
}

// StatusError reports a request the API refused; Message is its own text.
type StatusError struct {
	Status  int
	Message string
}

func (failure *StatusError) Error() string {
	return fmt.Sprintf("hf jobs: HTTP %d: %s", failure.Status, failure.Message)
}

// HFJobsClient calls the Jobs API for one namespace with one token. It holds
// no state between calls, so one value is safe to share.
type HFJobsClient struct {
	client    iohttp.IHTTPClient
	endpoint  string
	namespace string
	token     string
}

// HFJobsClientOption configures an [HFJobsClient].
type HFJobsClientOption func(client *HFJobsClient) error

// WithEndpoint reaches the API at endpoint instead of [DefaultEndpoint].
func WithEndpoint(endpoint string) HFJobsClientOption {
	return func(client *HFJobsClient) error {
		client.endpoint = strings.TrimRight(endpoint, "/")
		return nil
	}
}

// NewHFJobsClient builds the client for namespace's jobs, authenticated by
// token, over the http capability the binary dialed.
func NewHFJobsClient(client iohttp.IHTTPClient, namespace string, token string, options ...HFJobsClientOption) (*HFJobsClient, error) {
	jobs := &HFJobsClient{client: client, endpoint: DefaultEndpoint, namespace: namespace, token: token}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("hf jobs: nil option")
		}
		if err := option(jobs); err != nil {
			return nil, err
		}
	}
	switch {
	case jobs.client == nil:
		return nil, ErrNoClient
	case jobs.token == "":
		return nil, ErrNoToken
	case jobs.namespace == "" || strings.ContainsAny(jobs.namespace, "/?#"):
		return nil, ErrNoNamespace
	}
	return jobs, nil
}

// WhoAmI is the account token belongs to: the namespace its jobs run under.
func WhoAmI(ctx context.Context, client iohttp.IHTTPClient, token string, options ...HFJobsClientOption) (string, error) {
	probe, err := NewHFJobsClient(client, whoamiNamespace, token, options...)
	if err != nil {
		return "", err
	}
	var account struct {
		Name string `json:"name"`
	}
	if _, err := probe.call(ctx, stdhttp.MethodGet, probe.endpoint+whoamiPath, nil, &account); err != nil {
		return "", err
	}
	if account.Name == "" {
		return "", ErrNoNamespace
	}
	return account.Name, nil
}

// Namespace is the account the client's jobs run under.
func (jobs *HFJobsClient) Namespace() string { return jobs.namespace }

// JobURL is the job's own page on the Hub.
func (jobs *HFJobsClient) JobURL(id string) string {
	return jobs.endpoint + jobPagePath + jobs.namespace + "/" + id
}

// Run starts one job.
func (jobs *HFJobsClient) Run(ctx context.Context, spec Spec) (Job, error) {
	if spec.DockerImage == "" || len(spec.Command) == 0 || spec.Flavor == "" {
		return Job{}, ErrInvalidSpec
	}
	if spec.Arguments == nil {
		spec.Arguments = []string{}
	}
	if spec.Environment == nil {
		spec.Environment = map[string]string{}
	}
	body, err := json.Marshal(spec)
	if err != nil {
		return Job{}, fmt.Errorf("hf jobs: encode spec: %w", err)
	}
	var started Job
	_, err = jobs.call(ctx, stdhttp.MethodPost, jobs.endpoint+jobsPath+jobs.namespace, body, &started)
	return started, err
}

// List is every job in the namespace, following the API's pages.
func (jobs *HFJobsClient) List(ctx context.Context) ([]Job, error) {
	listed := []Job{}
	next := jobs.endpoint + jobsPath + jobs.namespace
	for page := 0; next != "" && page < maxPages; page++ {
		var batch []Job
		header, err := jobs.call(ctx, stdhttp.MethodGet, next, nil, &batch)
		if err != nil {
			return nil, err
		}
		listed = append(listed, batch...)
		next = nextPage(header.Get(headerLink))
	}
	return listed, nil
}

// Inspect reads one job.
func (jobs *HFJobsClient) Inspect(ctx context.Context, id string) (Job, error) {
	if !jobID.MatchString(id) {
		return Job{}, fmt.Errorf("%w: %q", ErrInvalidJobID, id)
	}
	var job Job
	_, err := jobs.call(ctx, stdhttp.MethodGet, jobs.endpoint+jobsPath+jobs.namespace+"/"+id, nil, &job)
	return job, err
}

// Cancel asks the API to stop one job. The API answers before the job has
// stopped; a caller that must know reads the job back.
func (jobs *HFJobsClient) Cancel(ctx context.Context, id string) error {
	if !jobID.MatchString(id) {
		return fmt.Errorf("%w: %q", ErrInvalidJobID, id)
	}
	_, err := jobs.call(ctx, stdhttp.MethodPost, jobs.endpoint+jobsPath+jobs.namespace+"/"+id+cancelSuffix, nil, nil)
	return err
}

// Hardware is every flavor and its price.
func (jobs *HFJobsClient) Hardware(ctx context.Context) ([]Hardware, error) {
	var hardware []Hardware
	_, err := jobs.call(ctx, stdhttp.MethodGet, jobs.endpoint+hardwarePath, nil, &hardware)
	return hardware, err
}

// call sends one request and decodes its JSON answer into target, when there
// is one, returning the answer's headers.
func (jobs *HFJobsClient) call(ctx context.Context, method string, url string, body []byte, target any) (stdhttp.Header, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := stdhttp.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("hf jobs: build %s %s: %w", method, url, err)
	}
	request.Header.Set(headerAuthorization, bearerPrefix+jobs.token)
	if body != nil {
		request.Header.Set(headerContentType, contentTypeJSON)
	}
	response, err := jobs.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("hf jobs: %s %s: %w", method, url, err)
	}
	defer func() { _ = response.Body.Close() }()
	content, err := io.ReadAll(io.LimitReader(response.Body, maxAnswerBytes))
	if err != nil {
		return nil, fmt.Errorf("hf jobs: read %s %s: %w", method, url, err)
	}
	if response.StatusCode < stdhttp.StatusOK || response.StatusCode >= stdhttp.StatusMultipleChoices {
		return nil, &StatusError{Status: response.StatusCode, Message: serverMessage(content)}
	}
	if target == nil {
		return response.Header, nil
	}
	if err := json.Unmarshal(content, target); err != nil {
		return nil, fmt.Errorf("hf jobs: decode %s %s: %w", method, url, err)
	}
	return response.Header, nil
}

// nextPage is the next page's URL in a Link header, or empty on the last.
func nextPage(link string) string {
	for _, part := range strings.Split(link, ",") {
		target, relation, found := strings.Cut(part, ";")
		if found && strings.Contains(relation, nextRelation) {
			return strings.Trim(strings.TrimSpace(target), "<>")
		}
	}
	return ""
}

// serverMessage is the API's {"error": ...} when it sent one, the raw body
// otherwise.
func serverMessage(content []byte) string {
	var failure struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(content, &failure) == nil && failure.Error != "" {
		return failure.Error
	}
	return strings.TrimSpace(string(content))
}
