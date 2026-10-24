// The bounds: what happens to a client that stops reading, to one that has
// died without saying so, and to one that sends something the channel will not
// read. Every interval these tests depend on is injected and tiny, so none of
// them sits out a real timeout, and every outcome is waited for as a condition
// rather than timed.

package channel

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// bulky is one message big enough that a few dozen of them cannot fit in the
// kernel's socket buffers. That is what makes a client which has stopped
// reading really block a write, rather than having everything it is sent
// quietly absorbed on its behalf.
func bulky(pipeline PipelineID) ChannelMessage {
	return logMessage(pipeline, strings.Repeat("x", 256<<10))
}

// A client that stops reading is disconnected by the write timeout, and the
// agent is not held up for even as long as that: the publisher hands the
// message over and carries on.
//
// The outbound queue is made large and the ping interval impossibly long, so
// that the write timeout is the only thing in this test that can end the
// connection. The overflow path is the next test.
func TestAClientThatStopsReadingIsDisconnectedByTheWriteTimeout(t *testing.T) {
	t.Parallel()
	hub := startChannel(t, Options{
		WriteTimeout:   brief,
		OutboundBuffer: 1024,
		PingInterval:   time.Hour,
	})
	client := hub.dial(t)
	subscribe(t, client, "alpha")

	// From here the client reads nothing at all, so its TCP window stops
	// moving and a write to it blocks.
	published := time.Now()
	for range 200 {
		hub.Publish(bulky("alpha"))
	}
	if blocked := time.Since(published); blocked > patience {
		t.Fatalf("publishing 200 messages to a client that had stopped reading took %s, so the client blocked the agent", blocked)
	}

	waitFor(t, "the client that stopped reading to be disconnected", func() bool { return hub.Connections() == 0 })
	if logged := hub.logs.String(); !strings.Contains(logged, "did not read a message within the write timeout") {
		t.Fatalf("the client was disconnected without the log saying it was the write timeout:\n%s", logged)
	}
}

// A client that falls behind its queue is disconnected for that, with no
// timeout involved: the write timeout here is an hour, so the only thing that
// can end this connection is the queue filling up.
func TestAClientThatFallsBehindIsDisconnected(t *testing.T) {
	t.Parallel()
	hub := startChannel(t, Options{
		WriteTimeout:   time.Hour,
		OutboundBuffer: 4,
		PingInterval:   time.Hour,
	})
	client := hub.dial(t)
	subscribe(t, client, "alpha")

	published := time.Now()
	for range 100 {
		hub.Publish(bulky("alpha"))
	}
	if blocked := time.Since(published); blocked > patience {
		t.Fatalf("publishing to a client with a full queue took %s, so a full queue blocks the agent", blocked)
	}

	waitFor(t, "the client that fell behind to be disconnected", func() bool { return hub.Connections() == 0 })
	if logged := hub.logs.String(); !strings.Contains(logged, "not keeping up") {
		t.Fatalf("the client was disconnected without the log saying it had fallen behind:\n%s", logged)
	}
}

// A dead peer is found by the heartbeat and the connection is closed. The peer
// here is dead in the way that is hard to notice: the socket is open, the
// process is alive, and it answers nothing -- which is what a slept laptop and
// a frozen tab look like from here.
//
// The intervals are injected rather than waited out, which is the whole reason
// they are Options: with the real ones this test would take 25 seconds to
// assert what it asserts in a few milliseconds.
func TestADeadPeerIsDetectedByTheHeartbeatAndDisconnected(t *testing.T) {
	t.Parallel()
	hub := startChannel(t, Options{
		PingInterval: brief,
		PongTimeout:  brief,
		// Long, so that a write cannot be what ends this connection.
		WriteTimeout: time.Hour,
	})
	client := hub.dial(t)
	subscribe(t, client, "alpha")
	// A coder/websocket client answers a ping from its read path, so a client
	// that never reads never pongs. Nothing else about it changes: the socket
	// stays open and the process stays alive.

	waitFor(t, "the dead peer to be disconnected", func() bool { return hub.Connections() == 0 })
	if logged := hub.logs.String(); !strings.Contains(logged, "did not answer a ping") {
		t.Fatalf("the dead peer was disconnected without the log saying it was the heartbeat:\n%s", logged)
	}
}

// The heartbeat does not disconnect a live peer. The same tiny intervals, many
// times over, with a client that is reading: it answers every ping, because
// reading is what makes a client answer, and it is still there at the end.
func TestTheHeartbeatLeavesALivePeerAlone(t *testing.T) {
	t.Parallel()
	hub := startChannel(t, Options{PingInterval: brief, PongTimeout: patience})
	client := hub.dial(t)
	subscribe(t, client, "alpha")

	// Reading in the background is what answers the pings; the ticker fires
	// many times over the life of this read.
	read := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 50*brief)
		defer cancel()
		for {
			if _, _, err := client.Read(ctx); err != nil {
				read <- err
				return
			}
		}
	}()
	select {
	case err := <-read:
		t.Fatalf("a live peer that was answering pings was disconnected: %v", err)
	case <-time.After(20 * brief):
	}
	if hub.Connections() != 1 {
		t.Fatal("a live peer that was answering pings was disconnected")
	}
}

// Something that is not a command ends the connection rather than being
// guessed at. Most of these are refused by the generated unmarshaller, which
// is the point: the envelope's own rules out of the schema are the validation,
// not a second copy of them written here.
func TestSomethingThatIsNotACommandEndsTheConnection(t *testing.T) {
	t.Parallel()
	for name, sent := range map[string]string{
		"not JSON at all":                      "hello",
		"JSON that is not an object":           `["subscribe","alpha"]`,
		"a command with no name":               `{"pipelineId":"alpha"}`,
		"a command with no pipeline":           `{"command":"subscribe"}`,
		"a command name that does not exist":   `{"command":"start","pipelineId":"alpha"}`,
		"a pipeline id no pipeline could have": `{"command":"subscribe","pipelineId":"../../etc/passwd"}`,
		"an unknown property":                  `{"command":"subscribe","pipelineId":"alpha","extra":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			hub := startChannel(t, Options{})
			client := hub.dial(t)
			waitFor(t, "the connection to be registered", func() bool { return hub.Connections() == 1 })

			ctx, cancel := context.WithTimeout(t.Context(), patience)
			defer cancel()
			if err := client.Write(ctx, websocket.MessageText, []byte(sent)); err != nil {
				t.Fatalf("send %s: %v", name, err)
			}
			waitFor(t, "the connection to be closed", func() bool { return hub.Connections() == 0 })
		})
	}
}

// A binary frame is not a command either.
func TestABinaryFrameEndsTheConnection(t *testing.T) {
	t.Parallel()
	hub := startChannel(t, Options{})
	client := hub.dial(t)
	waitFor(t, "the connection to be registered", func() bool { return hub.Connections() == 1 })

	ctx, cancel := context.WithTimeout(t.Context(), patience)
	defer cancel()
	if err := client.Write(ctx, websocket.MessageBinary, []byte(`{"command":"subscribe","pipelineId":"alpha"}`)); err != nil {
		t.Fatalf("send a binary frame: %v", err)
	}
	waitFor(t, "the connection to be closed", func() bool { return hub.Connections() == 0 })
}

// A message over the read limit is refused rather than buffered. The limit is
// injected small; what matters is that a client cannot make the agent hold an
// arbitrary amount of memory by sending one message.
func TestAMessageOverTheReadLimitEndsTheConnection(t *testing.T) {
	t.Parallel()
	hub := startChannel(t, Options{ReadLimit: 64})
	client := hub.dial(t)
	waitFor(t, "the connection to be registered", func() bool { return hub.Connections() == 1 })

	ctx, cancel := context.WithTimeout(t.Context(), patience)
	defer cancel()
	// Valid JSON, valid command, far too long: the limit is what refuses it.
	oversize := `{"command":"subscribe","pipelineId":"` + strings.Repeat("a", 4096) + `"}`
	if err := client.Write(ctx, websocket.MessageText, []byte(oversize)); err != nil {
		// A write that fails because the server has already hung up is the
		// outcome this test is about, so it is not a failure.
		t.Logf("the oversize message could not be fully written, which is the server refusing it: %v", err)
	}
	waitFor(t, "the connection to be closed", func() bool { return hub.Connections() == 0 })
}

// A client cannot make the agent remember an unbounded number of pipeline ids.
func TestAClientCannotSubscribeToMorePipelinesThanTheLimit(t *testing.T) {
	t.Parallel()
	hub := startChannel(t, Options{})
	client := hub.dial(t)

	for index := range maxSubscriptions {
		subscribe(t, client, PipelineID("pipeline-"+strconv.Itoa(index)))
	}
	// Re-subscribing to one it already has is not a new subscription, so it
	// is still allowed at the limit.
	subscribe(t, client, "pipeline-0")

	command(t, client, ChannelCommandNameSubscribe, "one-too-many")
	waitFor(t, "the connection to be closed", func() bool { return hub.Connections() == 0 })
	if logged := hub.logs.String(); !strings.Contains(logged, "more pipelines than one connection may have") {
		t.Fatalf("the connection was closed without the log saying why:\n%s", logged)
	}
}

// Closing the hub disconnects every client and leaves nothing running: when
// Close returns, every connection has been removed, which it can only know by
// having waited for each one.
func TestClosingTheHubDisconnectsEveryClient(t *testing.T) {
	t.Parallel()
	hub := startChannel(t, Options{})
	for range 5 {
		client := hub.dial(t)
		subscribe(t, client, "alpha")
	}
	if hub.Connections() != 5 {
		t.Fatalf("the hub has %d connections, want 5", hub.Connections())
	}

	hub.Close()
	if left := hub.Connections(); left != 0 {
		t.Fatalf("%d connections were still registered when Close returned", left)
	}

	// And it refuses a new one, rather than handing out a connection that is
	// about to be thrown away.
	ctx, cancel := context.WithTimeout(t.Context(), patience)
	defer cancel()
	if ws, _, err := websocket.Dial(ctx, hub.url, nil); err == nil {
		_ = ws.CloseNow()
		t.Fatal("a closed hub accepted a handshake")
	}

	// Closing twice is not a failure: a shutdown path should not have to
	// remember whether it has already run.
	hub.Close()
}
