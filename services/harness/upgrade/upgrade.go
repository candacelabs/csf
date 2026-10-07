// Copyright 2026 Candace Labs

// Package upgrade replaces the csf binary with a released one and restarts
// the harness on it, keeping the binary it replaced for a rollback.
//
// A release is the one CI publishes on every merge to main: the binary and
// its SHA-256, under a release named for the commit, and the same two assets
// under latest-main. An upgrade fetches both, refuses a binary whose checksum
// does not match without touching the installed one, and only then swaps it
// in: the installed binary is hard-linked to <binary>.previous and the new one
// renamed over <binary>, so the path always names a whole binary. The restart
// the binary grants follows; when it fails the kept binary is put back and
// restarted, so a broken release never leaves the harness down. A rollback
// swaps <binary> and <binary>.previous the same way.
package upgrade

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/candacelabs/csf/pkg/atomicfile"
)

const (
	// LatestTag is the release CI moves to every merge to main.
	LatestTag = "latest-main"
	// CommitTagPrefix prefixes the commit in the release published for it.
	CommitTagPrefix = "csf-"
	// Asset is the binary's release asset, and ChecksumSuffix names the
	// asset holding its SHA-256 in sha256sum's format.
	Asset          = "csf-linux-amd64"
	ChecksumSuffix = ".sha256"
	// KeptSuffix names the binary an upgrade replaced, beside it.
	KeptSuffix = ".previous"

	linkSuffix = ".link"
	binaryMode = 0o755
	// stagedSuffix names the staged binary the score check runs on before it
	// is renamed over the installed one (#416).
	stagedSuffix = ".new"
	// binaryPrefix is sha256sum's marker for a file read in binary mode.
	binaryPrefix = "*"
)

var (
	// ErrChecksumMismatch reports a fetched binary whose SHA-256 is not the
	// published one; the installed binary is untouched.
	ErrChecksumMismatch = errors.New("upgrade: the binary's SHA-256 does not match the published checksum")
	// ErrMalformedChecksum reports a checksum asset that names no SHA-256 for
	// the binary.
	ErrMalformedChecksum = errors.New("upgrade: the checksum asset names no SHA-256 for the binary")
	// ErrNothingKept reports a rollback with no kept binary.
	ErrNothingKept = errors.New("upgrade: no previous binary is kept to roll back to")
	// ErrRestartFailed reports a restart that failed; the binary it ran on
	// was replaced by the kept one, and that was restarted.
	ErrRestartFailed = errors.New("upgrade: the harness did not restart on the new binary, so the previous one was restored")
	// ErrInvalidOption reports an option set the upgrader cannot be built
	// from.
	ErrInvalidOption = errors.New("upgrade: invalid option")
)

// Fetch returns one asset of one release.
type Fetch func(ctx context.Context, tag string, asset string) ([]byte, error)

// Restart restarts the harness on the binary now installed and returns once
// it answers.
type Restart func(ctx context.Context) error

// Progress receives one line, as it happens, for every step of an upgrade
// and of a restart.
type Progress func(line string)

// ScoreCheck admits a staged binary before it goes live: it reports the
// build's score on the evaluation suite and refuses a build with none or one
// that regressed (#416). A refused build is never installed.
type ScoreCheck func(ctx context.Context, staged string) error

// silent is the progress of an upgrader given none.
func silent(line string) {}

// Result is what an upgrade or a rollback did.
type Result struct {
	Binary string `json:"binary"`
	Kept   string `json:"kept"`
	Tag    string `json:"tag,omitempty"`
	SHA256 string `json:"sha256"`
}

// Upgrader swaps the installed binary and restarts the harness on it.
type Upgrader struct {
	binary   string
	fetch    Fetch
	restart  Restart
	progress Progress
	score    ScoreCheck
}

// Option configures an [Upgrader].
type Option func(upgrader *Upgrader) error

// WithBinary names the installed binary an upgrade replaces. It is
// required.
func WithBinary(path string) Option {
	return func(upgrader *Upgrader) error {
		if path == "" {
			return fmt.Errorf("%w: empty binary path", ErrInvalidOption)
		}
		upgrader.binary = path
		return nil
	}
}

// WithFetch grants the release download. An upgrader without one can only
// roll back.
func WithFetch(fetch Fetch) Option {
	return func(upgrader *Upgrader) error {
		if fetch == nil {
			return fmt.Errorf("%w: nil fetch", ErrInvalidOption)
		}
		upgrader.fetch = fetch
		return nil
	}
}

// WithRestart grants the harness restart. It is required.
func WithRestart(restart Restart) Option {
	return func(upgrader *Upgrader) error {
		if restart == nil {
			return fmt.Errorf("%w: nil restart", ErrInvalidOption)
		}
		upgrader.restart = restart
		return nil
	}
}

// WithProgress grants the line every step is reported through.
func WithProgress(progress Progress) Option {
	return func(upgrader *Upgrader) error {
		if progress == nil {
			return fmt.Errorf("%w: nil progress", ErrInvalidOption)
		}
		upgrader.progress = progress
		return nil
	}
}

// WithScoreCheck grants the score-before-live check an upgrade runs on the
// staged binary before swapping it in. Without it no build is checked.
func WithScoreCheck(check ScoreCheck) Option {
	return func(upgrader *Upgrader) error {
		if check == nil {
			return fmt.Errorf("%w: nil score check", ErrInvalidOption)
		}
		upgrader.score = check
		return nil
	}
}

// NewUpgrader validates the whole option set before building the upgrader.
func NewUpgrader(options ...Option) (*Upgrader, error) {
	upgrader := &Upgrader{progress: silent}
	for _, option := range options {
		if err := option(upgrader); err != nil {
			return nil, err
		}
	}
	if upgrader.binary == "" || upgrader.restart == nil {
		return nil, fmt.Errorf("%w: a binary and a restart are required", ErrInvalidOption)
	}
	return upgrader, nil
}

// Tag is the release of commit, or latest-main for an empty commit.
func Tag(commit string) string {
	if commit == "" {
		return LatestTag
	}
	return CommitTagPrefix + commit
}

// Kept is where the binary an upgrade replaced is kept.
func (upgrader *Upgrader) Kept() string { return upgrader.binary + KeptSuffix }

// Upgrade installs the binary released for commit, or latest main, keeps the
// one it replaces and restarts the harness on it.
func (upgrader *Upgrader) Upgrade(ctx context.Context, commit string) (Result, error) {
	if upgrader.fetch == nil {
		return Result{}, fmt.Errorf("%w: no release download granted", ErrInvalidOption)
	}
	tag := Tag(commit)
	upgrader.progress(fmt.Sprintf("release %s: downloading %s", tag, Asset))
	binary, err := upgrader.fetch(ctx, tag, Asset)
	if err != nil {
		return Result{}, err
	}
	upgrader.progress(fmt.Sprintf("downloaded %s: %d bytes", Asset, len(binary)))
	checksums, err := upgrader.fetch(ctx, tag, Asset+ChecksumSuffix)
	if err != nil {
		return Result{}, err
	}
	want, err := publishedChecksum(checksums, Asset)
	if err != nil {
		return Result{}, err
	}
	digest := sha256.Sum256(binary)
	got := hex.EncodeToString(digest[:])
	if got != want {
		upgrader.progress(fmt.Sprintf("checksum: expected %s, actual %s: MISMATCH, nothing installed", want, got))
		return Result{}, fmt.Errorf("%w: %s got %s, published %s", ErrChecksumMismatch, tag, got, want)
	}
	upgrader.progress(fmt.Sprintf("checksum: expected %s, actual %s: match", want, got))
	result := Result{Binary: upgrader.binary, Kept: upgrader.Kept(), Tag: tag, SHA256: got}
	staged := upgrader.binary + stagedSuffix
	if err := atomicfile.WriteFile(staged, binary, binaryMode); err != nil {
		return result, err
	}
	if upgrader.score != nil {
		if err := upgrader.score(ctx, staged); err != nil {
			upgrader.progress(fmt.Sprintf("score before live: REFUSED, nothing installed: %v", err))
			return result, errors.Join(err, os.Remove(staged))
		}
	}
	if err := replaceLink(upgrader.binary, upgrader.Kept()); err != nil {
		return result, errors.Join(err, os.Remove(staged))
	}
	if err := os.Rename(staged, upgrader.binary); err != nil {
		return result, errors.Join(err, os.Remove(staged))
	}
	upgrader.progress(fmt.Sprintf("swapped: %s is the new binary, the old one kept as %s", upgrader.binary, upgrader.Kept()))
	return result, upgrader.restartOrRestore(ctx, func() error { return os.Rename(upgrader.Kept(), upgrader.binary) })
}

// Rollback swaps the installed binary with the kept one and restarts the
// harness on it; the binary it replaces becomes the kept one.
func (upgrader *Upgrader) Rollback(ctx context.Context) (Result, error) {
	kept, err := os.ReadFile(upgrader.Kept())
	if errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("%w: %s does not exist", ErrNothingKept, upgrader.Kept())
	}
	if err != nil {
		return Result{}, err
	}
	digest := sha256.Sum256(kept)
	result := Result{Binary: upgrader.binary, Kept: upgrader.Kept(), SHA256: hex.EncodeToString(digest[:])}
	if err := upgrader.swap(); err != nil {
		return result, err
	}
	upgrader.progress(fmt.Sprintf("swapped: %s is the kept binary again, the replaced one kept as %s", upgrader.binary, upgrader.Kept()))
	return result, upgrader.restartOrRestore(ctx, upgrader.swap)
}

// restartOrRestore restarts the harness; when that fails it restores the
// previous binary and restarts the harness on it.
func (upgrader *Upgrader) restartOrRestore(ctx context.Context, restore func() error) error {
	failure := upgrader.restart(ctx)
	if failure == nil {
		return nil
	}
	if err := restore(); err != nil {
		return fmt.Errorf("%w: %w; restoring the previous binary failed too: %w", ErrRestartFailed, failure, err)
	}
	if err := upgrader.restart(ctx); err != nil {
		return fmt.Errorf("%w: %w; the restart on the previous binary failed too: %w", ErrRestartFailed, failure, err)
	}
	return fmt.Errorf("%w: %w", ErrRestartFailed, failure)
}

// swap exchanges the installed and the kept binary, so the path always names
// a whole binary.
func (upgrader *Upgrader) swap() error {
	link := upgrader.binary + linkSuffix
	if err := replaceLink(upgrader.binary, link); err != nil {
		return err
	}
	if err := os.Rename(upgrader.Kept(), upgrader.binary); err != nil {
		return errors.Join(err, os.Remove(link))
	}
	return os.Rename(link, upgrader.Kept())
}

// replaceLink makes target a hard link to source, replacing whatever target
// was, without a moment at which target is missing.
func replaceLink(source string, target string) error {
	temporary := target + linkSuffix
	if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Link(source, temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		return errors.Join(err, os.Remove(temporary))
	}
	return nil
}

// publishedChecksum reads asset's SHA-256 from sha256sum output.
func publishedChecksum(checksums []byte, asset string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(checksums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], binaryPrefix) != asset {
			continue
		}
		sum := strings.ToLower(fields[0])
		if decoded, err := hex.DecodeString(sum); err != nil || len(decoded) != sha256.Size {
			return "", fmt.Errorf("%w: %q is not a SHA-256", ErrMalformedChecksum, fields[0])
		}
		return sum, nil
	}
	return "", fmt.Errorf("%w: no line for %s", ErrMalformedChecksum, asset)
}
