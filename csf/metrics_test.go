package csf

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	mocks "github.com/candacelabs/csf/csf/internal/mocks"
	"github.com/candacelabs/csf/pkg/httpserver"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ = Describe("Inspection in the shared host", func() {
	DescribeTable("reports retained receipts and refreshes after retention changes", func(passed, failed int) {
		root := GinkgoT().TempDir()
		at := time.Now().Add(-time.Hour).Truncate(time.Second)
		for code, count := range []int{passed, failed} {
			for index := range count {
				receipt := &pb.CommandReceipt{SchemaVersion: 1, ReceiptId: fmt.Sprintf("%d-%d", code, index),
					ExitCode: proto.Int32(int32(code)), FinishedAt: timestamppb.New(at.Add(time.Duration(index) * time.Second))}
				writeInspectionReceipt(root, receipt)
			}
		}
		view := FarmView{UpdatedAt: at.Format(time.RFC3339), Agents: []FarmAgent{{State: "running"}, {State: "unrecognized"}}}
		inspection := NewInspection(WithInspectionReceipts(root), WithInspectionSnapshot(func() FarmView { return view }))
		families := scrapeInspection(inspection)
		Expect(families).To(HaveKey("go_goroutines"))
		Expect(families).To(HaveKey("process_start_time_seconds"))
		Expect(inspectionGauges(families, "runtime_count", "")).To(Equal(map[string]float64{"": 1}))
		Expect(inspectionGauges(families, "check_receipts", "result")).To(Equal(map[string]float64{"passed": float64(passed), "failed": float64(failed)}))
		latest := make(map[string]float64)
		for result, count := range map[string]int{"passed": passed, "failed": failed} {
			if count > 0 {
				latest[result] = float64(at.Add(time.Duration(count-1) * time.Second).Unix())
			}
		}
		Expect(inspectionGauges(families, "last_check_timestamp_seconds", "result")).To(Equal(latest))
		Expect(inspectionGauges(families, "agent_reports", "state")).To(Equal(map[string]float64{"running": 1, "idle": 0, "unknown": 1, "failed": 0, "completed": 0}))
		Expect(inspectionGauges(families, "work_report_readable", "")).To(Equal(map[string]float64{"": 1}))
		Expect(inspectionGauges(families, "work_report_timestamp_seconds", "")).To(Equal(map[string]float64{"": float64(at.Unix())}))
		// A fresh scrape must observe removal, not a cached total or monotonic counter.
		Expect(os.RemoveAll(root)).To(Succeed())
		Expect(os.Mkdir(root, 0700)).To(Succeed())
		view.Agents = nil
		families = scrapeInspection(inspection)
		Expect(inspectionGauges(families, "check_receipts", "result")).To(Equal(map[string]float64{"passed": 0, "failed": 0}))
		Expect(families).NotTo(HaveKey(inspectionPrefix + "last_check_timestamp_seconds"))
		for _, count := range inspectionGauges(families, "agent_reports", "state") {
			Expect(count).To(BeZero())
		}
	}, Entry("empty collection", 0, 0), Entry("multiple receipts per outcome", 3, 7), Entry("one outcome absent", 4, 0))

	DescribeTable("keeps incomplete observations distinct from measured zero", func(invalidate func(receipt *pb.CommandReceipt)) {
		root := GinkgoT().TempDir()
		receipt := &pb.CommandReceipt{SchemaVersion: 1, ReceiptId: "broken", ExitCode: proto.Int32(0), FinishedAt: timestamppb.New(time.Now().Add(-time.Minute))}
		invalidate(receipt)
		writeInspectionReceipt(root, receipt)
		inspection := NewInspection(WithInspectionReceipts(root), WithInspectionSnapshot(func() FarmView { return FarmView{SourceError: "unreadable"} }))
		families := scrapeInspection(inspection)
		Expect(inspectionGauges(families, "receipt_collection_readable", "")).To(Equal(map[string]float64{"": 0}))
		Expect(inspectionGauges(families, "work_report_readable", "")).To(Equal(map[string]float64{"": 0}))
		for _, name := range []string{"check_receipts", "last_check_timestamp_seconds", "work_report_timestamp_seconds"} {
			Expect(families).NotTo(HaveKey(inspectionPrefix + name))
		}
	},
		Entry("no exit status", func(receipt *pb.CommandReceipt) { receipt.ExitCode = nil }),
		Entry("no completion time", func(receipt *pb.CommandReceipt) { receipt.FinishedAt = nil }),
		Entry("future completion", func(receipt *pb.CommandReceipt) { receipt.FinishedAt = timestamppb.New(time.Now().Add(time.Hour)) }),
		Entry("unsupported schema", func(receipt *pb.CommandReceipt) { receipt.SchemaVersion++ }),
	)
})

var _ = Describe("projection queue observations", func() {
	DescribeTable("keeps queue read failures distinct from measured zero", func(readError error) {
		store := mocks.NewMockIKnowledgeStore(gomock.NewController(GinkgoT()))
		store.EXPECT().CountProjections(gomock.Any()).Return([]*pb.ProjectionCount{{State: pb.ProjectionState_PROJECTION_STATE_PENDING, Count: 0}}, readError)
		workers := &ProjectionWorkers{service: &Service{store: store}}
		families := scrapeInspection(NewInspection(WithInspectionProjectionWorkers(workers)))
		Expect(inspectionGauges(families, "projection_workers", "")).To(Equal(map[string]float64{"": float64(workers.Configured())}))
		Expect(inspectionGauges(families, "projection_active_workers", "")).To(Equal(map[string]float64{"": 0}))
		if readError != nil {
			Expect(inspectionGauges(families, "projection_queue_readable", "")).To(Equal(map[string]float64{"": 0}))
			Expect(families).NotTo(HaveKey(inspectionPrefix + "projection_tasks"))
		} else {
			Expect(inspectionGauges(families, "projection_queue_readable", "")).To(Equal(map[string]float64{"": 1}))
			Expect(inspectionGauges(families, "projection_tasks", "state")).To(Equal(map[string]float64{"pending": 0}))
		}
	}, Entry("measured zero", nil), Entry("unreadable", errors.New("unavailable")))
})

func writeInspectionReceipt(root string, receipt *pb.CommandReceipt) {
	GinkgoHelper()
	content, err := protojson.Marshal(receipt)
	Expect(err).NotTo(HaveOccurred())
	directory := filepath.Join(root, receipt.ReceiptId)
	Expect(os.Mkdir(directory, 0700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(directory, "receipt.json"), content, 0600)).To(Succeed())
}

// Parse the consumer's actual HTTP response so malformed exposition fails too.
func scrapeInspection(inspection *Inspection) map[string]*dto.MetricFamily {
	GinkgoHelper()
	router := httpserver.NewEngine("inspection-test")
	inspection.Register(router)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	Expect(response.Code).To(Equal(http.StatusOK))
	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(strings.NewReader(response.Body.String()))
	Expect(err).NotTo(HaveOccurred())
	return families
}

func inspectionGauges(families map[string]*dto.MetricFamily, name, label string) map[string]float64 {
	GinkgoHelper()
	values := make(map[string]float64)
	family, present := families[inspectionPrefix+name]
	if !present {
		return values
	}
	Expect(family.GetType()).To(Equal(dto.MetricType_GAUGE), name)
	for _, metric := range family.Metric {
		key := ""
		if label == "" {
			Expect(metric.Label).To(BeEmpty(), name)
		} else {
			Expect(metric.Label).To(HaveLen(1), name)
			Expect(metric.Label[0].GetName()).To(Equal(label), name)
			key = metric.Label[0].GetValue()
		}
		Expect(metric.Gauge).NotTo(BeNil(), name)
		Expect(values).NotTo(HaveKey(key), name)
		values[key] = metric.Gauge.GetValue()
	}
	return values
}
