// The checks every request goes through: the token, the origin, and the one
// answer a failed check gets.

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestARequestWithTheTokenAndAnAllowedOriginIsAnswered(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})

	for _, r := range apiRoutes() {
		_, path := methodAndPathOf(t, r.pattern)
		answer := server.do(t, server.allowed(path))
		if answer.Status != http.StatusOK {
			t.Fatalf("GET %s answered %d, want 200: %s", path, answer.Status, answer)
		}
		var body map[string]string
		if err := json.Unmarshal([]byte(answer.Body), &body); err != nil {
			t.Fatalf("GET %s answered %q, which is not JSON: %v", path, answer.Body, err)
		}
		if len(body) == 0 {
			t.Fatalf("GET %s answered an empty object", path)
		}
	}
}

// rejectionCases is every way a request can fail a check. They are kept
// together because the point of the next two tests is that a client cannot
// tell them apart.
func rejectionCases(server *testServer) map[string]request {
	right := server.Token().Secret()
	own := server.URL()
	wrongSameLength := strings.Repeat("A", len(right))
	return map[string]request{
		"no token at all":                        {origins: []string{own}},
		"a wrong token of the right length":      {token: wrongSameLength, origins: []string{own}},
		"a wrong token one byte off":             {token: right[:len(right)-1] + "!", origins: []string{own}},
		"a wrong token far too short":            {token: "x", origins: []string{own}},
		"a wrong token far too long":             {token: right + strings.Repeat("x", 200), origins: []string{own}},
		"an empty bearer token":                  {authorization: []string{"Bearer "}, origins: []string{own}},
		"a token in the wrong auth scheme":       {authorization: []string{"Basic " + right}, origins: []string{own}},
		"two Authorization headers":              {authorization: []string{"Bearer " + right, "Bearer " + right}, origins: []string{own}},
		"a wrong token in the query":             {queryToken: wrongSameLength, origins: []string{own}},
		"no Origin header":                       {token: right},
		"a disallowed origin":                    {token: right, origins: []string{"https://evil.example"}},
		"a null origin":                          {token: right, origins: []string{"null"}},
		"two Origin headers":                     {token: right, origins: []string{own, own}},
		"an origin that is only a prefix":        {token: right, origins: []string{own + ".evil.example"}},
		"neither the token nor the origin":       {},
		"an unauthorized OPTIONS *":              {method: http.MethodOptions, asterisk: true},
		"a path that does not exist":             {path: "/api/does-not-exist"},
		"a method no route serves, unauthorized": {method: http.MethodDelete},
	}
}

// The criterion is that nothing in a rejection says which check failed, so
// this compares the whole observable answer across every failing case rather
// than reading the bodies and judging them similar enough. Status, every
// header but the clock, and the body all have to be identical.
func TestEveryRejectionIsTheSameAnswer(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})

	var first string
	var firstAnswer response
	for name, want := range rejectionCases(server) {
		answer := server.do(t, want)
		if answer.Status != rejectionStatus {
			t.Errorf("%s answered %d, want %d: %s", name, answer.Status, rejectionStatus, answer)
			continue
		}
		if first == "" {
			first, firstAnswer = name, answer
			continue
		}
		if answer != firstAnswer {
			t.Errorf("%q and %q are told apart by their answers:\n%s\n\nversus\n\n%s", name, first, answer, firstAnswer)
		}
	}
	if first == "" {
		t.Fatal("no rejection was observed, so nothing was compared")
	}
	if firstAnswer.Body != rejectionBody {
		t.Errorf("a rejection's body is %q, want %q", firstAnswer.Body, rejectionBody)
	}
}

// Even one rejection on its own must not describe the check it failed, so that
// a wording change cannot turn the uniform answer above into a hint.
func TestARejectionDescribesNothing(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	answer := server.do(t, request{})

	said := strings.ToLower(answer.String())
	for _, giveaway := range []string{"token", "origin", "auth", "unauthorized", "forbidden", "allowlist", "bearer", "missing", "invalid", "expected"} {
		if strings.Contains(said, giveaway) {
			t.Errorf("a rejection mentions %q: %s", giveaway, answer)
		}
	}
}

// What a rejection hides from an unauthorized caller, and what it does not
// hide from an authorized one. Nothing unauthorized can tell a path that
// exists from one that does not; a caller holding the token can, because
// net/http's own routing answers it, and hiding the shape of the API from the
// session that owns it would buy nothing.
func TestRoutingIsVisibleOnlyToAnAuthorizedCaller(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})

	existing := server.do(t, request{path: "/api/health"})
	imaginary := server.do(t, request{path: "/api/does-not-exist"})
	if existing != imaginary {
		t.Errorf("an unauthorized caller can tell a real path from an imaginary one:\n%s\n\nversus\n\n%s", existing, imaginary)
	}
	if existing.Status != rejectionStatus {
		t.Errorf("an unauthorized request to a real path answered %d, want %d", existing.Status, rejectionStatus)
	}

	authorized := server.allowed("/api/does-not-exist")
	if answer := server.do(t, authorized); answer.Status != http.StatusNotFound || answer.Body == rejectionBody {
		t.Errorf("an authorized request to an imaginary path got the guard's rejection rather than net/http's routing: %s", answer)
	}
	wrongMethod := server.allowed("/api/health")
	wrongMethod.method = http.MethodDelete
	if answer := server.do(t, wrongMethod); answer.Status != http.StatusMethodNotAllowed {
		t.Errorf("an authorized request with a method no route serves answered %d, want 405: %s", answer.Status, answer)
	}
}

// The token may never reach a response or a log, whichever way it was
// presented and whether the request succeeded or failed.
func TestTheTokenNeverReachesAResponseOrTheLogs(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	secret := server.Token().Secret()

	presentations := map[string]request{
		"in the Authorization header": server.allowed("/api/health"),
		"in the query parameter":      {queryToken: secret, origins: []string{server.URL()}},
		"on a route that rejects":     {path: "/api/does-not-exist", queryToken: secret},
		"with a disallowed origin":    {token: secret, origins: []string{"https://evil.example"}},
		"with no origin":              {token: secret},
	}
	for name, want := range presentations {
		answer := server.do(t, want)
		if strings.Contains(answer.String(), secret) {
			t.Errorf("the answer to a request with the token %s echoes it back: %s", name, answer)
		}
	}

	if logged := server.logs.String(); strings.Contains(logged, secret) {
		t.Errorf("the token reached the log:\n%s", logged)
	}
	// The startup line is the one place the secret is allowed, and it is not
	// the log, so the log must not even carry the startup URL.
	if logged := server.logs.String(); strings.Contains(logged, server.StartupURL()) {
		t.Errorf("the startup URL, which carries the token, reached the log:\n%s", logged)
	}
}

// Every route the agent serves is behind the checks. The test walks the route
// table rather than naming routes, so a route added without the checks fails
// here instead of shipping.
func TestEveryRegisteredRouteIsBehindTheChecks(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	routes := apiRoutes()
	if len(routes) == 0 {
		t.Fatal("there are no routes, so this test proves nothing")
	}

	for _, r := range routes {
		method, path := methodAndPathOf(t, r.pattern)
		for name, want := range map[string]request{
			"no token":       {method: method, path: path, origins: []string{server.URL()}},
			"a wrong token":  {method: method, path: path, token: "wrong", origins: []string{server.URL()}},
			"a bad origin":   {method: method, path: path, token: server.Token().Secret(), origins: []string{"https://evil.example"}},
			"no origin":      {method: method, path: path, token: server.Token().Secret()},
			"nothing at all": {method: method, path: path},
		} {
			answer := server.do(t, want)
			if answer.Status != rejectionStatus || answer.Body != rejectionBody {
				t.Errorf("%s %s with %s was not rejected: %s", method, path, name, answer)
			}
		}
	}
}

// The structural claim: a route cannot be registered outside the guard,
// because newHandler is the only thing that builds a mux and it wraps what it
// built. This test adds a route exactly the way a future change would, with no
// checks of its own, and insists it is still protected. If someone reshaped
// the package so that a route could be registered on an unguarded mux, this is
// what fails.
func TestANewlyAddedRouteCannotSkipTheChecks(t *testing.T) {
	t.Parallel()

	token, err := NewToken()
	if err != nil {
		t.Fatalf("mint a token: %v", err)
	}
	origins, err := newOriginPolicy(1234, []string{"https://ui.example"})
	if err != nil {
		t.Fatalf("build the origin policy: %v", err)
	}

	reached := make(chan struct{}, 1)
	added := route{"GET /api/added-without-a-thought", func(w http.ResponseWriter, _ *http.Request) {
		reached <- struct{}{}
		_, _ = w.Write([]byte("the handler ran"))
	}}
	handler := newHandler(&guard{token: token, origins: origins, logger: discardLogger()}, append(apiRoutes(), added))

	call := func(r *http.Request) *httptest.ResponseRecorder {
		recorded := httptest.NewRecorder()
		handler.ServeHTTP(recorded, r)
		return recorded
	}

	for name, build := range map[string]func() *http.Request{
		"with no token": func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/added-without-a-thought", nil)
			r.Header.Set("Origin", "https://ui.example")
			return r
		},
		"with no origin": func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/added-without-a-thought", nil)
			r.Header.Set(authorizationHeader, "Bearer "+token.Secret())
			return r
		},
	} {
		recorded := call(build())
		if recorded.Code != rejectionStatus || recorded.Body.String() != rejectionBody {
			t.Errorf("the added route answered a request %s itself: %d %q", name, recorded.Code, recorded.Body.String())
		}
		select {
		case <-reached:
			t.Errorf("the added route's handler ran for a request %s", name)
		default:
		}
	}

	authorized := httptest.NewRequest(http.MethodGet, "/api/added-without-a-thought", nil)
	authorized.Header.Set("Origin", "https://ui.example")
	authorized.Header.Set(authorizationHeader, "Bearer "+token.Secret())
	if recorded := call(authorized); recorded.Body.String() != "the handler ran" {
		t.Fatalf("the added route did not run for an authorized request: %d %q", recorded.Code, recorded.Body.String())
	}
	select {
	case <-reached:
	default:
		t.Fatal("the added route's handler never ran, so the rejections above prove nothing")
	}
}

func TestTheTokenIsAcceptedInBothPlacesAndNowhereElse(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	secret := server.Token().Secret()
	own := server.URL()

	accepted := map[string]request{
		"a bearer token":                  {token: secret, origins: []string{own}},
		"a bearer token in lower case":    {authorization: []string{"bearer " + secret}, origins: []string{own}},
		"a bearer token in upper case":    {authorization: []string{"BEARER " + secret}, origins: []string{own}},
		"a bearer token with extra space": {authorization: []string{"Bearer  " + secret + " "}, origins: []string{own}},
		"the query parameter":             {queryToken: secret, origins: []string{own}},
	}
	for name, want := range accepted {
		if answer := server.do(t, want); answer.Status != http.StatusOK {
			t.Errorf("%s was not accepted: %s", name, answer)
		}
	}

	// A request that presents an unusable Authorization header has presented
	// something, and the query parameter is not a second chance at it.
	refused := map[string]request{
		"a bad header with the right token in the query": {
			authorization: []string{"Bearer wrong"},
			queryToken:    secret,
			origins:       []string{own},
		},
		"a header in another scheme with the right token in the query": {
			authorization: []string{"Basic " + secret},
			queryToken:    secret,
			origins:       []string{own},
		},
	}
	for name, want := range refused {
		if answer := server.do(t, want); answer.Status != rejectionStatus {
			t.Errorf("%s was accepted: %s", name, answer)
		}
	}
}

func TestTheOriginAllowlistIsExact(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{AllowedOrigins: []string{"http://localhost:5173", "https://UI.Example"}})
	secret := server.Token().Secret()

	allowed := []string{
		server.URL(),
		"http://localhost:" + itoa(server.Port()),
		"http://localhost:5173",
		"https://ui.example",
		"HTTPS://UI.EXAMPLE",
	}
	for _, origin := range allowed {
		if answer := server.do(t, request{token: secret, origins: []string{origin}}); answer.Status != http.StatusOK {
			t.Errorf("the allowed origin %q was rejected: %s", origin, answer)
		}
	}

	disallowed := []string{
		"https://ui.example.evil.test",
		"https://evil.test",
		"http://ui.example",
		"https://ui.example:8443",
		"http://127.0.0.1:1",
		"http://localhost:5174",
		"null",
		"",
		"https://ui.example/",
		"*",
	}
	for _, origin := range disallowed {
		if answer := server.do(t, request{token: secret, origins: []string{origin}}); answer.Status != rejectionStatus {
			t.Errorf("the disallowed origin %q was accepted: %s", origin, answer)
		}
	}
}

// The deliberate rule for a request with no Origin, asserted rather than left
// to the comment that explains it: absence is a rejection, not permission.
func TestARequestWithNoOriginIsRejectedByTheDocumentedRule(t *testing.T) {
	t.Parallel()
	if !originRequired {
		t.Fatal("originRequired is false, but the documented rule in origin.go is that a request with no Origin is rejected; change one or the other deliberately")
	}
	server := startServer(t, Options{})
	answer := server.do(t, request{token: server.Token().Secret()})
	if answer.Status != rejectionStatus {
		t.Fatalf("a request with the right token and no Origin was answered %d, want it rejected: %s", answer.Status, answer)
	}
}

func TestAConfiguredOriginThatIsNotAnOriginFailsStartup(t *testing.T) {
	t.Parallel()
	for name, origin := range map[string]string{
		"a wildcard":       "*",
		"a wildcard path":  "http://localhost:5173/*",
		"a path":           "http://localhost:5173/app",
		"a trailing slash": "http://localhost:5173/",
		"no scheme":        "localhost:5173",
		"the wrong scheme": "ws://localhost:5173",
		"no host":          "http://",
		"credentials":      "http://user:pass@localhost:5173",
		"the null origin":  "null",
		"nothing":          "",
		"a query":          "http://localhost:5173?a=b",
		"a space":          "http://localhost:5173 http://localhost:5174",
	} {
		server, err := New(Options{AllowedOrigins: []string{origin}})
		if err == nil {
			_ = server.Close()
			t.Errorf("%s (%q) was accepted as an allowed origin", name, origin)
			continue
		}
		if kind := kindOf(err); kind != KindOrigin {
			t.Errorf("%s (%q) failed with kind %q, want %q: %v", name, origin, kind, KindOrigin, err)
		}
		if !strings.Contains(err.Error(), "/allowedOrigins/0") {
			t.Errorf("%s (%q) failed without naming the offending option: %v", name, origin, err)
		}
	}
}

// The same claim as the structural test, made against the running server: the
// handler the server actually serves rejects an unauthorized request to any
// path at all, and serves an authorized one. A handler swapped for something
// that answers without the checks fails here.
func TestTheHandlerTheServerServesIsGuarded(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})

	served := server.http.Handler
	if served == nil {
		t.Fatal("the server serves a nil handler, which means http.DefaultServeMux")
	}

	// The asterisk form is not here because it is not a path: it is sent as
	// raw bytes in bypass_test.go, where net/http's own handling of it is
	// what matters.
	paths := []string{"/", "/api", "/api/health", "/api/version", "/anything", "/api/does-not-exist"}
	for _, r := range apiRoutes() {
		_, path := methodAndPathOf(t, r.pattern)
		paths = append(paths, path)
	}
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodOptions, http.MethodHead, http.MethodDelete} {
			recorded := httptest.NewRecorder()
			served.ServeHTTP(recorded, httptest.NewRequest(method, "http://"+server.Addr()+path, nil))
			if recorded.Code != rejectionStatus || recorded.Body.String() != rejectionBody {
				t.Errorf("the served handler answered an unauthorized %s %s with %d %q", method, path, recorded.Code, recorded.Body.String())
			}
		}
	}

	// The control: the same handler does serve an authorized request, so the
	// rejections above are not a handler that refuses everything.
	authorized := httptest.NewRequest(http.MethodGet, "http://"+server.Addr()+"/api/health", nil)
	authorized.Header.Set("Origin", server.URL())
	authorized.Header.Set(authorizationHeader, "Bearer "+server.Token().Secret())
	recorded := httptest.NewRecorder()
	served.ServeHTTP(recorded, authorized)
	if recorded.Code != http.StatusOK {
		t.Fatalf("the served handler answered an authorized request %d %q", recorded.Code, recorded.Body.String())
	}
}
