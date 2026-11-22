// Structural test: the harness takes bytes and never a path. No behaviour
// shows that, so this package's imports do.

package parser

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestTheHarnessTakesBytesAndNeverAPath(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read this package's directory: %v", err)
	}
	set := token.NewFileSet()
	parsed := 0
	importsProcess := false
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(set, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		parsed++
		for _, spec := range file.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			switch {
			case path == "os", path == "path/filepath", path == "io/fs", path == "io/ioutil":
				t.Errorf("%s imports %s; the harness is handed bytes the caller read through the path guard, never a path", name, path)
			case path == "github.com/zssvaidar/kpv/agent/internal/process":
				importsProcess = true
			case strings.HasPrefix(path, "github.com/zssvaidar/kpv/"):
				t.Errorf("%s imports %s; internal/parser imports no agent package but internal/process", name, path)
			}
		}
	}
	if parsed < 3 || !importsProcess {
		t.Fatalf("parsed %d files and saw internal/process imported: %v; this test is looking at the wrong thing", parsed, importsProcess)
	}
}
