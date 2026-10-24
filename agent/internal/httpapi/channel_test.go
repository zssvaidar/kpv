// The channel at the boundary: what an upgrade has to present before it is
// accepted, what a refused one is told, and what is left behind when a client
// goes away.
//
// The checks themselves are #4's and #58's, and their tests are in guard_test.go
// and origin_test.go, unchanged. What is new here is that an upgrade inherits
// them rather than having its own, and that a refused upgrade says nothing a
// rejected GET does not.

package httpapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/zssvaidar/kpv/agent/internal/channel"
)

// patience is the ceiling on anything a test waits for: long, because it is
// not a measurement. A test that needs more than a few milliseconds of it has
// found a real failure, and a loaded runner needs the room.
const patience = 15 * time.Second

// dialTimeoutForChannel is kept separate from bypass_test.go's dialTimeout so
// that file stays exactly as #58 left it.
const dialTimeoutForChannel = patience

// upgradeHeaders is a well-formed WebSocket handshake's headers, which is what
// makes the rejection tests about the checks rather than about the handshake.
func upgradeHeaders(t *testing.T) http.Header {
	t.Helper()
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("make a Sec-WebSocket-Key: %v", err)
	}
	return http.Header{
		"Connection":            []string{"Upgrade"},
		"Upgrade":               []string{"websocket"},
		"Sec-Websocket-Version": []string{"13"},
		"Sec-Websocket-Key":     []string{base64.StdEncoding.EncodeToString(key)},
	}
}

// channelDial connects a real WebSocket client to the channel, presenting the
// token the way a browser has to: in the query parameter, because a
// WebSocket constructor cannot set a header.
func (ts *testServer) channelDial(t *testing.T, client *http.Client, query url.Values, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	target := "ws://" + ts.Addr() + "/api/channel"
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	ctx, cancel := context.WithTimeout(t.Context(), dialTimeoutForChannel)
	defer cancel()
	return websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: client, HTTPHeader: header})
}

// channelClient is one HTTP client for the WebSocket dials of one test. It is
// not shared between tests, so one test's connections cannot be counted
// against another's.
func channelClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
}

// connected is the client's side of an open channel: the connection, with the
// token it presented, so a test can look for that token in what arrives.
func (ts *testServer) connect(t *testing.T, client *http.Client) *websocket.Conn {
	t.Helper()
	header := http.Header{"Origin": []string{ts.URL()}}
	query := url.Values{tokenParameter: []string{ts.Token().Secret()}}
	ws, _, err := ts.channelDial(t, client, query, header)
	if err != nil {
		t.Fatalf("connect to the channel with the token and an allowed origin: %v", err)
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	return ws
}

// subscribeTo sends a subscription and waits for the acknowledgement, so that
// what follows in a test happens with the subscription in force.
func subscribeTo(t *testing.T, ws *websocket.Conn, pipeline string) {
	t.Helper()
	raw, err := json.Marshal(channel.ChannelCommand{
		Command:    channel.ChannelCommandNameSubscribe,
		PipelineID: channel.PipelineID(pipeline),
	})
	if err != nil {
		t.Fatalf("marshal a subscription: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), patience)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatalf("subscribe to %s: %v", pipeline, err)
	}
	message, _ := receiveFrom(t, ws)
	if message.Kind != channel.ChannelMessageKindSubscribed {
		t.Fatalf("subscribing to %s was answered with %v", pipeline, message.Kind)
	}
}

func receiveFrom(t *testing.T, ws *websocket.Conn) (channel.ChannelMessage, []byte) {
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
	var message channel.ChannelMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatalf("the channel sent %q, which is not an envelope: %v", raw, err)
	}
	return message, raw
}

func logLine(pipeline, line string) channel.ChannelMessage {
	return channel.ChannelMessage{
		Kind:       channel.ChannelMessageKindLog,
		PipelineID: channel.PipelineID(pipeline),
		Payload:    channel.ChannelPayload{"line": line},
	}
}

// waitUntil polls a condition until it holds or patience runs out. The poll
// interval is not synchronisation: the assertion is the condition.
func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(patience)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %s", what, patience)
		}
		time.Sleep(time.Millisecond)
	}
}

// The client that does everything right gets a channel, and the token travels
// the way a browser has to send it.
func TestAChannelClientWithTheTokenAndAnAllowedOriginIsServed(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	client := channelClient()

	ws := server.connect(t, client)
	subscribeTo(t, ws, "alpha")
	server.Channel().Publish(logLine("alpha", "the-line"))

	message, _ := receiveFrom(t, ws)
	if message.Kind != channel.ChannelMessageKindLog || message.PipelineID != "alpha" {
		t.Fatalf("the channel delivered %v %q", message.Kind, message.PipelineID)
	}
	if line, _ := message.Payload["line"].(string); line != "the-line" {
		t.Fatalf("the channel delivered the payload %v", message.Payload)
	}
}

// The token may also arrive in the header, which is what a client that is not
// a browser would do. Both places are the guard's, not this route's.
func TestAChannelClientMayPresentTheTokenInTheHeader(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	client := channelClient()

	header := http.Header{
		"Origin":            []string{server.URL()},
		authorizationHeader: []string{"Bearer " + server.Token().Secret()},
	}
	ws, _, err := server.channelDial(t, client, nil, header)
	if err != nil {
		t.Fatalf("connect with a bearer token: %v", err)
	}
	defer func() { _ = ws.CloseNow() }()
	subscribeTo(t, ws, "alpha")
}

// Every way an upgrade can fail a check answers exactly what a rejected GET to
// a path that does not exist answers: the same status, the same headers, the
// same body, byte for byte.
//
// This is the test for the thing that is easy to get wrong here. The library
// is helpful when it will not upgrade -- 426 with the header it wanted, 400
// with the version it supports, 403 naming the origin it rejected -- and every
// one of those sentences would be something this endpoint says that no other
// path on this server will.
func TestARefusedUpgradeIsAnsweredLikeEveryOtherRejection(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{AllowedOrigins: []string{"https://ui.example"}})
	right := server.Token().Secret()
	own := server.URL()

	// The baseline: an unauthorized request to a path that was never there.
	baseline := server.do(t, request{path: "/api/does-not-exist"})
	if baseline.Status != rejectionStatus || baseline.Body != rejectionBody {
		t.Fatalf("the baseline rejection is not the uniform one: %s", baseline)
	}

	upgrade := func(extra func(http.Header)) http.Header {
		header := upgradeHeaders(t)
		if extra != nil {
			extra(header)
		}
		return header
	}
	withOrigin := func(origin string) func(http.Header) {
		return func(header http.Header) { header.Set("Origin", origin) }
	}

	for name, want := range map[string]request{
		"an upgrade with no token": {
			path: "/api/channel", origins: []string{own}, headers: upgrade(nil),
		},
		"an upgrade with a wrong token of the right length": {
			path: "/api/channel", token: strings.Repeat("A", len(right)), origins: []string{own}, headers: upgrade(nil),
		},
		"an upgrade with the token in the query but no origin": {
			path: "/api/channel", queryToken: right, headers: upgrade(nil),
		},
		"an upgrade with a disallowed origin": {
			path: "/api/channel", token: right, headers: upgrade(withOrigin("https://evil.example")),
		},
		"an upgrade with the null origin": {
			path: "/api/channel", token: right, headers: upgrade(withOrigin("null")),
		},
		// #58's exception is keyed on fetch metadata and deliberately
		// excludes an upgrade, because a browser does send Origin on a
		// handshake. These two are that exclusion: no relaxation is
		// inherited here.
		"an upgrade with no origin but same-origin fetch metadata": {
			path: "/api/channel", token: right, headers: upgrade(func(header http.Header) {
				header.Set(secFetchSiteHeader, "same-origin")
			}),
		},
		"an upgrade with no origin but no-initiator fetch metadata": {
			path: "/api/channel", token: right, headers: upgrade(func(header http.Header) {
				header.Set(secFetchSiteHeader, "none")
			}),
		},
		"an upgrade with neither the token nor an origin": {
			path: "/api/channel", headers: upgrade(nil),
		},
		// Past the checks, and refused by the handshake itself. A caller
		// holding the token learns nothing about this endpoint that a caller
		// without it learns about any other.
		"an authorized request that is not an upgrade at all": {
			path: "/api/channel", token: right, origins: []string{own},
		},
		"an authorized upgrade asking for a protocol version that does not exist": {
			path: "/api/channel", token: right, origins: []string{own}, headers: upgrade(func(header http.Header) {
				header.Set("Sec-Websocket-Version", "8")
			}),
		},
		"an authorized upgrade with no Connection header": {
			path: "/api/channel", token: right, origins: []string{own}, headers: upgrade(func(header http.Header) {
				header.Del("Connection")
			}),
		},
		"an authorized upgrade with a key that is not a key": {
			path: "/api/channel", token: right, origins: []string{own}, headers: upgrade(func(header http.Header) {
				header.Set("Sec-Websocket-Key", "not-sixteen-bytes-of-base64")
			}),
		},
		"an authorized upgrade with two keys": {
			path: "/api/channel", token: right, origins: []string{own}, headers: upgrade(func(header http.Header) {
				header.Add("Sec-Websocket-Key", base64.StdEncoding.EncodeToString(make([]byte, 16)))
			}),
		},
	} {
		answer := server.do(t, want)
		if want.headers != nil && want.origins != nil {
			// The table sets an origin either in the request or in the
			// headers, never both, so a case cannot pass by accident.
			if _, both := want.headers["Origin"]; both {
				t.Fatalf("%s sets Origin twice, so what it is testing is not clear", name)
			}
		}
		if answer.Status != baseline.Status || answer.Headers != baseline.Headers || answer.Body != baseline.Body {
			t.Errorf("%s was answered something a rejected GET is not:\nwanted:\n%s\ngot:\n%s", name, baseline, answer)
		}
		if strings.Contains(answer.Body, right) {
			t.Errorf("%s was answered a body carrying the token", name)
		}
	}

	// One case that is not comparable to the baseline and is here so it is
	// not forgotten: a caller with the token asking for a method the route
	// does not serve gets net/http's own routing answer, as it does for every
	// other route. It still must not be a success.
	authorizedPost := server.do(t, request{method: http.MethodPost, path: "/api/channel", token: right, origins: []string{own}})
	if authorizedPost.Status >= 200 && authorizedPost.Status < 300 {
		t.Errorf("POST /api/channel with the token was a success: %s", authorizedPost)
	}
}

// A configured allowed origin is served, which is the other half of the
// origin rule: the allowlist is what decides, and it is the guard's allowlist,
// so an origin the operator added works here exactly as it works on every
// other route.
func TestAChannelClientFromAConfiguredAllowedOriginIsServed(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{AllowedOrigins: []string{"https://ui.example"}})
	client := channelClient()

	query := url.Values{tokenParameter: []string{server.Token().Secret()}}
	ws, _, err := server.channelDial(t, client, query, http.Header{"Origin": []string{"https://ui.example"}})
	if err != nil {
		t.Fatalf("connect from a configured allowed origin: %v", err)
	}
	defer func() { _ = ws.CloseNow() }()
	subscribeTo(t, ws, "alpha")
}

// An unauthenticated upgrade is refused and is never delivered a message. The
// publish really happens and really reaches a subscriber, so this is not a
// test that nothing was published.
func TestAnUnauthenticatedUpgradeIsNeverDeliveredAMessage(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	client := channelClient()

	// A client that did everything right, to prove the publish below is real.
	authorized := server.connect(t, client)
	subscribeTo(t, authorized, "alpha")

	// And the one that presented nothing. The handshake is well formed; what
	// it lacks is the token.
	const secretish = "a-line-no-unauthorized-client-may-see"
	raw := rawUpgrade(t, server.Addr(), "", func() {
		server.Channel().Publish(logLine("alpha", secretish))
	})

	if !strings.Contains(raw, " "+itoa(rejectionStatus)+" ") {
		t.Fatalf("an unauthenticated upgrade was answered:\n%s", raw)
	}
	if strings.Contains(raw, "101") || strings.Contains(raw, "Sec-WebSocket-Accept") {
		t.Fatalf("an unauthenticated upgrade was accepted:\n%s", raw)
	}
	if strings.Contains(raw, secretish) || strings.Contains(raw, "alpha") {
		t.Fatalf("an unauthenticated upgrade was delivered a message:\n%s", raw)
	}

	// The message reached the client that was entitled to it, so the publish
	// above was not a no-op.
	message, _ := receiveFrom(t, authorized)
	if line, _ := message.Payload["line"].(string); line != secretish {
		t.Fatalf("the authorized client read %v, so the publish did not happen as this test assumes", message.Payload)
	}
	if connected := server.Channel().Connections(); connected != 1 {
		t.Fatalf("the channel has %d connections, so the unauthenticated upgrade got one", connected)
	}

	// A real client library refuses it too, which is what a browser would see.
	if ws, _, err := server.channelDial(t, client, nil, http.Header{"Origin": []string{server.URL()}}); err == nil {
		_ = ws.CloseNow()
		t.Fatal("a WebSocket client with no token was given a connection")
	}
}

// rawUpgrade sends a handshake as bytes and returns everything the server
// answers. token is presented in the query when it is not empty.
//
// during runs once the request has been written, which is how a test publishes
// something while a refused handshake is still on the wire.
//
// The request asks for the connection to be closed as well as upgraded, so the
// read ends at EOF and nothing here waits on a clock.
func rawUpgrade(t *testing.T, address, token string, during func()) string {
	t.Helper()
	target := "/api/channel"
	if token != "" {
		target += "?" + url.Values{tokenParameter: []string{token}}.Encode()
	}
	key := base64.StdEncoding.EncodeToString(make([]byte, 16))
	wire := "GET " + target + " HTTP/1.1\r\n" +
		"Host: " + address + "\r\n" +
		"Origin: http://" + address + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade, close\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n\r\n"

	connection, err := net.DialTimeout("tcp", address, dialTimeoutForChannel)
	if err != nil {
		t.Fatalf("connect to the agent API: %v", err)
	}
	defer func() { _ = connection.Close() }()
	if err := connection.SetDeadline(time.Now().Add(dialTimeoutForChannel)); err != nil {
		t.Fatalf("set a deadline: %v", err)
	}
	if _, err := connection.Write([]byte(wire)); err != nil {
		t.Fatalf("write the handshake: %v", err)
	}
	if during != nil {
		during()
	}

	reader := bufio.NewReader(connection)
	var answer strings.Builder
	buffer := make([]byte, 4096)
	for {
		read, err := reader.Read(buffer)
		answer.Write(buffer[:read])
		if err != nil {
			break
		}
	}
	return answer.String()
}

// Nothing the channel sends carries the token, and the token is in the URL the
// client dialled, so there is a real way for it to get in: an echo, a
// diagnostic, a message built from the request.
func TestNothingTheChannelSendsCarriesTheToken(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	client := channelClient()
	secret := server.Token().Secret()

	ws := server.connect(t, client)
	frames := []string{}

	raw := func() {
		_, frame := receiveFrom(t, ws)
		frames = append(frames, string(frame))
	}
	// The acknowledgement, a message, and an acknowledgement of the other
	// command: every shape the channel sends unprompted or in answer.
	subscribeTo(t, ws, "alpha")
	server.Channel().Publish(logLine("alpha", "a-line"))
	raw()
	server.Channel().Publish(channel.ChannelMessage{
		Kind:       channel.ChannelMessageKindStatus,
		PipelineID: "alpha",
		Payload:    channel.ChannelPayload{"state": "running"},
	})
	raw()

	unsubscribe, err := json.Marshal(channel.ChannelCommand{
		Command: channel.ChannelCommandNameUnsubscribe, PipelineID: "alpha",
	})
	if err != nil {
		t.Fatalf("marshal an unsubscription: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), patience)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageText, unsubscribe); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	raw()

	if len(frames) != 3 {
		t.Fatalf("read %d frames, want 3", len(frames))
	}
	for _, frame := range frames {
		if strings.Contains(frame, secret) {
			t.Errorf("a frame carried the session token: %s", frame)
		}
		if strings.Contains(frame, tokenParameter+"=") {
			t.Errorf("a frame carried the query the client dialled with: %s", frame)
		}
	}
	if logged := server.logs.String(); strings.Contains(logged, secret) {
		t.Errorf("the token reached the log while the channel was in use:\n%s", logged)
	}
}

// Many clients come and go, and nothing is left running. The count is what the
// criterion asks for; the stacks are what makes a failure say which goroutine
// it was, and they are checked by name, because a count can be right while a
// goroutine of this package's own is stuck.
//
// This test is not parallel: it counts goroutines, and the count is only
// meaningful while nothing else in the package is starting any. Go runs the
// parallel tests after the sequential ones for exactly this reason.
func TestManyChannelConnectionsLeaveNoGoroutineBehind(t *testing.T) {
	server := startServer(t, Options{})
	// One client, one HTTP client, reused by every cycle: a new Transport per
	// cycle would be goroutines of the test's own making, which is not what
	// is being measured.
	client := channelClient()

	cycle := func() {
		ws := server.connect(t, client)
		subscribeTo(t, ws, "alpha")
		server.Channel().Publish(logLine("alpha", "a-line"))
		if _, _, err := ws.Read(t.Context()); err != nil {
			t.Fatalf("read the message: %v", err)
		}
		if err := ws.Close(websocket.StatusNormalClosure, ""); err != nil {
			t.Fatalf("close the connection: %v", err)
		}
		waitUntil(t, "the server to let go of the connection", func() bool {
			return server.Channel().Connections() == 0
		})
	}

	// A warm-up cycle first, so that everything lazily created exists before
	// the count is taken: the transport's connection goroutines, the
	// library's buffers, the server's own. The baseline is then taken once
	// the count has stopped moving, not at an arbitrary instant.
	cycle()
	before := quiesce(t)

	const cycles = 50
	for range cycles {
		cycle()
	}

	after := settleTo(t, before)
	if after > before {
		t.Errorf("%d goroutines before %d connections, %d after; %d were left behind:\n%s",
			before, cycles, after, after-before, goroutineStacks())
	}
	for _, leaked := range []string{"channel.(*connection).writeLoop", "channel.(*Hub).Serve"} {
		if strings.Contains(goroutineStacks(), leaked) {
			t.Errorf("a goroutine of this agent's own is still running after every client left: %s\n%s", leaked, goroutineStacks())
		}
	}
}

// The two ways this test waits for a goroutine count, and why it waits at all.
//
// A count is not stable the instant a connection closes: the server's handler
// goroutine, the transport's and the library's all end in their own time.
// Sleeping "long enough" is what makes a leak test flaky in both directions --
// a slow runner fails, and a real leak passes on a fast one -- so both of
// these watch the number instead.

// quiesce returns the count once it has stopped changing, which is what a
// baseline has to be: a number taken at an arbitrary instant would be whatever
// was still winding down.
func quiesce(t *testing.T) int {
	t.Helper()
	const still = 100 // consecutive polls, so about a tenth of a second
	deadline := time.Now().Add(patience)
	count, unchanged := runtime.NumGoroutine(), 0
	for unchanged < still {
		if time.Now().After(deadline) {
			t.Fatalf("the goroutine count never settled; it is %d:\n%s", count, goroutineStacks())
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
		now := runtime.NumGoroutine()
		if now == count {
			unchanged++
			continue
		}
		count, unchanged = now, 0
	}
	return count
}

// settleTo waits for the count to come back down to target, returning what it
// reached: target if it got there, and a higher number if it stopped coming
// down, which is a leak.
func settleTo(t *testing.T, target int) int {
	t.Helper()
	deadline := time.Now().Add(patience)
	count := runtime.NumGoroutine()
	for count > target && time.Now().Before(deadline) {
		runtime.Gosched()
		time.Sleep(time.Millisecond)
		count = runtime.NumGoroutine()
	}
	return count
}

func goroutineStacks() string {
	buffer := make([]byte, 1<<16)
	return string(buffer[:runtime.Stack(buffer, true)])
}

// Shutting the server down takes the channel's clients with it. net/http does
// not know about a hijacked connection, so without the server closing the
// channel itself a shut-down agent would keep every client and its goroutine.
func TestShuttingTheServerDownDisconnectsChannelClients(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	client := channelClient()

	ws := server.connect(t, client)
	subscribeTo(t, ws, "alpha")

	ctx, cancel := context.WithTimeout(t.Context(), patience)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("shut the server down: %v", err)
	}
	if connected := server.Channel().Connections(); connected != 0 {
		t.Fatalf("%d channel clients were still connected when shutdown returned", connected)
	}
	if _, _, err := ws.Read(ctx); err == nil {
		t.Fatal("a channel client could still read after the server shut down")
	}
}

// A channel that has been closed answers the route uniformly too, and this is
// the case that would otherwise be the worst of them: the channel refuses
// before the library is reached, so nothing has been written, and a handler
// that returns having written nothing gets net/http's empty 200 -- a
// distinguishable answer, and a successful-looking one.
//
// It is built by hand because a shut-down server stops accepting connections
// before a request could reach the route at all, which is the only way this
// state arises in the agent itself.
func TestAClosedChannelRejectsTheRouteUniformly(t *testing.T) {
	t.Parallel()
	token, err := NewToken()
	if err != nil {
		t.Fatalf("mint a token: %v", err)
	}
	origins, err := newOriginPolicy(1234, nil)
	if err != nil {
		t.Fatalf("build the origin policy: %v", err)
	}
	hub := channel.New(channel.Options{})
	hub.Close()
	handler := newHandler(&guard{token: token, origins: origins, logger: discardLogger(), channel: hub}, apiRoutes())

	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1234/api/channel", nil)
	r.Header.Set(authorizationHeader, "Bearer "+token.Secret())
	r.Header.Set("Origin", "http://127.0.0.1:1234")
	for name, value := range upgradeHeaders(t) {
		r.Header[name] = value
	}
	recorded := httptest.NewRecorder()
	handler.ServeHTTP(recorded, r)
	if recorded.Code != rejectionStatus || recorded.Body.String() != rejectionBody {
		t.Fatalf("a closed channel answered %d %q, want the uniform rejection", recorded.Code, recorded.Body.String())
	}
	if recorded.Header().Get("Content-Type") != rejectionContentType {
		t.Fatalf("a closed channel answered with the headers %v", recorded.Header())
	}
}

// A server built with no channel answers the route the way it answers
// everything else. Only a test builds one, and this is that test: the
// alternative to answering uniformly would be a nil dereference, which is a
// 500 and a stack trace in the log.
func TestAServerWithNoChannelRejectsTheRouteUniformly(t *testing.T) {
	t.Parallel()
	token, err := NewToken()
	if err != nil {
		t.Fatalf("mint a token: %v", err)
	}
	origins, err := newOriginPolicy(1234, nil)
	if err != nil {
		t.Fatalf("build the origin policy: %v", err)
	}
	handler := newHandler(&guard{token: token, origins: origins, logger: discardLogger()}, apiRoutes())

	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1234/api/channel", nil)
	r.Header.Set(authorizationHeader, "Bearer "+token.Secret())
	r.Header.Set("Origin", "http://127.0.0.1:1234")
	for name, value := range upgradeHeaders(t) {
		r.Header[name] = value
	}
	recorded := httptest.NewRecorder()
	handler.ServeHTTP(recorded, r)
	if recorded.Code != rejectionStatus || recorded.Body.String() != rejectionBody {
		t.Fatalf("a server with no channel answered %d %q", recorded.Code, recorded.Body.String())
	}
}

// The channel is registered on the one mux, in the one function that builds
// it, and there is nothing else on that mux the rejection table above does not
// know about.
//
// Why this is worth asserting: the channel is not a row in apiRoutes, because
// every row is a request-and-an-answer route and the table's tests hold each
// row to answering 200 with JSON. A handshake is not that shape. So the thing
// that keeps the table's guarantee honest -- "every registered route was
// tested" -- has to be stated here instead: newHandler registers the table and
// the channel, and nothing else.
func TestNewHandlerRegistersTheTableAndTheChannelAndNothingElse(t *testing.T) {
	t.Parallel()
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "routes.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse routes.go: %v", err)
	}

	var builder *ast.FuncDecl
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == "newHandler" {
			builder = function
		}
	}
	if builder == nil {
		t.Fatal("newHandler is not in routes.go any more, so this test is checking nothing")
	}

	var registered []string
	ast.Inspect(builder.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		var rendered strings.Builder
		if err := printer.Fprint(&rendered, fileSet, call.Fun); err != nil {
			t.Fatalf("print a call: %v", err)
		}
		if !strings.HasSuffix(rendered.String(), ".Handle") || len(call.Args) == 0 {
			return true
		}
		var pattern strings.Builder
		if err := printer.Fprint(&pattern, fileSet, call.Args[0]); err != nil {
			t.Fatalf("print a pattern: %v", err)
		}
		registered = append(registered, pattern.String())
		return true
	})

	want := map[string]bool{"r.pattern": true, "channelPattern": true}
	if len(registered) != len(want) {
		t.Fatalf("newHandler registers %v; this test and the rejection table above know about %v, so one of them needs updating", registered, want)
	}
	for _, pattern := range registered {
		if !want[pattern] {
			t.Errorf("newHandler registers %s, which no test here covers; add it to the rejection table and to this list", pattern)
		}
	}
	if channelPattern != "GET /api/channel" {
		t.Errorf("the channel pattern is %q, so the paths this file asserts about are the wrong ones", channelPattern)
	}
}
