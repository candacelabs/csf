package mounting_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/docs/guide/_samples/mounting"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// These specs are the executable half of docs/quickstart.md §2. Each one pins a
// sentence that page now states, and three of them pin a failure that page now
// tells a reader how to recognise: a mount that redirects the upgrade, a
// runtime URL answered by the catch-all, and a first paint frozen at start-up.
//
// They are behavioural rather than table-driven because each failure has a
// different shape, and the shape is the finding. No mocks: the collaborators
// here are net/http's own router and templ's own handler, and substituting
// either would test the substitute.

const mountPath = "/app/ui"

// stubApp stands in for App.Handler(). What matters about the real one is that
// it routes by path suffix and therefore does not care what prefix it is
// reached at, which is exactly the property these specs exercise.
func stubApp() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Reached", "app")
		_, _ = w.Write([]byte(r.URL.Path))
	})
}

func stubPage() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Reached", "page")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html></html>"))
	})
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

var _ = Describe("Routes", func() {
	var router http.Handler

	BeforeEach(func() {
		router = mounting.Routes(mountPath, stubApp(), stubPage())
	})

	It("delivers the upgrade path to the live handler, with no redirect", func() {
		rec := get(router, mountPath)
		Expect(rec.Code).To(Equal(http.StatusOK),
			"a redirect here is not an upgrade: a WebSocket client cannot follow one")
		Expect(rec.Header().Get("X-Reached")).To(Equal("app"))
	})

	It("delivers the runtime's URL to the live handler", func() {
		rec := get(router, mountPath+"/gotth-live.min.js")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("X-Reached")).To(Equal("app"))
	})

	It("passes the whole path through, because the handler needs no stripping", func() {
		rec := get(router, mountPath+"/gotth-live.min.js")
		Expect(rec.Body.String()).To(Equal(mountPath + "/gotth-live.min.js"))
	})

	It("answers a path neither live pattern claims with the page", func() {
		// The catch-all, stated because it is the mechanism that makes the
		// missing-subtree mistake silent (F-8).
		for _, path := range []string{"/", "/favicon.ico", "/anything/at/all"} {
			rec := get(router, path)
			Expect(rec.Header().Get("X-Reached")).To(Equal("page"), "for %s", path)
		}
	})

	Describe("the two mounts that look right and are not", func() {
		It("redirects the upgrade when only the subtree pattern is registered", func() {
			// F-2 as QA-1 built it. ServeMux redirects /app/ui to /app/ui/
			// because only the subtree is registered; measured 307 on Go 1.26,
			// asserted as a class because the code is the standard library's
			// choice and the point is that it is a redirect at all.
			mux := http.NewServeMux()
			mux.Handle(mountPath+"/", http.StripPrefix(mountPath, stubApp()))
			mux.Handle("/", stubPage())

			rec := get(mux, mountPath)
			Expect(rec.Code).To(BeNumerically(">=", http.StatusMultipleChoices))
			Expect(rec.Code).To(BeNumerically("<", http.StatusBadRequest))
			Expect(rec.Header().Get("Location")).To(Equal(mountPath + "/"))
		})

		It("redirects the upgrade when StripPrefix is used on the exact pattern", func() {
			// The repair a reader reaches for next, and it is worse: stripping
			// the mount path from the exact pattern leaves the empty path, and
			// the live handler's own mux — a ServeMux with "/" registered,
			// which is what this stub is — redirects that to "/". The upgrade
			// then lands on the page.
			inner := http.NewServeMux()
			inner.Handle("/", stubApp())

			mux := http.NewServeMux()
			mux.Handle(mountPath, http.StripPrefix(mountPath, inner))
			mux.Handle(mountPath+"/", http.StripPrefix(mountPath, inner))
			mux.Handle("/", stubPage())

			rec := get(mux, mountPath)
			Expect(rec.Code).To(BeNumerically(">=", http.StatusMultipleChoices))
			Expect(rec.Code).To(BeNumerically("<", http.StatusBadRequest))
			Expect(rec.Header().Get("Location")).To(Equal("/"))
		})
	})
})

var _ = Describe("the first paint", func() {
	It("renders the state the loader returns, on every request", func() {
		// Through (*live.App).PageHandler, which is what FirstPaint returns:
		// the assertion is about the library's handler, because that is what a
		// reader of this page is now told to write.
		store := mounting.NewStore(41)
		handler := store.FirstPaint()

		Expect(get(handler, "/").Body.String()).To(ContainSubstring("<output>41</output>"))

		store.Set(7)
		Expect(get(handler, "/").Body.String()).To(ContainSubstring("<output>7</output>"))
	})

	It("agrees with Init, which is the only reason the first patch changes nothing", func() {
		store := mounting.NewStore(41)

		s, _, err := store.Init(context.Background(), live.Session[live.AnonymousIdentity]{})
		Expect(err).NotTo(HaveOccurred())
		Expect(get(store.FirstPaint(), "/").Body.String()).
			To(ContainSubstring("<output>" + strconv.Itoa(s.N) + "</output>"))
	})

	It("answers 500, and no half-written page, when the loader fails", func() {
		// PageHandler renders into a buffer before it writes anything, so a
		// failed load is a 500 rather than a 200 carrying part of a document.
		// The hand-rolled handler this sample used to carry had to remember to
		// do that; this spec is here because the reason it mattered did not
		// stop mattering when the library took the job over.
		store := mounting.NewStore(41)
		rec := httptest.NewRecorder()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		store.FirstPaint().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))

		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		Expect(rec.Body.String()).NotTo(ContainSubstring("<output>"))
	})

	It("is frozen at start-up when the component is built once", func() {
		// The defect, pinned: this is the quickstart's own
		// templ.Handler(Page(State{})) line with an Init that returns
		// something else. It serves the start-up value to every visitor,
		// forever, with no error anywhere.
		store := mounting.NewStore(41)
		start, err := store.Load(context.Background())
		Expect(err).NotTo(HaveOccurred())
		frozen := templ.Handler(mounting.Page(start))

		store.Set(7)
		Expect(get(frozen, "/").Body.String()).To(ContainSubstring("<output>41</output>"))
	})
})
