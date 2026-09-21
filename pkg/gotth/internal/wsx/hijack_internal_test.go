package wsx

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The specs are in package wsx rather than wsx_test because sizedHijacker is
// unexported and the property under test — that the transport receives THIS
// library's buffers with net/http's pipelined bytes still in them — is not
// observable from outside the package without a real client racing a real
// upgrade. They register into the same suite wsx_test.go runs.

// fakeHijacker is a ResponseWriter that hands back a *bufio.ReadWriter the spec
// controls, which is what net/http's own Hijack does and the only part of it
// that matters here.
type fakeHijacker struct {
	http.ResponseWriter
	conn net.Conn
	brw  *bufio.ReadWriter
	err  error
}

func (f *fakeHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.conn, f.brw, nil
}

// ginHijacker is a ResponseWriter shaped like gin's: it has WriteHeaderNow, and
// it records whether anything called it.
type ginHijacker struct {
	*fakeHijacker
	wroteNow int
}

func (g *ginHijacker) WriteHeaderNow() { g.wroteNow++ }

// hijackFixture builds a hijackable ResponseWriter whose reader already holds
// `pipelined` — the bytes a client sent behind its upgrade request and net/http
// buffered while parsing the headers — and whose far end is a live socket. The
// returned far end is the peer; both are closed by the spec's DeferCleanup.
func hijackFixture(pipelined string) (*fakeHijacker, net.Conn) {
	ours, theirs := net.Pipe()
	DeferCleanup(func() {
		_ = ours.Close()
		_ = theirs.Close()
	})

	// A reader with `pipelined` buffered ahead of the socket, which is the
	// state net/http's bufr is in at hijack time.
	br := bufio.NewReaderSize(io.MultiReader(strings.NewReader(pipelined), ours), 4096)
	if pipelined != "" {
		_, _ = br.Peek(len(pipelined))
	}

	return &fakeHijacker{
		ResponseWriter: httptest.NewRecorder(),
		conn:           ours,
		brw:            bufio.NewReadWriter(br, bufio.NewWriterSize(ours, 4096)),
	}, theirs
}

var _ = Describe("the hijack wrapper", func() {
	Describe("the buffers it hands the transport", func() {
		It("replaces net/http's 4 KiB pair with this library's sizes", func() {
			fake, _ := hijackFixture("")

			_, brw, err := rightSized(fake).(http.Hijacker).Hijack()
			Expect(err).NotTo(HaveOccurred())
			Expect(brw.Reader.Size()).To(Equal(readBufferBytes))
			Expect(brw.Writer.Size()).To(Equal(writeBufferBytes))
		})

		It("leaves a ResponseWriter that cannot be hijacked alone", func() {
			plain := httptest.NewRecorder()
			Expect(rightSized(plain)).To(BeIdenticalTo(http.ResponseWriter(plain)))
		})

		It("passes the hijack error through rather than inventing one", func() {
			fake, _ := hijackFixture("")
			fake.err = http.ErrHijacked

			_, _, err := rightSized(fake).(http.Hijacker).Hijack()
			Expect(err).To(MatchError(http.ErrHijacked))
		})
	})

	Describe("the bytes a client pipelined behind its upgrade", func() {
		// This is the property the wrapper exists not to break. The transport
		// immediately does Peek(Buffered()) and resets the reader over what it
		// got, so a replacement reader reporting zero buffered would have the
		// transport reset away the only copy of those bytes.
		It("reports them as buffered, so the transport's Peek finds them", func() {
			fake, _ := hijackFixture("hello-frame")

			_, brw, err := rightSized(fake).(http.Hijacker).Hijack()
			Expect(err).NotTo(HaveOccurred())
			Expect(brw.Reader.Buffered()).To(Equal(len("hello-frame")))

			peeked, err := brw.Reader.Peek(brw.Reader.Buffered())
			Expect(err).NotTo(HaveOccurred())
			Expect(string(peeked)).To(Equal("hello-frame"))
		})

		It("survives the reset the transport performs right after the hijack", func() {
			fake, far := hijackFixture("first")

			netConn, brw, err := rightSized(fake).(http.Hijacker).Hijack()
			Expect(err).NotTo(HaveOccurred())

			// Verbatim from coder/websocket's accept(), because the point of
			// the spec is that this exact sequence loses nothing.
			b, _ := brw.Reader.Peek(brw.Reader.Buffered())
			brw.Reader.Reset(io.MultiReader(bytes.NewReader(b), netConn))

			go func() {
				defer GinkgoRecover()
				_, _ = far.Write([]byte("second"))
				_ = far.Close()
			}()

			all, err := io.ReadAll(brw.Reader)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(all)).To(Equal("firstsecond"))
			Expect(brw.Reader.Size()).To(Equal(readBufferBytes),
				"the reset must not have restored net/http's buffer size")
		})

		It("hands net/http's own buffers back when they do not fit the smaller one", func() {
			fake, _ := hijackFixture(strings.Repeat("x", readBufferBytes+1))

			_, brw, err := rightSized(fake).(http.Hijacker).Hijack()
			Expect(err).NotTo(HaveOccurred())
			Expect(brw.Reader.Size()).To(Equal(4096),
				"correctness is not traded for the memory: the original buffers come back")
			Expect(brw.Reader.Buffered()).To(Equal(readBufferBytes + 1))
		})
	})

	Describe("what it forwards", func() {
		It("forwards gin's WriteHeaderNow, without which a gin mount would hijack an unwritten 101", func() {
			fake, _ := hijackFixture("")
			gin := &ginHijacker{fakeHijacker: fake}

			rightSized(gin).(interface{ WriteHeaderNow() }).WriteHeaderNow()
			Expect(gin.wroteNow).To(Equal(1))
		})

		It("is a no-op on a ResponseWriter that has no WriteHeaderNow", func() {
			fake, _ := hijackFixture("")

			Expect(func() {
				rightSized(fake).(interface{ WriteHeaderNow() }).WriteHeaderNow()
			}).NotTo(Panic())
		})

		It("unwraps to the writer it wrapped, for http.ResponseController", func() {
			fake, _ := hijackFixture("")

			unwrapped := rightSized(fake).(interface {
				Unwrap() http.ResponseWriter
			}).Unwrap()
			Expect(unwrapped).To(BeIdenticalTo(http.ResponseWriter(fake)))
		})
	})
})

// BenchmarkFrameWrite measures what hijack.go's writeBufferBytes constant
// costs, because the constant is a trade and a trade quoted without its price
// is an assertion.
//
// It is a stdlib benchmark and not a Ginkgo spec because it is a measurement,
// not a behaviour: Ginkgo has no *testing.B.
//
// One WebSocket server frame is a ≤10-byte header written into the bufio.Writer
// followed by the payload, then a Flush — coder/websocket's writeFrame,
// reproduced here rather than driven through it so that the only variable is
// the buffer size. The payload never meets an empty buffer, so it never takes
// bufio's direct-write fast path, and the syscall count is 1 when
// header+payload fits the buffer and 2 when it does not, at ANY buffer size.
// The band to compare is therefore writeBufferBytes against 4096 at payloads
// between them, where the smaller buffer pays the extra write and 4096 does not.
func BenchmarkFrameWrite(b *testing.B) {
	header := make([]byte, 10)

	for _, bufSize := range []int{writeBufferBytes, 4096} {
		for _, payload := range []int{64, 512, 2048, 4000, 8192} {
			name := "buf" + strconv.Itoa(bufSize) + "/payload" + strconv.Itoa(payload)
			b.Run(name, func(b *testing.B) {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					b.Fatal(err)
				}
				defer func() { _ = ln.Close() }()

				done := make(chan struct{})
				go func() {
					defer close(done)
					sink, err := ln.Accept()
					if err != nil {
						return
					}
					defer func() { _ = sink.Close() }()
					_, _ = io.Copy(io.Discard, sink)
				}()

				conn, err := net.Dial("tcp", ln.Addr().String())
				if err != nil {
					b.Fatal(err)
				}
				bw := bufio.NewWriterSize(conn, bufSize)
				body := make([]byte, payload)

				b.SetBytes(int64(len(header) + payload))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := bw.Write(header); err != nil {
						b.Fatal(err)
					}
					if _, err := bw.Write(body); err != nil {
						b.Fatal(err)
					}
					if err := bw.Flush(); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				_ = conn.Close()
				<-done
			})
		}
	}
}

// C-36: the memory win was silently conditional on a ResponseWriter shape this
// library does not control.
//
// Since Go 1.20 the documented way to write ResponseWriter middleware is to
// implement `Unwrap() http.ResponseWriter` and let `http.ResponseController`
// find capabilities; such wrappers routinely do NOT implement `Hijack`. Behind
// one, the old direct `w.(http.Hijacker)` test declined, the original writer
// went back, coder/websocket's own walk found net/http's hijacker past us, and
// the session paid 4,096 + 4,096 for its life — 6,656 B/session, with no log,
// no metric and no failing spec. L9-1 measured it. These are the falsifier.

// unwrapOnly is Go 1.20+'s documented middleware shape: it forwards through
// Unwrap and implements no capability of its own.
type unwrapOnly struct{ inner http.ResponseWriter }

func (u unwrapOnly) Header() http.Header         { return u.inner.Header() }
func (u unwrapOnly) Write(b []byte) (int, error) { return u.inner.Write(b) }
func (u unwrapOnly) WriteHeader(statusCode int)  { u.inner.WriteHeader(statusCode) }
func (u unwrapOnly) Unwrap() http.ResponseWriter { return u.inner }

// ownHijacker is middleware that implements Hijack itself, which it is entitled
// to have preferred over anything further down the chain.
type ownHijacker struct {
	http.ResponseWriter
	called bool
	conn   net.Conn
	brw    *bufio.ReadWriter
}

func (o *ownHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	o.called = true
	return o.conn, o.brw, nil
}
func (o *ownHijacker) Unwrap() http.ResponseWriter { return o.ResponseWriter }

var _ = Describe("the hijack wrapper, behind middleware (C-36)", func() {
	It("still sizes the buffers behind an Unwrap-only ResponseWriter", func() {
		fake, _ := hijackFixture("")

		wrapped := rightSized(unwrapOnly{inner: fake})

		hj, ok := wrapped.(http.Hijacker)
		Expect(ok).To(BeTrue(),
			"rightSized declined to wrap an Unwrap-only ResponseWriter, so coder/websocket's own "+
				"walk finds net/http's hijacker past us and the session keeps 4,096+4,096 for its "+
				"life — 6,656 B/session, silently (C-36)")

		_, brw, err := hj.Hijack()
		Expect(err).NotTo(HaveOccurred())
		Expect(brw.Reader.Size()).To(Equal(readBufferBytes))
		Expect(brw.Writer.Size()).To(Equal(writeBufferBytes))
	})

	It("survives more than one layer of Unwrap-only middleware", func() {
		fake, _ := hijackFixture("")

		wrapped := rightSized(unwrapOnly{inner: unwrapOnly{inner: unwrapOnly{inner: fake}}})

		_, brw, err := wrapped.(http.Hijacker).Hijack()
		Expect(err).NotTo(HaveOccurred())
		Expect(brw.Reader.Size()).To(Equal(readBufferBytes))
	})

	It("prefers a middleware's own Hijack over anything further down", func() {
		// The walk tests http.Hijacker before Unwrap at each level, so
		// middleware that took the trouble to implement Hijack still wins. A
		// fix that walked straight to the bottom would break that, and this is
		// the entry that catches it.
		fake, _ := hijackFixture("")
		own := &ownHijacker{ResponseWriter: fake, conn: fake.conn, brw: fake.brw}

		_, brw, err := rightSized(own).(http.Hijacker).Hijack()

		Expect(err).NotTo(HaveOccurred())
		Expect(own.called).To(BeTrue(),
			"the wrapper walked past a middleware that implements Hijack, taking a capability "+
				"away from the layer that declared it")
		Expect(brw.Reader.Size()).To(Equal(readBufferBytes))
	})

	It("still declines a ResponseWriter that is hijackable nowhere in its chain", func() {
		plain := unwrapOnly{inner: httptest.NewRecorder()}
		Expect(rightSized(plain)).To(BeIdenticalTo(http.ResponseWriter(plain)))
	})

	It("forwards gin's WriteHeaderNow through Unwrap-only middleware", func() {
		fake, _ := hijackFixture("")
		gin := &ginHijacker{fakeHijacker: fake}

		rightSized(unwrapOnly{inner: gin}).(interface{ WriteHeaderNow() }).WriteHeaderNow()

		Expect(gin.wroteNow).To(Equal(1),
			"a gin writer behind an Unwrap-only wrapper is still a gin writer, and its 101 still "+
				"has to be flushed before the hijack")
	})
})
