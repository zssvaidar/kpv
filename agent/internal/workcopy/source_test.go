// Structural tests: which packages this one may use, which may use it, and
// that no HTTP route stages. They read the non-test source, because an
// import or a call is not observable from behaviour.

package workcopy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/zssvaidar/kpv/agent"

// files parses every non-test Go file in directory.
func files(t *testing.T, directory string) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var out []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, file)
	}
	if len(out) == 0 {
		t.Fatalf("no source files in %s", directory)
	}
	return out
}

func importsOf(t *testing.T, directory string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, file := range files(t, directory) {
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			out[path] = true
		}
	}
	return out
}

func TestThisPackageImportsOnlyCatalogAndProjectfsFromTheAgent(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{module + "/internal/catalog": true, module + "/internal/projectfs": true}
	found := importsOf(t, ".")
	for path := range found {
		if strings.HasPrefix(path, module) && !allowed[path] {
			t.Errorf("internal/workcopy imports %s", path)
		}
		if !strings.HasPrefix(path, module) && strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
			t.Errorf("internal/workcopy imports %s, which is not the standard library", path)
		}
		switch path {
		case "os", "path/filepath", "io/fs", "io/ioutil", "os/exec", "syscall":
			t.Errorf("internal/workcopy imports %s; its filesystem access is projectfs's and catalog's", path)
		}
	}
	for path := range allowed {
		if !found[path] {
			t.Errorf("internal/workcopy no longer imports %s, so this test is looking at the wrong thing", path)
		}
	}
}

func TestTheLowerPackagesDoNotImportThisPackage(t *testing.T) {
	t.Parallel()
	for _, lower := range []string{"projectfs", "pipeline", "catalog", "codegen", "secrets", "channel", "connections", "process", "parser"} {
		if importsOf(t, filepath.Join("..", lower))[module+"/internal/workcopy"] {
			t.Errorf("internal/%s imports internal/workcopy", lower)
		}
	}
}

// Staging is an in-process method that #77 and #97 call; no HTTP route
// stages. internal/httpapi may hold a *workcopy.Store to list, review,
// apply, restore and discard, but no selector in it names Stage.
func TestHttpapiCallsNoStagingMethod(t *testing.T) {
	t.Parallel()
	httpapi := filepath.Join("..", "httpapi")
	if !importsOf(t, httpapi)[module+"/internal/workcopy"] {
		t.Fatal("internal/httpapi does not import internal/workcopy, so this test is looking at the wrong thing")
	}
	for _, file := range files(t, httpapi) {
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.SelectorExpr:
				if node.Sel.Name == "Stage" {
					t.Errorf("internal/httpapi names Stage, the staging method: no route stages")
				}
			case *ast.Ident:
				if node.Name == "Stage" {
					t.Errorf("internal/httpapi names Stage: no route stages")
				}
			}
			return true
		})
	}
}
