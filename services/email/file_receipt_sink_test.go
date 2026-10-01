package email

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
)

var _ = Describe("FileReceiptSink", func() {
	It("atomically replaces UNKNOWN with a private final protobuf at the same receipt ID", func() {
		directory := filepath.Join(GinkgoT().TempDir(), "receipts")
		sink, err := NewFileReceiptSink(directory)
		Expect(err).NotTo(HaveOccurred())
		receipt := newAttemptReceipt(func() *provenancev1.ReceiptMetadata {
			metadata := validMetadata()
			metadata.SchemaVersion = 1
			metadata.ReceiptId = fixedID
			metadata.RecordedAt = timestamppb.New(fixedTime)
			return metadata
		}(), []byte("wire bytes"))
		Expect(sink.Record(context.Background(), receipt)).To(Succeed())
		receipt.Outcome = emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED
		receipt.ErrorCode = ""
		receipt.CompletedAt = timestamppb.New(fixedTime)
		Expect(sink.Record(context.Background(), receipt)).To(Succeed())

		entries, err := os.ReadDir(directory)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
		path := filepath.Join(directory, fixedID+".pb")
		info, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		data, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(data), "operator@example.invalid")).To(BeFalse())
		Expect(strings.Contains(string(data), "wire bytes")).To(BeFalse())
		var stored emailv1.EmailReceipt
		Expect(proto.Unmarshal(data, &stored)).To(Succeed())
		Expect(stored.GetOutcome()).To(Equal(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED))
		Expect(stored.GetProvenance().GetReceiptId()).To(Equal(fixedID))
	})

	DescribeTable("rejects a receipt containing a credential-bearing URL", func(unsafeURL string) {
		directory := filepath.Join(GinkgoT().TempDir(), "receipts")
		sink, err := NewFileReceiptSink(directory)
		Expect(err).NotTo(HaveOccurred())
		metadata := validMetadata()
		metadata.SchemaVersion = 1
		metadata.ReceiptId = fixedID
		metadata.RecordedAt = timestamppb.New(fixedTime)
		metadata.Sessions[0].Url = unsafeURL
		receipt := newAttemptReceipt(metadata, nil)
		Expect(sink.Record(context.Background(), receipt)).To(MatchError(ContainSubstring("unsafe session URL")))
	},
		Entry("query", "https://example.invalid/session?token=secret"),
		Entry("fragment", "https://example.invalid/session#token=secret"),
		Entry("fragment route query", "https://example.invalid/#/session?token=secret"),
	)
})
