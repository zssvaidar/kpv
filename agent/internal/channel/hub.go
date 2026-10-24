package channel

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/coder/websocket"
)

// Hub is the set of live connections and the one way to reach them.
//
// A Hub is safe for concurrent use. Publish is called from whatever goroutine
// produced the message, Serve from the HTTP handler goroutine of each client,
// and Close from the shutdown path; none of them waits on a client.
type Hub struct {
	options Options
	logger  *slog.Logger

	// mu guards connections and closed. It is held for the length of a map
	// operation and never while writing to a connection, so a slow client
	// cannot keep a publisher out of the map.
	mu          sync.Mutex
	connections map[*connection]struct{}
	closed      bool
}

// New builds a hub. It serves nothing by itself: a connection arrives through
// Serve, which the HTTP boundary calls once the request has passed the token
// and origin checks.
func New(options Options) *Hub {
	options = options.withDefaults()
	return &Hub{
		options:     options,
		logger:      options.Logger,
		connections: make(map[*connection]struct{}),
	}
}

// Serve upgrades r to a WebSocket and runs that connection until the client
// goes away, the connection is closed for not keeping up or for not answering
// a ping, or the hub shuts down. It blocks for the lifetime of the connection,
// on the caller's goroutine, which is the HTTP handler's: the only goroutine
// this package starts per connection is the one that writes.
//
// Everything about authorization has happened before this is called. Serve
// does not look at the token, the Origin header or anything else about the
// request beyond what the handshake itself needs, because a check here would
// be a second, weaker copy of the one in #4.
//
// On a refused handshake the library has already written a response, which is
// why the caller passes a ResponseWriter that turns anything other than the
// 101 into the boundary's one uniform rejection. The error is returned for the
// agent's log.
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request) error {
	// Checked before the upgrade, so a client cannot be handed a connection
	// that is about to be thrown away by a hub that is already closing.
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if closed {
		// Nothing is written here. This package does not answer HTTP: the
		// caller turns a handshake that did not become a 101 into the
		// boundary's one uniform rejection, whether the library wrote
		// something or nothing was written at all.
		return &ChannelError{Op: "serve", Kind: KindClosed, Message: "the channel is closed, so the handshake was refused"}
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Defence in depth only; see Options.AllowedOrigins. The boundary has
		// already decided this request's origin is allowed, and it is the
		// decision that counts.
		OriginPatterns: h.options.AllowedOrigins,
		// No compression. There is nothing to gain on a loopback socket, and
		// a compressor shared across messages is a side channel between them.
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return &ChannelError{Op: "serve", Kind: KindUpgrade, Message: "the WebSocket handshake was not completed", Err: err}
	}
	ws.SetReadLimit(h.options.ReadLimit)

	connection := newConnection(h, ws)
	if !h.add(connection) {
		// The hub closed between the check above and here. Nothing has been
		// sent on this connection and nothing will be.
		connection.hangUp("the channel closed while the connection was being set up")
		return &ChannelError{Op: "serve", Kind: KindClosed, Message: "the channel closed while the handshake was in flight"}
	}
	defer h.remove(connection)

	h.logger.Debug("a channel client connected", "connections", h.Connections())
	connection.run()
	h.logger.Debug("a channel client disconnected", "reason", connection.reason())
	return nil
}

// Publish delivers message to every connection subscribed to its pipeline,
// and to no other connection. A client that subscribed to nothing receives
// nothing, so there is no broadcast to be forgotten about.
//
// Publish does not block: a connection that cannot take the message is
// disconnected, not waited for. It is safe to call from any goroutine, and
// from several at once.
//
// The message is serialized once here rather than once per subscriber, which
// is also where a message that cannot be serialized is dealt with: it is
// logged and dropped. It reaches no connection, so it can break neither one
// connection nor the agent. A payload comes from the agent's own code, so this
// is a programming mistake rather than a client's doing, and the log is how it
// gets found.
//
// A pipeline id that is not a legal identifier is not rejected here. It cannot
// do any harm: a subscription only ever comes from a command the generated
// envelope has already validated, so an id no pipeline could have matches no
// subscription and the message is simply delivered to nobody.
func (h *Hub) Publish(message ChannelMessage) {
	frame, err := json.Marshal(message)
	if err != nil {
		h.logger.Error("a channel message could not be serialized and was dropped",
			"kind", string(message.Kind), "pipelineId", string(message.PipelineID), "error", err)
		return
	}
	for _, connection := range h.subscribers(message.PipelineID) {
		connection.send(frame)
	}
}

// Connections is how many clients are connected. It is what the agent reports
// and what a test waits on, so it is a count and not a list: a caller has no
// business reaching a connection.
func (h *Hub) Connections() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.connections)
}

// Close disconnects every client and refuses any further handshake. It returns
// once every connection's write loop has stopped, so a caller that is shutting
// the process down knows there is nothing left running.
//
// It is idempotent, and it does not wait for a client: a connection is closed
// by closing its socket, which is what unblocks whatever its loops are doing.
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	live := make([]*connection, 0, len(h.connections))
	for connection := range h.connections {
		live = append(live, connection)
	}
	h.mu.Unlock()

	// Outside the lock, because a connection's own goroutine takes it on the
	// way out and this waits for that goroutine.
	for _, connection := range live {
		connection.hangUp("the agent is shutting down")
	}
	for _, connection := range live {
		<-connection.finished
	}
}

// add registers a connection, reporting false when the hub has closed in the
// meantime.
func (h *Hub) add(c *connection) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.connections[c] = struct{}{}
	return true
}

func (h *Hub) remove(c *connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.connections, c)
}

// subscribers snapshots the connections subscribed to a pipeline. It is a
// snapshot because delivery happens with no lock held: a connection that is
// disconnected while a message is on its way to it simply drops it.
func (h *Hub) subscribers(pipeline PipelineID) []*connection {
	h.mu.Lock()
	defer h.mu.Unlock()
	var subscribed []*connection
	for connection := range h.connections {
		if connection.subscribed(pipeline) {
			subscribed = append(subscribed, connection)
		}
	}
	return subscribed
}
