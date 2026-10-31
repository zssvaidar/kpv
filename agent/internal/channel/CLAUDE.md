# internal/channel

One WebSocket per client (`github.com/coder/websocket`), the envelope
generated from `schema/channel.schema.json` on it, and a subscription per
pipeline. The package comment in `channel.go` gives the reasons for each rule
below. What goes in the `log` and `status` payloads is #34 and #35.

- no authorization here: the token and origin are checked by `httpapi` before
  the upgrade. This package is never given a credential, cannot import
  `httpapi`, and may not name one (`source_test.go`, `agent/CLAUDE.md`)
- every frame, message or ping, is written by the connection's write loop and
  nothing else; `TestTheWriteLoopIsTheOnlyThingThatWritesToASocket`
  (`serialization_test.go`) reads the syntax tree and fails on a write anywhere
  else. A behavioural test would not catch a second writer
- `Publish` (`hub.go`) never blocks: a full client queue disconnects that
  client. Every write is bounded by `WriteTimeout`, and a dead peer is closed
  within `DefaultPingInterval + DefaultPongTimeout` (25s). The intervals are
  `Options`, so tests inject milliseconds (`limits_test.go`)
- closing closes the socket; no close frame is written, since it would be a
  second writer
- a message that will not serialize is logged and dropped once, in `Publish`.
  A client that subscribed to nothing receives nothing
