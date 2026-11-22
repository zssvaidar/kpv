//go:build unix

// Credentials and the token (#24, #23, #4), and rejections that say
// nothing: after every corpus and after a restart of the binary, no canary
// and no token is anywhere a client, the log or the disk could show it.

package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// encodings is text as raw bytes, base64, hex, JSON-escaped and
// URL-escaped.
func encodings(text string) []string {
	quoted, _ := json.Marshal(text)
	out := []string{
		text,
		base64.StdEncoding.EncodeToString([]byte(text)),
		base64.RawStdEncoding.EncodeToString([]byte(text)),
		base64.URLEncoding.EncodeToString([]byte(text)),
		base64.RawURLEncoding.EncodeToString([]byte(text)),
		hex.EncodeToString([]byte(text)),
		strings.ToUpper(hex.EncodeToString([]byte(text))),
		string(quoted[1 : len(quoted)-1]),
		url.QueryEscape(text),
		url.PathEscape(text),
	}
	return out
}

// credentialNeedles is every form a canary credential may not take: the
// password and the key in every encoding, and each line of the key's PEM
// body long enough not to occur by chance.
func (f *fixture) credentialNeedles() map[string]string {
	needles := map[string]string{}
	for _, form := range encodings(f.password) {
		needles[form] = "the SASL password"
	}
	for _, form := range encodings(f.key) {
		needles[form] = "the client key"
	}
	block, _ := pem.Decode([]byte(f.key))
	if block != nil {
		for _, form := range []string{hex.EncodeToString(block.Bytes), base64.StdEncoding.EncodeToString(block.Bytes)} {
			needles[form] = "the client key's DER"
		}
	}
	for _, line := range strings.Split(f.key, "\n") {
		if len(line) >= 16 && !strings.HasPrefix(line, "-----") {
			needles[line] = "a line of the client key's PEM body"
		}
	}
	return needles
}

// credentialErrorPaths sends a canary in every kind of refused connection
// body: each refusal and its log lines are free of it.
func (f *fixture) credentialErrorPaths(t *testing.T) {
	canary := randomText(t, "ERRPATHCANARY")
	pemCanary := "-----BEGIN PRIVATE KEY-----\n" + base64.StdEncoding.EncodeToString([]byte(canary)) + "\n-----END PRIVATE KEY-----\n"
	sasl := func(extra string) string {
		return `{"name":"errpath","bootstrapServers":["localhost:9092"],"securityProtocol":"SASL_SSL","saslMechanism":"PLAIN","saslUsername":"u"` + extra + `}`
	}
	bodies := map[string]string{
		"a create invalid in another field":  `{"name":"errpath","bootstrapServers":["not a server"],"securityProtocol":"SASL_PLAINTEXT","saslMechanism":"PLAIN","saslUsername":"u","saslPassword":` + quote(canary) + `}`,
		"a credential over its length limit": sasl(`,"saslPassword":` + quote(canary+strings.Repeat("x", 2048))),
		"a malformed PEM key":                sasl(`,"saslPassword":` + quote(canary) + `,"tlsClientCertificate":` + quote(f.certificate) + `,"tlsClientKey":` + quote(pemCanary)),
		"a PEM key that is not PEM":          sasl(`,"saslPassword":"p","tlsClientCertificate":` + quote(f.certificate) + `,"tlsClientKey":` + quote(canary)),
		"an unknown key's name and value":    sasl(`,"saslPassword":"p",` + quote(canary) + `:` + quote(canary)),
		"a duplicate credential key":         sasl(`,"saslPassword":` + quote(canary) + `,"saslPassword":` + quote(canary)),
		"a duplicate key credential":         sasl(`,"tlsClientKey":` + quote(canary) + `,"tlsClientKey":` + quote(canary)),
	}
	needles := encodings(canary)
	needles = append(needles, encodings(base64.StdEncoding.EncodeToString([]byte(canary)))...)
	for name, body := range bodies {
		for _, method := range []string{"POST", "PUT"} {
			sent := body
			if method == "PUT" {
				sent = `{"current":` + quote(f.spare) + `,` + body[1:]
			}
			logged := len(f.a.stderr.Bytes())
			header := f.a.authorized()
			header.Set("Content-Type", "application/json")
			got := f.a.send(request{method: method, target: "/api/connections", body: []byte(sent), header: header})
			if got.status == http.StatusOK {
				t.Errorf("%s %s was accepted: %s", method, name, got.body)
			}
			log := string(f.a.stderr.Bytes()[logged:])
			for _, needle := range needles {
				if strings.Contains(got.wire(), needle) {
					t.Errorf("%s %s: the refusal carries the canary: %s", method, name, got.body)
				}
				if strings.Contains(log, needle) {
					t.Errorf("%s %s: the log carries the canary", method, name)
				}
			}
		}
	}
}

// searchable is every place a canary or the token may not be, by name.
func (f *fixture) searchable(a *agent) map[string]string {
	places := map[string]string{}
	for i, got := range a.recorded() {
		places["the answer to "+got.method+" "+shortPath(got.target)+" #"+itoa(i)] = got.wire()
	}
	for i, frame := range f.channelFrames() {
		places["channel frame #"+itoa(i)] = string(frame)
	}
	places["stdout after the startup lines"] = string(a.stdout.Bytes()[a.startupBytes:])
	places["stderr"] = string(a.stderr.Bytes())
	return places
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// filesToSearch is every regular file under the scratch home but
// connections.json, which is where the credentials are meant to be.
func (f *fixture) filesToSearch(t *testing.T) map[string]string {
	places := map[string]string{}
	for _, path := range filesUnder(t, f.s.home) {
		if path == filepath.Join(f.s.settings, "connections.json") {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			continue // a planted unreadable file
		}
		places[path] = string(body)
	}
	return places
}

func (f *fixture) checkNoLeak(t *testing.T, a *agent, phase string) {
	t.Helper()
	credentials := f.credentialNeedles()
	places := f.searchable(a)
	for name, text := range places {
		for needle, what := range credentials {
			if strings.Contains(text, needle) {
				t.Errorf("%s: %s is in %s", phase, what, name)
			}
		}
		if strings.Contains(text, f.envCanary) {
			t.Errorf("%s: the pipeline's .env value is in %s", phase, name)
		}
		if strings.Contains(text, a.token) {
			t.Errorf("%s: the token is in %s", phase, name)
		}
	}
	for name, text := range f.filesToSearch(t) {
		for needle, what := range credentials {
			if strings.Contains(text, needle) {
				t.Errorf("%s: %s is in the file %s", phase, what, name)
			}
		}
	}
	// The token is in no file at all, the stores included.
	for _, path := range filesUnder(t, f.s.base) {
		body, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(body), a.token) {
			t.Errorf("%s: the token is in the file %s", phase, path)
		}
	}
	// And on stdout it is on the open line only.
	for _, line := range strings.Split(string(a.stdout.Bytes()), "\n") {
		if strings.Contains(line, a.token) && !strings.HasPrefix(line, "open ") {
			t.Errorf("%s: the token is on the stdout line %q", phase, line)
		}
	}
}

func (f *fixture) nothingLeaks(t *testing.T) {
	// Every route answers once more, successes included, so the corpus's
	// answers end with what the canary connection looks like now.
	for _, route := range f.routes {
		if route.changesState() || route.row.channel {
			continue
		}
		target, _ := route.build(t, route.row.valid(f))
		f.a.send(request{method: route.method, target: target, header: f.a.authorized()})
	}
	if got := f.call(t, "DELETE", "/api/connections", map[string]any{"name": f.connection}); errorKind(got) != "referenced" {
		t.Errorf("deleting the canary connection a pipeline names answered %d %s, want referenced", got.status, got.body)
	}
	f.checkNoLeak(t, f.a, "after the whole corpus")
	if !strings.Contains(string(f.call(t, "GET", "/api/connections", nil).body), `"saslPasswordSet":true`) {
		t.Fatal("the canary connection no longer holds its password, so the leak checks prove little")
	}

	f.a.stop()
	restarted := startAgent(t, f.s, nil)
	f.a = restarted
	f.subscribe(t)
	listed := f.call(t, "GET", "/api/connections", nil)
	if !strings.Contains(string(listed.body), `"tlsClientKeySet":true`) {
		t.Errorf("after a restart the canary connection is %s", listed.body)
	}
	for _, route := range f.routes {
		if route.row.channel {
			continue
		}
		target, body := route.build(t, route.row.valid(f))
		header := f.a.authorized()
		if body != nil {
			header.Set("Content-Type", "application/json")
		}
		f.a.send(request{method: route.method, target: target, body: body, header: header})
	}
	// A pipeline in the second root names the canary connection, so its
	// delete is refused referenced, and that answer is searched too.
	if got := f.call(t, "DELETE", "/api/connections", map[string]any{"name": f.connection}); errorKind(got) != "referenced" {
		t.Errorf("deleting the canary connection a pipeline names answered %d %s, want referenced", got.status, got.body)
	}
	f.checkNoLeak(t, restarted, "after a restart")
}

// refusalWords are what no authorized refusal may contain.
var refusalWords = []string{"no such file", "permission denied", "not a directory", "too many levels", "errno", "*fs.PathError", "goroutine ", ".go:"}

// rejectionsSayNothing holds that every unauthorized answer the corpus got
// is the same answer, and that no authorized refusal says more than its
// kind: no scratch path, no OS error text, no Go type or stack.
func (f *fixture) rejectionsSayNothing(t *testing.T) {
	uniform := 0
	// The message of each kind on each route is one fixed sentence: a
	// sentence that varied could carry the error behind it.
	sentences := map[string]string{}
	for _, got := range f.a.recorded() {
		if got.status == f.unauthorized.status && (string(got.body) == string(f.unauthorized.body) || got.method == "HEAD") {
			uniform++
			f.expectRejected(t, "an unauthorized answer", got)
			continue
		}
		if got.status < 400 {
			continue
		}
		path, _, _ := strings.Cut(got.target, "?")
		key := got.method + " " + path + " " + errorKind(got)
		message := messageOf(got)
		if was, seen := sentences[key]; seen && was != message {
			t.Errorf("%s answers two sentences: %q and %q", key, was, message)
		}
		sentences[key] = message
		// The fixed sentence is the agent's own wording ("this path is not
		// a directory"), so the words below are looked for in everything
		// else the answer says.
		text := strings.ToLower(withoutListedRoots(got.body, false))
		if strings.Contains(text, strings.ToLower(f.s.base)) {
			t.Errorf("the refusal to %s %s names a scratch path: %s", got.method, shortPath(got.target), shortPath(string(got.body)))
		}
		rest := strings.ToLower(withoutListedRoots(got.body, true))
		for _, word := range refusalWords {
			if strings.Contains(rest, strings.ToLower(word)) {
				t.Errorf("the refusal to %s %s says %q: %s", got.method, shortPath(got.target), word, shortPath(string(got.body)))
			}
		}
	}
	if uniform < 100 {
		t.Errorf("only %d unauthorized answers were recorded, so the corpus did not run", uniform)
	}
}

// withoutListedRoots is a refusal's body, without its fixed sentence when
// asked (the caller holds that sentence fixed separately), and without the
// fields that name a
// listed root, which are the user's own and answered by GET /api/roots
// anyway: an overlaps refusal's root (#63) and a referenced refusal's
// references and unchecked places (#24).
func withoutListedRoots(body []byte, withoutMessage bool) string {
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		return string(body)
	}
	if withoutMessage {
		delete(object, "message")
	}
	if object["error"] == "overlaps" {
		delete(object, "root")
	}
	if object["error"] == "referenced" {
		delete(object, "references")
		delete(object, "unchecked")
	}
	out, _ := json.Marshal(object)
	return string(out)
}
