// The source file endpoints (#87), against a real server, a real catalog and
// a real tree in a scratch home.

package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

const sourceText = "def run(message):\r\n    return message\r\n"

func sha256Of(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// newSourceTree is a pipelines tree with the pipeline orders, holding
// src/main.py.
func newSourceTree(t *testing.T) *pipelinesTree {
	t.Helper()
	tree := newPipelinesTree(t)
	tree.write(t, "orders", minimalPipeline)
	mustMkdir(t, filepath.Join(tree.root, "orders", "src"))
	if err := os.WriteFile(filepath.Join(tree.root, "orders", "src", "main.py"), []byte(sourceText), 0o644); err != nil {
		t.Fatal(err)
	}
	return tree
}

func sourcePath(query url.Values) string {
	return "/api/pipelines/source?" + query.Encode()
}

func (tree *pipelinesTree) readSource(t *testing.T, path string) response {
	t.Helper()
	return tree.server.do(t, tree.server.allowed(sourcePath(url.Values{"root": {tree.root}, "directory": {"orders"}, "path": {path}})))
}

func (tree *pipelinesTree) writeSource(t *testing.T, body string) response {
	t.Helper()
	want := tree.server.allowed("/api/pipelines/source")
	want.method = http.MethodPut
	want.body = body
	want.headers = http.Header{"Content-Type": {"application/json"}}
	return tree.server.do(t, want)
}

func (tree *pipelinesTree) writeBody(path, text string, fields map[string]any) string {
	body := map[string]any{"root": tree.root, "directory": "orders", "path": path, "text": text}
	for key, value := range fields {
		body[key] = value
	}
	encoded, _ := json.Marshal(body)
	return string(encoded)
}

func sourceFileFrom(t *testing.T, answer response) SourceFile {
	t.Helper()
	if answer.Status != http.StatusOK {
		t.Fatalf("want a 200 source file, got %s", answer)
	}
	if err := sourceSchemas(t).file.Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("the file is not valid against the schema: %v\n%s", err, answer.Body)
	}
	var file SourceFile
	if err := json.Unmarshal([]byte(answer.Body), &file); err != nil {
		t.Fatal(err)
	}
	return file
}

func sourceWrittenFrom(t *testing.T, answer response) SourceWritten {
	t.Helper()
	if answer.Status != http.StatusOK {
		t.Fatalf("want a 200 write, got %s", answer)
	}
	if err := sourceSchemas(t).written.Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("the answer is not valid against the schema: %v\n%s", err, answer.Body)
	}
	var written SourceWritten
	if err := json.Unmarshal([]byte(answer.Body), &written); err != nil {
		t.Fatal(err)
	}
	return written
}

// sourceFailureOf checks an answer is the schema's error with the status,
// kind and fixed sentence wanted, keeping the schema's pairings.
func sourceFailureOf(t *testing.T, answer response, kind SourceErrorKind) SourceError {
	t.Helper()
	want := sourceFailures[kind]
	if answer.Status != want.status {
		t.Fatalf("want %d for %s, got %s", want.status, kind, answer)
	}
	if err := sourceSchemas(t).failure.Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("the error is not valid against the schema: %v\n%s", err, answer.Body)
	}
	var failure SourceError
	if err := json.Unmarshal([]byte(answer.Body), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error != kind || failure.Message != want.message || !sourcePairingsHold(&failure) {
		t.Fatalf("want a %s error with its fixed message, got %+v", kind, failure)
	}
	return failure
}

func TestTheSourceEndpointsAreBehindTheChecks(t *testing.T) {
	t.Parallel()
	tree := newSourceTree(t)
	server := tree.server
	jsonType := http.Header{"Content-Type": {"application/json"}}
	body := tree.writeBody("src/main.py", "attacker\n", map[string]any{"expectedSha256": sha256Of(sourceText)})
	for _, pattern := range []string{sourceReadPattern, sourceWritePattern} {
		method, path := methodAndPathOf(t, pattern)
		if pattern == sourceReadPattern {
			path = sourcePath(url.Values{"root": {tree.root}, "directory": {"orders"}, "path": {"src/main.py"}})
		}
		for name, want := range map[string]request{
			"no token":       {method: method, path: path, origins: []string{server.URL()}, body: body, headers: jsonType},
			"a wrong token":  {method: method, path: path, token: "wrong", origins: []string{server.URL()}, body: body, headers: jsonType},
			"a bad origin":   {method: method, path: path, token: server.Token().Secret(), origins: []string{"https://evil.example"}, body: body, headers: jsonType},
			"no origin":      {method: method, path: path, token: server.Token().Secret(), body: body, headers: jsonType},
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
	// A write with no Origin is refused even when fetch metadata vouches.
	want := server.pageFetch("/api/pipelines/source")
	want.method = http.MethodPut
	want.body = body
	want.headers.Set("Content-Type", "application/json")
	if answer := server.do(t, want); answer.Status != rejectionStatus {
		t.Errorf("an origin-less write was answered %s", answer)
	}
	if got, _ := os.ReadFile(filepath.Join(tree.root, "orders", "src", "main.py")); string(got) != sourceText {
		t.Errorf("a rejected write changed the file: %q", got)
	}
	// The page's own GET is served, as #58 has it.
	if answer := server.do(t, server.pageFetch(sourcePath(url.Values{"root": {tree.root}, "directory": {"orders"}, "path": {"src/main.py"}}))); answer.Status != http.StatusOK {
		t.Errorf("the page's own read was answered %s", answer)
	}
}

func TestAServerWithNoCatalogRejectsTheSourceEndpointsUniformly(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	for _, pattern := range []string{sourceReadPattern, sourceWritePattern} {
		method, path := methodAndPathOf(t, pattern)
		want := server.allowed(path + "?root=/x&path=a")
		want.method = method
		want.body = `{"root":"/x","path":"a","text":"","expectedAbsent":true}`
		want.headers = http.Header{"Content-Type": {"application/json"}}
		if answer := server.do(t, want); answer.Status != rejectionStatus || answer.Body != rejectionBody {
			t.Errorf("a server with no catalog answered %s with %s", pattern, answer)
		}
	}
}

func TestASourceFileIsReadAndWrittenBackExactly(t *testing.T) {
	t.Parallel()
	tree := newSourceTree(t)
	file := sourceFileFrom(t, tree.readSource(t, "src/main.py"))
	if file.Text != sourceText || string(file.Sha256) != sha256Of(sourceText) || file.Generated ||
		file.Root != tree.root || *file.Directory != "orders" || file.Path != "src/main.py" {
		t.Fatalf("the read answered %+v", file)
	}
	next := "def run(message):\n    return None"
	written := sourceWrittenFrom(t, tree.writeSource(t, tree.writeBody("src/main.py", next, map[string]any{"expectedSha256": string(file.Sha256)})))
	if string(written.Sha256) != sha256Of(next) || written.Path != "src/main.py" || *written.Directory != "orders" {
		t.Errorf("the write answered %+v", written)
	}
	if got, _ := os.ReadFile(filepath.Join(tree.root, "orders", "src", "main.py")); string(got) != next {
		t.Errorf("the file holds %q", got)
	}
	// The same hash again is stale now, and is answered with the current one.
	failure := sourceFailureOf(t, tree.writeSource(t, tree.writeBody("src/main.py", "again", map[string]any{"expectedSha256": string(file.Sha256)})), SourceErrorKindChanged)
	if failure.Sha256 == nil || string(*failure.Sha256) != sha256Of(next) {
		t.Errorf("a stale write answered %+v", failure)
	}
	// A new file in a new directory, then a refusal to create it twice.
	created := sourceWrittenFrom(t, tree.writeSource(t, tree.writeBody("lib/new.py", "", map[string]any{"expectedAbsent": true})))
	if string(created.Sha256) != sha256Of("") {
		t.Errorf("the create answered %+v", created)
	}
	failure = sourceFailureOf(t, tree.writeSource(t, tree.writeBody("lib/new.py", "x", map[string]any{"expectedAbsent": true})), SourceErrorKindExists)
	if failure.Sha256 != nil {
		t.Errorf("exists answered %+v", failure)
	}
	// generated/ is read and says so, and is not written.
	mustMkdir(t, filepath.Join(tree.root, "orders", "generated"))
	if err := os.WriteFile(filepath.Join(tree.root, "orders", "generated", "main.py"), []byte("# generated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if file := sourceFileFrom(t, tree.readSource(t, "generated/main.py")); !file.Generated {
		t.Errorf("a generated file reads as %+v", file)
	}
	sourceFailureOf(t, tree.writeSource(t, tree.writeBody("generated/main.py", "x", map[string]any{"expectedSha256": sha256Of("# generated\n")})), SourceErrorKindGenerated)
	// A root that is itself the pipeline is addressed with no directory.
	single := filepath.Join(tree.home, "single")
	mustMkdir(t, filepath.Join(single, "src"))
	if err := os.WriteFile(filepath.Join(single, "pipeline.yaml"), []byte(minimalPipeline), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(single, "src", "a.py"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tree.roots.Authorize(single); err != nil {
		t.Fatal(err)
	}
	if file := sourceFileFrom(t, tree.server.do(t, tree.server.allowed(sourcePath(url.Values{"root": {single}, "path": {"src/a.py"}})))); file.Directory != nil || file.Text != "a\n" {
		t.Errorf("a root pipeline's file reads as %+v", file)
	}
}

func TestEachSourceRefusalIsItsKind(t *testing.T) {
	t.Parallel()
	tree := newSourceTree(t)
	dir := filepath.Join(tree.root, "orders")
	if err := os.WriteFile(filepath.Join(dir, "big.py"), []byte(strings.Repeat("#", catalog.MaxFileSize+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "latin1.py"), []byte("caf\xe9"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "src", "main.py"), filepath.Join(dir, "link.py")); err != nil {
		t.Fatal(err)
	}
	for path, kind := range map[string]SourceErrorKind{
		"src/gone.py":   SourceErrorKindNotFound,
		"pipeline.yaml": SourceErrorKindReserved,
		"Pipeline.YAML": SourceErrorKindReserved,
		"link.py":       SourceErrorKindLink,
		"src":           SourceErrorKindNotRegularFile,
		"big.py":        SourceErrorKindTooLarge,
		"latin1.py":     SourceErrorKindNotText,
		"src/../x":      SourceErrorKindInvalid,
		"src/.env":      SourceErrorKindInvalid,
		"src/":          SourceErrorKindInvalid,
	} {
		failure := sourceFailureOf(t, tree.readSource(t, path), kind)
		if failure.Sha256 != nil || failure.Reason != nil {
			t.Errorf("%s answered %+v", path, failure)
		}
	}
	for name, test := range map[string]struct {
		body string
		want SourceErrorKind
	}{
		"text over 1 MiB": {tree.writeBody("src/main.py", strings.Repeat("x", catalog.MaxFileSize+1), map[string]any{"expectedSha256": sha256Of(sourceText)}), SourceErrorKindTooLarge},
		"a NUL":           {tree.writeBody("src/main.py", "a\x00", map[string]any{"expectedSha256": sha256Of(sourceText)}), SourceErrorKindNotText},
		"reserved":        {tree.writeBody("pipeline.yaml", "x", map[string]any{"expectedAbsent": true}), SourceErrorKindReserved},
		"a link":          {tree.writeBody("link.py", "x", map[string]any{"expectedSha256": sha256Of(sourceText)}), SourceErrorKindLink},
		"a missing file":  {tree.writeBody("src/gone.py", "x", map[string]any{"expectedSha256": sha256Of(sourceText)}), SourceErrorKindNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			sourceFailureOf(t, tree.writeSource(t, test.body), test.want)
		})
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "src", "main.py")); string(got) != sourceText {
		t.Errorf("a refused write changed the file: %q", got)
	}
	// Not a listed pipeline, and roots the open route refuses, answer as it
	// does.
	query := func(root, directory string) string {
		return sourcePath(url.Values{"root": {root}, "directory": {directory}, "path": {"src/main.py"}})
	}
	sourceFailureOf(t, tree.server.do(t, tree.server.allowed(query(tree.root, "gone"))), SourceErrorKindNotFound)
	sourceFailureOf(t, tree.server.do(t, tree.server.allowed(query(filepath.Join(tree.home, "outside"), "orders"))), SourceErrorKindNotAuthorized)
	if err := os.Rename(tree.root, tree.root+"-moved"); err != nil {
		t.Fatal(err)
	}
	failure := sourceFailureOf(t, tree.readSource(t, "src/main.py"), SourceErrorKindRootUnavailable)
	opened := pipelinesFailureOf(t, tree.open(t, "orders"), PipelinesErrorKindRootUnavailable)
	if string(*failure.Reason) != string(*opened.Reason) || failure.Message != opened.Message {
		t.Errorf("an unavailable root reads as %+v and opens as %+v", failure, opened)
	}
}

func TestTheSourceReadQueryTheEndpointWillNotReasonAboutIsInvalid(t *testing.T) {
	t.Parallel()
	tree := newSourceTree(t)
	for name, query := range map[string]url.Values{
		"no root":            {"directory": {"orders"}, "path": {"src/main.py"}},
		"an empty root":      {"root": {""}, "directory": {"orders"}, "path": {"src/main.py"}},
		"two roots":          {"root": {tree.root, tree.root}, "directory": {"orders"}, "path": {"src/main.py"}},
		"two directories":    {"root": {tree.root}, "directory": {"orders", "orders"}, "path": {"src/main.py"}},
		"an empty directory": {"root": {tree.root}, "directory": {""}, "path": {"src/main.py"}},
		"a climb":            {"root": {tree.root}, "directory": {".."}, "path": {"code/orders/src/main.py"}},
		"no path":            {"root": {tree.root}, "directory": {"orders"}},
		"an empty path":      {"root": {tree.root}, "directory": {"orders"}, "path": {""}},
		"two paths":          {"root": {tree.root}, "directory": {"orders"}, "path": {"src/main.py", "src/main.py"}},
		"an absolute path":   {"root": {tree.root}, "directory": {"orders"}, "path": {filepath.Join(tree.root, "orders", "src", "main.py")}},
		"a backslash":        {"root": {tree.root}, "directory": {"orders"}, "path": {`src\main.py`}},
		"a NUL":              {"root": {tree.root}, "directory": {"orders"}, "path": {"src/main.py\x00"}},
		"another parameter":  {"root": {tree.root}, "directory": {"orders"}, "path": {"src/main.py"}, "follow": {"1"}},
	} {
		t.Run(name, func(t *testing.T) {
			sourceFailureOf(t, tree.server.do(t, tree.server.allowed(sourcePath(query))), SourceErrorKindInvalid)
		})
	}
}

func TestASourceWriteBodyTheEndpointWillNotReasonAboutIsInvalid(t *testing.T) {
	t.Parallel()
	tree := newSourceTree(t)
	hash := sha256Of(sourceText)
	valid := tree.writeBody("src/main.py", "x", map[string]any{"expectedSha256": hash})
	jsonType := []string{"application/json"}
	cases := map[string]struct {
		body         string
		contentTypes []string
	}{
		"not JSON":                  {"root=" + tree.root, jsonType},
		"no Content-Type":           {valid, nil},
		"a text Content-Type":       {valid, []string{"text/plain"}},
		"an unknown field":          {strings.Replace(valid, "{", `{"force":true,`, 1), jsonType},
		"a key in another case":     {strings.Replace(valid, `"text"`, `"Text"`, 1), jsonType},
		"a key twice":               {strings.Replace(valid, "{", `{"text":"other",`, 1), jsonType},
		"the hash twice":            {strings.Replace(valid, "{", `{"expectedSha256":"`+hash+`",`, 1), jsonType},
		"the root missing":          {strings.Replace(valid, `"root":`, `"rooted":`, 1), jsonType},
		"the path missing":          {strings.Replace(tree.writeBody("x", "x", map[string]any{"expectedSha256": hash}), `"path":"x",`, "", 1), jsonType},
		"the text missing":          {strings.Replace(valid, `,"text":"x"`, "", 1), jsonType},
		"neither expectation":       {tree.writeBody("src/main.py", "x", nil), jsonType},
		"both expectations":         {tree.writeBody("src/main.py", "x", map[string]any{"expectedSha256": hash, "expectedAbsent": true}), jsonType},
		"expectedAbsent false":      {tree.writeBody("src/new.py", "x", map[string]any{"expectedAbsent": false}), jsonType},
		"expectedAbsent twice":      {strings.Replace(tree.writeBody("src/new.py", "x", map[string]any{"expectedAbsent": true}), "{", `{"expectedAbsent":true,`, 1), jsonType},
		"expectedAbsent a string":   {tree.writeBody("src/new.py", "x", map[string]any{"expectedAbsent": "true"}), jsonType},
		"an upper-case hash":        {tree.writeBody("src/main.py", "x", map[string]any{"expectedSha256": strings.ToUpper(hash)}), jsonType},
		"a short hash":              {tree.writeBody("src/main.py", "x", map[string]any{"expectedSha256": hash[:10]}), jsonType},
		"the text not a string":     {strings.Replace(valid, `"text":"x"`, `"text":1`, 1), jsonType},
		"a null text":               {strings.Replace(valid, `"text":"x"`, `"text":null`, 1), jsonType},
		"an object inside":          {strings.Replace(valid, `"text":"x"`, `"text":{"a":"b"}`, 1), jsonType},
		"an empty root":             {strings.Replace(valid, `"root":"`+tree.root+`"`, `"root":""`, 1), jsonType},
		"an empty directory":        {strings.Replace(valid, `"directory":"orders"`, `"directory":""`, 1), jsonType},
		"a climbing path":           {tree.writeBody("src/../../escape.py", "x", map[string]any{"expectedAbsent": true}), jsonType},
		"two objects":               {valid + valid, jsonType},
		"an array":                  {"[" + valid + "]", jsonType},
		"over the cap":              {tree.writeBody("src/main.py", strings.Repeat("\x01", maxSourceRequestBytes/6+1), map[string]any{"expectedSha256": hash}), jsonType},
		"invalid UTF-8":             {strings.Replace(valid, `"text":"x"`, "\"text\":\"\xff\"", 1), jsonType},
		"an empty body":             {"", jsonType},
		"a field name with a space": {strings.Replace(valid, `"path"`, `"path "`, 1), jsonType},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			want := tree.server.allowed("/api/pipelines/source")
			want.method = http.MethodPut
			want.body = test.body
			if test.contentTypes != nil {
				want.headers = http.Header{"Content-Type": test.contentTypes}
			}
			answer := tree.server.do(t, want)
			sourceFailureOf(t, answer, SourceErrorKindInvalid)
			if strings.Contains(answer.Body, "\\u0001") || strings.Contains(answer.Body, tree.root) {
				t.Errorf("the refusal echoes the request: %s", answer.Body)
			}
		})
	}
	if got, _ := os.ReadFile(filepath.Join(tree.root, "orders", "src", "main.py")); string(got) != sourceText {
		t.Errorf("an invalid request changed the file: %q", got)
	}
	if _, err := os.Stat(filepath.Join(tree.home, "escape.py")); err == nil {
		t.Error("a climbing path wrote outside the pipeline")
	}
	// A text of exactly 1 MiB, all escaped, fits the body and is written.
	full := strings.Repeat("\x01", catalog.MaxFileSize)
	sourceWrittenFrom(t, tree.writeSource(t, tree.writeBody("src/full.txt", full, map[string]any{"expectedAbsent": true})))
}

// No answer carries text from an underlying error, nor a path, nor the file's
// text: the messages are the table's, and every kind the schema names has one.
func TestEverySourceErrorTheEndpointsSendIsValidAndFixed(t *testing.T) {
	t.Parallel()
	schemas := sourceSchemas(t)
	for kind, failure := range sourceFailures {
		recorded := httptest.NewRecorder()
		writeSourceError(recorded, &catalog.Error{Kind: catalog.ErrorKind(kind), Reason: projectfs.KindChanged, SHA256: sha256Of("x"),
			Unreadable: &catalog.Unreadable{Kind: catalog.UnreadableUnreadable, Problems: []catalog.Problem{}}})
		if recorded.Code != failure.status {
			t.Errorf("%s answered %d, want %d", kind, recorded.Code, failure.status)
		}
		if err := schemas.failure.Validate(decodeDocument(t, recorded.Body.Bytes())); err != nil {
			t.Errorf("the %s error is not valid: %v", kind, err)
		}
		var answer SourceError
		if err := json.Unmarshal(recorded.Body.Bytes(), &answer); err != nil {
			t.Fatal(err)
		}
		if answer.Message != failure.message || !sourcePairingsHold(&answer) {
			t.Errorf("%s answered %+v", kind, answer)
		}
	}
	if len(sourceFailures) != len(enumValues_SourceErrorKind) {
		t.Errorf("there are %d error kinds with an answer, want the schema's %d", len(sourceFailures), len(enumValues_SourceErrorKind))
	}
	for _, kind := range enumValues_SourceErrorKind {
		if _, ok := sourceFailures[SourceErrorKind(kind.(string))]; !ok {
			t.Errorf("the schema's %s has no answer", kind)
		}
	}
	recorded := httptest.NewRecorder()
	writeSourceError(recorded, os.ErrPermission)
	if recorded.Code != http.StatusInternalServerError || strings.Contains(recorded.Body.String(), "permission") {
		t.Errorf("a foreign error answered %d %s", recorded.Code, recorded.Body.String())
	}
}

func TestSourceRequestsLogKindsAndNoPathOrText(t *testing.T) {
	t.Parallel()
	tree := newSourceTree(t)
	secret := "secret-content-" + strings.Repeat("z", 8)
	if err := os.WriteFile(filepath.Join(tree.root, "orders", "src", "secret-name.py"), []byte(secret), 0o644); err != nil {
		t.Fatal(err)
	}
	tree.readSource(t, "src/secret-name.py")
	tree.readSource(t, "src/secret-missing.py")
	answer := tree.writeSource(t, tree.writeBody("src/secret-name.py", secret+"2", map[string]any{"expectedSha256": sha256Of("stale")}))
	if strings.Contains(answer.Body, "secret") {
		t.Errorf("a changed answer carries the text: %s", answer.Body)
	}
	tree.writeSource(t, tree.writeBody("src/secret-name.py", secret+"3", map[string]any{"expectedAbsent": true}))
	logs := tree.server.logs.String()
	if !strings.Contains(logs, "kind=notFound") || !strings.Contains(logs, "kind=changed") {
		t.Errorf("the refusals were not logged by kind: %s", logs)
	}
	if strings.Contains(logs, tree.home) || strings.Contains(logs, "secret") {
		t.Errorf("a log line carries a path, a name or the text: %s", logs)
	}
}
