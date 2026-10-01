package email

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
)

const (
	receiptDirectoryMode = 0o700
	receiptFileMode      = 0o600
)

// FileReceiptSink stores one deterministic protobuf file per receipt ID. A
// final record atomically replaces the pre-send UNKNOWN record at the same path.
// It never stores message bodies, SMTP credentials, senders, or recipients.
type FileReceiptSink struct {
	directory string
}

var _ IReceiptSink = (*FileReceiptSink)(nil)

// NewFileReceiptSink prepares a private receipt directory. Existing
// directories must already deny group and other access.
func NewFileReceiptSink(directory string) (*FileReceiptSink, error) {
	if directory == "" {
		return nil, errors.New("email: receipt directory is empty")
	}
	if err := os.MkdirAll(directory, receiptDirectoryMode); err != nil {
		return nil, fmt.Errorf("creating receipt directory: %w", err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		return nil, fmt.Errorf("statting receipt directory: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("email: receipt path is not a directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("email: receipt directory permits group or other access")
	}
	return &FileReceiptSink{directory: directory}, nil
}

// Record validates and atomically persists receipt as deterministic protobuf.
func (sink *FileReceiptSink) Record(ctx context.Context, receipt *emailv1.EmailReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateReceipt(receipt); err != nil {
		return fmt.Errorf("validating receipt for persistence: %w", err)
	}
	receiptID := receipt.GetProvenance().GetReceiptId()
	if _, err := uuid.Parse(receiptID); err != nil {
		return errors.New("email: receipt ID is not a UUID")
	}
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(receipt)
	if err != nil {
		return fmt.Errorf("marshaling receipt: %w", err)
	}
	return sink.replace(receiptID, data)
}

func (sink *FileReceiptSink) replace(receiptID string, data []byte) error {
	temporary, err := os.CreateTemp(sink.directory, receiptID+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temporary receipt: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(receiptFileMode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("securing temporary receipt: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("writing temporary receipt: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("syncing temporary receipt: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("closing temporary receipt: %w", err)
	}
	path := filepath.Join(sink.directory, receiptID+".pb")
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replacing receipt: %w", err)
	}
	return syncDirectory(sink.directory)
}

func syncDirectory(directory string) error {
	dir, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("opening receipt directory for sync: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("syncing receipt directory: %w", err)
	}
	return nil
}
