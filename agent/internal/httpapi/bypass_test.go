// Requests that do not look like requests, sent as bytes on a socket rather
// than built by net/http's client.
//
// This file exists because of a real defect: net/http answered `OPTIONS *`
// out of its own built-in handler, before Server.Handler was consulted, so an
// unauthorized caller got a 200 where everything else got the uniform 404.
// The guard was not at fault; the server it was installed on served one
// request shape ahead of it. That is a class, not an instance, so the test
// below is about the class: whatever a client puts on the wire, nothing
// without the token gets a 2xx.

package httpapi

import (
	"bufio"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rawRequest sends exactly these bytes to the server and returns the status
// line and the body, or "" when the server answered nothing at all. Every
// request below asks for the connection to be closed, so the read ends at EOF
// and nothing waits on a clock.
func rawRequest(t *testing.T, address, wire string) (status, body string) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, dialTimeout)
	if err != nil {
		t.Fatalf("connect to the agent API: %v", err)
	}
	defer func() { _ = connection.Close() }()
	if err := connection.SetDeadline(time.Now().Add(dialTimeout)); err != nil {
		t.Fatalf("set a deadline: %v", err)
	}
	if _, err := connection.Write([]byte(wire)); err != nil {
		t.Fatalf("write the request: %v", err)
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
	whole := answer.String()
	if whole == "" {
		return "", ""
	}
	head, rest, _ := strings.Cut(whole, "\r\n\r\n")
	status, _, _ = strings.Cut(head, "\r\n")
	return status, rest
}

// statusCodeOf pulls the three digits out of a status line.
func statusCodeOf(t *testing.T, status string) int {
	t.Helper()
	if status == "" {
		// No answer at all is not a 2xx, which is all this file asserts.
		return 0
	}
	fields := strings.Fields(status)
	if len(fields) < 2 {
		t.Fatalf("the status line %q has no code in it", status)
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("the status line %q has no numeric code: %v", status, err)
	}
	return code
}

// The instance: OPTIONS * is now the guard's to answer, with the same bytes
// every other rejection gets.
func TestTheAsteriskFormRequestIsRejectedLikeEverythingElse(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})

	status, body := rawRequest(t, server.Addr(), "OPTIONS * HTTP/1.1\r\nHost: "+server.Addr()+"\r\nConnection: close\r\n\r\n")
	if code := statusCodeOf(t, status); code != rejectionStatus {
		t.Fatalf("OPTIONS * answered %q with body %q, want the uniform %d; net/http's own globalOptionsHandler is answering ahead of the guard", status, body, rejectionStatus)
	}
	if body != rejectionBody {
		t.Fatalf("OPTIONS * answered the body %q, want the uniform %q", body, rejectionBody)
	}
}

// The class: no request shape, however odd, gets a 2xx without the token.
// Each case is raw bytes, because a client library normalizes away exactly
// the shapes worth trying.
func TestNoRequestShapeGetsASuccessWithoutTheToken(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	address := server.Addr()
	own := server.URL()
	secret := server.Token().Secret()

	wire := map[string]string{
		"the asterisk form":                "OPTIONS * HTTP/1.1\r\nHost: " + address + "\r\nConnection: close\r\n\r\n",
		"the asterisk form with an origin": "OPTIONS * HTTP/1.1\r\nHost: " + address + "\r\nOrigin: " + own + "\r\nConnection: close\r\n\r\n",
		"the asterisk form with the token": "OPTIONS * HTTP/1.1\r\nHost: " + address + "\r\nAuthorization: Bearer " + secret + "\r\nConnection: close\r\n\r\n",
		"a preflight":                      "OPTIONS /api/health HTTP/1.1\r\nHost: " + address + "\r\nOrigin: " + own + "\r\nAccess-Control-Request-Method: GET\r\nAccess-Control-Request-Headers: authorization\r\nConnection: close\r\n\r\n",
		"the absolute form":                "GET " + own + "/api/health HTTP/1.1\r\nHost: " + address + "\r\nConnection: close\r\n\r\n",
		"a CONNECT":                        "CONNECT " + address + " HTTP/1.1\r\nHost: " + address + "\r\nConnection: close\r\n\r\n",
		"a TRACE":                          "TRACE /api/health HTTP/1.1\r\nHost: " + address + "\r\nConnection: close\r\n\r\n",
		"a HEAD":                           "HEAD /api/health HTTP/1.1\r\nHost: " + address + "\r\nConnection: close\r\n\r\n",
		"HTTP/1.0 with no Host":            "GET /api/health HTTP/1.0\r\nConnection: close\r\n\r\n",
		"an empty Host":                    "GET /api/health HTTP/1.1\r\nHost: \r\nConnection: close\r\n\r\n",
		"someone else's Host":              "GET /api/health HTTP/1.1\r\nHost: evil.example\r\nConnection: close\r\n\r\n",
		"a doubled slash":                  "GET //api/health HTTP/1.1\r\nHost: " + address + "\r\nConnection: close\r\n\r\n",
		"a traversal":                      "GET /x/../api/health HTTP/1.1\r\nHost: " + address + "\r\nConnection: close\r\n\r\n",
		"an encoded slash":                 "GET /api%2fhealth HTTP/1.1\r\nHost: " + address + "\r\nConnection: close\r\n\r\n",
		"a trailing slash":                 "GET /api/health/ HTTP/1.1\r\nHost: " + address + "\r\nConnection: close\r\n\r\n",
		"a path parameter":                 "GET /api/health;token=" + secret + " HTTP/1.1\r\nHost: " + address + "\r\nConnection: close\r\n\r\n",
		"an unsupported expectation":       "GET /api/health HTTP/1.1\r\nHost: " + address + "\r\nExpect: the-moon\r\nConnection: close\r\n\r\n",
		"a continue expectation":           "POST /api/health HTTP/1.1\r\nHost: " + address + "\r\nExpect: 100-continue\r\nContent-Length: 4\r\nConnection: close\r\n\r\nbody",
		"the token in a cookie":            "GET /api/health HTTP/1.1\r\nHost: " + address + "\r\nCookie: token=" + secret + "\r\nOrigin: " + own + "\r\nConnection: close\r\n\r\n",
		"the token in another header":      "GET /api/health HTTP/1.1\r\nHost: " + address + "\r\nX-Token: " + secret + "\r\nOrigin: " + own + "\r\nConnection: close\r\n\r\n",
		"the token in a fragment":          "GET /api/health#token=" + secret + " HTTP/1.1\r\nHost: " + address + "\r\nOrigin: " + own + "\r\nConnection: close\r\n\r\n",
		"the query parameter in caps":      "GET /api/health?TOKEN=" + secret + " HTTP/1.1\r\nHost: " + address + "\r\nOrigin: " + own + "\r\nConnection: close\r\n\r\n",
		"a smuggled second request":        "GET /api/health HTTP/1.1\r\nHost: " + address + "\r\nContent-Length: 0\r\nConnection: close\r\n\r\nGET /api/version?" + tokenParameter + "=" + secret + " HTTP/1.1\r\nHost: " + address + "\r\n\r\n",
	}

	for name, bytes := range wire {
		status, body := rawRequest(t, address, bytes)
		code := statusCodeOf(t, status)
		if code >= 200 && code < 300 {
			t.Errorf("%s was answered %q with body %q, a success without the token", name, status, body)
		}
		if strings.Contains(body, secret) {
			t.Errorf("%s was answered a body carrying the token: %q", name, body)
		}
	}

	// And the control: the same socket, used properly, does succeed, so the
	// rejections above are not a server that answers nothing at all.
	status, body := rawRequest(t, address, "GET /api/health HTTP/1.1\r\nHost: "+address+"\r\nOrigin: "+own+"\r\nAuthorization: Bearer "+secret+"\r\nConnection: close\r\n\r\n")
	if code := statusCodeOf(t, status); code != http.StatusOK {
		t.Fatalf("a proper request over a raw socket answered %q, want 200: %q", status, body)
	}
}
