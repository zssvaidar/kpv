// Structural tests: which packages this one may use, which may use it, and
// where it reveals a credential. They read the non-test source, because
// neither is observable from behaviour.

package connections

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

func sourceFiles(t *testing.T, directory string) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = file
	}
	if len(out) == 0 {
		t.Fatalf("no source files in %s", directory)
	}
	return out
}

func imports(t *testing.T, directory string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, file := range sourceFiles(t, directory) {
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

func TestThisPackageImportsOnlyProjectfsSecretsAndCatalogFromTheAgent(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		module + "/internal/projectfs": true, module + "/internal/secrets": true, module + "/internal/catalog": true,
	}
	// The schema validator and its message printer, as internal/pipeline uses.
	external := []string{"github.com/santhosh-tekuri/jsonschema/v6", "golang.org/x/text/"}
	found := imports(t, ".")
	for path := range found {
		if strings.HasPrefix(path, module) {
			if !allowed[path] {
				t.Errorf("internal/connections imports %s", path)
			}
			continue
		}
		if strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
			ok := false
			for _, prefix := range external {
				ok = ok || strings.HasPrefix(path, prefix)
			}
			if !ok {
				t.Errorf("internal/connections imports %s", path)
			}
		}
		if path == "os" || path == "path/filepath" || path == "os/exec" || path == "syscall" || path == "io/fs" {
			t.Errorf("internal/connections imports %s; its file is projectfs's", path)
		}
	}
	for path := range allowed {
		if !found[path] {
			t.Errorf("internal/connections no longer imports %s, so this test is looking at the wrong thing", path)
		}
	}
}

func TestTheLowerPackagesDoNotImportThisPackage(t *testing.T) {
	t.Parallel()
	for _, directory := range []string{"../projectfs", "../pipeline", "../catalog", "../codegen", "../secrets", "../channel"} {
		if imports(t, directory)[module+"/internal/connections"] {
			t.Errorf("%s imports internal/connections", directory)
		}
	}
}

// The store file write is the one place here that calls Reveal.
func TestOnlyTheStoreFileWriteRevealsACredential(t *testing.T) {
	t.Parallel()
	callers := map[string]int{}
	for _, file := range sourceFiles(t, ".") {
		for _, decl := range file.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "Reveal" {
					callers[function.Name.Name]++
				}
				return true
			})
		}
	}
	if len(callers) != 1 || callers["encodeStore"] == 0 {
		t.Fatalf("Reveal is called from %v, want encodeStore only", callers)
	}
}
