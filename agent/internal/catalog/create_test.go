// Creating a pipeline (#48): the id it gets, the directory it claims, the
// file it writes, and what it never touches.

package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
)

var identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,62}$`)

func request(root, name string) CreateRequest {
	return CreateRequest{Root: root, Name: name, Language: "go", SourceTopic: "orders.v1", DestinationTopic: "billing.v1", NetworkMode: "host"}
}

func TestTheIDIsDerivedFromTheName(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("abcdefghij", 20)
	for name, want := range map[string]string{
		"Orders to billing":             "orders-to-billing",
		"  Orders -- to: billing (EU)!": "orders-to-billing-eu",
		"Заказы → счета":                "pipeline",
		"":                              "pipeline",
		"2024 orders":                   "pipeline-2024-orders",
		"_under_score":                  "under-score",
		"Ünïcode Mix 7":                 "n-code-mix-7",
		long:                            long[:63],
		strings.Repeat("ab ", 70):       strings.TrimRight(strings.Repeat("ab-", 21), "-"),
		// Leading and trailing separators are stripped, not turned into "-".
		"My Orders!":  "my-orders",
		"   Orders! ": "orders",
		"!!!":         "pipeline",
		"-_-":         "pipeline",
		// The cut at 63 lands just after a separator, which is dropped.
		strings.Repeat("a", 62) + " b c d": strings.Repeat("a", 62),
	} {
		got := deriveID(name)
		if got != want {
			t.Errorf("%q derived %q, want %q", name, got, want)
		}
		if !identifier.MatchString(got) {
			t.Errorf("%q derived %q, which is not an identifier", name, got)
		}
	}
	if len(deriveID(long)) != 63 {
		t.Errorf("a 200-character name derived %d characters", len(deriveID(long)))
	}
	for _, n := range []int{2, 3, 10, 9999} {
		got := withSuffix(long[:63], n)
		if len(got) > 63 || !strings.HasSuffix(got, fmt.Sprintf("-%d", n)) || !identifier.MatchString(got) {
			t.Errorf("suffix %d gave %q", n, got)
		}
	}
	// With a suffix, the base is cut first so the whole stays within 63, and
	// a cut that lands just after a separator drops it before "-<n>".
	for base, want := range map[string]string{
		strings.Repeat("a", 63):              strings.Repeat("a", 61) + "-2",
		strings.Repeat("a", 60) + "-bc":      strings.Repeat("a", 60) + "-2",
		strings.Repeat("a", 59) + "-b-cdefg": strings.Repeat("a", 59) + "-b-2",
	} {
		if got := withSuffix(base, 2); got != want || len(got) > 63 {
			t.Errorf("suffix 2 on %q gave %q, want %q", base, got, want)
		}
	}
	if withSuffix("orders", 1) != "orders" || withSuffix("orders", 2) != "orders-2" {
		t.Error("the first try is the id itself and the second is -2")
	}
}

func TestCreateWritesAValidMinimalPipelineAndOpensIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	document, err := w.catalog.Create(request(root, "Orders to billing"))
	if err != nil {
		t.Fatal(err)
	}
	if document.Root != root || document.Directory != "orders-to-billing" || document.Dir != filepath.Join(root, "orders-to-billing") {
		t.Fatalf("created at %+v", document.Location)
	}
	written := read(t, filepath.Join(root, "orders-to-billing", FileName))
	want := "destination:\n  id: destination\n  topic: billing.v1\n  type: kafkaDestination\nid: orders-to-billing\nlanguage: go\nname: Orders to billing\n" +
		"runtimeConfiguration:\n  networkMode: host\nsource:\n  id: source\n  topic: orders.v1\n  type: kafkaSource\n"
	model, steps, err := pipeline.ParseYAML([]byte(written))
	if err != nil {
		t.Fatalf("the written file does not load: %v\n%s", err, written)
	}
	if len(steps) != 0 || strings.Contains(written, "processingSteps") {
		t.Errorf("the file has processing steps:\n%s", written)
	}
	for _, defaulted := range []string{"logLevel", "autoOffsetReset", "messageFormat", "acks", "preserveKey"} {
		if strings.Contains(written, defaulted) {
			t.Errorf("the file writes out the default %s:\n%s", defaulted, written)
		}
	}
	if written != want || string(model.ID) != "orders-to-billing" {
		t.Errorf("the file is\n%s\nwant\n%s", written, want)
	}
	if document.SHA256 != digest([]byte(written)) || document.Pipeline == nil {
		t.Errorf("the answer is not the opened pipeline: %+v", document)
	}
	if info, err := os.Stat(filepath.Join(root, "orders-to-billing", FileName)); err != nil || info.Mode() != 0o644 {
		t.Errorf("the new file is %v, want 0644", info.Mode())
	}
	if info, err := os.Stat(filepath.Join(root, "orders-to-billing")); err != nil || !info.IsDir() || info.Mode().Perm()&^0o755 != 0 {
		t.Errorf("the new directory is %v, want 0755 less the umask", info.Mode())
	}
	if found := entry(t, listed(t, w.catalog.List(), root), "orders-to-billing"); !found.Readable() || found.Name != "Orders to billing" {
		t.Errorf("the new pipeline is listed %+v", found)
	}
	if _, err := w.catalog.Open(document.Location); err != nil {
		t.Errorf("the new pipeline does not open: %v", err)
	}
}

func TestACreateWhoseFieldsFailTheSchemaWritesNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	before := snapshot(t, root)
	for name, broken := range map[string]func(*CreateRequest){
		"blank name":       func(r *CreateRequest) { r.Name = "   " },
		"no language":      func(r *CreateRequest) { r.Language = "" },
		"bad topic":        func(r *CreateRequest) { r.SourceTopic = "orders v1" },
		"no destination":   func(r *CreateRequest) { r.DestinationTopic = "" },
		"no network mode":  func(r *CreateRequest) { r.NetworkMode = "" },
		"bad network mode": func(r *CreateRequest) { r.NetworkMode = "bridge" },
	} {
		r := request(root, "Orders")
		broken(&r)
		_, err := w.catalog.Create(r)
		fault, ok := err.(*Error)
		if !ok || fault.Kind != KindInvalid || len(fault.Problems) == 0 || fault.DirectoryLeftBehind != nil {
			t.Errorf("%s answered %v", name, err)
		}
	}
	if after := snapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Errorf("a refused create changed the root: %v", after)
	}
}

func TestTwoCreatesOfOneNameGetDistinctIDs(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	var got []string
	for range 3 {
		document, err := w.catalog.Create(request(root, "Orders"))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, document.Directory+"="+string(document.Pipeline.ID))
	}
	if want := []string{"orders=orders", "orders-2=orders-2", "orders-3=orders-3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("created %v, want %v", got, want)
	}
}

func TestCreateNeverWritesIntoSomethingThatIsAlreadyThere(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	write(t, filepath.Join(root, "orders", "keep.txt"), "the user's file")
	write(t, filepath.Join(root, "orders-2"), "a file named like the id")
	write(t, filepath.Join(w.home, "target", "inside.txt"), "behind the link")
	if err := os.Symlink(filepath.Join(w.home, "target"), filepath.Join(root, "orders-3")); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, w.home)
	document, err := w.catalog.Create(request(root, "Orders"))
	if err != nil {
		t.Fatal(err)
	}
	if document.Directory != "orders-4" {
		t.Errorf("created in %s, want orders-4", document.Directory)
	}
	after := snapshot(t, w.home)
	for path, was := range before {
		if after[path] != was {
			t.Errorf("%s changed from %q to %q", path, was, after[path])
		}
	}
}

func TestAnIDAnyListedPipelineHasIsTakenIgnoringCase(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	other := w.root("other")
	root := w.root("code")
	pipelineAt(t, other, "somewhere", "ORDERS")
	document, err := w.catalog.Create(request(root, "orders"))
	if err != nil {
		t.Fatal(err)
	}
	if document.Directory != "orders-2" {
		t.Errorf("created %s, though another root lists the id ORDERS", document.Directory)
	}
}

func TestConcurrentCreatesOfOneNameEachGetTheirOwnDirectory(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	const n = 8
	var wait sync.WaitGroup
	results := make(chan string, n)
	for range n {
		wait.Add(1)
		go func() {
			defer wait.Done()
			document, err := w.catalog.Create(request(root, "Orders"))
			if err != nil {
				t.Error(err)
				return
			}
			results <- document.Directory
		}()
	}
	wait.Wait()
	close(results)
	seen := map[string]bool{}
	for directory := range results {
		if seen[directory] {
			t.Errorf("two creates got %s", directory)
		}
		seen[directory] = true
		model, _, err := pipeline.ParseYAML([]byte(read(t, filepath.Join(root, directory, FileName))))
		if err != nil || string(model.ID) != directory {
			t.Errorf("%s holds %v, %v", directory, model, err)
		}
	}
	if len(seen) != n {
		t.Errorf("%d directories for %d creates", len(seen), n)
	}
}

func TestCreateIsRefusedWhereTheListCouldNotShowIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	single := w.root("single")
	write(t, filepath.Join(single, FileName), valid("single"))
	gone := w.root("gone")
	revoked := w.root("revoked")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := w.roots.Revoke(revoked); err != nil {
		t.Fatal(err)
	}
	if _, err := w.catalog.Create(request(single, "x")); kindOf(t, err) != KindRootIsPipeline {
		t.Errorf("a root that is a pipeline answered %v", err)
	}
	if _, err := w.catalog.Create(request(gone, "x")); kindOf(t, err) != KindRootUnavailable {
		t.Errorf("an unavailable root answered %v", err)
	}
	if _, err := w.catalog.Create(request(revoked, "x")); kindOf(t, err) != KindNotAuthorized {
		t.Errorf("a revoked root answered %v", err)
	}
	if _, err := w.catalog.Create(request(w.home, "x")); kindOf(t, err) != KindNotAuthorized {
		t.Errorf("home, which is not a root, answered %v", err)
	}
	if entries, _ := os.ReadDir(revoked); len(entries) != 0 {
		t.Errorf("a refused create wrote into a revoked root: %v", entries)
	}
}

func TestADirectorySwappedForALinkBeforeThePinIsRefusedAndNothingIsWritten(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	target := filepath.Join(root, "target")
	mkdir(t, target)
	w.catalog.hook.afterMkdir = func(name string) {
		if err := os.Rename(filepath.Join(root, name), filepath.Join(root, ".moved")); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Error(err)
		}
	}
	_, err := w.catalog.Create(request(root, "Orders"))
	fault, ok := err.(*Error)
	if !ok || fault.Kind != KindChanged || fault.DirectoryLeftBehind == nil || !*fault.DirectoryLeftBehind {
		t.Fatalf("the swap answered %v", err)
	}
	for _, directory := range []string{target, filepath.Join(root, ".moved")} {
		if entries, _ := os.ReadDir(directory); len(entries) != 0 {
			t.Errorf("%s was written into: %v", directory, entries)
		}
	}
}

func TestAFailedWriteRemovesTheDirectoryOnlyWhenItIsStillItsOwnAndEmpty(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	for _, testCase := range []struct {
		name       string
		meddle     func(t *testing.T, directory string)
		leftBehind bool
	}{
		{"empty, so removed", func(*testing.T, string) {}, false},
		{"something put in it", func(t *testing.T, directory string) {
			write(t, filepath.Join(directory, "theirs.txt"), "not create's")
		}, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			w := newWorld(t)
			root := w.root("code")
			w.catalog.hook.beforeWrite = func(name string) {
				directory := filepath.Join(root, name)
				testCase.meddle(t, directory)
				if err := os.Chmod(directory, 0o555); err != nil {
					t.Error(err)
				}
				t.Cleanup(func() { _ = os.Chmod(directory, 0o755) })
			}
			_, err := w.catalog.Create(request(root, "Orders"))
			fault, ok := err.(*Error)
			if !ok || fault.Kind != KindIO || fault.DirectoryLeftBehind == nil || *fault.DirectoryLeftBehind != testCase.leftBehind {
				t.Fatalf("answered %v, want io with the directory left behind = %v", err, testCase.leftBehind)
			}
			if _, statErr := os.Lstat(filepath.Join(root, "orders")); (statErr == nil) != testCase.leftBehind {
				t.Errorf("the directory is there = %v, want %v", statErr == nil, testCase.leftBehind)
			}
			if got := listed(t, w.catalog.List(), root); len(got.Pipelines) != 0 {
				t.Errorf("a directory without pipeline.yaml is listed: %v", directories(got))
			}
		})
	}
}

func TestCreateLogsKindsAndNoPath(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	if _, err := w.catalog.Create(request(root, "Secret project")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.catalog.Create(request(root, "  ")); err == nil {
		t.Fatal("a blank name was created")
	}
	logs := w.logs.String()
	if !strings.Contains(logs, "a pipeline was created") || !strings.Contains(logs, "kind=invalid") {
		t.Errorf("the creates were not logged: %s", logs)
	}
	if strings.Contains(logs, w.home) || strings.Contains(logs, "secret") {
		t.Errorf("a log line carries a path or a name: %s", logs)
	}
}
