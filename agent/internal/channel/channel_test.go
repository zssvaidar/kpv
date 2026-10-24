// Shared scaffolding for the channel tests, and the subscription behaviour:
// what a client receives, and what it never receives.
//
// Nothing here waits on a clock to decide a result. Where something has to
// happen before an assertion can be made, the test waits for the thing itself:
// an acknowledgement read off the socket, or a condition polled until it holds
// or the test gives up. The only durations are a generous ceiling, so that a
// loaded runner is slow rather than wrong, and the deliberately tiny intervals
// a test injects to make a timeout happen now instead of in ten seconds.

package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// patience is the ceiling on anything a test waits for. It is long because it
// is not a measurement: a test that needs more than a few milliseconds of it
// has found a real failure, and a runner under load needs the room.
const patience = 15 * time.Second

// brief is the length of an interval a test makes small on purpose, so that
// the timeout it is about happens immediately rather than after the real one.
const brief = 20 * time.Millisecond

// safeBuffer is a log sink several goroutines write to, because a connection
// logs from its own.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// testChannel is a hub serving on a real loopback socket, with its log
// captured.
//
// There is no authorization here on purpose: the token and the origin are the
// HTTP boundary's, they are tested there, and a copy of them in this package's
// scaffolding would be a copy of a check this package deliberately does not
// have.
type testChannel struct {
	*Hub
	url  string
	logs *safeBuffer
}

func startChannel(t *testing.T, options Options) *testChannel {
	t.Helper()
	logs := &safeBuffer{}
	options.Logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hub := New(options)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The error is the agent's to log, not the test's to fail on: a
		// client that was hung up on is a passing test in most of this file.
		_ = hub.Serve(w, r)
	}))
	t.Cleanup(func() {
		hub.Close()
		server.Close()
	})
	return &testChannel{Hub: hub, url: "ws" + strings.TrimPrefix(server.URL, "http"), logs: logs}
}

// dial connects a client. The returned connection is closed when the test
// ends, however the test ends.
func (tc *testChannel) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), patience)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, tc.url, nil)
	if err != nil {
		t.Fatalf("dial the channel: %v", err)
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	return ws
}

// command sends one subscription command, as a client's own code would.
func command(t *testing.T, ws *websocket.Conn, name ChannelCommandName, pipeline PipelineID) {
	t.Helper()
	raw, err := json.Marshal(ChannelCommand{Command: name, PipelineID: pipeline})
	if err != nil {
		t.Fatalf("marshal a %s command: %v", name, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), patience)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatalf("send a %s command: %v", name, err)
	}
}

// receive reads the next message, with the envelope decoded and the raw frame
// kept: a test that asserts about bytes needs the bytes.
func receive(t *testing.T, ws *websocket.Conn) (ChannelMessage, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), patience)
	defer cancel()
	kind, raw, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("read from the channel: %v", err)
	}
	if kind != websocket.MessageText {
		t.Fatalf("the channel sent a %v frame, want text", kind)
	}
	var message ChannelMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatalf("the channel sent %q, which is not an envelope: %v", raw, err)
	}
	return message, raw
}

// subscribe sends a subscription and waits for the acknowledgement, so that
// everything after it in the test happens with the subscription in force.
// That is why the acknowledgement exists: without it a test would have to
// publish and hope.
func subscribe(t *testing.T, ws *websocket.Conn, pipeline PipelineID) {
	t.Helper()
	command(t, ws, ChannelCommandNameSubscribe, pipeline)
	message, _ := receive(t, ws)
	if message.Kind != ChannelMessageKindSubscribed || message.PipelineID != pipeline {
		t.Fatalf("subscribing to %q was acknowledged as %v %q", pipeline, message.Kind, message.PipelineID)
	}
}

// logMessage is a message of the shape #34 will publish: a kind, a pipeline
// and a payload with something recognizable in it.
func logMessage(pipeline PipelineID, line string) ChannelMessage {
	return ChannelMessage{
		Kind:       ChannelMessageKindLog,
		PipelineID: pipeline,
		Payload:    ChannelPayload{"line": line},
	}
}

// lineOf pulls the recognizable part back out of a payload.
func lineOf(t *testing.T, message ChannelMessage) string {
	t.Helper()
	line, ok := message.Payload["line"].(string)
	if !ok {
		t.Fatalf("the payload %v carries no line", message.Payload)
	}
	return line
}

// waitFor polls a condition until it holds or patience runs out.
//
// The poll interval is not synchronisation: the assertion is the condition,
// and the loop exits the moment it holds. What it replaces is a sleep long
// enough to "probably" be enough, which is the thing that fails on a loaded
// runner.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(patience)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %s", what, patience)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitForLog waits for the log to say something, and fails with the whole log
// when it does not.
//
// Why waiting is necessary rather than fussy: a connection takes itself out of
// the hub as it stops, and the line explaining why it stopped is written just
// after that, by the handler goroutine on its way out. So a test that has
// watched the connection disappear has not necessarily seen the log yet.
func (tc *testChannel) waitForLog(t *testing.T, phrase, what string) {
	t.Helper()
	deadline := time.Now().Add(patience)
	for !strings.Contains(tc.logs.String(), phrase) {
		if time.Now().After(deadline) {
			t.Fatalf("the log never said it was %s; it says:\n%s", what, tc.logs.String())
		}
		time.Sleep(time.Millisecond)
	}
}

// A client receives the pipelines it asked for, and only those. The messages
// are published interleaved so that cross-talk is a failure about order and
// content rather than about a timeout: if A ever saw B's pipeline, A's second
// read would be B's message, here and now.
func TestEachClientReceivesOnlyThePipelinesItSubscribedTo(t *testing.T) {
	t.Parallel()
	hub := startChannel(t, Options{})

	a := hub.dial(t)
	b := hub.dial(t)
	subscribe(t, a, "alpha")
	subscribe(t, b, "beta")

	hub.Publish(logMessage("alpha", "alpha-one"))
	hub.Publish(logMessage("beta", "beta-one"))
	hub.Publish(logMessage("alpha", "alpha-two"))
	hub.Publish(logMessage("gamma", "nobody-asked-for-this"))
	hub.Publish(logMessage("beta", "beta-two"))

	for _, line := range []string{"alpha-one", "alpha-two"} {
		message, _ := receive(t, a)
		if message.PipelineID != "alpha" {
			t.Fatalf("the client subscribed to alpha was sent a message about %q", message.PipelineID)
		}
		if got := lineOf(t, message); got != line {
			t.Fatalf("the client subscribed to alpha read %q, want %q", got, line)
		}
	}
	for _, line := range []string{"beta-one", "beta-two"} {
		message, _ := receive(t, b)
		if message.PipelineID != "beta" {
			t.Fatalf("the client subscribed to beta was sent a message about %q", message.PipelineID)
		}
		if got := lineOf(t, message); got != line {
			t.Fatalf("the client subscribed to beta read %q, want %q", got, line)
		}
	}

	// And the sentinel: the next thing either client reads is its own, so
	// nothing was queued behind what was read above.
	hub.Publish(logMessage("alpha", "alpha-last"))
	hub.Publish(logMessage("beta", "beta-last"))
	if message, _ := receive(t, a); lineOf(t, message) != "alpha-last" {
		t.Fatalf("the client subscribed to alpha had something else queued: %v", message)
	}
	if message, _ := receive(t, b); lineOf(t, message) != "beta-last" {
		t.Fatalf("the client subscribed to beta had something else queued: %v", message)
	}
}

// A client that subscribed to nothing is sent nothing. Asserted without a
// negative timeout: whatever was published before the subscription has to not
// be the first thing the client reads afterwards.
func TestAClientThatSubscribedToNothingIsSentNothing(t *testing.T) {
	t.Parallel()
	hub := startChannel(t, Options{})
	client := hub.dial(t)

	// The hub has the connection, and the connection has no subscriptions.
	waitFor(t, "the connection to be registered", func() bool { return hub.Connections() == 1 })
	hub.Publish(logMessage("alpha", "sent-before-anyone-asked"))
	hub.Publish(ChannelMessage{Kind: ChannelMessageKindStatus, PipelineID: "beta", Payload: ChannelPayload{}})

	subscribe(t, client, "alpha")
	hub.Publish(logMessage("alpha", "the-first-thing-it-should-see"))
	message, _ := receive(t, client)
	if got := lineOf(t, message); got != "the-first-thing-it-should-see" {
		t.Fatalf("the first message a client read was %q, so it was sent something it had not subscribed to", got)
	}
}

// Unsubscribing stops delivery, and the acknowledgement is what says when.
func TestUnsubscribingStopsDelivery(t *testing.T) {
	t.Parallel()
	hub := startChannel(t, Options{})
	client := hub.dial(t)

	subscribe(t, client, "alpha")
	command(t, client, ChannelCommandNameUnsubscribe, "alpha")
	acknowledgement, _ := receive(t, client)
	if acknowledgement.Kind != ChannelMessageKindUnsubscribed || acknowledgement.PipelineID != "alpha" {
		t.Fatalf("unsubscribing was acknowledged as %v %q", acknowledgement.Kind, acknowledgement.PipelineID)
	}

	hub.Publish(logMessage("alpha", "after-unsubscribing"))
	subscribe(t, client, "beta")
	hub.Publish(logMessage("beta", "the-next-thing-it-should-see"))
	message, _ := receive(t, client)
	if got := lineOf(t, message); got != "the-next-thing-it-should-see" {
		t.Fatalf("after unsubscribing from alpha the client read %q", got)
	}
}

// Two clients subscribed to the same pipeline both get it, so a subscription
// is not a handover.
func TestTwoClientsOnOnePipelineBothReceiveIt(t *testing.T) {
	t.Parallel()
	hub := startChannel(t, Options{})
	a, b := hub.dial(t), hub.dial(t)
	subscribe(t, a, "alpha")
	subscribe(t, b, "alpha")

	hub.Publish(logMessage("alpha", "to-both-of-them"))
	for name, client := range map[string]*websocket.Conn{"the first client": a, "the second client": b} {
		message, _ := receive(t, client)
		if got := lineOf(t, message); got != "to-both-of-them" {
			t.Fatalf("%s read %q", name, got)
		}
	}
}

// A message that cannot be serialized is logged and dropped. The value is a
// channel inside the payload, which encoding/json genuinely refuses: nothing
// is mocked, so what is being tested is what would really happen.
func TestAMessageThatCannotBeSerializedIsLoggedAndDropped(t *testing.T) {
	t.Parallel()
	hub := startChannel(t, Options{})
	client := hub.dial(t)
	subscribe(t, client, "alpha")

	hub.Publish(ChannelMessage{
		Kind:       ChannelMessageKindLog,
		PipelineID: "alpha",
		Payload:    ChannelPayload{"doomed": make(chan int)},
	})

	// The connection survived it, and so did the process: the next message
	// arrives on the same connection.
	hub.Publish(logMessage("alpha", "after-the-bad-one"))
	message, _ := receive(t, client)
	if got := lineOf(t, message); got != "after-the-bad-one" {
		t.Fatalf("the message after the unserializable one was %q", got)
	}
	if hub.Connections() != 1 {
		t.Fatalf("the channel has %d connections, so the unserializable message took one down", hub.Connections())
	}
	if logged := hub.logs.String(); !strings.Contains(logged, "could not be serialized") {
		t.Fatalf("the unserializable message was dropped without a word in the log:\n%s", logged)
	}
}

// The log never carries a payload. A payload is the one part of a message that
// could hold something an operator's log should not, and the channel logs a
// kind and a pipeline id instead.
func TestTheLogNeverCarriesAPayload(t *testing.T) {
	t.Parallel()
	hub := startChannel(t, Options{})
	client := hub.dial(t)
	subscribe(t, client, "alpha")

	const secretish = "this-string-is-in-a-payload-and-nowhere-else"
	hub.Publish(logMessage("alpha", secretish))
	if message, _ := receive(t, client); lineOf(t, message) != secretish {
		t.Fatal("the message did not arrive, so this test proves nothing")
	}
	hub.Publish(ChannelMessage{Kind: ChannelMessageKindLog, PipelineID: "alpha", Payload: ChannelPayload{"doomed": make(chan int), "line": secretish}})

	if logged := hub.logs.String(); strings.Contains(logged, secretish) {
		t.Fatalf("a payload reached the log:\n%s", logged)
	}
}
