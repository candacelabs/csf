// Copyright 2026 Candace Labs

package views

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/io/ipc/docker"
	ionet "github.com/candacelabs/csf/io/net"
	"github.com/candacelabs/csf/pkg/atomicfile"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mocks/mock_stack.go -package=mocks github.com/candacelabs/csf/services/views IContainers

const (
	// PrometheusRecordFile and GrafanaRecordFile are the two owned
	// containers' records under the state directory, mode 0600: where each
	// is and on which port.
	PrometheusRecordFile = "prometheus.json"
	GrafanaRecordFile    = "grafana.json"
	// Directory holds, under the state directory, the files the containers
	// mount (Prometheus's configuration and backfill, Grafana's provisioning
	// and dashboard) and the backfill mark.
	Directory = "views"
	// BackfillMarkFile records how far the backfill reached, in Directory.
	BackfillMarkFile = "backfill.json"

	// PrometheusImage and GrafanaImage are the pinned images, by digest.
	PrometheusImage = "prom/prometheus:v3.5.0@sha256:63805ebb8d2b3920190daf1cb14a60871b16fd38bed42b857a3182bc621f4996"
	GrafanaImage    = "grafana/grafana:12.2.1@sha256:35c41e0fd0295f5d0ee5db7e780cf33506abfaf47686196f825364889dee878b"

	// ScrapeInterval is how often Prometheus reads /metrics. The fold behind
	// it refreshes on the same period, so a shorter scrape would only repeat
	// samples; sessions run for minutes to hours, so a minute resolves them.
	ScrapeInterval = time.Minute
	// RetentionShare is the share of the free disk measured at first
	// provisioning that Prometheus may keep; the measured value and the
	// derivation are recorded in its record.
	RetentionShare = 100

	prometheusPrefix   = "csf-prometheus-"
	grafanaPrefix      = "csf-grafana-"
	prometheusPort     = "9090/tcp"
	grafanaPort        = "3000/tcp"
	prometheusData     = "/prometheus"
	grafanaData        = "/var/lib/grafana"
	prometheusMount    = "/etc/csf-prometheus"
	grafanaProvisioned = "/etc/grafana/provisioning"
	dashboardsMount    = "/var/lib/csf-dashboards"
	prometheusConfig   = "prometheus.yml"
	prometheusFiles    = "prometheus"
	grafanaFiles       = "grafana"
	dashboardFiles     = "dashboards"
	datasourceFile     = "datasources/csf.yaml"
	providerFile       = "dashboards/csf.yaml"
	dashboardName      = "csf.json"
	datasourceUID      = "csf-prometheus"
	reloadSignal       = "HUP"
	healthInterval     = 5 * time.Second
	markMode           = 0o600
	// mountedMode and mountedFileMode let the containers' own users
	// (Prometheus runs as nobody, Grafana as 472) read the files they mount;
	// none of them holds a secret.
	mountedMode     = 0o755
	mountedFileMode = 0o644
	bytesUnit       = "B"
	// Prometheus's flags the stack sets.
	flagConfigFile    = "--config.file="
	flagStoragePath   = "--storage.tsdb.path="
	flagRetentionSize = "--storage.tsdb.retention.size="
	schemeHTTP        = "http://"
	// grafanaPasswordVariable takes the admin password the record holds.
	grafanaPasswordVariable = "GF_SECURITY_ADMIN_PASSWORD="
)

// healthChecks are the commands the Engine runs in each container.
var (
	prometheusHealth = []string{"wget", "-q", "--spider", "http://127.0.0.1:9090/-/ready"}
	grafanaHealth    = []string{"wget", "-q", "--spider", "http://127.0.0.1:3000/api/health"}
)

// grafanaEnvironment configures Grafana. Viewers need no account: the
// dashboard is read-only and published only on loopback and the address csf
// serve listens on; nothing phones home.
var grafanaEnvironment = []string{
	"GF_AUTH_ANONYMOUS_ENABLED=true",
	"GF_AUTH_ANONYMOUS_ORG_ROLE=Viewer",
	"GF_USERS_ALLOW_SIGN_UP=false",
	"GF_ANALYTICS_REPORTING_ENABLED=false",
	"GF_ANALYTICS_CHECK_FOR_UPDATES=false",
	"GF_ANALYTICS_CHECK_FOR_PLUGIN_UPDATES=false",
	"GF_NEWS_NEWS_FEED_ENABLED=false",
	// Grafana otherwise downloads its preinstalled plugins on a first start.
	"GF_PLUGINS_PREINSTALL_DISABLED=true",
	"GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH=" + dashboardsMount + "/" + dashboardName,
}

var (
	// ErrInvalidOption reports a nil option or a value the views cannot use.
	ErrInvalidOption = errors.New("views: invalid option")
	// ErrMissingCapability reports views or a stack built without what they
	// need: the corpus; or the state directory, containers, listener,
	// free-disk measure, scrape target or an address a container reaches.
	ErrMissingCapability = errors.New("views: a required capability is missing")
)

// IContainers is the part of the container capability the stack uses: the
// owned-service lifecycle, a signal to reread configuration, and the
// promtool run the backfill is.
type IContainers interface {
	docker.IServices
	SignalService(ctx context.Context, name string, signal string) error
	Exec(ctx context.Context, name string, spec docker.ExecSpec) (docker.ExecResult, error)
}

var _ IContainers = (*docker.ContainerHost)(nil)

// FreeBytes measures the free bytes of the filesystem holding path.
type FreeBytes func(path string) (uint64, error)

// Retention is Prometheus's own part of its record: how many bytes it may
// keep, and how that was derived.
type Retention struct {
	Bytes      uint64 `json:"bytes"`
	Derivation string `json:"derivation"`
}

// GrafanaAccess is Grafana's own part of its record: the admin password.
type GrafanaAccess struct {
	Password string `json:"password"`
}

// StackRecord is the stack as Ensure leaves it: both owned containers'
// records, the addresses they are published on, csf serve's address as
// Prometheus scrapes it, and how far the backfill reached.
type StackRecord struct {
	Prometheus        docker.OwnedRecord[Retention]     `json:"prometheus"`
	Grafana           docker.OwnedRecord[GrafanaAccess] `json:"grafana"`
	Addresses         []string                          `json:"addresses"`
	ScrapeTarget      string                            `json:"scrape_target"`
	BackfilledThrough time.Time                         `json:"backfilled_through"`
}

// GrafanaURL is Grafana's address on host, one of the addresses it is
// published on.
func (record StackRecord) GrafanaURL(host string) string {
	return schemeHTTP + net.JoinHostPort(host, strconv.Itoa(int(record.Grafana.Port)))
}

// backfillMark is BackfillMarkFile.
type backfillMark struct {
	Through time.Time `json:"through"`
}

// Stack is the Prometheus and Grafana a state directory owns: two
// [docker.OwnedService] containers and the files they mount.
type Stack struct {
	state      string
	containers IContainers
	listener   ionet.IListener
	clock      clock.IClock
	free       FreeBytes
	addresses  []netip.Addr
	target     string
	prometheus *docker.OwnedService[Retention]
	grafana    *docker.OwnedService[GrafanaAccess]
}

// StackOption configures a [Stack].
type StackOption func(stack *Stack) error

// WithStateDirectory names the state directory the records and the mounted
// files live in. Required.
func WithStateDirectory(directory string) StackOption {
	return func(stack *Stack) error {
		if !filepath.IsAbs(directory) {
			return fmt.Errorf("%w: the state directory must be absolute, got %q", ErrInvalidOption, directory)
		}
		stack.state = directory
		return nil
	}
}

// WithContainers grants the container capability. Required.
func WithContainers(containers IContainers) StackOption {
	return func(stack *Stack) error {
		if containers == nil {
			return fmt.Errorf("%w: nil containers", ErrInvalidOption)
		}
		stack.containers = containers
		return nil
	}
}

// WithListener grants the network capability the first Ensure probes free
// ports with. Required.
func WithListener(listener ionet.IListener) StackOption {
	return func(stack *Stack) error {
		if listener == nil {
			return fmt.Errorf("%w: nil listener", ErrInvalidOption)
		}
		stack.listener = listener
		return nil
	}
}

// WithFreeBytes grants the free-disk measure retention is derived from.
// Required.
func WithFreeBytes(free FreeBytes) StackOption {
	return func(stack *Stack) error {
		if free == nil {
			return fmt.Errorf("%w: nil free-disk measure", ErrInvalidOption)
		}
		stack.free = free
		return nil
	}
}

// WithPublishedAddresses names the host addresses Prometheus and Grafana are
// published on: loopback and the address csf serve already listens on
// beyond it. Required; the first non-loopback one is also how Grafana's
// container reaches Prometheus.
func WithPublishedAddresses(addresses ...netip.Addr) StackOption {
	return func(stack *Stack) error {
		for _, address := range addresses {
			if !address.IsValid() || address.IsUnspecified() {
				return fmt.Errorf("%w: published address %q", ErrInvalidOption, address)
			}
		}
		stack.addresses = addresses
		return nil
	}
}

// WithScrapeTarget names csf serve's host:port as a container reaches it,
// where Prometheus scrapes /metrics. Required.
func WithScrapeTarget(target string) StackOption {
	return func(stack *Stack) error {
		if _, _, err := net.SplitHostPort(target); err != nil {
			return fmt.Errorf("%w: scrape target %q: %w", ErrInvalidOption, target, err)
		}
		stack.target = target
		return nil
	}
}

// WithStackClock replaces the clock the health wait is paced by.
func WithStackClock(source clock.IClock) StackOption {
	return func(stack *Stack) error {
		if source == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidOption)
		}
		stack.clock = source
		return nil
	}
}

// NewStack validates the whole option set and defines the two owned
// containers.
func NewStack(options ...StackOption) (*Stack, error) {
	stack := &Stack{clock: clock.NewSystemClock()}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(stack); err != nil {
			return nil, err
		}
	}
	if stack.state == "" || stack.containers == nil || stack.listener == nil || stack.free == nil || stack.target == "" || stack.reachable() == nil {
		return nil, ErrMissingCapability
	}
	capabilities := []docker.OwnedServiceOption{docker.WithServices(stack.containers), docker.WithListener(stack.listener), docker.WithClock(stack.clock)}
	var err error
	if stack.prometheus, err = docker.NewOwnedService(docker.OwnedServiceDefinition[Retention]{
		StateDirectory: stack.state, RecordFile: PrometheusRecordFile, NamePrefix: prometheusPrefix,
		DataDirectory: prometheusData, Image: PrometheusImage, Host: stack.addresses[0],
		NewSettings: stack.retention, Spec: stack.prometheusSpec,
	}, capabilities...); err != nil {
		return nil, err
	}
	if stack.grafana, err = docker.NewOwnedService(docker.OwnedServiceDefinition[GrafanaAccess]{
		StateDirectory: stack.state, RecordFile: GrafanaRecordFile, NamePrefix: grafanaPrefix,
		DataDirectory: grafanaData, Image: GrafanaImage, Host: stack.addresses[0],
		NewSettings: func() (GrafanaAccess, error) { return GrafanaAccess{Password: rand.Text()}, nil },
		Spec:        stack.grafanaSpec,
	}, capabilities...); err != nil {
		return nil, err
	}
	return stack, nil
}

// reachable is the first published address a container reaches the host
// on, or nil: a container's loopback is its own.
func (stack *Stack) reachable() *netip.Addr {
	for _, address := range stack.addresses {
		if !address.IsLoopback() {
			return &address
		}
	}
	return nil
}

// retention derives Prometheus's retention from the free disk now, on its
// first start only.
func (stack *Stack) retention() (Retention, error) {
	free, err := stack.free(stack.state)
	if err != nil {
		return Retention{}, fmt.Errorf("views: measure free disk: %w", err)
	}
	return Retention{Bytes: free / RetentionShare, Derivation: fmt.Sprintf("1/%d of the %d bytes free on the state directory's filesystem at %s",
		RetentionShare, free, stack.clock.Now().UTC().Format(time.RFC3339))}, nil
}

// Ensure writes the files the containers mount and brings Prometheus, then
// Grafana, up: created on their first start, reused on every later one. The
// files are rewritten each time, so a new binary's dashboard and scrape
// target take effect; Prometheus is told to reread its configuration when
// that changed. It returns once both health checks pass.
func (stack *Stack) Ensure(ctx context.Context) (StackRecord, error) {
	changed, err := stack.writePrometheusFiles()
	if err != nil {
		return StackRecord{}, err
	}
	prometheus, err := stack.prometheus.Ensure(ctx)
	if err != nil {
		return StackRecord{}, err
	}
	if err := stack.writeGrafanaFiles(prometheus.Port); err != nil {
		return StackRecord{}, err
	}
	grafana, err := stack.grafana.Ensure(ctx)
	if err != nil {
		return StackRecord{}, err
	}
	if changed {
		if err := stack.containers.SignalService(ctx, prometheus.Container, reloadSignal); err != nil {
			return StackRecord{}, err
		}
	}
	return stack.record(prometheus, grafana)
}

// Record reads the stack as recorded, without Grafana's password, or
// reports [docker.ErrServiceNotOwned].
func (stack *Stack) Record() (StackRecord, error) {
	prometheus, err := stack.prometheus.Record()
	if err != nil {
		return StackRecord{}, err
	}
	grafana, err := stack.grafana.Record()
	if err != nil {
		return StackRecord{}, err
	}
	grafana.Settings = GrafanaAccess{}
	return stack.record(prometheus, grafana)
}

func (stack *Stack) record(prometheus docker.OwnedRecord[Retention], grafana docker.OwnedRecord[GrafanaAccess]) (StackRecord, error) {
	record := StackRecord{Prometheus: prometheus, Grafana: grafana, ScrapeTarget: stack.target}
	for _, address := range stack.addresses {
		record.Addresses = append(record.Addresses, address.String())
	}
	content, err := os.ReadFile(filepath.Join(stack.state, Directory, BackfillMarkFile))
	if errors.Is(err, os.ErrNotExist) {
		return record, nil
	}
	if err != nil {
		return StackRecord{}, err
	}
	var mark backfillMark
	if err := json.Unmarshal(content, &mark); err != nil {
		return StackRecord{}, fmt.Errorf("views: decode %s: %w", BackfillMarkFile, err)
	}
	record.BackfilledThrough = mark.Through
	return record, nil
}

// MarkBackfilled records how far the backfill reached.
func (stack *Stack) MarkBackfilled(through time.Time) error {
	content, err := json.Marshal(backfillMark{Through: through.UTC()})
	if err != nil {
		return err
	}
	path := filepath.Join(stack.state, Directory, BackfillMarkFile)
	if err := os.MkdirAll(filepath.Dir(path), mountedMode); err != nil {
		return err
	}
	return atomicfile.WriteFile(path, content, markMode)
}

// published splits the published addresses beyond the first, which the
// owned service publishes on.
func (stack *Stack) published() []netip.Addr { return stack.addresses[1:] }

func (stack *Stack) prometheusSpec(record docker.OwnedRecord[Retention]) docker.ServiceSpec {
	return docker.ServiceSpec{
		Mounts: []docker.Mount{{Source: filepath.Join(stack.state, Directory, prometheusFiles), Target: prometheusMount, ReadOnly: true}},
		Command: []string{
			flagConfigFile + prometheusMount + "/" + prometheusConfig,
			flagStoragePath + prometheusData,
			flagRetentionSize + strconv.FormatUint(record.Settings.Bytes, 10) + bytesUnit,
		},
		Port: prometheusPort, ExtraHostAddresses: stack.published(),
		HealthCheck: prometheusHealth, HealthInterval: healthInterval,
	}
}

func (stack *Stack) grafanaSpec(record docker.OwnedRecord[GrafanaAccess]) docker.ServiceSpec {
	return docker.ServiceSpec{
		Environment: append([]string{grafanaPasswordVariable + record.Settings.Password}, grafanaEnvironment...),
		Mounts: []docker.Mount{
			{Source: filepath.Join(stack.state, Directory, grafanaFiles), Target: grafanaProvisioned, ReadOnly: true},
			{Source: filepath.Join(stack.state, Directory, dashboardFiles), Target: dashboardsMount, ReadOnly: true},
		},
		Port: grafanaPort, ExtraHostAddresses: stack.published(),
		HealthCheck: grafanaHealth, HealthInterval: healthInterval,
	}
}

// writePrometheusFiles writes Prometheus's configuration and reports
// whether it changed.
func (stack *Stack) writePrometheusFiles() (bool, error) {
	root := filepath.Join(stack.state, Directory)
	path := filepath.Join(root, prometheusFiles, prometheusConfig)
	previous, _ := os.ReadFile(path)
	configuration := []byte(fmt.Sprintf(prometheusTemplate, ScrapeInterval, ScrapeInterval/2, stack.target))
	if err := writeMounted(root, path, configuration); err != nil {
		return false, err
	}
	return !bytes.Equal(previous, configuration), nil
}

// writeGrafanaFiles writes Grafana's provisioning, its datasource being
// Prometheus on the address a container reaches the host on, and the
// dashboard.
func (stack *Stack) writeGrafanaFiles(prometheusPort uint16) error {
	root := filepath.Join(stack.state, Directory)
	files := map[string][]byte{
		filepath.Join(root, grafanaFiles, datasourceFile): []byte(fmt.Sprintf(datasourceTemplate, datasourceUID,
			schemeHTTP+net.JoinHostPort(stack.reachable().String(), strconv.Itoa(int(prometheusPort))))),
		filepath.Join(root, grafanaFiles, providerFile):    []byte(fmt.Sprintf(providerTemplate, dashboardsMount)),
		filepath.Join(root, dashboardFiles, dashboardName): dashboardDocument,
	}
	for path, content := range files {
		if err := writeMounted(root, path, content); err != nil {
			return err
		}
	}
	// Grafana reads every provisioning kind and logs an error for a missing
	// directory; these two are provisioned empty.
	for _, empty := range grafanaEmptyProvisioning {
		if err := os.MkdirAll(filepath.Join(root, grafanaFiles, empty), mountedMode); err != nil {
			return err
		}
	}
	return nil
}

// grafanaEmptyProvisioning are the provisioning kinds CSF provisions nothing of.
var grafanaEmptyProvisioning = []string{"plugins", "alerting"}

// The mounted configuration files.
const (
	prometheusTemplate = `global:
  scrape_interval: %s
  scrape_timeout: %s
scrape_configs:
  - job_name: csf
    metrics_path: /metrics
    static_configs:
      - targets: [%q]
`
	datasourceTemplate = `apiVersion: 1
datasources:
  - name: CSF Prometheus
    uid: %s
    type: prometheus
    access: proxy
    url: %s
    isDefault: true
    editable: false
`
	providerTemplate = `apiVersion: 1
providers:
  - name: csf
    type: file
    disableDeletion: true
    allowUiUpdates: false
    updateIntervalSeconds: 30
    options:
      path: %s
`
)

// writeMounted writes a file a container mounts, readable by its user, with
// every directory from root down to it traversable.
func writeMounted(root string, path string, content []byte) error {
	for directory := filepath.Dir(path); ; directory = filepath.Dir(directory) {
		if err := os.MkdirAll(directory, mountedMode); err != nil {
			return err
		}
		if err := os.Chmod(directory, mountedMode); err != nil {
			return err
		}
		if directory == root || directory == filepath.Dir(directory) {
			break
		}
	}
	return atomicfile.WriteFile(path, content, mountedFileMode)
}
