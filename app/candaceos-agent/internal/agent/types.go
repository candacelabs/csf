package agent

import (
	"time"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"google.golang.org/protobuf/proto"
)

// Command is a directly executed argv vector. It is never interpreted by a
// shell.
type Command struct {
	Argv []string `json:"argv"`
}

// Plan is a validated preflight plus convergence command.
type Plan struct {
	Commands []Command `json:"commands"`
}

// Snapshot is the node-local durable reconciliation record.
type Snapshot struct {
	Fence      *candaceosv1.Fence
	Assignment *candaceosv1.Assignment
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

func cloneFence(in *candaceosv1.Fence) *candaceosv1.Fence {
	if in == nil {
		return nil
	}
	return proto.Clone(in).(*candaceosv1.Fence)
}

func cloneAssignment(in *candaceosv1.Assignment) *candaceosv1.Assignment {
	if in == nil {
		return nil
	}
	return proto.Clone(in).(*candaceosv1.Assignment)
}

func cloneCommands(in []Command) []Command {
	out := make([]Command, len(in))
	for i := range in {
		out[i].Argv = append([]string(nil), in[i].Argv...)
	}
	return out
}
