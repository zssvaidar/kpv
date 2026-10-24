// The rule for a request that carries no Origin header, and the gap this task
// exists to close.
//
// THE PIN. The test below asserts the behaviour as it is before the fix: the
// request a page served from the agent's own origin makes -- a GET with the
// session token, no Origin header, and the fetch metadata a browser attaches
// to its own page's request -- is rejected. It passes at this commit, which is
// what makes the fix demonstrably a fix: once the rule changes, this test has
// to fail, and the commit that changes the rule is the commit that turns this
// assertion round.
//
// Nothing here is a wish. Every case is the request shape a browser really
// sends, per the Fetch standard: no Origin on a same-origin GET, and
// Sec-Fetch-Site on every request a browser makes.

package httpapi

import (
	"net/http"
	"testing"
)

// pageFetch is the request the agent's own page makes once it is served from
// the agent's own origin: the token in the Authorization header, no Origin,
// and the fetch metadata the browser attaches and the page cannot.
func (ts *testServer) pageFetch(path string) request {
	return request{
		path:    path,
		token:   ts.Token().Secret(),
		headers: http.Header{"Sec-Fetch-Site": {"same-origin"}},
	}
}

// The gap, pinned: the page's own fetch is rejected today, on every route the
// agent serves, with the same answer an unauthorized stranger gets.
func TestTheSameOriginPageFetchIsRejectedToday(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})

	stranger := server.do(t, request{})
	if stranger.Status != rejectionStatus {
		t.Fatalf("an unauthorized request answered %d, so this test is not measuring against the rejection: %s", stranger.Status, stranger)
	}

	routes := apiRoutes()
	if len(routes) == 0 {
		t.Fatal("there are no routes, so this test proves nothing")
	}
	for _, r := range routes {
		_, path := methodAndPathOf(t, r.pattern)
		answer := server.do(t, server.pageFetch(path))
		if answer.Status != rejectionStatus || answer.Body != rejectionBody {
			t.Errorf("the page's own fetch of %s was answered %d %q, want the uniform rejection; the gap this task closes is no longer there to close", path, answer.Status, answer.Body)
			continue
		}
		if answer != stranger {
			t.Errorf("the page's own fetch of %s is told apart from an unauthorized stranger:\n%s\n\nversus\n\n%s", path, answer, stranger)
		}
	}
}
