package httpapi

import (
	"bufio"
	"errors"
	"net"
	"net/http"

	"github.com/zssvaidar/kpv/agent/internal/channel"
)

// channelPattern is the WebSocket endpoint: the push channel #5 adds, for logs
// and status.
//
// It is registered by newHandler rather than listed in apiRoutes, and the
// difference is one of kind rather than of care. Every row of apiRoutes is a
// request-and-an-answer route, and the table's tests hold each row to exactly
// that: an authorized GET to it answers 200 with a JSON object. A handshake is
// not that shape, and making it pretend to be would mean a GET to this path
// answering something it should not.
//
// What matters is that it is registered in the same function, on the same mux,
// inside the same wrap, so it is behind the token and origin checks for
// exactly the reason the rows are. Nothing about it is checked later or
// separately. channel_test.go walks what newHandler registers rather than what
// apiRoutes returns, so this route is covered by the same rejection matrix the
// table's routes are, and a future route registered the same way is too.
const channelPattern = "GET /api/channel"

// channelRoute is the handler for the channel endpoint.
//
// By the time it runs, the request has already presented the right token and
// an allowed origin: the guard wraps the mux, so both checks happen before the
// handshake is even looked at, let alone accepted. That ordering is the first
// acceptance criterion of #5 and it is not implemented here -- it is inherited,
// which is the point of there being one boundary.
//
// A browser's WebSocket constructor cannot set a header, so the token arrives
// in the query parameter; the guard already accepts it there, which is how the
// startup URL hands it to the page in the first place. Nothing extra is needed
// here, and nothing extra is done.
//
// One thing that is not done here and would be a bug if it were needed: the
// server's read and write timeouts are not cleared for this connection. A
// WebSocket outlives both of them by design, and a deadline left on the socket
// would kill it at the write timeout. net/http clears the deadlines itself
// when a connection is hijacked (hijackLocked calls SetDeadline with the zero
// time), so there is nothing to undo; a channel that died after exactly
// writeTimeout would be this assumption being wrong.
func (g *guard) channelRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.channel == nil {
			// A server built with no channel serves no channel, and says so
			// the way it says everything else. Only a test builds one.
			reject(w)
			return
		}
		handshake := &handshakeWriter{ResponseWriter: w}
		if err := g.channel.Serve(handshake, r); err != nil {
			handshake.rejectUnlessAnswered()
			// The kind, and nothing else. The library's own message quotes
			// what the client sent -- the Origin header, for one -- and a
			// client's bytes do not go in the agent's log.
			g.logger.Debug("a channel handshake was refused", "kind", string(channel.KindOf(err)))
		}
	}
}

// handshakeWriter is the ResponseWriter a handshake is given, and it exists so
// that a refused upgrade says nothing a rejected GET does not.
//
// The library answers a handshake it will not accept itself, and it answers it
// helpfully: 426 with the Connection header it wanted, 400 with the
// Sec-WebSocket-Version it supports, 403 naming the Origin it rejected. Every
// one of those is a sentence about this endpoint that a caller cannot get out
// of any other path on this server, where a missing token, a wrong token, a
// disallowed origin and a route that was never there all answer the same 404
// with the same body and the same headers.
//
// So: a 101 passes through untouched, and anything else -- whatever status,
// whatever headers the library had set, whatever explanation it was about to
// write -- becomes that same 404. Nothing a refused upgrade answers is new.
type handshakeWriter struct {
	http.ResponseWriter
	// answered records that a status has been decided, so a second
	// WriteHeader cannot write a second one, and so that a handler which
	// wrote nothing at all can be told apart from one that did.
	answered bool
	// upgraded records that the status was the 101, which is the one case
	// where the body belongs to the client rather than to the rejection.
	upgraded bool
}

// WriteHeader passes a 101 through and turns anything else into the uniform
// rejection.
func (h *handshakeWriter) WriteHeader(status int) {
	if h.answered {
		// net/http would log a "superfluous WriteHeader" and ignore it; this
		// ignores it quietly, because the first answer is the one that was
		// written and there is nothing to add to it.
		return
	}
	h.answered = true
	if status == http.StatusSwitchingProtocols {
		h.upgraded = true
		h.ResponseWriter.WriteHeader(status)
		return
	}
	// Everything the library had set on the way to refusing goes, so that the
	// headers are the rejection's own and not a hint about what was tried.
	clear(h.Header())
	reject(h.ResponseWriter)
}

// Write drops whatever a refused handshake was going to explain, and passes
// through anything written after an accepted one.
func (h *handshakeWriter) Write(b []byte) (int, error) {
	if !h.answered {
		// A body with no status is a 200 in net/http, and no rejection may be
		// a 200. Deciding the status first sends it through the branch above,
		// which turns it into the rejection.
		h.WriteHeader(http.StatusOK)
	}
	if !h.upgraded {
		// The rejection has already been written in full. The library's
		// explanation is reported as written so that it does not then treat
		// this as an I/O failure, and goes nowhere.
		return len(b), nil
	}
	return h.ResponseWriter.Write(b)
}

// rejectUnlessAnswered writes the uniform rejection when a refused handshake
// answered nothing at all.
//
// It is the case that would otherwise be a 200: a handler that returns having
// written nothing gets net/http's empty 200, which is a distinguishable answer
// and a successful-looking one. Refusing before the library is reached -- a
// channel that is already shut down -- is exactly that shape.
func (h *handshakeWriter) rejectUnlessAnswered() {
	if h.answered {
		return
	}
	h.WriteHeader(http.StatusInternalServerError)
}

// Hijack hands over the connection, which is how a WebSocket stops being an
// HTTP response. Without it the wrapper would hide the server's own Hijacker
// and no handshake could be completed at all.
func (h *handshakeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := h.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("the agent's HTTP server does not support hijacking, so no channel can be served")
	}
	return hijacker.Hijack()
}

// Unwrap exposes the wrapped writer, which is how net/http's own
// ResponseController and anything following its convention reaches the
// capabilities this wrapper does not name itself.
func (h *handshakeWriter) Unwrap() http.ResponseWriter {
	return h.ResponseWriter
}
