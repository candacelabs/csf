// Copyright 2026 Candace Labs

package housekeeping_test

import (
	"context"
	"encoding/json"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/services/database"
	"github.com/candacelabs/csf/services/housekeeping"
)

const (
	databaseContainer = "csf-postgres-0123456789ab"
	newDump           = "backups/csf-20261002T120000Z.dump"
	dumpBytes         = 100
)

// withDatabase records the owned database and three earlier dumps.
func withDatabase(machine *host) {
	content, err := json.Marshal(database.Record{
		OwnedLocation: docker.OwnedLocation{Container: databaseContainer, Volume: databaseContainer + "-data",
			Image: database.Image, Host: "127.0.0.1", Port: 15432},
		Settings: database.Access{User: database.User, Database: database.Name, Password: "fixture",
			BackupDirectory: state + "/backups", BackupUser: "1000:1000"},
	})
	Expect(err).NotTo(HaveOccurred())
	machine.state[database.RecordFile] = &fstest.MapFile{Data: content}
	for _, name := range []string{"csf-20261002T090000Z.dump", "csf-20261002T100000Z.dump", "csf-20261002T110000Z.dump"} {
		machine.state["backups/"+name] = &fstest.MapFile{Data: make([]byte, dumpBytes)}
	}
	machine.state["backups/notes.txt"] = &fstest.MapFile{Data: []byte("the operator's")}
}

// expectDump answers pg_dump by writing the dump it was asked for.
func expectDump(machine *host) *docker.ExecSpec {
	var ran docker.ExecSpec
	machine.containers.EXPECT().Exec(gomock.Any(), databaseContainer, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, spec docker.ExecSpec) (docker.ExecResult, error) {
			ran = spec
			machine.state[newDump] = &fstest.MapFile{Data: make([]byte, dumpBytes)}
			return docker.ExecResult{}, nil
		})
	return &ran
}

var _ = Describe("the database_backup trigger", func() {
	var machine *host

	BeforeEach(func() { machine = newHost() })

	It("dumps the owned database as the backup user and keeps the derived number of dumps, newest first", func() {
		withDatabase(machine)
		ran := expectDump(machine)
		// floor 6 GiB (peak 3 GiB x 2); 400 bytes held: (free + 400 - floor) / 4 = 250 -> keep 2 dumps of 100.
		machine.free = []uint64{6<<30 + 600}

		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerDatabaseBackup)).To(Succeed())

		Expect(ran.User).To(Equal("1000:1000"))
		Expect(ran.Command).To(Equal([]string{"pg_dump", "--username", "csf", "--dbname", "csf", "--format=custom",
			"--file", "/backups/csf-20261002T120000Z.dump"}))
		Expect(machine.recorded(housekeeping.RecordBackup, state+"/"+newDump)).To(HaveField("Bytes", uint64(dumpBytes)))
		retention := machine.recorded(housekeeping.RecordRetention, state+"/backups")
		Expect(retention).NotTo(BeNil())
		Expect(retention.Bytes).To(Equal(uint64(2)))
		Expect(retention.Detail).To(HavePrefix("(6442451544 free + 400 held by backups - 6442450944 floor) / 4 = 250 bytes budget / 100 bytes newest dump = keep 2"))

		Expect(machine.ranNaming("rm", state+"/backups/csf-20261002T090000Z.dump")).To(BeTrue())
		Expect(machine.ranNaming("rm", state+"/backups/csf-20261002T100000Z.dump")).To(BeTrue())
		Expect(machine.ranNaming("rm", state+"/backups/csf-20261002T110000Z.dump")).To(BeFalse())
		Expect(machine.ranNaming("rm", state+"/"+newDump)).To(BeFalse())
		Expect(machine.ranNaming("rm", state+"/backups/notes.txt")).To(BeFalse())
		Expect(machine.recorded(housekeeping.RecordDeletion, state+"/backups/csf-20261002T090000Z.dump")).To(HaveField("Kind", housekeeping.KindBackup))
	})

	It("keeps at least the newest dump however little space is left", func() {
		withDatabase(machine)
		expectDump(machine)
		machine.free = []uint64{0}

		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerDatabaseBackup)).To(Succeed())
		Expect(machine.recorded(housekeeping.RecordRetention, state+"/backups")).To(HaveField("Bytes", uint64(1)))
		Expect(machine.ranNaming("rm", state+"/"+newDump)).To(BeFalse())
		Expect(machine.ranNaming("rm", state+"/backups/csf-20261002T110000Z.dump")).To(BeTrue())
	})

	It("fails the occurrence and removes nothing when pg_dump fails", func() {
		withDatabase(machine)
		machine.containers.EXPECT().Exec(gomock.Any(), databaseContainer, gomock.Any()).Return(
			docker.ExecResult{ExitCode: 1, Stderr: []byte("connection refused")}, nil)

		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerDatabaseBackup)).To(MatchError(ContainSubstring("connection refused")))
		Expect(machine.ran("rm")).To(BeFalse())
	})

	It("has nothing to back up on a host whose database is someone else's", func() {
		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerDatabaseBackup)).To(Succeed())
		Expect(machine.records).To(BeEmpty())
	})

	It("plans the retention without dumping or removing in a dry run", func() {
		withDatabase(machine)
		machine.free = []uint64{0}

		Expect(machine.housekeeper(housekeeping.WithDryRun()).Run(context.Background(), housekeeping.TriggerDatabaseBackup)).To(Succeed())
		Expect(machine.ran("rm")).To(BeFalse())
		Expect(machine.records).To(ContainElement(And(HaveField("Type", housekeeping.RecordDeletion), HaveField("DryRun", BeTrue()))))
	})
})
