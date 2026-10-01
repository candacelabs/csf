package e2e

// Multi-process e2e for the gRPC WatchCluster stream: an EXTERNAL gRPC client
// opens a cluster watch against a follower, the leader is killed, and the stream
// (continuing on the surviving follower, reconnecting to any survivor on error)
// must reflect the newly-elected leader at a strictly higher term. This proves
// the streaming plane end to end over the real single-port cmux server, not just
// the unary election RPCs the other e2e tests exercise.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	wardenv1 "github.com/candacelabs/csf/services/warden/proto/warden/v1"
)

// buildWardenBinary compiles the warden binary into a temp path.
func buildWardenBinary(t *testing.T) string {
	t.Helper()
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolving module root: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "warden")
	build := exec.Command("go", "build", "-o", bin, "github.com/candacelabs/csf/app/warden/cmd")
	build.Dir = moduleRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building warden binary: %v\n%s", err, out)
	}
	return bin
}

// newClusterOnPorts builds a 3-node cluster description on the given ports.
func newClusterOnPorts(t *testing.T, bin string, ids []string, ports []int) *cluster {
	t.Helper()
	tmp := t.TempDir()
	c := &cluster{t: t, bin: bin, nodes: map[string]*node{}}
	var peerParts []string
	for i, id := range ids {
		addr := fmt.Sprintf("127.0.0.1:%d", ports[i])
		dir := filepath.Join(tmp, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		c.nodes[id] = &node{
			id:        id,
			addr:      addr,
			dataDir:   dir,
			alertFile: filepath.Join(dir, "alerts.jsonl"),
			logFile:   filepath.Join(dir, "warden.log"),
		}
		peerParts = append(peerParts, id+"="+addr)
	}
	c.peers = strings.Join(peerParts, ",")
	return c
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// dialWarden opens an h2c gRPC client to a warden node (insecure creds; the loop
// is over localhost, standing in for the tailnet).
func dialWarden(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialing warden %s: %v", addr, err)
	}
	return cc
}

// openWatch opens a WatchCluster stream on cc.
func openWatch(t *testing.T, ctx context.Context, cc *grpc.ClientConn) wardenv1.WardenService_WatchClusterClient {
	t.Helper()
	stream, err := wardenv1.NewWardenServiceClient(cc).WatchCluster(ctx, &wardenv1.WatchClusterRequest{})
	if err != nil {
		t.Fatalf("opening WatchCluster: %v", err)
	}
	return stream
}

func TestClusterWatchLeaderChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-process e2e test in -short mode")
	}

	bin := buildWardenBinary(t)
	ids := []string{"n1", "n2", "n3"}
	ports := []int{19721, 19722, 19723}
	c := newClusterOnPorts(t, bin, ids, ports)
	for _, id := range ids {
		c.start(id)
	}
	t.Cleanup(c.stopAll)

	leader0, term0 := c.awaitConsistentLeader(ids, "", 0, "initial election")

	// Pick a follower to stream from (it survives the leader kill).
	var follower string
	for _, id := range ids {
		if id != leader0 {
			follower = id
			break
		}
	}
	t.Logf("streaming WatchCluster from follower %s (leader=%s term=%d)", follower, leader0, term0)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Open the watch and read the initial snapshot (proves the stream works).
	cc := dialWarden(t, c.nodes[follower].addr)
	stream := openWatch(t, ctx, cc)
	initial, err := stream.Recv()
	if err != nil {
		t.Fatalf("initial watch recv from %s: %v", follower, err)
	}
	t.Logf("initial snapshot: leader=%s term=%d authoritative=%v",
		initial.GetView().GetLeaderId(), initial.GetView().GetTerm(), initial.GetView().GetAuthoritative())

	// Kill the leader; survivors re-elect.
	c.kill(leader0)
	survivors := remove(ids, leader0)

	// Read the stream until it reflects a new leader at a higher term,
	// reconnecting to any survivor if the current stream errors.
	deadline := time.Now().Add(45 * time.Second)
	curCC, curStream := cc, stream
	for {
		if time.Now().After(deadline) {
			curCC.Close()
			c.dumpLogs()
			t.Fatalf("watch never reflected a new leader after killing %s within 45s", leader0)
		}
		upd, err := curStream.Recv()
		if err != nil {
			// The streamed node became unreachable; reconnect to any survivor.
			curCC.Close()
			curCC, curStream = nil, nil
			for _, s := range survivors {
				ncc := dialWarden(t, c.nodes[s].addr)
				ns, serr := wardenv1.NewWardenServiceClient(ncc).WatchCluster(ctx, &wardenv1.WatchClusterRequest{})
				if serr != nil {
					ncc.Close()
					continue
				}
				curCC, curStream = ncc, ns
				t.Logf("reconnected watch to survivor %s", s)
				break
			}
			if curStream == nil {
				// Pacing, not an await: this loop is already blocked on the
				// stream, and the sleep spaces out reconnection attempts
				// against a cluster that is mid-election. Nothing is being
				// polled for a condition here, so there is nothing for
				// patience.Await to own.
				time.Sleep(pollEvery)
			}
			continue
		}
		v := upd.GetView()
		if v.GetAuthoritative() && v.GetTerm() > term0 &&
			v.GetLeaderId() != "" && v.GetLeaderId() != leader0 && contains(survivors, v.GetLeaderId()) {
			t.Logf("watch reflected new leader=%s term=%d (was leader=%s term=%d)",
				v.GetLeaderId(), v.GetTerm(), leader0, term0)
			curCC.Close()
			return
		}
	}
}
