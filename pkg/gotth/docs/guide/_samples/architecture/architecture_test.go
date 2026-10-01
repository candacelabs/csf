package architecture_test

import (
	"bytes"
	"context"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/docs/guide/_samples/architecture"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

// This spec exists because the page it backs makes one claim a reader cannot
// check from the outside and that this project's own guide got wrong until
// 2026-08-05: Authorize does NOT run on the session's actor goroutine. It runs
// on the connection's read pump, ahead of the mailbox.
//
// The difference is not academic. It is why a refused event costs a mailbox
// slot of nothing, and it is why blocking in Authorize stalls acknowledgements
// and heartbeats — a failure mode a reader who believes the hook runs behind
// the mailbox will not predict.
//
// The observation is taken in Init and in Authorize, and deliberately not in
// Reduce: a reducer that recorded anything would be the FR-14 violation this
// tree just finished removing from the sample next door.

// goroutineID reads the running goroutine's identifier out of its own stack
// header, which is the only way Go exposes it.
//
// It is a test-only device and it is here rather than in the sample for that
// reason: nothing an application writes should depend on this number. The
// header's first line is "goroutine <n> [running]:" and has been since Go 1.0.
func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	line := buf[:n]
	line = bytes.TrimPrefix(line, []byte("goroutine "))
	if i := bytes.IndexByte(line, ' '); i >= 0 {
		line = line[:i]
	}
	id, err := strconv.ParseUint(string(line), 10, 64)
	Expect(err).NotTo(HaveOccurred(), "the goroutine stack header did not start with an identifier: %q", buf[:n])
	return id
}

// observer records which goroutine each hook was called on.
type observer struct {
	mu        sync.Mutex
	init      uint64
	authorize []uint64
}

func (o *observer) record(dst *uint64) {
	id := goroutineID()
	o.mu.Lock()
	defer o.mu.Unlock()
	*dst = id
}

func (o *observer) recordAuthorize() {
	id := goroutineID()
	o.mu.Lock()
	defer o.mu.Unlock()
	o.authorize = append(o.authorize, id)
}

func (o *observer) snapshot() (initGID uint64, authorizeGIDs []uint64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.init, append([]uint64(nil), o.authorize...)
}

var _ = Describe("where the hooks run", func() {
	const origin = "http://127.0.0.1:8080"

	var (
		obs    *observer
		room   *architecture.Room
		client *livetest.Client
	)

	BeforeEach(func() {
		obs = &observer{}
		room = architecture.NewRoom()

		cfg := architecture.Config(room, []string{origin})

		// The sample's own hooks, wrapped rather than replaced, so this spec
		// measures the application on the page and not a fixture that
		// resembles it.
		init := cfg.Init
		cfg.Init = func(ctx context.Context, sess live.Session[live.AnonymousIdentity]) (architecture.State, []live.Effect[live.AnonymousIdentity], error) {
			obs.record(&obs.init)
			return init(ctx, sess)
		}
		authorize := cfg.Authorize
		cfg.Authorize = func(ctx context.Context, sess live.Session[live.AnonymousIdentity], ev live.Event) error {
			obs.recordAuthorize()
			return authorize(ctx, sess, ev)
		}

		app, err := live.New(cfg)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = app.Close(context.Background()) })

		mux := http.NewServeMux()
		mux.Handle("/live", app.Handler())
		mux.Handle("/live/", app.Handler())

		client = livetest.NewClient(GinkgoTB(), mux, livetest.ClientOptions{
			Path:   "/live",
			Origin: origin,
		})
		DeferCleanup(func() { _ = client.Close() })
	})

	It("authorizes on a different goroutine than the one that owns the session", func() {
		client.Send(architecture.EventShout, architecture.FragmentBoard,
			map[string]string{architecture.FieldBody: "hello"})
		client.WaitFor(architecture.FragmentBoard, func(html string) bool {
			return len(html) > 0
		})

		initGID, authorizeGIDs := obs.snapshot()
		Expect(initGID).NotTo(BeZero(), "Init was never called, so this spec measured nothing")
		Expect(authorizeGIDs).NotTo(BeEmpty(), "Authorize was never called, so this spec measured nothing")
		Expect(authorizeGIDs[0]).NotTo(Equal(initGID),
			"Authorize ran on the same goroutine as Init, which is the session's actor. "+
				"Either the library moved authorization behind the mailbox, or "+
				"docs/guide/architecture.md is now wrong about where it runs.")
	})

	It("authorizes every event of one connection on the same goroutine", func() {
		for _, body := range []string{"one", "two", "three"} {
			client.Send(architecture.EventShout, architecture.FragmentBoard,
				map[string]string{architecture.FieldBody: body})
		}
		Eventually(func() int { return len(room.Said()) }, 5*time.Second).Should(Equal(3))

		_, authorizeGIDs := obs.snapshot()
		Expect(authorizeGIDs).To(HaveLen(3))
		for _, id := range authorizeGIDs[1:] {
			Expect(id).To(Equal(authorizeGIDs[0]),
				"one connection has one read pump, so its events authorize on one goroutine")
		}
	})

	It("refuses an event at the read pump without it ever reaching the reducer", func() {
		long := make([]byte, 281)
		for i := range long {
			long[i] = 'a'
		}
		client.Send(architecture.EventShout, architecture.FragmentBoard,
			map[string]string{architecture.FieldBody: string(long)})

		// The refusal is an Error frame, and the room never heard it: the
		// event was denied before it occupied a mailbox slot.
		Consistently(func() int { return len(room.Said()) }, 500*time.Millisecond).Should(BeZero())
	})
})
