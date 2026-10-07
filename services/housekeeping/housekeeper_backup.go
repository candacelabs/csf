// Copyright 2026 Candace Labs

package housekeeping

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/ipc/docker"
	cronservice "github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/database"
)

const (
	backupPrefix = "csf-"
	backupSuffix = ".dump"
	backupStamp  = "20060102T150405Z"
	programDump  = "pg_dump"
	dumpUser     = "--username"
	dumpDatabase = "--dbname"
	dumpFormat   = "--format=custom"
	dumpFile     = "--file"
	// backupShare is the part of the space above the disk floor backups may
	// hold, counting their own: the shared cache holds up to half of it, and
	// half of the other half stays free.
	backupShare = 4
	// maxBackups is a week of hourly dumps: an older one restores a state no
	// later one does only if the database went bad unnoticed for a week.
	maxBackups = 7 * 24
)

// DatabaseBackup dumps the database this host owns into <state>/backups
// with pg_dump, run inside its container, and then keeps only as many dumps
// as the derived retention allows, newest first. A host whose database is
// someone else's (no <state>/database.json) has nothing to back up.
func (housekeeper *Housekeeper) DatabaseBackup(ctx context.Context, occurrence cronservice.Occurrence) error {
	at := passOf(occurrence)
	content, err := housekeeper.state.ReadFile(database.RecordFile)
	if errors.Is(err, stdfs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	record, err := database.DecodeRecord(path.Join(housekeeper.stateDirectory, database.RecordFile), content)
	if err != nil {
		return err
	}
	if !housekeeper.dryRun {
		if err := housekeeper.dump(ctx, at, record); err != nil {
			return err
		}
	}
	return housekeeper.retainBackups(ctx, at)
}

// dump runs pg_dump as the record's backup user, so the file belongs to the
// host's user, and records the file with its size.
func (housekeeper *Housekeeper) dump(ctx context.Context, at pass, record database.Record) error {
	name := backupPrefix + housekeeper.clock.Now().UTC().Format(backupStamp) + backupSuffix
	result, err := housekeeper.containers.Exec(ctx, record.Container, docker.ExecSpec{
		User: record.Settings.BackupUser,
		Command: []string{programDump, dumpUser, record.Settings.User, dumpDatabase, record.Settings.Database, dumpFormat,
			dumpFile, path.Join(database.ContainerBackupDirectory, name)},
	})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("housekeeping: pg_dump exited %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	info, err := stdfs.Stat(housekeeper.state, path.Join(database.BackupDirectory, name))
	if err != nil {
		return fmt.Errorf("housekeeping: the dump pg_dump reported is missing: %w", err)
	}
	return housekeeper.record(at, Record{Type: RecordBackup, What: housekeeper.backupPath(name), Bytes: uint64(info.Size())})
}

// backup is one dump in the backup directory.
type backup struct {
	name  string
	bytes uint64
}

// retainBackups derives how many dumps to keep and removes the older ones.
// Only names this trigger writes are counted or removed.
func (housekeeper *Housekeeper) retainBackups(ctx context.Context, at pass) error {
	backups, err := housekeeper.backups()
	if err != nil || len(backups) == 0 {
		return err
	}
	keep, derivation, err := housekeeper.deriveRetention(ctx, backups)
	if err != nil {
		return err
	}
	if err := housekeeper.record(at, Record{Type: RecordRetention, What: housekeeper.backupPath(""), Bytes: uint64(keep), Detail: derivation}); err != nil {
		return err
	}
	var failures []error
	for _, old := range backups[min(keep, len(backups)):] {
		target := housekeeper.backupPath(old.name)
		failures = append(failures, housekeeper.delete(ctx, at, Record{Kind: KindBackup, What: target, Bytes: old.bytes},
			func(ctx context.Context) (uint64, error) {
				return old.bytes, housekeeper.removeTree(ctx, target)
			}))
	}
	return errors.Join(failures...)
}

// deriveRetention keeps as many dumps of the newest one's size as fit in
// backupShare of the space above the disk floor, counting the space the
// dumps hold now, between one and maxBackups.
func (housekeeper *Housekeeper) deriveRetention(ctx context.Context, backups []backup) (int, string, error) {
	sessions, err := housekeeper.census(ctx)
	if err != nil {
		return 0, "", err
	}
	floor := housekeeper.deriveFloor(ctx, sessions)
	free, err := housekeeper.freeBytes(ctx)
	if err != nil {
		return 0, "", err
	}
	var held uint64
	for _, existing := range backups {
		held += existing.bytes
	}
	var budget uint64
	if free+held > floor.bytes {
		budget = (free + held - floor.bytes) / backupShare
	}
	newest := max(backups[0].bytes, 1)
	keep := int(min(max(budget/newest, 1), maxBackups))
	return keep, fmt.Sprintf("(%d free + %d held by backups - %d floor) / %d = %d bytes budget / %d bytes newest dump = keep %d, between 1 and %d (a week of hourly dumps); floor: %s",
		free, held, floor.bytes, backupShare, budget, newest, keep, maxBackups, floor.derivation), nil
}

// backups lists the dumps, newest first.
func (housekeeper *Housekeeper) backups() ([]backup, error) {
	entries, err := housekeeper.state.ReadDir(database.BackupDirectory)
	if errors.Is(err, stdfs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var backups []backup
	for _, entry := range entries {
		stamp, prefixed := strings.CutPrefix(entry.Name(), backupPrefix)
		stamp, suffixed := strings.CutSuffix(stamp, backupSuffix)
		if !prefixed || !suffixed || !entry.Type().IsRegular() {
			continue
		}
		if _, err := time.Parse(backupStamp, stamp); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		backups = append(backups, backup{name: entry.Name(), bytes: uint64(info.Size())})
	}
	// The stamp sorts by time, so the names sort newest first in reverse.
	slices.SortFunc(backups, func(left backup, right backup) int { return strings.Compare(right.name, left.name) })
	return backups, nil
}

func (housekeeper *Housekeeper) backupPath(name string) string {
	return path.Join(housekeeper.stateDirectory, database.BackupDirectory, name)
}
