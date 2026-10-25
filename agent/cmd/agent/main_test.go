// What startup prints, and where the token does and does not go.

package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStartupPrintsTheURLCarryingTheToken(t *testing.T) {
	isolateConfig(t)
	var printed, logged bytes.Buffer
	server, err := start(&printed, slog.New(slog.NewTextHandler(&logged, nil)))
	if err != nil {
		t.Fatalf("start the agent: %v", err)
	}
	defer func() { _ = server.Close() }()

	secret := server.Token().Secret()
	lines := printed.String()

	if !strings.Contains(lines, server.StartupURL()) {
		t.Fatalf("startup did not print the URL to open:\n%s", lines)
	}
	if !strings.Contains(lines, secret) {
		t.Fatalf("the printed URL does not carry the session token:\n%s", lines)
	}
	if !strings.Contains(lines, server.Addr()) {
		t.Errorf("startup did not print the address it is listening on:\n%s", lines)
	}

	// The printed URL is usable: it parses, it points at the loopback port the
	// kernel chose, and the token in it is the live one.
	for _, field := range strings.Fields(lines) {
		if !strings.HasPrefix(field, "http://") {
			continue
		}
		parsed, parseErr := url.Parse(field)
		if parseErr != nil {
			t.Fatalf("startup printed %q, which does not parse: %v", field, parseErr)
		}
		if parsed.Query().Get("token") != secret {
			t.Fatalf("startup printed %q, which does not carry the session token", field)
		}
	}

	// The log is the thing that gets redirected to a file and pasted into an
	// issue, so the token may not be in it.
	if strings.Contains(logged.String(), secret) {
		t.Fatalf("the session token reached the log:\n%s", logged.String())
	}
	if logged.Len() == 0 {
		t.Error("startup logged nothing at all, so the check above proves little")
	}
}

// The agent as started browses the user's home directory without it being
// authorized, and a home it cannot open does not stop it starting.
func TestTheStartedAgentBrowsesTheHomeDirectory(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	if err := os.Mkdir(filepath.Join(home, "code"), 0o755); err != nil {
		t.Fatalf("make a directory in home: %v", err)
	}

	for name, test := range map[string]struct {
		home   string
		status int
		want   string
	}{
		"a home it can open": {home, http.StatusOK, `"path":` + strconv.Quote(home)},
		"no home at all":     {filepath.Join(home, "missing"), http.StatusBadRequest, `"error":"invalid"`},
	} {
		t.Run(name, func(t *testing.T) {
			isolateConfig(t)
			t.Setenv("HOME", test.home)
			var printed bytes.Buffer
			server, err := start(&printed, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatalf("start the agent: %v", err)
			}
			defer func() { _ = server.Close() }()
			go func() { _ = server.Serve() }()

			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL()+"/api/directories", nil)
			if err != nil {
				t.Fatalf("build the request: %v", err)
			}
			request.Header.Set("Authorization", "Bearer "+server.Token().Secret())
			request.Header.Set("Origin", server.URL())
			answer, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatalf("ask for the home directory: %v", err)
			}
			defer func() { _ = answer.Body.Close() }()
			body, err := io.ReadAll(answer.Body)
			if err != nil {
				t.Fatalf("read the answer: %v", err)
			}
			if answer.StatusCode != test.status || !strings.Contains(string(body), test.want) {
				t.Fatalf("got %d %s, want %d with %s", answer.StatusCode, body, test.status, test.want)
			}
		})
	}
}

// isolateConfig points the user config directory at a directory of the test's
// own, so starting the agent never reads or writes the real store of
// authorized roots.
func isolateConfig(t *testing.T) string {
	t.Helper()
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	return config
}

// The authorized roots are kept in kpv/roots.json under the user config
// directory, and a restarted agent lists and browses them again.
func TestTheStartedAgentKeepsItsAuthorizedRootsAcrossARestart(t *testing.T) {
	config := isolateConfig(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	home := filepath.Join(base, "home")
	project := filepath.Join(home, "code", "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("make the project: %v", err)
	}
	t.Setenv("HOME", home)

	call := func(server interface {
		URL() string
	}, token, method, path, body string) (int, string) {
		t.Helper()
		var sent io.Reader
		if body != "" {
			sent = strings.NewReader(body)
		}
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL()+path, sent)
		if err != nil {
			t.Fatalf("build the request: %v", err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Origin", server.URL())
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		answer, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer func() { _ = answer.Body.Close() }()
		read, err := io.ReadAll(answer.Body)
		if err != nil {
			t.Fatalf("read the answer: %v", err)
		}
		return answer.StatusCode, string(read)
	}
	startAgent := func() (*bytes.Buffer, func(method, path, body string) (int, string), func()) {
		var printed, logged bytes.Buffer
		server, err := start(&printed, slog.New(slog.NewTextHandler(&logged, nil)))
		if err != nil {
			t.Fatalf("start the agent: %v", err)
		}
		go func() { _ = server.Serve() }()
		secret := server.Token().Secret()
		return &logged, func(method, path, body string) (int, string) {
			return call(server, secret, method, path, body)
		}, func() { _ = server.Close() }
	}

	logged, do, stop := startAgent()
	status, body := do(http.MethodPost, "/api/roots", `{"path":`+strconv.Quote(project)+`}`)
	if status != http.StatusOK || body != `{"path":`+strconv.Quote(project)+"}\n" {
		t.Fatalf("authorize answered %d %s", status, body)
	}
	firstLog := logged.String()
	stop()

	store := filepath.Join(config, "kpv", "roots.json")
	info, err := os.Stat(store)
	if err != nil {
		t.Fatalf("the store is not where it is documented: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("the store has mode %o", info.Mode().Perm())
	}
	if strings.Contains(firstLog, project) {
		t.Fatalf("the log names the root:\n%s", firstLog)
	}

	_, do, stop = startAgent()
	defer stop()
	status, body = do(http.MethodGet, "/api/roots", "")
	if status != http.StatusOK || body != `{"roots":[{"available":true,"path":`+strconv.Quote(project)+"}]}\n" {
		t.Fatalf("after the restart, the roots are %d %s", status, body)
	}
	status, body = do(http.MethodGet, "/api/directories?path="+url.QueryEscape(project), "")
	if status != http.StatusOK || !strings.Contains(body, `"path":`+strconv.Quote(project)) {
		t.Fatalf("after the restart, the root browses as %d %s", status, body)
	}
}
