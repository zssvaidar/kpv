//go:build unix

// The security boundary suite (#46): the built agent binary, started as a
// child process with a scratch HOME, XDG_CONFIG_HOME and PATH, attacked as a
// hostile local client over HTTP and the channel. This file is the harness:
// building and starting the binary, the scratch directories, a client that
// records every answer, and snapshots of the disk. The routes it attacks come
// from security_routes_test.go, and TestSecurityBoundary in
// security_suite_test.go runs every corpus against one agent.

package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// startupTimeout bounds how long the child may take to print its startup
// lines. It is a bound on a wait, not a sleep.
const startupTimeout = 60 * time.Second

// answerTimeout bounds one request. Every server timeout the suite relies on
// is shorter than this except readTimeout, which no case waits out.
const answerTimeout = 20 * time.Second

// scratch is every directory the agent under attack is given. base holds all
// of them, so a walk of base sees everything the agent could have written.
type scratch struct {
	base     string // everything below
	home     string // HOME
	config   string // XDG_CONFIG_HOME, inside home as ~/.config is
	settings string // the agent's own directory, config/kpv
	bin      string // PATH: recording stand-ins only
	markers  string // where the stand-ins record a run
	work     string // the child's working directory
	outside  string // a directory outside home, holding canaries
}

func newScratch(t *testing.T) *scratch {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the scratch directory: %v", err)
	}
	s := &scratch{
		base:    base,
		home:    filepath.Join(base, "home"),
		bin:     filepath.Join(base, "bin"),
		markers: filepath.Join(base, "markers"),
		work:    filepath.Join(base, "work"),
		outside: filepath.Join(base, "outside"),
	}
	s.config = filepath.Join(s.home, ".config")
	s.settings = filepath.Join(s.config, "kpv")
	for _, dir := range []string{s.home, s.config, s.bin, s.markers, s.work, s.outside} {
		mustMkdir(t, dir)
	}
	return s
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make %s: %v", dir, err)
	}
}

func mustWrite(t *testing.T, path string, body string, mode os.FileMode) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustLink(t *testing.T, target, link string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(link))
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("link %s to %s: %v", link, target, err)
	}
}

// randomText is a canary: random enough that it appears nowhere by chance.
func randomText(t *testing.T, prefix string) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("read random bytes: %v", err)
	}
	return prefix + hex.EncodeToString(raw)
}

var (
	buildOnce   sync.Once
	builtBinary string
	buildErr    error
)

// agentBinary builds agent/cmd/agent once per test process with the go
// command on the test's own PATH. The binary sits in a directory of its own
// that the process removes on exit.
func agentBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "kpv-security-agent-")
		if err != nil {
			buildErr = err
			return
		}
		builtBinary = filepath.Join(dir, "agent")
		command := exec.Command("go", "build", "-o", builtBinary, ".")
		output, err := command.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build: %w\n%s", err, output)
		}
	})
	if buildErr != nil {
		t.Fatalf("build the agent: %v", buildErr)
	}
	return builtBinary
}

// lockedBuffer collects what a child writes to one of its streams.
type lockedBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}

func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data.Bytes()...)
}

// answer is one response as a client sees it.
type answer struct {
	method, target string
	status         int
	header         http.Header
	body           []byte
}

// wire is the answer's headers and body as one text, which is what the leak
// checks search.
func (a answer) wire() string {
	var out strings.Builder
	fmt.Fprintf(&out, "%d\n", a.status)
	for name, values := range a.header {
		for _, value := range values {
			fmt.Fprintf(&out, "%s: %s\n", name, value)
		}
	}
	out.Write(a.body)
	return out.String()
}

// agent is one run of the binary.
type agent struct {
	t       *testing.T
	s       *scratch
	command *exec.Cmd
	addr    string // 127.0.0.1:port
	port    string
	origin  string // http://127.0.0.1:port, the agent's own origin
	token   string
	stdout  *lockedBuffer
	stderr  *lockedBuffer
	// startupBytes is how much of stdout the startup lines are.
	startupBytes int
	client       *http.Client
	exited       chan struct{}
	// answers is every answer any request got, kept for the leak checks.
	mu      sync.Mutex
	answers []answer
	frames  *[][]byte
}

var openLine = regexp.MustCompile(`(?m)^open (\S+)$`)

// startAgent runs the binary with HOME, XDG_CONFIG_HOME and PATH pointing at
// the scratch directories and nothing else in its environment, and reads the
// token from the open line.
func startAgent(t *testing.T, s *scratch, frames *[][]byte) *agent {
	t.Helper()
	command := exec.Command(agentBinary(t))
	command.Env = []string{"HOME=" + s.home, "XDG_CONFIG_HOME=" + s.config, "PATH=" + s.bin}
	command.Dir = s.work
	a := &agent{t: t, s: s, command: command, stdout: &lockedBuffer{}, stderr: &lockedBuffer{}, exited: make(chan struct{}), frames: frames}
	command.Stderr = a.stderr
	pipe, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("pipe the agent's stdout: %v", err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start the agent: %v", err)
	}
	lines := make(chan struct{})
	go func() {
		reader := bufio.NewReader(pipe)
		signalled := false
		for {
			line, err := reader.ReadString('\n')
			_, _ = a.stdout.Write([]byte(line))
			if !signalled && strings.HasPrefix(line, "open ") {
				signalled = true
				close(lines)
			}
			if err != nil {
				break
			}
		}
		_ = command.Wait()
		close(a.exited)
	}()
	select {
	case <-lines:
	case <-a.exited:
		t.Fatalf("the agent exited before it printed its open line:\n%s\n%s", a.stdout.Bytes(), a.stderr.Bytes())
	case <-time.After(startupTimeout):
		_ = command.Process.Kill()
		t.Fatalf("the agent printed no open line within %s", startupTimeout)
	}
	t.Cleanup(a.stop)

	printed := a.stdout.Bytes()
	a.startupBytes = len(printed)
	match := openLine.FindSubmatch(printed)
	if match == nil {
		t.Fatalf("no open line in %q", printed)
	}
	parsed, err := url.Parse(string(match[1]))
	if err != nil {
		t.Fatalf("the open line's URL does not parse: %v", err)
	}
	a.token = parsed.Query().Get("token")
	a.addr = parsed.Host
	_, a.port, _ = net.SplitHostPort(a.addr)
	a.origin = "http://" + a.addr
	if a.token == "" || a.port == "" {
		t.Fatalf("the open line %q carries no token or port", match[1])
	}
	a.client = &http.Client{
		Timeout:       answerTimeout,
		Transport:     &http.Transport{DisableCompression: true, MaxIdleConnsPerHost: 4},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return a
}

// stop kills the child and waits for it.
func (a *agent) stop() {
	select {
	case <-a.exited:
		return
	default:
	}
	_ = a.command.Process.Kill()
	<-a.exited
	a.client.CloseIdleConnections()
}

// pid is the child's process id.
func (a *agent) pid() int { return a.command.Process.Pid }

// record keeps an answer for the leak checks.
func (a *agent) record(got answer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.answers = append(a.answers, got)
}

func (a *agent) recorded() []answer {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]answer(nil), a.answers...)
}

// request is one request to send. target is sent exactly as written: path
// and query, never cleaned or re-encoded by the client.
type request struct {
	method string
	target string
	body   []byte
	header http.Header
	host   string // a Host other than the agent's address
	// chunked sends the body with no Content-Length.
	chunked bool
}

// authorized is the header of a request that passes the checks: the token as
// a bearer and the agent's own origin.
func (a *agent) authorized() http.Header {
	return http.Header{"Authorization": {"Bearer " + a.token}, "Origin": {a.origin}}
}

// send sends r and returns the answer, recording it.
func (a *agent) send(r request) answer {
	a.t.Helper()
	got, err := a.trySend(r)
	if err != nil {
		a.t.Fatalf("%s %s: %v", r.method, r.target, err)
	}
	return got
}

func (a *agent) trySend(r request) (answer, error) {
	var body io.Reader
	if r.body != nil {
		body = bytes.NewReader(r.body)
		if r.chunked {
			// A plain io.Reader, so the client cannot learn the length
			// and sends the body chunked.
			body = io.MultiReader(bytes.NewReader(r.body))
		}
	}
	built, err := http.NewRequest(r.method, a.origin+"/", body)
	if err != nil {
		return answer{}, err
	}
	// Opaque is sent as the request target verbatim, so ".." and "%2e" reach
	// the server as written.
	path, query, _ := strings.Cut(r.target, "?")
	built.URL.Opaque = path
	built.URL.RawQuery = query
	if r.chunked {
		built.ContentLength = -1
	}
	for name, values := range r.header {
		for _, value := range values {
			built.Header.Add(name, value)
		}
	}
	if r.host != "" {
		built.Host = r.host
	}
	response, err := a.client.Do(built)
	if err != nil {
		return answer{}, err
	}
	defer func() { _ = response.Body.Close() }()
	read, err := io.ReadAll(response.Body)
	if err != nil {
		return answer{}, err
	}
	got := answer{method: r.method, target: r.target, status: response.StatusCode, header: response.Header.Clone(), body: read}
	a.record(got)
	return got, nil
}

// sameAnswer reports how two answers differ in status, body or any header
// but Date, or "" when they do not.
func sameAnswer(want, got answer) string {
	if want.status != got.status {
		return fmt.Sprintf("status %d, want %d", got.status, want.status)
	}
	if !bytes.Equal(want.body, got.body) {
		return fmt.Sprintf("body %q, want %q", got.body, want.body)
	}
	names := map[string]bool{}
	for name := range want.header {
		names[name] = true
	}
	for name := range got.header {
		names[name] = true
	}
	for name := range names {
		if name == "Date" {
			continue
		}
		if strings.Join(want.header.Values(name), "\x00") != strings.Join(got.header.Values(name), "\x00") {
			return fmt.Sprintf("header %s %q, want %q", name, got.header.Values(name), want.header.Values(name))
		}
	}
	return ""
}

// entry is one name under a snapshot: its type and mode, its bytes, its
// link target.
type entry struct {
	mode   fs.FileMode
	body   string
	target string
}

// snapshot reads everything under root, without following a link and
// without opening anything but a regular file, so a FIFO cannot block it.
// skip leaves out names under it.
func snapshot(t *testing.T, root string, skip ...string) map[string]entry {
	t.Helper()
	out := map[string]entry{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable planted directory is recorded as it is.
			if path != root {
				out[path] = entry{body: "unreadable"}
				return fs.SkipDir
			}
			return err
		}
		for _, skipped := range skip {
			if path == skipped || strings.HasPrefix(path, skipped+string(filepath.Separator)) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		e := entry{mode: info.Mode()}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			e.target, err = os.Readlink(path)
		case info.Mode().IsRegular() && info.Mode().Perm()&0o400 != 0:
			var body []byte
			body, err = os.ReadFile(path)
			e.body = string(body)
		}
		if err != nil {
			return err
		}
		out[path] = e
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return out
}

// diffSnapshots names every path that was created, removed or changed.
func diffSnapshots(before, after map[string]entry) []string {
	var changed []string
	for path, was := range before {
		now, still := after[path]
		switch {
		case !still:
			changed = append(changed, "removed "+path)
		case now != was:
			changed = append(changed, "changed "+path)
		}
	}
	for path := range after {
		if _, was := before[path]; !was {
			changed = append(changed, "created "+path)
		}
	}
	sort.Strings(changed)
	return changed
}

// unchanged fails t when anything under root differs from before.
func unchanged(t *testing.T, what string, before map[string]entry, root string, skip ...string) {
	t.Helper()
	if changed := diffSnapshots(before, snapshot(t, root, skip...)); len(changed) != 0 {
		t.Errorf("%s changed the disk: %v", what, changed)
	}
}

// filesUnder lists every regular file under root.
func filesUnder(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	for path, e := range snapshot(t, root) {
		if e.mode.IsRegular() {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files
}

// shortPath keeps a failure message readable when the target is long.
func shortPath(target string) string {
	if len(target) > 160 {
		return target[:160] + fmt.Sprintf("...(%d bytes)", len(target))
	}
	return target
}
