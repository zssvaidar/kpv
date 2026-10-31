package secrets

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// swapForLink renames the directory at at and puts a link to target at its
// name, and returns where the original now is.
func swapForLink(t *testing.T, at, target string) string {
	t.Helper()
	moved := at + ".moved"
	if err := os.Rename(at, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
	return moved
}

// outsidePipe drops the pipeline directory's own subtree from a snapshot of
// the root, leaving everything a write must not touch.
func outsidePipe(snap map[string]string, pipe string) map[string]string {
	out := map[string]string{}
	for name, value := range snap {
		if name == pipe || strings.HasPrefix(name, pipe+string(filepath.Separator)) {
			continue
		}
		out[name] = value
	}
	return out
}

// A link anywhere in the pipeline directory's path, whether it was there when
// the call was made or swapped in between Resolve and Pin, is the guard's
// changed, and nothing is read or written.
func TestALinkInThePipelinePathIsRefusedChanged(t *testing.T) {
	t.Parallel()
	calls := map[string]func(p *project, dir string) error{
		"write": func(p *project, dir string) error {
			_, err := p.files.WriteEnv(context.Background(), dir, secretsOf(map[string]string{"K": canary}), nil)
			return err
		},
		"gitignore": func(p *project, dir string) error {
			_, err := p.files.WriteGitignore(context.Background(), dir)
			return err
		},
		"read": func(p *project, dir string) error { _, err := p.files.ReadEnv(dir); return err },
	}
	for call, run := range calls {
		for _, swapped := range []string{"given", "pipeline", "ancestor"} {
			t.Run(call+" "+swapped, func(t *testing.T) {
				t.Parallel()
				p := newProjectAt(t, filepath.Join("group", "pipe"))
				write(t, p.path(EnvFile), "A=1\n")
				write(t, filepath.Join(p.root, "group", "sibling", "pipeline.yaml"), "id: other\n")
				write(t, filepath.Join(p.root, "other", "pipe", "pipeline.yaml"), "id: other\n")
				dir := p.pipe
				switch swapped {
				case "given":
					dir = filepath.Join(p.root, "alias")
					if err := os.Symlink(p.pipe, dir); err != nil {
						t.Fatal(err)
					}
				case "pipeline":
					p.files.hooks.beforePin = func() {
						swapForLink(t, p.pipe, filepath.Join(p.root, "group", "sibling"))
					}
				case "ancestor":
					p.files.hooks.beforePin = func() {
						swapForLink(t, filepath.Join(p.root, "group"), filepath.Join(p.root, "other"))
					}
				}
				before := snapshot(t, filepath.Dir(p.root))
				err := run(p, dir)
				var refusal *Error
				if !errors.As(err, &refusal) || refusal.Problems[0].Kind != KindRefused ||
					refusal.Problems[0].Guard != projectfs.KindChanged || projectfs.KindOf(err) != projectfs.KindChanged {
					t.Fatalf("%v", err)
				}
				after := snapshot(t, filepath.Dir(p.root))
				if swapped == "given" {
					sameSnapshot(t, before, after)
					return
				}
				// Only the swap itself changed anything.
				var changed []string
				for name, value := range after {
					if before[name] != value {
						changed = append(changed, name)
					}
				}
				for _, name := range changed {
					if !strings.HasSuffix(name, ".moved") && !strings.Contains(name, ".moved"+string(filepath.Separator)) &&
						name != filepath.Join("project", "group", "pipe") && name != filepath.Join("project", "group") {
						t.Errorf("%s changed", name)
					}
				}
				for name := range after {
					if filepath.Base(name) == EnvFile && !strings.Contains(name, ".moved") {
						t.Errorf("a .env appeared at %s", name)
					}
				}
			})
		}
	}
}

// The pipeline directory, or a directory above it, is renamed away and a link
// to an in-root sibling put at its name during a write: both files land in
// the original directory, under its new name, and the sibling is untouched.
func TestADirectorySwappedDuringAWriteKeepsGettingThatWrite(t *testing.T) {
	t.Parallel()
	for _, swapped := range []string{"pipeline", "ancestor", "renamed only"} {
		for _, when := range []string{"before the gitignore", "between the writes"} {
			t.Run(swapped+" "+when, func(t *testing.T) {
				t.Parallel()
				p := newProjectAt(t, filepath.Join("group", "pipe"))
				gitInit(t, p.ceiling(), p.pipe)
				sibling := filepath.Join(p.root, "group", "sibling")
				write(t, filepath.Join(sibling, "pipeline.yaml"), "id: sibling\n")
				write(t, filepath.Join(sibling, GitignoreFile), "dist\n")
				write(t, filepath.Join(p.root, "other", "pipe", "pipeline.yaml"), "id: other\n")
				siblingBefore := snapshot(t, sibling)
				otherBefore := snapshot(t, filepath.Join(p.root, "other"))

				original := ""
				swap := func() {
					switch swapped {
					case "pipeline":
						original = swapForLink(t, p.pipe, sibling)
					case "ancestor":
						original = filepath.Join(swapForLink(t, filepath.Join(p.root, "group"), filepath.Join(p.root, "other")), "pipe")
					case "renamed only":
						original = p.pipe + ".moved"
						if err := os.Rename(p.pipe, original); err != nil {
							t.Fatal(err)
						}
					}
				}
				if when == "before the gitignore" {
					p.files.hooks.beforeGitignore = swap
				} else {
					p.files.hooks.betweenWrites = swap
				}
				report, err := p.write(t, map[string]string{"K": canary})
				if err != nil || report.Gitignore != Created || report.Env != Created {
					t.Fatalf("%+v %v", report, err)
				}
				if original == "" {
					t.Fatal("the hook did not run")
				}
				env := read(t, filepath.Join(original, EnvFile))
				if !strings.Contains(env, `K="`) || !strings.Contains(read(t, filepath.Join(original, GitignoreFile)), StartMarker) {
					t.Fatalf("the files are not in the original directory: %q", env)
				}
				if !gitIgnores(t, p.ceiling(), original, EnvFile) {
					t.Fatal("git does not ignore the .env written")
				}
				// For an ancestor swap the link at group/ leads to other/, so
				// group/pipe then names other/pipe; group/sibling moved with
				// its parent.
				sameSnapshot(t, otherBefore, snapshot(t, filepath.Join(p.root, "other")))
				if swapped == "ancestor" {
					sibling = filepath.Join(filepath.Dir(original), "sibling")
				}
				sameSnapshot(t, siblingBefore, snapshot(t, sibling))
			})
		}
	}
}

// A goroutine swaps the pipeline directory for a link to an in-root sibling
// and back while writes run in a loop. Every write succeeds or is refused by
// the guard, no write lands outside the pipeline directory, and every .env
// sits next to a .gitignore that ignores it.
func TestWritesRacingADirectorySwapNeverLandOutsideThePipelineDirectory(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	gitInit(t, p.ceiling(), p.pipe)
	sibling := filepath.Join(p.root, "sibling")
	write(t, filepath.Join(sibling, "pipeline.yaml"), "id: sibling\n")
	write(t, filepath.Join(sibling, GitignoreFile), "dist\n")
	before := outsidePipe(snapshot(t, p.root), "pipe")

	var stop atomic.Bool
	var swapper sync.WaitGroup
	halt := func() {
		stop.Store(true)
		swapper.Wait()
	}
	defer halt()
	swapper.Add(1)
	go func() {
		defer swapper.Done()
		away := p.pipe + ".away"
		for !stop.Load() {
			if err := os.Rename(p.pipe, away); err != nil {
				t.Error(err)
				return
			}
			if err := os.Symlink(sibling, p.pipe); err != nil {
				t.Error(err)
				return
			}
			time.Sleep(50 * time.Microsecond)
			if err := os.Remove(p.pipe); err != nil {
				t.Error(err)
				return
			}
			if err := os.Rename(away, p.pipe); err != nil {
				t.Error(err)
				return
			}
			time.Sleep(200 * time.Microsecond)
		}
	}()

	refused := map[projectfs.ErrorKind]int{}
	succeeded := 0
	for i := range 300 {
		var err error
		if i%3 == 2 {
			_, err = p.change(t, nil, fmt.Sprintf("K%d", i-1))
		} else {
			_, err = p.write(t, map[string]string{fmt.Sprintf("K%d", i): canary})
		}
		if err == nil {
			succeeded++
			continue
		}
		if text := fmt.Sprintf("%v %+v %#v", err, err, err); strings.Contains(text, "Pa55w0rd") {
			t.Fatalf("the value is in the error: %s", text)
		}
		var refusal *Error
		if !errors.As(err, &refusal) {
			t.Fatalf("write %d: %T %v", i, err, err)
		}
		for _, problem := range refusal.Problems {
			switch {
			case problem.Kind != KindRefused:
				t.Fatalf("write %d: %v", i, err)
			case problem.Guard == projectfs.KindChanged, problem.Guard == projectfs.KindNotFound, problem.Guard == projectfs.KindNotDirectory:
				refused[problem.Guard]++
			default:
				t.Fatalf("write %d: refused %s: %v (%+v)", i, problem.Guard, err, errors.Unwrap(err))
			}
		}
	}
	halt()
	if t.Failed() {
		return
	}
	t.Logf("%d succeeded, refused %v", succeeded, refused)
	if succeeded == 0 {
		t.Fatal("no write succeeded, so this checked nothing")
	}

	sameSnapshot(t, before, outsidePipe(snapshot(t, p.root), "pipe"))
	err := filepath.WalkDir(p.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Name() != EnvFile {
			return err
		}
		directory := filepath.Dir(path)
		if directory != p.pipe {
			t.Errorf("a .env at %s", path)
		}
		if !gitIgnores(t, p.ceiling(), directory, EnvFile) {
			t.Errorf("%s is not ignored", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(p.path(EnvFile)); err != nil {
		t.Fatal(err)
	}
}
