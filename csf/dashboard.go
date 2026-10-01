package csf

import (
	"bufio"
	"bytes"
	"container/list"
	_ "embed"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"

	brainspinev1 "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

//go:embed dashboard.html
var dashboardHTML []byte

const (
	SnapshotPath  = "/api/snapshot"
	CompilePath   = "/api/compile"
	maxEventBytes = 16 * 1024 * 1024
	maxMetrics    = 128
	maxViewEvents = 20000
	maxViewIssues = 128
)

// Dashboard observes one run's append-only artifact. It never owns the runtime.
type Dashboard struct{ eventPath string }

func NewDashboard(eventPath string) *Dashboard { return &Dashboard{eventPath: eventPath} }

func (dashboard *Dashboard) Snapshot() *brainspinev1.Snapshot {
	snapshot := &brainspinev1.Snapshot{}
	file, err := os.Open(dashboard.eventPath)
	if err != nil {
		snapshot.Issues = append(snapshot.Issues, "No observations yet: "+err.Error())
		return snapshot
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		snapshot.Issues = append(snapshot.Issues, err.Error())
		return snapshot
	}
	readDashboardEvents(io.NewSectionReader(file, 0, info.Size()), snapshot)
	return snapshot
}

type retainedDashboardEvent struct {
	event *brainspinev1.ResearchEvent
	bytes int
}

// Scan the retained log to keep definitions registered anywhere in the run;
// only the bounded, most recent observations remain in the browser snapshot.
func readDashboardEvents(source io.Reader, snapshot *brainspinev1.Snapshot) {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64*1024), maxEventBytes+1)
	scanner.Split(splitDashboardEvent)
	definitions := map[string]*brainspinev1.MetricDefinition{}
	var recent list.List
	retainedBytes := 0
	for line := 1; scanner.Scan(); line++ {
		raw := scanner.Bytes()
		snapshot.BytesRead += uint64(len(raw))
		// A producer may still be writing the last record.
		if len(bytes.TrimSpace(raw)) == 0 || raw[len(raw)-1] != '\n' {
			continue
		}
		count := len(definitions)
		event, err := parseDashboardEvent(raw, definitions)
		if err != nil {
			if len(snapshot.Issues) < maxViewIssues {
				snapshot.Issues = append(snapshot.Issues, fmt.Sprintf("line %d: %v", line, err))
			} else {
				snapshot.Truncated = true
			}
			continue
		}
		if event.GetDefinition() != nil {
			if len(definitions) > count {
				snapshot.Events = append(snapshot.Events, event)
			}
			continue
		}
		recent.PushBack(retainedDashboardEvent{event: event, bytes: len(raw)})
		retainedBytes += len(raw)
		for recent.Len() > maxViewEvents-maxMetrics || retainedBytes > maxEventBytes {
			snapshot.Truncated = true
			retainedBytes -= recent.Remove(recent.Front()).(retainedDashboardEvent).bytes
		}
	}
	if err := scanner.Err(); err != nil {
		snapshot.Issues = append(snapshot.Issues, "Cannot read remaining events: "+err.Error())
	}
	for entry := recent.Front(); entry != nil; entry = entry.Next() {
		snapshot.Events = append(snapshot.Events, entry.Value.(retainedDashboardEvent).event)
	}
}

func splitDashboardEvent(data []byte, atEOF bool) (int, []byte, error) {
	if index := bytes.IndexByte(data, '\n'); index >= 0 {
		return index + 1, data[:index+1], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func parseDashboardEvent(raw []byte, definitions map[string]*brainspinev1.MetricDefinition) (*brainspinev1.ResearchEvent, error) {
	event := &brainspinev1.ResearchEvent{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(raw, event); err != nil {
		return nil, err
	}
	if err := validateEvent(event, definitions); err != nil {
		return nil, err
	}
	return event, nil
}

func validateEvent(event *brainspinev1.ResearchEvent, definitions map[string]*brainspinev1.MetricDefinition) error {
	if event.SchemaVersion != SchemaVersion || event.RecordedAt == "" {
		return fmt.Errorf("unsupported or undated event")
	}
	switch payload := event.Payload.(type) {
	case *brainspinev1.ResearchEvent_Definition:
		definition := payload.Definition
		if err := brainspinev1.ValidateMetricDefinition(definition); err != nil {
			return err
		}
		if previous, exists := definitions[definition.Name]; exists {
			if !proto.Equal(previous, definition) {
				return fmt.Errorf("metric %s was redefined", definition.Name)
			}
			return nil
		}
		if len(definitions) >= maxMetrics {
			return fmt.Errorf("metric registry capacity exceeded")
		}
		definitions[definition.Name] = definition
	case *brainspinev1.ResearchEvent_Measurement:
		measurement := payload.Measurement
		if measurement == nil || definitions[measurement.Metric] == nil {
			return fmt.Errorf("unregistered measurement")
		}
		if math.IsNaN(measurement.Value) || math.IsInf(measurement.Value, 0) {
			return fmt.Errorf("non-finite measurement")
		}
		if measurement.RunId == "" {
			return fmt.Errorf("measurement has no run identity")
		}
	case *brainspinev1.ResearchEvent_Status:
		if payload.Status == nil || payload.Status.RunId == "" || payload.Status.Phase == "" {
			return fmt.Errorf("invalid run status")
		}
	default:
		return fmt.Errorf("event needs one typed payload")
	}
	return nil
}

// Register mounts the basic metric view; Service owns its generated APIs.
func (dashboard *Dashboard) Register(router gin.IRouter) {
	router.GET("/", func(ctx *gin.Context) {
		ctx.Header("Cache-Control", "no-store")
		ctx.Data(http.StatusOK, "text/html; charset=utf-8", dashboardHTML)
	})
}

func writeProto(response http.ResponseWriter, message proto.Message) {
	data, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(message)
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	_, _ = response.Write(data)
}
