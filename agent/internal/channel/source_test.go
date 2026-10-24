// Structural tests: two claims about this package that no behaviour can show.
//
// The first is the one #5 states as a constraint and which is easy to satisfy
// today and lose tomorrow: credentials never appear in an envelope payload.
// The strongest form of that is not an assertion about a message, it is that
// this package has no credential to put in one. So: it is not given the token,
// it cannot reach the type that holds it, and nothing in it is named after
// one.
//
// The second is #4's rule, extended to the package it now reaches: nothing
// here reads the filesystem, because the path authorization that would make
// that safe is #6.

package channel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// parsePackage parses this package's non-test files. The test files are left
// out on purpose: a test may import anything, including the os this one needs
// to find them, and what matters is what the shipped code does.
func parsePackage(t *testing.T) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read this package's directory: %v", err)
	}
	files := make(map[string]*ast.File, len(entries))
	fileSet := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(fileSet, name, nil, parser.ParseComments)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		files[name] = parsed
	}
	if len(files) < 3 {
		t.Fatalf("only %d source files parsed, so this test is looking at the wrong thing", len(files))
	}
	return files
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

// This package cannot reach a credential, which is why no payload it builds
// can carry one.
//
// It is not handed the token: the HTTP boundary checks it and hands over a
// request that has already passed. It cannot reach the type that holds one
// either, because importing httpapi would be a cycle as well as a mistake.
// And nothing in it is named after a credential, which is what would be true
// if someone started passing one in.
func TestThisPackageCannotReachACredential(t *testing.T) {
	t.Parallel()
	files := parsePackage(t)

	for name, file := range files {
		for _, path := range importsOf(t, file) {
			if path == "github.com/zssvaidar/kpv/agent/internal/httpapi" {
				t.Errorf("%s imports httpapi, which is where the session token lives; the checks are the boundary's and the token never comes in here", name)
			}
		}
	}

	// Identifiers and string literals, not comments: the package doc talks
	// about the token on purpose, to say that it never arrives here.
	credentials := []string{"token", "secret", "password", "credential", "authorization", "bearer"}
	for name, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			var written string
			switch typed := node.(type) {
			case *ast.Ident:
				written = typed.Name
			case *ast.BasicLit:
				if typed.Kind != token.STRING {
					return true
				}
				written = typed.Value
			default:
				return true
			}
			lowered := strings.ToLower(written)
			for _, credential := range credentials {
				if strings.Contains(lowered, credential) {
					t.Errorf("%s has %q in its code; nothing in this package should know about a credential, because none is ever passed in", name, written)
				}
			}
			return true
		})
	}
}

// Nothing here reads the filesystem. This is #4's rule for the HTTP boundary,
// which now reaches this package, so the rule reaches it too: a route that can
// read a path needs the path authorization that is #6.
//
// embed is the one exception, and it is a different thing: it is resolved by
// the compiler, the one file it names is the schema this package's own types
// are generated from, and there is no path from a request to it. Reading a
// file at runtime is what the rest of the list is about.
func TestNothingInThisPackageCanReachTheFilesystem(t *testing.T) {
	t.Parallel()
	banned := []string{
		"os",
		"io/fs",
		"io/ioutil",
		"path/filepath",
		"path",
		"os/exec",
		"github.com/zssvaidar/kpv/agent/internal/pipeline",
	}
	embedded := false
	for name, file := range parsePackage(t) {
		for _, path := range importsOf(t, file) {
			if path == "embed" {
				embedded = true
			}
			for _, forbidden := range banned {
				if path == forbidden {
					t.Errorf("%s imports %s, so the channel could reach the filesystem; that needs the path guard in #6 first", name, path)
				}
			}
		}
	}
	if !embedded {
		t.Error("no file in this package embeds the schema any more, so the copy beside it is unused and the staleness check is guarding nothing")
	}
}
