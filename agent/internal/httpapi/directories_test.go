// The directory browsing endpoint (#7), against a real server and a real tree.

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// browseTree is a real tree and a server browsing it:
//
//	base/
//	  home/            the home directory: browsable, not authorized
//	    app/.git/      a project
//	    app/go.mod
//	    notes.txt
//	  project/src/     the one authorized root
//	  outside/secret/  neither
//	  outside/secret.txt
type browseTree struct {
	base, home, project, outside string
	server                       *testServer
}

func newBrowseTree(t *testing.T) *browseTree {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	tree := &browseTree{
		base:    base,
		home:    filepath.Join(base, "home"),
		project: filepath.Join(base, "project"),
		outside: filepath.Join(base, "outside"),
	}
	for _, directory := range []string{
		filepath.Join(tree.home, "app", ".git"),
		filepath.Join(tree.project, "src"),
		filepath.Join(tree.outside, "secret"),
	} {
		mustMkdir(t, directory)
	}
	mustWrite(t, filepath.Join(tree.home, "notes.txt"))
	mustWrite(t, filepath.Join(tree.home, "app", "go.mod"))
	mustWrite(t, filepath.Join(tree.outside, "secret.txt"))

	guard, err := projectfs.New(tree.project)
	if err != nil {
		t.Fatalf("authorize the project: %v", err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	browser, err := projectfs.NewBrowser(guard, tree.home)
	if err != nil {
		t.Fatalf("build the browser: %v", err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	tree.server = startServer(t, Options{Browser: browser})
	return tree
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("make %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// browse asks the endpoint for the given query, with the token and the
// server's own origin.
func (tree *browseTree) browse(t *testing.T, query url.Values) response {
	t.Helper()
	path := "/api/directories"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return tree.server.do(t, tree.server.allowed(path))
}

func (tree *browseTree) browsePath(t *testing.T, directory string) response {
	t.Helper()
	return tree.browse(t, url.Values{pathParameter: {directory}})
}

// listingOf checks an answer is a 200 listing the schema accepts, and decodes
// it.
func listingOf(t *testing.T, answer response) DirectoryListing {
	t.Helper()
	if answer.Status != http.StatusOK {
		t.Fatalf("want a 200 listing, got %s", answer)
	}
	listingSchema, _ := directorySchemas(t)
	if err := listingSchema.Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("the listing is not valid against the schema: %v\n%s", err, answer.Body)
	}
	var listing DirectoryListing
	if err := json.Unmarshal([]byte(answer.Body), &listing); err != nil {
		t.Fatalf("the listing does not parse into the generated struct: %v", err)
	}
	return listing
}

// failureOf checks an answer is the error the schema describes, with the
// status and kind wanted, and nothing else in it.
func failureOf(t *testing.T, answer response, status int, kind DirectoryErrorKind) {
	t.Helper()
	if answer.Status != status {
		t.Fatalf("want %d, got %s", status, answer)
	}
	_, errorSchema := directorySchemas(t)
	if err := errorSchema.Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("the error is not valid against the schema: %v\n%s", err, answer.Body)
	}
	var failure DirectoryError
	if err := json.Unmarshal([]byte(answer.Body), &failure); err != nil {
		t.Fatalf("the error does not parse: %v", err)
	}
	if failure.Error != kind || failure.Message != directoryFailures[kind].message {
		t.Fatalf("want a %s error with its fixed message, got %+v", kind, failure)
	}
}

func entryOf(t *testing.T, listing DirectoryListing, name string) DirectoryEntry {
	t.Helper()
	for _, entry := range listing.Entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("%s has no entry %q", listing.Path, name)
	return DirectoryEntry{}
}

func TestTheDirectoryEndpointIsBehindTheChecks(t *testing.T) {
	t.Parallel()
	tree := newBrowseTree(t)
	server := tree.server
	method, path := methodAndPathOf(t, directoriesPattern)
	withPath := path + "?" + url.Values{pathParameter: {tree.home}}.Encode()
	for _, target := range []string{path, withPath} {
		for name, want := range map[string]request{
			"no token":       {method: method, path: target, origins: []string{server.URL()}},
			"a wrong token":  {method: method, path: target, token: "wrong", origins: []string{server.URL()}},
			"a bad origin":   {method: method, path: target, token: server.Token().Secret(), origins: []string{"https://evil.example"}},
			"no origin":      {method: method, path: target, token: server.Token().Secret()},
			"nothing at all": {method: method, path: target},
		} {
			answer := server.do(t, want)
			if answer.Status != rejectionStatus || answer.Body != rejectionBody {
				t.Errorf("%s %s with %s was not rejected: %s", method, target, name, answer)
			}
		}
	}
	// The served handler itself, not only the route: an unauthorized request
	// by any method is the uniform rejection.
	for _, unauthorizedMethod := range []string{http.MethodGet, http.MethodPost, http.MethodHead, http.MethodDelete} {
		recorded := httptest.NewRecorder()
		server.http.Handler.ServeHTTP(recorded, httptest.NewRequest(unauthorizedMethod, "http://"+server.Addr()+withPath, nil))
		if recorded.Code != rejectionStatus || recorded.Body.String() != rejectionBody {
			t.Errorf("the served handler answered an unauthorized %s with %d %q", unauthorizedMethod, recorded.Code, recorded.Body.String())
		}
	}
	// The page's own fetch, which carries no Origin, is served like the
	// rows of the table are.
	if answer := server.do(t, server.pageFetch(withPath)); answer.Status != http.StatusOK {
		t.Errorf("the page's own fetch was answered %s", answer)
	}
}

func TestAServerWithNoBrowserRejectsTheEndpointUniformly(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	answer := server.do(t, server.allowed("/api/directories?path=%2F"))
	if answer.Status != rejectionStatus || answer.Body != rejectionBody {
		t.Fatalf("a server with no browser answered %s", answer)
	}
}

func TestTheEndpointListsTheHomeDirectoryByDefault(t *testing.T) {
	t.Parallel()
	tree := newBrowseTree(t)
	listing := listingOf(t, tree.browse(t, nil))
	if listing.Path != tree.home {
		t.Fatalf("with no path, listed %s, want the home directory %s", listing.Path, tree.home)
	}
	if listing.Parent != nil {
		t.Errorf("home has the parent %q, which is not browsable", *listing.Parent)
	}
	if entry := entryOf(t, listing, "app"); entry.Kind != DirectoryEntryKindDirectory || entry.Path != filepath.Join(tree.home, "app") {
		t.Errorf("app is %+v", entry)
	}
	if entry := entryOf(t, listing, "notes.txt"); entry.Kind != DirectoryEntryKindFile {
		t.Errorf("notes.txt is %+v, want a file", entry)
	}
}

func TestTheEndpointReportsWhetherADirectoryLooksLikeAProject(t *testing.T) {
	t.Parallel()
	tree := newBrowseTree(t)
	app := listingOf(t, tree.browsePath(t, filepath.Join(tree.home, "app")))
	if !app.LooksLikeProject {
		t.Fatalf("app does not look like a project: %+v", app)
	}
	want := []ProjectEvidence{{Marker: ".git", Describes: "a Git repository"}, {Marker: "go.mod", Describes: "a Go module"}}
	if fmt.Sprint(app.Evidence) != fmt.Sprint(want) {
		t.Fatalf("evidence is %+v, want %+v", app.Evidence, want)
	}
	if app.Parent == nil || *app.Parent != tree.home {
		t.Errorf("app's parent is %v, want %s", app.Parent, tree.home)
	}
	home := listingOf(t, tree.browsePath(t, tree.home))
	if home.LooksLikeProject || len(home.Evidence) != 0 {
		t.Fatalf("home looks like a project: %+v", home)
	}
}

func TestTheEndpointMarksSymlinksAndNeverFollowsOneOut(t *testing.T) {
	t.Parallel()
	tree := newBrowseTree(t)
	for name, target := range map[string]string{
		"to-app":     filepath.Join(tree.home, "app"),
		"to-notes":   "notes.txt",
		"to-secret":  filepath.Join(tree.outside, "secret"),
		"to-nothing": filepath.Join(tree.outside, "nothing"),
		"dangling":   "missing",
	} {
		if err := os.Symlink(target, filepath.Join(tree.home, name)); err != nil {
			t.Fatalf("link %s: %v", name, err)
		}
	}
	listing := listingOf(t, tree.browsePath(t, tree.home))
	for name, want := range map[string]LinkTarget{
		"to-app": LinkTargetDirectory, "to-notes": LinkTargetFile,
		"to-secret": LinkTargetOutside, "to-nothing": LinkTargetOutside, "dangling": LinkTargetBroken,
	} {
		entry := entryOf(t, listing, name)
		if entry.Kind != DirectoryEntryKindSymlink || entry.LinkTarget == nil || *entry.LinkTarget != want {
			t.Errorf("%s is %+v, want a symlink to %s", name, entry, want)
		}
	}
	for _, entry := range listing.Entries {
		if entry.Kind != DirectoryEntryKindSymlink && entry.LinkTarget != nil {
			t.Errorf("%s is not a link and has a link target", entry.Name)
		}
	}
	failureOf(t, tree.browsePath(t, filepath.Join(tree.home, "to-secret")), http.StatusForbidden, DirectoryErrorKindForbidden)
}

// The criterion is that a missing directory is told apart from a forbidden
// one; the constraint behind it is that a forbidden answer says nothing about
// what exists outside the browsable directories. So every outside shape gets
// the same bytes, and none of them mentions any path.
func TestAForbiddenAnswerSaysNothingAboutWhatIsOutside(t *testing.T) {
	t.Parallel()
	tree := newBrowseTree(t)
	if err := os.Symlink(tree.outside, filepath.Join(tree.home, "out")); err != nil {
		t.Fatalf("link: %v", err)
	}
	shapes := []string{
		filepath.Join(tree.outside, "secret"),
		filepath.Join(tree.outside, "secret.txt"),
		filepath.Join(tree.outside, "no-such-directory"),
		filepath.Join(tree.outside, "secret.txt", "below-a-file"),
		filepath.Join(tree.outside, "no-such-directory", "deeper"),
		tree.home + "/../outside/secret",
		tree.home + "/../outside/no-such-directory",
		tree.home + "/../home",
		filepath.Join(tree.home, "out"),
		filepath.Join(tree.home, "out", "secret"),
		filepath.Join(tree.home, "out", "no-such-directory"),
		tree.base,
		"/",
		"/etc",
		"/no-such-top-level-directory",
	}
	var first response
	for index, shape := range shapes {
		answer := tree.browsePath(t, shape)
		failureOf(t, answer, http.StatusForbidden, DirectoryErrorKindForbidden)
		if strings.Contains(answer.Body, tree.base) || strings.Contains(answer.Body, "/") {
			t.Errorf("the refusal of %s mentions a path: %s", shape, answer.Body)
		}
		if index == 0 {
			first = answer
			continue
		}
		if answer != first {
			t.Errorf("the refusal of %s differs from the refusal of %s:\n%s\n---\n%s", shape, shapes[0], answer, first)
		}
	}
}

func TestAMissingDirectoryIsReportedDistinctlyFromAForbiddenOne(t *testing.T) {
	t.Parallel()
	tree := newBrowseTree(t)
	missing := tree.browsePath(t, filepath.Join(tree.home, "no-such-directory"))
	failureOf(t, missing, http.StatusNotFound, DirectoryErrorKindNotFound)
	failureOf(t, tree.browsePath(t, filepath.Join(tree.project, "no-such-directory")), http.StatusNotFound, DirectoryErrorKindNotFound)
	forbidden := tree.browsePath(t, filepath.Join(tree.outside, "no-such-directory"))
	failureOf(t, forbidden, http.StatusForbidden, DirectoryErrorKindForbidden)
	if missing.Body == forbidden.Body {
		t.Fatal("missing and forbidden are the same answer")
	}
	// Nor is a missing directory confused with the uniform rejection, which
	// is the other 404.
	if missing.Body == rejectionBody {
		t.Fatal("a missing directory answers like a failed check")
	}
}

func TestTheAuthorizedRootAndHomeAreBothBrowsable(t *testing.T) {
	t.Parallel()
	tree := newBrowseTree(t)
	listingOf(t, tree.browsePath(t, tree.project))
	listingOf(t, tree.browsePath(t, filepath.Join(tree.project, "src")))
	listingOf(t, tree.browsePath(t, tree.home))
}

func TestPermissionErrorsAreReportedOverTheAPI(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("running as root, which every permission check lets through")
	}
	tree := newBrowseTree(t)
	locked := filepath.Join(tree.home, "locked")
	private := filepath.Join(tree.home, "private.txt")
	mustMkdir(t, filepath.Join(locked, "inside"))
	mustWrite(t, private)
	for _, path := range []string{locked, private} {
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("chmod %s: %v", path, err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
	}

	// An entry the agent cannot read is listed, by name, marked unreadable.
	listing := listingOf(t, tree.browsePath(t, tree.home))
	for _, name := range []string{"locked", "private.txt"} {
		if entry := entryOf(t, listing, name); !entry.Unreadable {
			t.Errorf("%s is %+v, want it marked unreadable", name, entry)
		}
	}
	if entry := entryOf(t, listing, "notes.txt"); entry.Unreadable {
		t.Errorf("a readable file is marked unreadable")
	}

	// A directory the agent cannot read is its own answer: not missing, and
	// not the outside refusal.
	for _, path := range []string{locked, filepath.Join(locked, "inside")} {
		failureOf(t, tree.browsePath(t, path), http.StatusForbidden, DirectoryErrorKindUnreadable)
	}
}

func TestAVeryLargeDirectoryIsAnsweredWithACappedPage(t *testing.T) {
	t.Parallel()
	tree := newBrowseTree(t)
	large := filepath.Join(tree.project, "large")
	mustMkdir(t, large)
	const total = 5000
	for index := 0; index < total; index++ {
		if err := os.WriteFile(filepath.Join(large, fmt.Sprintf("f%05d", index)), nil, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	full := listingOf(t, tree.browsePath(t, large))
	if len(full.Entries) != projectfs.MaxListing || !full.Truncated || full.Limit != projectfs.MaxListing {
		t.Fatalf("a directory of %d answered %d entries, truncated=%v, limit=%d", total, len(full.Entries), full.Truncated, full.Limit)
	}
	page := listingOf(t, tree.browse(t, url.Values{pathParameter: {large}, limitParameter: {"10"}}))
	if len(page.Entries) != 10 || !page.Truncated || page.Limit != 10 {
		t.Fatalf("a limit of 10 answered %d entries, truncated=%v, limit=%d", len(page.Entries), page.Truncated, page.Limit)
	}
	whole := listingOf(t, tree.browse(t, url.Values{pathParameter: {tree.home}, limitParameter: {"10"}}))
	if whole.Truncated {
		t.Fatalf("a directory smaller than the limit is marked truncated")
	}
}

func TestARequestTheEndpointWillNotReasonAboutIsInvalid(t *testing.T) {
	t.Parallel()
	tree := newBrowseTree(t)
	for name, query := range map[string]url.Values{
		"a relative path":    {pathParameter: {"home"}},
		"a dot path":         {pathParameter: {"./home"}},
		"an empty path":      {pathParameter: {""}},
		"a path with a NUL":  {pathParameter: {tree.home + "\x00x"}},
		"two paths":          {pathParameter: {tree.home, tree.project}},
		"a limit of zero":    {pathParameter: {tree.home}, limitParameter: {"0"}},
		"a negative limit":   {pathParameter: {tree.home}, limitParameter: {"-1"}},
		"a limit over 1000":  {pathParameter: {tree.home}, limitParameter: {"1001"}},
		"a limit of letters": {pathParameter: {tree.home}, limitParameter: {"ten"}},
		"two limits":         {pathParameter: {tree.home}, limitParameter: {"1", "2"}},
	} {
		t.Run(name, func(t *testing.T) {
			failureOf(t, tree.browse(t, query), http.StatusBadRequest, DirectoryErrorKindInvalid)
		})
	}
	failureOf(t, tree.browsePath(t, filepath.Join(tree.home, "notes.txt")), http.StatusConflict, DirectoryErrorKindNotDirectory)
}
