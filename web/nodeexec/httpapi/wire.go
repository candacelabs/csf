package httpapi

import (
	deployv1 "github.com/candacelabs/csf/proto/candace/deploy/v1"
	"github.com/candacelabs/csf/services/nodeexec"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func healthToProto(nodeID string, dryRun bool) *deployv1.HealthResponse {
	return &deployv1.HealthResponse{
		Status: "ok",
		NodeId: nodeID,
		DryRun: dryRun,
	}
}

func statusToProto(nodeID string, dryRun bool, workspace string, snapshot nodeexec.Snapshot) *deployv1.AgentStatus {
	status := &deployv1.AgentStatus{
		NodeId:    nodeID,
		DryRun:    dryRun,
		Workspace: workspace,
		Commands:  nodeexec.CommandsToProto(snapshot.Commands),
	}
	if snapshot.Fence != nil {
		status.Fence = proto.Clone(snapshot.Fence).(*deployv1.Fence)
	}
	if snapshot.Assignment != nil {
		status.Assignment = proto.Clone(snapshot.Assignment).(*deployv1.Assignment)
	}
	if !snapshot.UpdatedAt.IsZero() {
		status.UpdatedAt = timestamppb.New(snapshot.UpdatedAt)
	}
	return status
}
