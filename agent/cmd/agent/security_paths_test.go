//go:build unix

// Paths and links (#6, #7, #63, #48, #87): every path input of every route
// gets one corpus, with links, a cycle and a FIFO planted inside an
// authorized root; probing outside the roots says nothing; and the agent's
// own files cannot be reached by any sequence of requests.

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// plant puts the links, the cycle and the FIFO inside the first root and
// its pipeline, and the settings directory's own subdirectory.
func (f *fixture) plant(t *testing.T) {
	s := f.s
	mustLink(t, s.outside, filepath.Join(f.root, "out-link"))
	mustLink(t, s.settings, filepath.Join(f.root, "settings-link"))
	mustLink(t, f.other, filepath.Join(f.root, "beta-link"))
	rel, err := filepath.Rel(f.root, s.outside)
	if err != nil {
		t.Fatal(err)
	}
	mustLink(t, rel, filepath.Join(f.root, "climb"))
	mustLink(t, "cycle-b", filepath.Join(f.root, "cycle-a"))
	mustLink(t, "cycle-a", filepath.Join(f.root, "cycle-b"))
	mustLink(t, filepath.Join(f.root, "nothing-here"), filepath.Join(f.root, "dangling"))
	// A link in place of a pipeline directory, of a directory on a source
	// path, and of a source file.
	mustLink(t, filepath.Join(s.outside, "pipe"), filepath.Join(f.root, "linked-pipeline"))
	mustLink(t, filepath.Join(s.outside, "dir"), filepath.Join(f.pipelineDir, "linked-dir"))
	mustLink(t, filepath.Join(s.outside, "secret.txt"), filepath.Join(f.pipelineDir, "linked-file.go"))
	mustLink(t, "../../../../outside/secret.txt", filepath.Join(f.pipelineDir, "climb.go"))
	if err := syscall.Mkfifo(filepath.Join(f.pipelineDir, "fifo.go"), 0o644); err != nil {
		t.Fatalf("make the FIFO: %v", err)
	}
	// The pipeline's .env, holding a canary, and a link named env to it.
	mustWrite(t, filepath.Join(f.pipelineDir, ".env"), "KPV_CANARY="+f.envCanary+"\n", 0o600)
	mustLink(t, ".env", filepath.Join(f.pipelineDir, "env"))
	// A directory inside the agent's settings directory, and links to the
	// settings directory, into it, and to the directory above it, from home.
	mustMkdir(t, filepath.Join(s.settings, "sub"))
	// The working-copy tree (#43) is made on its first write, which no route
	// makes; it is made here so it can be named.
	mustMkdir(t, filepath.Join(s.settings, "workcopies"))
	mustLink(t, s.settings, filepath.Join(s.home, "links", "settings"))
	mustLink(t, filepath.Join(s.settings, "sub"), filepath.Join(s.home, "links", "settings-sub"))
	mustLink(t, s.config, filepath.Join(s.home, "links", "config"))
	// The targets outside the roots the probing check compares.
	for _, dir := range []string{s.outside, f.elsewhere} {
		mustMkdir(t, filepath.Join(dir, "probe-dir"))
		mustWrite(t, filepath.Join(dir, "probe-file"), f.outsideCanary, 0o644)
		mustMkdir(t, filepath.Join(dir, "probe-unreadable"))
		if err := os.Chmod(filepath.Join(dir, "probe-unreadable"), 0); err != nil {
			t.Fatal(err)
		}
		mustLink(t, "probe-cycle-b", filepath.Join(dir, "probe-cycle-a"))
		mustLink(t, "probe-cycle-a", filepath.Join(dir, "probe-cycle-b"))
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(s.outside, "probe-unreadable"), 0o755)
		_ = os.Chmod(filepath.Join(f.elsewhere, "probe-unreadable"), 0o755)
	})
}

// restoreRoots authorizes the two roots again if a request revoked one, so
// every corpus runs against the same roots.
func (f *fixture) restoreRoots(t *testing.T) {
	t.Helper()
	listed := string(f.mustCall(t, "GET", "/api/roots", nil).body)
	for _, root := range []string{f.root, f.other} {
		quoted, _ := json.Marshal(root)
		if !strings.Contains(listed, `"path":`+string(quoted)) {
			f.mustCall(t, "POST", "/api/roots", map[string]any{"path": root})
		}
	}
}

// pathCorpus is the one corpus every path input gets. Each value is sent
// as written; an input that takes an absolute path and one that takes a
// name or a relative path both get all of them.
func (f *fixture) pathCorpus() []string {
	s := f.s
	long := strings.Repeat("a", 4097)
	values := []string{
		// .. out of the root and out of home, and absolute paths outside home.
		"..", "../..", "../../..", "../../../../outside/secret.txt", "../" + filepath.Base(f.other),
		f.root + "/..", f.root + "/../..", f.root + "/../../..", f.root + "/../../../outside/secret.txt",
		f.pipelineDir + "/../../beta", s.home + "/..", s.home + "/../outside/secret.txt",
		"/etc/passwd", "/", "/etc", s.outside, s.outside + "/secret.txt",
		// Home itself, and ~.
		s.home, s.home + "/", "~", "~/", "~/projects/alpha",
		// Percent-encoded, which a query decodes once and nothing may decode
		// again.
		"..%2f..%2f..%2foutside", "%2e%2e/%2e%2e", "%2e%2e%2f%2e%2e", f.root + "/..%2f..%2f..%2foutside", f.root + "/%2e%2e/%2e%2e",
		// A backslash, a NUL, and a path over 4096 bytes.
		`..\..\outside`, f.root + `\..\..`, "src\\main.go", "a\x00b", f.root + "\x00/../..", "src/main.go\x00.txt",
		f.root + "/" + long, long, strings.Repeat("a/", 2100),
		// A trailing / and /.
		f.root + "/", f.root + "/.", f.directory + "/", f.directory + "/.", f.sourcePath + "/", f.sourcePath + "/.", f.pipelineDir + "/",
		// generated and pipeline.yaml in mixed case.
		"GeNeRaTeD", "GENERATED/x.go", "Generated/../src/main.go", "PiPeLiNe.YaMl", "PIPELINE.YAML", f.pipelineDir + "/Pipeline.Yaml",
		// Lookalikes of . and /.
		"．．", "．．/．．/outside", "․․", "․․/․․", "..∕..∕outside",
		"..⁄..", "..／..／outside", "．．／．．", f.root + "/．．/．．", "٠٠",
		// The agent's own files and directories.
		s.settings, s.settings + "/", filepath.Join(s.settings, "roots.json"), filepath.Join(s.settings, "connections.json"),
		s.config, filepath.Join(s.settings, "sub"), filepath.Join(s.settings, "workcopies"), "../../.config/kpv", "../../../.config/kpv/workcopies", "../../../.config/kpv/roots.json",
		"../../../../.config/kpv/connections.json", ".config/kpv/roots.json",
		// The planted links, cycle and FIFO, by name and by absolute path.
		"out-link", "out-link/secret.txt", "settings-link", "settings-link/roots.json", "beta-link", "beta-link/beta.txt",
		"climb", "climb/secret.txt", "cycle-a", "cycle-a/x", "dangling", "linked-pipeline", "linked-dir", "linked-dir/a.go",
		"linked-file.go", "climb.go", "fifo.go", "env",
		f.root + "/out-link", f.root + "/out-link/secret.txt", f.root + "/settings-link", f.root + "/beta-link", f.root + "/climb",
		f.root + "/cycle-a", f.root + "/dangling", f.root + "/linked-pipeline", f.pipelineDir + "/linked-dir",
		f.pipelineDir + "/linked-file.go", f.pipelineDir + "/fifo.go", s.home + "/links/settings", s.home + "/links/config",
	}
	return values
}

// outsideTheRoot is everything the path corpus may not create, change or
// truncate: the scratch directory but the first root, settings included.
func (f *fixture) outsideTheRoot(t *testing.T) map[string]entry {
	return snapshot(t, f.s.base, f.root, f.s.markers)
}

// pathsAndLinks is the path corpus over every path input.
func (f *fixture) pathsAndLinks(t *testing.T) {
	roots, err := os.ReadFile(filepath.Join(f.s.settings, "roots.json"))
	if err != nil {
		t.Fatal(err)
	}
	attacked := 0
	for _, route := range f.routes {
		for _, input := range route.row.paths {
			for _, value := range f.pathCorpus() {
				before := f.outsideTheRoot(t)
				target, body := route.build(t, route.with(f, input, value))
				header := f.a.authorized()
				if body != nil {
					header.Set("Content-Type", "application/json")
				}
				got := f.a.send(request{method: route.method, target: target, body: body, header: header})
				what := route.pattern + " with " + input + "=" + shortPath(value)
				f.checkPathAnswer(t, what, route, got, value, roots)
				skip := []string{f.root, f.s.markers}
				if route.path == "/api/roots" && got.status == http.StatusOK {
					// The route did what it is for, to a root: authorizing or
					// revoking one of the two is a change to roots.json, and
					// restoreRoots puts it back.
					f.restoreRoots(t)
					skip = append(skip, filepath.Join(f.s.settings, "roots.json"))
					delete(before, filepath.Join(f.s.settings, "roots.json"))
				}
				if changed := diffSnapshots(before, snapshot(t, f.s.base, skip...)); len(changed) != 0 {
					t.Errorf("%s changed the disk outside the root: %v", what, changed)
				}
				attacked++
			}
		}
	}
	if attacked == 0 {
		t.Fatal("no path input was attacked")
	}
	if runtime.GOOS == "linux" {
		t.Run("a directory swapped for a link between two requests", f.swap)
	}
	t.Run("probing outside the roots", f.probing)
	t.Run("the agent's own files", f.agentFiles)
}

// checkPathAnswer holds every answer to the path corpus: no byte of a file
// outside the root, and one of the route's documented kinds.
func (f *fixture) checkPathAnswer(t *testing.T, what string, route attackRoute, got answer, value string, roots []byte) {
	t.Helper()
	text := string(got.body)
	if strings.Contains(text, f.outsideCanary) {
		t.Errorf("%s answered the bytes of a file outside the root: %s", what, shortPath(text))
	}
	if strings.Contains(text, f.envCanary) {
		t.Errorf("%s answered the pipeline's .env", what)
	}
	if len(roots) > 0 && strings.Contains(text, strings.TrimSpace(string(roots))) {
		t.Errorf("%s answered roots.json", what)
	}
	if got.status == http.StatusOK {
		return
	}
	// A refusal says its kind and its fixed sentence, never the input it
	// refused (rejectionsSayNothing holds the sentence fixed).
	if len(value) >= 4 {
		if strings.Contains(withoutListedRoots(got.body, true), value) || strings.Contains(withoutListedRoots(got.body, true), strings.Trim(quote(value), `"`)) {
			t.Errorf("%s: the refusal carries the path it was sent: %s", what, shortPath(text))
		}
	}
	if kind := errorKind(got); !contains(route.row.kinds, kind) {
		t.Errorf("%s answered %d %s, which is not one of the route's kinds %v", what, got.status, shortPath(text), route.row.kinds)
	}
}

// swap reads a file through a directory, swaps the directory for a link to
// one outside the root, and reads again.
func (f *fixture) swap(t *testing.T) {
	dir := filepath.Join(f.pipelineDir, "swapped")
	mustWrite(t, filepath.Join(dir, "a.go"), "package swapped\n", 0o644)
	read := func() answer {
		return f.call(t, "GET", "/api/pipelines/source?"+queryOf("root", f.root, "directory", f.directory, "path", "swapped/a.go"), nil)
	}
	list := func() answer { return f.call(t, "GET", "/api/directories?"+queryOf("path", dir), nil) }
	if got := read(); got.status != http.StatusOK {
		t.Fatalf("the read before the swap answered %d %s", got.status, got.body)
	}
	list()
	before := f.outsideTheRoot(t)
	if err := os.Rename(dir, dir+".moved"); err != nil {
		t.Fatal(err)
	}
	mustLink(t, filepath.Join(f.s.outside, "dir"), dir)
	route := f.routeFor(t, "GET /api/pipelines/source")
	f.checkPathAnswer(t, "a read after the swap", route, read(), "swapped/a.go", nil)
	if got := read(); got.status == http.StatusOK || errorKind(got) != "link" {
		t.Errorf("a read through a directory swapped for a link answered %d %s, want link", got.status, got.body)
	}
	f.checkPathAnswer(t, "a listing after the swap", f.routeFor(t, "GET /api/directories"), list(), dir, nil)
	write := f.call(t, "PUT", "/api/pipelines/source", map[string]any{"root": f.root, "directory": f.directory, "path": "swapped/a.go",
		"text": "overwritten\n", "expectedSha256": strings.Repeat("0", 64)})
	f.checkPathAnswer(t, "a write after the swap", f.routeFor(t, "PUT /api/pipelines/source"), write, "swapped/a.go", nil)
	created := f.call(t, "PUT", "/api/pipelines/source", map[string]any{"root": f.root, "directory": f.directory, "path": "swapped/new.go",
		"text": "created\n", "expectedAbsent": true})
	f.checkPathAnswer(t, "a create after the swap", f.routeFor(t, "PUT /api/pipelines/source"), created, "swapped/new.go", nil)
	unchanged(t, "the requests after the swap", before, f.s.base, f.root, f.s.markers)
}

func (f *fixture) routeFor(t *testing.T, pattern string) attackRoute {
	t.Helper()
	for _, route := range f.routes {
		if route.pattern == pattern {
			return route
		}
	}
	t.Fatalf("no route %s", pattern)
	return attackRoute{}
}

func queryOf(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, pairs[i]+"="+urlQueryEscape(pairs[i+1]))
	}
	return strings.Join(parts, "&")
}

// probing holds that a target outside the roots answers the same whatever
// it is: a directory, a file, missing, unreadable or a link cycle.
func (f *fixture) probing(t *testing.T) {
	variants := []string{"probe-dir", "probe-file", "probe-missing", "probe-unreadable", "probe-cycle-a"}
	for _, route := range f.routes {
		for _, input := range route.row.paths {
			places := []string{f.s.outside}
			// Inside home the browser and the authorize route are meant to
			// tell these apart (#7, #63); outside home nothing may.
			if route.pattern != "GET /api/directories" && route.pattern != "POST /api/roots" {
				places = append(places, f.elsewhere)
			}
			for _, place := range places {
				var first answer
				for i, variant := range variants {
					value := filepath.Join(place, variant)
					if input == "directory" || input == "name" || (input == "path" && route.path == "/api/pipelines/source") {
						relative, err := filepath.Rel(f.pipelineDir, value)
						if err != nil {
							t.Fatal(err)
						}
						value = relative
					}
					target, body := route.build(t, route.with(f, input, value))
					header := f.a.authorized()
					if body != nil {
						header.Set("Content-Type", "application/json")
					}
					got := f.a.send(request{method: route.method, target: target, body: body, header: header})
					if i == 0 {
						first = got
						continue
					}
					if input == "name" {
						// A name is never resolved: it becomes an id inside
						// the root, and the answer echoes it and the file
						// written from it. What is at the place it spells
						// may not show, so the status and kind are compared.
						if got.status != first.status || errorKind(got) != errorKind(first) {
							t.Errorf("%s with name naming %s outside the roots answers %d %s, and %d %s for %s", route.pattern, variant, got.status, errorKind(got), first.status, errorKind(first), variants[0])
						}
						continue
					}
					if difference := sameAnswer(first, got); difference != "" {
						t.Errorf("%s with %s naming %s outside the roots answers differently from %s: %s", route.pattern, input, variant, variants[0], difference)
					}
				}
			}
			f.restoreRoots(t)
		}
	}
}

// agentFiles holds that the settings directory, a directory in it (the
// working-copy tree, #43, among them), the
// directory above it and a link to any of them cannot be authorized, and
// that no sequence of authorize, create and source read or write answers a
// byte of roots.json or connections.json or changes either.
func (f *fixture) agentFiles(t *testing.T) {
	s := f.s
	before := snapshot(t, s.settings)
	roots, _ := os.ReadFile(filepath.Join(s.settings, "roots.json"))
	connections, _ := os.ReadFile(filepath.Join(s.settings, "connections.json"))
	if len(roots) == 0 || len(connections) == 0 {
		t.Fatal("the settings directory holds no roots.json or connections.json to protect")
	}
	candidates := []string{s.settings, filepath.Join(s.settings, "sub"), filepath.Join(s.settings, "workcopies"), s.config,
		filepath.Join(s.home, "links", "settings"), filepath.Join(s.home, "links", "settings-sub"), filepath.Join(s.home, "links", "config"),
		f.root + "/settings-link"}
	for _, candidate := range candidates {
		got := f.call(t, "POST", "/api/roots", map[string]any{"path": candidate})
		if kind := errorKind(got); kind != "forbidden" && kind != "agentSettings" {
			t.Errorf("authorizing %s answered %d %s, want forbidden or agentSettings", candidate, got.status, got.body)
		}
		f.restoreRoots(t)
		// Whatever the authorize said, nothing that follows reaches the
		// files.
		for _, location := range [][2]string{{candidate, ""}, {candidate, "kpv"}, {candidate, "sub"}, {f.root, "settings-link"}} {
			values := map[string]any{"root": location[0]}
			query := []string{"root", location[0]}
			if location[1] != "" {
				values["directory"] = location[1]
				query = append(query, "directory", location[1])
			}
			answers := []answer{
				f.call(t, "POST", "/api/pipelines", map[string]any{"root": location[0], "name": "kpv", "language": "go",
					"sourceTopic": "a", "destinationTopic": "b", "networkMode": "docker"}),
				f.call(t, "GET", "/api/pipelines/open?"+queryOf(query...), nil),
			}
			for _, file := range []string{"roots.json", "connections.json", "kpv/roots.json", "../roots.json", "workcopies/x.json"} {
				answers = append(answers, f.call(t, "GET", "/api/pipelines/source?"+queryOf(append(query, "path", file)...), nil))
				write := map[string]any{"path": file, "text": "{}", "expectedSha256": strings.Repeat("0", 64)}
				absent := map[string]any{"path": file, "text": "{}", "expectedAbsent": true}
				for name, value := range values {
					write[name], absent[name] = value, value
				}
				answers = append(answers, f.call(t, "PUT", "/api/pipelines/source", write), f.call(t, "PUT", "/api/pipelines/source", absent))
			}
			for _, got := range answers {
				for _, protected := range [][]byte{roots, connections} {
					if strings.Contains(string(got.body), strings.TrimSpace(string(protected))) {
						t.Errorf("%s %s answered an agent file: %s", got.method, shortPath(got.target), got.body)
					}
				}
				if strings.Contains(string(got.body), f.password) {
					t.Errorf("%s %s answered the stored password", got.method, shortPath(got.target))
				}
			}
		}
	}
	unchanged(t, "the requests at the agent's own files", before, s.settings)
}
