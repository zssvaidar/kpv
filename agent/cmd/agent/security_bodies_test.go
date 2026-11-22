//go:build unix

// Bodies and headers: every body-taking route gets one corpus of bodies
// that must be refused without being acted on, the header cap and the slow
// header timeout are held, and the agent is still responsive afterwards.

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// bodyCase is one body every body-taking route gets.
type bodyCase struct {
	name        string
	body        []byte
	contentType string
	chunked     bool
	// tooLarge is a source text over 1 MiB, which PUT /api/pipelines/source
	// answers tooLarge rather than invalid.
	tooLarge bool
}

// bodyCorpus builds the corpus for route around its valid body. marker is
// put in every body so the log can be searched for it.
func (f *fixture) bodyCorpus(t *testing.T, route attackRoute, marker string) []bodyCase {
	_, valid := route.build(t, route.row.valid(f))
	first := route.row.body[0]
	text := route.row.text
	// substituted is the valid body with text's value replaced by raw, which
	// is written into the JSON as it is.
	const placeholder = "KPVPLACEHOLDER"
	_, template := route.build(t, route.with(f, text, placeholder))
	substituted := func(raw string) []byte {
		return bytes.Replace(template, []byte(placeholder), []byte(raw), 1)
	}
	over := substituted(marker + strings.Repeat("a", route.row.cap+1-len(template)+len(placeholder)-len(marker)))
	inner := valid[1 : len(valid)-1]
	field := func(json string) []byte { return []byte("{" + json + "," + string(inner) + "}") }
	cases := []bodyCase{
		{name: "one byte over the cap", body: over},
		{name: "one byte over the cap, chunked", body: over, chunked: true},
		{name: "empty", body: []byte{}},
		{name: "null", body: []byte("null")},
		{name: "an array", body: []byte(`["` + marker + `"]`)},
		{name: "a string", body: []byte(`"` + marker + `"`)},
		{name: "a number", body: []byte("12345")},
		{name: "an array nested 100000 deep", body: []byte(strings.Repeat("[", 100000) + strings.Repeat("]", 100000))},
		{name: "a field nested 100000 deep", body: []byte(`{"` + first + `":` + strings.Repeat("[", 100000) + strings.Repeat("]", 100000) + `}`)},
		{name: "a key twice", body: field(`"` + first + `":"` + marker + `"`)},
		{name: "a key differing only in case", body: field(`"` + strings.ToUpper(first[:1]) + first[1:] + `":"` + marker + `"`)},
		{name: "an unknown key", body: field(`"kpvUnknown` + marker + `":"` + marker + `"`)},
		{name: "invalid UTF-8", body: substituted(marker + "\xff\xfe")},
		{name: "a lone \\ud800", body: substituted(marker + `\ud800`)},
		{name: "a NUL in a string", body: substituted(marker + `\u0000`)},
		{name: "bytes after the object", body: append(append([]byte(nil), valid...), []byte(` {"`+marker+`":1}`)...)},
		{name: "text/plain", body: valid, contentType: "text/plain"},
		{name: "application/json with another charset", body: valid, contentType: "application/json; charset=latin1"},
		{name: "application/x-www-form-urlencoded", body: valid, contentType: "application/x-www-form-urlencoded"},
		{name: "no Content-Type", body: valid, contentType: "-"},
	}
	if len(over) != route.row.cap+1 {
		t.Fatalf("%s: the over-the-cap body is %d bytes, want %d", route.pattern, len(over), route.row.cap+1)
	}
	if route.pattern == "PUT /api/pipelines/source" {
		big := map[string]any{"root": f.root, "directory": f.directory, "path": "src/huge.go",
			"text": marker + strings.Repeat("a", 1<<20), "expectedAbsent": true}
		_, body := route.build(t, big)
		cases = append(cases, bodyCase{name: "a text over 1 MiB", body: body, tooLarge: true})
	}
	return cases
}

func quote(text string) string {
	var out bytes.Buffer
	fmt.Fprintf(&out, "%q", text)
	return out.String()
}

// invalidAnswer is what the route answers a body it will not reason about:
// the answer to text/plain, whose sentence every case must match.
func (f *fixture) invalidAnswer(t *testing.T, route attackRoute) answer {
	t.Helper()
	target, body := route.build(t, route.row.valid(f))
	header := f.a.authorized()
	header.Set("Content-Type", "text/plain")
	got := f.a.send(request{method: route.method, target: target, body: body, header: header})
	if got.status != http.StatusBadRequest || errorKind(got) != "invalid" {
		t.Fatalf("%s answered text/plain with %d %s, not invalid", route.pattern, got.status, got.body)
	}
	return got
}

func messageOf(got answer) string {
	var body struct {
		Message string `json:"message"`
	}
	_ = jsonUnmarshal(got.body, &body)
	return body.Message
}

func (f *fixture) bodiesAndHeaders(t *testing.T) {
	fdBefore := 0
	if runtime.GOOS == "linux" {
		fdBefore = openDescriptors(t, f.a.pid())
	}
	before := snapshot(t, f.s.base, f.s.markers)
	for _, route := range f.routes {
		if !route.takesBody() {
			continue
		}
		marker := randomText(t, "BODYMARK")
		invalid := f.invalidAnswer(t, route)
		target, _ := route.build(t, route.row.valid(f))
		for _, c := range f.bodyCorpus(t, route, marker) {
			if issue, failing := bodyIssues[route.pattern+" "+c.name]; failing {
				t.Run(route.pattern+" with a body "+c.name, func(t *testing.T) {
					skipForIssue(t, issue, "the route accepts this body")
				})
				continue
			}
			header := f.a.authorized()
			switch c.contentType {
			case "":
				header.Set("Content-Type", "application/json")
			case "-":
			default:
				header.Set("Content-Type", c.contentType)
			}
			logged := len(f.a.stderr.Bytes())
			got := f.a.send(request{method: route.method, target: target, body: c.body, header: header, chunked: c.chunked})
			what := route.pattern + " with a body " + c.name
			switch {
			case c.tooLarge:
				if got.status != http.StatusRequestEntityTooLarge || errorKind(got) != "tooLarge" {
					t.Errorf("%s answered %d %s, want tooLarge", what, got.status, shortPath(string(got.body)))
				}
			case got.status != http.StatusBadRequest || errorKind(got) != "invalid" || messageOf(got) != messageOf(invalid):
				t.Errorf("%s answered %d %s, want invalid with %q", what, got.status, shortPath(string(got.body)), messageOf(invalid))
			}
			if strings.Contains(got.wire(), marker) {
				t.Errorf("%s answered a byte of the body", what)
			}
			if strings.Contains(string(f.a.stderr.Bytes()[logged:]), marker) {
				t.Errorf("%s logged a byte of the body", what)
			}
		}
		t.Run(route.pattern+" with a body that never ends", func(t *testing.T) {
			if issue, failing := neverEndingBodyIssues[route.pattern]; failing {
				skipForIssue(t, issue, "net/http reads up to 256 KiB more of an unread body before it answers")
			}
			f.neverEndingBody(t, route, target)
		})
	}
	unchanged(t, "the body corpus", before, f.s.base, f.s.markers)

	t.Run("headers over the cap", f.headersOverTheCap)
	t.Run("headers slower than the timeout", f.slowHeaders)

	start := time.Now()
	health := f.a.send(request{method: "GET", target: "/api/health", header: f.a.authorized()})
	if health.status != http.StatusOK || time.Since(start) > time.Second {
		t.Errorf("after the body and header corpus /api/health answered %d in %s", health.status, time.Since(start))
	}
	if runtime.GOOS == "linux" {
		f.a.client.CloseIdleConnections()
		if after := openDescriptors(t, f.a.pid()); after > fdBefore+10 {
			t.Errorf("the agent has %d open descriptors after the corpus, %d before", after, fdBefore)
		}
	}
}

// bodyIssues are the bodies a route accepts against the shipped controls.
var bodyIssues = map[string]int{
	"POST /api/pipelines a lone \\ud800":    106,
	"POST /api/pipelines a NUL in a string": 106,
}

// neverEndingBodyIssues are the routes whose answer to a body that never
// ends waits for more than twice their cap.
var neverEndingBodyIssues = map[string]int{
	"POST /api/roots":     104,
	"DELETE /api/roots":   104,
	"POST /api/pipelines": 104,
	// The working-copy routes (#43), 8192 bytes each.
	"POST /api/workcopies/apply":      104,
	"POST /api/workcopies/restore":    104,
	"POST /api/workcopies/discard":    104,
	"DELETE /api/workcopies/snapshot": 104,
}

// neverEndingBody sends a chunked body that never ends: twice the cap and
// then nothing more, and the answer or the connection closing must arrive
// without more being sent. The read is bounded well inside readTimeout, so
// a server waiting for the rest is a failure, not a pass when it gives up.
func (f *fixture) neverEndingBody(t *testing.T, route attackRoute, target string) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", f.a.addr, answerTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	head := fmt.Sprintf("%s %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nOrigin: %s\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n",
		route.method, target, f.a.addr, f.a.token, f.a.origin)
	if _, err := connection.Write([]byte(head)); err != nil {
		t.Fatal(err)
	}
	answered := make(chan answer, 1)
	go func() {
		_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
		reader := bufio.NewReader(connection)
		built, _ := http.NewRequest(route.method, "http://x/", nil)
		response, err := http.ReadResponse(reader, built)
		if err != nil {
			answered <- answer{status: -1}
			return
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		answered <- answer{method: route.method, target: target, status: response.StatusCode, header: response.Header, body: body}
	}()
	chunk := []byte(`{"` + route.row.body[0] + `":"` + strings.Repeat("a", 4000))
	sent := 0
	for sent < 2*route.row.cap {
		if _, err := fmt.Fprintf(connection, "%x\r\n%s\r\n", len(chunk), chunk); err != nil {
			break // closed by the agent, which is an answer too
		}
		sent += len(chunk)
		chunk = bytes.Repeat([]byte("a"), 4000)
	}
	got := <-answered
	switch {
	case got.status == -1:
		// The connection was closed or the deadline passed with no answer.
		// A deadline is the agent waiting for more than twice its cap.
		if ok, ne := connectionTimedOut(connection); ok {
			t.Errorf("%s with a body that never ends: no answer after %d bytes (%v)", route.pattern, sent, ne)
		}
	case got.status != http.StatusBadRequest || errorKind(got) != "invalid":
		t.Errorf("%s with a body that never ends answered %d %s", route.pattern, got.status, got.body)
	default:
		f.a.record(got)
	}
}

// connectionTimedOut tells a connection the agent closed from one whose
// read deadline passed: a closed one reads EOF at once.
func connectionTimedOut(connection net.Conn) (bool, error) {
	_ = connection.SetReadDeadline(time.Now().Add(time.Millisecond))
	_, err := connection.Read(make([]byte, 1))
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true, err
	}
	return false, nil
}

// headersOverTheCap sends an authorize the agent would act on with headers
// over maxHeaderBytes (64 KiB, server.go).
func (f *fixture) headersOverTheCap(t *testing.T) {
	target := filepath.Join(f.s.home, "projects", "delta")
	mustMkdir(t, target)
	before := snapshot(t, f.s.settings)
	for _, size := range []int{64<<10 + 1, 64<<10 + 4096, 128 << 10} {
		t.Run(fmt.Sprintf("%d bytes", size), func(t *testing.T) {
			if size < 72<<10 {
				skipForIssue(t, 105, "net/http allows 4096 bytes of slack over MaxHeaderBytes and buffers more")
			}
			f.headerOverTheCap(t, target, size)
		})
	}
	unchanged(t, "requests with headers over the cap", before, f.s.settings)
	f.restoreRoots(t)
	f.call(t, "DELETE", "/api/roots", map[string]any{"path": target})
}

// headerOverTheCap sends one authorize with size bytes of one header.
func (f *fixture) headerOverTheCap(t *testing.T, target string, size int) {
	{
		header := f.a.authorized()
		header.Set("Content-Type", "application/json")
		header.Set("X-Padding", strings.Repeat("p", size))
		got, err := f.a.trySend(request{method: "POST", target: "/api/roots", body: []byte(`{"path":` + quote(target) + `}`), header: header})
		if err != nil {
			t.Errorf("headers of %d bytes: %v", size, err)
			return
		}
		if got.status != http.StatusRequestHeaderFieldsTooLarge {
			t.Errorf("headers of %d bytes answered %d %s, want 431", size, got.status, got.body)
		}
	}
}

// slowHeaders opens a connection, sends part of a request's headers and
// stops: the agent closes it after readHeaderTimeout (5 seconds).
func (f *fixture) slowHeaders(t *testing.T) {
	connection, err := net.DialTimeout("tcp", f.a.addr, answerTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	if _, err := fmt.Fprintf(connection, "GET /api/health HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\n", f.a.addr, f.a.token); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_ = connection.SetReadDeadline(start.Add(15 * time.Second))
	read, err := io.ReadAll(connection)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("a client sending its headers slower than readHeaderTimeout was still connected after %s", time.Since(start))
	}
	if bytes.Contains(read, []byte(" 200 ")) {
		t.Errorf("a request whose headers never ended was answered: %q", read)
	}
}

// openDescriptors counts /proc/<pid>/fd.
func openDescriptors(t *testing.T, pid int) int {
	t.Helper()
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		t.Fatalf("read the agent's descriptors: %v", err)
	}
	return len(entries)
}
