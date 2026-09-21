package httpapi

import (
	"github.com/candacelabs/csf/app/candaceos-agent/internal/agent"
	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func healthToProto(nodeID string, dryRun bool) *candaceosv1.HealthResponse {
	return &candaceosv1.HealthResponse{
		Status: "ok",
		NodeId: nodeID,
		DryRun: dryRun,
	}
}

func statusToProto(nodeID string, dryRun bool, workspace string, snapshot agent.Snapshot) *candaceosv1.AgentStatus {
	status := &candaceosv1.AgentStatus{
		NodeId:    nodeID,
		DryRun:    dryRun,
		Workspace: workspace,
		Commands:  agent.CommandsToProto(snapshot.Commands),
	}
	if snapshot.Fence != nil {
		status.Fence = proto.Clone(snapshot.Fence).(*candaceosv1.Fence)
	}
	if snapshot.Assignment != nil {
		status.Assignment = proto.Clone(snapshot.Assignment).(*candaceosv1.Assignment)
	}
	if !snapshot.UpdatedAt.IsZero() {
		status.UpdatedAt = timestamppb.New(snapshot.UpdatedAt)
	}
	return status
}
