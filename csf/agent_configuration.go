package csf

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"google.golang.org/protobuf/proto"
)

const (
	agentSecretReferencePrefix        = "agent/"
	agentConfigurationRequiredMessage = "configuration required"
	langfuseConfigurationName         = "Langfuse"
	openSearchConfigurationName       = "OpenSearch"
	agentEndpointHTTPScheme           = "http"
	agentEndpointHTTPSScheme          = "https"
)

var agentSecretReferenceName = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)

type agentIdentity struct {
	id        string
	sessionID string
}

type agentIdentityContextKey struct{}

func withVerifiedAgentIdentity(ctx context.Context, id string, sessionID string) context.Context {
	return context.WithValue(ctx, agentIdentityContextKey{}, agentIdentity{id: id, sessionID: sessionID})
}

func agentIDFromContext(ctx context.Context) (string, error) {
	identity, ok := ctx.Value(agentIdentityContextKey{}).(agentIdentity)
	if !ok || identity.id == "" || identity.sessionID == "" {
		return "", ErrUnauthorized
	}
	return identity.id, nil
}

// GetOwnAgentConfiguration returns the configuration selected by the signed
// transport identity. Callers cannot choose an agent ID through this API.
func (service *Service) GetOwnAgentConfiguration(ctx context.Context, _ *pb.GetOwnAgentConfigurationRequest) (*pb.GetOwnAgentConfigurationResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if service.agentConfigurations == nil {
		return nil, ErrAgentConfigurationUnavailable
	}
	agentID, err := agentIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	configuration, err := service.agentConfigurations.GetAgentConfiguration(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if err := validateStoredAgentConfiguration(agentID, configuration); err != nil {
		return nil, err
	}
	return &pb.GetOwnAgentConfigurationResponse{Configuration: proto.CloneOf(configuration)}, nil
}

// UpdateOwnAgentConfiguration replaces only the signed caller's references.
// expected_revision is a compare-and-swap precondition owned by the store.
func (service *Service) UpdateOwnAgentConfiguration(ctx context.Context, request *pb.UpdateOwnAgentConfigurationRequest) (*pb.UpdateOwnAgentConfigurationResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if service.agentConfigurations == nil {
		return nil, ErrAgentConfigurationUnavailable
	}
	agentID, err := agentIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	configuration, err := validatedAgentConfigurationInput(agentID, request.GetConfiguration())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	updated, err := service.agentConfigurations.PutAgentConfiguration(ctx, agentID, request.GetExpectedRevision(), configuration)
	if err != nil {
		return nil, err
	}
	if err := validateStoredAgentConfiguration(agentID, updated); err != nil {
		return nil, err
	}
	return &pb.UpdateOwnAgentConfigurationResponse{Configuration: proto.CloneOf(updated)}, nil
}

func validateStoredAgentConfiguration(agentID string, configuration *pb.AgentConfiguration) error {
	if configuration == nil || configuration.AgentId != agentID {
		return fmt.Errorf("agent configuration store returned another identity")
	}
	if err := pb.ValidateAgentConfiguration(configuration); err != nil {
		return fmt.Errorf("invalid retained agent configuration: %w", err)
	}
	if err := validateAgentConfigurationReferences(agentID, configuration.Langfuse, configuration.Opensearch); err != nil {
		return fmt.Errorf("invalid retained agent configuration: %w", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, configuration.UpdatedAt); err != nil {
		return fmt.Errorf("invalid retained agent configuration timestamp: %w", err)
	}
	return nil
}

func validatedAgentConfigurationInput(agentID string, input *pb.AgentConfigurationInput) (*pb.AgentConfigurationInput, error) {
	if input == nil {
		return nil, fmt.Errorf(agentConfigurationRequiredMessage)
	}
	configuration := proto.CloneOf(input)
	if configuration.Langfuse == nil {
		configuration.Langfuse = &pb.AgentLangfuseConfiguration{}
	}
	if configuration.Opensearch == nil {
		configuration.Opensearch = &pb.AgentOpenSearchConfiguration{}
	}
	if err := pb.ValidateAgentConfigurationInput(configuration); err != nil {
		return nil, err
	}
	if err := validateAgentConfigurationReferences(agentID, configuration.Langfuse, configuration.Opensearch); err != nil {
		return nil, err
	}
	return configuration, nil
}

func validateAgentConfigurationReferences(agentID string, langfuse *pb.AgentLangfuseConfiguration, opensearch *pb.AgentOpenSearchConfiguration) error {
	if langfuse == nil || opensearch == nil {
		return fmt.Errorf(agentConfigurationRequiredMessage)
	}
	if err := validateAgentLangfuseConfiguration(agentID, langfuse); err != nil {
		return err
	}
	return validateAgentOpenSearchConfiguration(agentID, opensearch)
}

func validateAgentLangfuseConfiguration(agentID string, configuration *pb.AgentLangfuseConfiguration) error {
	enabled := configuration.EndpointUrl != "" || configuration.PublicKeySecretRef != "" || configuration.SecretKeySecretRef != ""
	if !enabled {
		return nil
	}
	if err := validateAgentEndpoint(langfuseConfigurationName, configuration.EndpointUrl); err != nil {
		return err
	}
	if err := validateAgentSecretReference(agentID, configuration.PublicKeySecretRef); err != nil {
		return fmt.Errorf("%s public key: %w", langfuseConfigurationName, err)
	}
	if err := validateAgentSecretReference(agentID, configuration.SecretKeySecretRef); err != nil {
		return fmt.Errorf("%s secret key: %w", langfuseConfigurationName, err)
	}
	return nil
}

func validateAgentOpenSearchConfiguration(agentID string, configuration *pb.AgentOpenSearchConfiguration) error {
	enabled := configuration.EndpointUrl != "" || configuration.Index != "" || configuration.EmbeddingModel != "" || configuration.CredentialsSecretRef != ""
	if !enabled {
		return nil
	}
	if err := validateAgentEndpoint(openSearchConfigurationName, configuration.EndpointUrl); err != nil {
		return err
	}
	if !searchIndexName.MatchString(configuration.Index) {
		return fmt.Errorf("%s index must match %s", openSearchConfigurationName, searchIndexName)
	}
	if err := validateAgentSecretReference(agentID, configuration.CredentialsSecretRef); err != nil {
		return fmt.Errorf("%s credentials: %w", openSearchConfigurationName, err)
	}
	return nil
}

func validateAgentEndpoint(name string, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != agentEndpointHTTPScheme && parsed.Scheme != agentEndpointHTTPSScheme) || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s endpoint must be an absolute HTTP(S) URL without credentials, query or fragment", name)
	}
	return nil
}

func validateAgentSecretReference(agentID string, reference string) error {
	prefix := agentSecretReferencePrefix + agentID + "/"
	if !strings.HasPrefix(reference, prefix) || !agentSecretReferenceName.MatchString(strings.TrimPrefix(reference, prefix)) {
		return fmt.Errorf("secret reference must remain under %q", prefix)
	}
	return nil
}
