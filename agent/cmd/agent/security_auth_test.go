//go:build unix

// Who may call (#4, #5, #58): the token, the origin, CSRF and DNS rebinding,
// over HTTP and as WebSocket handshakes on the channel, against every route
// the agent registers and paths none registers.

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// expectRejected fails t unless got is the uniform rejection.
func (f *fixture) expectRejected(t *testing.T, what string, got answer) {
	t.Helper()
	want := f.unauthorized
	if got.method == "HEAD" {
		want = f.unauthorizedHead
	}
	if difference := sameAnswer(want, got); difference != "" {
		t.Errorf("%s: %s %s is not the uniform rejection: %s", what, got.method, shortPath(got.target), difference)
	}
}

// shape is one way of presenting (or failing to present) the token and the
// origin.
type shape struct {
	name   string
	header http.Header
	// query is added to the target's query.
	query string
	// body replaces the route's body, or is sent where it takes none.
	body []byte
	// issue is the issue a shape that fails against the shipped controls
	// was filed as; the shape is skipped naming it.
	issue int
}

// unauthorizedShapes is every shape that must be rejected, against an agent
// whose token is token and whose origin is origin.
func (f *fixture) unauthorizedShapes() []shape {
	token, origin, port := f.a.token, f.a.origin, f.a.port
	offByOne := token[:len(token)-1] + string(rune(token[len(token)-1])^1)
	wrong := strings.Repeat("A", len(token))
	bearer := func(value string) http.Header {
		return http.Header{"Authorization": {"Bearer " + value}, "Origin": {origin}}
	}
	withOrigin := func(value string) http.Header {
		return http.Header{"Authorization": {"Bearer " + token}, "Origin": {value}}
	}
	return []shape{
		{name: "no token", header: http.Header{"Origin": {origin}}},
		{name: "a wrong token of the same length", header: bearer(wrong)},
		{name: "the token one byte off", header: bearer(offByOne)},
		{name: "the token cut short", header: bearer(token[:len(token)-1])},
		{name: "the token cut short in the query", header: http.Header{"Origin": {origin}}, query: "token=" + token[:len(token)-8]},
		{name: "the token in a Cookie", header: http.Header{"Origin": {origin}, "Cookie": {"token=" + token}}},
		{name: "the token as a bare Cookie", header: http.Header{"Origin": {origin}, "Cookie": {token}}},
		{name: "the token in X-Token", header: http.Header{"Origin": {origin}, "X-Token": {token}}},
		{name: "the token in Sec-WebSocket-Protocol", header: http.Header{"Origin": {origin}, "Sec-Websocket-Protocol": {token}}},
		{name: "the token as a non-bearer Authorization", header: http.Header{"Origin": {origin}, "Authorization": {token}}},
		{name: "the token in the body", header: http.Header{"Origin": {origin}, "Content-Type": {"application/json"}}, body: []byte(`{"token":"` + token + `"}`)},
		{name: "the token in another query parameter", header: http.Header{"Origin": {origin}}, query: "access_token=" + token},
		{name: "the token in a query parameter in another case", header: http.Header{"Origin": {origin}}, query: "Token=" + token},
		{name: "another site's origin", header: withOrigin("http://evil.example")},
		{name: "Origin null", header: withOrigin("null")},
		{name: "the agent's address on another port", header: withOrigin(fmt.Sprintf("http://127.0.0.1:%d", otherPort(port)))},
		{name: "localhost for 127.0.0.1", header: withOrigin("http://localhost:" + port), issue: 102},
		{name: "https for http", header: withOrigin("https://127.0.0.1:" + port)},
		{name: "another site's origin with the token in the query", header: http.Header{"Origin": {"http://evil.example"}}, query: "token=" + token},
	}
}

func otherPort(port string) int {
	var number int
	_, _ = fmt.Sscan(port, &number)
	if number == 65535 {
		return 1024
	}
	return number + 1
}

// validRequest is the target and body of a request the route serves.
func (f *fixture) validRequest(t *testing.T, route attackRoute) (string, []byte) {
	t.Helper()
	return route.build(t, route.row.valid(f))
}

func addQuery(target, query string) string {
	if query == "" {
		return target
	}
	if strings.Contains(target, "?") {
		return target + "&" + query
	}
	return target + "?" + query
}

// whoMayCall is the authorization corpus. The whole scratch directory is
// compared byte for byte before and after it.
func (f *fixture) whoMayCall(t *testing.T) {
	before := snapshot(t, f.s.base)
	defer unchanged(t, "the authorization corpus", before, f.s.base)

	for _, sh := range f.unauthorizedShapes() {
		t.Run(sh.name, func(t *testing.T) {
			if sh.issue != 0 {
				skipForIssue(t, sh.issue, "the agent accepts this shape")
			}
			f.everyRouteRejects(t, sh)
		})
	}

	t.Run("CSRF", f.csrf)
	t.Run("Sec-Fetch-Site", f.secFetchSite)
	t.Run("DNS rebinding", f.rebinding)
}

// everyRouteRejects sends sh to every route with its own method.
func (f *fixture) everyRouteRejects(t *testing.T, sh shape) {
	for _, route := range f.routes {
		target, body := f.validRequest(t, route)
		{
			sent := body
			if sh.body != nil {
				sent = sh.body
			}
			header := sh.header.Clone()
			if route.takesBody() && header.Get("Content-Type") == "" {
				header.Set("Content-Type", "application/json")
			}
			what := route.pattern + ", " + sh.name
			if route.row.channel {
				f.expectHandshakeRejected(t, what, addQuery(target, sh.query), header, "")
				continue
			}

			f.expectRejected(t, what, f.a.send(request{method: route.method, target: addQuery(target, sh.query), body: sent, header: header}))
		}
	}
}

// formBodies are the bodies an HTML form can send without a preflight.
var formBodies = []struct{ contentType, body string }{
	{"application/x-www-form-urlencoded", "path=%2F&name=x"},
	{"multipart/form-data; boundary=kpv", "--kpv\r\nContent-Disposition: form-data; name=\"path\"\r\n\r\n/\r\n--kpv--\r\n"},
	{"text/plain", `{"path":"/"}`},
}

func (f *fixture) csrf(t *testing.T) {
	for _, route := range f.routes {
		if !route.changesState() {
			continue
		}
		target, _ := f.validRequest(t, route)
		for _, form := range formBodies {
			for _, origin := range []string{"http://evil.example", "null"} {
				for _, method := range uniqueMethods(route.method, "POST") {
					header := http.Header{"Origin": {origin}, "Content-Type": {form.contentType}}
					f.expectRejected(t, route.pattern+" as a form from "+origin, f.a.send(request{method: method, target: target, body: []byte(form.body), header: header}))
				}
			}
			header := f.a.authorized()
			header.Set("Content-Type", form.contentType)
			got := f.a.send(request{method: route.method, target: target, body: []byte(form.body), header: header})
			if kind := errorKind(got); got.status != http.StatusBadRequest || kind != "invalid" {
				t.Errorf("%s with the token and %s answered %d %s, want the route's invalid", route.pattern, form.contentType, got.status, got.body)
			}
		}
	}
}

func uniqueMethods(methods ...string) []string {
	var out []string
	for _, m := range methods {
		if !contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// fetchSites is every Sec-Fetch-Site value, and two values that are not one.
var fetchSites = []string{"same-origin", "none", "same-site", "cross-site", "", "same-origin, none"}

func (f *fixture) secFetchSite(t *testing.T) {
	for _, route := range f.routes {
		if !route.changesState() && !route.row.channel {
			continue
		}
		target, body := f.validRequest(t, route)
		for _, site := range fetchSites {
			header := http.Header{"Authorization": {"Bearer " + f.a.token}, "Sec-Fetch-Site": {site}}
			what := route.pattern + " with the token, no Origin and Sec-Fetch-Site " + site
			if route.row.channel {
				f.expectHandshakeRejected(t, what, addQuery(target, "token="+f.a.token), header, "")
				continue
			}
			header.Set("Content-Type", "application/json")
			f.expectRejected(t, what, f.a.send(request{method: route.method, target: target, body: body, header: header}))
		}
	}
}

func (f *fixture) rebinding(t *testing.T) {
	host := "rebind.example:" + f.a.port
	origin := "http://" + host
	for _, route := range f.routes {
		target, body := f.validRequest(t, route)
		withToken := func(header http.Header) http.Header {
			header = header.Clone()
			header.Set("Authorization", "Bearer "+f.a.token)
			return header
		}
		page := http.Header{"Origin": {origin}}
		if route.takesBody() {
			page.Set("Content-Type", "application/json")
		}
		if route.row.channel {
			f.expectHandshakeRejected(t, "the channel from the rebinding page", target, page, host)
			f.expectHandshakeRejected(t, "the channel from the rebinding page with the token", target, withToken(page), host)
			f.expectHandshakeRejected(t, "the channel from the rebinding page with the token in the query", addQuery(target, "token="+f.a.token), page, host)
			continue
		}
		f.expectRejected(t, route.pattern+" from the rebinding page", f.a.send(request{method: route.method, target: target, body: body, header: page, host: host}))
		f.expectRejected(t, route.pattern+" from the rebinding page with the token", f.a.send(request{method: route.method, target: target, body: body, header: withToken(page), host: host}))
		if route.method == "GET" {
			// No Origin with Sec-Fetch-Site: same-origin, which a GET or HEAD
			// from the rebinding page sends. Without the token this is
			// rejected; with it, whether Host is checked is #101's.
			sameOrigin := http.Header{"Sec-Fetch-Site": {"same-origin"}}
			for _, method := range []string{"GET", "HEAD"} {
				f.expectRejected(t, route.pattern+" from the rebinding page with no Origin", f.a.send(request{method: method, target: target, header: sameOrigin, host: host}))
			}
		}
	}
}

// expectHandshakeRejected sends a WebSocket handshake on a socket of its
// own and fails t unless it gets the uniform 404, never 101, and unless
// nothing but HTTP follows on that connection: a second, unauthorized
// request is sent on it, and what comes back must parse as that request's
// rejection, or the connection is closed. A frame delivered after the 404
// would be bytes before that answer.
func (f *fixture) expectHandshakeRejected(t *testing.T, what, target string, header http.Header, host string) {
	t.Helper()
	if host == "" {
		host = f.a.addr
	}
	connection, err := net.DialTimeout("tcp", f.a.addr, answerTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(answerTimeout))
	var wire bytes.Buffer
	fmt.Fprintf(&wire, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n", target, host)
	for name, values := range header {
		for _, value := range values {
			fmt.Fprintf(&wire, "%s: %s\r\n", name, value)
		}
	}
	wire.WriteString("\r\n")
	if _, err := connection.Write(wire.Bytes()); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(connection)
	got := readRawAnswer(t, reader, "GET", target)
	if got.status == http.StatusSwitchingProtocols {
		t.Errorf("%s: the handshake was accepted", what)
		return
	}
	f.expectRejected(t, what, got)
	f.a.record(got)
	// The follow-up request on the same connection.
	if _, err := connection.Write([]byte("GET /kpv-security-no-such-path HTTP/1.1\r\nHost: " + f.a.addr + "\r\nConnection: close\r\n\r\n")); err != nil {
		return // closed after the rejection: nothing was delivered
	}
	if _, err := reader.Peek(1); err != nil {
		return // closed: nothing was delivered
	}
	follow := readRawAnswer(t, reader, "GET", "/kpv-security-no-such-path")
	f.expectRejected(t, what+", then a request on the same connection", follow)
	if rest, _ := io.ReadAll(reader); len(rest) != 0 {
		t.Errorf("%s: %d bytes followed the rejections on the connection", what, len(rest))
	}
}

// readRawAnswer parses one HTTP answer off a raw connection.
func readRawAnswer(t *testing.T, reader *bufio.Reader, method, target string) answer {
	t.Helper()
	built, _ := http.NewRequest(method, "http://x/", nil)
	response, err := http.ReadResponse(reader, built)
	if err != nil {
		t.Fatalf("%s %s: read the answer: %v", method, target, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusSwitchingProtocols {
		return answer{method: method, target: target, status: response.StatusCode, header: response.Header}
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("%s %s: read the body: %v", method, target, err)
	}
	return answer{method: method, target: target, status: response.StatusCode, header: response.Header, body: body}
}

// unregisteredPaths probes paths no route registers: without the token
// each is the uniform rejection; with it none answers 2xx, and a redirect
// net/http's path cleaning makes goes to the same origin.
func (f *fixture) unregisteredPaths(t *testing.T) {
	before := snapshot(t, f.s.base)
	defer unchanged(t, "the unregistered path probes", before, f.s.base)
	type probe struct{ method, target string }
	probes := []probe{{"GET", "/"}, {"GET", "/api"}, {"GET", "/api/"}, {"GET", "/api/x"}, {"POST", "/api/x"}}
	for _, route := range f.routes {
		path := route.path
		for _, variant := range []string{path + "/", path + "/.", path + "/..", path + "%2e", path + "/%2e", path + "/%2e%2e",
			"/" + path, strings.Replace(path, "/", "//", 2), path + "//", strings.ToUpper(path), strings.ToUpper(path[:5]) + path[5:]} {
			probes = append(probes, probe{route.method, variant})
			if route.method != "GET" {
				probes = append(probes, probe{"GET", variant})
			}
		}
	}
	for _, p := range probes {
		f.expectRejected(t, "an unregistered path", f.a.send(request{method: p.method, target: p.target}))
		f.expectRejected(t, "an unregistered path with the token in the query and no Origin", f.a.send(request{method: p.method, target: addQuery(p.target, "token="+f.a.token)}))
		f.notServed(t, p.method, p.target, f.a.authorized())
	}
	// The startup URL's way of presenting the token, on the same paths. A
	// redirect net/http's path cleaning makes keeps the query, so its
	// Location would carry the token.
	t.Run("with the token in the query", func(t *testing.T) {
		skipForIssue(t, 103, "a redirect from path cleaning answers the token in Location")
		for _, p := range probes {
			f.notServed(t, p.method, addQuery(p.target, "token="+f.a.token), http.Header{"Origin": {f.a.origin}})
		}
	})
}

// notServed fails t when an authorized request to an unregistered path
// answers 2xx, or redirects anywhere but the agent's own origin. net/http's
// path cleaning redirects "/api/health/.." to "/api" and "//api//roots" to
// "/roots"; that is routing, and the next request is checked like any
// other.
func (f *fixture) notServed(t *testing.T, method, target string, header http.Header) {
	t.Helper()
	got := f.a.send(request{method: method, target: target, header: header})
	switch {
	case got.status >= 200 && got.status < 300:
		t.Errorf("%s %s with the token answered %d %s", method, target, got.status, got.body)
	case got.status >= 300 && got.status < 400:
		location := got.header.Get("Location")
		path := strings.TrimPrefix(location, f.a.origin)
		if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
			t.Errorf("%s %s redirected to %q, not the agent's own origin", method, target, location)
			return
		}
		if strings.Contains(location, f.a.token) {
			t.Errorf("%s %s redirected with the token in Location", method, target)
		}
	}
}

// errorKind is the error field of a JSON answer, or "".
func errorKind(got answer) string {
	var body struct {
		Error string `json:"error"`
	}
	if err := jsonUnmarshal(got.body, &body); err != nil {
		return ""
	}
	return body.Error
}
