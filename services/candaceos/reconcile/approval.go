package reconcile

import (
	"context"

	"google.golang.org/protobuf/proto"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos"
)

// Prepare resolves the exact source revision covered by a deployment approval.
func (s *Service) Prepare(
	ctx context.Context,
	input *candaceosv1.ReconcileIntent,
) (*candaceosv1.ReconcileRevision, error) {
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
	if err := candaceosv1.ValidateReconcileRevision(result); err != nil {
		return nil, err
	}
	return proto.Clone(result).(*candaceosv1.ReconcileRevision), nil
}

func approvedRevision(revision candaceos.AppRevision, composePath string) *candaceosv1.ReconcileRevision {
	return &candaceosv1.ReconcileRevision{
		Id:             revision.ID,
		Source:         revision.Source,
		SourceRevision: revision.Revision,
		ContentDigest:  revision.Digest,
		ComposePath:    composePath,
	}
}
