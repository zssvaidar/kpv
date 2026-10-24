// The rule for a request that carries no Origin header: what is accepted, and
// everything near it that is not.
//
// THE PIN, and how to re-run it. The first test below asserted the opposite of
// what it asserts now. At commit fdd9719, before the rule changed, it required
// the page's own fetch -- a GET with the session token, no Origin, and the
// fetch metadata a browser attaches -- to be rejected, and it passed. Against
// the fixed code that same test fails, on both routes:
//
//	the page's own fetch of /api/health was answered 200 "{\"status\":\"ok\"}\n",
//	want the uniform rejection; the gap this task closes is no longer there to close
//
// which is what makes this a fix rather than a claim. `git show fdd9719` is the
// pinned version.
//
// Nothing here is a wish about browsers. Every case is a request shape the
// Fetch standard says a browser really sends, or raw bytes a hostile client
// really can send.

package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// pageFetch is the request the agent's own page makes once it is served from
// the agent's own origin: the token in the Authorization header, no Origin
// header, and the fetch metadata the browser attaches and the page cannot.
func (ts *testServer) pageFetch(path string) request {
	return request{
		path:    path,
		token:   ts.Token().Secret(),
		headers: http.Header{secFetchSiteHeader: {"same-origin"}},
	}
}

// The gap, closed: the page's own fetch is answered on every route the agent
// serves, with no Origin header on the request.
func TestThePageOwnFetchIsAnsweredWithNoOrigin(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})

	routes := apiRoutes()
	if len(routes) == 0 {
		t.Fatal("there are no routes, so this test proves nothing")
	}
	for _, r := range routes {
		_, path := methodAndPathOf(t, r.pattern)
		answer := server.do(t, server.pageFetch(path))
		if answer.Status != http.StatusOK {
			t.Errorf("the page's own fetch of %s was answered %d, want 200: %s", path, answer.Status, answer)
			continue
		}
		var body map[string]string
		if err := json.Unmarshal([]byte(answer.Body), &body); err != nil {
			t.Errorf("the page's own fetch of %s answered %q, which is not JSON: %v", path, answer.Body, err)
			continue
		}
		if len(body) == 0 {
			t.Errorf("the page's own fetch of %s answered an empty object", path)
		}
	}

	// The token is accepted in both the places #4 accepts it, with no Origin
	// as with one. The query parameter matters here specifically: it is how
	// the startup URL hands the token to the browser in the first place, so
	// it is what a page has before it has anything else.
	inQuery := request{queryToken: server.Token().Secret(), headers: http.Header{secFetchSiteHeader: {"same-origin"}}}
	if answer := server.do(t, inQuery); answer.Status != http.StatusOK {
		t.Errorf("the page's own fetch with the token in the query parameter was answered %d, want 200: %s", answer.Status, answer)
	}

	// The same shape with the request the user's own navigation makes: no
	// document initiated it, so no page could have caused it.
	navigation := server.pageFetch("/api/health")
	navigation.headers = http.Header{secFetchSiteHeader: {"none"}}
	if answer := server.do(t, navigation); answer.Status != http.StatusOK {
		t.Errorf("a user-initiated request with the token and no Origin was answered %d, want 200: %s", answer.Status, answer)
	}
}

// Nothing is loosened for a caller that does not hold the token. The fetch
// metadata is one half of a conjunction, so every shape that the metadata
// vouches for is still the uniform rejection without the right token -- and
// byte-identical to the rejection an unauthorized stranger already got before
// this rule existed, which is the property #4 shipped.
func TestTheVouchedShapesStillNeedTheToken(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	right := server.Token().Secret()

	baseline := server.do(t, request{})
	if baseline.Status != rejectionStatus || baseline.Body != rejectionBody {
		t.Fatalf("an unauthorized request answered %d %q, so there is no baseline to compare against", baseline.Status, baseline.Body)
	}

	for _, site := range []string{"same-origin", "none"} {
		vouched := http.Header{secFetchSiteHeader: {site}}
		cases := map[string]request{
			"no token at all":                       {headers: vouched},
			"a wrong token of the same length":      {token: strings.Repeat("A", len(right)), headers: vouched},
			"a wrong token one byte off":            {token: right[:len(right)-1] + "!", headers: vouched},
			"an empty bearer token":                 {authorization: []string{"Bearer "}, headers: vouched},
			"the token in the wrong scheme":         {authorization: []string{"Basic " + right}, headers: vouched},
			"two Authorization headers":             {authorization: []string{"Bearer " + right, "Bearer " + right}, headers: vouched},
			"a wrong token in the query":            {queryToken: "wrong", headers: vouched},
			"no token, on a path that is not there": {path: "/api/does-not-exist", headers: vouched},
			"no token, on the other route":          {path: "/api/version", headers: vouched},
		}
		for name, want := range cases {
			answer := server.do(t, want)
			if answer.Status != rejectionStatus || answer.Body != rejectionBody {
				t.Errorf("%q with Sec-Fetch-Site: %s was answered %d %q, want the uniform rejection", name, site, answer.Status, answer.Body)
				continue
			}
			if answer != baseline {
				t.Errorf("%q with Sec-Fetch-Site: %s is told apart from an unauthorized stranger:\n%s\n\nversus\n\n%s", name, site, answer, baseline)
			}
		}
	}
}

// A client that sends neither header is rejected, token or no token. That is
// the documented behaviour of the rule for everything that is not a browser:
// curl, a script, and a browser too old to implement fetch metadata.
func TestAClientThatSendsNeitherHeaderIsRejected(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})

	for name, want := range map[string]request{
		"with the right token": {token: server.Token().Secret()},
		"with no token":        {},
		"with a wrong token":   {token: "wrong"},
		"on the other route":   {path: "/api/version", token: server.Token().Secret()},
	} {
		answer := server.do(t, want)
		if answer.Status != rejectionStatus || answer.Body != rejectionBody {
			t.Errorf("a request with neither an Origin nor a Sec-Fetch-Site, %s, was answered %d %q, want the uniform rejection", name, answer.Status, answer.Body)
		}
	}
}

// Only the two values that vouch are accepted, and the ones a hostile page's
// requests carry are not. The right token is presented throughout, so what is
// being measured is the metadata and nothing else.
func TestOnlyTheTwoVouchedFetchSiteValuesAreAccepted(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	secret := server.Token().Secret()

	// A single header carrying this value, and nothing else.
	one := func(value string) request {
		return request{token: secret, headers: http.Header{secFetchSiteHeader: {value}}}
	}

	for _, value := range []string{"same-origin", "none"} {
		if answer := server.do(t, one(value)); answer.Status != http.StatusOK {
			t.Errorf("Sec-Fetch-Site: %q was rejected: %s", value, answer)
		}
	}

	// What a hostile page's request carries, and every near miss worth
	// trying. A value a later specification adds lands here too, which is the
	// fail-closed direction.
	refused := []string{
		"cross-site",
		"same-site",
		"",
		"SAME-ORIGIN",
		"Same-Origin",
		"NONE",
		"same-origin, cross-site",
		"cross-site, same-origin",
		"same-origin,none",
		"same-origin;",
		"same-origin\tsame-origin",
		"samesite",
		"sameorigin",
		"same-origin.evil.example",
		"null",
		"*",
		"none-such",
	}
	for _, value := range refused {
		if answer := server.do(t, one(value)); answer.Status != rejectionStatus {
			t.Errorf("Sec-Fetch-Site: %q was accepted: %s", value, answer)
		}
	}

	// More than one header is not a request to work out the intent of, which
	// is the same ruling the Origin header gets, and it holds even when every
	// value vouches on its own.
	for name, values := range map[string][]string{
		"two that vouch":        {"same-origin", "same-origin"},
		"two different ones":    {"same-origin", "none"},
		"a vouch and a hostile": {"same-origin", "cross-site"},
		"a hostile and a vouch": {"cross-site", "same-origin"},
		"three":                 {"same-origin", "same-origin", "same-origin"},
		"a vouch and an empty":  {"same-origin", ""},
	} {
		answer := server.do(t, request{token: secret, headers: http.Header{secFetchSiteHeader: values}})
		if answer.Status != rejectionStatus {
			t.Errorf("%s Sec-Fetch-Site header values (%q) were accepted: %s", name, values, answer)
		}
	}

	// What net/http itself normalizes away before any of this code sees it:
	// the optional whitespace around a header value. These are the accepted
	// value, spelled with the padding the HTTP grammar allows, so they are
	// accepted -- recorded here because it is the first thing to try against
	// an exact string comparison, and the answer is that there is nothing to
	// find.
	for _, value := range []string{" same-origin", "same-origin ", "  same-origin  "} {
		if answer := server.do(t, one(value)); answer.Status != http.StatusOK {
			t.Errorf("Sec-Fetch-Site: %q is the accepted value with the whitespace HTTP allows around it, and was rejected: %s", value, answer)
		}
	}
}

// Present but wrong is not absent. Fetch metadata is consulted only when there
// is no Origin header at all, so nothing a request can say about itself
// rescues an Origin that is not on the allowlist.
func TestAPresentOriginIsNotRescuedByFetchMetadata(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{AllowedOrigins: []string{"https://ui.example"}})
	secret := server.Token().Secret()

	for _, site := range []string{"same-origin", "none"} {
		for name, origins := range map[string][]string{
			"a hostile origin":           {"https://evil.example"},
			"the null origin":            {"null"},
			"an origin that is a prefix": {server.URL() + ".evil.example"},
			"the right host, wrong port": {"http://127.0.0.1:1"},
			"the right host, https":      {"https://127.0.0.1:" + itoa(server.Port())},
			"an empty Origin":            {""},
			"two allowed origins":        {server.URL(), server.URL()},
			"an allowed and a hostile":   {"https://ui.example", "https://evil.example"},
			"a hostile and an allowed":   {"https://evil.example", "https://ui.example"},
		} {
			answer := server.do(t, request{token: secret, origins: origins, headers: http.Header{secFetchSiteHeader: {site}}})
			if answer.Status != rejectionStatus || answer.Body != rejectionBody {
				t.Errorf("%s (%q) with Sec-Fetch-Site: %s was answered %d %q, want the uniform rejection", name, origins, site, answer.Status, answer.Body)
			}
		}
	}

	// The control: an allowed Origin is still answered, whatever the metadata
	// says alongside it, because that is the path this change did not touch.
	for _, site := range []string{"same-origin", "cross-site", ""} {
		answer := server.do(t, request{token: secret, origins: []string{"https://ui.example"}, headers: http.Header{secFetchSiteHeader: {site}}})
		if answer.Status != http.StatusOK {
			t.Errorf("an allowed Origin with Sec-Fetch-Site: %q was rejected: %s", site, answer)
		}
	}
}

// The relaxation is only for a read. A browser attaches Origin to every method
// that is not GET or HEAD, same-origin included, so a state-changing route
// needs nothing relaxed and a request that asks for one without an Origin is
// not a shape a browser produces.
func TestTheRelaxationIsOnlyForReads(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	secret := server.Token().Secret()
	vouched := http.Header{secFetchSiteHeader: {"same-origin"}}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		answer := server.do(t, request{method: method, token: secret, headers: vouched})
		if answer.Status != rejectionStatus || answer.Body != rejectionBody {
			t.Errorf("a %s with the token, no Origin and vouching metadata was answered %d %q, want the uniform rejection", method, answer.Status, answer.Body)
		}
	}

	// HEAD is the other half of a read, and the route table's GET pattern
	// serves it, so it is answered.
	if answer := server.do(t, request{method: http.MethodHead, token: secret, headers: vouched}); answer.Status != http.StatusOK {
		t.Errorf("a HEAD with the token, no Origin and vouching metadata was answered %d, want 200: %s", answer.Status, answer)
	}

	// And the method restriction lives in the origin rule, not in a block on
	// writes: the same POST with an allowed Origin reaches net/http's routing,
	// which answers 405 because no route serves it. That is what a page's own
	// POST will do once there is a route for it, with no relaxation needed.
	withOrigin := server.do(t, request{method: http.MethodPost, token: secret, origins: []string{server.URL()}, headers: vouched})
	if withOrigin.Status != http.StatusMethodNotAllowed {
		t.Errorf("a POST with the token and an allowed Origin was answered %d, want 405 from routing: %s", withOrigin.Status, withOrigin)
	}
}

// The WebSocket upgrade path gets no relaxation to inherit. A browser sends
// Origin on a handshake, so an upgrade never needs this, and an upgrade is a
// GET, so it would otherwise have fallen into it. Raw bytes, because a client
// library normalizes away exactly these headers.
func TestAnUpgradeRequestGetsNoRelaxation(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	address := server.Addr()
	secret := server.Token().Secret()

	handshake := func(originLine string) string {
		return "GET /api/health HTTP/1.1\r\nHost: " + address + "\r\n" +
			"Authorization: Bearer " + secret + "\r\n" +
			secFetchSiteHeader + ": same-origin\r\n" +
			originLine +
			"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n" +
			"Connection: close\r\n\r\n"
	}

	status, body := rawRequest(t, address, handshake(""))
	if code := statusCodeOf(t, status); code != rejectionStatus || body != rejectionBody {
		t.Errorf("an upgrade with the token, vouching metadata and no Origin was answered %q with body %q, want the uniform rejection; #5 must not inherit a relaxation", status, body)
	}

	// Without the Upgrade header the same bytes are the page's own read, so
	// the rejection above is the upgrade and not something else in the wire
	// form.
	plain := "GET /api/health HTTP/1.1\r\nHost: " + address + "\r\nAuthorization: Bearer " + secret + "\r\n" +
		secFetchSiteHeader + ": same-origin\r\nConnection: close\r\n\r\n"
	if status, body := rawRequest(t, address, plain); statusCodeOf(t, status) != http.StatusOK {
		t.Errorf("the same request without the upgrade headers answered %q %q, want 200, so the test above proves nothing", status, body)
	}

	// And an upgrade carrying an allowed Origin, which is what a browser
	// really sends, is not rejected by the origin rule: it reaches the routes,
	// where there is nothing to upgrade to yet.
	status, body = rawRequest(t, address, handshake("Origin: "+server.URL()+"\r\n"))
	if code := statusCodeOf(t, status); code != http.StatusOK {
		t.Errorf("an upgrade with an allowed Origin was answered %q with body %q; the origin rule should have let it reach the routes", status, body)
	}
}
