package csf

import (
	"context"
	"errors"
	"strings"

	db "github.com/candacelabs/csf/csf/internal/brainspinedb"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const configurationAgentID = "configuration-agent"

func validAgentConfigurationInput(agentID string) *pb.AgentConfigurationInput {
	return &pb.AgentConfigurationInput{
		Langfuse:   &pb.AgentLangfuseConfiguration{PublicKeySecretRef: "agent/" + agentID + "/public", SecretKeySecretRef: "agent/" + agentID + "/secret"},
		Opensearch: &pb.AgentOpenSearchConfiguration{EndpointUrl: "https://search.example.invalid", Index: "agent-events", CredentialsSecretRef: "agent/" + agentID + "/search"},
	}
}

type agentConfigurationErrorDB struct {
	err error
}

func (database agentConfigurationErrorDB) Exec(ctx context.Context, query string, arguments ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, database.err
}

func (database agentConfigurationErrorDB) Query(ctx context.Context, query string, arguments ...interface{}) (pgx.Rows, error) {
	return nil, database.err
}

func (database agentConfigurationErrorDB) QueryRow(ctx context.Context, query string, arguments ...interface{}) pgx.Row {
	return agentConfigurationErrorRow{err: database.err}
}

type agentConfigurationErrorRow struct {
	err error
}

func (row agentConfigurationErrorRow) Scan(destinations ...any) error {
	return row.err
}

var _ = Describe("agent configuration Postgres errors", func() {
	It("retains both not-found classification and the database no-row error", func() {
		store := &Postgres{queries: db.New(agentConfigurationErrorDB{err: pgx.ErrNoRows})}

		_, err := store.GetAgentConfiguration(context.Background(), configurationAgentID)

		Expect(errors.Is(err, ErrNotFound)).To(BeTrue())
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("get agent configuration")))
	})

	It("retains both create-conflict classification and the database no-row error", func() {
		store := &Postgres{queries: db.New(agentConfigurationErrorDB{err: pgx.ErrNoRows})}

		_, err := store.PutAgentConfiguration(context.Background(), configurationAgentID, 0, validAgentConfigurationInput(configurationAgentID))

		Expect(errors.Is(err, ErrConflict)).To(BeTrue())
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("create agent configuration")))
	})

	It("retains both update-conflict classification and the database no-row error", func() {
		store := &Postgres{queries: db.New(agentConfigurationErrorDB{err: pgx.ErrNoRows})}

		_, err := store.PutAgentConfiguration(context.Background(), configurationAgentID, 1, validAgentConfigurationInput(configurationAgentID))

		Expect(errors.Is(err, ErrConflict)).To(BeTrue())
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("update agent configuration")))
	})

	It("adds operation context while preserving typed database errors", func() {
		databaseError := &pgconn.PgError{Code: "08006", Message: "connection failure"}
		store := &Postgres{queries: db.New(agentConfigurationErrorDB{err: databaseError})}

		_, err := store.GetAgentConfiguration(context.Background(), configurationAgentID)

		var retained *pgconn.PgError
		Expect(errors.As(err, &retained)).To(BeTrue())
		Expect(retained).To(BeIdenticalTo(databaseError))
		Expect(strings.Contains(err.Error(), "get agent configuration")).To(BeTrue())
	})
})
