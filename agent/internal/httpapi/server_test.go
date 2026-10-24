// What the server is bound to, what it reports, and what it refuses to be
// reachable from.

package httpapi

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// dialTimeout bounds a connection attempt in these tests. It is not a wait for
// anything to become true: the expected outcome is an immediate refusal, and
// the timeout only exists so a firewall that silently drops instead of
// refusing cannot hang the test.
const dialTimeout = 5 * time.Second

// nonLoopbackAddress finds an address of this machine that is not loopback, so
// the test can try the agent's port on it. It skips rather than guesses when
// the machine has no external interface, and it hardcodes nothing: a fixed
// address would test the network, not the server.
func nonLoopbackAddress(t *testing.T) string {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("list this machine's interfaces: %v", err)
	}
	for _, device := range interfaces {
		if device.Flags&net.FlagUp == 0 {
			continue
		}
		addresses, err := device.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			network, ok := address.(*net.IPNet)
			if !ok {
				continue
			}
			ip := network.IP
			switch {
			case ip.IsLoopback(), ip.IsUnspecified(), ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(), ip.IsMulticast():
				continue
			}
			return ip.String()
		}
	}
	t.Skip("this machine has no non-loopback address up, so there is nothing to prove the binding against")
	return ""
}

// The binding is the security boundary, so it is proved three ways in one
// test: the agent's port answers on loopback, a listener on the non-loopback
// address does accept connections, and the agent's port on that same
// non-loopback address does not. Without the middle step a refusal could just
// mean the interface is unusable, and the test would prove nothing.
func TestTheServerIsReachableOnLoopbackAndNowhereElse(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})

	if host, _, err := net.SplitHostPort(server.Addr()); err != nil || host != loopbackHost {
		t.Fatalf("the server reports the address %q, want host %q", server.Addr(), loopbackHost)
	}

	onLoopback, err := net.DialTimeout("tcp", net.JoinHostPort(loopbackHost, itoa(server.Port())), dialTimeout)
	if err != nil {
		t.Fatalf("the server's own port does not accept a loopback connection: %v", err)
	}
	_ = onLoopback.Close()

	outside := nonLoopbackAddress(t)

	// A listener of our own on that address, to show the address itself is one
	// a connection can be made to on this machine.
	probe, err := net.Listen("tcp", net.JoinHostPort(outside, "0"))
	if err != nil {
		t.Skipf("this machine will not let a test listen on %s, so the comparison would be meaningless: %v", outside, err)
	}
	defer func() { _ = probe.Close() }()
	probePort := probe.Addr().(*net.TCPAddr).Port
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		connection, err := probe.Accept()
		if err == nil {
			_ = connection.Close()
		}
	}()
	toProbe, err := net.DialTimeout("tcp", net.JoinHostPort(outside, itoa(probePort)), dialTimeout)
	if err != nil {
		t.Skipf("a connection to %s does not work on this machine even for a test's own listener, so the refusal below would prove nothing: %v", outside, err)
	}
	_ = toProbe.Close()
	<-accepted

	// The real assertion: the agent's port, on the address the probe just
	// proved reachable, is not accepting.
	toAgent, err := net.DialTimeout("tcp", net.JoinHostPort(outside, itoa(server.Port())), dialTimeout)
	if err == nil {
		_ = toAgent.Close()
		t.Fatalf("the agent's port %d accepted a connection on the non-loopback address %s, so it is not bound to %s only", server.Port(), outside, loopbackHost)
	}
	t.Logf("a connection to the agent's port on %s was refused: %v", outside, err)
}

// The port is ephemeral and it is knowable before anything is served: New
// returns with the listener already bound, so a caller reads the port and the
// URL and then makes its first request, with nothing to wait for.
func TestThePortIsEphemeralAndKnownBeforeAnythingIsServed(t *testing.T) {
	t.Parallel()

	server, err := New(Options{})
	if err != nil {
		t.Fatalf("bind the agent API: %v", err)
	}
	defer func() { _ = server.Close() }()

	if server.Port() == 0 {
		t.Fatal("the server reports port 0, so no port was chosen")
	}
	if want := net.JoinHostPort(loopbackHost, itoa(server.Port())); server.Addr() != want {
		t.Fatalf("the server reports the address %q, want %q", server.Addr(), want)
	}
	if want := "http://" + net.JoinHostPort(loopbackHost, itoa(server.Port())); server.URL() != want {
		t.Fatalf("the server reports the URL %q, want %q", server.URL(), want)
	}

	// The port is already bound, so a connection succeeds before Serve is
	// ever called. That is what makes a sleep unnecessary anywhere in these
	// tests.
	connection, err := net.DialTimeout("tcp", server.Addr(), dialTimeout)
	if err != nil {
		t.Fatalf("the reported port does not accept a connection before Serve is called: %v", err)
	}
	_ = connection.Close()

	// Two servers at once cannot have been handed the same port, and neither
	// port can be a constant.
	second, err := New(Options{})
	if err != nil {
		t.Fatalf("bind a second agent API: %v", err)
	}
	defer func() { _ = second.Close() }()
	if second.Port() == server.Port() {
		t.Fatalf("two servers both report port %d", server.Port())
	}
}

// Fresh per run, where a run is a process that calls New once. Two servers
// standing side by side is as close as a test gets to two runs, and they must
// not share a token.
func TestEachServerMintsItsOwnToken(t *testing.T) {
	t.Parallel()

	const runs = 16
	seen := make(map[string]struct{}, runs)
	for run := range runs {
		server, err := New(Options{})
		if err != nil {
			t.Fatalf("run %d: bind the agent API: %v", run, err)
		}
		secret := server.Token().Secret()
		_ = server.Close()
		if _, repeated := seen[secret]; repeated {
			t.Fatalf("run %d was handed a token an earlier run already had", run)
		}
		seen[secret] = struct{}{}
	}
}

// Startup has to print a URL that carries the token, because that URL is how
// the token reaches the browser session. This checks the pieces of it rather
// than the whole string, so the shape is pinned without pinning the port.
func TestTheStartupURLCarriesTheTokenAndTheChosenPort(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})

	parsed, err := url.Parse(server.StartupURL())
	if err != nil {
		t.Fatalf("the startup URL does not parse: %v", err)
	}
	if parsed.Scheme != "http" {
		t.Errorf("the startup URL's scheme is %q, want http", parsed.Scheme)
	}
	if want := net.JoinHostPort(loopbackHost, itoa(server.Port())); parsed.Host != want {
		t.Errorf("the startup URL's host is %q, want %q", parsed.Host, want)
	}
	if carried := parsed.Query().Get(tokenParameter); carried != server.Token().Secret() {
		t.Errorf("the startup URL carries %q in the %s parameter, want the session token", carried, tokenParameter)
	}

	// The URL is usable as it stands: opening it is what hands the token over,
	// so the token in it has to be the one the guard accepts.
	answer := server.do(t, request{path: "/api/health", queryToken: parsed.Query().Get(tokenParameter), origins: []string{server.URL()}})
	if answer.Status != http.StatusOK {
		t.Errorf("the token from the startup URL was not accepted: %s", answer)
	}

	// And the base URL, the one that is safe to log, carries no token.
	if strings.Contains(server.URL(), server.Token().Secret()) {
		t.Error("the base URL carries the token")
	}
}

// No timeout may be left unbounded: an unauthorized client that opens a
// connection and stops talking must not be able to hold it forever.
func TestTheServerTimeoutsAreSet(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})

	for name, value := range map[string]time.Duration{
		"ReadHeaderTimeout": server.http.ReadHeaderTimeout,
		"ReadTimeout":       server.http.ReadTimeout,
		"WriteTimeout":      server.http.WriteTimeout,
		"IdleTimeout":       server.http.IdleTimeout,
	} {
		if value <= 0 {
			t.Errorf("%s is %v, which is unbounded", name, value)
		}
	}
	if server.http.ReadHeaderTimeout > server.http.ReadTimeout {
		t.Errorf("ReadHeaderTimeout %v is longer than ReadTimeout %v, so the header timeout can never fire", server.http.ReadHeaderTimeout, server.http.ReadTimeout)
	}
	if server.http.MaxHeaderBytes <= 0 || server.http.MaxHeaderBytes > 1<<20 {
		t.Errorf("MaxHeaderBytes is %d, want a cap below net/http's megabyte default", server.http.MaxHeaderBytes)
	}
}
