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
// None of them touches the filesystem: reading and writing pipeline.yaml
// exists in internal/pipeline already, and it is deliberately not wired up
// here, because a route that reads a path needs the path authorization that is
// #6. These two answer out of memory and out of the build.
func apiRoutes() []route {
	return []route{
		{"GET /api/health", handleHealth},
		{"GET /api/version", handleVersion},
	}
}

// newHandler builds the handler the server serves: a fresh ServeMux carrying
// routes, wrapped in the guard. Nothing else in this package creates a mux or
// registers a pattern, so every route and every unmatched path is checked.
func newHandler(g *guard, routes []route) http.Handler {
	mux := http.NewServeMux()
	for _, r := range routes {
		mux.Handle(r.pattern, r.handler)
	}
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
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
