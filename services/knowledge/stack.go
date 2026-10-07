// Copyright 2026 Candace Labs

package knowledge

import (
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	"github.com/candacelabs/csf/io/ipc/docker"
)

// The search index: OpenSearch, one node, on loopback only.
const (
	// SearchRecordFile is the index's record under the state directory.
	SearchRecordFile = "search.json"
	// SearchImage is the pinned OpenSearch image, by digest.
	SearchImage = "opensearchproject/opensearch:3.9.0@sha256:adfa61f85025d06b4aeb562e7e74fde7e31c437039c93c3862c17e9acebd6c7c"
	// SearchIndex is the index the knowledge projection writes its chunks to.
	SearchIndex = "csf-knowledge"

	searchPrefix        = "csf-opensearch-"
	searchDataDirectory = "/usr/share/opensearch/data"
	searchPort          = "9200/tcp"
	searchHealth        = "http://127.0.0.1:9200/_cluster/health?wait_for_status=yellow&timeout=1s"
	// A one-node index on a host whose disk is the binding limit: the heap is
	// fixed, and the disk watermarks are absolute, because OpenSearch's
	// default flood stage, 95% used, blocks every write on a disk that is
	// already past it (measured 2026-10-05: a 97%-full disk refused index
	// creation with FORBIDDEN/10). The index stops taking writes 2 GB before
	// the disk is full.
	searchHeap      = "OPENSEARCH_JAVA_OPTS=-Xms1g -Xmx1g"
	searchWatermark = "cluster.routing.allocation.disk.watermark."
	searchLow       = searchWatermark + "low=6gb"
	searchHigh      = searchWatermark + "high=4gb"
	searchFlood     = searchWatermark + "flood_stage=2gb"
	searchSingle    = "discovery.type=single-node"
	searchNoSecure  = "DISABLE_SECURITY_PLUGIN=true"
	searchNoDemo    = "DISABLE_INSTALL_DEMO_CONFIG=true"

	healthProgram = "curl"
	healthSilent  = "--silent"
	healthFail    = "--fail"
	httpScheme    = "http"
)

// The reranker: a llama.cpp server with a cross-encoder, on the GPU.
const (
	// RerankerRecordFile is the reranker's record under the state directory.
	RerankerRecordFile = "reranker.json"
	// RerankerImage is the pinned llama.cpp server image (CUDA 13, which the
	// host's RTX 5070 needs), by digest.
	RerankerImage = "ghcr.io/ggml-org/llama.cpp:server-cuda13-b11277@sha256:f9f7c9f689cf6dba993075b74697253c46c639f7c5956a3421fcabf7e8aef715"
	// RerankerModel is the cross-encoder: bge-reranker-v2-m3, 568M parameters,
	// quantized to 8 bits (636 MB), pinned to a commit of its GGUF repository.
	RerankerModel    = "bge-reranker-v2-m3-Q8_0"
	rerankerModelURL = "https://huggingface.co/gpustack/bge-reranker-v2-m3-GGUF/resolve/3093af03b1a635e67b084b1d8c03c5f5e020fd05/bge-reranker-v2-m3-Q8_0.gguf"

	rerankerPrefix        = "csf-reranker-"
	rerankerDataDirectory = "/models"
	rerankerModelPath     = rerankerDataDirectory + "/" + RerankerModel + ".gguf"
	rerankerPort          = "8080"
	rerankerContainerPort = rerankerPort + "/tcp"
	rerankerHealth        = "http://127.0.0.1:" + rerankerPort + "/health"
	// A query reranks 30 chunks of at most 2,000 runes: one 8,192-token
	// context and batch hold a pair at a time with room to spare.
	rerankerContext = "8192"
	allLayers       = "99"
	listenAll       = "0.0.0.0"
	labelTrue       = "true"

	// A first start downloads the model before the server answers its health
	// check; the owned service's own wait bounds it.
	healthInterval = 5 * time.Second
)

// loopback is the only address the index and the reranker are published on;
// the host that owns them is their only client.
var loopback = netip.MustParseAddr("127.0.0.1")

// rerankerCommand serves the model, downloaded into the data volume on the
// first start, with every layer on the GPU and the rerank endpoint on.
var rerankerCommand = []string{"-m", rerankerModelPath, "--model-url", rerankerModelURL, "--reranking",
	"--host", listenAll, "--port", rerankerPort, "-ngl", allLayers,
	"-c", rerankerContext, "-b", rerankerContext, "-ub", rerankerContext}

// SearchSettings is the index's own part of its record.
type SearchSettings struct {
	Index string `json:"index"`
}

// RerankerSettings is the reranker's own part of its record.
type RerankerSettings struct {
	Model string `json:"model"`
}

// Endpoint is the URL a record's published port answers on.
func Endpoint(location docker.OwnedLocation) string {
	return (&url.URL{Scheme: httpScheme, Host: net.JoinHostPort(location.Host, strconv.Itoa(int(location.Port)))}).String()
}

// NewOwnedSearch is the OpenSearch node the state directory owns.
func NewOwnedSearch(state string, options ...docker.OwnedServiceOption) (*docker.OwnedService[SearchSettings], error) {
	return docker.NewOwnedService(docker.OwnedServiceDefinition[SearchSettings]{
		StateDirectory: state,
		RecordFile:     SearchRecordFile,
		NamePrefix:     searchPrefix,
		DataDirectory:  searchDataDirectory,
		Image:          SearchImage,
		Host:           loopback,
		NewSettings:    func() (SearchSettings, error) { return SearchSettings{Index: SearchIndex}, nil },
		Spec: func(record docker.OwnedRecord[SearchSettings]) docker.ServiceSpec {
			return docker.ServiceSpec{
				Environment:    []string{searchSingle, searchNoSecure, searchNoDemo, searchHeap, searchLow, searchHigh, searchFlood},
				Port:           searchPort,
				HealthCheck:    []string{healthProgram, healthSilent, healthFail, searchHealth},
				HealthInterval: healthInterval,
			}
		},
	}, options...)
}

// NewOwnedReranker is the reranker the state directory owns. It holds the
// GPU while idle, so it is labelled a model server: GPU consumers that yield
// to others do not yield to it.
func NewOwnedReranker(state string, options ...docker.OwnedServiceOption) (*docker.OwnedService[RerankerSettings], error) {
	return docker.NewOwnedService(docker.OwnedServiceDefinition[RerankerSettings]{
		StateDirectory: state,
		RecordFile:     RerankerRecordFile,
		NamePrefix:     rerankerPrefix,
		DataDirectory:  rerankerDataDirectory,
		Image:          RerankerImage,
		Host:           loopback,
		NewSettings:    func() (RerankerSettings, error) { return RerankerSettings{Model: RerankerModel}, nil },
		Spec: func(record docker.OwnedRecord[RerankerSettings]) docker.ServiceSpec {
			return docker.ServiceSpec{
				Command:        rerankerCommand,
				Labels:         map[string]string{docker.ModelServerLabel: labelTrue},
				Port:           rerankerContainerPort,
				HealthCheck:    []string{healthProgram, healthSilent, healthFail, rerankerHealth},
				HealthInterval: healthInterval,
				GPU:            true,
			}
		},
	}, options...)
}
