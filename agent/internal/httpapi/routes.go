package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/zssvaidar/kpv/agent/internal/version"
)

// route is one route of the local API: a net/http pattern, method included,
// and the handler for it.
//
// Routes are values in a table rather than calls on a mux because of what the
// table makes impossible. newHandler is the only place a mux exists, it builds
// that mux from a table and immediately wraps it in the guard, and the mux
// never escapes. Adding a route means adding a row, and a row cannot be behind
// anything other than the guard.
type route struct {
	pattern string
	handler http.HandlerFunc
}

// apiRoutes is every route the agent serves.
//
// None of them touches the filesystem; these two answer out of memory and out
// of the build. The one route that reads the filesystem is the directory
// browsing endpoint, which is not a row here (see directoriesPattern) and
// reaches the filesystem only through the projectfs.Browser the guard
// carries. internal/pipeline is still not wired up: its store hands back a
// path to open, which is the weaker route #6 documented, and #48 moves it.
func apiRoutes() []route {
	return []route{
		{"GET /api/health", handleHealth},
		{"GET /api/version", handleVersion},
	}
}

// newHandler builds the handler the server serves: a fresh ServeMux carrying
// routes and the channel endpoint, wrapped in the guard. Nothing else in this
// package creates a mux or registers a pattern, so every route and every
// unmatched path is checked.
func newHandler(g *guard, routes []route) http.Handler {
	mux := http.NewServeMux()
	for _, r := range routes {
		mux.Handle(r.pattern, r.handler)
	}
	// The channel is a route on this mux like any other, which is what puts
	// it inside the wrap below: the token and the origin are checked before
	// the handshake is looked at, never after it is accepted. See
	// channelPattern in channel.go for why it is registered here instead of
	// being a row in the table.
	mux.Handle(channelPattern, g.channelRoute())
	// The directory browsing endpoint is registered here for the same reason;
	// see directoriesPattern in directories.go.
	mux.Handle(directoriesPattern, g.directoriesRoute())
	return g.wrap(mux)
}

// handleHealth answers that the agent is up. It is the route the page polls,
// so it reads nothing and allocates almost nothing.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleVersion answers with the agent's build version, the same string the
// startup line prints.
func handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"version": version.String()})
}

// writeJSON writes a response body. The value is marshalled before anything is
// written, so a marshalling failure cannot leave a half-written body behind a
// 200; it is also why no handler can accidentally stream a partial answer.
func writeJSON(w http.ResponseWriter, value any) {
	writeJSONStatus(w, http.StatusOK, value)
}

// writeJSONStatus is writeJSON with a status other than 200, which is how the
// directory browsing endpoint answers a request it could not serve.
func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		// Nothing in this package marshals anything that can fail, so this is
		// unreachable; it answers with the same empty rejection as everything
		// else rather than describing the problem to a client.
		reject(w)
		return
	}
	body = append(body, '\n')
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
