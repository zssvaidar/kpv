// Structural tests: facts about this package's own source that a behavioural
// test cannot reach.
//
// Three of this task's constraints are about how the code is written, not only
// about what it answers. "The comparison is constant-time" would pass for ==
// if the only test were that a wrong token is rejected. "The token comes from a
// cryptographic source" would pass for math/rand. "No route reads the
// filesystem" is not observable from a response at all. So those three are
// asserted against the package's syntax tree, which is the only place they are
// visible.

package httpapi

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// parsePackage parses this package's non-test files, keyed by file name. The
// test files are left out on purpose: a test may import anything, including
// the os this file needs to find them, and what matters is what the shipped
// code does.
func parsePackage(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read this package's directory: %v", err)
	}
	fileSet := token.NewFileSet()
	files := make(map[string]*ast.File, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(fileSet, name, nil, parser.ParseComments)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		if parsed.Name.Name != "httpapi" {
			t.Fatalf("%s is in package %s, not httpapi", name, parsed.Name.Name)
		}
		files[name] = parsed
	}
	if len(files) < 2 {
		t.Fatalf("only %d source files parsed, so this test is looking at the wrong thing", len(files))
	}
	return fileSet, files
}

func importsOf(t *testing.T, file *ast.File) []string {
	t.Helper()
	out := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("unquote the import %s: %v", spec.Path.Value, err)
		}
		out = append(out, path)
	}
	return out
}

// source renders a piece of syntax back to Go, so an assertion can be made
// about what an expression is written as rather than about where it is.
func source(t *testing.T, fileSet *token.FileSet, node ast.Node) string {
	t.Helper()
	var out strings.Builder
	if err := printer.Fprint(&out, fileSet, node); err != nil {
		t.Fatalf("print a node: %v", err)
	}
	return out.String()
}

// The token has to come from a cryptographic source. math/rand would pass
// every behavioural test in this package: the tokens would all differ, they
// would be the right length, and a wrong one would be rejected. The only way
// to tell is to look at what is imported.
func TestTheTokenSourceIsCryptographic(t *testing.T) {
	t.Parallel()
	_, files := parsePackage(t)

	banned := map[string]string{
		"math/rand":    "a predictable random source",
		"math/rand/v2": "a predictable random source",
	}
	cryptographic := false
	for name, file := range files {
		for _, path := range importsOf(t, file) {
			if why, forbidden := banned[path]; forbidden {
				t.Errorf("%s imports %s, which is %s", name, path, why)
			}
			if path == "crypto/rand" {
				cryptographic = true
			}
		}
	}
	if !cryptographic {
		t.Error("no file in this package imports crypto/rand, so the token comes from somewhere else")
	}
}

// No route may reach the filesystem except through internal/projectfs, which
// is where every path is checked (#6). A response cannot show this, so the
// imports do: nothing here imports a package that opens a file, and the
// directory browsing endpoint (#7) reads through a projectfs.Browser only.
func TestNothingInThisPackageCanReachTheFilesystem(t *testing.T) {
	t.Parallel()
	_, files := parsePackage(t)

	banned := []string{"os", "io/fs", "io/ioutil", "path/filepath", "embed", "os/exec", "github.com/zssvaidar/kpv/agent/internal/pipeline"}
	for name, file := range files {
		for _, path := range importsOf(t, file) {
			for _, forbidden := range banned {
				if path == forbidden {
					t.Errorf("%s imports %s, so a route here could reach the filesystem without going through the path guard in internal/projectfs", name, path)
				}
			}
		}
	}
}

// The home directory is browsable without being authorized (#7), and the way
// that stays a browse allowance rather than becoming a write path is that this
// package is never handed anything that writes. It holds a projectfs.Browser,
// which only lists, and it never names the Guard or a Path, which are the
// types that read and write files. This lists every projectfs name the
// package's source uses and fails on any outside the read-only set, so a
// route that grows a write, or a handle to one, has to change this list in
// plain sight first.
func TestThisPackageUsesOnlyTheReadOnlyPartOfProjectfs(t *testing.T) {
	t.Parallel()
	fileSet, files := parsePackage(t)

	readOnly := map[string]bool{
		"projectfs.Browser": true, "projectfs.Listing": true, "projectfs.MaxListing": true,
		"projectfs.KindOf": true, "projectfs.KindForbidden": true, "projectfs.KindNotFound": true,
		"projectfs.KindUnreadable": true, "projectfs.KindNotDirectory": true, "projectfs.KindInvalid": true,
	}
	used := map[string]bool{}
	for name, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := selector.X.(*ast.Ident); !ok || pkg.Name != "projectfs" {
				return true
			}
			written := source(t, fileSet, selector)
			used[written] = true
			if !readOnly[written] {
				t.Errorf("%s uses %s, which is not part of the read-only browsing surface; this package must not be able to write a file", name, written)
			}
			return true
		})
	}
	// Non-vacuity: the browsing endpoint does use projectfs, so finding no
	// use at all means this is looking at the wrong thing.
	if !used["projectfs.Browser"] || !used["projectfs.KindOf"] {
		t.Fatalf("found only %v from projectfs; this test is not seeing the browsing endpoint", used)
	}
}

// The token comparison has to be the constant-time one. This looks at how
// `matches` is written, because a == would satisfy every other test here.
func TestTheTokenComparisonIsTheConstantTimeOne(t *testing.T) {
	t.Parallel()
	fileSet, files := parsePackage(t)

	var matches *ast.FuncDecl
	for _, file := range files {
		for _, decl := range file.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if ok && function.Name.Name == "matches" && function.Recv != nil {
				matches = function
			}
		}
	}
	if matches == nil {
		t.Fatal("Token.matches no longer exists under that name, so this test is not checking the comparison any more")
	}

	constantTime := false
	ast.Inspect(matches.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if source(t, fileSet, call.Fun) == "subtle.ConstantTimeCompare" {
			constantTime = true
		}
		return true
	})
	if !constantTime {
		t.Error("Token.matches does not call subtle.ConstantTimeCompare")
	}

	// Nothing derived from what the client presented may be compared with an
	// operator: that is the leak the constant-time compare exists to close,
	// and a length check in front of it leaks the length just as surely.
	ast.Inspect(matches.Body, func(node ast.Node) bool {
		comparison, ok := node.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		switch comparison.Op {
		case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
		default:
			return true
		}
		written := source(t, fileSet, comparison)
		if strings.Contains(written, "subtle.ConstantTimeCompare") {
			// Reading the constant-time compare's own result is the point of
			// it, not a leak: the comparison already happened in constant
			// time and this is the 1 it reports.
			return true
		}
		for _, derived := range []string{"presented", "digest", "len("} {
			if strings.Contains(written, derived) {
				t.Errorf("Token.matches compares %q, which leaks something about the presented value", written)
			}
		}
		return true
	})

	// And no sloppy equality anywhere near the token, in any file.
	for name, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch source(t, fileSet, call.Fun) {
			case "bytes.Equal", "strings.Compare", "subtle.ConstantTimeEq":
				t.Errorf("%s calls %s; the token comparison is subtle.ConstantTimeCompare", name, source(t, fileSet, call.Fun))
			}
			return true
		})
	}
}

// The checks cannot be forgotten because there is one mux, it is built in one
// function, and that function wraps it in the guard before returning it. This
// asserts the shape that makes that true: nothing else creates a mux, and
// nothing else registers a pattern on one.
func TestOnlyNewHandlerBuildsAMuxAndItWrapsIt(t *testing.T) {
	t.Parallel()
	fileSet, files := parsePackage(t)

	type site struct {
		file, enclosing, written string
	}
	var muxes, registrations []site
	var newHandler *ast.FuncDecl

	for name, file := range files {
		functions := map[*ast.FuncDecl]bool{}
		for _, decl := range file.Decls {
			if function, ok := decl.(*ast.FuncDecl); ok {
				functions[function] = true
				if function.Name.Name == "newHandler" {
					newHandler = function
				}
			}
		}
		enclosing := func(pos token.Pos) string {
			for function := range functions {
				if pos >= function.Pos() && pos <= function.End() {
					return function.Name.Name
				}
			}
			return "no function"
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			written := source(t, fileSet, call.Fun)
			switch {
			case written == "http.NewServeMux":
				muxes = append(muxes, site{name, enclosing(call.Pos()), written})
			case strings.HasSuffix(written, ".Handle"), strings.HasSuffix(written, ".HandleFunc"):
				registrations = append(registrations, site{name, enclosing(call.Pos()), written})
			}
			return true
		})
	}

	if len(muxes) != 1 {
		t.Fatalf("http.NewServeMux is called %d times, want exactly once: %+v", len(muxes), muxes)
	}
	if muxes[0].enclosing != "newHandler" {
		t.Errorf("the mux is created in %s, not in newHandler", muxes[0].enclosing)
	}
	for _, registration := range registrations {
		if registration.enclosing != "newHandler" {
			t.Errorf("%s registers a route with %s in %s; routes belong in the table newHandler builds the mux from, or they can miss the checks", registration.file, registration.written, registration.enclosing)
		}
	}

	if newHandler == nil {
		t.Fatal("newHandler no longer exists, so nothing here is checking how the handler is built")
	}
	// Whatever newHandler returns has to be the wrapped handler, never the mux
	// itself.
	returned := 0
	ast.Inspect(newHandler.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		returned++
		for _, result := range statement.Results {
			written := source(t, fileSet, result)
			if !strings.Contains(written, ".wrap(") {
				t.Errorf("newHandler returns %q, which is not the guard-wrapped handler", written)
			}
		}
		return true
	})
	if returned != 1 {
		t.Errorf("newHandler has %d return statements, want one, so there is one thing it can hand back", returned)
	}
}

// What the server serves. The test above makes it impossible to register a
// route outside the guard; this one makes it impossible to serve a handler
// that is not the guarded one, which is the other half of "the checks cannot
// be forgotten". Without it, replacing the Handler field with a hand-rolled
// router that never calls .Handle would serve an unguarded path with the whole
// suite still green.
func TestTheServedHandlerIsExactlyTheGuardedOne(t *testing.T) {
	t.Parallel()
	fileSet, files := parsePackage(t)

	// The one expression allowed to be the served handler. It is compared as
	// written, so anything wrapping it, replacing it, or adding to it fails.
	const guardedHandler = "newHandler(g, apiRoutes())"

	var servers, assignments []string
	for name, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.CompositeLit:
				if typed.Type == nil {
					// An element of a slice or map literal, which spells its
					// type once on the outside: nothing to print here.
					return true
				}
				if source(t, fileSet, typed.Type) != "http.Server" {
					return true
				}
				servers = append(servers, name)
				for _, element := range typed.Elts {
					field, ok := element.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if key, ok := field.Key.(*ast.Ident); !ok || key.Name != "Handler" {
						continue
					}
					written := source(t, fileSet, field.Value)
					assignments = append(assignments, written)
					if written != guardedHandler {
						t.Errorf("%s serves %q as the handler, want exactly %q; anything else can bypass the guard", name, written, guardedHandler)
					}
				}
			case *ast.AssignStmt:
				// An assignment to a Handler field after the fact would
				// replace the guarded handler just as effectively.
				for _, target := range typed.Lhs {
					if written := source(t, fileSet, target); strings.HasSuffix(written, ".Handler") {
						t.Errorf("%s assigns to %s after construction; the served handler is set once, in New", name, written)
					}
				}
			}
			return true
		})
	}

	if len(servers) != 1 {
		t.Fatalf("there are %d http.Server literals in this package, want exactly one: %v", len(servers), servers)
	}
	if len(assignments) != 1 {
		t.Fatalf("the http.Server literal sets Handler %d times, want once: %v", len(assignments), assignments)
	}
	// A nil Handler is the worst case of all: net/http falls back to
	// http.DefaultServeMux, which anything in the process can register on.
	if assignments[0] == "nil" {
		t.Fatal("the server serves a nil handler, which means http.DefaultServeMux")
	}
}
