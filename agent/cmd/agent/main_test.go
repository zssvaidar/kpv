// What startup prints, and where the token does and does not go.

package main

import (
	"bytes"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

func TestStartupPrintsTheURLCarryingTheToken(t *testing.T) {
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
