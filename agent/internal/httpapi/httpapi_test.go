// Shared scaffolding for the HTTP API tests: a server that is really bound and
// really serving, on whatever ephemeral port the kernel gave it, with its log
// captured so a test can assert on what was and was not written.

package httpapi

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// safeBuffer is a log sink several goroutines write to, because the server
// logs from its own. Without the mutex this is a data race, which -race would
// rightly fail on.
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

// testServer is a running Server plus everything it logged.
type testServer struct {
	*Server
	logs   *safeBuffer
	client *http.Client
}

// startServer binds and serves a server for the duration of the test. There is
// no sleep anywhere: New has already bound the listener when it returns, so the
// port is known and the socket accepts before Serve is even called.
func startServer(t *testing.T, options Options) *testServer {
	t.Helper()
	logs := &safeBuffer{}
	options.Logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	server, err := New(options)
	if err != nil {
		t.Fatalf("start the agent API: %v", err)
	}

	served := make(chan error, 1)
	go func() { served <- server.Serve() }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("close the agent API: %v", err)
		}
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("serving ended with: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("serving did not end after the server was closed")
		}
	})

	return &testServer{
		Server: server,
		logs:   logs,
		// No redirect following and no shared state with any other test's
		// client, so one test's connection reuse cannot affect another's.
		client: &http.Client{
			Transport:     &http.Transport{DisableKeepAlives: true},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// response is everything a client can observe about an answer, flattened so
// two answers can be compared for equality rather than looked at by eye. The
// Date header is dropped because it is a clock, not a signal.
type response struct {
	Status  int
	Headers string
	Body    string
}

func (r response) String() string {
	return fmt.Sprintf("%d\n%s\nbody: %q", r.Status, r.Headers, r.Body)
}

// request is one request to make, described rather than built, so the uniform
// rejection test can hold a table of awkward cases.
type request struct {
	// method and path default to GET /api/health.
	method, path string
	// token, when set, is presented as a bearer token. presentNoToken sends
	// no Authorization header at all.
	token string
	// authorization overrides token entirely, including with several values.
	authorization []string
	// queryToken, when set, is presented in the token query parameter.
	queryToken string
	// origins are the Origin header values; nil sends no Origin header.
	origins []string
}

func (ts *testServer) do(t *testing.T, want request) response {
	t.Helper()
	method := want.method
	if method == "" {
		method = http.MethodGet
	}
	path := want.path
	if path == "" {
		path = "/api/health"
	}
	target := ts.URL() + path
	if want.queryToken != "" {
		target += "?" + url.Values{tokenParameter: []string{want.queryToken}}.Encode()
	}

	r, err := http.NewRequestWithContext(t.Context(), method, target, nil)
	if err != nil {
		t.Fatalf("build a %s %s request: %v", method, path, err)
	}
	switch {
	case want.authorization != nil:
		for _, value := range want.authorization {
			r.Header.Add(authorizationHeader, value)
		}
	case want.token != "":
		r.Header.Set(authorizationHeader, "Bearer "+want.token)
	}
	for _, origin := range want.origins {
		r.Header.Add("Origin", origin)
	}

	answer, err := ts.client.Do(r)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = answer.Body.Close() }()
	body, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("read the body of %s %s: %v", method, path, err)
	}

	lines := make([]string, 0, len(answer.Header))
	for name, values := range answer.Header {
		if name == "Date" {
			continue
		}
		lines = append(lines, name+": "+strings.Join(values, ", "))
	}
	sort.Strings(lines)
	return response{Status: answer.StatusCode, Headers: strings.Join(lines, "\n"), Body: string(body)}
}

// allowed is a request that passes both checks: the right token and the
// server's own origin.
func (ts *testServer) allowed(path string) request {
	return request{path: path, token: ts.Token().Secret(), origins: []string{ts.URL()}}
}

// methodAndPathOf splits a net/http route pattern into the two things a test
// needs to call it.
func methodAndPathOf(t *testing.T, pattern string) (method, path string) {
	t.Helper()
	method, path, found := strings.Cut(pattern, " ")
	if !found {
		t.Fatalf("the route pattern %q names no method, so this test cannot call it", pattern)
	}
	return method, path
}

// discardLogger is the logger for a guard built outside startServer: a guard
// has to have one, and these tests assert on responses, not on the log.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// itoa keeps the origin tables readable.
func itoa(value int) string {
	return strconv.Itoa(value)
}
