package connections

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
	"github.com/zssvaidar/kpv/agent/internal/secrets"
)

// The known credentials the leak tests look for.
const (
	knownPassword = "leak-canary-password-5f2c9a"
	knownUsername = "svc-orders"
)

type lockedBuffer struct {
	mutex sync.Mutex
	buf   bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buf.String()
}

// world is a scratch home with a roots store, a catalog over those roots,
// and a connection store beside the roots store. Nothing here is the real
// home or ~/.config/kpv.
type world struct {
	t        *testing.T
	home     string
	file     string
	roots    *projectfs.Roots
	private  *projectfs.PrivateFile
	catalog  *catalog.Catalog
	store    *Store
	logs     *lockedBuffer
	logger   *slog.Logger
	guard    *projectfs.Guard
	browser  *projectfs.Browser
	rootsLoc string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, home: home, logs: &lockedBuffer{}}
	w.logger = slog.New(slog.NewTextHandler(w.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	w.rootsLoc = filepath.Join(home, ".config", "kpv", "roots.json")
	w.file = filepath.Join(home, ".config", "kpv", "connections.json")
	w.open()
	return w
}

// open starts everything again over the same disk, as an agent restart does.
func (w *world) open() {
	w.t.Helper()
	guard, err := projectfs.New()
	if err != nil {
		w.t.Fatal(err)
	}
	browser, err := projectfs.NewBrowser(guard, w.home)
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { _ = browser.Close() })
	roots, err := projectfs.OpenRoots(browser, w.rootsLoc, w.logger)
	if err != nil {
		w.t.Fatal(err)
	}
	cat, err := catalog.New(roots, guard, w.logger)
	if err != nil {
		w.t.Fatal(err)
	}
	private, err := projectfs.OpenPrivateFile(roots, "connections.json")
	if err != nil {
		w.t.Fatal(err)
	}
	w.guard, w.browser, w.roots, w.catalog, w.private = guard, browser, roots, cat, private
	w.store = Open(private, cat, w.logger)
}

// root makes a directory under home and authorizes it.
func (w *world) root(name string) string {
	w.t.Helper()
	path := filepath.Join(w.home, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.roots.Authorize(path); err != nil {
		w.t.Fatalf("authorize %s: %v", name, err)
	}
	return path
}

// pipelineYAML is a valid pipeline whose blocks name source and destination
// ("" leaves the key out).
func pipelineYAML(id, source, destination string) string {
	text := "id: " + id + "\nlanguage: python\nsource:\n  id: in\n  type: kafkaSource\n  topic: in.v1\n"
	if source != "" {
		text += "  connection: " + source + "\n"
	}
	text += "destination:\n  id: out\n  type: kafkaDestination\n  topic: out.v1\n"
	if destination != "" {
		text += "  connection: " + destination + "\n"
	}
	return text + "runtimeConfiguration:\n  networkMode: docker\n"
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pems are a real certificate and its private key, made for each test run.
type pems struct {
	certificate, key, rsaKey, otherCertificate string
}

var (
	pemOnce sync.Once
	pemSet  pems
)

func testPEMs(t *testing.T) pems {
	t.Helper()
	pemOnce.Do(func() {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			panic(err)
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "kpv test"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			panic(err)
		}
		pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			panic(err)
		}
		rsaKey, err := rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			panic(err)
		}
		pemSet = pems{
			certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
			key:         string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})),
			rsaKey:      string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)})),
		}
		pemSet.otherCertificate = pemSet.certificate
	})
	return pemSet
}

// draft builds a request draft the way httpapi does: every non-credential
// field as decoded JSON, every credential through Credential.
func draft(fields map[string]any) Draft {
	d := Draft{Document: map[string]any{}, Credentials: map[string]secrets.Secret{}}
	for key, value := range fields {
		if IsCredential(key) {
			secret, problems := Credential(key, value.(string))
			d.Credentials[key] = secret
			d.Problems = append(d.Problems, problems...)
			continue
		}
		if list, ok := value.([]string); ok {
			items := make([]any, 0, len(list))
			for _, item := range list {
				items = append(items, item)
			}
			value = items
		}
		d.Document[key] = value
	}
	return d
}

// saslRecord is a SASL_SSL connection with a password and a client key.
func saslRecord(t *testing.T, name string) map[string]any {
	p := testPEMs(t)
	return map[string]any{
		"name": name, "bootstrapServers": []string{"broker-1:9093", "[::1]:9094"},
		"securityProtocol": "SASL_SSL", "saslMechanism": "SCRAM-SHA-512",
		"saslUsername": knownUsername, "saslPassword": knownPassword,
		"tlsCaCertificate": p.certificate, "tlsClientCertificate": p.certificate, "tlsClientKey": p.key,
	}
}

// update is an update request for current, built from a record.
func update(current string, record map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{"current": current}
	for key, value := range record {
		out[key] = value
	}
	for key, value := range extra {
		if value == nil {
			delete(out, key)
			continue
		}
		out[key] = value
	}
	return out
}

func kindOf(t *testing.T, err error) ErrorKind {
	t.Helper()
	if err == nil {
		return ""
	}
	return KindOf(err)
}

// fakeLister answers a fixed pipeline list.
type fakeLister struct{ list catalog.List }

func (f fakeLister) List() catalog.List { return f.list }
