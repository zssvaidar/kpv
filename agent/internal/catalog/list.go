package catalog

import (
	"errors"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// scan is what one root's scan found.
type scan struct {
	// failed is why the root could not be scanned, "" when it was.
	failed       projectfs.ErrorKind
	truncated    bool
	skippedLinks int
	entries      []Entry
}

// List is every stored root with the pipelines in it, read from disk now.
//
// The roots are checked through Roots.List before the scan and again after
// it, and the answer is the second check's roots, in its order. A root that
// is unavailable in either is reported with its reason and no pipelines, and
// one whose scan failed part-way (it went away, became unreadable, or was
// revoked) is reported with the reason the scan met, never as a partial
// list. A root authorized while the list ran is scanned too.
//
// List never creates, changes or removes anything on disk.
func (c *Catalog) List() List {
	before := c.roots.List()
	scanned := make(map[string]scan, len(before))
	for _, status := range before {
		if status.Available {
			scanned[status.Path] = c.scanRoot(status.Path)
		} else {
			scanned[status.Path] = scan{failed: rootReason(status.Reason)}
		}
	}
	after := c.roots.List()
	out := List{Roots: make([]Root, 0, len(after))}
	for _, status := range after {
		root := Root{Path: status.Path, Pipelines: []Entry{}}
		if !status.Available {
			root.Reason = rootReason(status.Reason)
			out.Roots = append(out.Roots, root)
			continue
		}
		result, seen := scanned[status.Path]
		if !seen {
			result = c.scanRoot(status.Path)
		}
		if result.failed != "" {
			root.Reason = result.failed
			out.Roots = append(out.Roots, root)
			continue
		}
		root.Available = true
		root.Truncated = result.truncated
		root.SkippedLinks = result.skippedLinks
		root.Pipelines = result.entries
		out.Roots = append(out.Roots, root)
	}
	markDuplicates(&out)
	c.logList(out)
	return out
}

func (c *Catalog) logList(list List) {
	var pipelines, unreadableCount, unavailable, truncated, links int
	for _, root := range list.Roots {
		if !root.Available {
			unavailable++
		}
		if root.Truncated {
			truncated++
		}
		links += root.SkippedLinks
		for _, entry := range root.Pipelines {
			pipelines++
			if !entry.Readable() {
				unreadableCount++
			}
		}
	}
	c.logger.Debug("the pipelines were listed", "roots", len(list.Roots), "unavailable", unavailable,
		"truncated", truncated, "pipelines", pipelines, "unreadable", unreadableCount, "skippedLinks", links)
}

// pinRoot resolves the stored root path through the guard and pins it. A root
// that no longer resolves to exactly that real path is KindChanged.
func (c *Catalog) pinRoot(path string) (*projectfs.PinnedDir, projectfs.ErrorKind) {
	resolved, err := c.guard.Resolve(path)
	if err != nil {
		return nil, rootReason(projectfs.KindOf(err))
	}
	if resolved.Real() != path || !resolved.IsRoot() {
		return nil, projectfs.KindChanged
	}
	pin, err := resolved.Pin()
	if err != nil {
		return nil, rootReason(projectfs.KindOf(err))
	}
	return pin, ""
}

// scanRoot lists the pipelines in one available root.
func (c *Catalog) scanRoot(path string) scan {
	pin, failed := c.pinRoot(path)
	if failed != "" {
		return scan{failed: failed}
	}
	defer func() { _ = pin.Close() }()
	dir := pin.Path()
	if c.hook.scanning != nil {
		c.hook.scanning(path)
	}

	// A root that holds pipeline.yaml is that one pipeline.
	file, problem, modified, err := readIn(dir)
	switch {
	case err == nil:
		return scan{entries: []Entry{entryOf(Location{Root: path}, dir.Real(), file, problem, modified)}}
	case err != errNoPipeline:
		return scan{failed: rootReason(projectfs.KindOf(err))}
	}

	names, truncated, links, failed := readNames(dir)
	if failed != "" {
		return scan{failed: failed}
	}
	result := scan{truncated: truncated, skippedLinks: links, entries: []Entry{}}
	for _, name := range names {
		entry, listed, skippedLink, failed := c.scanDirectory(dir, path, name)
		if failed != "" {
			return scan{failed: failed}
		}
		if skippedLink {
			result.skippedLinks++
		}
		if listed {
			result.entries = append(result.entries, entry)
		}
	}
	return result
}

// readNames reads at most projectfs.MaxListing entries of the pinned root and
// returns, sorted by name, those that are directories whose name does not
// start with ".", whether there were more entries than that, and how many of
// the examined entries are symbolic links.
func readNames(dir projectfs.Path) ([]string, bool, int, projectfs.ErrorKind) {
	opened, err := dir.Open()
	if err != nil {
		return nil, false, 0, rootReason(projectfs.KindOf(err))
	}
	defer func() { _ = opened.Close() }()
	entries, err := opened.ReadDir(projectfs.MaxListing + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		if errors.Is(err, fs.ErrPermission) {
			return nil, false, 0, projectfs.KindUnreadable
		}
		return nil, false, 0, projectfs.KindIO
	}
	truncated := len(entries) > projectfs.MaxListing
	if truncated {
		entries = entries[:projectfs.MaxListing]
	}
	names := make([]string, 0, len(entries))
	links := 0
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasPrefix(name, "."):
		case entry.Type()&fs.ModeSymlink != 0:
			links++
		case entry.IsDir():
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, truncated, links, ""
}

// scanDirectory looks at one directory directly inside a root. It answers the
// entry and whether it is listed, whether it turned out to be a symbolic link
// (one swapped in since it was read), or that the root itself stopped
// working.
func (c *Catalog) scanDirectory(dir projectfs.Path, root, name string) (Entry, bool, bool, projectfs.ErrorKind) {
	location := Location{Root: root, Directory: name}
	sub, err := dir.Join(name)
	if err != nil {
		return Entry{}, false, false, ""
	}
	if c.hook.beforePin != nil {
		c.hook.beforePin(name)
	}
	pin, err := sub.Pin()
	if err != nil {
		switch projectfs.KindOf(err) {
		case projectfs.KindChanged:
			// A link swapped in since the directory was read, or a directory
			// that cannot be searched, which Pin cannot tell from a swap.
			if info, lstatErr := sub.Lstat(); lstatErr == nil && info.IsDir() {
				return entryOf(location, sub.Real(), nil, unreadable(UnreadableUnreadable), time.Time{}), true, false, ""
			}
			return Entry{}, false, true, ""
		case projectfs.KindNotFound, projectfs.KindNotDirectory:
			return Entry{}, false, false, ""
		case projectfs.KindForbidden:
			return Entry{}, false, false, projectfs.KindForbidden
		}
		// It cannot be opened, so it may hold a pipeline.
		return entryOf(location, sub.Real(), nil, unreadable(UnreadableUnreadable), time.Time{}), true, false, ""
	}
	defer func() { _ = pin.Close() }()
	file, problem, modified, err := readIn(pin.Path())
	switch {
	case err == errNoPipeline:
		return Entry{}, false, false, ""
	case err != nil:
		return Entry{}, false, false, rootReason(projectfs.KindOf(err))
	}
	return entryOf(location, pin.Path().Real(), file, problem, modified), true, false, ""
}

// entryOf builds a list entry from what readIn answered.
func entryOf(location Location, dir string, file *loaded, problem *Unreadable, modified time.Time) Entry {
	entry := Entry{Location: location, Dir: dir, Modified: modified, Duplicates: []Location{}}
	if file == nil {
		entry.Unreadable = problem
		return entry
	}
	entry.ID = string(file.model.ID)
	if file.model.Name != nil {
		entry.Name = *file.model.Name
	}
	entry.Language = string(file.model.Language)
	entry.Pipeline = file.model
	entry.Steps = file.steps
	return entry
}

// markDuplicates gives every readable entry the locations of the others with
// its id, compared ignoring letter case.
func markDuplicates(list *List) {
	byID := map[string][]Location{}
	for _, root := range list.Roots {
		for _, entry := range root.Pipelines {
			if entry.Readable() {
				key := strings.ToLower(entry.ID)
				byID[key] = append(byID[key], entry.Location)
			}
		}
	}
	for r := range list.Roots {
		for e := range list.Roots[r].Pipelines {
			entry := &list.Roots[r].Pipelines[e]
			if !entry.Readable() {
				continue
			}
			for _, other := range byID[strings.ToLower(entry.ID)] {
				if other != entry.Location {
					entry.Duplicates = append(entry.Duplicates, other)
				}
			}
		}
	}
}
