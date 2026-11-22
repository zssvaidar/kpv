//go:build unix

// TestSecurityBoundary runs every corpus of the security boundary suite
// (#46) against one run of the built binary, in order, then checks what has
// to hold after all of them: no canary and no token anywhere they may not
// be, before and after a restart. Run it alone with
//
//	cd agent && go test ./cmd/agent -run '^TestSecurity' -v

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"go/ast"
	"go/parser"
	"go/token"
	"math/big"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fixture is the state every corpus attacks: two authorized roots, a
// pipeline with a source file in the first, a stored connection holding
// canary credentials, and the uniform rejection to compare against.
type fixture struct {
	s *scratch
	a *agent

	root, other string // the two authorized roots, alpha and beta
	notARoot    string // a directory in home that is not a root
	elsewhere   string // a directory in home outside every root
	directory   string // the pipeline's directory name in root
	pipelineDir string
	sourcePath  string // a source file in the pipeline, relative to it
	sourceText  string

	connection, spare string // the canary connection and one to update
	password          string // the canary saslPassword
	key               string // the canary tlsClientKey, a real PEM key
	certificate       string

	envCanary     string // the value in the pipeline's .env
	outsideCanary string // the bytes of every file planted outside the roots

	routes []attackRoute

	// unauthorized is the answer an unauthorized GET to a path no route
	// registers gets: the uniform rejection everything else is compared to.
	unauthorized, unauthorizedHead answer

	// frames is every frame the channel delivered to the suite's own
	// subscribed client.
	framesMu sync.Mutex
	frames   [][]byte
}

func TestSecurityBoundary(t *testing.T) {
	f := &fixture{s: newScratch(t)}
	f.routes = attackRoutes(t)
	if t.Failed() {
		t.FailNow()
	}
	f.envCanary = randomText(t, "ENVCANARY")
	f.outsideCanary = randomText(t, "OUTSIDECANARY")
	f.password = randomText(t, "PASSWORDCANARY")
	f.key, f.certificate = generateKey(t)
	writeStandIns(t, f.s)
	f.layOut(t)

	f.a = startAgent(t, f.s, nil)
	f.baseline(t)
	f.populate(t)
	f.subscribe(t)
	f.plant(t)

	t.Run("routes not registered", f.unregisteredPaths)
	t.Run("who may call", f.whoMayCall)
	t.Run("paths and links", f.pathsAndLinks)
	t.Run("env and gitignore", f.envAndGitignore)
	t.Run("bodies and headers", f.bodiesAndHeaders)
	t.Run("no request reaches a process", f.noProcess)
	t.Run("credentials on error paths", f.credentialErrorPaths)
	t.Run("rejections say nothing", f.rejectionsSayNothing)
	t.Run("no canary or token leaves the agent", f.nothingLeaks)
}

// layOut makes the directories before the agent starts.
func (f *fixture) layOut(t *testing.T) {
	s := f.s
	f.root = filepath.Join(s.home, "projects", "alpha")
	f.other = filepath.Join(s.home, "projects", "beta")
	f.notARoot = filepath.Join(s.home, "projects", "not-a-root")
	f.elsewhere = filepath.Join(s.home, "elsewhere")
	for _, dir := range []string{f.root, f.other, f.notARoot, f.elsewhere} {
		mustMkdir(t, dir)
	}
	mustWrite(t, filepath.Join(s.outside, "secret.txt"), f.outsideCanary, 0o644)
	mustWrite(t, filepath.Join(s.outside, "dir", "a.go"), f.outsideCanary, 0o644)
	mustWrite(t, filepath.Join(s.outside, "pipe", "pipeline.yaml"), f.outsideCanary, 0o644)
	mustWrite(t, filepath.Join(f.other, "beta.txt"), f.outsideCanary, 0o644)
	mustWrite(t, filepath.Join(f.elsewhere, "secret.txt"), f.outsideCanary, 0o644)
	// A pipeline in the second root that names the canary connection, so a
	// delete of it answers referenced.
	mustWrite(t, filepath.Join(f.other, "uses-canary", "pipeline.yaml"), "id: usescanary\nname: Uses canary\nlanguage: python\n"+
		"source:\n  id: in\n  type: kafkaSource\n  topic: in\n  connection: canary\n"+
		"destination:\n  id: out\n  type: kafkaDestination\n  topic: out\nruntimeConfiguration:\n  networkMode: docker\n", 0o644)
}

// baseline records the uniform rejection.
func (f *fixture) baseline(t *testing.T) {
	f.unauthorized = f.a.send(request{method: "GET", target: "/kpv-security-no-such-path"})
	f.unauthorizedHead = f.a.send(request{method: "HEAD", target: "/kpv-security-no-such-path"})
	if f.unauthorized.status != http.StatusNotFound || len(f.unauthorized.body) == 0 {
		t.Fatalf("the uniform rejection is %d %q, not a 404 with a body", f.unauthorized.status, f.unauthorized.body)
	}
}

// call sends an authorized JSON request and returns the answer.
func (f *fixture) call(t *testing.T, method, target string, body any) answer {
	t.Helper()
	header := f.a.authorized()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
		header.Set("Content-Type", "application/json")
	}
	return f.a.send(request{method: method, target: target, body: raw, header: header})
}

func (f *fixture) mustCall(t *testing.T, method, target string, body any) answer {
	t.Helper()
	got := f.call(t, method, target, body)
	if got.status != http.StatusOK {
		t.Fatalf("%s %s answered %d %s", method, target, got.status, got.body)
	}
	return got
}

// populate authorizes the roots, creates the pipeline and its source file,
// and stores the canary connection and a spare.
func (f *fixture) populate(t *testing.T) {
	f.mustCall(t, "POST", "/api/roots", map[string]any{"path": f.root})
	f.mustCall(t, "POST", "/api/roots", map[string]any{"path": f.other})
	created := f.mustCall(t, "POST", "/api/pipelines", map[string]any{"root": f.root, "name": "Orders", "language": "go",
		"sourceTopic": "in.v1", "destinationTopic": "out.v1", "networkMode": "docker"})
	var pipeline struct {
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal(created.body, &pipeline); err != nil || pipeline.Directory == "" {
		t.Fatalf("the create answer %s names no directory", created.body)
	}
	f.directory = pipeline.Directory
	f.pipelineDir = filepath.Join(f.root, f.directory)
	f.sourcePath = "src/main.go"
	f.sourceText = "package main\n\nfunc main() {}\n"
	f.mustCall(t, "PUT", "/api/pipelines/source", map[string]any{"root": f.root, "directory": f.directory,
		"path": f.sourcePath, "text": f.sourceText, "expectedAbsent": true})

	f.connection, f.spare = "canary", "spare"
	f.mustCall(t, "POST", "/api/connections", map[string]any{"name": f.connection, "bootstrapServers": []string{"broker.example:9093"},
		"securityProtocol": "SASL_SSL", "saslMechanism": "SCRAM-SHA-512", "saslUsername": "canary-user", "saslPassword": f.password,
		"tlsClientCertificate": f.certificate, "tlsClientKey": f.key})
	f.mustCall(t, "POST", "/api/connections", map[string]any{"name": f.spare, "bootstrapServers": []string{"localhost:9092"},
		"securityProtocol": "PLAINTEXT"})
}

// subscribe opens the suite's own channel client, subscribed to the
// pipeline, and keeps every frame it is delivered until the test ends.
func (f *fixture) subscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	header := http.Header{"Origin": {f.a.origin}}
	connection, response, err := websocket.Dial(ctx, "ws://"+f.a.addr+"/api/channel?token="+f.a.token, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		cancel()
		t.Fatalf("open the channel: %v", err)
	}
	if response.Body != nil {
		_ = response.Body.Close()
	}
	connection.SetReadLimit(1 << 20)
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"command":"subscribe","pipelineId":"`+f.directory+`"}`)); err != nil {
		cancel()
		t.Fatalf("subscribe: %v", err)
	}
	done, first := make(chan struct{}), make(chan struct{})
	already := len(f.channelFrames())
	go func() {
		defer close(done)
		for {
			_, frame, err := connection.Read(ctx)
			if err != nil {
				return
			}
			f.framesMu.Lock()
			f.frames = append(f.frames, frame)
			if len(f.frames) == already+1 {
				close(first)
			}
			f.framesMu.Unlock()
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = connection.CloseNow()
		<-done
	})
	// The subscription is acknowledged, so the client is known to be in force
	// before any corpus runs.
	select {
	case <-first:
	case <-time.After(answerTimeout):
		t.Fatal("the channel never acknowledged the subscription")
	}
	if frames := f.channelFrames(); !strings.Contains(string(frames[already]), "subscribed") {
		t.Fatalf("the channel's first frame is %s, not the acknowledgement", frames[already])
	}
}

func (f *fixture) channelFrames() [][]byte {
	f.framesMu.Lock()
	defer f.framesMu.Unlock()
	return append([][]byte(nil), f.frames...)
}

// generateKey makes a real PEM private key for the run and a certificate
// for it, since a client key needs its certificate.
func generateKey(t *testing.T) (key, certificate string) {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "kpv-security-suite"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &private.PublicKey, private)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
}

// skipForIssue skips a case that fails against the shipped controls. Such a
// case is not fixed here: it is filed as its own issue, and the skip names
// it. TestSecuritySkipsEachNameAnIssue holds that every skip in the suite
// goes through here with a positive issue number written as a literal.
func skipForIssue(t *testing.T, issue int, why string) {
	t.Helper()
	if issue <= 0 {
		t.Fatalf("a skip with no issue number: %s", why)
	}
	t.Skipf("fails against the shipped controls, filed as #%d: %s", issue, why)
}

// Every skip in the suite goes through skipForIssue, which fails a skip
// without a positive issue number, so a skip cannot quietly stand in for a
// case nobody filed. It reads the suite's own files.
func TestSecuritySkipsEachNameAnIssue(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("security_*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("find the suite's files: %v", err)
	}
	set := token.NewFileSet()
	for _, name := range files {
		file, err := parser.ParseFile(set, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok || function.Name.Name == "skipForIssue" {
				continue
			}
			ast.Inspect(function, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
					switch selector.Sel.Name {
					case "Skip", "Skipf", "SkipNow":
						t.Errorf("%s: %s calls %s; a skip goes through skipForIssue, naming the issue it was filed as", set.Position(call.Pos()), function.Name.Name, selector.Sel.Name)
					}
				}
				if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "skipForIssue" {
					if literal, ok := call.Args[1].(*ast.BasicLit); ok && literal.Kind == token.INT && (literal.Value == "0" || strings.HasPrefix(literal.Value, "-")) {
						t.Errorf("%s: a skip with issue %s", set.Position(call.Pos()), literal.Value)
					}
				}
				return true
			})
		}
	}
	for what, issues := range map[string]map[string]int{"bodyIssues": bodyIssues, "neverEndingBodyIssues": neverEndingBodyIssues} {
		for name, issue := range issues {
			if issue <= 0 {
				t.Errorf("%s holds %q with no issue number", what, name)
			}
		}
	}
}
