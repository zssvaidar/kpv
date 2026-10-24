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

	// The same shape with the request the user's own navigation makes: no
	// document initiated it, so no page could have caused it.
	navigation := server.pageFetch("/api/health")
	navigation.headers = http.Header{secFetchSiteHeader: {"none"}}
	if answer := server.do(t, navigation); answer.Status != http.StatusOK {
		t.Errorf("a user-initiated request with the token and no Origin was answered %d, want 200: %s", answer.Status, answer)
	}
}
