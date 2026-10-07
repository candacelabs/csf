package nodeexec

import (
	"time"

	deployv1 "github.com/candacelabs/csf/proto/candace/deploy/v1"
	"google.golang.org/protobuf/proto"
)

// Snapshot is the node-local durable reconciliation record.
type Snapshot struct {
	Fence      *deployv1.Fence
	Assignment *deployv1.Assignment
	Commands   []Command
	UpdatedAt  time.Time
}

func cloneSnapshot(in Snapshot) Snapshot {
	out := in
	out.Fence = cloneFence(in.Fence)
	out.Assignment = cloneAssignment(in.Assignment)
	out.Commands = cloneCommands(in.Commands)
	return out
}

func cloneFence(in *deployv1.Fence) *deployv1.Fence {
	if in == nil {
		return nil
	}
	return proto.Clone(in).(*deployv1.Fence)
}

func cloneAssignment(in *deployv1.Assignment) *deployv1.Assignment {
	if in == nil {
		return nil
	}
	return proto.Clone(in).(*deployv1.Assignment)
}
