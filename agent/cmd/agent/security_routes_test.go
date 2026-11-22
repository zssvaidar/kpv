//go:build unix

// Which routes the security suite attacks, and what each one takes. The
// routes are discovered from what newHandler in internal/httpapi/routes.go
// registers, never listed here; routeTable says what each takes, and
// TestSecurityTableHasARowForEveryRegisteredRoute fails when the two
// disagree in either direction. A new route is one row.

package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// httpapiDir is the package whose routes.go registers the routes.
const httpapiDir = "../../internal/httpapi"

// registeredRoute is one pattern newHandler registers.
type registeredRoute struct {
	pattern, method, path string
}

// discoverRoutes reads routes.go and resolves what newHandler registers: each
// mux.Handle or HandleFunc in it, whose pattern is a string or a constant of
// the package, and, for the loop over the routes it is handed, each row of
// apiRoutes(), which is what the server hands it
// (TestTheServedHandlerIsExactlyTheGuardedOne in httpapi holds that). The
// package's other files are read only for their constant declarations, which
// is where the pattern constants are.
func discoverRoutes(t *testing.T) []registeredRoute {
	t.Helper()
	routes, problems := discoverRoutesIn(httpapiDir)
	for _, problem := range problems {
		t.Error(problem)
	}
	return routes
}

// discoverRoutesIn is discoverRoutes over any directory, answering what it
// could not resolve as problems rather than failing, so a planted package
// can be checked too.
func discoverRoutesIn(dir string) ([]registeredRoute, []string) {
	var problems []string
	fail := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	set := token.NewFileSet()
	constants := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []string{fmt.Sprintf("read %s: %v", dir, err)}
	}
	var routesFile *ast.File
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(set, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, []string{fmt.Sprintf("parse %s: %v", name, err)}
		}
		if name == "routes.go" {
			routesFile = file
		}
		for _, decl := range file.Decls {
			general, ok := decl.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value := spec.(*ast.ValueSpec)
				for i, ident := range value.Names {
					if i < len(value.Values) {
						if literal, ok := value.Values[i].(*ast.BasicLit); ok && literal.Kind == token.STRING {
							text, _ := strconv.Unquote(literal.Value)
							constants[ident.Name] = text
						}
					}
				}
			}
		}
	}
	if routesFile == nil {
		return nil, []string{"internal/httpapi has no routes.go, so the suite cannot find what is registered"}
	}
	resolve := func(expression ast.Expr) (string, bool) {
		switch e := expression.(type) {
		case *ast.BasicLit:
			text, err := strconv.Unquote(e.Value)
			return text, err == nil && e.Kind == token.STRING
		case *ast.Ident:
			text, ok := constants[e.Name]
			return text, ok
		}
		return "", false
	}

	functions := map[string]*ast.FuncDecl{}
	for _, decl := range routesFile.Decls {
		if function, ok := decl.(*ast.FuncDecl); ok && function.Recv == nil {
			functions[function.Name.Name] = function
		}
	}
	newHandler, apiRoutes := functions["newHandler"], functions["apiRoutes"]
	if newHandler == nil || apiRoutes == nil {
		return nil, []string{"routes.go has no newHandler or no apiRoutes, so the suite cannot find what is registered"}
	}

	var patterns []string
	tableLoops := 0
	ast.Inspect(newHandler.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (selector.Sel.Name != "Handle" && selector.Sel.Name != "HandleFunc") || len(call.Args) != 2 {
			return true
		}
		if pattern, ok := resolve(call.Args[0]); ok {
			patterns = append(patterns, pattern)
			return true
		}
		// The one registration that is not a pattern is the loop over the
		// table newHandler is handed: r.pattern of a range over routes.
		if field, ok := call.Args[0].(*ast.SelectorExpr); ok && field.Sel.Name == "pattern" {
			tableLoops++
			return true
		}
		fail("newHandler registers %s with a pattern the suite cannot resolve to a method and path", selector.Sel.Name)
		return true
	})
	if tableLoops != 1 {
		fail("newHandler registers the table it is handed %d times, want once", tableLoops)
	}
	rows := 0
	ast.Inspect(apiRoutes.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if _, isArray := literal.Type.(*ast.ArrayType); isArray {
			for _, element := range literal.Elts {
				row, ok := element.(*ast.CompositeLit)
				if !ok || len(row.Elts) == 0 {
					fail("an apiRoutes row is not a route literal")
					continue
				}
				first := row.Elts[0]
				if pair, ok := first.(*ast.KeyValueExpr); ok {
					first = pair.Value
				}
				pattern, ok := resolve(first)
				if !ok {
					fail("an apiRoutes row has a pattern the suite cannot resolve")
					continue
				}
				patterns = append(patterns, pattern)
				rows++
			}
			return false
		}
		return true
	})
	if rows == 0 {
		fail("apiRoutes has no rows the suite can read")
	}

	var out []registeredRoute
	seen := map[string]bool{}
	for _, pattern := range patterns {
		method, path, found := strings.Cut(pattern, " ")
		if !found || method == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "{} ") {
			fail("the pattern %q is not a method and a literal path, which the suite does not know how to attack", pattern)
			continue
		}
		if seen[pattern] {
			fail("the pattern %q is registered twice", pattern)
		}
		seen[pattern] = true
		out = append(out, registeredRoute{pattern, method, path})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pattern < out[j].pattern })
	return out, problems
}

// routeRow is what one route takes. Every corpus is applied through it:
// query and body are its inputs, cap its body cap, paths the inputs that are
// paths and strs the inputs that are strings (paths included), kinds the
// error kinds it documents, and valid a value for every input that makes a
// request the route serves against the suite's fixture.
type routeRow struct {
	query []string
	body  []string
	cap   int
	paths []string
	strs  []string
	kinds []string
	// text is the string input the body corpus writes invalid UTF-8, a lone
	// surrogate and a NUL into.
	text  string
	valid func(f *fixture) map[string]any
	// channel marks the WebSocket handshake route.
	channel bool
}

// The error kinds each schema documents.
var (
	directoryKinds   = []string{"invalid", "forbidden", "notFound", "unreadable", "notDirectory", "io"}
	rootsKinds       = []string{"invalid", "forbidden", "tooBroad", "notFound", "notDirectory", "unreadable", "overlaps", "io", "agentSettings"}
	pipelinesKinds   = []string{"invalid", "notAuthorized", "rootUnavailable", "notFound", "unreadable", "rootIsPipeline", "changed", "noFreeID", "io"}
	sourceKinds      = []string{"invalid", "notAuthorized", "rootUnavailable", "notFound", "reserved", "generated", "link", "changed", "exists", "notRegularFile", "tooLarge", "notText", "unreadable", "io"}
	connectionsKinds = []string{"invalid", "notFound", "duplicateName", "referenced", "io"}
	workcopyKinds    = []string{"invalid", "unavailable", "notAuthorized", "rootUnavailable", "notFound", "link", "changed", "unreadable",
		"generated", "reserved", "notRegularFile", "tooLarge", "notText", "tooMany", "drift", "snapshot", "io"}
)

// The body caps, from the issue and each handler's comment.
const (
	rootsCap       = 4096
	createCap      = 8192
	sourceCap      = 6*(1<<20) + 16384
	connectionsCap = 262144
	workcopyCap    = 8192
)

// A revision and a snapshot id of the shapes the working-copy routes take,
// naming nothing staged.
const (
	noRevision = "00000000000000000000000000000000"
	noSnapshot = "20260101T000000Z-00000000"
)

// workcopyRow is a working-copy route whose body is root, directory and one
// pattern-checked field.
func workcopyRow(field, value string) routeRow {
	return routeRow{
		body: []string{"root", "directory", field}, cap: workcopyCap, paths: []string{"root", "directory"},
		strs: []string{"root", "directory", field}, kinds: workcopyKinds, text: field,
		valid: func(f *fixture) map[string]any {
			return map[string]any{"root": f.root, "directory": f.directory, field: value}
		},
	}
}

func noInputs(*fixture) map[string]any { return map[string]any{} }

// routeTable is every route the suite attacks, keyed by its pattern. The
// connection routes take no path: a PEM field is text
// (schema/connection.schema.json).
var routeTable = map[string]routeRow{
	"GET /api/health":            {valid: noInputs},
	"GET /api/version":           {valid: noInputs},
	"GET /api/channel":           {valid: noInputs, channel: true},
	"GET /api/roots":             {valid: noInputs},
	"GET /api/pipelines":         {valid: noInputs},
	"GET /api/connections":       {valid: noInputs},
	"GET /api/connections/names": {valid: noInputs},
	"GET /api/directories": {
		query: []string{"path"}, paths: []string{"path"}, strs: []string{"path"}, kinds: directoryKinds,
		valid: func(f *fixture) map[string]any { return map[string]any{"path": f.root} },
	},
	"POST /api/roots": {
		body: []string{"path"}, cap: rootsCap, paths: []string{"path"}, strs: []string{"path"}, kinds: rootsKinds, text: "path",
		valid: func(f *fixture) map[string]any { return map[string]any{"path": f.root} },
	},
	"DELETE /api/roots": {
		body: []string{"path"}, cap: rootsCap, paths: []string{"path"}, strs: []string{"path"}, kinds: rootsKinds, text: "path",
		// Not a stored root, so the valid request revokes nothing.
		valid: func(f *fixture) map[string]any { return map[string]any{"path": f.notARoot} },
	},
	"GET /api/pipelines/open": {
		query: []string{"root", "directory"}, paths: []string{"root", "directory"}, strs: []string{"root", "directory"}, kinds: pipelinesKinds,
		valid: func(f *fixture) map[string]any { return map[string]any{"root": f.root, "directory": f.directory} },
	},
	"POST /api/pipelines": {
		body: []string{"root", "name", "language", "sourceTopic", "destinationTopic", "networkMode"}, cap: createCap,
		paths: []string{"root", "name"}, strs: []string{"root", "name", "sourceTopic", "destinationTopic"}, kinds: pipelinesKinds, text: "name",
		valid: func(f *fixture) map[string]any {
			return map[string]any{"root": f.root, "name": "Attack target", "language": "go", "sourceTopic": "in.v1", "destinationTopic": "out.v1", "networkMode": "docker"}
		},
	},
	"GET /api/pipelines/source": {
		query: []string{"root", "directory", "path"}, paths: []string{"root", "directory", "path"}, strs: []string{"root", "directory", "path"}, kinds: sourceKinds,
		valid: func(f *fixture) map[string]any {
			return map[string]any{"root": f.root, "directory": f.directory, "path": f.sourcePath}
		},
	},
	"PUT /api/pipelines/source": {
		body: []string{"root", "directory", "path", "text", "expectedAbsent", "expectedSha256"}, cap: sourceCap,
		paths: []string{"root", "directory", "path"}, strs: []string{"root", "directory", "path", "text"}, kinds: sourceKinds, text: "path",
		valid: func(f *fixture) map[string]any {
			return map[string]any{"root": f.root, "directory": f.directory, "path": "src/attack.go", "text": "package main\n", "expectedAbsent": true}
		},
	},
	"POST /api/connections": {
		body: connectionFields, cap: connectionsCap, strs: []string{"name", "bootstrapServers", "saslUsername"}, kinds: connectionsKinds, text: "name",
		valid: func(*fixture) map[string]any {
			return map[string]any{"name": "attack", "bootstrapServers": []any{"localhost:9092"}, "securityProtocol": "SASL_PLAINTEXT",
				"saslMechanism": "PLAIN", "saslUsername": "user", "saslPassword": "attack-password"}
		},
	},
	"PUT /api/connections": {
		body: append([]string{"current", "remove", "confirm"}, connectionFields...), cap: connectionsCap,
		strs: []string{"current", "name", "bootstrapServers", "saslUsername"}, kinds: connectionsKinds, text: "current",
		valid: func(f *fixture) map[string]any {
			return map[string]any{"current": f.spare, "name": f.spare, "bootstrapServers": []any{"localhost:9092"}, "securityProtocol": "SASL_PLAINTEXT",
				"saslMechanism": "PLAIN", "saslUsername": "user", "saslPassword": "attack-password"}
		},
	},
	"DELETE /api/connections": {
		body: []string{"name", "confirm"}, cap: connectionsCap, strs: []string{"name"}, kinds: connectionsKinds, text: "name",
		// No such connection, so the valid request deletes nothing.
		valid: func(*fixture) map[string]any { return map[string]any{"name": "no-such-connection"} },
	},
	// The working-copy routes (#43). No route stages, so against the fixture
	// they review nothing and find no revision or snapshot to act on.
	"GET /api/workcopies": {valid: noInputs},
	"GET /api/workcopies/review": {
		query: []string{"root", "directory"}, paths: []string{"root", "directory"}, strs: []string{"root", "directory"}, kinds: workcopyKinds,
		valid: func(f *fixture) map[string]any { return map[string]any{"root": f.root, "directory": f.directory} },
	},
	"POST /api/workcopies/apply":      workcopyRow("revision", noRevision),
	"POST /api/workcopies/restore":    workcopyRow("snapshot", noSnapshot),
	"POST /api/workcopies/discard":    workcopyRow("revision", noRevision),
	"DELETE /api/workcopies/snapshot": workcopyRow("snapshot", noSnapshot),
}

var connectionFields = []string{"name", "bootstrapServers", "securityProtocol", "saslMechanism", "saslUsername", "saslPassword",
	"tlsCaCertificate", "tlsClientCertificate", "tlsClientKey"}

// attackRoute is a registered route with its row.
type attackRoute struct {
	registeredRoute
	row routeRow
}

// takesBody reports whether the route reads a JSON body.
func (r attackRoute) takesBody() bool { return r.row.cap > 0 }

// changesState is every route that is not a GET or HEAD.
func (r attackRoute) changesState() bool { return r.method != "GET" && r.method != "HEAD" }

// build turns input values into a request: the row's query inputs into the
// query, its body inputs into a JSON object. A value for an input the row
// does not list is an error in the suite.
func (r attackRoute) build(t *testing.T, values map[string]any) (target string, body []byte) {
	t.Helper()
	query := url.Values{}
	object := map[string]any{}
	for name, value := range values {
		switch {
		case contains(r.row.query, name):
			text, ok := value.(string)
			if !ok {
				t.Fatalf("%s: query input %s is not a string", r.pattern, name)
			}
			query.Set(name, text)
		case contains(r.row.body, name):
			object[name] = value
		default:
			t.Fatalf("%s: the table lists no input %s", r.pattern, name)
		}
	}
	target = r.path
	if len(query) != 0 {
		target += "?" + query.Encode()
	}
	if r.takesBody() {
		var err error
		body, err = json.Marshal(object)
		if err != nil {
			t.Fatalf("%s: marshal the body: %v", r.pattern, err)
		}
	}
	return target, body
}

// with is the route's valid values with one input set to value. A list input
// (bootstrapServers) gets value as its one element.
func (r attackRoute) with(f *fixture, name string, value string) map[string]any {
	values := r.row.valid(f)
	if _, isList := values[name].([]any); isList {
		values[name] = []any{value}
	} else {
		values[name] = value
	}
	return values
}

func contains(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}

// attackRoutes is every registered route with its row; a route without one,
// or a row for nothing registered, fails t naming it.
func attackRoutes(t *testing.T) []attackRoute {
	t.Helper()
	registered := discoverRoutes(t)
	var out []attackRoute
	seen := map[string]bool{}
	for _, route := range registered {
		seen[route.pattern] = true
		row, ok := routeTable[route.pattern]
		if !ok {
			t.Errorf("%s is registered by newHandler and has no row in routeTable (security_routes_test.go), so no attack reaches it; add one", route.pattern)
			continue
		}
		for _, name := range append(append([]string(nil), row.paths...), row.strs...) {
			if !contains(row.query, name) && !contains(row.body, name) {
				t.Errorf("%s: the row names %s as a path or string input that is neither a query nor a body input", route.pattern, name)
			}
		}
		if (row.cap > 0) != (len(row.body) > 0) {
			t.Errorf("%s: the row has body inputs without a cap, or a cap without body inputs", route.pattern)
		}
		if row.cap > 0 && !contains(row.strs, row.text) {
			t.Errorf("%s: the row takes a body and names no string input for the body corpus's encodings", route.pattern)
		}
		out = append(out, attackRoute{route, row})
	}
	for pattern := range routeTable {
		if !seen[pattern] {
			t.Errorf("routeTable has a row for %s, which newHandler no longer registers; remove it", pattern)
		}
	}
	return out
}

func TestSecurityTableHasARowForEveryRegisteredRoute(t *testing.T) {
	t.Parallel()
	routes := attackRoutes(t)
	// The 23 routes newHandler registered when the suite was written: #46's
	// 17 and #43's six. More is a new route, which needs only its row; fewer
	// is the discovery breaking, which this catches.
	if len(routes) < 23 {
		t.Errorf("discovered %d routes, fewer than the 23 registered when the suite was written: %v", len(routes), routes)
	}
}

// The discovery is what makes a new route attacked, so it is checked against
// planted packages: a constant, a literal and a table row each come back, a
// route added by a new Handle call is found without anything else changing,
// and a pattern it cannot resolve is a problem rather than a route quietly
// left out.
func TestSecurityRouteDiscoveryResolvesEveryRegistrationShape(t *testing.T) {
	t.Parallel()
	plant := func(t *testing.T, routes string) ([]string, []string) {
		t.Helper()
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "consts.go"), "package httpapi\nconst (\n\tfooPattern = \"POST /api/foo\"\n\thealthPattern = \"GET /api/health\"\n)\n", 0o644)
		mustWrite(t, filepath.Join(dir, "routes.go"), "package httpapi\n"+routes, 0o644)
		found, problems := discoverRoutesIn(dir)
		var patterns []string
		for _, route := range found {
			patterns = append(patterns, route.pattern)
		}
		return patterns, problems
	}
	const table = "func apiRoutes() []route { return []route{{healthPattern, h}, {\"GET /api/version\", h}} }\n"
	got, problems := plant(t, table+"func newHandler(g *guard, routes []route) http.Handler {\n\tmux := http.NewServeMux()\n\tfor _, r := range routes { mux.Handle(r.pattern, r.handler) }\n\tmux.Handle(fooPattern, x)\n\tmux.HandleFunc(\"DELETE /api/bar\", y)\n\treturn g.wrap(mux)\n}\n")
	if len(problems) != 0 || strings.Join(got, ",") != "DELETE /api/bar,GET /api/health,GET /api/version,POST /api/foo" {
		t.Errorf("discovery of a planted newHandler found %v with problems %v", got, problems)
	}
	_, problems = plant(t, table+"func newHandler(g *guard, routes []route) http.Handler {\n\tmux := http.NewServeMux()\n\tfor _, r := range routes { mux.Handle(r.pattern, r.handler) }\n\tmux.Handle(prefix+\"/x\", x)\n\treturn g.wrap(mux)\n}\n")
	if len(problems) == 0 {
		t.Error("a pattern built at run time was not reported, so a route registered that way would go unattacked")
	}
	_, problems = plant(t, table+"func newHandler(g *guard, routes []route) http.Handler {\n\tmux := http.NewServeMux()\n\tmux.Handle(\"GET /api/{name}\", x)\n\treturn g.wrap(mux)\n}\n")
	if len(problems) < 2 {
		t.Errorf("a wildcard pattern and a missing table loop were not both reported: %v", problems)
	}

	routes := discoverRoutes(t)
	var found []string
	for _, route := range routes {
		found = append(found, route.pattern)
	}
	for _, want := range []string{"GET /api/health", "GET /api/channel", "PUT /api/pipelines/source", "DELETE /api/connections"} {
		if !contains(found, want) {
			t.Errorf("discovery did not find %s in %v", want, found)
		}
	}
}
