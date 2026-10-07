package csf

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	brainspinev1 "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	maxInspectionReceipts     = 1024
	maxInspectionReceiptBytes = 1 << 20
)

type inspectionReceiptSummary struct {
	count  int
	latest time.Time
}

type inspectionReceipts struct {
	passed inspectionReceiptSummary
	failed inspectionReceiptSummary
}

// readInspectionReceipts reads the retained command receipts under path. Only
// the receipt.py result envelope is read. Commands, logs and source
// transcripts are never metric labels or exposed by this endpoint.
func readInspectionReceipts(path string) (inspectionReceipts, error) {
	var result inspectionReceipts
	root, err := os.OpenRoot(path)
	if err != nil {
		return result, err
	}
	defer func() { _ = root.Close() }()
	directory, err := root.Open(".")
	if err != nil {
		return result, err
	}
	defer func() { _ = directory.Close() }()
	entries, err := directory.ReadDir(maxInspectionReceipts + 1)
	if err != nil && err != io.EOF {
		return result, err
	}
	if len(entries) > maxInspectionReceipts {
		return result, fmt.Errorf("receipt retention exceeds inspection bound")
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		file, err := root.Open(filepath.Join(entry.Name(), "receipt.json"))
		if err != nil {
			return result, err
		}
		content, err := io.ReadAll(io.LimitReader(file, maxInspectionReceiptBytes+1))
		_ = file.Close()
		if err != nil || len(content) > maxInspectionReceiptBytes {
			return result, fmt.Errorf("cannot read bounded receipt %q", entry.Name())
		}
		receipt := &brainspinev1.CommandReceipt{}
		err = protojson.Unmarshal(content, receipt)
		if err != nil || receipt.SchemaVersion != 1 || receipt.ReceiptId == "" || receipt.ExitCode == nil || receipt.FinishedAt == nil || receipt.FinishedAt.CheckValid() != nil || receipt.FinishedAt.AsTime().After(time.Now()) {
			return result, fmt.Errorf("invalid command receipt %q", entry.Name())
		}
		outcome := &result.passed
		if *receipt.ExitCode != 0 {
			outcome = &result.failed
		}
		outcome.count++
		if finished := receipt.FinishedAt.AsTime(); finished.After(outcome.latest) {
			outcome.latest = finished
		}
	}
	return result, nil
}
