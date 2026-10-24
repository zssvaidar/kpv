// Command agent is the kpv agent: a local HTTP API bound to loopback on an
// ephemeral port, reachable only by the browser session that was handed the
// startup URL.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/zssvaidar/kpv/agent/internal/httpapi"
	"github.com/zssvaidar/kpv/agent/internal/version"
)

func main() {
	server, err := start(os.Stdout, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "kpv agent: %v\n", err)
		os.Exit(1)
	}
	if err := server.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "kpv agent: %v\n", err)
		os.Exit(1)
	}
}

// start binds the API and writes the startup lines to out, returning before
// anything is served. It is separate from main so that a test can read what
// startup prints without having to stop a serving process.
//
// out is the operator's terminal and is the one place the session token is
// written: the URL has to carry it, because opening that URL is how the token
// reaches the browser. The logger deliberately gets the address only, so a
// redirected log never contains the token.
func start(out io.Writer, logger *slog.Logger) (*httpapi.Server, error) {
	server, err := httpapi.New(httpapi.Options{Logger: logger})
	if err != nil {
		return nil, err
	}
	lines := fmt.Sprintf("kpv agent %s\nlistening on %s\nopen %s\n", version.String(), server.Addr(), server.StartupURL())
	if _, err := io.WriteString(out, lines); err != nil {
		// Nothing can reach the API if the operator never saw the URL, so a
		// terminal that cannot be written to is a failed startup, not a
		// warning: the server is closed again rather than left listening with
		// a token nobody has.
		_ = server.Close()
		return nil, fmt.Errorf("the startup lines could not be written: %w", err)
	}
	return server, nil
}
