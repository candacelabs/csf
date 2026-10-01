package conformance_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// ---------------------------------------------------------------------------
// The counter example, end to end, against the real binary.
//
// This is not the example's own test. It compiles examples/counter as a
// consumer would, runs the resulting binary as its own process, and drives it
// over a real WebSocket with the generated codecs — so nothing in this spec
// shares memory, a logger, or a test double with the server it is checking.
// The provenance assertion in particular is a join between a frame that
// crossed a socket and a JSON line that crossed a pipe.
//
// It is opt-in because it compiles a second module. Run it with:
//
//	GOTTHLIVE_E2E=1 go test ./test/... -args -ginkgo.label-filter=e2e
// ---------------------------------------------------------------------------

func e2eOnly() {
	GinkgoHelper()
	if os.Getenv("GOTTHLIVE_E2E") == "" {
		Skip("e2e: set GOTTHLIVE_E2E=1 to run (compiles examples/counter)")
	}
}

// counterServer is the example running as its own process.
type counterServer struct {
	addr   string
	origin string

	mu    sync.Mutex
	lines []string
}

// startCounter builds and runs examples/counter, returning once it is serving.
//
// extraOrigins are added to the example's allowlist with its -origin flag. A
// caller that fronts the counter with a proxy needs this: the browser's Origin
// header then names the proxy, and the example's allowlist is derived from its
// own listen address, so without it the handshake is refused — correctly, which
// is why the answer is to configure the allowlist rather than to weaken it.
func startCounter(extraOrigins ...string) *counterServer {
	GinkgoHelper()

	// The examples left this tree when gotth-live became candace/pkg/gotth, so
	// the path walks out to the export root — two directories above the library
	// root — and back down into examples/gotth.
	libraryRoot, err := filepath.Abs("../../..")
	Expect(err).NotTo(HaveOccurred())
	exportRoot := filepath.Dir(filepath.Dir(libraryRoot))
	src := filepath.Join(exportRoot, "examples", "gotth", "counter")
	Expect(src).To(BeADirectory(), "examples/gotth/counter is not present")

	bin := filepath.Join(GinkgoT().TempDir(), "counter")
	// -buildvcs=false: the fixture binary needs no VCS stamp, and CI mounts the
	// checkout under a different owner, where git status exits 128.
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".")
	build.Dir = src
	out, err := build.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "building examples/gotth/counter failed:\n%s", out)

	addr := freeAddr()
	args := []string{"-addr", addr, "-provenance"}
	if len(extraOrigins) > 0 {
		args = append(args, "-origin", strings.Join(extraOrigins, ","))
	}
	cmd := exec.Command(bin, args...)
	stdout, err := cmd.StdoutPipe()
	Expect(err).NotTo(HaveOccurred())
	cmd.Stderr = cmd.Stdout

	s := &counterServer{addr: addr, origin: "http://" + addr}
	Expect(cmd.Start()).To(Succeed())
	DeferCleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			s.mu.Lock()
			s.lines = append(s.lines, scanner.Text())
			s.mu.Unlock()
		}
	}()

	// Wait for the listener rather than for a fixed sleep.
	Eventually(func() error {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			return err
		}
		return conn.Close()
	}, 30*time.Second, 100*time.Millisecond).Should(Succeed(), "the counter never started listening")

	return s
}

func freeAddr() string {
	GinkgoHelper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	addr := l.Addr().String()
	Expect(l.Close()).To(Succeed())
	return addr
}

// provenanceRows returns every provenance record the process has logged.
func (s *counterServer) provenanceRows() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []map[string]any
	for _, line := range s.lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "{") {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		if row["logger"] != "gotthlive.provenance" {
			continue
		}
		out = append(out, row)
	}
	return out
}

// dialCounter opens a live session against the running example.
func (s *counterServer) dial(origin string) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	DeferCleanup(cancel)

	headers := http.Header{}
	if origin != "" {
		headers.Set("Origin", origin)
	}
	conn, resp, err := websocket.Dial(ctx, "ws://"+s.addr+"/live", &websocket.DialOptions{
		HTTPHeader:   headers,
		Subprotocols: []string{"gotth-live.v1"},
	})
	if conn != nil {
		DeferCleanup(func() { _ = conn.CloseNow() })
	}
	return conn, resp, err
}

var _ = Describe("The counter example, end to end", Label("e2e"), func() {
	It("carries a click through to a patch whose provenance resolves", func() {
		e2eOnly()

		server := startCounter()

		conn, _, err := server.dial(server.origin)
		Expect(err).NotTo(HaveOccurred(), "a correctly-originated client could not connect")

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		DeferCleanup(cancel)

		read := func() *pb.Frame {
			GinkgoHelper()
			typ, data, err := conn.Read(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(typ).To(Equal(websocket.MessageBinary),
				"every payload in both directions is a binary Frame (FR-3)")
			var f pb.Frame
			Expect(proto.Unmarshal(data, &f)).To(Succeed(),
				"a payload off the wire did not parse as a gotthlive.v1.Frame")
			return &f
		}

		// 1. The initial render.
		first := read()
		snap := first.GetSnapshot()
		Expect(snap).NotTo(BeNil(), "the first frame must be the Snapshot (H-10)")
		Expect(first.GetSessionId()).To(HaveLen(16))
		Expect(snap.GetServerSeq()).To(Equal(uint64(1)))
		Expect(snap.GetOrigin().GetKind()).To(Equal(pb.OriginKind_MOUNT))

		var rendered []string
		for _, u := range snap.GetUpdates() {
			rendered = append(rendered, u.GetFragmentId())
			Expect(u.GetHtml()).NotTo(BeEmpty(), "fragment %s rendered nothing", u.GetFragmentId())
		}
		Expect(rendered).To(ContainElement("counter.value"))
		Expect(rendered).To(ContainElement("counter.controls"))

		// 2. The click, as the browser runtime would encode it.
		const myRef = 4242
		event := &pb.Frame{
			ProtocolVersion: 1,
			SessionId:       first.GetSessionId(),
			Payload: &pb.Frame_Event{Event: &pb.Event{
				ClientRef:     myRef,
				Name:          "counter.increment",
				FragmentId:    "counter.controls",
				SeenServerSeq: snap.GetServerSeq(),
			}},
		}
		encoded, err := proto.Marshal(event)
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Write(ctx, websocket.MessageBinary, encoded)).To(Succeed())

		// 3. The patch the click produced.
		//
		// It carries origin EFFECT, not CLIENT_EVENT, and that is the example's
		// architecture rather than a surprise: the reducer delegates to a shared
		// store, so the click's own transition changes no session state and is
		// suppressed, and the visible patch is the one the store's broadcast
		// produced. Both transitions are real and both are recorded.
		var patch *pb.Patch
		for patch == nil {
			f := read()
			patch = f.GetPatch()
		}

		Expect(patch.GetServerSeq()).To(Equal(snap.GetServerSeq()+1),
			"P3: the sequence must advance by exactly one")
		Expect(patch.GetUpdates()).NotTo(BeEmpty())
		Expect(patch.GetUpdates()[0].GetHtml()).To(ContainSubstring(">1<"),
			"the click did not reach the rendered value")

		origin := patch.GetOrigin()
		Expect(origin.GetSource()).NotTo(BeEmpty(), "P1: no orphan patches")
		Expect(origin.GetKind()).NotTo(Equal(pb.OriginKind_ORIGIN_KIND_UNSPECIFIED))

		// 4. The click's own transition is in the index, with the identifiers
		//    the browser sent.
		var clickRow map[string]any
		Eventually(func() map[string]any {
			for _, row := range server.provenanceRows() {
				if row["origin_source"] == "event:counter.increment" {
					clickRow = row
					return row
				}
			}
			return nil
		}, 10*time.Second, 100*time.Millisecond).ShouldNot(BeNil(),
			"the click produced no provenance record at all")

		Expect(clickRow["origin_kind"]).To(Equal("CLIENT_EVENT"))
		Expect(uint64(clickRow["client_ref"].(float64))).To(Equal(uint64(myRef)),
			"the recorded transition does not carry the client_ref the browser sent")
		Expect(uint64(clickRow["event_id"].(float64))).NotTo(BeZero())

		// 5. The visible patch resolves, from its own bytes plus the log.
		var patchRow map[string]any
		Eventually(func() map[string]any {
			for _, row := range server.provenanceRows() {
				if uint64(row["patch_id"].(float64)) == patch.GetPatchId() {
					patchRow = row
					return row
				}
			}
			return nil
		}, 10*time.Second, 100*time.Millisecond).ShouldNot(BeNil(),
			"no provenance record names patch_id %d", patch.GetPatchId())

		Expect(uint64(patchRow["transition_id"].(float64))).To(Equal(patch.GetTransitionId()))
		Expect(uint64(patchRow["state_version"].(float64))).To(Equal(patch.GetStateVersion()))
		Expect(uint64(patchRow["server_seq"].(float64))).To(Equal(patch.GetServerSeq()))
		Expect(patchRow["origin_source"]).To(Equal(origin.GetSource()))
		Expect(patchRow["origin_source"]).NotTo(Equal("unknown"), "FR-42 forbids unknown")

		AddReportEntry("counter e2e — what the wire actually carries", fmt.Sprintf(
			"click:  client_ref %d → event_id %v, transition %v, patch_id %v (suppressed, state unchanged)\n"+
				"patch:  transition %v, state_version %v, patch_id %d, server_seq %d, origin %s/%q, contributing %v",
			myRef, clickRow["event_id"], clickRow["transition_id"], clickRow["patch_id"],
			patch.GetTransitionId(), patch.GetStateVersion(), patch.GetPatchId(),
			patch.GetServerSeq(), origin.GetKind(), origin.GetSource(),
			origin.GetContributingEventIds()))
	})

	// QA-1 defect D-1, re-verified 2026-08-04 after DEV-1's remediation.
	//
	// FR-42 requires a server-effect patch to name "the upstream event that
	// scheduled it" where one exists, and protocol.md §4.2 puts that id in
	// contributing_event_ids. The counter's only user-visible interaction has
	// one — the click — and before the fix it was discarded twice over.
	//
	// The positive arm is below. The negative arm is the spec after it, and it
	// is the one that matters more: an edge is only worth carrying if it is
	// never fabricated.
	It("names the click among the contributing events of the patch it caused (FR-42, D-1)", func() {
		e2eOnly()

		server := startCounter()

		conn, _, err := server.dial(server.origin)
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		DeferCleanup(cancel)

		read := func() *pb.Frame {
			GinkgoHelper()
			_, data, err := conn.Read(ctx)
			Expect(err).NotTo(HaveOccurred())
			var f pb.Frame
			Expect(proto.Unmarshal(data, &f)).To(Succeed())
			return &f
		}

		first := read()
		encoded, err := proto.Marshal(&pb.Frame{
			ProtocolVersion: 1, SessionId: first.GetSessionId(),
			Payload: &pb.Frame_Event{Event: &pb.Event{
				ClientRef: 4242, Name: "counter.increment",
				FragmentId: "counter.controls", SeenServerSeq: 1,
			}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Write(ctx, websocket.MessageBinary, encoded)).To(Succeed())

		var patch *pb.Patch
		for patch == nil {
			patch = read().GetPatch()
		}

		Expect(patch.GetOrigin().GetContributingEventIds()).NotTo(BeEmpty(),
			"the patch a click caused names no upstream event, so the causal chain "+
				"from the click to the DOM is broken on the wire")

		// The edge must name a real event of THIS session. The click was the
		// session's first inbound event, so the server minted it event_id 1.
		Expect(patch.GetOrigin().GetContributingEventIds()).To(ContainElement(uint64(1)),
			"the contributing edge does not name the click's own event id")
	})

	// The negative arm of D-1, and the failure mode a fix for it invites.
	//
	// A causal id is session-scoped: event 1 in one session and event 1 in
	// another are different events that share a number. So when a shared store
	// fans a change out to every subscribed tab, the tab that clicked may carry
	// the edge and every other tab must carry none — inventing one would make
	// the provenance chain confidently wrong, which is worse than the empty
	// field the defect started as.
	It("invents no contributing edge for the tabs that did not click (FR-42, D-1)", func() {
		e2eOnly()

		server := startCounter()

		type tab struct {
			conn *websocket.Conn
			id   []byte
			in   chan *pb.Frame
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		DeferCleanup(cancel)

		// Each tab gets a background pump. Reading on demand with a per-read
		// deadline would close the socket the moment a spec waited for silence
		// — coder/websocket cancels the connection with the Read context — and
		// waiting for silence is exactly what this spec has to do.
		open := func() tab {
			GinkgoHelper()
			conn, _, err := server.dial(server.origin)
			Expect(err).NotTo(HaveOccurred())

			in := make(chan *pb.Frame, 256)
			go func() {
				defer close(in)
				for {
					_, data, err := conn.Read(ctx)
					if err != nil {
						return
					}
					var f pb.Frame
					if proto.Unmarshal(data, &f) != nil {
						return
					}
					in <- &f
				}
			}()

			select {
			case f := <-in:
				Expect(f.GetSnapshot()).NotTo(BeNil())
				return tab{conn: conn, id: f.GetSessionId(), in: in}
			case <-time.After(10 * time.Second):
				Fail("a tab never received its opening snapshot")
				return tab{}
			}
		}

		next := func(t tab, wait time.Duration) *pb.Frame {
			select {
			case f, ok := <-t.in:
				if !ok {
					return nil
				}
				return f
			case <-time.After(wait):
				return nil
			}
		}

		clicker := open()
		watcher := open()

		// The watcher has already been told the tab count changed. Settle it to
		// a known point, so what arrives after the click is attributable to it.
		for next(watcher, 700*time.Millisecond) != nil {
		}

		encoded, err := proto.Marshal(&pb.Frame{
			ProtocolVersion: 1, SessionId: clicker.id,
			Payload: &pb.Frame_Event{Event: &pb.Event{
				ClientRef: 77, Name: "counter.increment",
				FragmentId: "counter.controls", SeenServerSeq: 1,
			}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(clicker.conn.Write(ctx, websocket.MessageBinary, encoded)).To(Succeed())

		// Collect what the watcher sees as a result of somebody else's click.
		var seen []*pb.Patch
		for {
			f := next(watcher, 3*time.Second)
			if f == nil {
				break
			}
			if p := f.GetPatch(); p != nil {
				seen = append(seen, p)
			}
		}

		Expect(seen).NotTo(BeEmpty(),
			"the watching tab never saw the other tab's change, so the fan-out was not exercised")

		for _, p := range seen {
			Expect(p.GetOrigin().GetSource()).NotTo(BeEmpty(), "P1 still holds on a fan-out patch")
			Expect(p.GetOrigin().GetEventId()).To(BeZero(),
				"a fan-out patch claims event_id %d, but this session did not raise it",
				p.GetOrigin().GetEventId())
			Expect(p.GetOrigin().GetContributingEventIds()).To(BeEmpty(),
				"a fan-out patch to a tab that did not click carries contributing edge %v: "+
					"causal ids are session-scoped, so this names an event of somebody else's session",
				p.GetOrigin().GetContributingEventIds())
		}
	})

	It("refuses a cross-origin connection", func() {
		e2eOnly()

		server := startCounter()

		_, resp, err := server.dial("http://evil.example")

		Expect(err).To(HaveOccurred(), "a cross-origin page opened a live session against the example")
		Expect(resp).NotTo(BeNil())
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
	})

	It("refuses a connection that sends no origin", func() {
		e2eOnly()

		server := startCounter()

		_, resp, err := server.dial("")

		Expect(err).To(HaveOccurred(), "a request with no Origin header opened a live session")
		Expect(resp).NotTo(BeNil())
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
	})

	// The example binds every session to live.Anonymous, which is the named,
	// greppable opt-out rather than an absent hook. So the rejection path this
	// example can demonstrate is the origin one; the identity rejection is
	// asserted against a configured hook in the handshake specs above. Stated
	// here rather than left as a gap in the coverage.
	It("binds an identity to the session even when it is the anonymous one", func() {
		e2eOnly()

		server := startCounter()

		conn, _, err := server.dial(server.origin)
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		DeferCleanup(cancel)

		_, data, err := conn.Read(ctx)
		Expect(err).NotTo(HaveOccurred())

		var f pb.Frame
		Expect(proto.Unmarshal(data, &f)).To(Succeed())
		Expect(f.GetSessionId()).To(HaveLen(16),
			"a session was established without a server-minted 16-byte identifier")
	})
})
