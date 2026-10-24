// Package httpapi is the agent's local HTTP API: a net/http server bound to
// loopback on an ephemeral port, behind a session token and an origin
// allowlist that every route inherits.
//
// The shape of it, and why:
//
//   - the listener is bound by New, not by Serve, so the port is known before
//     anything can be asked of the server and a caller never has to wait and
//     hope to find out which port it got
//   - the address is 127.0.0.1 and nothing configures that, so the socket is
//     not reachable from another machine even if a firewall is not
//   - the token is minted by New from crypto/rand, fresh for the process, and
//     is written in exactly one place: the startup line the operator reads
//   - the served handler is built once, from a route table, already wrapped in
//     the guard, so a new route cannot be registered outside the checks
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// loopbackHost is the only address the agent binds. It is IPv4 loopback
// literally rather than "localhost", which resolves to whatever the host says
// and can include an address that is not loopback at all.
const loopbackHost = "127.0.0.1"

// The timeouts on the server. None of them is left unbounded: a client that
// opens a connection and then stops talking holds a goroutine and a file
// descriptor, and on a loopback socket the only client that behaves that way
// is one that means to.
const (
	// readHeaderTimeout bounds the slowest part to abuse: a client that sends
	// a request line and then dribbles headers forever.
	readHeaderTimeout = 5 * time.Second
	// readTimeout bounds headers plus body.
	readTimeout = 30 * time.Second
	// writeTimeout bounds how long a response may take to write.
	writeTimeout = 30 * time.Second
	// idleTimeout bounds a kept-alive connection between requests.
	idleTimeout = 2 * time.Minute
	// maxHeaderBytes caps the headers a request may carry. net/http's default
	// is 1MB, which is a lot of memory to hand an unauthorized caller.
	maxHeaderBytes = 64 << 10
)

// Options configures a Server. The zero value is what the agent uses: every
// field is optional, and the two that matter most, the address and the port,
// are deliberately not fields at all.
type Options struct {
	// AllowedOrigins are origins allowed in addition to the server's own,
	// which is always allowed. Each has to be a bare origin, a scheme, a host
	// and a port; a wildcard or a path is a startup failure, not a rule.
	AllowedOrigins []string
	// Logger receives server events. It never receives the token, nor
	// anything a client presented. A nil Logger discards everything.
	Logger *slog.Logger
}

// Server is the agent's local HTTP API, bound and ready to serve.
type Server struct {
	token    Token
	listener net.Listener
	address  *net.TCPAddr
	origins  *originPolicy
	http     *http.Server
	logger   *slog.Logger
}

// New mints a session token, binds the loopback listener on an ephemeral port
// and builds the guarded handler. It does not serve: when it returns, the port
// is already chosen and reportable, and nothing is answering yet.
func New(options Options) (*Server, error) {
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	token, err := NewToken()
	if err != nil {
		return nil, err
	}

	// Port 0 asks the kernel for a free port, so there is no fixed port to
	// collide with another process or with a second agent.
	listener, err := net.Listen("tcp", net.JoinHostPort(loopbackHost, "0"))
	if err != nil {
		return nil, &ServerError{
			Op: "new", Kind: KindBind,
			Message: "the loopback listener could not be bound",
			Err:     err,
		}
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return nil, &ServerError{
			Op: "new", Kind: KindBind,
			Message: "the loopback listener did not report a TCP address",
		}
	}

	origins, err := newOriginPolicy(address.Port, options.AllowedOrigins)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}

	g := &guard{token: token, origins: origins, logger: logger}
	server := &Server{
		token:    token,
		listener: listener,
		address:  address,
		origins:  origins,
		logger:   logger,
		http: &http.Server{
			Handler:           newHandler(g, apiRoutes()),
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
			// The server's own log goes through slog as well, so a connection
			// error cannot reach stderr by a path nothing here controls.
			ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelDebug),
		},
	}
	logger.Info("the agent API is bound", "address", server.Addr(), "allowedOrigins", origins.origins())
	return server, nil
}

// Addr is the address the server is bound to: 127.0.0.1 and the port the
// kernel chose.
func (s *Server) Addr() string {
	return s.address.String()
}

// Port is the ephemeral port the kernel chose, known as soon as New returns.
func (s *Server) Port() int {
	return s.address.Port
}

// URL is the base URL of the API, without the token.
func (s *Server) URL() string {
	return "http://" + net.JoinHostPort(loopbackHost, strconv.Itoa(s.Port()))
}

// StartupURL is the URL to open: the base URL carrying the session token, so
// that opening it is what hands the token to the browser session.
//
// This is the one place the token is written out. It goes to the operator's own
// terminal on purpose, and never to the logger.
func (s *Server) StartupURL() string {
	query := url.Values{tokenParameter: []string{s.token.Secret()}}
	return s.URL() + "/?" + query.Encode()
}

// Token is the session token. It redacts itself when formatted, so reaching
// the secret takes a deliberate call to Secret.
func (s *Server) Token() Token {
	return s.token
}

// AllowedOrigins is the origin allowlist, the server's own origin included.
func (s *Server) AllowedOrigins() []string {
	return s.origins.origins()
}

// Serve answers requests on the listener New bound, and blocks until the
// server is shut down or closed. A clean stop is not an error.
func (s *Server) Serve() error {
	err := s.http.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	if err != nil {
		return &ServerError{Op: "serve", Kind: KindBind, Message: "the agent API stopped serving", Err: err}
	}
	return nil
}

// Shutdown stops the server gracefully: no new connections, and in-flight
// requests get until ctx is done to finish.
func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.http.Shutdown(ctx); err != nil {
		return &ServerError{Op: "shutdown", Kind: KindShutdown, Message: "the agent API did not shut down cleanly", Err: err}
	}
	s.logger.Info("the agent API has shut down", "address", s.Addr())
	return nil
}

// Close stops the server at once, dropping anything in flight. It is what a
// test defers and what a failed startup path uses.
func (s *Server) Close() error {
	if err := s.http.Close(); err != nil {
		return &ServerError{Op: "shutdown", Kind: KindShutdown, Message: "the agent API did not close cleanly", Err: err}
	}
	return nil
}
