package projectfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxSymlinkHops bounds how many symbolic links one path may be built out of,
// so a link that points at itself ends in a refusal rather than a loop. Linux
// uses 40 for the same job.
const maxSymlinkHops = 40

var (
	// errTooManyLinks is a path with more symbolic links on it than will be
	// followed, which is how a link cycle shows up.
	errTooManyLinks = errors.New("too many symbolic links")
	// errNotDirectory is a path that tries to descend through something that
	// is not a directory.
	errNotDirectory = errors.New("not a directory")
)

// absolute makes path absolute without cleaning it.
//
// filepath.Abs would be the obvious call and it is the wrong one: it cleans,
// and cleaning resolves ".." lexically. "root/link/../x" cleans to "root/x",
// which is a different file from the one the kernel would open when link
// points somewhere else. Every ".." on a path has to be applied to the parent
// that link resolution actually arrived at, so the segments are kept intact
// here and resolved one at a time in realPath.
func absolute(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	working, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return working + string(filepath.Separator) + path, nil
}

// splitSegments splits a path into its raw components, keeping nothing but the
// text between separators.
func splitSegments(path string) []string {
	return strings.Split(path, string(filepath.Separator))
}

// realPath walks raw one segment at a time from the filesystem root and
// returns the real path it arrives at: every symbolic link followed, and every
// ".." applied to the parent that link resolution had actually reached rather
// than to the text of the path. This is the "resolution happens on the real
// path, not on the string" half of the guard, and it is the same job
// filepath.EvalSymlinks does, with one thing EvalSymlinks cannot do: a path
// whose tail does not exist yet, which is every file that is about to be
// created.
//
// The returned missing tail is the segments the filesystem had nothing for,
// starting at the first one that was not there. They are returned rather than
// reported as an error because a path only has to exist for a read, and
// because deciding whether a path is allowed has to come before saying
// anything about what exists.
//
// reached is filled in as far as the walk got even when err is not nil, so the
// caller can still ask whether that much is inside an authorized root before
// it decides what the failure was.
func realPath(raw string) (reached string, missing []string, err error) {
	parts := splitSegments(raw)
	current := string(filepath.Separator)
	currentIsDirectory := true
	hops := 0

	for index := 0; index < len(parts); index++ {
		part := parts[index]
		if part == "" || part == "." {
			continue
		}
		if !currentIsDirectory {
			// Descending through a file. The kernel answers ENOTDIR and so
			// does this walk, rather than quietly stepping over it.
			return current, nil, errNotDirectory
		}
		if part == ".." {
			// current is a real path, so its parent is the real parent. This
			// is the step a lexical clean gets wrong.
			current = filepath.Dir(current)
			continue
		}

		next := filepath.Join(current, part)
		info, statErr := os.Lstat(next)
		if statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				tail := make([]string, 0, len(parts)-index)
				tail = append(tail, part)
				tail = append(tail, parts[index+1:]...)
				return current, tail, nil
			}
			return current, nil, statErr
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			current = next
			currentIsDirectory = info.IsDir()
			continue
		}

		hops++
		if hops > maxSymlinkHops {
			return current, nil, errTooManyLinks
		}
		target, linkErr := os.Readlink(next)
		if linkErr != nil {
			return current, nil, linkErr
		}
		// Splice the link's target in front of whatever is left of the path,
		// and start again. A relative target is relative to the directory the
		// link is in, which is current; an absolute one starts over at the
		// filesystem root.
		rest := parts[index+1:]
		targetParts := splitSegments(target)
		spliced := make([]string, 0, len(targetParts)+len(rest))
		spliced = append(spliced, targetParts...)
		spliced = append(spliced, rest...)
		parts = spliced
		index = -1
		if filepath.IsAbs(target) {
			current = string(filepath.Separator)
			currentIsDirectory = true
		}
	}
	return current, missing, nil
}

// reduceTail applies "." and ".." to the segments realPath found nothing for.
//
// Reducing them lexically is exact rather than a shortcut: a component that
// does not exist cannot be a symbolic link, so there is no link for the
// filesystem to disagree about. climbed says whether any ".." was there at
// all, because a path that climbs back out through a component that does not
// exist is one the kernel would answer ENOENT for, and this package refuses it
// rather than quietly meaning a different file.
func reduceTail(base string, missing []string) (string, []string, bool) {
	out := make([]string, 0, len(missing))
	climbed := false
	for _, part := range missing {
		switch part {
		case "", ".":
			continue
		case "..":
			climbed = true
			if len(out) > 0 {
				out = out[:len(out)-1]
			} else {
				base = filepath.Dir(base)
			}
		default:
			out = append(out, part)
		}
	}
	return base, out, climbed
}

// segments splits an absolute path into its components.
func segments(absolutePath string) []string {
	cleaned := filepath.Clean(absolutePath)
	trimmed := strings.TrimPrefix(cleaned, string(filepath.Separator))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, string(filepath.Separator))
}

// within reports whether candidate is root itself or a path beneath it, and
// returns the path from root to candidate when it is. Both paths must already
// be real, absolute paths.
//
// The comparison is segment by segment, and each segment is compared byte for
// byte. Both halves of that matter.
//
// Segments, because a string prefix is not containment: "/home/user/projectX"
// starts with "/home/user/project" and is a different directory, which is a
// bypass that reads as correct.
//
// Byte for byte, because folding case here is a bypass on a case-sensitive
// filesystem: "/home/user/Project" would then be accepted as the authorized
// "/home/user/project" while being a directory an attacker can create and
// fill. The cost is on a case-insensitive filesystem, where a differently
// cased spelling of an authorized root names the same directory and is still
// refused. That is the fail-closed direction of the trade and it is what "a
// case-collision attempt is rejected on case-insensitive filesystems" asks
// for: a path is inside a root when it is spelled the way the root was
// authorized, and not when it merely collides with it.
func within(root, candidate string) (string, bool) {
	rootParts := segments(root)
	candidateParts := segments(candidate)
	if len(candidateParts) < len(rootParts) {
		return "", false
	}
	for index, part := range rootParts {
		if candidateParts[index] != part {
			return "", false
		}
	}
	if len(candidateParts) == len(rootParts) {
		return ".", true
	}
	return filepath.Join(candidateParts[len(rootParts):]...), true
}
