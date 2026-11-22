// Source files (#87): reading and writing a file a custom-code block names,
// inside a pipeline directory, and every way the path, the directories on it
// or the file can be other than expected.

package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func hashOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// sourceWorld is a root holding the pipeline orders, with src/main.py in it.
func sourceWorld(t *testing.T) (*world, string, Location) {
	t.Helper()
	w := newWorld(t)
	root := w.root("code")
	pipelineAt(t, root, "orders", "orders")
	write(t, filepath.Join(root, "orders", "src", "main.py"), "print('hi')\n")
	return w, root, Location{Root: root, Directory: "orders"}
}

func sourceKind(t *testing.T, err error) ErrorKind {
	t.Helper()
	if err == nil {
		t.Fatal("the request succeeded, want a refusal")
	}
	return kindOf(t, err)
}

func TestASourceFileReadsBackExactlyAsOnDisk(t *testing.T) {
	t.Parallel()
	w, root, orders := sourceWorld(t)
	for name, text := range map[string]string{
		"crlf.py":       "a = 1\r\nb = 2\r\n",
		"bom.py":        "\ufeffa = 1\n",
		"no-newline.py": "a = 1",
		"empty.py":      "",
		"unicode.py":    "name = 'café ✓'\n",
	} {
		write(t, filepath.Join(root, "orders", "src", name), text)
		got, err := w.catalog.ReadSource(orders, "src/"+name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got.Text) != text || got.SHA256 != hashOf(text) || got.Generated || got.Path != "src/"+name || got.Location != orders {
			t.Errorf("%s read as %+v", name, got)
		}
	}
	// Exactly 1 MiB is read; generated/ is readable and says so, in any case.
	write(t, filepath.Join(root, "orders", "exact.txt"), strings.Repeat("x", MaxFileSize))
	if got, err := w.catalog.ReadSource(orders, "exact.txt"); err != nil || len(got.Text) != MaxFileSize {
		t.Errorf("a file of exactly 1 MiB: %v", err)
	}
	write(t, filepath.Join(root, "orders", "generated", "main.py"), "# generated\n")
	write(t, filepath.Join(root, "orders", "Generated", "other.py"), "# generated\n")
	for _, path := range []string{"generated/main.py", "Generated/other.py"} {
		if got, err := w.catalog.ReadSource(orders, path); err != nil || !got.Generated {
			t.Errorf("%s: %+v %v", path, got, err)
		}
	}
	// pipeline.yaml deeper than the top is any other file.
	write(t, filepath.Join(root, "orders", "src", FileName), "x: 1\n")
	if _, err := w.catalog.ReadSource(orders, "src/"+FileName); err != nil {
		t.Errorf("src/pipeline.yaml: %v", err)
	}
	// A root that is itself the pipeline is addressed with no directory.
	single := w.root("single")
	pipelineAt(t, single, "", "single")
	write(t, filepath.Join(single, "src", "a.py"), "a\n")
	if got, err := w.catalog.ReadSource(Location{Root: single}, "src/a.py"); err != nil || string(got.Text) != "a\n" {
		t.Errorf("a root pipeline's file: %+v %v", got, err)
	}
}

func TestASourcePathTheAgentWillNotReasonAboutIsInvalidBeforeTheDiskIsTouched(t *testing.T) {
	t.Parallel()
	w, root, orders := sourceWorld(t)
	// Not even authorized: the path is refused before the root is looked at.
	elsewhere := Location{Root: filepath.Join(w.home, "elsewhere"), Directory: "x"}
	for _, path := range []string{
		"", ".", "..", "src/../../x", "src/./main.py", "src//main.py", "src/", "/etc/passwd", ".env",
		"src/.env", `src\main.py`, "src/main.py\x00", "src/ main.py", "../code/orders/src/main.py",
		strings.Repeat("a", maxSourcePath+1), "src/.git/config", "-x", "é.py",
	} {
		for _, location := range []Location{orders, elsewhere, {Root: root, Directory: "../x"}, {}} {
			if _, err := w.catalog.ReadSource(location, path); sourceKind(t, err) != KindInvalid {
				t.Errorf("read %q in %+v: %v", path, location, err)
			}
			if _, err := w.catalog.WriteSource(SourceWrite{Location: location, Path: path, Text: "x", ExpectedAbsent: true}); sourceKind(t, err) != KindInvalid {
				t.Errorf("write %q in %+v: %v", path, location, err)
			}
		}
	}
	if _, err := w.catalog.ReadSource(Location{Root: root, Directory: "a/b"}, "src/main.py"); sourceKind(t, err) != KindInvalid {
		t.Errorf("a directory with a slash: %v", err)
	}
	// The longest path the schema allows is read.
	longest := strings.Repeat("a", 200) + "/" + strings.Repeat("b", 200) + "/" + strings.Repeat("c", maxSourcePath-402)
	write(t, filepath.Join(root, "orders", filepath.FromSlash(longest)), "long\n")
	if _, err := w.catalog.ReadSource(orders, longest); err != nil {
		t.Errorf("a path of %d bytes: %v", len(longest), err)
	}
}

func TestPipelineYAMLIsReservedAndGeneratedIsNotWritten(t *testing.T) {
	t.Parallel()
	w, root, orders := sourceWorld(t)
	before := snapshot(t, root)
	for _, path := range []string{"pipeline.yaml", "PIPELINE.YAML", "Pipeline.Yaml"} {
		if _, err := w.catalog.ReadSource(orders, path); sourceKind(t, err) != KindReserved {
			t.Errorf("read %s: %v", path, err)
		}
		if _, err := w.catalog.WriteSource(SourceWrite{Location: orders, Path: path, Text: "x", ExpectedAbsent: true}); sourceKind(t, err) != KindReserved {
			t.Errorf("write %s: %v", path, err)
		}
	}
	for _, path := range []string{"generated/main.py", "GENERATED/new.py", "generated"} {
		if _, err := w.catalog.WriteSource(SourceWrite{Location: orders, Path: path, Text: "x", ExpectedAbsent: true}); sourceKind(t, err) != KindGenerated {
			t.Errorf("write %s: %v", path, err)
		}
	}
	if after := snapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Errorf("a refused write changed the disk")
	}
}

func TestEveryWayASourceFileIsNotThereOrNotAFile(t *testing.T) {
	t.Parallel()
	w, root, orders := sourceWorld(t)
	dir := filepath.Join(root, "orders")
	pipelineAt(t, root, "billing", "billing")
	write(t, filepath.Join(root, "billing", "src", "main.py"), "billing\n")
	mkdir(t, filepath.Join(root, "plain"))
	write(t, filepath.Join(root, "plain", "src", "main.py"), "not a pipeline\n")
	pipelineAt(t, root, ".hidden", "hidden")
	write(t, filepath.Join(root, ".hidden", "a.py"), "a\n")

	write(t, filepath.Join(dir, "big.py"), strings.Repeat("#", MaxFileSize+1))
	write(t, filepath.Join(dir, "latin1.py"), "caf\xe9\n")
	write(t, filepath.Join(dir, "nul.py"), "a\x00b\n")
	mkdir(t, filepath.Join(dir, "folder"))
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Links that stay inside the pipeline directory and the root are refused
	// all the same.
	if err := os.Symlink(filepath.Join(dir, "src", "main.py"), filepath.Join(dir, "file-link.py")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "src"), filepath.Join(dir, "dir-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "billing"), filepath.Join(root, "pipeline-link")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "file"), "a file\n")

	for name, test := range map[string]struct {
		location Location
		path     string
		want     ErrorKind
	}{
		"a missing file":                {orders, "src/gone.py", KindNotFound},
		"a missing directory":           {orders, "lib/gone.py", KindNotFound},
		"a file where a directory is":   {orders, "file/x.py", KindNotFound},
		"a directory with no pipeline":  {Location{Root: root, Directory: "plain"}, "src/main.py", KindNotFound},
		"a missing pipeline":            {Location{Root: root, Directory: "gone"}, "src/main.py", KindNotFound},
		"a hidden pipeline":             {Location{Root: root, Directory: ".hidden"}, "a.py", KindNotFound},
		"a root that is not a pipeline": {Location{Root: root}, "orders/src/main.py", KindNotFound},
		"a file link":                   {orders, "file-link.py", KindLink},
		"a directory link":              {orders, "dir-link/main.py", KindLink},
		"a pipeline directory link":     {Location{Root: root, Directory: "pipeline-link"}, "src/main.py", KindLink},
		"a directory":                   {orders, "folder", KindNotRegularFile},
		"a FIFO":                        {orders, "fifo", KindNotRegularFile},
		"over 1 MiB":                    {orders, "big.py", KindTooLarge},
		"not UTF-8":                     {orders, "latin1.py", KindNotText},
		"a NUL":                         {orders, "nul.py", KindNotText},
	} {
		if _, err := w.catalog.ReadSource(test.location, test.path); sourceKind(t, err) != test.want {
			t.Errorf("read %s: %v, want %s", name, err, test.want)
		}
	}
}

func TestASourceInAnUnavailableOrUnauthorizedRootAnswersAsOpenDoes(t *testing.T) {
	t.Parallel()
	w, root, orders := sourceWorld(t)
	outside := Location{Root: filepath.Join(w.home, "outside"), Directory: "orders"}
	if _, err := w.catalog.ReadSource(outside, "src/main.py"); sourceKind(t, err) != KindNotAuthorized {
		t.Errorf("an unauthorized root: %v", err)
	}
	if _, err := w.catalog.WriteSource(SourceWrite{Location: outside, Path: "src/main.py", Text: "x", ExpectedAbsent: true}); sourceKind(t, err) != KindNotAuthorized {
		t.Errorf("a write to an unauthorized root: %v", err)
	}
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	_, openErr := w.catalog.Open(orders)
	_, err := w.catalog.ReadSource(orders, "src/main.py")
	if sourceKind(t, err) != KindRootUnavailable || !reflect.DeepEqual(err, openErr) {
		t.Errorf("an unavailable root reads as %v, opens as %v", err, openErr)
	}
	_, err = w.catalog.WriteSource(SourceWrite{Location: orders, Path: "src/main.py", Text: "x", ExpectedSHA256: hashOf("x")})
	if !reflect.DeepEqual(err, openErr) {
		t.Errorf("an unavailable root writes as %v, opens as %v", err, openErr)
	}
	if _, err := w.roots.Revoke(root); err != nil {
		t.Fatal(err)
	}
	if _, err := w.catalog.ReadSource(orders, "src/main.py"); sourceKind(t, err) != KindNotAuthorized {
		t.Errorf("a revoked root: %v", err)
	}
}

func TestAnUnreadableSourceIsUnreadable(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	w, root, orders := sourceWorld(t)
	file := filepath.Join(root, "orders", "src", "main.py")
	if err := os.Chmod(file, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(file, 0o644) })
	if _, err := w.catalog.ReadSource(orders, "src/main.py"); sourceKind(t, err) != KindUnreadable {
		t.Errorf("an unreadable file: %v", err)
	}
	src := filepath.Join(root, "orders", "src")
	if err := os.Chmod(src, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(src, 0o755) })
	if _, err := w.catalog.ReadSource(orders, "src/main.py"); sourceKind(t, err) != KindUnreadable {
		t.Errorf("an unsearchable directory: %v", err)
	}
}

func TestAWriteCreatesTheFileAndItsDirectoriesInsideThePipelineOnly(t *testing.T) {
	t.Parallel()
	w, root, orders := sourceWorld(t)
	write(t, filepath.Join(root, "beside.txt"), "beside\n")
	pipelineAt(t, root, "billing", "billing")
	outsideBefore := snapshot(t, root)
	for path := range outsideBefore {
		if path == "orders" || strings.HasPrefix(path, "orders"+string(filepath.Separator)) {
			delete(outsideBefore, path)
		}
	}

	text := "def run(message):\r\n    return message\r\n"
	sum, err := w.catalog.WriteSource(SourceWrite{Location: orders, Path: "lib/steps/run.py", Text: text, ExpectedAbsent: true})
	if err != nil || sum != hashOf(text) {
		t.Fatalf("the write answered %q %v", sum, err)
	}
	written := filepath.Join(root, "orders", "lib", "steps", "run.py")
	if got := read(t, written); got != text {
		t.Errorf("the bytes written are %q", got)
	}
	umask := currentUmask(t, root)
	for path, want := range map[string]os.FileMode{
		filepath.Join(root, "orders", "lib"):          0o755 &^ umask,
		filepath.Join(root, "orders", "lib", "steps"): 0o755 &^ umask,
		written: 0o644 &^ umask,
	} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Errorf("%s has mode %v, want %v (%v)", path, info.Mode().Perm(), want, err)
		}
	}
	got, err := w.catalog.ReadSource(orders, "lib/steps/run.py")
	if err != nil || got.SHA256 != sum || string(got.Text) != text {
		t.Errorf("the read after the write is %+v %v", got, err)
	}

	// An update with the hash the read answered.
	next := "def run(message):\n    return None\n"
	again, err := w.catalog.WriteSource(SourceWrite{Location: orders, Path: "lib/steps/run.py", Text: next, ExpectedSHA256: got.SHA256})
	if err != nil || again != hashOf(next) || read(t, written) != next {
		t.Errorf("the update answered %q %v and wrote %q", again, err, read(t, written))
	}

	outsideAfter := snapshot(t, root)
	for path := range outsideAfter {
		if path == "orders" || strings.HasPrefix(path, "orders"+string(filepath.Separator)) {
			delete(outsideAfter, path)
		}
	}
	if !reflect.DeepEqual(outsideBefore, outsideAfter) {
		t.Errorf("the rest of the root changed:\nbefore %v\nafter  %v", outsideBefore, outsideAfter)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "orders", "lib", "steps"))
	if len(entries) != 1 {
		t.Errorf("the write left %v", entries)
	}
}

// currentUmask is the process umask, read off a directory made under it.
func currentUmask(t *testing.T, root string) os.FileMode {
	t.Helper()
	probe := filepath.Join(filepath.Dir(root), "umask-probe")
	if err := os.Mkdir(probe, 0o777); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(probe) }()
	info, err := os.Stat(probe)
	if err != nil {
		t.Fatal(err)
	}
	return 0o777 &^ info.Mode().Perm()
}

func TestAWriteThatDoesNotMatchWhatIsOnDiskWritesNothing(t *testing.T) {
	t.Parallel()
	w, root, orders := sourceWorld(t)
	current := hashOf("print('hi')\n")
	before := snapshot(t, root)
	_, err := w.catalog.WriteSource(SourceWrite{Location: orders, Path: "src/main.py", Text: "x", ExpectedSHA256: hashOf("something else")})
	var fault *Error
	if !errors.As(err, &fault) || fault.Kind != KindChanged || fault.SHA256 != current {
		t.Errorf("a stale hash answered %+v", err)
	}
	if _, err := w.catalog.WriteSource(SourceWrite{Location: orders, Path: "src/main.py", Text: "x", ExpectedAbsent: true}); sourceKind(t, err) != KindExists {
		t.Errorf("expectedAbsent over a file: %v", err)
	}
	if _, err := w.catalog.WriteSource(SourceWrite{Location: orders, Path: "src/gone.py", Text: "x", ExpectedSHA256: current}); sourceKind(t, err) != KindNotFound {
		t.Errorf("a hash for a missing file: %v", err)
	}
	if _, err := w.catalog.WriteSource(SourceWrite{Location: orders, Path: "lib/gone.py", Text: "x", ExpectedSHA256: current}); sourceKind(t, err) != KindNotFound {
		t.Errorf("a hash for a file in a missing directory: %v", err)
	}
	for name, write := range map[string]SourceWrite{
		"neither":   {Location: orders, Path: "src/main.py", Text: "x"},
		"both":      {Location: orders, Path: "src/main.py", Text: "x", ExpectedSHA256: current, ExpectedAbsent: true},
		"too large": {Location: orders, Path: "src/main.py", Text: strings.Repeat("x", MaxFileSize+1), ExpectedSHA256: current},
		"a NUL":     {Location: orders, Path: "src/main.py", Text: "a\x00", ExpectedSHA256: current},
		"not UTF-8": {Location: orders, Path: "src/main.py", Text: "caf\xe9", ExpectedSHA256: current},
	} {
		want := map[string]ErrorKind{"neither": KindInvalid, "both": KindInvalid, "too large": KindTooLarge, "a NUL": KindNotText, "not UTF-8": KindNotText}[name]
		if _, err := w.catalog.WriteSource(write); sourceKind(t, err) != want {
			t.Errorf("%s: %v, want %s", name, err, want)
		}
	}
	// Exactly 1 MiB is written.
	if _, err := w.catalog.WriteSource(SourceWrite{Location: orders, Path: "src/exact.txt", Text: strings.Repeat("x", MaxFileSize), ExpectedAbsent: true}); err != nil {
		t.Errorf("a text of exactly 1 MiB: %v", err)
	}
	_ = os.Remove(filepath.Join(root, "orders", "src", "exact.txt"))
	if after := snapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Errorf("a refused write changed the disk")
	}
}

func TestAWriteIsNeverMadeThroughALink(t *testing.T) {
	t.Parallel()
	w, root, orders := sourceWorld(t)
	dir := filepath.Join(root, "orders")
	pipelineAt(t, root, "billing", "billing")
	write(t, filepath.Join(root, "billing", "src", "main.py"), "billing\n")
	if err := os.Symlink(filepath.Join(root, "billing", "src", "main.py"), filepath.Join(dir, "file-link.py")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "billing", "src"), filepath.Join(dir, "dir-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "billing"), filepath.Join(root, "pipeline-link")); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, root)
	for name, test := range map[string]struct {
		location Location
		path     string
		absent   bool
	}{
		"a file link":               {orders, "file-link.py", false},
		"a file link, absent":       {orders, "file-link.py", true},
		"a directory link":          {orders, "dir-link/main.py", false},
		"a directory link, new":     {orders, "dir-link/new.py", true},
		"a pipeline directory link": {Location{Root: root, Directory: "pipeline-link"}, "src/main.py", false},
	} {
		write := SourceWrite{Location: test.location, Path: test.path, Text: "attacker\n", ExpectedAbsent: test.absent}
		if !test.absent {
			write.ExpectedSHA256 = hashOf("billing\n")
		}
		if _, err := w.catalog.WriteSource(write); sourceKind(t, err) != KindLink {
			t.Errorf("%s: %v", name, err)
		}
	}
	if after := snapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Errorf("a write through a link changed the disk")
	}
}

func TestAWriteThatFailsPartwayLeavesThePreviousBytes(t *testing.T) {
	t.Parallel()
	w, root, orders := sourceWorld(t)
	w.catalog.hook.midSourceWrite = func() error { return errors.New("the disk is full") }
	before := snapshot(t, root)
	_, err := w.catalog.WriteSource(SourceWrite{Location: orders, Path: "src/main.py", Text: "a whole new file\n", ExpectedSHA256: hashOf("print('hi')\n")})
	if sourceKind(t, err) != KindIO {
		t.Errorf("a failed write answered %v", err)
	}
	if fault := err.Error(); strings.Contains(fault, "disk") || strings.Contains(fault, root) {
		t.Errorf("the error carries the underlying failure or a path: %s", fault)
	}
	if after := snapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Errorf("a failed write changed the disk:\nbefore %v\nafter  %v", before, after)
	}
}

func TestTwoWritesToOneFileDoNotInterleave(t *testing.T) {
	t.Parallel()
	w, root, orders := sourceWorld(t)
	original := hashOf("print('hi')\n")
	entered := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	w.catalog.hook.midSourceWrite = func() error {
		once.Do(func() {
			close(entered)
			<-proceed
		})
		return nil
	}
	first := make(chan error, 1)
	go func() {
		_, err := w.catalog.WriteSource(SourceWrite{Location: orders, Path: "src/main.py", Text: "first\n", ExpectedSHA256: original})
		first <- err
	}()
	<-entered
	second := make(chan error, 1)
	go func() {
		_, err := w.catalog.WriteSource(SourceWrite{Location: orders, Path: "src/main.py", Text: "second\n", ExpectedSHA256: original})
		second <- err
	}()
	close(proceed)
	if err := <-first; err != nil {
		t.Fatalf("the first write: %v", err)
	}
	err := <-second
	var fault *Error
	if !errors.As(err, &fault) || fault.Kind != KindChanged || fault.SHA256 != hashOf("first\n") {
		t.Errorf("the second write answered %+v, want changed with the first's hash", err)
	}
	if got := read(t, filepath.Join(root, "orders", "src", "main.py")); got != "first\n" {
		t.Errorf("the file holds %q", got)
	}
}

// A directory on the path swapped for a link after it was looked at, even one
// leading inside the root, is never read or written through.
func TestADirectorySwappedForALinkWhileItIsPinnedIsNotFollowed(t *testing.T) {
	t.Parallel()
	w, root, orders := sourceWorld(t)
	pipelineAt(t, root, "billing", "billing")
	write(t, filepath.Join(root, "billing", "src", "main.py"), "print('hi')\n")
	src := filepath.Join(root, "orders", "src")
	w.catalog.hook.beforeSourcePin = func(name string) {
		if name != "src" {
			return
		}
		if err := os.Rename(src, src+"-moved"); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(filepath.Join(root, "billing", "src"), src); err != nil {
			t.Error(err)
		}
	}
	if _, err := w.catalog.ReadSource(orders, "src/main.py"); sourceKind(t, err) != KindLink {
		t.Errorf("a read through a swapped directory: %v", err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(src+"-moved", src); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, filepath.Join(root, "billing"))
	_, err := w.catalog.WriteSource(SourceWrite{Location: orders, Path: "src/main.py", Text: "attacker\n", ExpectedSHA256: hashOf("print('hi')\n")})
	if kind := sourceKind(t, err); kind != KindLink && kind != KindChanged {
		t.Errorf("a write through a swapped directory: %v", err)
	}
	if after := snapshot(t, filepath.Join(root, "billing")); !reflect.DeepEqual(before, after) {
		t.Errorf("the write reached the link's target")
	}
}

// Every refusal a read has for what is on the disk, a write has too, and
// writes nothing.
func TestAWriteHasEveryRefusalTheReadHas(t *testing.T) {
	t.Parallel()
	w, root, orders := sourceWorld(t)
	dir := filepath.Join(root, "orders")
	mkdir(t, filepath.Join(root, "plain"))
	write(t, filepath.Join(root, "plain", "src", "main.py"), "not a pipeline\n")
	pipelineAt(t, root, ".hidden", "hidden")
	write(t, filepath.Join(dir, "big.py"), strings.Repeat("#", MaxFileSize+1))
	write(t, filepath.Join(dir, "latin1.py"), "caf\xe9\n")
	write(t, filepath.Join(dir, "nul.py"), "a\x00b\n")
	mkdir(t, filepath.Join(dir, "folder"))
	write(t, filepath.Join(dir, "file"), "a file\n")
	// The snapshot reads every file, which a FIFO would block, so it is taken
	// before the FIFO is made and compared after it is removed.
	before := snapshot(t, root)
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		location Location
		path     string
		want     ErrorKind
	}{
		"a directory with no pipeline": {Location{Root: root, Directory: "plain"}, "src/main.py", KindNotFound},
		"a missing pipeline":           {Location{Root: root, Directory: "gone"}, "src/main.py", KindNotFound},
		"a hidden pipeline":            {Location{Root: root, Directory: ".hidden"}, "a.py", KindNotFound},
		"a file where a directory is":  {orders, "file/x.py", KindNotFound},
		"a directory":                  {orders, "folder", KindNotRegularFile},
		"a FIFO":                       {orders, "fifo", KindNotRegularFile},
		"over 1 MiB on disk":           {orders, "big.py", KindTooLarge},
		"not UTF-8 on disk":            {orders, "latin1.py", KindNotText},
		"a NUL on disk":                {orders, "nul.py", KindNotText},
	} {
		for _, absent := range []bool{false, true} {
			request := SourceWrite{Location: test.location, Path: test.path, Text: "new\n", ExpectedAbsent: absent}
			if !absent {
				request.ExpectedSHA256 = hashOf("anything")
			}
			if _, err := w.catalog.WriteSource(request); sourceKind(t, err) != test.want {
				t.Errorf("write %s (absent %v): %v, want %s", name, absent, err, test.want)
			}
		}
	}
	if info, err := os.Lstat(fifo); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("the FIFO was replaced: %v %v", info, err)
	}
	if err := os.Remove(fifo); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Errorf("a refused write changed the disk:\nbefore %v\nafter  %v", before, after)
	}
}
