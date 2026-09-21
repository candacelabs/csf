package csf

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	db "github.com/candacelabs/csf/csf/internal/brainspinedb"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/jackc/pgx/v5"
)

var _ IAgentConfigurationStore = (*Postgres)(nil)

func (store *Postgres) GetAgentConfiguration(ctx context.Context, agentID string) (*pb.AgentConfiguration, error) {
	configuration, err := store.queries.GetAgentConfiguration(ctx, agentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get agent configuration: %w: %w", ErrNotFound, err)
		}
		return nil, fmt.Errorf("get agent configuration: %w", err)
	}
	return agentConfigurationMessage(configuration)
}

func (store *Postgres) PutAgentConfiguration(ctx context.Context, agentID string, expectedRevision uint32, configuration *pb.AgentConfigurationInput) (*pb.AgentConfiguration, error) {
	if configuration == nil || configuration.Langfuse == nil || configuration.Opensearch == nil {
		return nil, fmt.Errorf(agentConfigurationRequiredMessage)
	}
	parameters := db.CreateAgentConfigurationParams{
		AgentID:                        agentID,
		LangfuseEndpointUrl:            configuration.Langfuse.EndpointUrl,
		LangfusePublicKeySecretRef:     configuration.Langfuse.PublicKeySecretRef,
		LangfuseSecretKeySecretRef:     configuration.Langfuse.SecretKeySecretRef,
		OpensearchEndpointUrl:          configuration.Opensearch.EndpointUrl,
		OpensearchIndex:                configuration.Opensearch.Index,
		OpensearchEmbeddingModel:       configuration.Opensearch.EmbeddingModel,
		OpensearchCredentialsSecretRef: configuration.Opensearch.CredentialsSecretRef,
	}
	if expectedRevision == 0 {
		created, err := store.queries.CreateAgentConfiguration(ctx, parameters)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("create agent configuration: %w: %w", ErrConflict, err)
			}
			return nil, fmt.Errorf("create agent configuration: %w", err)
		}
		return agentConfigurationMessage(created)
	}
	updated, err := store.queries.UpdateAgentConfiguration(ctx, db.UpdateAgentConfigurationParams{
		LangfuseEndpointUrl:            parameters.LangfuseEndpointUrl,
		LangfusePublicKeySecretRef:     parameters.LangfusePublicKeySecretRef,
		LangfuseSecretKeySecretRef:     parameters.LangfuseSecretKeySecretRef,
		OpensearchEndpointUrl:          parameters.OpensearchEndpointUrl,
		OpensearchIndex:                parameters.OpensearchIndex,
		OpensearchEmbeddingModel:       parameters.OpensearchEmbeddingModel,
		OpensearchCredentialsSecretRef: parameters.OpensearchCredentialsSecretRef,
		AgentID:                        agentID,
		ExpectedRevision:               int64(expectedRevision),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("update agent configuration: %w: %w", ErrConflict, err)
		}
		return nil, fmt.Errorf("update agent configuration: %w", err)
	}
	return agentConfigurationMessage(updated)
}

func agentConfigurationMessage(configuration db.CsfAgentConfiguration) (*pb.AgentConfiguration, error) {
	if configuration.Revision < 1 || configuration.Revision > math.MaxUint32 || !configuration.UpdatedAt.Valid {
		return nil, fmt.Errorf("invalid stored agent configuration")
	}
	return &pb.AgentConfiguration{
		AgentId:  configuration.AgentID,
		Revision: uint32(configuration.Revision),
		Langfuse: &pb.AgentLangfuseConfiguration{
			EndpointUrl:        configuration.LangfuseEndpointUrl,
			PublicKeySecretRef: configuration.LangfusePublicKeySecretRef,
			SecretKeySecretRef: configuration.LangfuseSecretKeySecretRef,
		},
		Opensearch: &pb.AgentOpenSearchConfiguration{
			EndpointUrl:          configuration.OpensearchEndpointUrl,
			Index:                configuration.OpensearchIndex,
			EmbeddingModel:       configuration.OpensearchEmbeddingModel,
			CredentialsSecretRef: configuration.OpensearchCredentialsSecretRef,
		},
		UpdatedAt: configuration.UpdatedAt.Time.UTC().Format(time.RFC3339Nano),
	}, nil
}
