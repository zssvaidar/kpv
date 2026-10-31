// Load and Save, the file layer that moved here from internal/pipeline (#48):
// every read and write goes through a projectfs Path, and a write is atomic.

package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// pipelineDir is a scratch root holding one pipeline directory, resolved
// through a guard over that root only.
func pipelineDir(t *testing.T) (string, projectfs.Path, *projectfs.Guard) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "root")
	directory := filepath.Join(root, "pipe")
	mkdir(t, directory)
	guard, err := projectfs.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	resolved, err := guard.Resolve(directory)
	if err != nil {
		t.Fatal(err)
	}
	return directory, resolved, guard
}

func TestSaveThenLoadRoundTripsThroughTheGuard(t *testing.T) {
	t.Parallel()
	directory, dir, _ := pipelineDir(t)
	want, _, err := pipeline.ParseYAML([]byte(valid("round")))
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, want); err != nil {
		t.Fatal(err)
	}
	first := read(t, filepath.Join(directory, FileName))
	got, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the round trip changed the pipeline\n got %+v\nwant %+v", got, want)
	}
	if err := Save(dir, got); err != nil {
		t.Fatal(err)
	}
	if again := read(t, filepath.Join(directory, FileName)); again != first {
		t.Errorf("a load-save cycle changed the bytes:\n%s\n%s", first, again)
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 1 {
		t.Errorf("the directory holds %v, want only %s", entries, FileName)
	}
	if info, _ := os.Stat(filepath.Join(directory, FileName)); info.Mode().Perm() != 0o644 {
		t.Errorf("a new file is %v, want 0644", info.Mode().Perm())
	}
}

func TestSaveKeepsTheModeOfAnExistingFile(t *testing.T) {
	t.Parallel()
	directory, dir, _ := pipelineDir(t)
	write(t, filepath.Join(directory, FileName), valid("a"))
	if err := os.Chmod(filepath.Join(directory, FileName), 0o600); err != nil {
		t.Fatal(err)
	}
	model, _, _ := Load(dir)
	if err := Save(dir, model); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(directory, FileName)); info.Mode().Perm() != 0o600 {
		t.Errorf("the file became %v, want its own 0600", info.Mode().Perm())
	}
}

func TestLoadAnswersWhyAFileDidNotLoad(t *testing.T) {
	t.Parallel()
	directory, dir, _ := pipelineDir(t)
	if _, _, err := Load(dir); kindOf(t, err) != KindNotFound {
		t.Errorf("a missing file answered %v", err)
	}
	write(t, filepath.Join(directory, FileName), "id: [")
	_, _, err := Load(dir)
	if fault, ok := err.(*Error); !ok || fault.Kind != KindUnreadable || fault.Unreadable.Kind != UnreadableParse {
		t.Errorf("a malformed file answered %v", err)
	}
	if strings.Contains(err.Error(), directory) {
		t.Errorf("the error carries the path: %v", err)
	}
}

func TestAnInvalidSaveAndAFailedSaveKeepThePreviousFile(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	directory, dir, _ := pipelineDir(t)
	write(t, filepath.Join(directory, FileName), valid("a"))
	before := read(t, filepath.Join(directory, FileName))
	model, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	model.Language = "cobol"
	err = Save(dir, model)
	if fault, ok := err.(*Error); !ok || fault.Kind != KindInvalid || len(fault.Problems) == 0 || fault.Problems[0].Pointer != "/language" {
		t.Errorf("an invalid pipeline answered %v", err)
	}
	if err := Save(dir, nil); kindOf(t, err) != KindInvalid {
		t.Errorf("no pipeline answered %v", err)
	}

	model.Language = "go"
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o755) })
	if err := Save(dir, model); kindOf(t, err) != KindIO {
		t.Errorf("a save into a read-only directory answered %v", err)
	}
	if after := read(t, filepath.Join(directory, FileName)); after != before {
		t.Errorf("a refused save changed the file:\n%s", after)
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 1 {
		t.Errorf("a failed save left %v", entries)
	}
}

func TestARevokedRootStopsTheReadAndTheWrite(t *testing.T) {
	t.Parallel()
	directory, dir, guard := pipelineDir(t)
	write(t, filepath.Join(directory, FileName), valid("a"))
	model, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Revoke(filepath.Dir(directory)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(dir); kindOf(t, err) != KindNotAuthorized {
		t.Errorf("a load after revoke answered %v", err)
	}
	if err := Save(dir, model); kindOf(t, err) != KindNotAuthorized {
		t.Errorf("a save after revoke answered %v", err)
	}
}

func TestAPipelineDirectoryWithALinkOnItsPathIsNeverWrittenThrough(t *testing.T) {
	t.Parallel()
	directory, dir, _ := pipelineDir(t)
	elsewhere := filepath.Join(filepath.Dir(directory), "elsewhere")
	mkdir(t, elsewhere)
	if err := os.Rename(directory, directory+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, directory); err != nil {
		t.Fatal(err)
	}
	model, _, _ := pipeline.ParseYAML([]byte(valid("a")))
	if err := Save(dir, model); kindOf(t, err) != KindNotFound {
		t.Errorf("a save through a link answered %v", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("the link's target was written into: %v", entries)
	}
}
