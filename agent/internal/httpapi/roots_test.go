// The project root endpoints (#63), against a real server and a real tree.

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// rootsTree is a real tree and a server authorizing in it:
//
//	base/
//	  home/            the home directory
//	    code/app/src/main.go
//	    code/lib/
//	    notes.txt
//	  config/kpv/      the store
//	  outside/secret/  outside home
//	  outside/secret.txt
type rootsTree struct {
	base, home, outside, store string
	guard                      *projectfs.Guard
	roots                      *projectfs.Roots
	server                     *testServer
}

func newRootsTree(t *testing.T) *rootsTree {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	tree := &rootsTree{
		base:    base,
		home:    filepath.Join(base, "home"),
		outside: filepath.Join(base, "outside"),
		store:   filepath.Join(base, "config", "kpv", "roots.json"),
	}
	for _, directory := range []string{
		filepath.Join(tree.home, "code", "app", "src"),
		filepath.Join(tree.home, "code", "lib"),
		filepath.Join(tree.outside, "secret"),
	} {
		mustMkdir(t, directory)
	}
	mustWrite(t, filepath.Join(tree.home, "notes.txt"))
	mustWrite(t, filepath.Join(tree.home, "code", "app", "src", "main.go"))
	mustWrite(t, filepath.Join(tree.outside, "secret.txt"))
	tree.start(t)
	return tree
}

// start serves the tree with a fresh guard, browser and roots read from the
// store, the way an agent starting up does.
func (tree *rootsTree) start(t *testing.T) {
	t.Helper()
	guard, err := projectfs.New()
	if err != nil {
		t.Fatalf("new guard: %v", err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	browser, err := projectfs.NewBrowser(guard, tree.home)
	if err != nil {
		t.Fatalf("new browser: %v", err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	roots, err := projectfs.OpenRoots(browser, tree.store, discardLogger())
	if err != nil {
		t.Fatalf("open the roots: %v", err)
	}
	tree.guard, tree.roots = guard, roots
	tree.server = startServer(t, Options{Browser: browser, Roots: roots})
}

func (tree *rootsTree) in(elements ...string) string {
	return filepath.Join(append([]string{tree.home}, elements...)...)
}

// send makes an allowed request to the root endpoints with a JSON body.
func (tree *rootsTree) send(t *testing.T, method, body string) response {
	t.Helper()
	want := tree.server.allowed("/api/roots")
	want.method = method
	want.body = body
	want.headers = http.Header{"Content-Type": {"application/json"}}
	return tree.server.do(t, want)
}

func pathBody(path string) string {
	body, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		panic(err)
	}
	return string(body)
}

func (tree *rootsTree) authorize(t *testing.T, path string) response {
	t.Helper()
	return tree.send(t, http.MethodPost, pathBody(path))
}

func (tree *rootsTree) revoke(t *testing.T, path string) response {
	t.Helper()
	return tree.send(t, http.MethodDelete, pathBody(path))
}

func (tree *rootsTree) list(t *testing.T) RootList {
	t.Helper()
	return rootListFrom(t, tree.server.do(t, tree.server.allowed("/api/roots")))
}

func rootListFrom(t *testing.T, answer response) RootList {
	t.Helper()
	if answer.Status != http.StatusOK {
		t.Fatalf("want a 200 root list, got %s", answer)
	}
	if err := rootsSchemas(t).list.Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("the list is not valid against the schema: %v\n%s", err, answer.Body)
	}
	var list RootList
	if err := json.Unmarshal([]byte(answer.Body), &list); err != nil {
		t.Fatalf("the list does not parse: %v", err)
	}
	assertRootsPairings(t, &list)
	return list
}

func authorizedFrom(t *testing.T, answer response) string {
	t.Helper()
	if answer.Status != http.StatusOK {
		t.Fatalf("want a 200 authorization, got %s", answer)
	}
	if err := rootsSchemas(t).authorized.Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("the answer is not valid against the schema: %v\n%s", err, answer.Body)
	}
	var authorized AuthorizedRoot
	if err := json.Unmarshal([]byte(answer.Body), &authorized); err != nil {
		t.Fatalf("the answer does not parse: %v", err)
	}
	return authorized.Path
}

// rootsFailureOf checks an answer is the schema's error with the status, the
// kind and the fixed sentence wanted, and returns it.
func rootsFailureOf(t *testing.T, answer response, kind RootsErrorKind) RootsError {
	t.Helper()
	want := rootsFailures[kind]
	if answer.Status != want.status {
		t.Fatalf("want %d, got %s", want.status, answer)
	}
	if err := rootsSchemas(t).failure.Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
		t.Fatalf("the error is not valid against the schema: %v\n%s", err, answer.Body)
	}
	var failure RootsError
	if err := json.Unmarshal([]byte(answer.Body), &failure); err != nil {
		t.Fatalf("the error does not parse: %v", err)
	}
	if failure.Error != kind || failure.Message != want.message {
		t.Fatalf("want a %s error with its fixed message, got %+v", kind, failure)
	}
	assertRootsPairings(t, &failure)
	return failure
}

func paths(list RootList) []string {
	out := []string{}
	for _, root := range list.Roots {
		out = append(out, root.Path)
	}
	return out
}

func TestTheRootEndpointsAreBehindTheChecks(t *testing.T) {
	t.Parallel()
	tree := newRootsTree(t)
	server := tree.server
	body := pathBody(tree.in("code", "app"))
	jsonType := http.Header{"Content-Type": {"application/json"}}
	for _, pattern := range []string{rootsListPattern, rootsAuthorizePattern, rootsRevokePattern} {
		method, path := methodAndPathOf(t, pattern)
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
	// Nothing was authorized by any of that.
	if list := tree.list(t); len(list.Roots) != 0 {
		t.Fatalf("a rejected request authorized %v", list)
	}
}

// #58's relaxation is GET and HEAD only: a POST or DELETE with no Origin is
// rejected even with the token and Sec-Fetch-Site: same-origin.
func TestAStateChangingRootRequestWithNoOriginIsRejectedWhateverFetchMetadataSays(t *testing.T) {
	t.Parallel()
	tree := newRootsTree(t)
	server := tree.server
	app := tree.in("code", "app")
	if got := authorizedFrom(t, tree.authorize(t, app)); got != app {
		t.Fatalf("authorized %s", got)
	}
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		for _, site := range []string{"same-origin", "none"} {
			want := server.pageFetch("/api/roots")
			want.method = method
			want.body = pathBody(tree.in("code", "lib"))
			want.headers.Set("Content-Type", "application/json")
			want.headers.Set(secFetchSiteHeader, site)
			if answer := server.do(t, want); answer.Status != rejectionStatus || answer.Body != rejectionBody {
				t.Errorf("%s with no Origin and Sec-Fetch-Site: %s was answered %s", method, site, answer)
			}
		}
	}
	if got := paths(tree.list(t)); !slices.Equal(got, []string{app}) {
		t.Fatalf("an origin-less request changed the roots: %v", got)
	}
	// The page's own GET is served, as #58 has it.
	if answer := server.do(t, server.pageFetch("/api/roots")); answer.Status != http.StatusOK {
		t.Fatalf("the page's own GET was answered %s", answer)
	}
}

func TestAServerWithNoRootsRejectsTheEndpointsUniformly(t *testing.T) {
	t.Parallel()
	server := startServer(t, Options{})
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		want := server.allowed("/api/roots")
		want.method = method
		want.body = `{"path":"/x"}`
		want.headers = http.Header{"Content-Type": {"application/json"}}
		if answer := server.do(t, want); answer.Status != rejectionStatus || answer.Body != rejectionBody {
			t.Errorf("a server with no roots answered %s with %s", method, answer)
		}
	}
}

func TestARootRequestTheEndpointsWillNotReasonAboutIsInvalid(t *testing.T) {
	t.Parallel()
	tree := newRootsTree(t)
	app := tree.in("code", "app")
	valid := pathBody(app)
	long := pathBody("/" + strings.Repeat("a", maxRootRequestBytes))
	cases := map[string]struct {
		body         string
		contentTypes []string
	}{
		"not JSON":                   {"path=" + app, []string{"application/json"}},
		"no Content-Type":            {valid, nil},
		"a text Content-Type":        {valid, []string{"text/plain"}},
		"a form Content-Type":        {valid, []string{"application/x-www-form-urlencoded"}},
		"a JSON suffix type":         {valid, []string{"application/merge-patch+json"}},
		"another charset":            {valid, []string{"application/json; charset=latin1"}},
		"another parameter":          {valid, []string{"application/json; profile=x"}},
		"two Content-Types":          {valid, []string{"application/json", "application/json"}},
		"a broken Content-Type":      {valid, []string{"application/json; ="}},
		"an unknown field":           {`{"path":"` + app + `","recursive":true}`, []string{"application/json"}},
		"only an unknown field":      {`{"directory":"` + app + `"}`, []string{"application/json"}},
		"the path in another case":   {`{"Path":"` + app + `"}`, []string{"application/json"}},
		"no path":                    {`{}`, []string{"application/json"}},
		"the path twice":             {`{"path":"` + app + `","path":"` + app + `"}`, []string{"application/json"}},
		"the path twice, different":  {`{"path":"/etc","path":"` + app + `"}`, []string{"application/json"}},
		"a path that is a number":    {`{"path":1}`, []string{"application/json"}},
		"a path that is null":        {`{"path":null}`, []string{"application/json"}},
		"a path that is an array":    {`{"path":["` + app + `"]}`, []string{"application/json"}},
		"a path that is an object":   {`{"path":{"p":"` + app + `"}}`, []string{"application/json"}},
		"an empty path":              {`{"path":""}`, []string{"application/json"}},
		"a relative path":            {`{"path":"code/app"}`, []string{"application/json"}},
		"a dot path":                 {`{"path":"./code/app"}`, []string{"application/json"}},
		"a path with a NUL":          {`{"path":"` + app + `\u0000x"}`, []string{"application/json"}},
		"an array":                   {`["` + app + `"]`, []string{"application/json"}},
		"a string":                   {`"` + app + `"`, []string{"application/json"}},
		"two objects":                {valid + valid, []string{"application/json"}},
		"trailing data":              {valid + " x", []string{"application/json"}},
		"an unfinished object":       {`{"path":"` + app + `"`, []string{"application/json"}},
		"over the cap":               {long, []string{"application/json"}},
		"invalid UTF-8":              {"{\"path\":\"" + app + "\xff\"}", []string{"application/json"}},
		"an empty body":              {"", []string{"application/json"}},
		"whitespace only":            {"   ", []string{"application/json"}},
		"a body of padding over cap": {valid + strings.Repeat(" ", maxRootRequestBytes), []string{"application/json"}},
	}
	for name, test := range cases {
		for _, method := range []string{http.MethodPost, http.MethodDelete} {
			t.Run(method+"/"+name, func(t *testing.T) {
				want := tree.server.allowed("/api/roots")
				want.method = method
				want.body = test.body
				if test.contentTypes != nil {
					want.headers = http.Header{"Content-Type": test.contentTypes}
				}
				rootsFailureOf(t, tree.server.do(t, want), RootsErrorKindInvalid)
			})
		}
	}
	if list := tree.list(t); len(list.Roots) != 0 {
		t.Fatalf("an invalid request authorized %v", list)
	}
	// And the shapes that are fine: a charset, and the cap exactly.
	for _, contentType := range []string{"application/json; charset=utf-8", "Application/JSON; Charset=UTF-8"} {
		want := tree.server.allowed("/api/roots")
		want.method = http.MethodPost
		want.body = valid
		want.headers = http.Header{"Content-Type": {contentType}}
		authorizedFrom(t, tree.server.do(t, want))
	}
	padded := valid + strings.Repeat(" ", maxRootRequestBytes-len(valid))
	if len(padded) != maxRootRequestBytes {
		t.Fatalf("the padded body is %d bytes", len(padded))
	}
	authorizedFrom(t, tree.send(t, http.MethodPost, padded))
}

// Authorization goes through the guard the browser shares, so a root
// authorized over the API is browsable at once, by the API, without a restart.
func TestAnAuthorizedRootIsListedAndBrowsableAtOnce(t *testing.T) {
	t.Parallel()
	tree := newRootsTree(t)
	app := tree.in("code", "app")
	if got := authorizedFrom(t, tree.authorize(t, app)); got != app {
		t.Fatalf("authorized %s, want %s", got, app)
	}
	list := tree.list(t)
	if len(list.Roots) != 1 || list.Roots[0].Path != app || !list.Roots[0].Available || list.Roots[0].Reason != nil {
		t.Fatalf("listed %+v", list)
	}
	if roots := tree.guard.Roots(); !slices.Equal(roots, []string{app}) {
		t.Fatalf("the browser's guard holds %v", roots)
	}
	listing := listingOf(t, tree.server.do(t, tree.server.allowed("/api/directories?path="+app)))
	if listing.Path != app || len(listing.Entries) != 1 || listing.Entries[0].Name != "src" {
		t.Fatalf("browsing the new root gave %+v", listing)
	}
}

// A forbidden answer is the same bytes, headers included, whatever the path
// outside home is, and says nothing about it.
func TestAForbiddenAuthorizationSaysNothingAboutWhatIsOutside(t *testing.T) {
	t.Parallel()
	tree := newRootsTree(t)
	locked := filepath.Join(tree.outside, "locked")
	mustMkdir(t, filepath.Join(locked, "inner"))
	if os.Geteuid() != 0 {
		if err := os.Chmod(locked, 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	}
	for name, target := range map[string]string{
		"loop-a": "loop-b", "loop-b": "loop-a",
	} {
		if err := os.Symlink(target, filepath.Join(tree.outside, name)); err != nil {
			t.Fatalf("link: %v", err)
		}
	}
	for name, target := range map[string]string{
		"to-secret": filepath.Join(tree.outside, "secret"), "to-file": filepath.Join(tree.outside, "secret.txt"),
		"to-missing": filepath.Join(tree.outside, "missing"), "to-loop": filepath.Join(tree.outside, "loop-a"),
	} {
		if err := os.Symlink(target, tree.in(name)); err != nil {
			t.Fatalf("link: %v", err)
		}
	}
	shapes := []string{
		filepath.Join(tree.outside, "secret"),
		filepath.Join(tree.outside, "secret.txt"),
		filepath.Join(tree.outside, "missing"),
		filepath.Join(tree.outside, "missing", "deeper"),
		filepath.Join(tree.outside, "secret.txt", "below"),
		locked,
		filepath.Join(locked, "inner"),
		filepath.Join(tree.outside, "loop-a"),
		tree.in("to-secret"),
		tree.in("to-file"),
		tree.in("to-missing"),
		tree.in("to-loop"),
		tree.home + "/../outside/secret",
		tree.home + "/../outside/missing",
		filepath.Dir(tree.store),
		"/etc",
		"/no-such-top-level-directory",
	}
	var first response
	for index, shape := range shapes {
		answer := tree.authorize(t, shape)
		rootsFailureOf(t, answer, RootsErrorKindForbidden)
		if strings.Contains(answer.Body, "/") {
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
	if list := tree.list(t); len(list.Roots) != 0 {
		t.Fatalf("an outside path was authorized: %v", list)
	}
	// The log carries the kind only.
	if strings.Contains(tree.server.logs.String(), tree.outside) {
		t.Fatalf("the log names an outside path:\n%s", tree.server.logs.String())
	}
}

func TestEachRefusalOfAnAuthorizationHasItsOwnKind(t *testing.T) {
	t.Parallel()
	tree := newRootsTree(t)
	mustMkdir(t, tree.in("sub"))
	if err := os.Symlink(tree.home, tree.in("self")); err != nil {
		t.Fatalf("link: %v", err)
	}
	cases := map[string]RootsErrorKind{
		"/":                       RootsErrorKindTooBroad,
		tree.base:                 RootsErrorKindTooBroad,
		tree.home:                 RootsErrorKindTooBroad,
		tree.home + "/":           RootsErrorKindTooBroad,
		tree.home + "/.":          RootsErrorKindTooBroad,
		tree.in("sub") + "/..":    RootsErrorKindTooBroad,
		tree.in("self"):           RootsErrorKindTooBroad,
		tree.in("missing"):        RootsErrorKindNotFound,
		tree.in("notes.txt"):      RootsErrorKindNotDirectory,
		tree.in("missing", "sub"): RootsErrorKindNotFound,
	}
	if os.Geteuid() != 0 {
		for _, mode := range []os.FileMode{0o000, 0o111, 0o444} {
			locked := tree.in(fmt.Sprintf("locked-%o", mode))
			mustMkdir(t, filepath.Join(locked, "inner"))
			if err := os.Chmod(locked, mode); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
			cases[locked] = RootsErrorKindUnreadable
		}
		cases[filepath.Join(tree.in("locked-0"), "inner")] = RootsErrorKindUnreadable
	}
	for path, kind := range cases {
		answer := tree.authorize(t, path)
		rootsFailureOf(t, answer, kind)
		if strings.Contains(answer.Body, "/") {
			t.Errorf("the refusal of %s mentions a path: %s", path, answer.Body)
		}
	}
	if list := tree.list(t); len(list.Roots) != 0 {
		t.Fatalf("a refused path was authorized: %v", list)
	}
	// tooBroad is its own answer, not forbidden's.
	if rootsFailures[RootsErrorKindTooBroad].message == rootsFailures[RootsErrorKindForbidden].message {
		t.Fatal("tooBroad and forbidden say the same thing")
	}
}

func TestAuthorizingAgainOrConcurrentlyLeavesOneRoot(t *testing.T) {
	t.Parallel()
	tree := newRootsTree(t)
	app := tree.in("code", "app")
	if err := os.Symlink(app, tree.in("link")); err != nil {
		t.Fatalf("link: %v", err)
	}
	spellings := []string{app, app + "/", app + "/.", app + "/src/..", tree.in("link"), tree.in("code") + "//app"}
	var group sync.WaitGroup
	answers := make(chan response, 24)
	for index := 0; index < 24; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			answers <- tree.authorize(t, spellings[index%len(spellings)])
		}()
	}
	group.Wait()
	close(answers)
	for answer := range answers {
		if got := authorizedFrom(t, answer); got != app {
			t.Fatalf("a concurrent authorization answered %s", got)
		}
	}
	if got := paths(tree.list(t)); !slices.Equal(got, []string{app}) {
		t.Fatalf("want exactly one root, got %v", got)
	}
	if roots := tree.guard.Roots(); len(roots) != 1 {
		t.Fatalf("the guard holds %v", roots)
	}
}

func TestANestedRootIsRefusedAndTheAnswerNamesTheExistingOne(t *testing.T) {
	t.Parallel()
	tree := newRootsTree(t)
	app := tree.in("code", "app")
	authorizedFrom(t, tree.authorize(t, app))
	for _, path := range []string{filepath.Join(app, "src"), tree.in("code")} {
		failure := rootsFailureOf(t, tree.authorize(t, path), RootsErrorKindOverlaps)
		if failure.Root == nil || *failure.Root != app {
			t.Fatalf("the overlap of %s names %v, want %s", path, failure.Root, app)
		}
	}
	// Revoke the outer root, and the one inside it can be authorized.
	rootListFrom(t, tree.revoke(t, app))
	authorizedFrom(t, tree.authorize(t, filepath.Join(app, "src")))
	// And now the containing one is the overlap.
	failure := rootsFailureOf(t, tree.authorize(t, app), RootsErrorKindOverlaps)
	if failure.Root == nil || *failure.Root != filepath.Join(app, "src") {
		t.Fatalf("the overlap names %v", failure.Root)
	}
}

func TestAuthorizingThroughALinkIsTheRealTargetAndMovingTheLinkChangesNothing(t *testing.T) {
	t.Parallel()
	tree := newRootsTree(t)
	app := tree.in("code", "app")
	link := tree.in("link")
	if err := os.Symlink(app, link); err != nil {
		t.Fatalf("link: %v", err)
	}
	if got := authorizedFrom(t, tree.authorize(t, link)); got != app {
		t.Fatalf("authorized %s, want the real %s", got, app)
	}
	if got := paths(tree.list(t)); !slices.Equal(got, []string{app}) {
		t.Fatalf("listed %v", got)
	}
	raw, err := os.ReadFile(tree.store)
	if err != nil || strings.Contains(string(raw), link) || !strings.Contains(string(raw), app) {
		t.Fatalf("the store holds %s (%v)", raw, err)
	}

	// Re-point the link, then replace it with a directory: neither changes
	// what the root is or reaches.
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.Symlink(tree.in("code", "lib"), link); err != nil {
		t.Fatalf("relink: %v", err)
	}
	if got := paths(tree.list(t)); !slices.Equal(got, []string{app}) {
		t.Fatalf("after the link moved, listed %v", got)
	}
	if roots := tree.guard.Roots(); !slices.Equal(roots, []string{app}) {
		t.Fatalf("after the link moved, the guard holds %v", roots)
	}
	listing := listingOf(t, tree.server.do(t, tree.server.allowed("/api/directories?path="+app)))
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "src" {
		t.Fatalf("the root lists %+v after the link moved", listing.Entries)
	}
	if err := tree.guard.WriteFile(filepath.Join(tree.in("code", "lib"), "x"), []byte("x"), 0o644); !projectfs.IsForbidden(err) {
		t.Fatalf("the link's new target became writable: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove: %v", err)
	}
	mustMkdir(t, link)
	if got := paths(tree.list(t)); !slices.Equal(got, []string{app}) {
		t.Fatalf("after the link was replaced, listed %v", got)
	}
	if _, err := tree.guard.ReadFile(filepath.Join(app, "src", "main.go")); err != nil {
		t.Fatalf("the root stopped reaching its directory: %v", err)
	}

	// A link inside home that leads outside is refused like any outside path.
	if err := os.Symlink(filepath.Join(tree.outside, "secret"), tree.in("out")); err != nil {
		t.Fatalf("link: %v", err)
	}
	outside := tree.authorize(t, filepath.Join(tree.outside, "missing"))
	rootsFailureOf(t, outside, RootsErrorKindForbidden)
	if answer := tree.authorize(t, tree.in("out")); answer != outside {
		t.Fatalf("a link out answered differently from an outside path:\n%s\n---\n%s", answer, outside)
	}
}

func TestRevokingARootOverTheAPI(t *testing.T) {
	t.Parallel()
	tree := newRootsTree(t)
	app := tree.in("code", "app")
	lib := tree.in("code", "lib")
	authorizedFrom(t, tree.authorize(t, app))
	authorizedFrom(t, tree.authorize(t, lib))

	remaining := rootListFrom(t, tree.revoke(t, app))
	if got := paths(remaining); !slices.Equal(got, []string{lib}) {
		t.Fatalf("the revoke answered %v", got)
	}
	if got := paths(tree.list(t)); !slices.Equal(got, []string{lib}) {
		t.Fatalf("after the revoke, listed %v", got)
	}
	// Writes under it are forbidden and create nothing; nothing was deleted.
	created := filepath.Join(app, "src", "created.txt")
	if err := tree.guard.WriteFile(created, []byte("x"), 0o644); !projectfs.IsForbidden(err) {
		t.Fatalf("a write after the revoke = %v", err)
	}
	if _, err := os.Lstat(created); !os.IsNotExist(err) {
		t.Fatalf("the refused write created a file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(app, "src", "main.go")); err != nil {
		t.Fatalf("the revoke deleted something: %v", err)
	}
	// Still browsable read-only, because it is under home.
	listing := listingOf(t, tree.server.do(t, tree.server.allowed("/api/directories?path="+app)))
	if listing.Path != app {
		t.Fatalf("the revoked root cannot be browsed: %+v", listing)
	}
}

func TestRevokingWhatIsNotARootIsNotFoundWhetherOrNotItExists(t *testing.T) {
	t.Parallel()
	tree := newRootsTree(t)
	authorizedFrom(t, tree.authorize(t, tree.in("code", "app")))
	var first response
	for index, path := range []string{
		tree.in("code", "lib"),
		tree.in("missing"),
		tree.in("code", "app", "src"),
		tree.in("notes.txt"),
		filepath.Join(tree.outside, "secret"),
		filepath.Join(tree.outside, "missing"),
		tree.home,
		"/",
	} {
		answer := tree.revoke(t, path)
		if answer.Status != revokeNotFound.status {
			t.Fatalf("revoking %s answered %s", path, answer)
		}
		var failure RootsError
		if err := json.Unmarshal([]byte(answer.Body), &failure); err != nil || failure.Error != RootsErrorKindNotFound || failure.Message != revokeNotFound.message || failure.Root != nil {
			t.Fatalf("revoking %s answered %s (%v)", path, answer.Body, err)
		}
		if err := rootsSchemas(t).failure.Validate(decodeDocument(t, []byte(answer.Body))); err != nil {
			t.Fatalf("not valid: %v", err)
		}
		if index == 0 {
			first = answer
		} else if answer != first {
			t.Errorf("revoking %s differs from revoking a directory that exists:\n%s\n---\n%s", path, answer, first)
		}
	}
	if got := paths(tree.list(t)); len(got) != 1 {
		t.Fatalf("a refused revoke changed the roots: %v", got)
	}
}

func TestARootIsRevokedByADifferentSpelling(t *testing.T) {
	t.Parallel()
	for name, spelling := range map[string]func(tree *rootsTree) string{
		"a trailing slash":              func(tree *rootsTree) string { return tree.in("code", "app") + "/" },
		"the link it was authorized by": func(tree *rootsTree) string { return tree.in("link") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tree := newRootsTree(t)
			if err := os.Symlink(tree.in("code", "app"), tree.in("link")); err != nil {
				t.Fatalf("link: %v", err)
			}
			authorizedFrom(t, tree.authorize(t, tree.in("link")))
			if got := paths(rootListFrom(t, tree.revoke(t, spelling(tree)))); len(got) != 0 {
				t.Fatalf("the revoke left %v", got)
			}
			if roots := tree.guard.Roots(); len(roots) != 0 {
				t.Fatalf("the guard still holds %v", roots)
			}
		})
	}
}

// The set survives a restart: a new guard, browser, roots and server over the
// same store list the same roots, available and browsable.
func TestTheRootsSurviveARestartOfTheServer(t *testing.T) {
	t.Parallel()
	tree := newRootsTree(t)
	app := tree.in("code", "app")
	lib := tree.in("code", "lib")
	authorizedFrom(t, tree.authorize(t, app))
	authorizedFrom(t, tree.authorize(t, lib))
	if err := tree.server.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// One stored root goes away while the agent is down.
	if err := os.RemoveAll(lib); err != nil {
		t.Fatalf("remove: %v", err)
	}
	tree.start(t)
	list := tree.list(t)
	if len(list.Roots) != 2 || list.Roots[0] != (ProjectRoot{Path: app, Available: true}) {
		t.Fatalf("after the restart, listed %+v", list)
	}
	if gone := list.Roots[1]; gone.Path != lib || gone.Available || gone.Reason == nil || *gone.Reason != RootUnavailableReasonNotFound {
		t.Fatalf("the missing root is listed as %+v", gone)
	}
	listingOf(t, tree.server.do(t, tree.server.allowed("/api/directories?path="+app)))
	// Revoking the unavailable one removes it from the store.
	if got := paths(rootListFrom(t, tree.revoke(t, lib))); !slices.Equal(got, []string{app}) {
		t.Fatalf("revoking the unavailable root left %v", got)
	}
	raw, err := os.ReadFile(tree.store)
	if err != nil || strings.Contains(string(raw), lib) {
		t.Fatalf("the store still holds the revoked root: %s (%v)", raw, err)
	}
}

// The token never reaches the store: the store holds the version and the
// roots, and nothing the server was given.
func TestTheTokenIsNeverWrittenToTheStore(t *testing.T) {
	t.Parallel()
	tree := newRootsTree(t)
	authorizedFrom(t, tree.authorize(t, tree.in("code", "app")))
	raw, err := os.ReadFile(tree.store)
	if err != nil {
		t.Fatalf("read the store: %v", err)
	}
	if strings.Contains(string(raw), tree.server.Token().Secret()) {
		t.Fatalf("the token is in the store: %s", raw)
	}
	if strings.Contains(tree.server.logs.String(), tree.server.Token().Secret()) {
		t.Fatal("the token is in the log")
	}
}

// Every reason projectfs can give a stored root is one the schema has.
func TestEveryUnavailableReasonIsInTheSchema(t *testing.T) {
	t.Parallel()
	for _, kind := range []projectfs.ErrorKind{
		projectfs.KindNotFound, projectfs.KindNotDirectory, projectfs.KindUnreadable, projectfs.KindForbidden,
		projectfs.KindTooBroad, projectfs.KindChanged, projectfs.KindOverlaps, projectfs.KindIO,
	} {
		if !slices.Contains(enumValues_RootUnavailableReason, any(string(kind))) {
			t.Errorf("projectfs reports the reason %s, which the schema does not have", kind)
		}
	}
}
