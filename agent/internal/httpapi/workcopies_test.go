// The working-copy endpoints (#43), against a real server, a real catalog, a
// real store in a scratch config directory and a real tree in a scratch home.
// Staging is done here through workcopy.Store.Stage in process, as #77 and
// #97 will, because no route stages.

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
	"github.com/zssvaidar/kpv/agent/internal/workcopy"
)

type workcopyTree struct {
	home, root string
	roots      *projectfs.Roots
	store      *workcopy.Store
	server     *testServer
	location   catalog.Location
}

// newWorkcopyTree is an authorized root with the pipeline orders, holding
// src/main.py, and a working-copy store beside the roots store; withStore
// false is an agent with no config directory.
func newWorkcopyTree(t *testing.T, withStore bool) *workcopyTree {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tree := &workcopyTree{home: filepath.Join(base, "home")}
	tree.root = filepath.Join(tree.home, "code")
	mustMkdir(t, filepath.Join(tree.root, "orders", "src"))
	if err := os.WriteFile(filepath.Join(tree.root, "orders", "pipeline.yaml"), []byte(minimalPipeline), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree.root, "orders", "src", "main.py"), []byte(sourceText), 0o644); err != nil {
		t.Fatal(err)
	}
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
	storeFile := ""
	if withStore {
		storeFile = filepath.Join(base, "config", "kpv", "roots.json")
	}
	roots, err := projectfs.OpenRoots(browser, storeFile, discardLogger())
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
	privateTree, err := projectfs.OpenPrivateTree(roots, workcopy.TreeName)
	if err != nil {
		t.Fatal(err)
	}
	store, err := workcopy.New(pipelines, privateTree, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	tree.roots, tree.store = roots, store
	tree.location = catalog.Location{Root: tree.root, Directory: "orders"}
	tree.server = startServer(t, Options{Browser: browser, Roots: roots, Pipelines: pipelines, Workcopies: store})
	return tree
}

func (tree *workcopyTree) stage(t *testing.T, changes ...workcopy.Change) string {
	t.Helper()
	revision, err := tree.store.Stage(tree.location, changes...)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func reviewPath(query url.Values) string { return "/api/workcopies/review?" + query.Encode() }

func (tree *workcopyTree) review(t *testing.T) response {
	t.Helper()
	return tree.server.do(t, tree.server.allowed(reviewPath(url.Values{"root": {tree.root}, "directory": {"orders"}})))
}

func (tree *workcopyTree) send(t *testing.T, pattern, body string) response {
	t.Helper()
	method, path := methodAndPathOf(t, pattern)
	want := tree.server.allowed(path)
	want.method = method
	want.body = body
	want.headers = http.Header{"Content-Type": {"application/json"}}
	return tree.server.do(t, want)
}

func (tree *workcopyTree) body(fields map[string]any) string {
	body := map[string]any{"root": tree.root, "directory": "orders"}
	for key, value := range fields {
		body[key] = value
	}
	encoded, _ := json.Marshal(body)
	return string(encoded)
}

// workcopyAnswer checks a 200 answer against a definition and decodes it.
func workcopyAnswer[T any](t *testing.T, answer response, definition string) T {
	t.Helper()
	var into T
	if answer.Status != http.StatusOK {
		t.Fatalf("want a 200 %s, got %s", definition, answer)
	}
	if err := workcopySchema(t, definition).Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("not valid against %s: %v\n%s", definition, err, answer.Body)
	}
	if err := json.Unmarshal([]byte(answer.Body), &into); err != nil {
		t.Fatal(err)
	}
	if !workcopyPairingsHold(&into) {
		t.Fatalf("breaks a pairing: %s", answer.Body)
	}
	return into
}

// workcopyFailureOf checks an answer is the schema's error with the status,
// kind and fixed sentence wanted.
func workcopyFailureOf(t *testing.T, answer response, kind WorkcopyErrorKind) WorkcopyError {
	t.Helper()
	want := workcopyFailures[kind]
	if answer.Status != want.status {
		t.Fatalf("want %d for %s, got %s", want.status, kind, answer)
	}
	if err := workcopySchema(t, "workcopyError").Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("the error is not valid against the schema: %v\n%s", err, answer.Body)
	}
	var failure WorkcopyError
	if err := json.Unmarshal([]byte(answer.Body), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error != kind || failure.Message != want.message || !workcopyPairingsHold(&failure) {
		t.Fatalf("want a %s error with its fixed message, got %+v", kind, failure)
	}
	return failure
}

var workcopyPatterns = []string{
	workcopiesListPattern, workcopyReviewPattern, workcopyApplyPattern,
	workcopyRestorePattern, workcopyDiscardPattern, workcopySnapshotPattern,
}

func TestTheWorkcopyEndpointsAreBehindTheChecks(t *testing.T) {
	t.Parallel()
	tree := newWorkcopyTree(t, true)
	revision := tree.stage(t, workcopy.Change{Path: "src/main.py", Text: []byte("staged\n")})
	server := tree.server
	jsonType := http.Header{"Content-Type": {"application/json"}}
	body := tree.body(map[string]any{"revision": revision})
	for _, pattern := range workcopyPatterns {
		method, path := methodAndPathOf(t, pattern)
		if pattern == workcopyReviewPattern {
			path = reviewPath(url.Values{"root": {tree.root}, "directory": {"orders"}})
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
	// A state change with no Origin is refused even when fetch metadata
	// vouches; the page's own GETs are served, as #58 has it.
	for _, pattern := range []string{workcopyApplyPattern, workcopyRestorePattern, workcopyDiscardPattern, workcopySnapshotPattern} {
		method, path := methodAndPathOf(t, pattern)
		want := server.pageFetch(path)
		want.method = method
		want.body = body
		want.headers.Set("Content-Type", "application/json")
		if answer := server.do(t, want); answer.Status != rejectionStatus {
			t.Errorf("an origin-less %s was answered %s", pattern, answer)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(tree.root, "orders", "src", "main.py")); string(got) != sourceText {
		t.Errorf("a rejected request changed the file: %q", got)
	}
	if answer := server.do(t, server.pageFetch(reviewPath(url.Values{"root": {tree.root}, "directory": {"orders"}}))); answer.Status != http.StatusOK {
		t.Errorf("the page's own review was answered %s", answer)
	}
}

func TestAServerWithNoWorkcopiesRejectsTheEndpointsUniformly(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	for _, pattern := range workcopyPatterns {
		method, path := methodAndPathOf(t, pattern)
		want := server.allowed(path + "?root=/x")
		want.method = method
		want.body = `{"root":"/x","revision":"` + strings.Repeat("a", 32) + `"}`
		want.headers = http.Header{"Content-Type": {"application/json"}}
		if answer := server.do(t, want); answer.Status != rejectionStatus || answer.Body != rejectionBody {
			t.Errorf("a server with no store answered %s with %s", pattern, answer)
		}
	}
}

func TestAMalformedWorkcopyRequestIsInvalidAndChangesNothing(t *testing.T) {
	t.Parallel()
	tree := newWorkcopyTree(t, true)
	revision := tree.stage(t, workcopy.Change{Path: "src/main.py", Text: []byte("staged\n")})
	good := `"root":"` + tree.root + `","directory":"orders"`
	bodies := map[string]string{
		"not an object":       `[]`,
		"no revision":         `{` + good + `}`,
		"a short revision":    `{` + good + `,"revision":"abc"}`,
		"an upper revision":   `{` + good + `,"revision":"` + strings.ToUpper(revision) + `"}`,
		"a revision twice":    `{` + good + `,"revision":"` + revision + `","revision":"` + revision + `"}`,
		"an unknown key":      `{` + good + `,"revision":"` + revision + `","force":"yes"}`,
		"a key in other case": `{` + good + `,"Revision":"` + revision + `"}`,
		"a number":            `{` + good + `,"revision":1}`,
		"an empty root":       `{"root":"","revision":"` + revision + `"}`,
		"an empty directory":  `{"root":"` + tree.root + `","directory":"","revision":"` + revision + `"}`,
		"trailing bytes":      `{` + good + `,"revision":"` + revision + `"} {}`,
		"too long":            `{` + good + `,"revision":"` + revision + `","x":"` + strings.Repeat("a", maxWorkcopyRequestBytes) + `"}`,
		"a snapshot path":     `{` + good + `,"snapshot":"../../x"}`,
	}
	for _, pattern := range []string{workcopyApplyPattern, workcopyDiscardPattern, workcopyRestorePattern, workcopySnapshotPattern} {
		for name, body := range bodies {
			workcopyFailureOf(t, tree.send(t, pattern, body), WorkcopyErrorKindInvalid)
			_ = name
		}
		method, path := methodAndPathOf(t, pattern)
		want := tree.server.allowed(path)
		want.method = method
		want.body = tree.body(map[string]any{"revision": revision})
		want.headers = http.Header{"Content-Type": {"text/plain"}}
		workcopyFailureOf(t, tree.server.do(t, want), WorkcopyErrorKindInvalid)
	}
	for name, query := range map[string]url.Values{
		"no root":         {"directory": {"orders"}},
		"an empty dir":    {"root": {tree.root}, "directory": {""}},
		"an unknown name": {"root": {tree.root}, "directory": {"orders"}, "path": {"x"}},
		"two roots":       {"root": {tree.root, tree.root}},
	} {
		workcopyFailureOf(t, tree.server.do(t, tree.server.allowed(reviewPath(query))), WorkcopyErrorKindInvalid)
		_ = name
	}
	if got, _ := os.ReadFile(filepath.Join(tree.root, "orders", "src", "main.py")); string(got) != sourceText {
		t.Errorf("an invalid request changed the file: %q", got)
	}
	review := workcopyAnswer[WorkcopyReview](t, tree.review(t), "workcopyReview")
	if review.Revision == nil || string(*review.Revision) != revision {
		t.Fatalf("an invalid request changed the working copy: %+v", review)
	}
}

func TestTheWorkcopyEndpointsReviewApplyRestoreAndDiscard(t *testing.T) {
	t.Parallel()
	tree := newWorkcopyTree(t, true)
	revision := tree.stage(t,
		workcopy.Change{Path: "src/main.py", Text: []byte("def run(message):\n    return None")},
		workcopy.Change{Path: "src/new.py", Text: []byte{}},
	)
	list := workcopyAnswer[WorkcopyList](t, tree.server.do(t, tree.server.allowed("/api/workcopies")), "")
	if len(list.Copies) != 1 || list.Copies[0].Files != 2 || !list.Copies[0].Available || string(list.Copies[0].Revision) != revision {
		t.Fatalf("list %+v", list)
	}
	review := workcopyAnswer[WorkcopyReview](t, tree.review(t), "workcopyReview")
	if len(review.Files) != 2 || *review.Files[0].BaseText != sourceText || *review.Files[1].StagedText != "" || review.Files[1].BaseText != nil {
		t.Fatalf("review %+v", review)
	}
	// The read route still answers the project file.
	if file := sourceFileFrom(t, tree.server.do(t, tree.server.allowed(sourcePath(url.Values{"root": {tree.root}, "directory": {"orders"}, "path": {"src/main.py"}})))); file.Text != sourceText {
		t.Fatalf("the source read answered the staged text: %q", file.Text)
	}
	// A stale revision is changed.
	workcopyFailureOf(t, tree.send(t, workcopyApplyPattern, tree.body(map[string]any{"revision": strings.Repeat("0", 32)})), WorkcopyErrorKindChanged)
	report := workcopyAnswer[WorkcopyReport](t, tree.send(t, workcopyApplyPattern, tree.body(map[string]any{"revision": revision})), "workcopyReport")
	if !report.Complete || len(report.Files) != 2 || report.Files[0].Result != WorkcopyResultWritten {
		t.Fatalf("report %+v", report)
	}
	if got, _ := os.ReadFile(filepath.Join(tree.root, "orders", "src", "main.py")); string(got) != "def run(message):\n    return None" {
		t.Fatalf("applied %q", got)
	}
	review = workcopyAnswer[WorkcopyReview](t, tree.review(t), "workcopyReview")
	if len(review.Files) != 0 || len(review.Snapshots) != 1 || review.Snapshots[0].ID != report.Snapshot {
		t.Fatalf("after apply %+v", review)
	}
	restored := workcopyAnswer[WorkcopyReport](t, tree.send(t, workcopyRestorePattern, tree.body(map[string]any{"snapshot": string(report.Snapshot)})), "workcopyReport")
	if !restored.Complete {
		t.Fatalf("restore %+v", restored)
	}
	if got, _ := os.ReadFile(filepath.Join(tree.root, "orders", "src", "main.py")); string(got) != sourceText {
		t.Fatalf("restored %q", got)
	}
	if _, err := os.Lstat(filepath.Join(tree.root, "orders", "src", "new.py")); !os.IsNotExist(err) {
		t.Fatalf("the added file is still there: %v", err)
	}
	removed := workcopyAnswer[WorkcopySnapshotRemoved](t, tree.send(t, workcopySnapshotPattern, tree.body(map[string]any{"snapshot": string(report.Snapshot)})), "workcopySnapshotRemoved")
	if removed.Snapshot != report.Snapshot {
		t.Fatalf("removed %+v", removed)
	}
	workcopyFailureOf(t, tree.send(t, workcopySnapshotPattern, tree.body(map[string]any{"snapshot": string(report.Snapshot)})), WorkcopyErrorKindNotFound)
	// Discard.
	revision = tree.stage(t, workcopy.Change{Path: "src/x.py", Text: []byte("x")})
	discarded := workcopyAnswer[WorkcopyDiscarded](t, tree.send(t, workcopyDiscardPattern, tree.body(map[string]any{"revision": revision})), "workcopyDiscarded")
	if discarded.Discarded != 1 {
		t.Fatalf("discarded %+v", discarded)
	}
	if _, err := os.Lstat(filepath.Join(tree.root, "orders", "src", "x.py")); !os.IsNotExist(err) {
		t.Fatalf("a discard wrote the project: %v", err)
	}
}

func TestADriftIsAnsweredWithEveryFileAndNoText(t *testing.T) {
	t.Parallel()
	tree := newWorkcopyTree(t, true)
	revision := tree.stage(t,
		workcopy.Change{Path: "src/main.py", Text: []byte("secret staged text")},
		workcopy.Change{Path: "src/created.py", Text: []byte("x")},
	)
	if err := os.WriteFile(filepath.Join(tree.root, "orders", "src", "main.py"), []byte("changed on disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree.root, "orders", "src", "created.py"), []byte("created"), 0o644); err != nil {
		t.Fatal(err)
	}
	answer := tree.send(t, workcopyApplyPattern, tree.body(map[string]any{"revision": revision}))
	failure := workcopyFailureOf(t, answer, WorkcopyErrorKindDrift)
	if len(failure.Drift) != 2 || failure.Drift[0].Path != "src/created.py" || failure.Drift[0].Now != WorkcopyStateCreated || failure.Drift[1].Now != WorkcopyStateChanged {
		t.Fatalf("drift %+v", failure.Drift)
	}
	if strings.Contains(answer.Body, "secret staged text") || strings.Contains(answer.Body, tree.home) {
		t.Fatalf("the answer carries text or a path: %s", answer.Body)
	}
	if strings.Contains(tree.server.logs.String(), "secret staged text") || strings.Contains(tree.server.logs.String(), "src/main.py") {
		t.Fatalf("the log carries text or a path: %s", tree.server.logs.String())
	}
	// The root revoked: listed unavailable with its reason, refused on apply.
	if _, err := tree.roots.Revoke(tree.root); err != nil {
		t.Fatal(err)
	}
	workcopyFailureOf(t, tree.send(t, workcopyApplyPattern, tree.body(map[string]any{"revision": revision})), WorkcopyErrorKindNotAuthorized)
	list := workcopyAnswer[WorkcopyList](t, tree.server.do(t, tree.server.allowed("/api/workcopies")), "")
	if len(list.Copies) != 1 || list.Copies[0].Available || *list.Copies[0].Reason != WorkcopyErrorKindNotAuthorized {
		t.Fatalf("list %+v", list)
	}
}

func TestWithNoConfigDirectoryTheEndpointsAnswerUnavailable(t *testing.T) {
	t.Parallel()
	tree := newWorkcopyTree(t, false)
	workcopyFailureOf(t, tree.server.do(t, tree.server.allowed("/api/workcopies")), WorkcopyErrorKindUnavailable)
	workcopyFailureOf(t, tree.review(t), WorkcopyErrorKindUnavailable)
	workcopyFailureOf(t, tree.send(t, workcopyApplyPattern, tree.body(map[string]any{"revision": strings.Repeat("a", 32)})), WorkcopyErrorKindUnavailable)
	if got, _ := os.ReadFile(filepath.Join(tree.root, "orders", "src", "main.py")); string(got) != sourceText {
		t.Errorf("the file changed: %q", got)
	}
}
