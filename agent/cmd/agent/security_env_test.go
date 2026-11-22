//go:build unix

// .env and .gitignore (#23): the source routes refuse them, the pipeline's
// .env canary is checked for everywhere at the end (nothingLeaks), and a
// create into a root holding both leaves them as they were.

package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func urlQueryEscape(value string) string { return url.QueryEscape(value) }

// dotNames are the names the source routes refuse as invalid.
var dotNames = []string{".env", ".ENV", "sub/.env", ".env.local", ".gitignore", "..env.tmp-x", ".git/config"}

func (f *fixture) envAndGitignore(t *testing.T) {
	for _, name := range dotNames {
		before := snapshot(t, f.pipelineDir)
		read := f.call(t, "GET", "/api/pipelines/source?"+queryOf("root", f.root, "directory", f.directory, "path", name), nil)
		absent := f.call(t, "PUT", "/api/pipelines/source", map[string]any{"root": f.root, "directory": f.directory, "path": name,
			"text": "KPV_WRITTEN=1\n", "expectedAbsent": true})
		hashed := f.call(t, "PUT", "/api/pipelines/source", map[string]any{"root": f.root, "directory": f.directory, "path": name,
			"text": "KPV_WRITTEN=1\n", "expectedSha256": strings.Repeat("a", 64)})
		for _, got := range []answer{read, absent, hashed} {
			if got.status != http.StatusBadRequest || errorKind(got) != "invalid" {
				t.Errorf("%s %s answered %d %s, want invalid", got.method, name, got.status, got.body)
			}
			if strings.Contains(string(got.body), f.envCanary) {
				t.Errorf("%s %s answered the .env", got.method, name)
			}
		}
		unchanged(t, "the source routes on "+name, before, f.pipelineDir)
	}
	got := f.call(t, "GET", "/api/pipelines/source?"+queryOf("root", f.root, "directory", f.directory, "path", "env"), nil)
	if errorKind(got) != "link" {
		t.Errorf("reading a link named env to .env answered %d %s, want link", got.status, got.body)
	}
	got = f.call(t, "PUT", "/api/pipelines/source", map[string]any{"root": f.root, "directory": f.directory, "path": "env",
		"text": "KPV_WRITTEN=1\n", "expectedAbsent": true})
	if errorKind(got) != "link" {
		t.Errorf("writing a link named env to .env answered %d %s, want link", got.status, got.body)
	}

	t.Run("a create into a root holding .env and .gitignore", f.createBesideEnv)
}

func (f *fixture) createBesideEnv(t *testing.T) {
	root := filepath.Join(f.s.home, "projects", "gamma")
	env := "KPV_USER=" + f.envCanary + "\n"
	gitignore := "node_modules/\n# the user's own\n.env\n\n*.log\n"
	mustWrite(t, filepath.Join(root, ".env"), env, 0o640)
	mustWrite(t, filepath.Join(root, ".gitignore"), gitignore, 0o604)
	envBefore, err := os.Lstat(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	f.mustCall(t, "POST", "/api/roots", map[string]any{"path": root})
	defer f.call(t, "DELETE", "/api/roots", map[string]any{"path": root})
	created := f.mustCall(t, "POST", "/api/pipelines", map[string]any{"root": root, "name": "Beside env", "language": "python",
		"sourceTopic": "in", "destinationTopic": "out", "networkMode": "docker"})
	if strings.Contains(string(created.body), f.envCanary) {
		t.Error("the create answered the root's .env")
	}
	body, err := os.ReadFile(filepath.Join(root, ".env"))
	info, statErr := os.Lstat(filepath.Join(root, ".env"))
	if err != nil || statErr != nil || string(body) != env || info.Mode() != envBefore.Mode() {
		t.Errorf("the root's .env is not as it was: %q, %v", body, info.Mode())
	}
	var made []string
	for path := range snapshot(t, root) {
		if filepath.Base(path) == ".env" && path != filepath.Join(root, ".env") {
			made = append(made, path)
		}
	}
	if len(made) != 0 {
		t.Errorf("the create made a .env: %v", made)
	}
	after, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if outsideMarkerBlock(string(after)) != outsideMarkerBlock(gitignore) {
		t.Errorf("the .gitignore lines outside #23's block changed:\n%q\nwas\n%q", after, gitignore)
	}
}

// outsideMarkerBlock drops #23's managed block, markers included, and
// keeps every other line byte for byte.
func outsideMarkerBlock(text string) string {
	var kept []string
	inside := false
	for _, line := range strings.SplitAfter(text, "\n") {
		switch {
		case strings.HasPrefix(line, "# >>> kpv managed block"):
			inside = true
		case strings.HasPrefix(line, "# <<< kpv managed block"):
			inside = false
		case !inside:
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "")
}
