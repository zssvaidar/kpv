// Command agent is the kpv agent: a local HTTP API bound to loopback on an
// ephemeral port, reachable only by the browser session that was handed the
// startup URL.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
	"github.com/zssvaidar/kpv/agent/internal/connections"
	"github.com/zssvaidar/kpv/agent/internal/httpapi"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
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
	guard, browser, err := newBrowser(logger)
	if err != nil {
		return nil, err
	}
	roots, err := projectfs.OpenRoots(browser, storeFile(logger), logger)
	if err != nil {
		_ = browser.Close()
		return nil, err
	}
	// The pipeline list, open and create (#48) read and write through the
	// guard the roots are authorized into.
	pipelines, err := catalog.New(roots, guard, logger)
	if err != nil {
		_ = browser.Close()
		return nil, err
	}
	// The named Kafka connections (#24) are kept in a file of the agent's own
	// beside the roots store, which is what keeps them outside every root;
	// with no store there, they last as long as the process. Their reference
	// check reads the pipeline list.
	connectionsFile, err := projectfs.OpenPrivateFile(roots, connectionsStoreName)
	if err != nil {
		_ = browser.Close()
		return nil, err
	}
	store := connections.Open(connectionsFile, pipelines, logger)
	server, err := httpapi.New(httpapi.Options{Logger: logger, Browser: browser, Roots: roots, Pipelines: pipelines, Connections: store})
	if err != nil {
		_ = browser.Close()
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

// newBrowser builds what the directory browsing endpoint reads through: the
// guard holding the authorized project roots, which starts with none, plus the
// user's home directory as a browse-only allowance. The guard is returned too,
// for the pipeline catalog, which reads and writes through it.
//
// A home directory that cannot be found or opened is not a failed startup:
// the agent still serves, and browsing is then limited to the authorized
// roots. Which home it was is not logged, only that there is none.
func newBrowser(logger *slog.Logger) (*projectfs.Guard, *projectfs.Browser, error) {
	guard, err := projectfs.New()
	if err != nil {
		return nil, nil, err
	}
	home, err := os.UserHomeDir()
	if err == nil {
		browser, browseErr := projectfs.NewBrowser(guard, home)
		if browseErr == nil {
			return guard, browser, nil
		}
		err = browseErr
	}
	logger.Warn("the home directory cannot be browsed, so only authorized project directories can", "kind", string(projectfs.KindOf(err)))
	browser, err := projectfs.NewBrowser(guard, "")
	return guard, browser, err
}

// rootsStoreName is the file the authorized project roots are kept in (#63),
// in a kpv directory of the user's config directory: $XDG_CONFIG_HOME/kpv on
// Linux, falling back to ~/.config/kpv. It is the agent's own file, never one
// inside a project or the repository.
const rootsStoreName = "roots.json"

// connectionsStoreName is the file the named Kafka connections are kept in
// (#24), beside rootsStoreName: the agent's own, 0600, never inside a
// project. It holds credentials, which is why it is there and nowhere else.
const connectionsStoreName = "connections.json"

// storeFile is where the authorized roots are stored, or "" when there is no
// config directory to keep them in, in which case they last as long as the
// process. Which directory it was is not logged.
func storeFile(logger *slog.Logger) string {
	config, err := os.UserConfigDir()
	if err != nil {
		logger.Warn("there is no user config directory, so authorized project directories are not kept across restarts")
		return ""
	}
	return filepath.Join(config, "kpv", rootsStoreName)
}
