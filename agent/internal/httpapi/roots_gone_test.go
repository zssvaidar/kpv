// A stored root that is gone at the time it is asked about (#67), over the
// API: GET lists it unavailable, browsing shows what is at its path now, POST
// authorizes that again in its place, and DELETE removes it.

package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

func reasonOf(root ProjectRoot) RootUnavailableReason {
	if root.Reason == nil {
		return ""
	}
	return *root.Reason
}

func TestARootThatIsGoneOverTheAPI(t *testing.T) {
	t.Parallel()
	tree := newRootsTree(t)
	app := tree.in("code", "app")
	lib := tree.in("code", "lib")
	authorizedFrom(t, tree.authorize(t, app))
	authorizedFrom(t, tree.authorize(t, lib))
	before, err := os.ReadFile(tree.store)
	if err != nil {
		t.Fatalf("read the store: %v", err)
	}

	// Removed and made again, then browsed before anything lists the roots.
	if err := os.RemoveAll(app); err != nil {
		t.Fatalf("remove: %v", err)
	}
	mustMkdir(t, app)
	mustWrite(t, filepath.Join(app, "new"))
	listing := listingOf(t, tree.server.do(t, tree.server.allowed("/api/directories?path="+app)))
	if listing.Path != app || len(listing.Entries) != 1 || listing.Entries[0].Name != "new" {
		t.Fatalf("browsing the recreated directory gave %+v", listing)
	}
	if err := tree.guard.WriteFile(filepath.Join(app, "x"), []byte("x"), 0o644); !projectfs.IsForbidden(err) {
		t.Fatalf("a write into the recreated directory = %v", err)
	}

	list := tree.list(t)
	if got := paths(list); !slices.Equal(got, []string{app, lib}) {
		t.Fatalf("listed %v", got)
	}
	if list.Roots[0].Available || reasonOf(list.Roots[0]) != RootUnavailableReasonChanged || !list.Roots[1].Available {
		t.Fatalf("listed %+v", list.Roots)
	}
	if after, _ := os.ReadFile(tree.store); string(after) != string(before) {
		t.Fatalf("the store changed: %s", after)
	}

	// POST authorizes the directory there now, in its old place.
	if got := authorizedFrom(t, tree.authorize(t, app)); got != app {
		t.Fatalf("authorized %s", got)
	}
	list = tree.list(t)
	if got := paths(list); !slices.Equal(got, []string{app, lib}) || !list.Roots[0].Available {
		t.Fatalf("after authorizing again, listed %+v", list.Roots)
	}
	if after, _ := os.ReadFile(tree.store); string(after) != string(before) {
		t.Fatalf("the store changed: %s", after)
	}

	// A root still listed available that is gone: POST checks first.
	if err := os.RemoveAll(lib); err != nil {
		t.Fatalf("remove: %v", err)
	}
	rootsFailureOf(t, tree.authorize(t, lib), RootsErrorKindNotFound)
	list = tree.list(t)
	if list.Roots[1].Available || reasonOf(list.Roots[1]) != RootUnavailableReasonNotFound {
		t.Fatalf("listed %+v", list.Roots)
	}

	// DELETE removes it by its listed path, with nothing there.
	remaining := rootListFrom(t, tree.revoke(t, lib))
	if got := paths(remaining); !slices.Equal(got, []string{app}) {
		t.Fatalf("the revoke answered %v", got)
	}
	if answer := tree.server.do(t, tree.server.allowed("/api/roots")); answer.Status != http.StatusOK {
		t.Fatalf("list after the revoke: %s", answer)
	}
}
