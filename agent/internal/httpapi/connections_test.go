// The connection endpoints (#24), against a real server, a real store beside
// a scratch roots store, and a real catalog over a scratch home.

package httpapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
	"github.com/zssvaidar/kpv/agent/internal/connections"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

const (
	leakPassword = "http-leak-canary-pw-8b3d"
	jsonType     = "application/json"
)

type connectionsTree struct {
	home, root, storeFile string
	server                *testServer
	storeLogs             *safeBuffer
}

func newConnectionsTree(t *testing.T) *connectionsTree {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tree := &connectionsTree{home: filepath.Join(base, "home")}
	tree.root = filepath.Join(tree.home, "code")
	mustMkdir(t, tree.root)
	guard, err := projectfs.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	browser, err := projectfs.NewBrowser(guard, tree.home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	rootsFile := filepath.Join(tree.home, ".config", "kpv", "roots.json")
	tree.storeFile = filepath.Join(filepath.Dir(rootsFile), "connections.json")
	roots, err := projectfs.OpenRoots(browser, rootsFile, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := roots.Authorize(tree.root); err != nil {
		t.Fatal(err)
	}
	pipelines, err := catalog.New(roots, guard, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	private, err := projectfs.OpenPrivateFile(roots, "connections.json")
	if err != nil {
		t.Fatal(err)
	}
	tree.storeLogs = &safeBuffer{}
	store := connections.Open(private, pipelines, slog.New(slog.NewTextHandler(tree.storeLogs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	tree.server = startServer(t, Options{Browser: browser, Roots: roots, Pipelines: pipelines, Connections: store})
	return tree
}

func (tree *connectionsTree) send(t *testing.T, method, body string) response {
	t.Helper()
	want := tree.server.allowed("/api/connections")
	want.method = method
	want.body = body
	want.headers = http.Header{"Content-Type": {jsonType}}
	return tree.server.do(t, want)
}

func (tree *connectionsTree) get(t *testing.T, path string) response {
	t.Helper()
	return tree.server.do(t, tree.server.allowed(path))
}

func testCertificateAndKey(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "kpv"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
}

func connectionBody(t *testing.T, fields map[string]any) string {
	t.Helper()
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func saslFields(t *testing.T, name string) map[string]any {
	certificate, key := testCertificateAndKey(t)
	return map[string]any{
		"name": name, "bootstrapServers": []string{"broker:9093"}, "securityProtocol": "SASL_SSL",
		"saslMechanism": "PLAIN", "saslUsername": "svc", "saslPassword": leakPassword,
		"tlsClientCertificate": certificate, "tlsClientKey": key,
	}
}

// connectionsFailureOf checks an answer is the schema's error with the
// status, kind and fixed sentence wanted.
func connectionsFailureOf(t *testing.T, answer response, kind ConnectionsErrorKind) ConnectionsError {
	t.Helper()
	want := connectionsFailures[kind]
	if answer.Status != want.status {
		t.Fatalf("want %d %s, got %s", want.status, kind, answer)
	}
	if err := connectionsSchemas(t).failure.Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("the error is not valid against the schema: %v\n%s", err, answer.Body)
	}
	var failure ConnectionsError
	if err := json.Unmarshal([]byte(answer.Body), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error != kind || failure.Message != want.message {
		t.Fatalf("want a %s error with its fixed message, got %+v", kind, failure)
	}
	if (failure.Problems != nil) && kind != ConnectionsErrorKindInvalid {
		t.Fatalf("a %s error has problems", kind)
	}
	if (failure.References != nil || failure.Unchecked != nil) != (kind == ConnectionsErrorKindReferenced) {
		t.Fatalf("a %s error has a report %+v", kind, failure)
	}
	return failure
}

func summaryFrom(t *testing.T, answer response) ConnectionSummary {
	t.Helper()
	if answer.Status != http.StatusOK {
		t.Fatalf("want a 200 summary, got %s", answer)
	}
	if err := connectionsSchemas(t).summary.Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("not a summary: %v\n%s", err, answer.Body)
	}
	var summary ConnectionSummary
	if err := json.Unmarshal([]byte(answer.Body), &summary); err != nil {
		t.Fatal(err)
	}
	return summary
}

var connectionPatterns = []string{
	connectionsListPattern, connectionsNamesPattern, connectionsCreatePattern, connectionsUpdatePattern, connectionsDeletePattern,
}

func TestTheConnectionEndpointsAreBehindTheChecks(t *testing.T) {
	t.Parallel()
	tree := newConnectionsTree(t)
	server := tree.server
	headers := http.Header{"Content-Type": {jsonType}}
	body := connectionBody(t, saslFields(t, "attacker"))
	for _, pattern := range connectionPatterns {
		method, path := methodAndPathOf(t, pattern)
		for name, want := range map[string]request{
			"no token":       {method: method, path: path, origins: []string{server.URL()}, body: body, headers: headers},
			"a wrong token":  {method: method, path: path, token: "wrong", origins: []string{server.URL()}, body: body, headers: headers},
			"a bad origin":   {method: method, path: path, token: server.Token().Secret(), origins: []string{"https://evil.example"}, body: body, headers: headers},
			"no origin":      {method: method, path: path, token: server.Token().Secret(), body: body, headers: headers},
			"nothing at all": {method: method, path: path},
		} {
			answer := server.do(t, want)
			if answer.Status != rejectionStatus || answer.Body != rejectionBody {
				t.Errorf("%s with %s was not rejected: %s", pattern, name, answer)
			}
		}
		recorded := httptest.NewRecorder()
		server.http.Handler.ServeHTTP(recorded, httptest.NewRequest(method, "http://"+server.Addr()+path, strings.NewReader(body)))
		if recorded.Code != rejectionStatus || recorded.Body.String() != rejectionBody {
			t.Errorf("the served handler answered an unauthorized %s with %d %q", pattern, recorded.Code, recorded.Body.String())
		}
	}
	// A change with no Origin is refused even when fetch metadata vouches.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		want := server.pageFetch("/api/connections")
		want.method = method
		want.body = body
		want.headers.Set("Content-Type", jsonType)
		if answer := server.do(t, want); answer.Status != rejectionStatus {
			t.Errorf("an origin-less %s was answered %s", method, answer)
		}
	}
	if _, err := os.Stat(tree.storeFile); !os.IsNotExist(err) {
		t.Errorf("a rejected request wrote the store: %v", err)
	}
	for _, path := range []string{"/api/connections", "/api/connections/names"} {
		if answer := server.do(t, server.pageFetch(path)); answer.Status != http.StatusOK {
			t.Errorf("the page's own %s was answered %s", path, answer)
		}
	}
}

func TestAServerWithNoStoreRejectsTheConnectionEndpointsUniformly(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	for _, pattern := range connectionPatterns {
		method, path := methodAndPathOf(t, pattern)
		want := server.allowed(path)
		want.method = method
		want.body = `{"name":"a"}`
		want.headers = http.Header{"Content-Type": {jsonType}}
		if answer := server.do(t, want); answer.Status != rejectionStatus || answer.Body != rejectionBody {
			t.Errorf("a server with no store answered %s with %s", pattern, answer)
		}
	}
}

func TestAnEmptyStoreListsNoConnectionsAndNoNames(t *testing.T) {
	t.Parallel()
	tree := newConnectionsTree(t)
	list := tree.get(t, "/api/connections")
	if list.Status != http.StatusOK || strings.TrimSpace(list.Body) != `{"connections":[]}` {
		t.Fatalf("list %s", list)
	}
	names := tree.get(t, "/api/connections/names")
	if names.Status != http.StatusOK || strings.TrimSpace(names.Body) != `{"names":[]}` {
		t.Fatalf("names %s", names)
	}
}

func TestCreateUpdateAndDeleteAnswerSummariesWithoutCredentials(t *testing.T) {
	t.Parallel()
	tree := newConnectionsTree(t)
	created := summaryFrom(t, tree.send(t, http.MethodPost, connectionBody(t, saslFields(t, "eu"))))
	if !created.SaslPasswordSet || !created.TlsClientKeySet || created.Name != "eu" {
		t.Fatalf("created %+v", created)
	}
	names := tree.get(t, "/api/connections/names")
	if err := connectionsSchemas(t).names.Validate(decodeDocument(t, []byte(names.Body))); err != nil || !strings.Contains(names.Body, `"eu"`) {
		t.Fatalf("names %s %v", names, err)
	}
	if strings.Contains(names.Body, "broker") || strings.Contains(names.Body, "Set") {
		t.Fatalf("the name list carries more than names: %s", names.Body)
	}
	list := tree.get(t, "/api/connections")
	if err := connectionsSchemas(t).list.Validate(decodeDocument(t, []byte(list.Body))); err != nil {
		t.Fatalf("list %s %v", list, err)
	}
	// An edit that leaves the credentials out keeps them.
	edit := saslFields(t, "eu-west")
	delete(edit, "saslPassword")
	delete(edit, "tlsClientKey")
	edit["current"] = "eu"
	updated := summaryFrom(t, tree.send(t, http.MethodPut, connectionBody(t, edit)))
	if updated.Name != "eu-west" || !updated.SaslPasswordSet || !updated.TlsClientKeySet {
		t.Fatalf("updated %+v", updated)
	}
	// Removing the key needs its certificate gone too, and says so on it.
	edit["current"] = "eu-west"
	edit["remove"] = []string{"tlsClientKey"}
	failure := connectionsFailureOf(t, tree.send(t, http.MethodPut, connectionBody(t, edit)), ConnectionsErrorKindInvalid)
	if len(failure.Problems) != 1 || failure.Problems[0].Pointer != "/tlsClientKey" {
		t.Fatalf("problems %+v", failure.Problems)
	}
	delete(edit, "tlsClientCertificate")
	if summary := summaryFrom(t, tree.send(t, http.MethodPut, connectionBody(t, edit))); summary.TlsClientKeySet {
		t.Fatalf("the key was not removed: %+v", summary)
	}
	remaining := tree.send(t, http.MethodDelete, `{"name":"eu-west"}`)
	if remaining.Status != http.StatusOK || strings.TrimSpace(remaining.Body) != `{"connections":[]}` {
		t.Fatalf("delete %s", remaining)
	}
}

func TestEachConnectionRefusalHasItsKind(t *testing.T) {
	t.Parallel()
	tree := newConnectionsTree(t)
	summaryFrom(t, tree.send(t, http.MethodPost, connectionBody(t, saslFields(t, "Kafka"))))
	connectionsFailureOf(t, tree.send(t, http.MethodPost, connectionBody(t, saslFields(t, "kafka"))), ConnectionsErrorKindDuplicateName)
	connectionsFailureOf(t, tree.send(t, http.MethodDelete, `{"name":"missing"}`), ConnectionsErrorKindNotFound)
	missing := saslFields(t, "missing")
	missing["current"] = "missing"
	connectionsFailureOf(t, tree.send(t, http.MethodPut, connectionBody(t, missing)), ConnectionsErrorKindNotFound)
	// A refusal from the store names the field: an invalid PEM, a rule.
	bad := saslFields(t, "bad")
	bad["tlsClientKey"] = "-----BEGIN PRIVATE KEY-----\nQUOTEME\n-----END PRIVATE KEY-----\n"
	bad["securityProtocol"] = "SASL_PLAINTEXT"
	failure := connectionsFailureOf(t, tree.send(t, http.MethodPost, connectionBody(t, bad)), ConnectionsErrorKindInvalid)
	pointers := map[string]bool{}
	for _, problem := range failure.Problems {
		pointers[problem.Pointer] = true
		if strings.Contains(problem.Message, "QUOTEME") {
			t.Errorf("a problem quotes the key: %s", problem.Message)
		}
	}
	if !pointers["/tlsClientKey"] || !pointers["/tlsClientCertificate"] {
		t.Fatalf("problems %+v", failure.Problems)
	}
	// An empty password is refused, not read as remove.
	empty := saslFields(t, "Kafka")
	empty["current"] = "Kafka"
	empty["saslPassword"] = ""
	failure = connectionsFailureOf(t, tree.send(t, http.MethodPut, connectionBody(t, empty)), ConnectionsErrorKindInvalid)
	if len(failure.Problems) == 0 || failure.Problems[0].Pointer != "/saslPassword" {
		t.Fatalf("problems %+v", failure.Problems)
	}
}

func TestAConnectionBodyTheEndpointWillNotReasonAboutIsInvalid(t *testing.T) {
	t.Parallel()
	tree := newConnectionsTree(t)
	valid := connectionBody(t, map[string]any{"name": "a", "bootstrapServers": []string{"b:1"}, "securityProtocol": "PLAINTEXT"})
	cases := map[string]struct {
		method, body string
		contentTypes []string
	}{
		"not JSON":                       {http.MethodPost, "name=a", []string{jsonType}},
		"no Content-Type":                {http.MethodPost, valid, nil},
		"a text Content-Type":            {http.MethodPost, valid, []string{"text/plain"}},
		"an unknown field":               {http.MethodPost, strings.Replace(valid, "{", `{"host":"x",`, 1), []string{jsonType}},
		"a key in another case":          {http.MethodPost, strings.Replace(valid, `"name"`, `"Name"`, 1), []string{jsonType}},
		"a credential in another case":   {http.MethodPost, strings.Replace(valid, "{", `{"SaslPassword":"`+leakPassword+`",`, 1), []string{jsonType}},
		"a credential-shaped field":      {http.MethodPost, strings.Replace(valid, "{", `{"password":"`+leakPassword+`",`, 1), []string{jsonType}},
		"a key twice":                    {http.MethodPost, strings.Replace(valid, "{", `{"name":"b",`, 1), []string{jsonType}},
		"a credential twice":             {http.MethodPost, strings.Replace(valid, "{", `{"saslPassword":"x","saslPassword":"`+leakPassword+`",`, 1), []string{jsonType}},
		"a credential not a string":      {http.MethodPost, strings.Replace(valid, "{", `{"saslPassword":1,`, 1), []string{jsonType}},
		"an object inside":               {http.MethodPost, strings.Replace(valid, `["b:1"]`, `{"saslPassword":"`+leakPassword+`"}`, 1), []string{jsonType}},
		"an array inside an array":       {http.MethodPost, strings.Replace(valid, `["b:1"]`, `[["b:1"]]`, 1), []string{jsonType}},
		"two objects":                    {http.MethodPost, valid + valid, []string{jsonType}},
		"an array":                       {http.MethodPost, "[" + valid + "]", []string{jsonType}},
		"invalid UTF-8":                  {http.MethodPost, strings.Replace(valid, `"a"`, "\"a\xff\"", 1), []string{jsonType}},
		"an empty body":                  {http.MethodPost, "", []string{jsonType}},
		"an update field on a create":    {http.MethodPost, strings.Replace(valid, "{", `{"current":"a",`, 1), []string{jsonType}},
		"a credential on a delete":       {http.MethodDelete, `{"name":"a","saslPassword":"` + leakPassword + `"}`, []string{jsonType}},
		"over the cap":                   {http.MethodPost, strings.Replace(valid, `"b:1"`, `"`+strings.Repeat(leakPassword, maxConnectionRequestBytes/len(leakPassword)+1)+`"`, 1), []string{jsonType}},
		"over the cap in a credential":   {http.MethodPost, strings.Replace(valid, "{", `{"saslPassword":"`+strings.Repeat(leakPassword, maxConnectionRequestBytes/len(leakPassword)+1)+`",`, 1), []string{jsonType}},
		"an update with a key twice":     {http.MethodPut, `{"current":"a","current":"b","name":"a","bootstrapServers":["b:1"],"securityProtocol":"PLAINTEXT"}`, []string{jsonType}},
		"a delete with an unknown field": {http.MethodDelete, `{"name":"a","force":true}`, []string{jsonType}},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			want := tree.server.allowed("/api/connections")
			want.method = test.method
			want.body = test.body
			if test.contentTypes != nil {
				want.headers = http.Header{"Content-Type": test.contentTypes}
			}
			answer := tree.server.do(t, want)
			failure := connectionsFailureOf(t, answer, ConnectionsErrorKindInvalid)
			if len(failure.Problems) != 0 {
				t.Errorf("a malformed body answered problems %v", failure.Problems)
			}
			if strings.Contains(answer.Body, leakPassword) || len(answer.Body) > 1024 {
				t.Errorf("the body was echoed: %.200s", answer.Body)
			}
		})
	}
	if _, err := os.Stat(tree.storeFile); !os.IsNotExist(err) {
		t.Errorf("an invalid request wrote the store: %v", err)
	}
}

func TestDeletingOrRenamingAReferencedConnectionReportsItUntilConfirmed(t *testing.T) {
	t.Parallel()
	tree := newConnectionsTree(t)
	summaryFrom(t, tree.send(t, http.MethodPost, connectionBody(t, saslFields(t, "eu"))))
	pipelineFile := filepath.Join(tree.root, "orders", "pipeline.yaml")
	content := strings.Replace(minimalPipeline, "  topic: in\n", "  topic: in\n  connection: eu\n", 1)
	mustMkdir(t, filepath.Dir(pipelineFile))
	if err := os.WriteFile(pipelineFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	failure := connectionsFailureOf(t, tree.send(t, http.MethodDelete, `{"name":"eu"}`), ConnectionsErrorKindReferenced)
	if len(failure.References) != 1 || failure.References[0].Root != tree.root || failure.References[0].Directory == nil ||
		*failure.References[0].Directory != "orders" || failure.References[0].Block != ConnectionBlockSource || failure.References[0].PipelineID != "orders" {
		t.Fatalf("references %+v", failure.References)
	}
	rename := saslFields(t, "eu-renamed")
	rename["current"] = "eu"
	delete(rename, "saslPassword")
	delete(rename, "tlsClientKey")
	connectionsFailureOf(t, tree.send(t, http.MethodPut, connectionBody(t, rename)), ConnectionsErrorKindReferenced)
	rename["confirm"] = true
	if summary := summaryFrom(t, tree.send(t, http.MethodPut, connectionBody(t, rename))); summary.Name != "eu-renamed" || !summary.SaslPasswordSet {
		t.Fatalf("confirmed rename %+v", summary)
	}
	if after, _ := os.ReadFile(pipelineFile); string(after) != content {
		t.Fatalf("the pipeline was rewritten:\n%s", after)
	}
	// A broken pipeline is unchecked, which needs the same confirmation.
	mustMkdir(t, filepath.Join(tree.root, "broken"))
	if err := os.WriteFile(filepath.Join(tree.root, "broken", "pipeline.yaml"), []byte("id: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	failure = connectionsFailureOf(t, tree.send(t, http.MethodDelete, `{"name":"eu-renamed"}`), ConnectionsErrorKindReferenced)
	if len(failure.References) != 0 || len(failure.Unchecked) != 1 || failure.Unchecked[0].Reason != UncheckedReasonParse {
		t.Fatalf("report %+v", failure)
	}
	if answer := tree.send(t, http.MethodDelete, `{"name":"eu-renamed","confirm":true}`); answer.Status != http.StatusOK {
		t.Fatalf("confirmed delete %s", answer)
	}
}

// No answer carries text from an underlying error: the messages are the
// table's, and every kind has one.
func TestEveryConnectionsErrorTheEndpointsSendIsValidAndFixed(t *testing.T) {
	t.Parallel()
	schemas := connectionsSchemas(t)
	for kind, failure := range connectionsFailures {
		recorded := httptest.NewRecorder()
		writeConnectionsError(recorded, &connections.Error{Kind: connections.ErrorKind(kind),
			Problems: []connections.Problem{{Pointer: "/name", Message: "m"}},
			Report:   &connections.Report{References: []connections.Reference{}, Unchecked: []connections.Unchecked{{Location: catalog.Location{Root: "/r"}, Reason: "truncated"}}}})
		if recorded.Code != failure.status {
			t.Errorf("%s answered %d, want %d", kind, recorded.Code, failure.status)
		}
		if err := schemas.failure.Validate(decodeDocument(t, recorded.Body.Bytes())); err != nil {
			t.Errorf("the %s error is not valid: %v", kind, err)
		}
	}
	if len(connectionsFailures) != len(enumValues_ConnectionsErrorKind) {
		t.Errorf("there are %d error kinds with an answer, want the schema's %d", len(connectionsFailures), len(enumValues_ConnectionsErrorKind))
	}
	recorded := httptest.NewRecorder()
	writeConnectionsError(recorded, os.ErrPermission)
	if recorded.Code != http.StatusInternalServerError || strings.Contains(recorded.Body.String(), "permission") {
		t.Errorf("a foreign error answered %d %s", recorded.Code, recorded.Body.String())
	}
}

// A known password and private key go through create, update, list, every
// validation failure, every store failure and every refused request, and
// appear in no response body and no log line.
func TestNoCredentialAppearsInAnyResponseOrLogLine(t *testing.T) {
	t.Parallel()
	tree := newConnectionsTree(t)
	fields := saslFields(t, "leak")
	key := fields["tlsClientKey"].(string)
	keyBody := strings.Split(key, "\n")[1]
	var answers []response
	send := func(method string, body map[string]any) {
		answers = append(answers, tree.send(t, method, connectionBody(t, body)))
	}
	with := func(changes map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range fields {
			out[k] = v
		}
		for k, v := range changes {
			if v == nil {
				delete(out, k)
				continue
			}
			out[k] = v
		}
		return out
	}
	send(http.MethodPost, fields)
	send(http.MethodPost, fields)                                                       // duplicate
	send(http.MethodPost, with(map[string]any{"name": "p", "securityProtocol": "SSL"})) // rule
	send(http.MethodPost, with(map[string]any{"name": "q", "tlsClientKey": leakPassword}))
	send(http.MethodPost, with(map[string]any{"name": "r", "saslPassword": strings.Repeat(leakPassword, 60)}))
	send(http.MethodPost, with(map[string]any{"name": "s", "tlsClientCertificate": key}))
	send(http.MethodPost, with(map[string]any{"name": "bad name"}))
	send(http.MethodPut, with(map[string]any{"current": "leak"}))
	send(http.MethodPut, with(map[string]any{"current": "leak", "remove": []string{"saslPassword"}}))
	send(http.MethodPut, with(map[string]any{"current": "missing"}))
	send(http.MethodPut, with(map[string]any{"current": "leak", "securityProtocol": "PLAINTEXT"}))
	send(http.MethodDelete, map[string]any{"name": "missing"})
	answers = append(answers, tree.get(t, "/api/connections"), tree.get(t, "/api/connections/names"))
	raw := tree.server.allowed("/api/connections")
	raw.method = http.MethodPost
	raw.body = `{"saslPassword":"` + leakPassword + `","SaslPassword":"` + leakPassword + `"}`
	raw.headers = http.Header{"Content-Type": {jsonType}}
	answers = append(answers, tree.server.do(t, raw))
	// A store failure: a directory has taken the store file's place, so
	// every write fails.
	if err := os.Remove(tree.storeFile); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, tree.storeFile)
	send(http.MethodPost, with(map[string]any{"name": "io"}))
	send(http.MethodPut, with(map[string]any{"current": "leak"}))
	if last := answers[len(answers)-1]; last.Status != http.StatusInternalServerError {
		t.Fatalf("a failed write answered %s", last)
	}
	_ = os.Remove(tree.storeFile)
	send(http.MethodPut, with(map[string]any{"current": "leak"}))
	for index, answer := range answers {
		for _, secret := range []string{leakPassword, keyBody} {
			if strings.Contains(answer.Body, secret) || strings.Contains(answer.Headers, secret) {
				t.Errorf("answer %d carries a credential: %.300s", index, answer.Body)
			}
		}
	}
	logs := tree.server.logs.String() + tree.storeLogs.String()
	if logs == "" {
		t.Fatal("nothing was logged, so this test sees nothing")
	}
	for _, secret := range []string{leakPassword, keyBody} {
		if strings.Contains(logs, secret) {
			t.Error("a log line holds a credential")
		}
	}
	if stored, _ := os.ReadFile(tree.storeFile); !strings.Contains(string(stored), leakPassword) {
		t.Error("the store file does not hold the password, so the test did not store one")
	}
}
