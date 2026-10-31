package catalog

import (
	"strings"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// Open reads the pipeline at location now, as the list would find it, and
// answers it with the SHA-256 of the exact bytes read.
//
// Only a location the list could show is opened: a directory name that is
// not a single name is KindInvalid; a root the roots store does not hold is
// KindNotAuthorized and one that is unavailable KindRootUnavailable; a
// directory that is gone, is a link, starts with ".", has no pipeline.yaml,
// or sits in a root that is itself a pipeline is KindNotFound; a
// pipeline.yaml that does not load is KindUnreadable with why. The root is
// checked again after the read, so a root that went away meanwhile answers
// that rather than what was read through it.
func (c *Catalog) Open(location Location) (*Document, error) {
	if location.Root == "" || (location.Directory != "" && !validDirectory(location.Directory)) {
		return nil, &Error{Kind: KindInvalid}
	}
	if fault := c.rootStatus(location.Root); fault != nil {
		return nil, fault
	}
	rootPin, failed := c.pinRoot(location.Root)
	if failed != "" {
		return nil, c.rootFault(location.Root, failed)
	}
	defer func() { _ = rootPin.Close() }()

	dir := rootPin.Path()
	if location.Directory != "" {
		if strings.HasPrefix(location.Directory, ".") {
			return nil, &Error{Kind: KindNotFound}
		}
		// The list does not look inside a root that is itself a pipeline.
		if file, err := dir.Join(FileName); err == nil {
			if _, err := file.Lstat(); err == nil {
				return nil, &Error{Kind: KindNotFound}
			}
		}
		sub, err := dir.Join(location.Directory)
		if err != nil {
			return nil, &Error{Kind: KindInvalid}
		}
		pin, err := sub.Pin()
		if err != nil {
			switch projectfs.KindOf(err) {
			case projectfs.KindForbidden:
				return nil, c.rootFault(location.Root, projectfs.KindForbidden)
			case projectfs.KindChanged:
				// A link, or a directory that cannot be searched, which Pin
				// cannot tell from a swap. As the list does (scanDirectory),
				// the name is looked at again: still a directory is
				// unreadable, anything else is not a pipeline the list shows.
				if info, lstatErr := sub.Lstat(); lstatErr == nil && info.IsDir() {
					return nil, c.unreadableUnlessRootGone(location.Root)
				}
				return nil, c.notFoundUnlessRootGone(location.Root)
			}
			if fault := c.rootStatus(location.Root); fault != nil {
				return nil, fault
			}
			return nil, pinFailure(err)
		}
		defer func() { _ = pin.Close() }()
		dir = pin.Path()
	}

	file, problem, modified, err := readIn(dir)
	if fault := c.rootStatus(location.Root); fault != nil {
		return nil, fault
	}
	switch {
	case err == errNoPipeline:
		return nil, &Error{Kind: KindNotFound}
	case err != nil:
		return nil, c.rootFault(location.Root, projectfs.KindOf(err))
	case problem != nil:
		return nil, &Error{Kind: KindUnreadable, Unreadable: problem}
	}
	return &Document{
		Location: location, Dir: dir.Real(), Modified: modified,
		Pipeline: file.model, Steps: file.steps, JSON: file.json, SHA256: digest(file.data),
	}, nil
}

// rootStatus is nil when the roots store holds root and it is available, and
// the *Error saying otherwise when not.
func (c *Catalog) rootStatus(root string) *Error {
	for _, status := range c.roots.List() {
		if status.Path == root {
			if status.Available {
				return nil
			}
			return &Error{Kind: KindRootUnavailable, Reason: rootReason(status.Reason)}
		}
	}
	return &Error{Kind: KindNotAuthorized}
}

// rootFault is the error for a root that stopped working with kind: what the
// roots store now says about it when that is not available, or kind.
func (c *Catalog) rootFault(root string, kind projectfs.ErrorKind) *Error {
	if fault := c.rootStatus(root); fault != nil {
		return fault
	}
	return rootFailure(kind)
}

// unreadableUnlessRootGone is an unreadable pipeline directory, unless the
// root itself has gone meanwhile, which is then the answer.
func (c *Catalog) unreadableUnlessRootGone(root string) *Error {
	if fault := c.rootStatus(root); fault != nil {
		return fault
	}
	return &Error{Kind: KindUnreadable, Unreadable: unreadable(UnreadableUnreadable)}
}

// notFoundUnlessRootGone is no pipeline at that directory, unless the root
// itself has gone meanwhile, which is then the answer.
func (c *Catalog) notFoundUnlessRootGone(root string) *Error {
	if fault := c.rootStatus(root); fault != nil {
		return fault
	}
	return &Error{Kind: KindNotFound}
}
