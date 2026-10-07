// Copyright 2026 Candace Labs

package intake

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

const (
	// RecoveryRetention is how far back GitHub keeps a hook's deliveries:
	// three days. A host that never kept a delivery looks back this far.
	RecoveryRetention = 72 * time.Hour
	// RecoverySlack is how far before the newest kept delivery recovery
	// still looks. Deliveries are answered concurrently, so one GitHub sent
	// earlier can be kept after a later one; the slack covers that overlap
	// many times over, since a delivery is answered within GitHub's own
	// ten-second timeout.
	RecoverySlack = 10 * time.Minute
	// maxRecoveryPages bounds one recovery's listing to 1,000 deliveries a
	// hook.
	maxRecoveryPages = 10

	// EventTypeRedelivery records one redelivery recovery asked GitHub for.
	EventTypeRedelivery = "github_redelivery"
	// KeyHook is a hook's identifier on a redelivery record.
	KeyHook = "hook"
)

// Hook is one repository webhook as GitHub lists it.
type Hook struct {
	ID  int64
	URL string
}

// HookDelivery is one attempt GitHub made to deliver to a hook; a
// redelivery is an attempt of its own with the same GUID.
type HookDelivery struct {
	ID          int64
	GUID        string
	Event       string
	DeliveredAt time.Time
	StatusCode  int
	Redelivery  bool
}

// IHookDeliveries is the part of the GitHub capability recovery reads a
// repository's hooks and their deliveries through, and asks redeliveries of.
type IHookDeliveries interface {
	// ListHooks lists the repository's webhooks.
	ListHooks(ctx context.Context, repository string) ([]Hook, error)
	// ListHookDeliveries lists one page of the hook's deliveries, newest
	// first, from cursor ("" for the newest); next is "" after the last page.
	ListHookDeliveries(ctx context.Context, repository string, hook int64, cursor string) (deliveries []HookDelivery, next string, err error)
	// RedeliverHookDelivery asks GitHub to deliver one attempt again.
	RedeliverHookDelivery(ctx context.Context, repository string, hook int64, delivery int64) error
}

// RecoveryReport is what one recovery did.
type RecoveryReport struct {
	Redelivered, Failed int
}

type recovery struct {
	deliveries   IHookDeliveries
	repositories []string
	stream       *Stream
	logger       *slog.Logger
}

// WithRecovery grants the GitHub capability recovery works through and names
// the owner/name repositories whose hooks deliver here. At Start the
// receiver asks GitHub to redeliver every delivery since the newest one it
// kept that it never received: what arrived while this host was down or
// unreachable.
func WithRecovery(deliveries IHookDeliveries, repositories ...string) WebhookOption {
	return func(receiver *WebhookReceiver) error {
		if deliveries == nil {
			return errors.New("intake: WithRecovery needs the GitHub capability")
		}
		if len(repositories) == 0 {
			return fmt.Errorf("%w: WithRecovery names no repository", ErrMissingOption)
		}
		for _, repository := range repositories {
			if err := ValidateRepository(repository); err != nil {
				return err
			}
		}
		receiver.recovery = &recovery{deliveries: deliveries, repositories: repositories}
		return nil
	}
}

// Recover runs recovery once, now; Start runs it after a restart.
func (receiver *WebhookReceiver) Recover(ctx context.Context) (RecoveryReport, error) {
	if receiver.recovery == nil {
		return RecoveryReport{}, fmt.Errorf("%w: WithRecovery", ErrMissingOption)
	}
	return receiver.recovery.recover(ctx), nil
}

func (recovery *recovery) recover(ctx context.Context) RecoveryReport {
	since := recovery.stream.now().Add(-RecoveryRetention)
	if last, err := recovery.stream.LastKept(); err == nil && !last.IsZero() {
		if after := last.Add(-RecoverySlack); after.After(since) {
			since = after
		}
	}
	var report RecoveryReport
	for _, repository := range recovery.repositories {
		hooks, err := recovery.deliveries.ListHooks(ctx, repository)
		if err != nil {
			report.Failed++
			recovery.logger.Warn("intake: hooks not listed", "repository", repository, "error", err)
			continue
		}
		for _, hook := range hooks {
			if !strings.HasSuffix(strings.TrimRight(hook.URL, "/"), WebhookPath) {
				continue
			}
			recovery.recoverHook(ctx, repository, hook, since, &report)
		}
	}
	return report
}

// recoverHook redelivers the newest attempt of every GUID delivered to hook
// since since that this host never kept.
func (recovery *recovery) recoverHook(ctx context.Context, repository string, hook Hook, since time.Time, report *RecoveryReport) {
	missed, err := recovery.missed(ctx, repository, hook, since)
	if err != nil {
		report.Failed++
		recovery.logger.Warn("intake: hook deliveries not listed", "repository", repository, "hook", hook.ID, "error", err)
	}
	for _, attempt := range missed {
		err := recovery.deliveries.RedeliverHookDelivery(ctx, repository, hook.ID, attempt.ID)
		recovery.stream.append(ctx, EventTypeRedelivery, "github redelivery of "+attempt.GUID, err, []slog.Attr{
			slog.String(KeyDelivery, attempt.GUID), slog.String(KeyEvent, attempt.Event),
			slog.String(KeyRepository, repository), slog.String(KeyHook, strconv.FormatInt(hook.ID, 10)),
		})
		if err != nil {
			report.Failed++
			continue
		}
		report.Redelivered++
	}
}

// missed lists, newest first, the newest attempt of each GUID since since
// that was never kept. Attempts already listed are returned with err when a
// later page fails.
func (recovery *recovery) missed(ctx context.Context, repository string, hook Hook, since time.Time) ([]HookDelivery, error) {
	seen := map[string]bool{}
	var missed []HookDelivery
	cursor := ""
	for range maxRecoveryPages {
		page, next, err := recovery.deliveries.ListHookDeliveries(ctx, repository, hook.ID, cursor)
		if err != nil {
			return missed, err
		}
		for _, attempt := range page {
			if attempt.DeliveredAt.Before(since) {
				return missed, nil
			}
			if seen[attempt.GUID] {
				continue
			}
			seen[attempt.GUID] = true
			if !recovery.stream.Kept(attempt.GUID) {
				missed = append(missed, attempt)
			}
		}
		if next == "" {
			return missed, nil
		}
		cursor = next
	}
	return missed, nil
}
