// The pipeline endpoints (#48), against a real server, a real catalog and a
// real tree in a scratch home.

package httpapi

import (
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

// pipelinesTree is a scratch home with one authorized root, code/, served by
// a server whose catalog lists, opens and creates through it.
type pipelinesTree struct {
	home, root string
	roots      *projectfs.Roots
	server     *testServer
}

const minimalPipeline = "id: orders\nname: Orders\nlanguage: python\nsource:\n  id: in\n  type: kafkaSource\n  topic: in\n" +
	"destination:\n  id: out\n  type: kafkaDestination\n  topic: out\nruntimeConfiguration:\n  networkMode: docker\n"

func newPipelinesTree(t *testing.T) *pipelinesTree {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tree := &pipelinesTree{home: filepath.Join(base, "home")}
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
	roots, err := projectfs.OpenRoots(browser, filepath.Join(base, "config", "kpv", "roots.json"), discardLogger())
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
	tree.roots = roots
	tree.server = startServer(t, Options{Browser: browser, Roots: roots, Pipelines: pipelines})
	return tree
}

func (tree *pipelinesTree) write(t *testing.T, directory, content string) {
	t.Helper()
	mustMkdir(t, filepath.Join(tree.root, directory))
	if err := os.WriteFile(filepath.Join(tree.root, directory, "pipeline.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openPath(query url.Values) string {
	return "/api/pipelines/open?" + query.Encode()
}

func (tree *pipelinesTree) open(t *testing.T, directory string) response {
	t.Helper()
	return tree.server.do(t, tree.server.allowed(openPath(url.Values{"root": {tree.root}, "directory": {directory}})))
}

func (tree *pipelinesTree) create(t *testing.T, body string) response {
	t.Helper()
	want := tree.server.allowed("/api/pipelines")
	want.method = http.MethodPost
	want.body = body
	want.headers = http.Header{"Content-Type": {"application/json"}}
	return tree.server.do(t, want)
}

func (tree *pipelinesTree) list(t *testing.T) PipelineList {
	t.Helper()
	answer := tree.server.do(t, tree.server.allowed("/api/pipelines"))
	if answer.Status != http.StatusOK {
		t.Fatalf("want a 200 list, got %s", answer)
	}
	if err := pipelinesSchemas(t).list.Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("the list is not valid against the schema: %v\n%s", err, answer.Body)
	}
	var list PipelineList
	if err := json.Unmarshal([]byte(answer.Body), &list); err != nil {
		t.Fatal(err)
	}
	assertPipelinesPairings(t, &list)
	return list
}

func createBody(root, name string) string {
	body, _ := json.Marshal(map[string]string{
		"root": root, "name": name, "language": "go", "sourceTopic": "orders.v1",
		"destinationTopic": "billing.v1", "networkMode": "docker",
	})
	return string(body)
}

func documentFrom(t *testing.T, answer response) PipelineDocument {
	t.Helper()
	if answer.Status != http.StatusOK {
		t.Fatalf("want a 200 document, got %s", answer)
	}
	if err := pipelinesSchemas(t).document.Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("the document is not valid against the schema: %v\n%s", err, answer.Body)
	}
	var document PipelineDocument
	if err := json.Unmarshal([]byte(answer.Body), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

// pipelinesFailureOf checks an answer is the schema's error with the status,
// kind and fixed sentence wanted.
func pipelinesFailureOf(t *testing.T, answer response, kind PipelinesErrorKind) PipelinesError {
	t.Helper()
	want := pipelinesFailures[kind]
	if answer.Status != want.status {
		t.Fatalf("want %d, got %s", want.status, answer)
	}
	if err := pipelinesSchemas(t).failure.Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("the error is not valid against the schema: %v\n%s", err, answer.Body)
	}
	var failure PipelinesError
	if err := json.Unmarshal([]byte(answer.Body), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error != kind || failure.Message != want.message {
		t.Fatalf("want a %s error with its fixed message, got %+v", kind, failure)
	}
	assertPipelinesPairings(t, &failure)
	return failure
}

func TestThePipelineEndpointsAreBehindTheChecks(t *testing.T) {
	t.Parallel()
	tree := newPipelinesTree(t)
	tree.write(t, "orders", minimalPipeline)
	server := tree.server
	jsonType := http.Header{"Content-Type": {"application/json"}}
	body := createBody(tree.root, "Made by an attacker")
	for _, pattern := range []string{pipelinesListPattern, pipelinesOpenPattern, pipelinesCreatePattern} {
		method, path := methodAndPathOf(t, pattern)
		if pattern == pipelinesOpenPattern {
			path = openPath(url.Values{"root": {tree.root}, "directory": {"orders"}})
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
	// A create with no Origin is refused even when fetch metadata vouches.
	want := server.pageFetch("/api/pipelines")
	want.method = http.MethodPost
	want.body = body
	want.headers.Set("Content-Type", "application/json")
	if answer := server.do(t, want); answer.Status != rejectionStatus {
		t.Errorf("an origin-less create was answered %s", answer)
	}
	if entries, _ := os.ReadDir(tree.root); len(entries) != 1 {
		t.Errorf("a rejected request created something: %v", entries)
	}
	// The page's own GETs are served, as #58 has it.
	if answer := server.do(t, server.pageFetch("/api/pipelines")); answer.Status != http.StatusOK {
		t.Errorf("the page's own list was answered %s", answer)
	}
}

func TestAServerWithNoCatalogRejectsTheEndpointsUniformly(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	for _, pattern := range []string{pipelinesListPattern, pipelinesOpenPattern, pipelinesCreatePattern} {
		method, path := methodAndPathOf(t, pattern)
		want := server.allowed(path + "?root=/x")
		want.method = method
		want.body = createBody("/x", "x")
		want.headers = http.Header{"Content-Type": {"application/json"}}
		if answer := server.do(t, want); answer.Status != rejectionStatus || answer.Body != rejectionBody {
			t.Errorf("a server with no catalog answered %s with %s", pattern, answer)
		}
	}
}

func TestTheListAnswersEveryRootAndEntry(t *testing.T) {
	t.Parallel()
	tree := newPipelinesTree(t)
	tree.write(t, "orders", minimalPipeline)
	tree.write(t, "copy", strings.Replace(minimalPipeline, "id: orders", "id: ORDERS", 1))
	tree.write(t, "broken", "id: [")
	list := tree.list(t)
	if len(list.Roots) != 1 || list.Roots[0].Path != tree.root || !list.Roots[0].Available || len(list.Roots[0].Pipelines) != 3 {
		t.Fatalf("the list is %+v", list)
	}
	byDirectory := map[string]PipelineEntry{}
	for _, entry := range list.Roots[0].Pipelines {
		byDirectory[*entry.Directory] = entry
	}
	orders := byDirectory["orders"]
	if !orders.Readable || *orders.ID != "orders" || *orders.Name != "Orders" || *orders.Language != "python" || orders.Modified == nil {
		t.Errorf("a readable entry is %+v", orders)
	}
	if len(orders.DuplicateID) != 1 || *orders.DuplicateID[0].Directory != "copy" {
		t.Errorf("a duplicate id is %+v", orders.DuplicateID)
	}
	broken := byDirectory["broken"]
	if broken.Readable || broken.Unreadable == nil || broken.Unreadable.Kind != UnreadableKindParse || broken.ID != nil {
		t.Errorf("an unreadable entry is %+v", broken)
	}
	// No path but the root and directory: the real pipeline directory the
	// catalog carries in-process is not sent.
	answer := tree.server.do(t, tree.server.allowed("/api/pipelines"))
	if strings.Contains(answer.Body, filepath.Join(tree.root, "orders")) {
		t.Errorf("the answer carries a joined path: %s", answer.Body)
	}
}

func TestAnUnavailableRootIsListedWithItsReason(t *testing.T) {
	t.Parallel()
	tree := newPipelinesTree(t)
	tree.write(t, "orders", minimalPipeline)
	if err := os.RemoveAll(tree.root); err != nil {
		t.Fatal(err)
	}
	list := tree.list(t)
	if len(list.Roots) != 1 || list.Roots[0].Available || *list.Roots[0].Reason != PipelineRootReasonNotFound || len(list.Roots[0].Pipelines) != 0 {
		t.Errorf("a removed root is %+v", list.Roots)
	}
	failure := pipelinesFailureOf(t, tree.open(t, "orders"), PipelinesErrorKindRootUnavailable)
	if *failure.Reason != PipelineRootReasonNotFound {
		t.Errorf("open names %s", *failure.Reason)
	}
	pipelinesFailureOf(t, tree.create(t, createBody(tree.root, "x")), PipelinesErrorKindRootUnavailable)
}

func TestOpenAnswersTheDocumentAndItsHash(t *testing.T) {
	t.Parallel()
	tree := newPipelinesTree(t)
	tree.write(t, "orders", minimalPipeline)
	document := documentFrom(t, tree.open(t, "orders"))
	if document.Root != tree.root || *document.Directory != "orders" || document.Pipeline["id"] != "orders" || len(document.Sha256) != 64 {
		t.Errorf("the document is %+v", document)
	}
	// A root that is itself the pipeline is opened with no directory.
	single := filepath.Join(tree.home, "single")
	mustMkdir(t, single)
	if err := os.WriteFile(filepath.Join(single, "pipeline.yaml"), []byte(minimalPipeline), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tree.roots.Authorize(single); err != nil {
		t.Fatal(err)
	}
	document = documentFrom(t, tree.server.do(t, tree.server.allowed(openPath(url.Values{"root": {single}}))))
	if document.Directory != nil || document.Root != single {
		t.Errorf("the root pipeline is %+v", document)
	}
}

func TestOpenRefusesWhatItWillNotReasonAbout(t *testing.T) {
	t.Parallel()
	tree := newPipelinesTree(t)
	tree.write(t, "orders", minimalPipeline)
	for name, query := range map[string]url.Values{
		"no root":            {"directory": {"orders"}},
		"an empty root":      {"root": {""}, "directory": {"orders"}},
		"two roots":          {"root": {tree.root, tree.root}, "directory": {"orders"}},
		"two directories":    {"root": {tree.root}, "directory": {"orders", "orders"}},
		"an empty directory": {"root": {tree.root}, "directory": {""}},
		"a dot":              {"root": {tree.root}, "directory": {"."}},
		"a dot dot":          {"root": {tree.root}, "directory": {".."}},
		"a slash":            {"root": {tree.root}, "directory": {"orders/x"}},
		"a backslash":        {"root": {tree.root}, "directory": {`orders\x`}},
		"a NUL":              {"root": {tree.root}, "directory": {"orders\x00"}},
		"a climb":            {"root": {tree.root}, "directory": {"../code/orders"}},
		"a path parameter":   {"root": {tree.root}, "directory": {"orders"}, "path": {"/etc"}},
	} {
		t.Run(name, func(t *testing.T) {
			pipelinesFailureOf(t, tree.server.do(t, tree.server.allowed(openPath(query))), PipelinesErrorKindInvalid)
		})
	}
}

func TestOpenAnswersWhatAnEntryIsNow(t *testing.T) {
	t.Parallel()
	tree := newPipelinesTree(t)
	tree.write(t, "hand-edited", strings.Replace(minimalPipeline, "  networkMode: docker\n", "  networkMode: bridge\n", 1))
	failure := pipelinesFailureOf(t, tree.open(t, "hand-edited"), PipelinesErrorKindUnreadable)
	if failure.Unreadable.Kind != UnreadableKindSchema || len(failure.Unreadable.Problems) == 0 ||
		failure.Unreadable.Problems[0].Pointer != "/runtimeConfiguration/networkMode" || failure.Unreadable.Problems[0].Line == nil {
		t.Errorf("a schema failure is %+v", failure.Unreadable)
	}
	pipelinesFailureOf(t, tree.open(t, "gone"), PipelinesErrorKindNotFound)
	// Readable but no longer searchable: unreadable, as the list shows it,
	// not notFound.
	if os.Geteuid() != 0 {
		tree.write(t, "unsearchable", minimalPipeline)
		if err := os.Chmod(filepath.Join(tree.root, "unsearchable"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(tree.root, "unsearchable"), 0o755) })
		failure := pipelinesFailureOf(t, tree.open(t, "unsearchable"), PipelinesErrorKindUnreadable)
		if failure.Unreadable.Kind != UnreadableKindUnreadable {
			t.Errorf("an unsearchable directory opens as %+v", failure.Unreadable)
		}
	}
	outside := filepath.Join(tree.home, "outside")
	pipelinesFailureOf(t, tree.server.do(t, tree.server.allowed(openPath(url.Values{"root": {outside}}))), PipelinesErrorKindNotAuthorized)
	if _, err := tree.roots.Revoke(tree.root); err != nil {
		t.Fatal(err)
	}
	pipelinesFailureOf(t, tree.open(t, "hand-edited"), PipelinesErrorKindNotAuthorized)
}

func TestCreateMakesAPipelineThatIsListedAndOpened(t *testing.T) {
	t.Parallel()
	tree := newPipelinesTree(t)
	document := documentFrom(t, tree.create(t, createBody(tree.root, "Orders to billing")))
	if *document.Directory != "orders-to-billing" || document.Pipeline["name"] != "Orders to billing" {
		t.Fatalf("created %+v", document)
	}
	second := documentFrom(t, tree.create(t, createBody(tree.root, "Orders to billing")))
	if *second.Directory != "orders-to-billing-2" {
		t.Errorf("the second create is %s", *second.Directory)
	}
	list := tree.list(t)
	if len(list.Roots[0].Pipelines) != 2 {
		t.Errorf("listed %+v", list.Roots[0].Pipelines)
	}
	if again := documentFrom(t, tree.open(t, "orders-to-billing")); again.Sha256 != document.Sha256 {
		t.Errorf("the opened hash %s is not the created one %s", again.Sha256, document.Sha256)
	}
}

func TestCreateAnswersEachRefusal(t *testing.T) {
	t.Parallel()
	tree := newPipelinesTree(t)
	failure := pipelinesFailureOf(t, tree.create(t, strings.Replace(createBody(tree.root, "x"), "orders.v1", "orders v1", 1)), PipelinesErrorKindInvalid)
	if len(failure.Problems) == 0 || failure.Problems[0].Pointer != "/source/topic" {
		t.Errorf("an invalid topic answered %+v", failure.Problems)
	}
	if err := os.WriteFile(filepath.Join(tree.root, "pipeline.yaml"), []byte(minimalPipeline), 0o644); err != nil {
		t.Fatal(err)
	}
	pipelinesFailureOf(t, tree.create(t, createBody(tree.root, "x")), PipelinesErrorKindRootIsPipeline)
	pipelinesFailureOf(t, tree.create(t, createBody(filepath.Join(tree.home, "elsewhere"), "x")), PipelinesErrorKindNotAuthorized)
}

func TestACreateBodyTheEndpointWillNotReasonAboutIsInvalid(t *testing.T) {
	t.Parallel()
	tree := newPipelinesTree(t)
	valid := createBody(tree.root, "Orders")
	cases := map[string]struct {
		body         string
		contentTypes []string
	}{
		"not JSON":              {"root=" + tree.root, []string{"application/json"}},
		"no Content-Type":       {valid, nil},
		"a text Content-Type":   {valid, []string{"text/plain"}},
		"an unknown field":      {strings.Replace(valid, "{", `{"path":"/x",`, 1), []string{"application/json"}},
		"a key in another case": {strings.Replace(valid, `"name"`, `"Name"`, 1), []string{"application/json"}},
		"a key twice":           {strings.Replace(valid, "{", `{"name":"Other",`, 1), []string{"application/json"}},
		"a field missing":       {strings.Replace(valid, `,"networkMode":"docker"`, "", 1), []string{"application/json"}},
		"a field not a string":  {strings.Replace(valid, `"networkMode":"docker"`, `"networkMode":1`, 1), []string{"application/json"}},
		"a null field":          {strings.Replace(valid, `"networkMode":"docker"`, `"networkMode":null`, 1), []string{"application/json"}},
		"an empty root":         {createBody("", "Orders"), []string{"application/json"}},
		"two objects":           {valid + valid, []string{"application/json"}},
		"an array":              {"[" + valid + "]", []string{"application/json"}},
		"over the cap":          {strings.Replace(valid, `"Orders"`, `"`+strings.Repeat("a", maxCreateRequestBytes)+`"`, 1), []string{"application/json"}},
		"invalid UTF-8":         {strings.Replace(valid, "Orders", "Orders\xff", 1), []string{"application/json"}},
		"an empty body":         {"", []string{"application/json"}},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			want := tree.server.allowed("/api/pipelines")
			want.method = http.MethodPost
			want.body = test.body
			if test.contentTypes != nil {
				want.headers = http.Header{"Content-Type": test.contentTypes}
			}
			failure := pipelinesFailureOf(t, tree.server.do(t, want), PipelinesErrorKindInvalid)
			if failure.Problems != nil {
				t.Errorf("a malformed body answered problems %v", failure.Problems)
			}
		})
	}
	if entries, _ := os.ReadDir(tree.root); len(entries) != 0 {
		t.Errorf("an invalid request created %v", entries)
	}
}

// No answer carries text from an underlying error, and none carries a path
// but the root and directory the request named: the messages are the table's.
func TestEveryPipelinesErrorTheEndpointsSendIsValidAndFixed(t *testing.T) {
	t.Parallel()
	schemas := pipelinesSchemas(t)
	left := true
	for kind, failure := range pipelinesFailures {
		recorded := httptest.NewRecorder()
		fault := &catalog.Error{Kind: catalog.ErrorKind(kind), Reason: projectfs.KindChanged, DirectoryLeftBehind: &left,
			Unreadable: &catalog.Unreadable{Kind: catalog.UnreadableLink, Problems: []catalog.Problem{}}}
		writePipelinesError(recorded, fault)
		if recorded.Code != failure.status {
			t.Errorf("%s answered %d, want %d", kind, recorded.Code, failure.status)
		}
		if err := schemas.failure.Validate(decodeDocument(t, recorded.Body.Bytes())); err != nil {
			t.Errorf("the %s error is not valid: %v", kind, err)
		}
		var answer PipelinesError
		if err := json.Unmarshal(recorded.Body.Bytes(), &answer); err != nil {
			t.Fatal(err)
		}
		assertPipelinesPairings(t, &answer)
		if answer.Message != failure.message {
			t.Errorf("%s answered %q", kind, answer.Message)
		}
	}
	if len(pipelinesFailures) != len(enumValues_PipelinesErrorKind) {
		t.Errorf("there are %d error kinds with an answer, want the schema's %d", len(pipelinesFailures), len(enumValues_PipelinesErrorKind))
	}
	// Something that is not a catalog error is io, and says nothing of itself.
	recorded := httptest.NewRecorder()
	writePipelinesError(recorded, os.ErrPermission)
	if recorded.Code != http.StatusInternalServerError || strings.Contains(recorded.Body.String(), "permission") {
		t.Errorf("a foreign error answered %d %s", recorded.Code, recorded.Body.String())
	}
}

func TestPipelineRequestsLogKindsAndNoPath(t *testing.T) {
	t.Parallel()
	tree := newPipelinesTree(t)
	tree.write(t, "secret-name", "id: [")
	tree.list(t)
	tree.open(t, "secret-name")
	tree.create(t, createBody(tree.root, "Another secret"))
	logs := tree.server.logs.String()
	if !strings.Contains(logs, "kind=unreadable") {
		t.Errorf("the refused open was not logged by kind: %s", logs)
	}
	if strings.Contains(logs, tree.home) || strings.Contains(logs, "secret") {
		t.Errorf("a log line carries a path or a name: %s", logs)
	}
}
