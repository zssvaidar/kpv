// Package channel is the agent's push channel: one WebSocket per client, a
// typed envelope on it, and a subscription per pipeline.
//
// The envelope in models_gen.go is generated from schema/channel.schema.json,
// which is the single source of truth for it; so is the copy of that schema in
// this directory, which exists only because a Go module cannot embed a file
// from outside itself. Run `pnpm run generate` after changing the schema; CI
// fails when the generated files or the copy are stale.
//
// What this package does not do, deliberately:
//
//   - It does not authenticate anything. The session token and the origin
//     allowlist are the HTTP boundary's job (#4, #58), they are checked before
//     the upgrade is accepted, and nothing here is reachable until they pass.
//     That is also why no credential can reach a payload: this package is
//     never given one. It cannot see the token, so it cannot leak it.
//   - It does not decide what a log line or a status is. That is #34 and #35;
//     here a message is an envelope with a kind, a pipeline id and a payload.
//
// The shape of a connection, and why:
//
//   - every frame is written by one goroutine, the connection's write loop,
//     and by nothing else. That is what makes concurrent sends impossible to
//     interleave: a publisher hands over bytes and never touches the socket.
//     writeLoopIsTheOnlyWriter in serialization_test.go holds the package to
//     it, because a correct behavioural test would still pass if a second
//     writer were added -- the library serializes frames internally too, so
//     the damage would be invisible until the day it was not.
//   - a publisher never blocks. Handing a message to a connection is a
//     non-blocking send on a buffered channel, and a client whose buffer is
//     full is disconnected rather than waited for.
//   - every write is bounded by WriteTimeout, so a client that has stopped
//     reading cannot hold the write loop either. Cancelling a write closes the
//     connection, which is the intended outcome and not a side effect.
//   - a dead peer is found by a ping that goes unanswered, within
//     PingInterval+PongTimeout of dying, and the connection is closed.
//   - closing is abrupt: the socket is closed, no close frame is written. A
//     close frame would be a second thing writing to the connection, and what
//     it would carry -- a status and a reason -- is nothing the agent owes a
//     client it is hanging up on. The cost is that a client sees an abnormal
//     closure (1006) rather than a code, which is what a disconnected client
//     has to handle anyway.
package channel

import (
	"bytes"
	_ "embed"
	"log/slog"
	"time"
)

//go:embed channel.schema.json
var schemaJSON []byte

// SchemaJSON returns the authoritative JSON Schema for the envelope, verbatim.
func SchemaJSON() []byte {
	return bytes.Clone(schemaJSON)
}

// The channel's timings and bounds. Every one of them is a named constant
// rather than a number in the middle of a loop, because each is a promise to
// a client and the promises are what a reader of this package needs.
//
// The heartbeat, stated as a guarantee: the write loop sends a ping every
// DefaultPingInterval and gives the peer DefaultPongTimeout to answer it, so a
// peer that has died is detected within DefaultPingInterval+DefaultPongTimeout
// -- 25 seconds with these values -- and its connection is closed. A dead peer
// is not just an unresponsive one: a laptop that slept, a browser tab the
// operating system froze and a cable pulled out all look exactly like this,
// and none of them closes the TCP connection on the way out.
//
// The values are what the Options leave out, so a caller configures only what
// it means to change, and a test can make every one of them small.
const (
	// DefaultPingInterval is how often the agent pings an idle or busy peer.
	DefaultPingInterval = 15 * time.Second
	// DefaultPongTimeout is how long a pong may take before the peer counts
	// as dead. It is shorter than the interval so that one unanswered ping is
	// settled before the next is due.
	DefaultPongTimeout = 10 * time.Second
	// DefaultWriteTimeout bounds one write. A client that stops reading stops
	// its own TCP window from moving, so a write to it eventually blocks; this
	// is how long the agent waits before deciding that client is gone.
	DefaultWriteTimeout = 10 * time.Second
	// DefaultOutboundBuffer is how many messages may be queued for one
	// connection before it is disconnected for not keeping up. It is a
	// burst allowance, not a backlog: the agent is a local process talking to
	// a page on the same machine, so a client that is tens of messages behind
	// is not slow, it is gone.
	DefaultOutboundBuffer = 64
	// DefaultReadLimit caps one inbound message. The only thing a client may
	// send is a subscription command, which is a few dozen bytes, so this is
	// generous by three orders of magnitude and still refuses to buffer
	// anything a hostile client would send.
	DefaultReadLimit = 4 << 10
	// maxSubscriptions caps how many pipelines one connection may subscribe
	// to, so a client cannot make the agent remember an unbounded number of
	// ids. There is one pipeline per project and a handful per machine.
	maxSubscriptions = 64
)

// Options configures a Hub. Every field is optional: the zero value gives the
// defaults above, which is what the agent runs with.
type Options struct {
	// Logger receives channel events. A nil Logger discards everything.
	//
	// It never receives a payload or anything a client sent: a kind, a
	// pipeline id and a reason are enough to read an incident out of a log,
	// and a payload is the one part of a message that could carry something
	// an operator's own log should not hold.
	Logger *slog.Logger
	// AllowedOrigins are the origins the handshake's own origin check accepts,
	// on top of the request's host. It is defence in depth and nothing more:
	// the HTTP boundary has already checked the Origin header against its
	// allowlist before this package is reached, and it is stricter, because it
	// compares the whole origin and refuses a request that carries no Origin
	// at all.
	AllowedOrigins []string
	// PingInterval, PongTimeout and WriteTimeout override the constants above.
	// A test sets them small so that it does not have to sit out a real
	// interval to watch a real timeout happen.
	PingInterval time.Duration
	PongTimeout  time.Duration
	WriteTimeout time.Duration
	// OutboundBuffer is how many messages may be queued for one connection.
	OutboundBuffer int
	// ReadLimit caps one inbound message, in bytes.
	ReadLimit int64
}

// withDefaults returns options with every zero field filled in from the
// constants above, so the rest of the package reads a value and never a
// "0 means something else" rule.
func (o Options) withDefaults() Options {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.PingInterval <= 0 {
		o.PingInterval = DefaultPingInterval
	}
	if o.PongTimeout <= 0 {
		o.PongTimeout = DefaultPongTimeout
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = DefaultWriteTimeout
	}
	if o.OutboundBuffer <= 0 {
		o.OutboundBuffer = DefaultOutboundBuffer
	}
	if o.ReadLimit <= 0 {
		o.ReadLimit = DefaultReadLimit
	}
	return o
}
