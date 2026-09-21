package httpserver

import (
	"io"

	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
)

// eventStreamHeaders is the response header set every server-sent-event
// endpoint needs, written once here rather than restated at each streaming
// handler. Connection: keep-alive is stated for HTTP/1.1 intermediaries;
// X-Accel-Buffering: no stops nginx-family proxies from buffering the stream
// into uselessness.
var eventStreamHeaders = map[string]string{
	"Content-Type":      sse.ContentType,
	"Cache-Control":     "no-cache",
	"Connection":        "keep-alive",
	"X-Accel-Buffering": "no",
}

// EventStreamHeaders is the middleware form: mount it on a route group and
// every response in that group is a server-sent-event stream. Handlers that
// stream from inside a generated wrapper use EventStream instead.
func EventStreamHeaders() gin.HandlerFunc {
	return func(context *gin.Context) {
		setEventStreamHeaders(context)
		context.Next()
	}
}

// EventStream writes the server-sent-event headers and then runs step until
// it returns false or the client hangs up. gin's Stream owns the
// flush-per-step and client-gone loop; step writes frames, for which
// EncodeEvent is the encoder.
func EventStream(context *gin.Context, step func(writer io.Writer) bool) {
	setEventStreamHeaders(context)
	context.Stream(step)
}

// EncodeEvent writes one server-sent-event frame. It is gin-contrib/sse's
// encoder, named here so a streaming handler depends on this package alone.
// Data is a type parameter rather than any so the caller's frame type is
// carried to this boundary and erased exactly once, where the third-party
// encoder's own interface{} field demands it (CS-7).
func EncodeEvent[Data any](writer io.Writer, identifier string, data Data) error {
	return sse.Encode(writer, sse.Event{Id: identifier, Data: data})
}

func setEventStreamHeaders(context *gin.Context) {
	for name, value := range eventStreamHeaders {
		context.Header(name, value)
	}
}
