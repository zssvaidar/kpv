package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/zssvaidar/kpv/agent/internal/channel"
)

// tokenParameter is the query parameter the session token may arrive in, and
// the one the startup URL carries.
const tokenParameter = "token"

// authorizationHeader and bearerScheme are the preferred way to present the
// token: a header, so it stays out of the browser's address bar, its history
// and any referrer.
const (
	authorizationHeader = "Authorization"
	bearerScheme        = "bearer"
)

// guard is the token and origin check. It wraps a handler rather than being a
// handler, and newHandler is the only thing that builds the served handler, so
// there is no way to register a route that is not behind it.
type guard struct {
	token   Token
	origins *originPolicy
	logger  *slog.Logger
	// channel is the push channel the WebSocket route serves (#5). It rides
	// on the guard because newHandler is handed the guard and the route
	// table, and nothing else: the table's rows are plain handlers with no
	// state behind them, and the two tests that make the checks impossible to
	// forget pin both of those shapes -- newHandler(g, apiRoutes()) is the
	// only expression allowed to be the served handler. So the one object the
	// handler needs is carried by the one object that reaches it.
	//
	// A nil channel means a server with no channel, which answers the route
	// with the same rejection as anything else. Only a test builds one.
	channel *channel.Hub
}

// wrap returns next behind the checks. Every request, including one that
// matches no route at all, is answered by the rejection below unless it
// carries the right token and an allowed origin.
func (g *guard) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Both checks run before either is acted on, and the token check runs
		// first. Short-circuiting here would make a missing token measurably
		// cheaper than a wrong one and a bad origin cheaper than both, which
		// is the same leak the uniform response below exists to close; `&&`
		// would short-circuit, so the results are computed into variables
		// first and only then combined.
		tokenOK := g.token.matches(presentedToken(r))
		originOK := g.origins.allowsRequest(r)
		if !tokenOK || !originOK {
			// Which of the two failed is deliberately not recorded, not even
			// in the log, so that a log the operator pastes somewhere cannot
			// answer the question the response refuses to.
			g.logger.Debug("rejected a request to the agent API", "method", r.Method, "path", r.URL.Path)
			reject(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// The one rejection every failed check produces.
//
// It is 404 with a fixed body: no detail, no header that varies, and the same
// answer an unauthorized request to a path that does not exist gets. Nothing
// without the token can tell a missing token from a wrong token from a
// disallowed origin from a route that was never there, so an unauthorized
// caller cannot even enumerate the API. A caller holding the token does see
// net/http's own 404 and 405, which is routing, not a check.
//
// 401 with a uniform body would satisfy the same criterion and would be the
// more conventional status; it is not used because it would confirm that
// something is listening and that the path exists, and the agent's job here is
// to say nothing useful to anything that is not the session that owns it.
const (
	rejectionStatus      = http.StatusNotFound
	rejectionBody        = "not found\n"
	rejectionContentType = "text/plain; charset=utf-8"
)

func reject(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Content-Type", rejectionContentType)
	// Nothing about a rejection is worth storing, and no cache may answer for
	// us. Both headers are constants, so they do not distinguish the cases.
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(rejectionStatus)
	// The error is ignored deliberately: the client hanging up is not
	// something a rejection reports, and there is nothing else to say.
	_, _ = w.Write([]byte(rejectionBody))
}

// presentedToken returns the token a request presents, or "" when it presents
// none. "" is then compared like any other wrong guess, so a request with no
// token takes the same path as a request with the wrong one.
//
// Two places are accepted, and the order matters:
//
//   - Authorization: Bearer <token>, which is where a fetch from the agent's
//     own page puts it.
//   - the token query parameter, which is how the token reaches that page in
//     the first place: the startup URL carries it, and a browser cannot attach
//     a header to a navigation or to a WebSocket handshake (#5).
//
// A malformed Authorization header is not retried as a query parameter: a
// request that presents something has presented it, and falling back would
// turn one bad guess into two.
func presentedToken(r *http.Request) string {
	if presented, ok := bearerToken(r.Header.Values(authorizationHeader)); ok {
		return presented
	}
	return r.URL.Query().Get(tokenParameter)
}

// bearerToken pulls the token out of the Authorization header values. It
// reports false only when there is no Authorization header at all; a header
// that is there but unusable yields "" and true, so it is compared and
// rejected rather than ignored.
func bearerToken(values []string) (string, bool) {
	if len(values) == 0 {
		return "", false
	}
	if len(values) > 1 {
		// More than one Authorization header is not a request to work out the
		// intent of.
		return "", true
	}
	scheme, credentials, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, bearerScheme) {
		return "", true
	}
	return strings.TrimSpace(credentials), true
}
