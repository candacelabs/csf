package csf

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/candacelabs/csf/pkg/patience"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var watchBudget = patience.Budget{Within: 5 * time.Second, Interval: 10 * time.Millisecond}
var _ = Describe("bounded source observer", func() {
	It("hashes preserved-time edits and records failure, repair, deletion and recreation", func() {
		root := GinkgoT().TempDir()
		path := filepath.Join(root, "owned.go")
		valid := []byte("package fixture\nvar value = 1\n")
		invalid := []byte("package fixture\nvar value = ?\n")
		Expect(os.WriteFile(path, valid, 0600)).To(Succeed())
		info, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		receipts := make(chan *pb.CommandReceipt, 8)
		watch, err := NewSourceWatch(root, []string{"owned.go"}, 10*time.Millisecond, func(ctx context.Context, request CheckRequest) error {
			receipt, err := CheckSourceSnapshot(ctx, request)
			receipts <- receipt
			return err
		})
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		results, err := watch.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
		for _, content := range [][]byte{invalid, valid, nil, valid} {
			if content == nil {
				Expect(os.Remove(path)).To(Succeed())
			} else {
				Expect(os.WriteFile(path, content, 0600)).To(Succeed())
				Expect(os.Chtimes(path, info.ModTime(), info.ModTime())).To(Succeed())
			}
			digest := fmt.Sprintf("%x", sha256.Sum256(content))
			// An in-place write can expose an empty or partial snapshot. Match the
			// receipt to the intended bytes, consuming its corresponding result.
			receipt := patience.Await(GinkgoTB(), "source receipt", watchBudget, func() *pb.CommandReceipt {
				select {
				case result := <-results:
					Expect(result.Changes).To(HaveLen(1))
					return <-receipts
				default:
					return nil
				}
			}, func(value *pb.CommandReceipt) bool {
				return value != nil && len(value.Evidence) == 1 &&
					value.Evidence[0].Sha256 == digest && value.Evidence[0].Missing == (content == nil)
			})
			Expect(receipt.Claim).To(Equal(WatchRegistryID))
			Expect(receipt.Command).To(ContainElement("go-ast-signatures"))
			if content == nil || string(content) == string(invalid) {
				Expect(receipt.GetExitCode()).To(Equal(int32(1)))
			} else {
				Expect(receipt.GetExitCode()).To(BeZero())
			}
		}
	})
	It("coalesces changes while callback is occupied and cancels with an unread result", func() {
		root := GinkgoT().TempDir()
		a := filepath.Join(root, "a.go")
		b := filepath.Join(root, "b.go")
		entered := make(chan CheckRequest, 4)
		release := make(chan struct{})
		watch, err := NewSourceWatch(root, []string{"a.go", "b.go"}, 10*time.Millisecond, func(ctx context.Context, request CheckRequest) error {
			entered <- request
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-release:
				return nil
			}
		})
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		results, err := watch.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(a, []byte("one"), 0600)).To(Succeed())
		patience.Await(GinkgoTB(), "first callback", watchBudget, func() CheckRequest {
			select {
			case request := <-entered:
				return request
			default:
				return CheckRequest{}
			}
		}, func(request CheckRequest) bool { return len(request.Changes) == 1 })
		for _, content := range []string{"two", "three", "four"} {
			Expect(os.WriteFile(a, []byte(content), 0600)).To(Succeed())
		}
		Expect(os.WriteFile(b, []byte("last"), 0600)).To(Succeed())
		close(release)
		request := patience.Await(GinkgoTB(), "coalesced snapshot", watchBudget, func() CheckRequest {
			select {
			case request := <-entered:
				return request
			default:
				return CheckRequest{}
			}
		}, func(request CheckRequest) bool { return len(request.Changes) == 2 })
		Expect(string(request.Files[0].Content)).To(Equal("four"))
		cancel()
		patience.Await(GinkgoTB(), "observer closed", watchBudget, func() bool {
			select {
			case _, open := <-results:
				return !open
			default:
				return false
			}
		}, func(closed bool) bool { return closed })
	})
	It("propagates cancellation to an occupied callback", func() {
		root := GinkgoT().TempDir()
		entered := make(chan struct{})
		watch, err := NewSourceWatch(root, []string{"owned.go"}, time.Millisecond, func(ctx context.Context, request CheckRequest) error { close(entered); <-ctx.Done(); return ctx.Err() })
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		results, err := watch.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(root, "owned.go"), []byte("package fixture"), 0600)).To(Succeed())
		patience.Await(GinkgoTB(), "occupied callback", watchBudget, func() bool {
			select {
			case <-entered:
				return true
			default:
				return false
			}
		}, func(value bool) bool { return value })
		cancel()
		patience.Await(GinkgoTB(), "cancelled callback and observer", watchBudget, func() bool {
			select {
			case _, open := <-results:
				return !open
			default:
				return false
			}
		}, func(value bool) bool { return value })
	})
	It("checks the real declared source allowlist", func() {
		// Bazel runfiles are symlinks outside their root. Materialize the real
		// source bytes so this fixture preserves the observer's bounded-root contract.
		directory := GinkgoT().TempDir()
		paths := ImportantSourcePaths()
		for _, path := range paths {
			content, err := os.ReadFile(filepath.Join("..", path))
			Expect(err).NotTo(HaveOccurred())
			destination := filepath.Join(directory, path)
			Expect(os.MkdirAll(filepath.Dir(destination), 0700)).To(Succeed())
			Expect(os.WriteFile(destination, content, 0600)).To(Succeed())
		}
		root, err := os.OpenRoot(directory)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = root.Close() }()
		watch := SourceWatch{paths: paths}
		receipt, err := CheckSourceSnapshot(context.Background(), CheckRequest{Files: watch.scan(context.Background(), root)})
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.GetExitCode()).To(BeZero())
	})

	It("rejects unsafe configurations and bounds oversized files", func() {
		root := GinkgoT().TempDir()
		check := func(ctx context.Context, request CheckRequest) error { return nil }
		_, err := NewSourceWatch(root, []string{"../escape"}, time.Second, check)
		Expect(err).To(HaveOccurred())
		_, err = NewSourceWatch(root, make([]string, maxWatchFiles+1), time.Second, check)
		Expect(err).To(HaveOccurred())
		path := filepath.Join(root, "big.go")
		Expect(os.WriteFile(path, make([]byte, maxWatchBytes+1), 0600)).To(Succeed())
		handle, err := os.OpenRoot(root)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = handle.Close() }()
		content, _, problem := readWatchFile(handle, "big.go")
		Expect(content).To(BeEmpty())
		Expect(problem).NotTo(BeEmpty())
	})
	It("rejects invalid OpenAPI and unnamed Go interface parameters", func() {
		for _, file := range []SourceFile{{Path: "bad.yaml", Content: []byte("openapi: broken")}, {Path: "bad.go", Content: []byte("package x; type Reader interface { Read(string) }")}} {
			receipt, err := CheckSourceSnapshot(context.Background(), CheckRequest{Files: []SourceFile{file}})
			Expect(err).To(HaveOccurred())
			Expect(receipt.GetExitCode()).To(Equal(int32(1)))
		}
	})
})
