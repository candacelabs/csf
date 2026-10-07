// Copyright 2026 Candace Labs

// Package database is CSF's own database: the PostgreSQL container a host
// provisions and owns when it is given no other, so CSF's state lives with
// CSF and never in another project's database.
//
// It is an [docker.OwnedService]: a pinned image, a named volume for the
// data, a port probed once and published on 127.0.0.1 only, and a password
// generated once, all recorded in <state>/database.json, mode 0600. What is
// PostgreSQL's alone lives here: the image, the credentials, the health
// check, the backup directory pg_dump writes into, and the csfpg settings a
// host opens its pool with.
package database

import (
	"crypto/rand"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	"github.com/candacelabs/csf/io/ipc/docker"
)

const (
	// RecordFile is the record under the state directory, mode 0600.
	RecordFile = "database.json"
	// BackupDirectory is where backups are written, under the state directory.
	BackupDirectory = "backups"
	// ContainerBackupDirectory is where the backup directory is mounted inside
	// the container, so pg_dump writes its file there.
	ContainerBackupDirectory = "/backups"
	// Image is the pinned PostgreSQL image, by digest.
	Image = "postgres:18.4-alpine3.24@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15"
	// User and Name are the role and database CSF's schema lives in.
	User = "csf"
	Name = "csf"

	containerPrefix = "csf-postgres-"
	dataDirectory   = "/var/lib/postgresql"
	postgresPort    = "5432/tcp"
	healthInterval  = 2 * time.Second
	urlScheme       = "postgres"
	sslModeQuery    = "sslmode=disable"

	environmentUser     = "POSTGRES_USER="
	environmentPassword = "POSTGRES_PASSWORD="
	environmentDatabase = "POSTGRES_DB="

	programReady = "pg_isready"
	readyHost    = "--host"
	readyUser    = "--username"
	readyName    = "--dbname"
)

// loopback is the only address the database is published on.
var loopback = netip.MustParseAddr("127.0.0.1")

// Access is the database's own part of its record: how to log in, and where
// and as whom backups are written.
type Access struct {
	User     string `json:"user"`
	Database string `json:"database"`
	Password string `json:"password"`
	// BackupDirectory is the host path mounted at [ContainerBackupDirectory].
	BackupDirectory string `json:"backup_directory"`
	// BackupUser is the user:group pg_dump runs as, so backups belong to the
	// host's user.
	BackupUser string `json:"backup_user"`
}

// Record is <state>/database.json.
type Record = docker.OwnedRecord[Access]

// DecodeRecord decodes database.json in its current or previous shape and
// requires every field the host acts on, so a missing backup directory is an
// error naming the file and the fix, never an empty path.
func DecodeRecord(path string, content []byte) (Record, error) {
	record, _, err := docker.DecodeOwnedRecord(path, content, validateAccess)
	return record, err
}

// validateAccess requires the login and an absolute backup directory.
func validateAccess(access Access) *docker.RecordError {
	const fix = "restore the field from the copy beside the file, or run `csf db status` with the binary that wrote it"
	switch {
	case access.User == "":
		return docker.RequiredField("user", fix)
	case access.Database == "":
		return docker.RequiredField("database", fix)
	case access.Password == "":
		return docker.RequiredField("password", fix+"; the password is the container's POSTGRES_PASSWORD")
	case access.BackupUser == "":
		return docker.RequiredField("backup_user", "set it to the host user:group, such as 1000:1000")
	case !filepath.IsAbs(access.BackupDirectory):
		return &docker.RecordError{Field: "backup_directory", Problem: fmt.Sprintf("is %q, not an absolute path", access.BackupDirectory),
			Fix: "set it to <state>/" + BackupDirectory}
	}
	return nil
}

// Settings is the csfpg configuration the record names.
func Settings(record Record) csfpg.Settings {
	location := url.URL{
		Scheme:   urlScheme,
		User:     url.UserPassword(record.Settings.User, record.Settings.Password),
		Host:     net.JoinHostPort(record.Host, strconv.Itoa(int(record.Port))),
		Path:     "/" + record.Settings.Database,
		RawQuery: sslModeQuery,
	}
	return csfpg.Settings{URL: location.String()}
}

// NewOwnedDatabase is the database the state directory owns, its backups
// written as backupUser (user:group).
func NewOwnedDatabase(state string, backupUser string, options ...docker.OwnedServiceOption) (*docker.OwnedService[Access], error) {
	return docker.NewOwnedService(docker.OwnedServiceDefinition[Access]{
		StateDirectory: state,
		RecordFile:     RecordFile,
		NamePrefix:     containerPrefix,
		DataDirectory:  dataDirectory,
		Image:          Image,
		Host:           loopback,
		NewSettings: func() (Access, error) {
			return Access{User: User, Database: Name, Password: rand.Text(),
				BackupDirectory: filepath.Join(state, BackupDirectory), BackupUser: backupUser}, nil
		},
		Spec:     serviceSpec,
		Validate: validateAccess,
	}, options...)
}

// serviceSpec is the PostgreSQL container the record describes.
func serviceSpec(record Record) docker.ServiceSpec {
	access := record.Settings
	return docker.ServiceSpec{
		Environment: []string{
			environmentUser + access.User, environmentPassword + access.Password, environmentDatabase + access.Database,
		},
		Mounts: []docker.Mount{{Source: access.BackupDirectory, Target: ContainerBackupDirectory}},
		Port:   postgresPort,
		// Over TCP, so the server the image's entrypoint runs during
		// initialization, which listens on its socket only, never passes.
		HealthCheck:    []string{programReady, readyHost, loopback.String(), readyUser, access.User, readyName, access.Database},
		HealthInterval: healthInterval,
	}
}
