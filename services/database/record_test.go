// Copyright 2026 Candace Labs

package database_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/io/ipc/docker/mocks"
	"github.com/candacelabs/csf/services/database"
)

// previousRecord is the database.json the CSF-DB binary (e9437ce) wrote on
// the production host, kept as database.json.pre-9d092e0 when the upgrade to
// 9d092e0 failed on it: every field, the password redacted and the home path
// replaced.
const previousRecord = "testdata/database.json.pre-9d092e0"

func readFixture() []byte {
	GinkgoHelper()
	content, err := os.ReadFile(previousRecord)
	Expect(err).NotTo(HaveOccurred())
	return content
}

var _ = Describe("database.json across its shape change", func() {
	It("loads tonight's real pre-9d092e0 record, flat settings and all, into the nested record", func() {
		record, err := database.DecodeRecord(previousRecord, readFixture())
		Expect(err).NotTo(HaveOccurred())

		Expect(record.OwnedLocation).To(Equal(docker.OwnedLocation{
			Container: "csf-postgres-8adc01e4cd30", Volume: "csf-postgres-8adc01e4cd30-data", Image: database.Image,
			Host: "127.0.0.1", Port: 44117}))
		Expect(record.Settings).To(Equal(database.Access{
			User: "csf", Database: "csf", Password: "redacted-for-the-fixture",
			BackupDirectory: "/var/lib/csf/harness/backups", BackupUser: "1000:1000"}))
		Expect(database.Settings(record).URL).To(Equal("postgres://csf:redacted-for-the-fixture@127.0.0.1:44117/csf?sslmode=disable"))
	})

	It("fails naming the file, the field and the fix when the backup directory is missing, rather than act on an empty path", func() {
		var fields map[string]any
		Expect(json.Unmarshal(readFixture(), &fields)).To(Succeed())
		delete(fields, "backup_directory")
		content, err := json.Marshal(fields)
		Expect(err).NotTo(HaveOccurred())

		_, err = database.DecodeRecord("/state/database.json", content)
		Expect(err).To(MatchError(docker.ErrRecordInvalid))
		Expect(err).To(MatchError(`ipc/docker: /state/database.json: field "backup_directory" is "", not an absolute path; fix: set it to <state>/backups`))
	})

	It("refuses the nested shape with its settings emptied, the shape tonight's upgrade decoded the old file into", func() {
		_, err := database.DecodeRecord("/state/database.json", []byte(`{"container":"c","volume":"v","image":"i","host":"127.0.0.1","port":1,"settings":{}}`))
		recordError := (*docker.RecordError)(nil)
		Expect(errors.As(err, &recordError)).To(BeTrue())
		Expect(recordError.Field).To(Equal("user"))
	})

	It("starts a host whose record is in the previous shape and writes it back in the current one", func(ctx SpecContext) {
		state := GinkgoT().TempDir()
		content := strings.Replace(string(readFixture()), "/var/lib/csf/harness/backups", filepath.Join(state, "backups"), 1)
		Expect(os.WriteFile(filepath.Join(state, database.RecordFile), []byte(content), 0o600)).To(Succeed())
		controller := gomock.NewController(GinkgoT())
		services := mocks.NewMockIServices(controller)
		services.EXPECT().EnsureService(gomock.Any(), gomock.Any()).Return(docker.ServiceState{Running: true, Health: "healthy", HostPort: 44117}, nil)
		owned, err := database.NewOwnedDatabase(state, backupUser, docker.WithServices(services), docker.WithListener(NewMockIListener(controller)))
		Expect(err).NotTo(HaveOccurred())

		record, err := owned.Ensure(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(record.Settings.BackupDirectory).To(Equal(filepath.Join(state, "backups")))
		Expect(record.Settings.BackupDirectory).To(BeADirectory())

		rewritten, err := os.ReadFile(filepath.Join(state, database.RecordFile))
		Expect(err).NotTo(HaveOccurred())
		var fields map[string]json.RawMessage
		Expect(json.Unmarshal(rewritten, &fields)).To(Succeed())
		Expect(fields).To(HaveKey("settings"))
		Expect(fields).NotTo(HaveKey("backup_directory"))
		again, err := database.DecodeRecord(database.RecordFile, rewritten)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(record))
	})
})
