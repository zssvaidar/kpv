package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// connection is one client: the socket, what it subscribed to, and the queue
// of frames waiting to go out on it.
//
// It is unexported and nothing hands one out. A publisher reaches a connection
// only through the hub, which is what keeps "one goroutine writes" a property
// of the package rather than a convention callers have to keep.
type connection struct {
	hub *Hub
	ws  *websocket.Conn

	// outbound holds frames the write loop has not sent yet. It is buffered,
	// and a send that would block disconnects the client instead of waiting,
	// which is the whole of "a slow client cannot block the agent".
	outbound chan []byte

	// ctx is cancelled when this connection is hung up, which is what stops
	// both loops: a read or a write on a cancelled context closes the socket.
	//
	// It is deliberately not derived from the request's context. The
	// connection outlives the HTTP request it arrived on -- the socket is
	// hijacked -- and net/http cancels a request's context when its handler
	// returns, so hanging this connection's lifetime off it would be a race
	// between two unrelated things.
	ctx    context.Context
	cancel context.CancelFunc

	// writerDone is closed when the write loop has stopped, and finished when
	// the whole connection has. Shutdown waits on the second, so that a
	// stopped agent has no connection goroutine left anywhere.
	writerDone chan struct{}
	finished   chan struct{}

	// mu guards everything below. None of it is held while writing to the
	// socket.
	mu            sync.Mutex
	subscriptions map[PipelineID]struct{}
	hungUp        bool
	why           string
}

func newConnection(hub *Hub, ws *websocket.Conn) *connection {
	ctx, cancel := context.WithCancel(context.Background())
	return &connection{
		hub:           hub,
		ws:            ws,
		outbound:      make(chan []byte, hub.options.OutboundBuffer),
		ctx:           ctx,
		cancel:        cancel,
		writerDone:    make(chan struct{}),
		finished:      make(chan struct{}),
		subscriptions: make(map[PipelineID]struct{}),
	}
}

// run drives the connection until the client is gone, on the caller's
// goroutine. Exactly one goroutine is started: the write loop.
func (c *connection) run() {
	// Order matters, and defers run last-registered-first: the connection
	// takes itself out of the hub before it says it has finished, so a
	// shutdown that waited for it also knows it is gone, rather than finding
	// it in the map for another instant.
	defer close(c.finished)
	defer c.hub.remove(c)
	// Belt and braces: every path out of the read loop hangs up, which
	// cancels this already. A context that is never cancelled is a leak of a
	// different kind.
	defer c.cancel()

	go c.writeLoop()
	c.readLoop()
	// The read loop has hung up by the time it returns, so the write loop is
	// already on its way out. Waiting for it here is what makes "the client
	// disconnected" and "nothing of this connection is still running" the
	// same moment, which is what the leak test relies on.
	<-c.writerDone
}

// writeLoop is the only thing in this package that writes to a socket, and
// that is the point. Every frame -- a message, an acknowledgement, a ping --
// is written here, on one goroutine, so concurrent sends queue instead of
// racing and two messages can never be interleaved on the wire.
//
// Removing that property is the mistake this is written to make impossible:
// writing from a publisher's own goroutine would look fine in a test, because
// the library takes a lock of its own per frame, right up until a frame is
// written in pieces. writeLoopIsTheOnlyWriter in serialization_test.go reads
// this package's syntax tree and fails if a write appears anywhere else.
func (c *connection) writeLoop() {
	defer close(c.writerDone)

	heartbeat := time.NewTicker(c.hub.options.PingInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return

		case frame := <-c.outbound:
			// Bounded, always. A client that has stopped reading stops its
			// own TCP window from moving, and without this the write loop
			// would wait on it for as long as the client liked.
			writeCtx, cancelWrite := context.WithTimeout(c.ctx, c.hub.options.WriteTimeout)
			err := c.ws.Write(writeCtx, websocket.MessageText, frame)
			cancelWrite()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					c.hangUp("the client did not read a message within the write timeout")
				} else {
					c.hangUp("a message could not be written to the client")
				}
				return
			}

		case <-heartbeat.C:
			// The heartbeat. A peer that is gone without having closed the
			// connection -- a slept laptop, a frozen tab, an unplugged cable
			// -- answers no ping, and this is where that is noticed, within
			// PingInterval+PongTimeout of it happening.
			pingCtx, cancelPing := context.WithTimeout(c.ctx, c.hub.options.PongTimeout)
			err := c.ws.Ping(pingCtx)
			cancelPing()
			if err != nil {
				c.hangUp("the client did not answer a ping within the pong timeout")
				return
			}
		}
	}
}

// readLoop reads the one thing a client may send, a subscription command, and
// returns when the client is gone or has said something the channel cannot
// read. Every path out of it hangs the connection up, so its return is the
// signal that the connection is over.
func (c *connection) readLoop() {
	for {
		kind, data, err := c.ws.Read(c.ctx)
		if err != nil {
			// The ordinary end of a connection: the client closed it, or it
			// was hung up on and this read was woken by that. The reason
			// recorded first is the one that is kept, so a client that was
			// disconnected for not keeping up does not have its reason
			// overwritten by the read that noticed.
			c.hangUp("the client went away")
			return
		}
		if kind != websocket.MessageText {
			c.hangUp("the client sent a frame that is not a command")
			return
		}
		command, err := readCommand(data)
		if err != nil {
			c.hangUp("the client sent something that is not a command")
			return
		}
		if !c.apply(command) {
			return
		}
	}
}

// strictCommand is a plain copy of the generated command type, which is to
// say: the same fields, without the generated UnmarshalJSON on the struct
// itself. encoding/json ignores DisallowUnknownFields for a type that defines
// its own unmarshaller, so this is what a property the schema does not have is
// refused through. The field types keep their own generated unmarshallers, so
// the command name is still held to the two names and the pipeline id to the
// identifier pattern.
type strictCommand ChannelCommand

// readCommand decodes one command, strictly.
//
// The bytes are read twice because the two passes refuse different things and
// neither can refuse both:
//
//   - into strictCommand, with unknown properties and anything trailing the
//     JSON value refused. A client that sends a property it believes means
//     something should be told it does not, rather than quietly getting a
//     plain subscription.
//   - into the generated ChannelCommand, which is where a required property
//     that is missing is refused. The generated unmarshaller is the schema's
//     own rules, which is why the validation is not written out here.
func readCommand(data []byte) (ChannelCommand, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var strict strictCommand
	if err := decoder.Decode(&strict); err != nil {
		return ChannelCommand{}, err
	}
	if decoder.More() {
		return ChannelCommand{}, errors.New("a command is one JSON object and nothing after it")
	}
	var command ChannelCommand
	if err := json.Unmarshal(data, &command); err != nil {
		return ChannelCommand{}, err
	}
	return command, nil
}

// apply carries out one command, reporting whether the connection survives it.
func (c *connection) apply(command ChannelCommand) bool {
	switch command.Command {
	case ChannelCommandNameSubscribe:
		if !c.subscribe(command.PipelineID) {
			c.hangUp("the client subscribed to more pipelines than one connection may have")
			return false
		}
		c.acknowledge(ChannelMessageKindSubscribed, command.PipelineID)
	case ChannelCommandNameUnsubscribe:
		c.unsubscribe(command.PipelineID)
		c.acknowledge(ChannelMessageKindUnsubscribed, command.PipelineID)
	default:
		// Unreachable: the generated unmarshaller accepts no other name. It
		// is handled rather than ignored because "unreachable" is a claim
		// about generated code, and the generated code can be regenerated.
		c.hangUp("the client sent a command the channel does not have")
		return false
	}
	return true
}

// acknowledge tells the client that its own command is in force, so that it
// knows when a subscription started rather than having to guess.
func (c *connection) acknowledge(kind ChannelMessageKind, pipeline PipelineID) {
	frame, err := json.Marshal(ChannelMessage{Kind: kind, PipelineID: pipeline, Payload: ChannelPayload{}})
	if err != nil {
		// Unreachable: an acknowledgement's payload is empty. Dropped with a
		// log rather than dignified with a response, the same as a message
		// that will not serialize in Publish.
		c.hub.logger.Error("a channel acknowledgement could not be serialized and was dropped",
			"kind", string(kind), "pipelineId", string(pipeline), "error", err)
		return
	}
	c.send(frame)
}

// send queues a frame for this connection, and never blocks.
//
// A full queue means the client is not reading what it asked for. It is
// disconnected, because the alternatives are worse: waiting would let one
// client hold up the agent, and dropping the message quietly would leave a
// client that believes it has seen everything.
func (c *connection) send(frame []byte) {
	select {
	case c.outbound <- frame:
	default:
		c.hub.logger.Warn("a channel client was disconnected for not keeping up",
			"queued", cap(c.outbound))
		c.hangUp("the client did not keep up with the messages it subscribed to")
	}
}

// hangUp closes the connection, recording why the first time it is called. It
// is idempotent and safe from any goroutine: the loops call it, the hub calls
// it on shutdown, and a publisher calls it through send.
//
// The socket is closed rather than closed down: no close frame is written. A
// close frame would mean something other than the write loop writing to the
// connection, and the status and reason it would carry are for the agent's log
// rather than for a client being hung up on.
func (c *connection) hangUp(why string) {
	c.mu.Lock()
	first := !c.hungUp
	c.hungUp = true
	if first {
		c.why = why
	}
	c.mu.Unlock()
	if !first {
		return
	}
	// Cancelling comes first: it is what wakes a loop that is inside a read or
	// a write, and closing the socket is what wakes one that is not.
	c.cancel()
	_ = c.ws.CloseNow()
}

// reason is why this connection ended, for the log.
func (c *connection) reason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.why
}

// subscribe adds a subscription, reporting false when the connection already
// holds as many as it may.
func (c *connection) subscribe(pipeline PipelineID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, already := c.subscriptions[pipeline]; already {
		return true
	}
	if len(c.subscriptions) >= maxSubscriptions {
		return false
	}
	c.subscriptions[pipeline] = struct{}{}
	return true
}

func (c *connection) unsubscribe(pipeline PipelineID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.subscriptions, pipeline)
}

// subscribed reports whether this connection asked for a pipeline's messages.
func (c *connection) subscribed(pipeline PipelineID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.subscriptions[pipeline]
	return ok
}
