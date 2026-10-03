package reconcile

import (
	"context"

	"google.golang.org/protobuf/proto"

	deployv1 "github.com/candacelabs/csf/proto/candace/deploy/v1"
	"github.com/candacelabs/csf/services/deploy"
)

// Prepare resolves the exact source revision covered by a deployment approval.
func (s *Service) Prepare(
	ctx context.Context,
	input *deployv1.ReconcileIntent,
) (*deployv1.ReconcileRevision, error) {
	input, err := ownedReconcileIntent(input)
	if err != nil {
		return nil, err
	}
	effective, revision, composePath, err := s.resolveInput(ctx, input)
	if err != nil {
		return nil, err
	}
	if _, _, err := assignmentFrom(effective, revision); err != nil {
		return nil, err
	}
	result := approvedRevision(revision, composePath)
	if err := deployv1.ValidateReconcileRevision(result); err != nil {
		return nil, err
	}
	return proto.Clone(result).(*deployv1.ReconcileRevision), nil
}

func approvedRevision(revision deploy.AppRevision, composePath string) *deployv1.ReconcileRevision {
	return &deployv1.ReconcileRevision{
		Id:             revision.ID,
		Source:         revision.Source,
		SourceRevision: revision.Revision,
		ContentDigest:  revision.Digest,
		ComposePath:    composePath,
	}
}
